package pwcli

import (
	"bytes"
	"compress/gzip"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/tinybind-go/configbind"
	"github.com/shibukawa/tinybind-go/minitoml"
	"go/build/constraint"
	"go/parser"
	"go/token"
)

// The Cloudflare Workers target, per requirement:cloudflare-workers-build-target.
//
// The adapter is github.com/syumai/workers, pinned here because the staged
// module is generated and nothing else names a version for it. The
// compatibility date is the one a build writes when the project names none;
// it is pinned rather than "today" so two builds of one project agree.
const (
	workersModule                  = "github.com/syumai/workers"
	workersModuleVersion           = "v0.33.0"
	defaultCompatibilityDate       = "2025-08-01"
	cloudflareBuildTag             = "pwcloudflare"
	cloudflareWorkerEntry          = "build/worker.mjs"
	cloudflareWasmArtifact         = "build/app.wasm"
	cloudflareLoader               = "build/wasm_exec.js"
	cloudflareCompatibilityDateFmt = "2006-01-02"
)

// cloudflareAssets are the framework-owned loader files, per
// decision:owned-wasm-loader: one wasm_exec.js per compiler, each the pinned
// compiler's own file with the Worker context proxy added, and the fetch entry.
//
//go:embed cloudflare_assets/*
var cloudflareAssets embed.FS

// cloudflareConfig is the deploy.cloudflare table of data:project-config.
type cloudflareConfig struct {
	// Compiler is tinygo or go, defaulting to project.toolchain.
	Compiler string
	// Name is the Worker name written into wrangler.jsonc, defaulting to
	// project.name.
	Name string
	// CompatibilityDate is the Wrangler compatibility date, a YYYY-MM-DD
	// string, defaulting to the date this release pins.
	CompatibilityDate string
	// D1 names the databases behind the d1:// bindings config.prod.toml
	// uses, per requirement:cloudflare-d1-engine. A binding the table does
	// not name still reaches wrangler.jsonc, under the project name and with
	// no id, because the id is a deployment fact the operator fills in.
	D1 []cloudflareD1Config
	// R2 names the bucket the external public tree is served from, per
	// requirement:cloudflare-r2-storage. Unset, the tree is reported as not
	// shipped and only the embedded tree is served.
	R2 cloudflareR2Config
	// KV names the namespaces behind the KV bindings config.prod.toml uses,
	// per requirement:cloudflare-kv-backends, the way D1 does for databases.
	KV []cloudflareKVConfig
}

// cloudflareKVConfig is one [[deploy.cloudflare.kv]] element.
type cloudflareKVConfig struct {
	// Binding is the name the configuration and the Worker env use.
	Binding string
	// ID is the namespace id, which deployment needs and local development
	// does not.
	ID string
}

// cloudflareR2Config is the deploy.cloudflare.r2 table.
type cloudflareR2Config struct {
	// Binding is the bucket binding's name in the Worker env.
	Binding string
	// BucketName is the R2 bucket's name, which wrangler commands take; it
	// defaults to the binding in lower case.
	BucketName string
}

// Configured reports whether a bucket was named at all.
func (config cloudflareR2Config) Configured() bool { return config.Binding != "" }

// cloudflareD1Config is one [[deploy.cloudflare.d1]] element.
type cloudflareD1Config struct {
	// Binding is the name the DSN and the Worker env use.
	Binding string
	// DatabaseName is the D1 database's name, which wrangler d1 commands
	// take; it defaults to the binding in lower case.
	DatabaseName string
	// DatabaseID is the D1 database's id, which deployment needs and local
	// development does not.
	DatabaseID string
}

// cloudflareSettings reads the deploy.cloudflare table. config carries the
// project values the defaults are drawn from, so it is read after them.
func cloudflareSettings(document minitoml.Document, config projectConfig) (cloudflareConfig, error) {
	settings := cloudflareConfig{Compiler: config.Toolchain, Name: config.Name, CompatibilityDate: defaultCompatibilityDate}
	compiler, err := optionalScalar(document, "deploy.cloudflare.compiler")
	if err != nil {
		return cloudflareConfig{}, err
	}
	if compiler != "" {
		if compiler != toolchainTinyGo && compiler != toolchainGo {
			return cloudflareConfig{}, fmt.Errorf("popcornweb.toml: deploy.cloudflare.compiler must be %q or %q", toolchainTinyGo, toolchainGo)
		}
		// A project scaffolded for host Go routes through the standard
		// ServeMux, whose method patterns TinyGo's net/http does not match:
		// the Worker would build and then answer 404 to every registered
		// route. The other direction is fine, because the TinyGo-compatible
		// mux works under both compilers.
		if compiler == toolchainTinyGo && config.Toolchain == toolchainGo {
			return cloudflareConfig{}, fmt.Errorf("popcornweb.toml: deploy.cloudflare.compiler = %q needs project.toolchain = %q; a project scaffolded for %q routes through the standard ServeMux, whose method patterns TinyGo does not match", toolchainTinyGo, toolchainTinyGo, toolchainGo)
		}
		settings.Compiler = compiler
	}
	name, err := optionalScalar(document, "deploy.cloudflare.name")
	if err != nil {
		return cloudflareConfig{}, err
	}
	if name != "" {
		settings.Name = name
	}
	date, err := optionalScalar(document, "deploy.cloudflare.compatibility_date")
	if err != nil {
		return cloudflareConfig{}, err
	}
	if date != "" {
		// Wrangler rejects a malformed date at deploy time, which is later
		// than the build that wrote it; this is the earlier place.
		if _, err := time.Parse(cloudflareCompatibilityDateFmt, date); err != nil {
			return cloudflareConfig{}, fmt.Errorf("popcornweb.toml: deploy.cloudflare.compatibility_date must be a YYYY-MM-DD date: %w", err)
		}
		settings.CompatibilityDate = date
	}
	if settings.R2.Binding, err = optionalScalar(document, "deploy.cloudflare.r2.binding"); err != nil {
		return cloudflareConfig{}, err
	}
	if settings.R2.BucketName, err = optionalScalar(document, "deploy.cloudflare.r2.bucket_name"); err != nil {
		return cloudflareConfig{}, err
	}
	if settings.R2.BucketName != "" && settings.R2.Binding == "" {
		return cloudflareConfig{}, errors.New("popcornweb.toml: deploy.cloudflare.r2.bucket_name needs deploy.cloudflare.r2.binding, the name the Worker reads the bucket by")
	}
	if settings.R2.Configured() && settings.R2.BucketName == "" {
		settings.R2.BucketName = strings.ToLower(settings.R2.Binding)
	}
	if value, ok := document.Get("deploy.cloudflare.kv"); ok {
		tables, err := value.AsTables()
		if err != nil {
			return cloudflareConfig{}, fmt.Errorf("popcornweb.toml: deploy.cloudflare.kv: %w", err)
		}
		seen := map[string]bool{}
		for index, table := range tables {
			for _, key := range table.Keys() {
				if key != "binding" && key != "id" {
					return cloudflareConfig{}, fmt.Errorf("popcornweb.toml: unknown key deploy.cloudflare.kv[%d].%s", index, key)
				}
			}
			entry := cloudflareKVConfig{}
			if entry.Binding, err = scalar(table, "binding"); err != nil {
				return cloudflareConfig{}, fmt.Errorf("popcornweb.toml: deploy.cloudflare.kv[%d]: %w", index, err)
			}
			if seen[entry.Binding] {
				return cloudflareConfig{}, fmt.Errorf("popcornweb.toml: deploy.cloudflare.kv names binding %q twice", entry.Binding)
			}
			seen[entry.Binding] = true
			if entry.ID, err = optionalScalar(table, "id"); err != nil {
				return cloudflareConfig{}, err
			}
			settings.KV = append(settings.KV, entry)
		}
	}
	if value, ok := document.Get("deploy.cloudflare.d1"); ok {
		tables, err := value.AsTables()
		if err != nil {
			return cloudflareConfig{}, fmt.Errorf("popcornweb.toml: deploy.cloudflare.d1: %w", err)
		}
		seen := map[string]bool{}
		for index, table := range tables {
			for _, key := range table.Keys() {
				if key != "binding" && key != "database_name" && key != "database_id" {
					return cloudflareConfig{}, fmt.Errorf("popcornweb.toml: unknown key deploy.cloudflare.d1[%d].%s", index, key)
				}
			}
			entry := cloudflareD1Config{}
			if entry.Binding, err = scalar(table, "binding"); err != nil {
				return cloudflareConfig{}, fmt.Errorf("popcornweb.toml: deploy.cloudflare.d1[%d]: %w", index, err)
			}
			if seen[entry.Binding] {
				return cloudflareConfig{}, fmt.Errorf("popcornweb.toml: deploy.cloudflare.d1 names binding %q twice", entry.Binding)
			}
			seen[entry.Binding] = true
			if entry.DatabaseName, err = optionalScalar(table, "database_name"); err != nil {
				return cloudflareConfig{}, err
			}
			if entry.DatabaseID, err = optionalScalar(table, "database_id"); err != nil {
				return cloudflareConfig{}, err
			}
			settings.D1 = append(settings.D1, entry)
		}
	}
	return settings, nil
}

// buildCloudflareDeployment stages and compiles the Worker.
//
// The stage is the source deployment shape of requirement:serverless-source-entrypoints
// — the application as a nested module beside a generated main package — with
// the compile run here rather than by the provider, because a Worker is
// uploaded as a module rather than built remotely. The compiler is the one
// deploy.cloudflare names, which makes this the one target api:cli-build
// compiles with tinygo, per decision:explicit-tinygo-compile-step: there is no
// Dockerfile for the operator to own that line in.
func buildCloudflareDeployment(ctx context.Context, root, stage string, config projectConfig, options buildOptions, progress *progressRegion, stdout, stderr io.Writer) (deploymentManifest, error) {
	progress.Phase("staging worker source")
	applicationRoot := filepath.Join(stage, "app")
	if err := copyProjectForFunction(root, applicationRoot); err != nil {
		return deploymentManifest{}, err
	}
	excluded, err := excludeNetdevRegistration(applicationRoot)
	if err != nil {
		return deploymentManifest{}, err
	}
	for _, name := range excluded {
		fmt.Fprintf(stdout, "cloudflare: %s registers the host network driver, which a Worker has no socket for; it is left out of this build\n", name)
	}
	modulePath, goVersion, err := normalizeApplicationModule(root, filepath.Join(applicationRoot, "go.mod"))
	if err != nil {
		return deploymentManifest{}, err
	}
	requires := []moduleRequirement{{path: workersModule, version: workersModuleVersion}}
	if err := writeFunctionModule(stage, modulePath, goVersion, filepath.Join(applicationRoot, "go.mod"), requires); err != nil {
		return deploymentManifest{}, err
	}
	if info, err := os.Stat(filepath.Join(applicationRoot, "go.sum")); err == nil {
		if err := copyDeploymentFile(filepath.Join(applicationRoot, "go.sum"), filepath.Join(stage, "go.sum"), info.Mode()); err != nil {
			return deploymentManifest{}, err
		}
	}
	if err := copyTransformedMain(root, stage, "main", config.Main, options.backend); err != nil {
		return deploymentManifest{}, err
	}
	rewritten, err := rewriteEngineImports(stage, applicationRoot)
	if err != nil {
		return deploymentManifest{}, err
	}
	for _, name := range rewritten {
		fmt.Fprintf(stdout, "cloudflare: %s links a SQL engine a Worker cannot reach; the staged copy links %s instead\n", name, d1DriverPackage)
	}
	wrapper, err := format.Source([]byte(cloudflareWrapper(config.Cloudflare)))
	if err != nil {
		return deploymentManifest{}, fmt.Errorf("format generated worker entrypoint: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stage, "popcornweb_serverless.go"), wrapper, 0o644); err != nil {
		return deploymentManifest{}, err
	}
	tidy := exec.CommandContext(ctx, "go", "mod", "tidy")
	tidy.Dir, tidy.Stdout, tidy.Stderr, tidy.Env = stage, stdout, stderr, os.Environ()
	if err := tidy.Run(); err != nil {
		return deploymentManifest{}, fmt.Errorf("resolve staged worker module: %w", err)
	}

	// The production configuration is read before the compiler runs, so a
	// configuration the host cannot hold is refused in a second rather than
	// after a minute, per requirement:cloudflare-process-state-refusal.
	vars := map[string]string{}
	var bindings, kvBindings, r2Bindings []string
	if source, err := os.ReadFile(filepath.Join(root, "config.prod.toml")); err == nil {
		document, err := minitoml.Parse(source)
		if err != nil {
			return deploymentManifest{}, fmt.Errorf("config.prod.toml: %w", err)
		}
		if refusals := cloudflareProcessStateRefusals(document); len(refusals) > 0 {
			return deploymentManifest{}, fmt.Errorf("config.prod.toml keeps state the Cloudflare Workers host cannot hold:\n  %s", strings.Join(refusals, "\n  "))
		}
		bindings = cloudflareD1Bindings(document)
		kvBindings = cloudflareKVBindings(document)
		r2Bindings = storageR2Bindings(document)
		var tableArrays []string
		vars, tableArrays = cloudflareVarsOf(document)
		for _, key := range tableArrays {
			// An array of tables has no environment form, so it cannot travel
			// as a var. It is named rather than dropped in silence, because a
			// section a capability needs and does not get is a server that
			// starts with the capability off.
			fmt.Fprintf(stdout, "cloudflare: config.prod.toml %s is an array of tables, which has no environment form; it is not carried into wrangler.jsonc\n", key)
		}
	} else if !os.IsNotExist(err) {
		return deploymentManifest{}, err
	} else {
		fmt.Fprintln(stdout, "cloudflare: no config.prod.toml; the Worker runs on defaults and wrangler vars")
	}

	settings := config.Cloudflare
	progress.Phase("compiling wasm with " + settings.Compiler)
	if err := os.MkdirAll(filepath.Join(stage, "build"), 0o755); err != nil {
		return deploymentManifest{}, err
	}
	compile, err := cloudflareCompileCommand(ctx, stage, settings.Compiler, options.debug)
	if err != nil {
		return deploymentManifest{}, err
	}
	compile.Stdout, compile.Stderr = stdout, stderr
	if err := compile.Run(); err != nil {
		return deploymentManifest{}, fmt.Errorf("%s build worker: %w", settings.Compiler, err)
	}

	progress.Phase("writing worker files")
	loader := "cloudflare_assets/wasm_exec_go.js"
	if settings.Compiler == toolchainTinyGo {
		loader = "cloudflare_assets/wasm_exec_tinygo.js"
	}
	for asset, destination := range map[string]string{loader: cloudflareLoader, "cloudflare_assets/worker.mjs": cloudflareWorkerEntry} {
		content, err := fs.ReadFile(cloudflareAssets, asset)
		if err != nil {
			return deploymentManifest{}, err
		}
		if err := os.WriteFile(filepath.Join(stage, filepath.FromSlash(destination)), content, 0o644); err != nil {
			return deploymentManifest{}, err
		}
	}
	if settings.R2.Configured() {
		staged, err := writeR2UploadScript(root, filepath.Join(stage, "r2-upload.sh"), settings.R2.BucketName)
		if err != nil {
			return deploymentManifest{}, err
		}
		fmt.Fprintf(stdout, "cloudflare: %d files of %s are served from bucket %s; upload them with sh r2-upload.sh --local or --remote\n", staged, externalPublicDir, settings.R2.BucketName)
	} else if skipped := countExternalAssets(root); skipped > 0 {
		fmt.Fprintf(stdout, "cloudflare: %s holds %d files a Worker cannot read; name a bucket under [deploy.cloudflare.r2] to serve them, or only the embedded public tree is served\n", externalPublicDir, skipped)
	}
	databases := cloudflareD1Databases(settings, bindings)
	if len(databases) > 0 {
		staged, err := stageD1Migrations(filepath.Join(root, config.Migration.Dir), filepath.Join(stage, "migrations"))
		if err != nil {
			return deploymentManifest{}, err
		}
		for index := range databases {
			if databases[index].DatabaseID == "" {
				// Wrangler refuses an empty id even for local development,
				// so the name stands in; deploy against it fails naming the
				// database, which is the right failure.
				databases[index].DatabaseID = databases[index].DatabaseName
				fmt.Fprintf(stdout, "cloudflare: binding %s has no database_id, so wrangler.jsonc carries the name %q in its place; add [[deploy.cloudflare.d1]] to popcornweb.toml before wrangler deploy (wrangler dev runs on the placeholder)\n", databases[index].Binding, databases[index].DatabaseName)
			}
			if staged > 0 {
				databases[index].MigrationsDir = "migrations"
			}
		}
		if staged > 0 {
			fmt.Fprintf(stdout, "cloudflare: %d migrations staged for wrangler d1 migrations apply\n", staged)
		}
	}
	namespaces := cloudflareKVNamespaces(settings, kvBindings)
	for _, namespace := range namespaces {
		if namespace.placeholder {
			fmt.Fprintf(stdout, "cloudflare: KV binding %s has no id, so wrangler.jsonc carries a placeholder; add [[deploy.cloudflare.kv]] to popcornweb.toml before wrangler deploy (wrangler dev runs on the placeholder)\n", namespace.Binding)
		}
	}
	wrangler, err := cloudflareWranglerConfig(settings, vars, databases, cloudflareR2Buckets(settings, r2Bindings), namespaces)
	if err != nil {
		return deploymentManifest{}, err
	}
	if err := os.WriteFile(filepath.Join(stage, "wrangler.jsonc"), wrangler, 0o644); err != nil {
		return deploymentManifest{}, err
	}
	raw, compressed, err := wasmSizes(filepath.Join(stage, filepath.FromSlash(cloudflareWasmArtifact)))
	if err != nil {
		return deploymentManifest{}, err
	}
	// The plan limit is the operator's to compare against, per
	// requirement:cloudflare-workers-hosting limits, so both numbers are
	// printed rather than one of them being judged here.
	fmt.Fprintf(stdout, "cloudflare: %s is %.1f MB raw, %.1f MB gzip\n", cloudflareWasmArtifact, float64(raw)/1e6, float64(compressed)/1e6)
	return deploymentManifest{
		Target:     options.target,
		Backend:    options.backend,
		Artifact:   cloudflareWasmArtifact,
		Entrypoint: cloudflareWorkerEntry,
		Compiler:   settings.Compiler,
	}, nil
}

// cloudflareCompileCommand is the one compiler invocation of the target,
// run in the stage with the wasm module as its output.
func cloudflareCompileCommand(ctx context.Context, stage, compiler string, debug bool) (*exec.Cmd, error) {
	output := filepath.FromSlash(cloudflareWasmArtifact)
	switch compiler {
	case toolchainTinyGo:
		arguments := []string{"build", "-o", output, "-target", "wasm", "-tags", cloudflareBuildTag}
		if !debug {
			arguments = append(arguments, "-no-debug")
		}
		command := exec.CommandContext(ctx, "tinygo", append(arguments, ".")...)
		command.Dir, command.Env = stage, os.Environ()
		return command, nil
	case toolchainGo:
		arguments := []string{"build", "-trimpath", "-tags", cloudflareBuildTag}
		if !debug {
			arguments = append(arguments, "-ldflags=-s -w")
		}
		command := exec.CommandContext(ctx, "go", append(arguments, "-o", output, ".")...)
		command.Dir = stage
		command.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
		return command, nil
	}
	return nil, fmt.Errorf("unsupported cloudflare compiler %q", compiler)
}

// cloudflareWrapper is the generated main package beside the transformed
// application main: it bridges the Worker environment into the configuration
// loader, initializes the application once per module instance, and hands
// the captured handler to the adapter.
func cloudflareWrapper(settings cloudflareConfig) string {
	// The R2 binding client registers the r2 storage backend of
	// requirement:object-storage, the one backend this host reaches, so it
	// is linked whether or not the external tree is served from a bucket.
	r2Import, r2Option := "\n\t_ \"github.com/shibukawa/popcornweb/cloudflare/r2\"", ""
	if settings.R2.Configured() {
		r2Import = "\n\t\"github.com/shibukawa/popcornweb/cloudflare/r2\""
		r2Option = "\n\t// The external public tree is read from the bucket the build uploaded it\n\t// to, because this host has no directory beside the process.\n\toptions = append(options, pw.WithExternalAssets(r2.ExternalAssets(" + strconv.Quote(settings.R2.Binding) + ")))"
	}
	return "// Code generated by pw build --target=" + targetCloudflareWorkers + ". DO NOT EDIT.\n\n" + `package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"syscall/js"

	// The D1 engine is linked here rather than by the application, because
	// a d1:// connection exists only on this host: the application keeps its
	// own engine import for development and names the binding in
	// config.prod.toml.
	_ "github.com/shibukawa/popcornweb/database/d1"
	// The KV rate limit counter, for the same reason: the namespace exists
	// only on this host, and ratelimit.backend = "cloudflarekv" selects it.
	_ "github.com/shibukawa/popcornweb/ratelimitstore/cloudflarekv"` + r2Import + `
	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/tinybind-go/configbind"
	"github.com/syumai/workers"
)

var applicationHandler http.Handler

// captureApplication stands where pw.Run stood in the application main. The
// Worker owns invocation, so there is no listener: the middleware chain is
// built and kept for the adapter.
func captureApplication(_ func(context.Context, http.Handler, ...pw.Option) error, ctx context.Context, handler http.Handler, options ...pw.Option) error {` + r2Option + `
	wrapped, err := pw.Middlewares(handler, options...)
	if err == nil {
		applicationHandler = wrapped
	}
	return err
}

// workerEnviron is the module's environment with every string value of the
// Worker env laid over it. wrangler vars and secrets are strings; a binding
// such as a KV namespace is an object and is left to the adapter's own API.
// The Worker env is read through the context global the loader provides,
// which is the same route on both compilers.
func workerEnviron() []string {
	environ := os.Environ()
	seen := map[string]bool{}
	for _, line := range environ {
		name, _, _ := strings.Cut(line, "=")
		seen[name] = true
	}
	runtimeContext := js.Global().Get("context")
	if runtimeContext.Type() != js.TypeObject {
		return environ
	}
	env := runtimeContext.Get("env")
	if env.Type() != js.TypeObject {
		return environ
	}
	keys := js.Global().Get("Object").Call("keys", env)
	for index := 0; index < keys.Length(); index++ {
		name := keys.Index(index).String()
		if seen[name] {
			continue
		}
		value := env.Get(name)
		if value.Type() != js.TypeString {
			continue
		}
		environ = append(environ, name+"="+value.String())
	}
	return environ
}

func main() {
	environ := workerEnviron()
	declared := false
	for _, line := range environ {
		if strings.HasPrefix(line, "APP_ENV=") {
			declared = true
			break
		}
	}
	if !declared {
		environ = append(environ, "APP_ENV=prod")
	}
	pwconfig.SetLoadOptions(configbind.LoadOptions{Environ: environ})
	initializeApplication()
	handler := applicationHandler
	if handler == nil {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		})
	}
	workers.Serve(handler)
}
`
}

// cloudflareVars flattens config.prod.toml into wrangler vars under the
// environment names configbind reads, so the file the project maintains is
// still what configures the deployment even though the Worker cannot read it.
// An array of tables has no environment form and is returned by key.
func cloudflareVars(source []byte) (map[string]string, []string, error) {
	document, err := minitoml.Parse(source)
	if err != nil {
		return nil, nil, err
	}
	vars, tableArrays := cloudflareVarsOf(document)
	return vars, tableArrays, nil
}

// cloudflareVarsOf is cloudflareVars over a parsed document.
func cloudflareVarsOf(document minitoml.Document) (map[string]string, []string) {
	vars := map[string]string{}
	var tableArrays []string
	for _, key := range document.Keys() {
		value, _ := document.Get(key)
		switch value.Kind {
		case minitoml.KindTableArray:
			// The connection array is the one array of tables the runtime
			// reads as JSON in one variable, per pwconfig.ConnectionsEnv; it
			// travels, and every other array is reported.
			if key == "middleware.rdb.connections" {
				if encoded, ok := encodeConnectionsTable(value.Tables); ok {
					vars[pwconfig.ConnectionsEnv] = encoded
					continue
				}
			}
			if key == "storage.buckets" {
				if encoded, ok := encodeBucketsTable(value.Tables); ok {
					vars[pwconfig.BucketsEnv] = encoded
					continue
				}
			}
			tableArrays = append(tableArrays, key)
		case minitoml.KindArray:
			parts := make([]string, len(value.Array))
			for index, element := range value.Array {
				parts[index] = element.String()
			}
			vars[configbind.EnvName(key)] = strings.Join(parts, ",")
		default:
			vars[configbind.EnvName(key)] = value.String()
		}
	}
	sort.Strings(tableArrays)
	return vars, tableArrays
}

// encodeConnectionsTable is the [[middleware.rdb.connections]] array as the
// JSON pwconfig reads from its environment variable. A table whose values do
// not parse is left to the report path, where the key is named.
func encodeConnectionsTable(tables []minitoml.Document) (string, bool) {
	connections := make([]pwconfig.RDBConnectionConfig, 0, len(tables))
	for _, table := range tables {
		connection := pwconfig.RDBConnectionConfig{ConnectTimeout: 5 * time.Second}
		for _, key := range table.Keys() {
			value, _ := table.Get(key)
			var err error
			switch key {
			case "group":
				connection.Group = value.String()
			case "dsn":
				connection.DSN = value.String()
			case "readonly":
				connection.ReadOnly, err = value.AsBool()
			case "max_open_conns":
				var n int64
				n, err = value.AsInt()
				connection.MaxOpenConns = int(n)
			case "max_idle_conns":
				var n int64
				n, err = value.AsInt()
				connection.MaxIdleConns = int(n)
			case "connect_timeout":
				connection.ConnectTimeout, err = time.ParseDuration(value.String())
			case "conn_max_lifetime":
				connection.ConnMaxLifetime, err = time.ParseDuration(value.String())
			case "conn_max_idle_time":
				connection.ConnMaxIdleTime, err = time.ParseDuration(value.String())
			default:
				return "", false
			}
			if err != nil {
				return "", false
			}
		}
		connections = append(connections, connection)
	}
	encoded, err := pwconfig.EncodeConnectionsEnv(connections)
	if err != nil {
		return "", false
	}
	return encoded, true
}

// encodeBucketsTable is the [[storage.buckets]] array as the JSON pwconfig
// reads from its environment variable.
func encodeBucketsTable(tables []minitoml.Document) (string, bool) {
	buckets := make([]pwconfig.StorageBucketConfig, 0, len(tables))
	for _, table := range tables {
		bucket := pwconfig.StorageBucketConfig{}
		for _, key := range table.Keys() {
			value, _ := table.Get(key)
			var err error
			switch key {
			case "name":
				bucket.Name = value.String()
			case "backend":
				bucket.Backend = value.String()
			case "directory":
				bucket.Directory = value.String()
			case "endpoint":
				bucket.Endpoint = value.String()
			case "region":
				bucket.Region = value.String()
			case "bucket":
				bucket.Bucket = value.String()
			case "access_key_id":
				bucket.AccessKeyID = value.String()
			case "secret_access_key":
				bucket.SecretAccessKey = value.String()
			case "path_style":
				bucket.PathStyle, err = value.AsBool()
			case "binding":
				bucket.Binding = value.String()
			default:
				return "", false
			}
			if err != nil {
				return "", false
			}
		}
		buckets = append(buckets, bucket)
	}
	encoded, err := pwconfig.EncodeBucketsEnv(buckets)
	if err != nil {
		return "", false
	}
	return encoded, true
}

// storageR2Bindings are the bucket bindings the production storage set uses,
// each becoming an r2_buckets element under a bucket named after it.
func storageR2Bindings(document minitoml.Document) []string {
	if !boolKey(document, "storage.enabled") {
		return nil
	}
	buckets, ok := document.Get("storage.buckets")
	if !ok || buckets.Kind != minitoml.KindTableArray {
		return nil
	}
	var bindings []string
	seen := map[string]bool{}
	for _, table := range buckets.Tables {
		backend, _ := table.Get("backend")
		binding, _ := table.Get("binding")
		name := strings.TrimSpace(binding.String())
		if backend.String() != "r2" || name == "" || seen[name] {
			continue
		}
		seen[name] = true
		bindings = append(bindings, name)
	}
	return bindings
}

func boolKey(document minitoml.Document, key string) bool {
	value, ok := document.Get(key)
	if !ok {
		return false
	}
	enabled, err := value.AsBool()
	return err == nil && enabled
}

// cloudflareProcessStateRefusals is the build-time half of
// requirement:cloudflare-process-state-refusal: the same keys the runtime
// refuses under the pwcloudflare tag, read from config.prod.toml before the
// compiler runs, because a person sees a build failure and a Worker's log
// only later. Each entry names the key, the value and the alternative.
func cloudflareProcessStateRefusals(document minitoml.Document) []string {
	var refusals []string
	boolean := func(key string) bool {
		value, ok := document.Get(key)
		if !ok {
			return false
		}
		enabled, err := value.AsBool()
		return err == nil && enabled
	}
	text := func(key string) string {
		value, ok := document.Get(key)
		if !ok {
			return ""
		}
		return value.String()
	}
	if boolean("cache.enabled") {
		refusals = append(refusals, "cache.enabled = true: a memo store lives in the process, which a Worker recreates per request, so every lookup would miss; set it false until a KV store exists")
	}
	if backend := text("session.backend"); backend == "dev-volatile" || backend == "dev-persist" {
		refusals = append(refusals, fmt.Sprintf("session.backend = %q: a development store does not survive a Worker request; use \"cookie\", or \"rdb\" over a d1 connection", backend))
	}
	if boolean("ratelimit.enabled") {
		if backend := text("ratelimit.backend"); backend == "" || backend == "memory" {
			refusals = append(refusals, "ratelimit.backend = \"memory\": a per-process counter limits nothing on a host that runs the process per request; disable the limiter until a KV backend exists")
		}
	}
	if boolean("storage.enabled") {
		if buckets, ok := document.Get("storage.buckets"); ok && buckets.Kind == minitoml.KindTableArray {
			for _, table := range buckets.Tables {
				backend, _ := table.Get("backend")
				name, _ := table.Get("name")
				if kind := backend.String(); kind != "r2" {
					if kind == "" {
						kind = "local"
					}
					refusals = append(refusals, fmt.Sprintf("storage.buckets[%s]: the %s backend needs a directory or a socket, and a Worker has neither; use the r2 backend over a bucket binding", name.String(), kind))
				}
			}
		}
	}
	if boolean("middleware.rdb.enabled") {
		connections, ok := document.Get("middleware.rdb.connections")
		if !ok || connections.Kind != minitoml.KindTableArray {
			refusals = append(refusals, "middleware.rdb.enabled = true with no [[middleware.rdb.connections]]: an enabled pool needs a d1:// binding to reach")
		}
		for _, table := range connections.Tables {
			dsn, _ := table.Get("dsn")
			group, _ := table.Get("group")
			scheme, _, found := strings.Cut(strings.TrimSpace(dsn.String()), "://")
			if found && scheme == "d1" {
				continue
			}
			refusals = append(refusals, fmt.Sprintf("middleware.rdb.connections[%s]: a %s:// connection needs a file or a socket, and a Worker has neither; only a d1:// binding is reachable", group.String(), scheme))
		}
	}
	return refusals
}

// cloudflareD1Database is one d1_databases element of wrangler.jsonc.
type cloudflareD1Database struct {
	Binding       string `json:"binding"`
	DatabaseName  string `json:"database_name"`
	DatabaseID    string `json:"database_id"`
	MigrationsDir string `json:"migrations_dir,omitempty"`
}

// cloudflareD1Bindings are the binding names behind the d1:// connections of
// the production configuration, in the order they are declared.
func cloudflareD1Bindings(document minitoml.Document) []string {
	connections, ok := document.Get("middleware.rdb.connections")
	if !ok || connections.Kind != minitoml.KindTableArray {
		return nil
	}
	var bindings []string
	seen := map[string]bool{}
	for _, table := range connections.Tables {
		dsn, _ := table.Get("dsn")
		scheme, rest, found := strings.Cut(strings.TrimSpace(dsn.String()), "://")
		if !found || scheme != "d1" || rest == "" || seen[rest] {
			continue
		}
		seen[rest] = true
		bindings = append(bindings, rest)
	}
	return bindings
}

// cloudflareD1Databases pairs each binding the configuration uses with what
// popcornweb.toml says about it. A binding the table does not name is still
// written, under a name derived from the binding and with no id, so wrangler
// dev runs and wrangler deploy says what is missing.
func cloudflareD1Databases(settings cloudflareConfig, bindings []string) []cloudflareD1Database {
	var databases []cloudflareD1Database
	for _, binding := range bindings {
		database := cloudflareD1Database{Binding: binding, DatabaseName: strings.ToLower(binding)}
		for _, entry := range settings.D1 {
			if entry.Binding != binding {
				continue
			}
			if entry.DatabaseName != "" {
				database.DatabaseName = entry.DatabaseName
			}
			database.DatabaseID = entry.DatabaseID
		}
		databases = append(databases, database)
	}
	return databases
}

// stageD1Migrations writes the up half of every goose migration into the
// stage, under the same file names, for wrangler d1 migrations apply: that
// command runs each file whole, so the down half and the goose directives
// have to go. It returns how many files it wrote; none is not an error,
// because a project may keep its schema elsewhere.
func stageD1Migrations(source, destination string) (int, error) {
	entries, err := os.ReadDir(source)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	staged := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			return 0, err
		}
		up := gooseUpSection(string(content))
		if strings.TrimSpace(up) == "" {
			continue
		}
		if err := os.MkdirAll(destination, 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(filepath.Join(destination, entry.Name()), []byte(up), 0o644); err != nil {
			return 0, err
		}
		staged++
	}
	return staged, nil
}

// gooseUpSection is the SQL between +goose Up and +goose Down, with the
// StatementBegin and StatementEnd directives removed, which is the file a
// tool that runs a migration whole expects.
func gooseUpSection(source string) string {
	var out []string
	inUp := false
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "-- +goose Up"):
			inUp = true
			continue
		case strings.HasPrefix(trimmed, "-- +goose Down"):
			inUp = false
			continue
		case strings.HasPrefix(trimmed, "-- +goose "):
			continue
		}
		if inUp {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n")) + "\n"
}

// cloudflareKVNamespace is one kv_namespaces element of wrangler.jsonc.
type cloudflareKVNamespace struct {
	Binding     string `json:"binding"`
	ID          string `json:"id"`
	placeholder bool
}

// cloudflareKVBindings are the KV binding names the production configuration
// uses: today the rate limiter's, when it counts in KV.
func cloudflareKVBindings(document minitoml.Document) []string {
	var bindings []string
	if backend, ok := document.Get("ratelimit.backend"); ok && backend.String() == "cloudflarekv" {
		if binding, ok := document.Get("ratelimit.cloudflarekv.binding"); ok && strings.TrimSpace(binding.String()) != "" {
			bindings = append(bindings, strings.TrimSpace(binding.String()))
		}
	}
	return bindings
}

// cloudflareKVNamespaces pairs each binding the configuration uses, and each
// one popcornweb.toml names, with its id. A binding without an id gets a
// placeholder, because wrangler refuses an empty one even locally, and deploy
// against the placeholder fails naming the namespace.
func cloudflareKVNamespaces(settings cloudflareConfig, bindings []string) []cloudflareKVNamespace {
	seen := map[string]bool{}
	var namespaces []cloudflareKVNamespace
	add := func(binding, id string) {
		if seen[binding] {
			return
		}
		seen[binding] = true
		namespace := cloudflareKVNamespace{Binding: binding, ID: id}
		if namespace.ID == "" {
			namespace.ID = strings.ToLower(binding) + "-local"
			namespace.placeholder = true
		}
		namespaces = append(namespaces, namespace)
	}
	for _, binding := range bindings {
		id := ""
		for _, entry := range settings.KV {
			if entry.Binding == binding {
				id = entry.ID
			}
		}
		add(binding, id)
	}
	for _, entry := range settings.KV {
		add(entry.Binding, entry.ID)
	}
	return namespaces
}

// cloudflareR2Bucket is one r2_buckets element of wrangler.jsonc.
type cloudflareR2Bucket struct {
	Binding    string `json:"binding"`
	BucketName string `json:"bucket_name"`
}

// cloudflareR2Buckets is the bucket list for wrangler.jsonc: the external
// asset bucket when one is named, and one bucket per binding the storage
// set of requirement:object-storage uses, named after the binding.
func cloudflareR2Buckets(settings cloudflareConfig, storageBindings []string) []cloudflareR2Bucket {
	var buckets []cloudflareR2Bucket
	seen := map[string]bool{}
	if settings.R2.Configured() {
		buckets = append(buckets, cloudflareR2Bucket{Binding: settings.R2.Binding, BucketName: settings.R2.BucketName})
		seen[settings.R2.Binding] = true
	}
	for _, binding := range storageBindings {
		if seen[binding] {
			continue
		}
		seen[binding] = true
		buckets = append(buckets, cloudflareR2Bucket{Binding: binding, BucketName: strings.ToLower(binding)})
	}
	return buckets
}

// writeR2UploadScript writes the script that puts every file of the external
// tree into the bucket under its manifest path, with its media type. It is a
// script rather than an action, because uploading is a remote step
// decision:serverless-target-scope leaves to provider tooling; --local and
// --remote pass through to wrangler, so the same file seeds wrangler dev.
func writeR2UploadScript(root, path, bucket string) (int, error) {
	var script strings.Builder
	script.WriteString("#!/bin/sh\n# Code generated by pw build --target=" + targetCloudflareWorkers + ". DO NOT EDIT.\n")
	script.WriteString("# Uploads " + externalPublicDir + " into the R2 bucket the Worker serves it from.\n")
	script.WriteString("# Usage: sh r2-upload.sh --local   (seed wrangler dev)\n#        sh r2-upload.sh --remote  (the deployed bucket)\n")
	script.WriteString("set -e\ncd \"$(dirname \"$0\")\"\n")
	count := 0
	source := filepath.Join(root, externalPublicDir)
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) && path == source {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		mediaType := mime.TypeByExtension(filepath.Ext(path))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		script.WriteString("npx wrangler r2 object put " + shellQuote(bucket+"/"+key) + " --file " + shellQuote(filepath.Join(externalPublicDir, relative)) + " --content-type " + shellQuote(mediaType) + " \"$@\"\n")
		count++
		return nil
	})
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	// The files are copied beside the script, so the stage is the whole
	// upload input and the project tree need not be present at upload time.
	if err := copyExternalAssets(root, filepath.Dir(path)); err != nil {
		return 0, err
	}
	return count, os.WriteFile(path, []byte(script.String()), 0o755)
}

// shellQuote single-quotes a word for a POSIX shell.
func shellQuote(word string) string {
	return "'" + strings.ReplaceAll(word, "'", "'\\''") + "'"
}

// cloudflareWranglerConfig is wrangler.jsonc: the name, the entry, the
// compatibility date, the vars, the D1 databases and the R2 buckets. It is
// plain JSON, which JSONC accepts, so the encoder writes it and key order is
// the encoder's.
func cloudflareWranglerConfig(settings cloudflareConfig, vars map[string]string, databases []cloudflareD1Database, buckets []cloudflareR2Bucket, namespaces []cloudflareKVNamespace) ([]byte, error) {
	document := struct {
		Name              string                  `json:"name"`
		Main              string                  `json:"main"`
		CompatibilityDate string                  `json:"compatibility_date"`
		Vars              map[string]string       `json:"vars,omitempty"`
		D1Databases       []cloudflareD1Database  `json:"d1_databases,omitempty"`
		R2Buckets         []cloudflareR2Bucket    `json:"r2_buckets,omitempty"`
		KVNamespaces      []cloudflareKVNamespace `json:"kv_namespaces,omitempty"`
	}{Name: settings.Name, Main: "./" + cloudflareWorkerEntry, CompatibilityDate: settings.CompatibilityDate, Vars: vars, D1Databases: databases, R2Buckets: buckets, KVNamespaces: namespaces}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// netdevPackage is the host network driver a TinyGo project registers, per
// the netdev_registration entry of rule:tinygo-runtime-compatibility.
const netdevPackage = "github.com/shibukawa/tinygodriver/netdev"

// excludeNetdevRegistration takes the host network driver out of the staged
// TinyGo build. The scaffold writes tinygohelper.go under tinygo alone, and
// that driver is a set of OS socket calls that do not exist on the wasm
// target, so a project scaffolded before the target existed would fail to
// compile on symbols in a file it never opens. The staged copy's constraint
// gains the exclusion the scaffold writes now; the project's own file is not
// touched. Returned are the files rewritten, for the build report.
func excludeNetdevRegistration(applicationRoot string) ([]string, error) {
	entries, err := os.ReadDir(applicationRoot)
	if err != nil {
		return nil, err
	}
	var rewritten []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(applicationRoot, entry.Name())
		source, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ImportsOnly|parser.ParseComments)
		if err != nil {
			// The compiler reports the file in its own words; this pass has
			// nothing to add.
			continue
		}
		imports := false
		for _, imported := range file.Imports {
			if strings.Trim(imported.Path.Value, "\"") == netdevPackage {
				imports = true
			}
		}
		if !imports {
			continue
		}
		lines := strings.SplitN(string(source), "\n", -1)
		for index, line := range lines {
			if !constraint.IsGoBuild(line) {
				if strings.HasPrefix(line, "package ") {
					break
				}
				continue
			}
			expression, err := constraint.Parse(line)
			if err != nil {
				break
			}
			mentionsTag := false
			selectsTinyGo := expression.Eval(func(tag string) bool {
				if tag == cloudflareBuildTag {
					mentionsTag = true
				}
				return tag == "tinygo"
			})
			if !selectsTinyGo || mentionsTag {
				break
			}
			lines[index] = "//go:build (" + strings.TrimPrefix(line, "//go:build ") + ") && !" + cloudflareBuildTag
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
				return nil, err
			}
			rewritten = append(rewritten, entry.Name())
			break
		}
	}
	sort.Strings(rewritten)
	return rewritten, nil
}

// hostEnginePackages are the SQL engines a process links and a Worker cannot
// reach: the SQLite driver is a C library ported to a host libc, which the
// wasm target has none of, and the two servers need a socket. In the staged
// copy each blank import becomes the D1 engine, per
// requirement:cloudflare-d1-engine, which is the one engine the host offers
// and the same dialect as the SQLite the project develops on.
var hostEnginePackages = map[string]bool{
	sqliteDriverPackage:   true,
	postgresDriverPackage: true,
	mysqlDriverPackage:    true,
}

// rewriteEngineImports swaps every blank import of a host SQL engine in the
// staged application for the D1 engine. The stage root holds the transformed
// main, and the application copy holds everything else, so both are walked;
// the project's own files are not touched. A non-blank import is an error,
// because a package that reads the engine's symbols has no D1 equivalent.
func rewriteEngineImports(stage, applicationRoot string) ([]string, error) {
	var rewritten []string
	visit := func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != stage && path != applicationRoot && (entry.Name() == "app" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ImportsOnly)
		if err != nil {
			return nil
		}
		changed := false
		text := string(source)
		for _, imported := range file.Imports {
			imported_path := strings.Trim(imported.Path.Value, "\"")
			if !hostEnginePackages[imported_path] {
				continue
			}
			if imported.Name == nil || imported.Name.Name != "_" {
				relative, _ := filepath.Rel(filepath.Dir(stage), path)
				return fmt.Errorf("%s imports %s by name, which has no Cloudflare equivalent; a Worker build needs the engine linked only by a blank import", relative, imported_path)
			}
			text = strings.ReplaceAll(text, "\""+imported_path+"\"", "\""+d1DriverPackage+"\"")
			changed = true
		}
		if !changed {
			return nil
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return err
		}
		relative, _ := filepath.Rel(stage, path)
		rewritten = append(rewritten, filepath.ToSlash(relative))
		return nil
	}
	if err := filepath.WalkDir(stage, func(path string, entry fs.DirEntry, walkErr error) error {
		// The stage root package only; the application copy is walked below,
		// and the build directory holds nothing to parse.
		if entry != nil && entry.IsDir() && path != stage {
			return filepath.SkipDir
		}
		return visit(path, entry, walkErr)
	}); err != nil {
		return nil, err
	}
	if err := filepath.WalkDir(applicationRoot, visit); err != nil {
		return nil, err
	}
	sort.Strings(rewritten)
	return rewritten, nil
}

// countExternalAssets is how many files the external public tree holds, which
// is the number a Worker build leaves behind. A dotfile is not counted: the
// scaffold keeps the directory in version control with one, and a sentinel
// is not an asset anyone expected to be served.
func countExternalAssets(root string) int {
	count := 0
	_ = filepath.WalkDir(filepath.Join(root, externalPublicDir), func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			count++
		}
		return nil
	})
	return count
}

// wasmSizes is the module's size raw and gzip-compressed, the two numbers a
// plan limit is stated in.
func wasmSizes(path string) (int64, int64, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(content); err != nil {
		return 0, 0, err
	}
	if err := writer.Close(); err != nil {
		return 0, 0, err
	}
	return int64(len(content)), int64(compressed.Len()), nil
}
