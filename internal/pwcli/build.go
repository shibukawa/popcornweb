package pwcli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// buildUsage shows the shared backend axis and build's deployment axis.
var buildUsage = "usage: pw build [--debug] [--backend nethttp|fasthttp] [--target lambda|azure-functions|google-cloud-run-functions|vercel-go|cloudflare-workers]"

// generateUsage is the other half of the same option set. The two commands share
// it because pw build is defined as pw generate plus the compiler.
var generateUsage = "usage: pw generate [--code-only] [--debug] [--backend nethttp|fasthttp]"

func runBuild(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	options, err := buildFlags("build", args)
	if err != nil {
		return err
	}
	root, config, err := buildProject("build", true)
	if err != nil {
		return err
	}
	if err := options.check(config); err != nil {
		return err
	}
	debug := options.debug
	progress := newProgressRegion(stdout)
	if err := generateBuildInputs(ctx, root, config, options, progress, stdout, stderr); err != nil {
		progress.Done()
		return err
	}
	if options.target != "" {
		err := buildDeployment(ctx, root, config, options, progress, stdout, stderr)
		progress.Done()
		return err
	}
	progress.Phase("compiling")
	// A pw build is the deployable artifact. Strip DWARF and the host linker
	// symbol table, while retaining Go's pclntab so panic stacks still carry
	// function names and line numbers. trimpath removes checkout-specific source
	// prefixes and makes otherwise identical builds reproducible across machines,
	// which is why it is passed either way: it removes no debug information.
	build := []string{"build", "-trimpath"}
	if !debug {
		build = append(build, "-ldflags=-s -w")
	}
	build = append(build, options.tags()...)
	command := exec.CommandContext(ctx, "go", append(build, config.Main)...)
	command.Dir, command.Stdout, command.Stderr, command.Env = root, stdout, stderr, os.Environ()
	err = command.Run()
	progress.Done()
	if err != nil {
		return fmt.Errorf("go build: %w", err)
	}
	return nil
}

// runGenerate is runBuild without its final compile step: it writes everything
// the compiler needs and stops. A build the framework does not drive — the
// tinygo invocation in Dockerfile.tinygo, a cross-compiled go build with the
// operator's own flags, an image builder that owns the compile step — needs the
// same tree and has nothing else to produce it with.
//
// This is the name a caller guesses for that job, which is why it has it. The
// narrower command that only wrote the generated Go used to hold it, and a tree
// prepared with that one fails to compile on a go:embed over a directory
// nothing built. It is --code-only now.
//
// Unlike runBuild it runs in a package project, because that is where a
// concept:component-package regenerates its committed artifacts.
func runGenerate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	options, err := buildFlags("generate", args)
	if err != nil {
		return err
	}
	root, config, err := buildProject("generate", false)
	if err != nil {
		return err
	}
	if err := options.check(config); err != nil {
		return err
	}
	progress := newProgressRegion(stdout)
	err = generateBuildInputs(ctx, root, config, options, progress, stdout, stderr)
	progress.Done()
	return err
}

const (
	backendNetHTTP  = "nethttp"
	backendFastHTTP = "fasthttp"

	targetLambda                  = "lambda"
	targetAzureFunctions          = "azure-functions"
	targetGoogleCloudRunFunctions = "google-cloud-run-functions"
	targetVercelGo                = "vercel-go"
	targetCloudflareWorkers       = "cloudflare-workers"
)

var deploymentTargets = map[string]bool{
	targetLambda:                  true,
	targetAzureFunctions:          true,
	targetGoogleCloudRunFunctions: true,
	targetVercelGo:                true,
	targetCloudflareWorkers:       true,
}

// buildOptions are how a build was invoked. Backend selects the HTTP
// implementation; target selects provider packaging and is build-only; codeOnly
// narrows generation to the Go it writes and is generate-only.
type buildOptions struct {
	debug    bool
	backend  string
	target   string
	codeOnly bool
}

// tags is what the compiler is told. A net/http build passes none, so its
// command line is byte for byte what it was before the second target existed.
func (o buildOptions) tags() []string {
	if o.backend != backendFastHTTP {
		return nil
	}
	return []string{"-tags", backendFastHTTP}
}

// check refuses a backend the project did not declare.
//
// Building for fasthttp without project.fasthttp = true would compile the
// authored net/http source with everything it needs tagged out — a package
// with no handlers, no binders and no route registration, which fails as a
// pile of undefined symbols rather than as the one thing that is wrong.
func (o buildOptions) check(config projectConfig) error {
	if o.backend == backendFastHTTP && !config.FastHTTP {
		return fmt.Errorf("--backend %s needs project.fasthttp = true in popcornweb.toml; "+
			"without it nothing generates the half that build compiles", backendFastHTTP)
	}
	// The Worker adapter serves an http.Handler, and the fasthttp fork has
	// not been verified under js/wasm, so the target takes the one backend
	// that was.
	if o.target == targetCloudflareWorkers && o.backend != backendNetHTTP {
		return fmt.Errorf("--target %s builds the %s backend only", targetCloudflareWorkers, backendNetHTTP)
	}
	return nil
}

// buildFlags reads the build and generate option set.
//
// It is on generate as well as on build, and that is the point rather than a
// convenience: generate exists for a compile this project does not run — the
// TinyGo Dockerfile, a cross-compiled go build, an image builder owning the
// final step — so those are deployments, and a flag only build understood would
// miss the path most likely to become production. What generate cannot carry is
// the linker half, which belongs to the compile its caller owns.
func buildFlags(command string, args []string) (buildOptions, error) {
	options := buildOptions{backend: backendNetHTTP}
	usage := buildUsage
	if command == "generate" {
		usage = generateUsage
	}
	for index := 0; index < len(args); index++ {
		switch arg := args[index]; {
		case arg == "--debug":
			options.debug = true
		case arg == "--code-only":
			options.codeOnly = true
		case arg == "--backend":
			index++
			if index >= len(args) {
				return buildOptions{}, fmt.Errorf("%s: --backend needs a value", command)
			}
			options.backend = args[index]
		case strings.HasPrefix(arg, "--backend="):
			options.backend = strings.TrimPrefix(arg, "--backend=")
		case arg == "--target":
			index++
			if index >= len(args) {
				return buildOptions{}, fmt.Errorf("%s: --target needs a value", command)
			}
			options.target = args[index]
		case strings.HasPrefix(arg, "--target="):
			options.target = strings.TrimPrefix(arg, "--target=")
		default:
			return buildOptions{}, fmt.Errorf("%s: unexpected argument %q; %s", command, arg, usage)
		}
		if options.backend != backendNetHTTP && options.backend != backendFastHTTP {
			return buildOptions{}, fmt.Errorf("%s: --backend %q is not a backend", command, options.backend)
		}
		if options.target != "" && !deploymentTargets[options.target] {
			return buildOptions{}, fmt.Errorf("%s: --target %q is not a target", command, options.target)
		}
	}
	if command == "generate" && options.target != "" {
		return buildOptions{}, fmt.Errorf("generate: --target is available only on pw build")
	}
	if command == "build" && options.codeOnly {
		return buildOptions{}, fmt.Errorf("build: --code-only is available only on pw generate, and a build needs every input")
	}
	// A flag with nothing to act on is worse than a rejected one, because the
	// caller reads the artifact as debuggable and it is not: --debug survives
	// only as source maps in the asset tree, which --code-only does not build.
	if options.codeOnly && options.debug {
		return buildOptions{}, fmt.Errorf("generate: --debug has nothing to keep with --code-only, which builds no asset tree")
	}
	return options, nil
}

// buildProject resolves the project the two commands above run in.
//
// refusePackage is what separates them. The kind is read before anything runs,
// because a build would generate successfully in a package and then fail its
// link step on a missing entry point, which is a late error about the wrong
// thing. Generation itself is the one thing a package project does want, so it
// passes false and degenerates instead, in generateBuildInputs.
func buildProject(command string, refusePackage bool) (string, projectConfig, error) {
	root, err := projectRoot(".")
	if err != nil {
		return "", projectConfig{}, err
	}
	config, err := loadProjectConfig(root)
	if err != nil {
		return "", projectConfig{}, err
	}
	if refusePackage {
		if err := refuseInPackage(config, command); err != nil {
			return "", projectConfig{}, err
		}
	}
	return root, config, nil
}

// generateBuildInputs writes everything a compiler needs that is not in version
// control: the generated Go, the production stylesheet, and the derived asset
// tree public.go embeds. It ends with the development-only import check, which
// belongs here rather than beside the compiler because pw generate hands the
// tree to a compiler it does not run.
//
// config is taken by value: the Tailwind minify override below is this
// sequence's, not the project's, and the source map decision is the same shape.
// It is a property of how the build was invoked, so it is written onto the
// config here rather than read from a file that has no way to say which
// invocation it meant.
func generateBuildInputs(ctx context.Context, root string, config projectConfig, options buildOptions, progress *progressRegion, stdout, stderr io.Writer) error {
	progress.Phase("generating")
	if _, err := generateProject(ctx, false, stdout, false); err != nil {
		return err
	}
	// A package project ends here whatever was asked for. It has no entry point
	// whose imports could be rejected, no public.go to embed a tree for, and no
	// document shell to style — so the generated Go is the whole of what a
	// compiler downstream will read, and --code-only is the shape rather than a
	// flag the author has to know to pass.
	if config.Kind == kindPackage {
		return nil
	}
	// --code-only keeps the import rejection. The steps it skips are the ones
	// that write files; a flag must not also be the way past a check that keeps
	// an identity provider signing users in without a password out of a
	// deployable binary. Its cost is a dependency-graph listing, not a compile.
	if options.codeOnly {
		return rejectDevelopmentImports(ctx, root, config.Main, options)
	}
	if config.Tailwind.Enabled {
		progress.Phase("building CSS")
		config.Tailwind.Minify = true
		if err := buildTailwind(ctx, root, config.Tailwind, stdout, stderr); err != nil {
			return err
		}
	}
	progress.Phase("building assets")
	config.Assets.SourceMaps = options.debug
	report, err := buildDerivedAssets(root, config.Assets)
	if err != nil {
		return err
	}
	reportDerivedAssets(stdout, report)
	return rejectDevelopmentImports(ctx, root, config.Main, options)
}

// developmentOnlyPackages must never reach a built application. Each one is a
// security defect in a deployable binary rather than a configuration mistake:
// the development identity provider authenticates nobody, the virtual
// authenticator holds a signing key that mints assertions a relying party
// accepts, and the authentication test seam builds a request context that is
// already logged in.
var developmentOnlyPackages = []string{
	"github.com/shibukawa/popcornweb/contrib/devidp",
	"github.com/shibukawa/popcornweb/contrib/passkey/passkeytest",
	"github.com/shibukawa/popcornweb/plugin/auth/authtest",
}

// The graph is listed under the same tags the compile uses. A development-only
// package reached from the second transport's half would be invisible to a list
// taken without them, which is the one build this check would then miss.
func rejectDevelopmentImports(ctx context.Context, root, mainPackage string, options buildOptions) error {
	arguments := append([]string{"list", "-deps", "-f", "{{.ImportPath}}"}, options.tags()...)
	command := exec.CommandContext(ctx, "go", append(arguments, mainPackage)...)
	command.Dir, command.Env = root, os.Environ()
	output, err := command.Output()
	if err != nil {
		// A dependency graph that cannot be listed fails the build below with
		// a compiler diagnostic, which is more useful than this check's error.
		return nil
	}
	for _, line := range strings.Split(string(output), "\n") {
		imported := strings.TrimSpace(line)
		for _, forbidden := range developmentOnlyPackages {
			if imported == forbidden {
				// The command is not named: this check runs for pw build and for
				// pw generate, and the sentence is about the import either way.
				return fmt.Errorf("%s imports %s, which is development-only and must not ship in an application", mainPackage, forbidden)
			}
		}
	}
	return nil
}
