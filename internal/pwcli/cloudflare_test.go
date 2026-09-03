package pwcli

import (
	"encoding/json"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibukawa/tinybind-go/minitoml"
)

func TestCloudflareSettingsDefaultToTheProject(t *testing.T) {
	document, err := minitoml.ParseString("[project]\nname = \"demo\"\n")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := cloudflareSettings(document, projectConfig{Name: "demo", Toolchain: toolchainGo})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Compiler != toolchainGo || settings.Name != "demo" || settings.CompatibilityDate != defaultCompatibilityDate {
		t.Errorf("defaults not drawn from the project: %+v", settings)
	}
}

func TestCloudflareSettingsReadTheTable(t *testing.T) {
	document, err := minitoml.ParseString("[deploy.cloudflare]\ncompiler = \"tinygo\"\nname = \"edge-demo\"\ncompatibility_date = \"2026-01-15\"\n")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := cloudflareSettings(document, projectConfig{Name: "demo", Toolchain: toolchainTinyGo})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Compiler != toolchainTinyGo || settings.Name != "edge-demo" || settings.CompatibilityDate != "2026-01-15" {
		t.Errorf("table not read: %+v", settings)
	}
}

func TestCloudflareSettingsRejectBadValues(t *testing.T) {
	for _, source := range []string{
		"[deploy.cloudflare]\ncompiler = \"rustc\"\n",
		"[deploy.cloudflare]\ncompatibility_date = \"yesterday\"\n",
	} {
		document, err := minitoml.ParseString(source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cloudflareSettings(document, projectConfig{Name: "demo", Toolchain: toolchainTinyGo}); err == nil {
			t.Errorf("accepted %q", source)
		}
	}
}

// A project scaffolded for host Go routes through the standard ServeMux, which
// TinyGo compiles but does not match method patterns on: the Worker answered
// 404 on every route when this was allowed. The other direction stays open.
func TestCloudflareSettingsRefuseTinyGoForAHostGoProject(t *testing.T) {
	document, err := minitoml.ParseString("[deploy.cloudflare]\ncompiler = \"tinygo\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cloudflareSettings(document, projectConfig{Name: "demo", Toolchain: toolchainGo}); err == nil || !strings.Contains(err.Error(), "ServeMux") {
		t.Errorf("tinygo accepted for a go project: %v", err)
	}
	document, err = minitoml.ParseString("[deploy.cloudflare]\ncompiler = \"go\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cloudflareSettings(document, projectConfig{Name: "demo", Toolchain: toolchainTinyGo}); err != nil {
		t.Errorf("go refused for a tinygo project: %v", err)
	}
}

func TestLoadProjectConfigCloudflareTable(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "popcornweb.toml"), "[project]\nname = \"demo\"\nmain = \"./cmd/demo\"\ntoolchain = \"tinygo\"\n"+
		"[generate]\nhandlers = []\ntemplates = []\nqueries = []\nconfig = []\n"+
		"[deploy.cloudflare]\ncompiler = \"go\"\n")
	config, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if config.Cloudflare.Compiler != toolchainGo || config.Cloudflare.Name != "demo" {
		t.Errorf("deploy.cloudflare not loaded: %+v", config.Cloudflare)
	}
}

func TestCloudflareTargetTakesTheNetHTTPBackendOnly(t *testing.T) {
	if !deploymentTargets[targetCloudflareWorkers] {
		t.Fatal("cloudflare-workers is not a target")
	}
	options := buildOptions{backend: backendFastHTTP, target: targetCloudflareWorkers}
	if err := options.check(projectConfig{FastHTTP: true}); err == nil {
		t.Error("fasthttp accepted under the cloudflare target")
	}
	options.backend = backendNetHTTP
	if err := options.check(projectConfig{}); err != nil {
		t.Errorf("nethttp refused: %v", err)
	}
}

func TestCloudflareWrapperBridgesTheWorkerEnvironment(t *testing.T) {
	source := cloudflareWrapper(cloudflareConfig{})
	if _, err := format.Source([]byte(source)); err != nil {
		t.Fatalf("invalid Go: %v\n%s", err, source)
	}
	for _, want := range []string{
		"package main", "pw.Middlewares", "workers.Serve(handler)", "pwconfig.SetLoadOptions", `_ "github.com/shibukawa/popcornweb/database/d1"`,
		`js.Global().Get("context")`, `"APP_ENV=prod"`, "initializeApplication()",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("wrapper misses %q", want)
		}
	}
	if strings.Contains(source, "os.Setenv") {
		t.Error("wrapper sets the process environment, which the host Go js runtime cannot do")
	}
	if strings.Contains(source, "WithExternalAssets") {
		t.Error("an unconfigured bucket reached the wrapper")
	}
	if !strings.Contains(source, `_ "github.com/shibukawa/popcornweb/cloudflare/r2"`) {
		t.Error("the r2 storage backend is not linked")
	}
	withBucket := cloudflareWrapper(cloudflareConfig{R2: cloudflareR2Config{Binding: "ASSETS", BucketName: "demo-assets"}})
	if _, err := format.Source([]byte(withBucket)); err != nil {
		t.Fatalf("invalid Go with a bucket: %v\n%s", err, withBucket)
	}
	if !strings.Contains(withBucket, `pw.WithExternalAssets(r2.ExternalAssets("ASSETS"))`) {
		t.Errorf("bucket option missing:\n%s", withBucket)
	}
}

// The loaders are the pinned compilers' own files plus the context proxy, per
// decision:owned-wasm-loader. The TinyGo import list is checked for the entry
// the upstream generator lacked, because that is the failure the decision
// exists to catch before a deploy does.
func TestCloudflareLoadersCarryTheContextAndTheImports(t *testing.T) {
	tinygo, err := fs.ReadFile(cloudflareAssets, "cloudflare_assets/wasm_exec_tinygo.js")
	if err != nil {
		t.Fatal(err)
	}
	hostGo, err := fs.ReadFile(cloudflareAssets, "cloudflare_assets/wasm_exec_go.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, loader := range map[string][]byte{"tinygo": tinygo, "go": hostGo} {
		for _, want := range []string{"async run(instance, context)", `if (prop === "context")`, "globalProxy,"} {
			if !strings.Contains(string(loader), want) {
				t.Errorf("%s loader misses %q", name, want)
			}
		}
	}
	if !strings.Contains(string(tinygo), `"runtime.getRandomData"`) {
		t.Error("tinygo loader lacks the runtime.getRandomData import TinyGo 0.42 modules need")
	}
	if !strings.Contains(string(hostGo), "[globalProxy, 5]") {
		t.Error("go loader maps globalThis's reference id to the proxy in _values but not in _ids")
	}
	worker, err := fs.ReadFile(cloudflareAssets, "cloudflare_assets/worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"globalThis.tryCatch", "ready()", "go.run(instance, context)", "binding.handleRequest(request)", "go.env = ", "Promise.race([readyPromise, exited])"} {
		if !strings.Contains(string(worker), want) {
			t.Errorf("worker.mjs misses %q", want)
		}
	}
}

func TestCloudflareVarsFlattenProductionConfig(t *testing.T) {
	vars, tableArrays, err := cloudflareVars([]byte(`
[server]
port = 8080
[server.public]
mount = "/public"
[observability]
minimum_level = "info"
[session]
secret = "${SESSION_SECRET}"
[middleware.rdb]
enabled = true
[[middleware.rdb.connections]]
group = "default"
dsn = "${DATABASE_URL}"
[html]
bot_user_agents = ["Googlebot", "Bingbot"]
`))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"SERVER_PORT": "8080", "SERVER_PUBLIC_MOUNT": "/public", "OBSERVABILITY_MINIMUM_LEVEL": "info",
		"SESSION_SECRET": "${SESSION_SECRET}", "MIDDLEWARE_RDB_ENABLED": "true", "HTML_BOT_USER_AGENTS": "Googlebot,Bingbot",
	} {
		if vars[name] != want {
			t.Errorf("%s = %q, want %q", name, vars[name], want)
		}
	}
	if len(tableArrays) != 0 {
		t.Errorf("table arrays reported as %v", tableArrays)
	}
	// The connection array travels as JSON in one variable, with the secret
	// reference intact for the Worker to resolve.
	if encoded := vars["MIDDLEWARE_RDB_CONNECTIONS"]; !strings.Contains(encoded, `"group":"default"`) || !strings.Contains(encoded, `"dsn":"${DATABASE_URL}"`) {
		t.Errorf("connections not encoded: %q", encoded)
	}
}

func TestCloudflareWranglerConfigIsJSON(t *testing.T) {
	encoded, err := cloudflareWranglerConfig(cloudflareConfig{Name: "demo", CompatibilityDate: "2025-08-01"}, map[string]string{"SERVER_PORT": "8080"}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Name, Main, CompatibilityDate string `json:"-"`
		Fields                        map[string]any
	}
	if err := json.Unmarshal(encoded, &document.Fields); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, encoded)
	}
	if document.Fields["name"] != "demo" || document.Fields["main"] != "./build/worker.mjs" || document.Fields["compatibility_date"] != "2025-08-01" {
		t.Errorf("wrangler.jsonc carries %v", document.Fields)
	}
	if vars, _ := document.Fields["vars"].(map[string]any); vars["SERVER_PORT"] != "8080" {
		t.Errorf("vars not written: %v", document.Fields["vars"])
	}
}

// A project scaffolded before the target existed registers the host network
// driver under tinygo alone, and that driver does not compile for wasm. The
// staged copy gains the exclusion the scaffold writes now; a file that already
// carries it, or that imports something else, is left as it is.
func TestExcludeNetdevRegistrationRewritesTheOldConstraintOnly(t *testing.T) {
	root := t.TempDir()
	old := "//go:build tinygo\n\npackage publicassets\n\nimport _ \"github.com/shibukawa/tinygodriver/netdev\"\n"
	current := "//go:build tinygo && !pwcloudflare\n\npackage publicassets\n\nimport _ \"github.com/shibukawa/tinygodriver/netdev\"\n"
	other := "//go:build tinygo\n\npackage publicassets\n\nimport _ \"embed\"\n"
	writeTestFile(t, filepath.Join(root, "tinygohelper.go"), old)
	writeTestFile(t, filepath.Join(root, "helper_current.go"), current)
	writeTestFile(t, filepath.Join(root, "other.go"), other)
	rewritten, err := excludeNetdevRegistration(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rewritten) != 1 || rewritten[0] != "tinygohelper.go" {
		t.Errorf("rewrote %v", rewritten)
	}
	source, _ := os.ReadFile(filepath.Join(root, "tinygohelper.go"))
	if !strings.HasPrefix(string(source), "//go:build (tinygo) && !pwcloudflare\n") {
		t.Errorf("constraint not rewritten:\n%s", source)
	}
	for _, name := range []string{"helper_current.go", "other.go"} {
		source, _ := os.ReadFile(filepath.Join(root, name))
		if string(source) != map[string]string{"helper_current.go": current, "other.go": other}[name] {
			t.Errorf("%s changed:\n%s", name, source)
		}
	}
}

func TestTinyGoScaffoldLeavesTheNetdevDriverOutOfWorkers(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", TinyGo: true})
	helper, ok := files["tinygohelper.go"]
	if !ok {
		t.Fatal("no tinygohelper.go scaffolded")
	}
	if !strings.HasPrefix(helper, "//go:build tinygo && !pwcloudflare\n") {
		t.Errorf("helper constraint: %s", strings.SplitN(helper, "\n", 2)[0])
	}
}

// The build refuses the same state the runtime refuses under the pwcloudflare
// tag, and does it before the compiler runs; the d1 scheme is the one the
// host can reach.
func TestCloudflareBuildRefusesProcessState(t *testing.T) {
	document, err := minitoml.ParseString(`
[cache]
enabled = true
[session]
enabled = true
backend = "dev-persist"
[ratelimit]
enabled = true
[middleware.rdb]
enabled = true
[[middleware.rdb.connections]]
group = "default"
dsn = "sqlite://app.db"
[[middleware.rdb.connections]]
group = "edge"
dsn = "d1://DB"
`)
	if err != nil {
		t.Fatal(err)
	}
	refusals := cloudflareProcessStateRefusals(document)
	joined := strings.Join(refusals, "\n")
	for _, want := range []string{"cache.enabled", `session.backend = "dev-persist"`, "ratelimit.backend", "connections[default]: a sqlite://"} {
		if !strings.Contains(joined, want) {
			t.Errorf("refusals miss %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "connections[edge]") {
		t.Errorf("the d1 connection was refused:\n%s", joined)
	}
	accepted, err := minitoml.ParseString("[session]\nenabled = true\nbackend = \"cookie\"\n[ratelimit]\nenabled = true\nbackend = \"redis\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if refusals := cloudflareProcessStateRefusals(accepted); len(refusals) != 0 {
		t.Errorf("a Worker configuration was refused: %v", refusals)
	}
}

func TestCloudflareD1DatabasesFollowTheProductionBindings(t *testing.T) {
	document, err := minitoml.ParseString(`
[middleware.rdb]
enabled = true
[[middleware.rdb.connections]]
group = "default"
dsn = "d1://DB"
[[middleware.rdb.connections]]
group = "replica"
dsn = "d1://DB"
[[middleware.rdb.connections]]
group = "analytics"
dsn = "d1://ANALYTICS"
`)
	if err != nil {
		t.Fatal(err)
	}
	bindings := cloudflareD1Bindings(document)
	if strings.Join(bindings, ",") != "DB,ANALYTICS" {
		t.Errorf("bindings %v", bindings)
	}
	settings := cloudflareConfig{D1: []cloudflareD1Config{{Binding: "DB", DatabaseName: "demo-prod", DatabaseID: "0000-1111"}}}
	databases := cloudflareD1Databases(settings, bindings)
	if len(databases) != 2 || databases[0].DatabaseName != "demo-prod" || databases[0].DatabaseID != "0000-1111" {
		t.Errorf("named binding not applied: %+v", databases)
	}
	if databases[1].Binding != "ANALYTICS" || databases[1].DatabaseName != "analytics" || databases[1].DatabaseID != "" {
		t.Errorf("unnamed binding not derived: %+v", databases[1])
	}
	// The build fills an empty id with the name before writing, because
	// wrangler refuses an empty one even locally; that happens at the call
	// site, so the derivation above stays honest about what it knows.
	encoded, err := cloudflareWranglerConfig(cloudflareConfig{Name: "demo", CompatibilityDate: "2025-08-01"}, nil, databases, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if list, _ := fields["d1_databases"].([]any); len(list) != 2 {
		t.Errorf("d1_databases not written:\n%s", encoded)
	}
}

func TestCloudflareSettingsReadTheD1Table(t *testing.T) {
	document, err := minitoml.ParseString("[[deploy.cloudflare.d1]]\nbinding = \"DB\"\ndatabase_name = \"demo\"\ndatabase_id = \"abc\"\n")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := cloudflareSettings(document, projectConfig{Name: "demo", Toolchain: toolchainGo})
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.D1) != 1 || settings.D1[0].Binding != "DB" || settings.D1[0].DatabaseID != "abc" {
		t.Errorf("d1 table not read: %+v", settings.D1)
	}
	for _, source := range []string{
		"[[deploy.cloudflare.d1]]\ndatabase_name = \"demo\"\n",
		"[[deploy.cloudflare.d1]]\nbinding = \"DB\"\n[[deploy.cloudflare.d1]]\nbinding = \"DB\"\n",
		"[[deploy.cloudflare.d1]]\nbinding = \"DB\"\nregion = \"apac\"\n",
	} {
		document, err := minitoml.ParseString(source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cloudflareSettings(document, projectConfig{Name: "demo", Toolchain: toolchainGo}); err == nil {
			t.Errorf("accepted %q", source)
		}
	}
}

func TestGooseUpSectionKeepsOnlyTheUpHalf(t *testing.T) {
	up := gooseUpSection("-- +goose Up\n-- +goose StatementBegin\nCREATE TABLE t (id INTEGER);\n-- +goose StatementEnd\n\n-- +goose Down\nDROP TABLE t;\n")
	if up != "CREATE TABLE t (id INTEGER);\n" {
		t.Errorf("up section %q", up)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "migrations", "00001_init.sql"), "-- +goose Up\nCREATE TABLE a (id INTEGER);\n-- +goose Down\nDROP TABLE a;\n")
	writeTestFile(t, filepath.Join(root, "migrations", "00002_empty.sql"), "-- +goose Up\n-- +goose Down\n")
	writeTestFile(t, filepath.Join(root, "migrations", "notes.txt"), "not sql")
	staged, err := stageD1Migrations(filepath.Join(root, "migrations"), filepath.Join(root, "stage"))
	if err != nil {
		t.Fatal(err)
	}
	if staged != 1 {
		t.Errorf("staged %d files", staged)
	}
	if content, _ := os.ReadFile(filepath.Join(root, "stage", "00001_init.sql")); string(content) != "CREATE TABLE a (id INTEGER);\n" {
		t.Errorf("staged file:\n%s", content)
	}
	if staged, err := stageD1Migrations(filepath.Join(root, "absent"), filepath.Join(root, "stage2")); err != nil || staged != 0 {
		t.Errorf("absent directory: %d, %v", staged, err)
	}
}

// A Worker cannot link the host SQL engines, so the staged copy links D1 in
// their place; the transformed main in the stage root and the application
// copy are both rewritten, and a named import is refused.
func TestRewriteEngineImportsSwapsBlankEngineImportsForD1(t *testing.T) {
	stage := t.TempDir()
	app := filepath.Join(stage, "app")
	writeTestFile(t, filepath.Join(stage, "main.go"), "package main\n\nimport (\n\t_ \"github.com/shibukawa/popcornweb/database/sqlite\"\n\t_ \"github.com/shibukawa/popcornweb/sessionstore/sqlite\"\n)\n\nfunc initializeApplication() {}\n")
	if err := os.MkdirAll(filepath.Join(app, "handlers"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(app, "handlers", "db.go"), "package handlers\n\nimport _ \"github.com/shibukawa/popcornweb/database/postgres\"\n")
	writeTestFile(t, filepath.Join(app, "handlers", "plain.go"), "package handlers\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n")
	rewritten, err := rewriteEngineImports(stage, app)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rewritten, ",") != "app/handlers/db.go,main.go" {
		t.Errorf("rewrote %v", rewritten)
	}
	main, _ := os.ReadFile(filepath.Join(stage, "main.go"))
	if !strings.Contains(string(main), `_ "github.com/shibukawa/popcornweb/database/d1"`) || strings.Contains(string(main), "database/sqlite") || !strings.Contains(string(main), "sessionstore/sqlite") {
		t.Errorf("main.go after rewrite:\n%s", main)
	}
	writeTestFile(t, filepath.Join(app, "handlers", "named.go"), "package handlers\n\nimport \"github.com/shibukawa/popcornweb/database/mysql\"\n\nvar _ = mysql.Dialect\n")
	if _, err := rewriteEngineImports(stage, app); err == nil || !strings.Contains(err.Error(), "by name") {
		t.Errorf("named engine import accepted: %v", err)
	}
}

func TestCloudflareR2SettingsAndUploadScript(t *testing.T) {
	document, err := minitoml.ParseString("[deploy.cloudflare.r2]\nbinding = \"ASSETS\"\n")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := cloudflareSettings(document, projectConfig{Name: "demo", Toolchain: toolchainGo})
	if err != nil {
		t.Fatal(err)
	}
	if settings.R2.Binding != "ASSETS" || settings.R2.BucketName != "assets" {
		t.Errorf("r2 settings %+v", settings.R2)
	}
	if document, err := minitoml.ParseString("[deploy.cloudflare.r2]\nbucket_name = \"x\"\n"); err == nil {
		if _, err := cloudflareSettings(document, projectConfig{Name: "demo", Toolchain: toolchainGo}); err == nil {
			t.Error("a bucket name without a binding was accepted")
		}
	}
	buckets := cloudflareR2Buckets(settings, []string{"UPLOADS", "ASSETS"})
	if len(buckets) != 2 || buckets[0].Binding != "ASSETS" || buckets[0].BucketName != "assets" || buckets[1].Binding != "UPLOADS" || buckets[1].BucketName != "uploads" {
		t.Errorf("r2_buckets %+v", buckets)
	}
	encoded, err := cloudflareWranglerConfig(settings, nil, nil, buckets, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"r2_buckets"`) {
		t.Errorf("wrangler.jsonc lacks r2_buckets:\n%s", encoded)
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, externalPublicDir, "video"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, externalPublicDir, "video", "intro.mp4"), "not really video")
	writeTestFile(t, filepath.Join(root, externalPublicDir, "it's.txt"), "quoted")
	writeTestFile(t, filepath.Join(root, externalPublicDir, ".keep"), "")
	stage := filepath.Join(root, "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	count, err := writeR2UploadScript(root, filepath.Join(stage, "r2-upload.sh"), "assets")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("scripted %d uploads", count)
	}
	script, _ := os.ReadFile(filepath.Join(stage, "r2-upload.sh"))
	for _, want := range []string{
		"npx wrangler r2 object put 'assets/video/intro.mp4' --file 'public-external/video/intro.mp4' --content-type 'video/mp4' \"$@\"",
		`'assets/it'\''s.txt'`,
	} {
		if !strings.Contains(string(script), want) {
			t.Errorf("script misses %q:\n%s", want, script)
		}
	}
	if strings.Contains(string(script), ".keep") {
		t.Error("the sentinel was scripted")
	}
	if _, err := os.Stat(filepath.Join(stage, externalPublicDir, "video", "intro.mp4")); err != nil {
		t.Error("the tree was not copied beside the script")
	}
	if count, err := writeR2UploadScript(filepath.Join(root, "absent"), filepath.Join(stage, "none.sh"), "assets"); err != nil || count != 0 {
		t.Errorf("absent tree: %d, %v", count, err)
	}
}

// A rebuild clears the stage, and wrangler dev's local state lives under it;
// keeping the state across the clear is what makes a rebuild not also a
// reset of every locally applied migration and uploaded object.
func TestWranglerStateSurvivesAStageRebuild(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "cloudflare-workers", "nethttp")
	for _, dir := range []string{filepath.Join(stage, ".wrangler", "state"), filepath.Join(stage, "build")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(stage, ".wrangler", "state", "d1.sqlite"), "data")
	writeTestFile(t, filepath.Join(stage, "build", "app.wasm"), "old")
	kept, err := keepWranglerState(stage, targetCloudflareWorkers)
	if err != nil || kept == "" {
		t.Fatalf("state not kept: %q, %v", kept, err)
	}
	if err := os.RemoveAll(stage); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := restoreWranglerState(stage, kept); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(filepath.Join(stage, ".wrangler", "state", "d1.sqlite")); string(content) != "data" {
		t.Error("state not restored")
	}
	if _, err := os.Stat(filepath.Join(stage, "build", "app.wasm")); err == nil {
		t.Error("the old artifact survived the clear")
	}
	if kept, err := keepWranglerState(stage, targetLambda); err != nil || kept != "" {
		t.Errorf("another target kept state: %q, %v", kept, err)
	}
}

func TestCloudflareKVNamespacesFollowTheRateLimiter(t *testing.T) {
	document, err := minitoml.ParseString("[ratelimit]\nenabled = true\nbackend = \"cloudflarekv\"\n[ratelimit.cloudflarekv]\nbinding = \"RATELIMIT\"\n")
	if err != nil {
		t.Fatal(err)
	}
	bindings := cloudflareKVBindings(document)
	if strings.Join(bindings, ",") != "RATELIMIT" {
		t.Errorf("bindings %v", bindings)
	}
	settings := cloudflareConfig{KV: []cloudflareKVConfig{{Binding: "CACHE", ID: "abc123"}}}
	namespaces := cloudflareKVNamespaces(settings, bindings)
	if len(namespaces) != 2 || namespaces[0].Binding != "RATELIMIT" || !namespaces[0].placeholder || namespaces[0].ID == "" {
		t.Errorf("rate limit namespace: %+v", namespaces)
	}
	if namespaces[1].Binding != "CACHE" || namespaces[1].ID != "abc123" || namespaces[1].placeholder {
		t.Errorf("named namespace: %+v", namespaces[1])
	}
	encoded, err := cloudflareWranglerConfig(cloudflareConfig{Name: "demo", CompatibilityDate: "2025-08-01"}, nil, nil, nil, namespaces)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"kv_namespaces"`) || strings.Contains(string(encoded), "placeholder") {
		t.Errorf("wrangler.jsonc:\n%s", encoded)
	}
	if refusals := cloudflareProcessStateRefusals(document); len(refusals) != 0 {
		t.Errorf("the KV limiter was refused: %v", refusals)
	}
	table, err := minitoml.ParseString("[[deploy.cloudflare.kv]]\nbinding = \"CACHE\"\nid = \"abc\"\n[[deploy.cloudflare.kv]]\nbinding = \"CACHE\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cloudflareSettings(table, projectConfig{Name: "demo", Toolchain: toolchainGo}); err == nil {
		t.Error("a duplicate KV binding was accepted")
	}
}

func TestCloudflareStorageBucketsTravelAsVarsAndBindings(t *testing.T) {
	document, err := minitoml.ParseString(`
[storage]
enabled = true
[[storage.buckets]]
name = "uploads"
backend = "r2"
binding = "UPLOADS"
[[storage.buckets]]
name = "archive"
backend = "s3"
bucket = "archive"
access_key_id = "${KEY}"
`)
	if err != nil {
		t.Fatal(err)
	}
	vars, tableArrays := cloudflareVarsOf(document)
	if len(tableArrays) != 0 {
		t.Errorf("table arrays reported: %v", tableArrays)
	}
	if encoded := vars["STORAGE_BUCKETS"]; !strings.Contains(encoded, `"binding":"UPLOADS"`) || !strings.Contains(encoded, `"access_key_id":"${KEY}"`) {
		t.Errorf("buckets not encoded: %q", encoded)
	}
	if bindings := storageR2Bindings(document); strings.Join(bindings, ",") != "UPLOADS" {
		t.Errorf("bindings %v", bindings)
	}
	refusals := cloudflareProcessStateRefusals(document)
	if len(refusals) != 1 || !strings.Contains(refusals[0], "buckets[archive]: the s3 backend") {
		t.Errorf("refusals %v", refusals)
	}
}
