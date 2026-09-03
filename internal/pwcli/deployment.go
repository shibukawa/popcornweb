package pwcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

const functionsFrameworkVersion = "v1.9.2"
const lambdaWebAdapterVersion = "1.0.1"

type deploymentManifest struct {
	Target     string `json:"target"`
	Backend    string `json:"backend"`
	Artifact   string `json:"artifact"`
	Entrypoint string `json:"entrypoint,omitempty"`
	// Compiler names the toolchain that produced the artifact where the
	// target lets the project choose one, per requirement:cloudflare-workers-build-target.
	Compiler string `json:"compiler,omitempty"`
}

func buildDeployment(ctx context.Context, root string, config projectConfig, options buildOptions, progress *progressRegion, stdout, stderr io.Writer) error {
	stage := filepath.Join(root, ".pw", "build", options.target, options.backend)
	// wrangler dev keeps its local D1, R2 and KV state under the stage, and a
	// rebuild that emptied it would take every locally applied migration and
	// uploaded object with it. The state is kept aside while the stage is
	// cleared, so a rebuild is a rebuild of the Worker and not of its data.
	preserved, keepErr := keepWranglerState(stage, options.target)
	if keepErr != nil {
		return keepErr
	}
	if err := os.RemoveAll(stage); err != nil {
		return fmt.Errorf("clear deployment staging: %w", err)
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return fmt.Errorf("create deployment staging: %w", err)
	}
	if err := restoreWranglerState(stage, preserved); err != nil {
		return err
	}

	var manifest deploymentManifest
	var err error
	switch options.target {
	case targetLambda, targetAzureFunctions:
		progress.Phase("compiling Linux application")
		manifest, err = buildProcessDeployment(ctx, root, stage, config, options, stdout, stderr)
	case targetGoogleCloudRunFunctions, targetVercelGo:
		progress.Phase("staging function source")
		manifest, err = buildSourceDeployment(ctx, root, stage, config, options, stdout, stderr)
	case targetCloudflareWorkers:
		manifest, err = buildCloudflareDeployment(ctx, root, stage, config, options, progress, stdout, stderr)
	default:
		err = fmt.Errorf("unsupported deployment target %q", options.target)
	}
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(stage, "deployment.json"), encoded, 0o644); err != nil {
		return fmt.Errorf("write deployment manifest: %w", err)
	}
	fmt.Fprintf(stdout, "deployment: %s\n", stage)
	return nil
}

// wranglerStateDir is where wrangler dev keeps a stage's local state.
const wranglerStateDir = ".wrangler"

// keepWranglerState moves the Cloudflare stage's local state out of the way
// before the stage is cleared, returning where it went, or nothing when the
// target is another provider or no state exists.
func keepWranglerState(stage, target string) (string, error) {
	if target != targetCloudflareWorkers {
		return "", nil
	}
	state := filepath.Join(stage, wranglerStateDir)
	if _, err := os.Stat(state); err != nil {
		return "", nil
	}
	kept := stage + ".wrangler-state"
	if err := os.RemoveAll(kept); err != nil {
		return "", err
	}
	if err := os.Rename(state, kept); err != nil {
		return "", fmt.Errorf("keep wrangler state: %w", err)
	}
	return kept, nil
}

// restoreWranglerState puts kept state back under the rebuilt stage.
func restoreWranglerState(stage, kept string) error {
	if kept == "" {
		return nil
	}
	if err := os.Rename(kept, filepath.Join(stage, wranglerStateDir)); err != nil {
		return fmt.Errorf("restore wrangler state: %w", err)
	}
	return nil
}

func buildProcessDeployment(ctx context.Context, root, stage string, config projectConfig, options buildOptions, stdout, stderr io.Writer) (deploymentManifest, error) {
	binary := "bootstrap"
	if options.target == targetAzureFunctions {
		binary = "handler"
	}
	arguments := []string{"build", "-trimpath", "-o", filepath.Join(stage, binary)}
	if !options.debug {
		arguments = append(arguments, "-ldflags=-s -w")
	}
	arguments = append(arguments, options.tags()...)
	arguments = append(arguments, config.Main)
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir, command.Stdout, command.Stderr = root, stdout, stderr
	command.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if err := command.Run(); err != nil {
		return deploymentManifest{}, fmt.Errorf("go build deployment: %w", err)
	}
	productionConfig := filepath.Join(root, "config.prod.toml")
	info, err := os.Stat(productionConfig)
	if err != nil {
		return deploymentManifest{}, fmt.Errorf("deployment requires config.prod.toml: %w", err)
	}
	if err := copyDeploymentFile(productionConfig, filepath.Join(stage, "config.prod.toml"), info.Mode()); err != nil {
		return deploymentManifest{}, err
	}
	if err := copyExternalAssets(root, stage); err != nil {
		return deploymentManifest{}, fmt.Errorf("copy %s: %w", externalPublicDir, err)
	}

	manifest := deploymentManifest{Target: options.target, Backend: options.backend, Artifact: binary}
	switch options.target {
	case targetLambda:
		dockerfile := "FROM public.ecr.aws/awsguru/aws-lambda-adapter:" + lambdaWebAdapterVersion + " AS adapter\n" +
			"FROM gcr.io/distroless/static-debian12:nonroot\n" +
			"COPY --from=adapter /lambda-adapter /opt/extensions/lambda-adapter\n" +
			"COPY --chown=nonroot:nonroot bootstrap config.prod.toml /\n" +
			// The tree that ships beside the binary. It lands at the root
			// because that is the working directory /bootstrap runs with, and
			// the server resolves the directory against that.
			"COPY --chown=nonroot:nonroot " + externalPublicDir + " /" + externalPublicDir + "\n" +
			"ENV APP_ENV=prod PORT=8080\nUSER nonroot\nENTRYPOINT [\"/bootstrap\"]\n"
		if err := os.WriteFile(filepath.Join(stage, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
			return deploymentManifest{}, err
		}
	case targetAzureFunctions:
		host := "{\n  \"version\": \"2.0\",\n  \"customHandler\": {\n    \"description\": {\n      \"defaultExecutablePath\": \"run.sh\"\n    },\n    \"enableProxyingHttpRequest\": true\n  },\n  \"extensions\": {\n    \"http\": {\n      \"routePrefix\": \"\"\n    }\n  }\n}\n"
		startup := "#!/bin/sh\nexport APP_ENV=\"${APP_ENV:-prod}\"\nexec ./handler\n"
		function := "{\n  \"bindings\": [\n    {\n      \"type\": \"httpTrigger\",\n      \"direction\": \"in\",\n      \"name\": \"req\",\n      \"authLevel\": \"anonymous\",\n      \"route\": \"{*route}\",\n      \"methods\": [\"get\", \"head\", \"post\", \"put\", \"patch\", \"delete\", \"options\"]\n    },\n    {\n      \"type\": \"http\",\n      \"direction\": \"out\",\n      \"name\": \"res\"\n    }\n  ]\n}\n"
		if err := os.WriteFile(filepath.Join(stage, "host.json"), []byte(host), 0o644); err != nil {
			return deploymentManifest{}, err
		}
		if err := os.WriteFile(filepath.Join(stage, "run.sh"), []byte(startup), 0o755); err != nil {
			return deploymentManifest{}, err
		}
		if err := os.MkdirAll(filepath.Join(stage, "http"), 0o755); err != nil {
			return deploymentManifest{}, err
		}
		if err := os.WriteFile(filepath.Join(stage, "http", "function.json"), []byte(function), 0o644); err != nil {
			return deploymentManifest{}, err
		}
	}
	return manifest, nil
}

func buildSourceDeployment(ctx context.Context, root, stage string, config projectConfig, options buildOptions, stdout, stderr io.Writer) (deploymentManifest, error) {
	applicationRoot := filepath.Join(stage, "app")
	if err := copyProjectForFunction(root, applicationRoot); err != nil {
		return deploymentManifest{}, err
	}
	modulePath, goVersion, err := normalizeApplicationModule(root, filepath.Join(applicationRoot, "go.mod"))
	if err != nil {
		return deploymentManifest{}, err
	}
	var requires []moduleRequirement
	if options.target == targetGoogleCloudRunFunctions {
		requires = append(requires, moduleRequirement{path: "github.com/GoogleCloudPlatform/functions-framework-go", version: functionsFrameworkVersion})
	}
	if err := writeFunctionModule(stage, modulePath, goVersion, filepath.Join(applicationRoot, "go.mod"), requires); err != nil {
		return deploymentManifest{}, err
	}
	for _, name := range []string{"go.sum", "config.prod.toml"} {
		source := filepath.Join(applicationRoot, name)
		if info, err := os.Stat(source); err == nil {
			if err := copyDeploymentFile(source, filepath.Join(stage, name), info.Mode()); err != nil {
				return deploymentManifest{}, err
			}
		} else if name == "config.prod.toml" {
			return deploymentManifest{}, fmt.Errorf("deployment requires config.prod.toml: %w", err)
		}
	}
	// Beside the configuration, for the same reason: both are resolved against
	// the working directory the function runs with, which is this stage rather
	// than the application subtree copyProjectForFunction wrote.
	if err := copyExternalAssets(root, stage); err != nil {
		return deploymentManifest{}, fmt.Errorf("copy %s: %w", externalPublicDir, err)
	}
	packageDir, packageName := stage, "function"
	entrypoint := "PopcornWeb"
	if options.target == targetVercelGo {
		packageDir, packageName = filepath.Join(stage, "api"), "handler"
		entrypoint = "Handler"
		if err := os.MkdirAll(packageDir, 0o755); err != nil {
			return deploymentManifest{}, err
		}
	}
	if err := copyTransformedMain(root, packageDir, packageName, config.Main, options.backend); err != nil {
		return deploymentManifest{}, err
	}
	wrapper, err := format.Source([]byte(sourceFunctionWrapper(packageName, options.target, options.backend)))
	if err != nil {
		return deploymentManifest{}, fmt.Errorf("format generated function entrypoint: %w", err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "popcornweb_serverless.go"), wrapper, 0o644); err != nil {
		return deploymentManifest{}, err
	}
	if options.target == targetVercelGo {
		flags := "-mod=vendor"
		if options.backend == backendFastHTTP {
			flags += " -tags fasthttp"
		}
		vercel := "{\n  \"$schema\": \"https://openapi.vercel.sh/vercel.json\",\n  \"build\": {\n    \"env\": {\n      \"GO_BUILD_FLAGS\": " + fmt.Sprintf("%q", flags) + "\n    }\n  }\n}\n"
		if err := os.WriteFile(filepath.Join(stage, "vercel.json"), []byte(vercel), 0o644); err != nil {
			return deploymentManifest{}, err
		}
	}
	tidy := exec.CommandContext(ctx, "go", "mod", "tidy")
	tidy.Dir, tidy.Stdout, tidy.Stderr, tidy.Env = stage, stdout, stderr, os.Environ()
	if err := tidy.Run(); err != nil {
		return deploymentManifest{}, fmt.Errorf("resolve staged function module: %w", err)
	}
	vendor := exec.CommandContext(ctx, "go", "mod", "vendor")
	vendor.Dir, vendor.Stdout, vendor.Stderr, vendor.Env = stage, stdout, stderr, os.Environ()
	if err := vendor.Run(); err != nil {
		return deploymentManifest{}, fmt.Errorf("vendor staged function module: %w", err)
	}

	progressArgs := []string{"test", "-mod=vendor"}
	progressArgs = append(progressArgs, options.tags()...)
	if options.target == targetGoogleCloudRunFunctions {
		progressArgs = append(progressArgs, ".")
	} else {
		progressArgs = append(progressArgs, "./api")
	}
	command := exec.CommandContext(ctx, "go", progressArgs...)
	command.Dir, command.Stdout, command.Stderr = stage, stdout, stderr
	command.Env = os.Environ()
	if err := command.Run(); err != nil {
		return deploymentManifest{}, fmt.Errorf("compile staged function: %w", err)
	}
	return deploymentManifest{Target: options.target, Backend: options.backend, Artifact: ".", Entrypoint: entrypoint}, nil
}

func copyProjectForFunction(root, stage string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." {
			return err
		}
		first := strings.Split(relative, string(filepath.Separator))[0]
		// externalPublicDir is skipped here and placed at the stage root
		// instead: the server resolves it against the working directory, which
		// is the stage rather than the application subtree. Copying it here as
		// well would put the largest files in the project into the bundle
		// twice, in a place nothing reads.
		if entry.IsDir() && (first == ".git" || first == ".pw" || first == ".devbox" || first == ".log" ||
			first == "node_modules" || first == "cmd" || first == externalPublicDir) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(stage, relative), 0o755)
		}
		base := filepath.Base(relative)
		if strings.HasSuffix(relative, ".db") || strings.HasSuffix(relative, "_test.go") ||
			strings.HasPrefix(base, ".env") ||
			(strings.HasPrefix(base, "config.") && strings.HasSuffix(base, ".toml") && base != "config.prod.toml") ||
			base == "devidp.toml" || base == "local.settings.json" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return copyDeploymentFile(path, filepath.Join(stage, relative), info.Mode())
	})
}

// copyExternalAssets carries the tree that is not in the binary into the stage
// root, beside the configuration file, because both are resolved against the
// working directory the function runs with.
//
// This is the one place the external tree is copied, and it is a deployment
// rather than a build: it happens once per artifact instead of once per
// compile, which is the cost requirement:external-public-assets was avoiding.
//
// The destination is created even when the project has no such tree, so an
// artifact always has the directory a container COPY or a function bundle
// expects to find.
func copyExternalAssets(root, stage string) error {
	destination := filepath.Join(stage, externalPublicDir)
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	source := filepath.Join(root, externalPublicDir)
	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(destination, relative), 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return copyDeploymentFile(path, filepath.Join(destination, relative), info.Mode())
	})
}

func copyDeploymentFile(source, destination string, mode fs.FileMode) error {
	if !mode.IsRegular() {
		return nil
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func copyTransformedMain(root, destination, packageName, mainPackage, backend string) error {
	directory := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(mainPackage, "./")))
	context := build.Default
	if backend == backendFastHTTP {
		context.BuildTags = []string{backendFastHTTP}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read main package: %w", err)
	}
	foundMain, foundRun := false, false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		matches, err := context.MatchFile(directory, entry.Name())
		if err != nil || !matches {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		file.Name.Name = packageName
		runtimePath, runtimeName := "github.com/shibukawa/popcornweb/pw", "pw"
		if backend == backendFastHTTP {
			runtimePath, runtimeName = "github.com/shibukawa/popcornweb/pwfast", "pwfast"
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil || path != runtimePath {
				continue
			}
			if imported.Name != nil && imported.Name.Name != "_" && imported.Name.Name != "." {
				runtimeName = imported.Name.Name
			}
		}
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil && function.Name.Name == "main" {
				function.Name.Name = "initializeApplication"
				foundMain = true
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Run" {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if !ok || identifier.Name != runtimeName {
				return true
			}
			call.Args = append([]ast.Expr{call.Fun}, call.Args...)
			call.Fun = ast.NewIdent("captureApplication")
			foundRun = true
			return true
		})
		var formatted bytes.Buffer
		if err := format.Node(&formatted, set, file); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, entry.Name()), formatted.Bytes(), 0o644); err != nil {
			return err
		}
	}
	if !foundMain || !foundRun {
		return fmt.Errorf("%s must contain one main function that starts the selected backend with Run", mainPackage)
	}
	return nil
}

func sourceFunctionWrapper(packageName, target, backend string) string {
	frameworkImport, registration := "", ""
	if target == targetGoogleCloudRunFunctions {
		frameworkImport = "\n\t\"github.com/GoogleCloudPlatform/functions-framework-go/functions\""
		registration = "\nfunc init() { functions.HTTP(\"PopcornWeb\", Handler) }\n"
	}
	capture := `func captureApplication(_ func(context.Context, http.Handler, ...pw.Option) error, ctx context.Context, handler http.Handler, options ...pw.Option) error {
	wrapped, err := pw.Middlewares(handler, options...)
	if err == nil { applicationHandler = wrapped }
	return err
}`
	backendImports := "\n\t\"github.com/shibukawa/popcornweb/pw\""
	if backend == backendFastHTTP {
		backendImports = "\n\t\"github.com/shibukawa/popcornweb/pwfast\"\n\t\"github.com/shibukawa/tinygodriver/fasthttp\""
		capture = `func captureApplication(_ func(context.Context, fasthttp.RequestHandler, ...pwfast.Option) error, ctx context.Context, handler fasthttp.RequestHandler, options ...pwfast.Option) error {
	wrapped, _, err := pwfast.Start(ctx, handler, options...)
	if err == nil { applicationHandler = pwfast.NetHTTPHandler(wrapped) }
	return err
}`
	}
	return "// Code generated by pw build --target=" + target + ". DO NOT EDIT.\n\npackage " + packageName + `

import (
	"context"
	"net/http"
	"os"
	"sync"` + frameworkImport + backendImports + `
)

var (
	applicationOnce sync.Once
	applicationHandler http.Handler
)

` + capture + `

func Handler(w http.ResponseWriter, r *http.Request) {
	applicationOnce.Do(func() {
		if os.Getenv("APP_ENV") == "" { _ = os.Setenv("APP_ENV", "prod") }
		initializeApplication()
	})
	if applicationHandler == nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	applicationHandler.ServeHTTP(w, r)
}
` + registration
}

func normalizeApplicationModule(root, path string) (string, string, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	module, err := modfile.Parse(path, source, nil)
	if err != nil {
		return "", "", err
	}
	if module.Module == nil {
		return "", "", fmt.Errorf("go.mod has no module directive")
	}
	for _, replacement := range append([]*modfile.Replace(nil), module.Replace...) {
		if replacement.New.Version != "" || filepath.IsAbs(replacement.New.Path) {
			continue
		}
		absolute, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(replacement.New.Path)))
		if err != nil {
			return "", "", err
		}
		if err := module.AddReplace(replacement.Old.Path, replacement.Old.Version, filepath.ToSlash(absolute), ""); err != nil {
			return "", "", err
		}
	}
	formatted, err := module.Format()
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return "", "", err
	}
	goVersion := "1.26.0"
	if module.Go != nil && module.Go.Version != "" {
		goVersion = module.Go.Version
	}
	return module.Module.Mod.Path, goVersion, nil
}

// moduleRequirement is one provider library the staged module pins beside the
// application, per requirement:serverless-source-entrypoints dependencies.
type moduleRequirement struct {
	path, version string
}

func writeFunctionModule(stage, applicationModule, goVersion, applicationGoMod string, requires []moduleRequirement) error {
	source := "module " + applicationModule + "/popcornweb-serverless\n\ngo " + goVersion + "\n\nrequire " + applicationModule + " v0.0.0\n"
	for _, requirement := range requires {
		source += "require " + requirement.path + " " + requirement.version + "\n"
	}
	source += "\nreplace " + applicationModule + " => ./app\n"
	module, err := modfile.Parse("go.mod", []byte(source), nil)
	if err != nil {
		return err
	}
	applicationSource, err := os.ReadFile(applicationGoMod)
	if err != nil {
		return err
	}
	application, err := modfile.Parse(applicationGoMod, applicationSource, nil)
	if err != nil {
		return err
	}
	for _, replacement := range application.Replace {
		if err := module.AddReplace(replacement.Old.Path, replacement.Old.Version, replacement.New.Path, replacement.New.Version); err != nil {
			return err
		}
	}
	formatted, err := module.Format()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stage, "go.mod"), formatted, 0o644)
}
