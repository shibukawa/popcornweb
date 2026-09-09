package pwcli

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureSources is the block every project fixture needs, because every
// generation purpose is required and none has a default.
const fixtureSources = `
[generate]
handlers = ["handlers"]
templates = ["handlers"]
queries = []
config = []
`

// writeProjectFixture writes popcornweb.toml with a valid generation source
// list appended, and creates the directory that list names.
func writeProjectFixture(t *testing.T, root, config string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "handlers"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "popcornweb.toml"), config+fixtureSources)
}

func TestLoadProjectConfigTailwind(t *testing.T) {
	root := t.TempDir()
	writeProjectFixture(t, root, `[project]
name = "fixture"
main = "./cmd/fixture"

[assets.tailwind]
enabled = true
input = "assets/app.css"
output = "internal/static/app.css"
minify = true
`)
	config, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if !config.Tailwind.Enabled || !config.Tailwind.Minify ||
		config.Tailwind.Input != "assets/app.css" ||
		config.Tailwind.Output != "internal/static/app.css" {
		t.Fatalf("unexpected Tailwind config: %#v", config.Tailwind)
	}
}

func TestLoadProjectConfigWatchPaths(t *testing.T) {
	root := t.TempDir()
	writeProjectFixture(t, root, `[project]
name = "fixture"
main = "."

[dev.watch]
includes = ["locales/*.json", "schema.graphql"]
excludes = ["./web/node_modules/"]
`)
	config, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(config.Watch.Includes, ",") != "locales/*.json,schema.graphql" {
		t.Fatalf("watch includes = %#v", config.Watch.Includes)
	}
	if strings.Join(config.Watch.Excludes, ",") != "web/node_modules" {
		t.Fatalf("watch excludes = %#v", config.Watch.Excludes)
	}
}

func TestLoadProjectConfigDevLogs(t *testing.T) {
	for _, testcase := range []struct {
		name      string
		section   string
		enabled   bool
		directory string
	}{
		{name: "defaults", enabled: true, directory: defaultDevLogDir},
		{name: "configured", section: "\n[dev.logs]\nenabled = false\ndirectory = \"tmp/logs\"\n", directory: "tmp/logs"},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root := t.TempDir()
			writeProjectFixture(t, root, "[project]\nname = \"fixture\"\nmain = \".\"\n"+testcase.section)
			config, err := loadProjectConfig(root)
			if err != nil {
				t.Fatal(err)
			}
			if config.Logs.Enabled != testcase.enabled || config.Logs.Directory != testcase.directory {
				t.Fatalf("logs = %#v", config.Logs)
			}
		})
	}
}

func TestLoadProjectConfigRejectsDevLogDirectoryOutsideProject(t *testing.T) {
	for _, directory := range []string{".", "..", "../shared", "/tmp/logs"} {
		t.Run(strings.ReplaceAll(directory, "/", "_"), func(t *testing.T) {
			root := t.TempDir()
			writeProjectFixture(t, root, "[project]\nname = \"fixture\"\nmain = \".\"\n\n[dev.logs]\ndirectory = \""+directory+"\"\n")
			if _, err := loadProjectConfig(root); err == nil || !strings.Contains(err.Error(), "dev.logs.directory") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestLoadProjectConfigToolchain(t *testing.T) {
	for _, testcase := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "absent defaults to TinyGo", want: toolchainTinyGo},
		{name: "explicit host Go", value: "\ntoolchain = \"go\"", want: toolchainGo},
		{name: "explicit TinyGo", value: "\ntoolchain = \"tinygo\"", want: toolchainTinyGo},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root := t.TempDir()
			writeProjectFixture(t, root, "[project]\nname = \"fixture\"\nmain = \".\""+testcase.value+"\n")
			config, err := loadProjectConfig(root)
			if err != nil {
				t.Fatal(err)
			}
			if config.Toolchain != testcase.want {
				t.Fatalf("toolchain = %q, want %q", config.Toolchain, testcase.want)
			}
		})
	}
}

func TestLoadProjectConfigRejectsUnknownToolchain(t *testing.T) {
	root := t.TempDir()
	writeProjectFixture(t, root, `[project]
name = "fixture"
main = "."
toolchain = "rust"
`)
	_, err := loadProjectConfig(root)
	if err == nil || !strings.Contains(err.Error(), "project.toolchain must be") {
		t.Fatalf("expected toolchain error, got %v", err)
	}
}

func TestLoadProjectConfigRejectsUnknownKeys(t *testing.T) {
	root := t.TempDir()
	writeProjectFixture(t, root, `[project]
name = "fixture"
main = "."
server.port = 8080
`)
	_, err := loadProjectConfig(root)
	if err == nil || !strings.Contains(err.Error(), "unknown key project.server.port") {
		t.Fatalf("expected unknown-key error, got %v", err)
	}
}

func TestLoadProjectConfigDefaultsEnabledTailwindPaths(t *testing.T) {
	root := t.TempDir()
	writeProjectFixture(t, root, `[project]
name = "fixture"
main = "."

[assets.tailwind]
enabled = true
`)
	config, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if config.Tailwind.Input != defaultTailwindInput || config.Tailwind.Output != defaultTailwindOutput {
		t.Fatalf("unexpected Tailwind defaults: %#v", config.Tailwind)
	}
}

func TestScaffoldFilesWithTailwind(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "fixture", TinyGo: true, Tailwind: true, Devbox: true, Database: true, Redis: true})
	for _, name := range []string{
		"assets/app.css",
		"public/generated/app.css",
		"templates/document.pw.html",
		"templates/400.pw.html",
		"templates/401.pw.html",
		"templates/403.pw.html",
		"templates/404.pw.html",
		"templates/409.pw.html",
		"templates/413.pw.html",
		"templates/429.pw.html",
		"templates/500.pw.html",
		"migrations/00001_init.sql",
		".vscode/settings.json",
	} {
		if _, ok := files[name]; !ok {
			t.Errorf("missing Tailwind scaffold file %s", name)
		}
	}
	if !strings.Contains(files[".vscode/settings.json"], `"**/*_pw_gen.go": true`) {
		t.Fatal("VS Code settings do not hide generated Go files")
	}
	if strings.Contains(files["popcornweb.toml"], "extra_watch") {
		t.Fatal("project scaffold contains the obsolete dev.extra_watch key")
	}
	for _, purpose := range []string{
		"handlers = [\"handlers\"]",
		"templates = [\"handlers\", \"templates\"]",
		"queries = [\"queries\"]",
		"config = [\"cmd/fixture\"]",
	} {
		if !strings.Contains(files["popcornweb.toml"], purpose) {
			t.Fatalf("project scaffold does not state %s:\n%s", purpose, files["popcornweb.toml"])
		}
	}
	if !strings.Contains(files[".gitignore"], "\n*_pw_gen.go\n") {
		t.Fatal(".gitignore does not exclude generated Go files")
	}
	if !strings.Contains(files[".gitignore"], "\n.log/\n") {
		t.Fatal(".gitignore does not exclude local JSONL logs")
	}
	// The local member of each dotenv pair holds this machine's secrets;
	// .env, .env.{env}, and the template are committed.
	if !strings.Contains(files[".gitignore"], "\n.env.local\n.env.*.local\n") {
		t.Fatalf(".gitignore does not exclude the local dotenv files:\n%s", files[".gitignore"])
	}
	for _, line := range strings.Split(files[".gitignore"], "\n") {
		if line == ".env" || line == ".env.*" {
			t.Fatalf(".gitignore excludes the committed dotenv files: %q", line)
		}
	}
	// The Tailwind stylesheet this preset configures and the component assets the
	// generator extracts land in one directory, and pw generate rebuilds both from
	// sources this scaffold also writes. A project that starts without the line
	// commits a stylesheet that then drifts from the templates it was scanned
	// from, per decision:generated-public-asset-version-control.
	if !strings.Contains(files[".gitignore"], "\n"+extractedAssetDir+"/\n") {
		t.Fatalf(".gitignore does not exclude the generated public assets:\n%s", files[".gitignore"])
	}
	// The line has to cover the configured Tailwind output, not merely resemble
	// it; the two constants are set in different files and nothing else pairs them.
	if !strings.HasPrefix(defaultTailwindOutput, extractedAssetDir+"/") {
		t.Fatalf("Tailwind writes %q, which the %q ignore rule does not cover", defaultTailwindOutput, extractedAssetDir)
	}
	if !strings.Contains(files["popcornweb.toml"], "[dev.logs]\nenabled = true\ndirectory = \".log\"") {
		t.Fatal("project scaffold does not state local log defaults")
	}
	for name, want := range map[string]string{
		"popcornweb.toml": "[dev.watch]\nincludes = []\nexcludes = []",
		"devbox.json":     "tailwindcss_4@4.1.18",
		// Named through AssetURL rather than as a literal, so the Tailwind
		// output is served under a revision segment and cached rather than
		// revalidated on every page load.
		"templates/document.pw.html": `href='{AssetURL("generated/app.css")}'`,
	} {
		if !strings.Contains(files[name], want) {
			t.Errorf("%s does not contain %q:\n%s", name, want, files[name])
		}
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "handlers/index.go", files["handlers/index.go"], parser.AllErrors); err != nil {
		t.Fatalf("Tailwind handler scaffold is invalid Go: %v\n%s", err, files["handlers/index.go"])
	}
	if !strings.Contains(files["templates/document.pw.html"], "<slot/>") {
		t.Fatal("document scaffold is missing its body slot")
	}
	if strings.Contains(files["handlers/home.pw.html"], "<!doctype") {
		t.Fatal("page scaffold duplicates the document shell")
	}
	if strings.Contains(files["handlers/home_handler.go"], "tinybind-go") ||
		strings.Contains(files["handlers/home_handler.go"], "HTMLWrapper") ||
		!strings.Contains(files["handlers/home_handler.go"], "pw.WriteHTML(w, r,") {
		t.Fatal("classic handler must use implicit document rendering")
	}
	plain := scaffoldFiles(initOptions{Name: "fixture", TinyGo: true, Devbox: true, Database: true, Redis: true})
	if strings.Contains(plain["devbox.json"], "tailwindcss") {
		t.Fatal("plain scaffold unexpectedly installs Tailwind")
	}
	if _, ok := plain["assets/app.css"]; ok {
		t.Fatal("plain scaffold unexpectedly contains a Tailwind entry")
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "handlers/index.go", plain["handlers/index.go"], parser.AllErrors); err != nil {
		t.Fatalf("plain handler scaffold is invalid Go: %v\n%s", err, plain["handlers/index.go"])
	}
}

// The one generated tree that has to be committed, because nothing regenerates
// it. Devbox writes a service's configuration on the single run that resolves
// the package and stamps plugin_version into devbox.lock, and never again — so
// an ignored devbox.d beside a committed devbox.lock means the author's clone is
// the only one where the service starts. Both scaffolds are checked, because the
// package one was written as a copy of the application one and inherited the rule
// the first time round.
func TestScaffoldsTrackDevboxServiceConfiguration(t *testing.T) {
	for name, ignore := range map[string]string{
		"application": scaffoldFiles(initOptions{Name: "fixture", Devbox: true, Redis: true})[".gitignore"],
		"package":     packageGitignore(),
	} {
		if strings.Contains(ignore, "devbox.d") {
			t.Errorf("the %s scaffold excludes devbox.d, so a clone cannot start the services devbox.lock pins:\n%s", name, ignore)
		}
	}
}

// The generated public assets invert between the two scaffolds, and the risk runs
// the opposite way from the devbox one above: an application that keeps them
// commits a stylesheet that drifts from the templates it was scanned from, while
// a package that excludes them publishes components whose styles and scripts the
// consumer links and cannot rebuild. Both directions are asserted here because
// the package scaffold began as a copy of the application one, which is how it
// inherited a rule it should not have had once already.
func TestGeneratedPublicAssetsInvertBetweenScaffolds(t *testing.T) {
	application := scaffoldFiles(initOptions{Name: "fixture", Devbox: true, Tailwind: true})[".gitignore"]
	if !strings.Contains(application, "\n"+extractedAssetDir+"/\n") {
		t.Errorf("the application scaffold tracks %s, which pw generate rebuilds:\n%s", extractedAssetDir, application)
	}
	if ignore := packageGitignore(); strings.Contains(ignore, extractedAssetDir) {
		t.Errorf("the package scaffold excludes %s, so a consumer links component assets nothing ships:\n%s", extractedAssetDir, ignore)
	}
}

func TestMainInitUsageMentionsTailwind(t *testing.T) {
	var output strings.Builder
	code := Main([]string{"init"}, &output, &output)
	if code != 1 || !strings.Contains(output.String(), "[--tailwind]") {
		t.Fatalf("unexpected result code=%d output=%q", code, output.String())
	}
}

func TestScaffoldTailwindUsesGeneratedPublicDirectory(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "fixture", TinyGo: true, Tailwind: true, Devbox: true, Database: true, Redis: true})
	if _, ok := files["internal/static/static.go"]; ok {
		t.Fatal("Tailwind scaffold unexpectedly creates its former private static package")
	}
	if _, ok := files["public/generated/app.css"]; !ok {
		t.Fatal("Tailwind scaffold is missing its generated public stylesheet")
	}
}

func TestTailwindWatchPathsSkipPublicOutput(t *testing.T) {
	root := t.TempDir()
	config := tailwindConfig{
		Enabled: true,
		Input:   "assets/app.css",
		Output:  "public/generated/app.css",
	}
	if paths := tailwindWatchPaths(root, config, false); len(paths) != 0 {
		t.Fatalf("public Tailwind output should not restart the application: %v", paths)
	}
	paths := tailwindWatchPaths(root, config, true)
	if len(paths) != 1 || paths[0] != filepath.Join(root, "assets", "app.css") {
		t.Fatalf("watch recovery should include only the Tailwind input: %v", paths)
	}

	config.Output = "internal/static/app.css"
	paths = tailwindWatchPaths(root, config, false)
	if len(paths) != 1 || paths[0] != filepath.Join(root, "internal", "static", "app.css") {
		t.Fatalf("custom output should restart the application: %v", paths)
	}
}

func TestSnapshotWatchFilesFollowsSourcesAndAssetsButNotTheBuiltTree(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "app.go"), "package fixture\n")
	writeTestFile(t, filepath.Join(root, "public.go"), "package fixture\n")
	writeProjectFixture(t, root, "[project]\n")
	writeTestFile(t, filepath.Join(root, "config.dev.toml"), "[server]\n")
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "config", "config.stg.toml"), "[server]\n")
	writeTestFile(t, filepath.Join(root, "config.toml"), "[server]\n")
	if err := os.MkdirAll(filepath.Join(root, "public", "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "public", "generated", "asset.go"), "static content\n")
	// The built tree is what the loop produces, so watching it would make every
	// rebuild trigger the next one.
	if err := os.MkdirAll(filepath.Join(root, "dist", "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "dist", "public", "app.css"), "body{}\n")

	state, err := snapshotWatchFiles(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state[filepath.Join(root, "app.go")]; !ok {
		t.Fatal("ordinary Go source is missing from watch state")
	}
	for _, included := range []string{
		filepath.Join(root, "public.go"),
		filepath.Join(root, "popcornweb.toml"),
		filepath.Join(root, "config.dev.toml"),
		filepath.Join(root, "config", "config.stg.toml"),
	} {
		if _, ok := state[included]; !ok {
			t.Errorf("default watch path is missing: %s", included)
		}
	}
	// An authored asset is a build input: editing one has to rebuild the served
	// tree, which is why the walk follows public rather than skipping it.
	if _, ok := state[filepath.Join(root, "public", "generated", "asset.go")]; !ok {
		t.Error("an authored asset is missing from watch state")
	}
	for _, ignored := range []string{
		filepath.Join(root, "config.toml"),
		filepath.Join(root, "dist", "public", "app.css"),
	} {
		if _, ok := state[ignored]; ok {
			t.Errorf("path unexpectedly triggers a rebuild: %s", ignored)
		}
	}
}

func TestConfiguredWatchPathsExpandsExtraGlobs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "locales"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "locales", "en.json"), "{}")
	paths := configuredWatchPaths(root, []string{"locales/*.json", "schema.graphql"}, nil)
	want := []string{
		filepath.Join(root, "locales", "en.json"),
		filepath.Join(root, "schema.graphql"),
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}

// The scan range is the one thing a reader should not have to infer, so the key
// is required rather than falling back to a walk of the whole project.
func TestLoadProjectConfigRequiresEveryGeneratePurpose(t *testing.T) {
	for _, missing := range []string{"handlers", "templates", "queries", "config"} {
		t.Run(missing, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "handlers"), 0o755); err != nil {
				t.Fatal(err)
			}
			block := "\n[generate]\n"
			for _, purpose := range []string{"handlers", "templates", "queries", "config"} {
				if purpose == missing {
					continue
				}
				block += purpose + " = []\n"
			}
			writeTestFile(t, filepath.Join(root, "popcornweb.toml"),
				"[project]\nname = \"fixture\"\nmain = \".\"\n"+block)
			_, err := loadProjectConfig(root)
			if err == nil || !strings.Contains(err.Error(), "generate."+missing+" is required") {
				t.Fatalf("err = %v, want one naming generate.%s", err, missing)
			}
		})
	}
}

// An empty list is how a project says a purpose generates nothing, which a
// missing key cannot express.
func TestLoadProjectConfigAcceptsAnEmptyGeneratePurpose(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "handlers"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "popcornweb.toml"),
		"[project]\nname = \"fixture\"\nmain = \".\"\n\n[generate]\n"+
			"handlers = [\"handlers\"]\ntemplates = []\nqueries = []\nconfig = []\n")
	config, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Generate.Handlers) != 1 || len(config.Generate.Queries) != 0 {
		t.Fatalf("scope = %#v", config.Generate)
	}
}

func TestLoadProjectConfigRejectsUnusableGenerateSources(t *testing.T) {
	for _, testcase := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "absolute", value: `["/srv/handlers"]`, want: "must be relative"},
		{name: "escaping", value: `["../shared"]`, want: "inside the project"},
		{name: "project root", value: `["."]`, want: "inside the project"},
		{name: "duplicate", value: `["handlers", "handlers"]`, want: "twice"},
		{name: "nested", value: `["handlers", "handlers/admin"]`, want: "already covered by"},
		{name: "missing directory", value: `["queries"]`, want: "queries"},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root := t.TempDir()
			for _, directory := range []string{"handlers", filepath.Join("handlers", "admin")} {
				if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			writeTestFile(t, filepath.Join(root, "popcornweb.toml"),
				"[project]\nname = \"fixture\"\nmain = \".\"\n\n[generate]\nhandlers = "+testcase.value+
					"\ntemplates = []\nqueries = []\nconfig = []\n")
			_, err := loadProjectConfig(root)
			if err == nil || !strings.Contains(err.Error(), testcase.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, testcase.want)
			}
		})
	}
}

func TestLoadProjectConfigNormalizesGenerateSources(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"templates", "handlers"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(root, "popcornweb.toml"),
		"[project]\nname = \"fixture\"\nmain = \".\"\n\n[generate]\n"+
			"handlers = [\"templates\", \"./handlers/\"]\ntemplates = []\nqueries = []\nconfig = []\n")
	config, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(config.Generate.Handlers, ",") != "handlers,templates" {
		t.Fatalf("handlers = %#v", config.Generate.Handlers)
	}
}
