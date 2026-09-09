package pwcli

import (
	"strings"
	"testing"
)

func containerOptions() initOptions {
	return initOptions{Name: "myapp", Devbox: true, Auth: authNone, Router: routerRegistered}
}

// Every application gets a recipe, without a question, because the Dockerfile a
// Go reader would write does not work here. The TinyGo file is the one that
// follows an answer: a project that never selected the toolchain has no use for
// a second recipe it cannot build.
func TestEveryApplicationIsScaffoldedWithAContainerRecipe(t *testing.T) {
	hosted := scaffoldFiles(containerOptions())
	for _, path := range []string{"Dockerfile", ".dockerignore", "config.prod.toml"} {
		if hosted[path] == "" {
			t.Errorf("%s is missing from a scaffolded project", path)
		}
	}
	if _, ok := hosted["Dockerfile.tinygo"]; ok {
		t.Error("a host Go project was given a TinyGo recipe it cannot build")
	}

	tiny := containerOptions()
	tiny.TinyGo = true
	if scaffoldFiles(tiny)["Dockerfile.tinygo"] == "" {
		t.Error("a TinyGo project has no Dockerfile.tinygo")
	}
}

// A package produces no binary, so there is no image to build and no
// environment to configure. Committing generated artifacts is also the one rule
// a package inverts, and the .dockerignore below excludes exactly those.
func TestAPackageGetsNoContainerFiles(t *testing.T) {
	options := containerOptions()
	options.Kind = kindPackage
	files := scaffoldFiles(options)
	for _, path := range []string{"Dockerfile", "Dockerfile.tinygo", ".dockerignore", "config.prod.toml"} {
		if _, ok := files[path]; ok {
			t.Errorf("a package project was given %s", path)
		}
	}
}

// The host phase is the whole reason these files are scaffolded. A recipe that
// reached the compiler without it would fail on symbols whose sources are in
// the tree, which is the confusing failure the scaffold exists to prevent.
func TestBothRecipesRunTheHostPhaseBeforeAnyCompiler(t *testing.T) {
	options := containerOptions()
	options.TinyGo = true
	files := scaffoldFiles(options)

	hosted := files["Dockerfile"]
	if !strings.Contains(hosted, "pw build") {
		t.Error("the host Go recipe does not run pw build")
	}
	if !strings.Contains(hosted, "CGO_ENABLED=0") {
		t.Error("the binary would not be static enough for the distroless base")
	}

	tiny := files["Dockerfile.tinygo"]
	// Unflagged: pw generate --code-only would write the generated Go and no
	// dist/public, and the compiler below reads a go:embed over that directory.
	generate := strings.Index(tiny, "RUN pw generate\n")
	compile := strings.Index(tiny, "tinygo build")
	if generate < 0 || compile < 0 {
		t.Fatalf("the TinyGo recipe is missing a step: generate=%d compile=%d", generate, compile)
	}
	if generate > compile {
		t.Error("the TinyGo recipe compiles before it writes the tree")
	}
	// Under the cooperative scheduler a blocking socket call holds the runtime,
	// so a driver's cancellation watcher never runs. It is harmless for an
	// engine that needs no socket, so the recipe passes it unconditionally
	// rather than deriving it from an answer the Dockerfile cannot see change.
	if !strings.Contains(tiny, "-scheduler=threads") {
		t.Error("the TinyGo recipe omits -scheduler=threads")
	}
}

// The probe is the binary itself, so the path Docker invokes and the path the
// image installs have to be the same one. They are written separately, which is
// exactly how they drift.
func TestTheHealthcheckInvokesTheBinaryTheImageInstalls(t *testing.T) {
	options := containerOptions()
	options.TinyGo = true
	for path, recipe := range scaffoldFiles(options) {
		if !strings.HasPrefix(path, "Dockerfile") {
			continue
		}
		if !strings.Contains(recipe, `CMD ["/app/myapp", "healthcheck"]`) {
			t.Errorf("%s: the probe does not call the application binary", path)
		}
		if !strings.Contains(recipe, `ENTRYPOINT ["/app/myapp"]`) {
			t.Errorf("%s: the entry point is not the binary the probe names", path)
		}
		// An unset APP_ENV resolves to dev, which would look for a file the
		// image does not carry and start on development defaults instead.
		if !strings.Contains(recipe, "ENV APP_ENV=prod") {
			t.Errorf("%s: the image does not select the production environment", path)
		}
	}
}

// devbox.json does not reach inside an image, so the pin is repeated in the
// Dockerfile. The two going out of step is a difference between the CSS a
// developer sees and the CSS the deployment serves.
func TestTailwindReachesTheBuilderStageAtThePinnedVersion(t *testing.T) {
	plain := scaffoldFiles(containerOptions())["Dockerfile"]
	if strings.Contains(plain, "tailwindcss") {
		t.Error("a project without Tailwind installs the CSS toolchain anyway")
	}

	options := containerOptions()
	options.Tailwind = true
	styled := scaffoldFiles(options)["Dockerfile"]
	version := tailwindPinnedVersion()
	if version == "latest" || version == "" {
		t.Fatalf("the Devbox package %q carries no version to pin", tailwindDevboxPackage)
	}
	if !strings.Contains(styled, "tailwindcss-linux-") {
		t.Error("the builder stage does not install the standalone Tailwind executable")
	}
	if !strings.Contains(styled, "download/v"+version+"/") {
		t.Errorf("the Dockerfile does not pin Tailwind %s, which devbox.json holds", version)
	}
}

// Everything excluded is either rebuilt inside the image or belongs to this
// machine. The generated Go matters most: a host copy would be carried in and
// then either overwritten or, if generation skipped it, linked.
func TestDockerignoreExcludesWhatTheImageRebuilds(t *testing.T) {
	ignore := scaffoldFiles(containerOptions())[".dockerignore"]
	for _, entry := range []string{"**/*_pw_gen.go", "dist/", "config.dev.toml", ".devbox/", ".env.dev\n", ".env.local\n", ".env.*.local\n"} {
		if !strings.Contains(ignore, entry) {
			t.Errorf(".dockerignore does not exclude %s", entry)
		}
	}
	// The production file is the one configuration the image needs.
	for _, line := range strings.Split(ignore, "\n") {
		if strings.TrimSpace(line) == "config.prod.toml" {
			t.Error(".dockerignore excludes the configuration the image copies")
		}
	}
}

// The file is copied into an image layer, and a layer is readable by anyone who
// can pull it. The development file's DSN and its generated keyring are exactly
// what must not follow it across.
func TestProductionConfigCarriesNoSecretAndNoDevelopmentValue(t *testing.T) {
	options := containerOptions()
	options.Database, options.Engine = true, enginePostgres
	options.Session = sessionCookie
	config := scaffoldFiles(options)["config.prod.toml"]

	// The probe reads server.health and exits 1 naming the key when it is
	// unset, so the HEALTHCHECK instruction and this key ship together.
	if !strings.Contains(config, `health = "/healthz"`) {
		t.Error("config.prod.toml does not set server.health, so the container probe cannot work")
	}
	if !strings.Contains(config, `stdout_format = "json"`) {
		t.Error("a container's logs are not machine readable")
	}
	if !strings.Contains(config, "${DATABASE_URL}") {
		t.Error("the database connection does not defer to the deployment")
	}
	for _, leaked := range []string{"127.0.0.1", "localhost", "keyring.secret"} {
		if strings.Contains(config, leaked) {
			t.Errorf("config.prod.toml carries the development value %q", leaked)
		}
	}
	// A session-carrying project cannot be finished by a scaffold, so the file
	// says which sections are still missing rather than starting the server
	// with a capability quietly switched off.
	if !strings.Contains(config, "STILL TO WRITE") || !strings.Contains(config, "session") {
		t.Error("the file does not name the sections it could not write")
	}
	if !strings.Contains(config, "--generate-config=toml") {
		t.Error("the file does not name the command that prints the full scaffold")
	}
}

// A project with nothing but a server has no section a scaffold cannot write,
// so it gets a complete file and no warning about one.
func TestAProjectWithNoStoreGetsACompleteProductionConfig(t *testing.T) {
	config := scaffoldFiles(containerOptions())["config.prod.toml"]
	if strings.Contains(config, "STILL TO WRITE") {
		t.Error("a project with no store was told a section is missing")
	}
}
