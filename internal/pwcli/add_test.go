package pwcli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// writeScaffoldedProject materializes what pw init would write, so the add and
// new tests run against the real starter rather than a hand-built fixture.
func writeScaffoldedProject(t *testing.T, options initOptions) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range scaffoldFiles(options) {
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The generation purposes may only name directories that exist.
	scope := scaffoldGenerationScope(options)
	for _, sources := range [][]string{scope.Handlers, scope.Templates, scope.Queries, scope.Config} {
		for _, source := range sources {
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(source)), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

// declinedProject is a project that took the registered router alone and
// neither the database, Redis, Tailwind, nor authentication, which is what pw
// add exists to repair.
func declinedProject(t *testing.T) string {
	t.Helper()
	return writeScaffoldedProject(t, initOptions{Name: "fixture", TinyGo: true, Auth: authNone})
}

func TestCapabilityDetectionReadsTheProjectFiles(t *testing.T) {
	full := writeScaffoldedProject(t, initOptions{
		Name: "fixture", Router: routerBoth, TinyGo: true, Devbox: true, Database: true, Redis: true,
		Dynamo: true, Firestore: true, Tailwind: true, Images: true, Auth: authOIDC, AuthEmulator: true,
	})
	state, err := loadProjectState(full)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := state.missingCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("a fully equipped project still reports %v as missing", missing)
	}

	state, err = loadProjectState(declinedProject(t))
	if err != nil {
		t.Fatal(err)
	}
	missing, err = state.missingCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{capabilityDiscovered, capabilityDevbox, capabilityDatabase, capabilityDynamo, capabilityFirestore, capabilityRedis, capabilityAuth, capabilityTailwind, capabilityImages}
	if strings.Join(missing, ",") != strings.Join(want, ",") {
		t.Fatalf("missing = %v, want %v", missing, want)
	}
}

// The point of the catalog is that declining at init costs nothing: adding the
// capability afterwards has to reach the same file state.
func TestAddDatabaseReachesTheScaffoldedState(t *testing.T) {
	root := declinedProject(t)
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	options := addOptions{Capability: capabilityDatabase, DSN: "sqlite://fixture.db"}
	plan, err := planCapability(state, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.apply(root); err != nil {
		t.Fatal(err)
	}

	scaffolded := scaffoldFiles(initOptions{Name: "fixture", TinyGo: true, Devbox: true, Database: true, Auth: authNone})
	for _, path := range []string{"migrations/00001_init.sql", "queries/users.pw.sql"} {
		added, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if string(added) != scaffolded[path] {
			t.Fatalf("%s differs from the scaffolded file:\n%s", path, added)
		}
	}
	config, err := os.ReadFile(filepath.Join(root, "config.dev.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "[[middleware.rdb.connections]]") ||
		!strings.Contains(string(config), `dsn = "sqlite://fixture.db"`) {
		t.Fatalf("the rdb section did not reach the environment config:\n%s", config)
	}
	// The purpose has to open with the directory, or generation would only warn
	// about the query it just wrote.
	reloaded, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(reloaded.config.Generate.Queries, ",") != "queries" {
		t.Fatalf("generate.queries = %#v", reloaded.config.Generate.Queries)
	}
	if _, present, err := reloaded.carries(capabilityDatabase); err != nil || !present {
		t.Fatalf("the database is not detected after adding it: %v", err)
	}
}

// The database reaches every environment the project configures, and the local
// DSN reaches only dev: a connections element has no environment variable of
// its own, so a deployment's value arrives through the ${NAME} reference rather
// than by an operator remembering to replace the development database.
func TestAddDatabaseNamesTheEnvironmentOutsideDev(t *testing.T) {
	root := declinedProject(t)
	if err := os.WriteFile(filepath.Join(root, "config.prod.toml"), []byte("[observability]\nminimum_level = \"info\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planCapability(state, addOptions{Capability: capabilityDatabase, DSN: "sqlite://fixture.db"})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.apply(root); err != nil {
		t.Fatal(err)
	}
	production, err := os.ReadFile(filepath.Join(root, "config.prod.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(production), `dsn = "${DATABASE_URL}"`) {
		t.Fatalf("the production file names no environment variable:\n%s", production)
	}
	if strings.Contains(string(production), "sqlite://fixture.db") {
		t.Fatalf("the development database reached the production file:\n%s", production)
	}
	development, err := os.ReadFile(filepath.Join(root, "config.dev.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(development), `dsn = "sqlite://fixture.db"`) {
		t.Fatalf("the development file lost its own database:\n%s", development)
	}
}

func TestAddAppendsRatherThanRewritingConfiguration(t *testing.T) {
	root := declinedProject(t)
	marker := "\n# operator note: keep this\n"
	path := filepath.Join(root, "config.dev.toml")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(original, marker...), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planCapability(state, addOptions{Capability: capabilityDatabase, DSN: "sqlite://fixture.db"})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.apply(root); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), marker) {
		t.Fatalf("an operator comment did not survive the edit:\n%s", updated)
	}
}

func TestAddAuthWritesFrameworkMigrationsAtTheNextFreeVersion(t *testing.T) {
	root := writeScaffoldedProject(t, initOptions{Name: "fixture", TinyGo: true, Devbox: true, Database: true, Auth: authNone})
	// An application that already applied its own migrations must not have them
	// renumbered, so the framework files take whatever is free.
	for _, name := range []string{"00002_add_tasks.sql", "00007_add_labels.sql"} {
		if err := os.WriteFile(filepath.Join(root, "migrations", name), []byte("-- +goose Up\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planCapability(state, addOptions{Capability: capabilityAuth, AuthEmulator: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.apply(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"migrations/00008_init_popcornweb_session.sql",
		"migrations/00009_init_popcornweb_auth.sql",
		"handlers/accounts.go",
		"devidp.toml",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "migrations", "00001_init.sql")); err != nil {
		t.Fatalf("an existing migration was renumbered: %v", err)
	}
	// The roster is useless to pw dev without the section that points at it, and
	// the port has to be pinned for the same reason pw init pins it: the
	// scaffolded resolver builds an account ID out of the issuer.
	project, err := os.ReadFile(filepath.Join(root, "popcornweb.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[dev.idp]", `config = "devidp.toml"`, "port = 18080"} {
		if !strings.Contains(string(project), want) {
			t.Fatalf("popcornweb.toml is missing %q:\n%s", want, project)
		}
	}
	// The entry point is edited rather than described, so nothing about this
	// capability is left for the operator to carry out by hand.
	if len(plan.manual) != 0 {
		t.Fatalf("manual steps = %#v", plan.manual)
	}
	if !strings.Contains(plan.edits["cmd/fixture/main.go"], "handlers.RegisterAccounts()") {
		t.Fatalf("the entry point edit does not install the account seams:\n%s", plan.edits["cmd/fixture/main.go"])
	}
	reloaded, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	if evidence, present, err := reloaded.carries(capabilityAuth); err != nil || !present {
		t.Fatalf("auth is not detected after adding it: %v %q", err, evidence)
	}
}

func TestAddRefusesToOverwriteAnApplicationFile(t *testing.T) {
	root := writeScaffoldedProject(t, initOptions{Name: "fixture", TinyGo: true, Devbox: true, Database: true, Auth: authNone})
	existing := filepath.Join(root, "handlers", "accounts.go")
	if err := os.WriteFile(existing, []byte("package handlers\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planCapability(state, addOptions{Capability: capabilityAuth})
	if err != nil {
		t.Fatal(err)
	}
	err = plan.apply(root)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want a conflict", err)
	}
	// Nothing may reach the project when any step would fail.
	if entries, _ := os.ReadDir(filepath.Join(root, "migrations")); len(entries) != 1 {
		t.Fatalf("a failed add left %d migrations behind", len(entries))
	}
	source, err := os.ReadFile(existing)
	if err != nil || string(source) != "package handlers\n" {
		t.Fatalf("the application file was overwritten: %q %v", source, err)
	}
}

func TestAddRedisAndTailwindEditDevbox(t *testing.T) {
	root := declinedProject(t)
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range []string{capabilityDevbox, capabilityRedis, capabilityTailwind} {
		plan, err := planCapability(state, addOptions{Capability: capability})
		if err != nil {
			t.Fatalf("%s: %v", capability, err)
		}
		if err := plan.apply(root); err != nil {
			t.Fatalf("%s: %v", capability, err)
		}
		if state, err = loadProjectState(root); err != nil {
			t.Fatalf("%s: %v", capability, err)
		}
	}
	devbox, err := os.ReadFile(filepath.Join(root, "devbox.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"valkey@latest", tailwindDevboxPackage} {
		if !strings.Contains(string(devbox), pkg) {
			t.Fatalf("devbox.json is missing %s:\n%s", pkg, devbox)
		}
	}
	if !state.config.Tailwind.Enabled {
		t.Fatal("popcornweb.toml did not enable Tailwind")
	}
	entry, err := os.ReadFile(filepath.Join(root, defaultTailwindInput))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), `@source "../handlers"`) {
		t.Fatalf("the CSS entry does not follow the template purpose:\n%s", entry)
	}
}

func TestCapabilityChainAddsTheRequiredCapabilityFirst(t *testing.T) {
	state, err := loadProjectState(declinedProject(t))
	if err != nil {
		t.Fatal(err)
	}
	chain := capabilityChain(state, capabilityAuth)
	if strings.Join(chain, ",") != capabilityDatabase+","+capabilityAuth {
		t.Fatalf("chain = %v", chain)
	}
	equipped, err := loadProjectState(writeScaffoldedProject(t,
		initOptions{Name: "fixture", TinyGo: true, Devbox: true, Database: true, Auth: authNone}))
	if err != nil {
		t.Fatal(err)
	}
	if chain := capabilityChain(equipped, capabilityAuth); strings.Join(chain, ",") != capabilityAuth {
		t.Fatalf("a project that already has the database still reinstalls it: %v", chain)
	}
}

func TestAddDevboxPackageLeavesAnUnknownShapeAlone(t *testing.T) {
	if _, err := addDevboxPackage(`{"shell": {}}`, "valkey@latest"); err == nil {
		t.Fatal("a devbox.json without a packages array was edited anyway")
	}
	edited, err := addDevboxPackage(`{"packages": []}`, "valkey@latest")
	if err != nil || edited != `{"packages": ["valkey@latest"]}` {
		t.Fatalf("edited = %q, err = %v", edited, err)
	}
	twice, err := addDevboxPackage(edited, "valkey@latest")
	if err != nil || twice != edited {
		t.Fatalf("adding a present package changed the file: %q", twice)
	}
}

// TestRunAddWizardOverKeystrokes exercises the real Bubble Tea program, so the
// step list, the review screen, and result extraction stay covered.
func TestRunAddWizardOverKeystrokes(t *testing.T) {
	root := declinedProject(t)
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := state.missingCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	// database (first choice), the engine (SQLite, first choice), the seeded
	// DSN, then the review screen.
	options, err := runAddWizard(state, missing,
		addOptions{Capability: capabilityDatabase, Engine: engineSQLite, DSN: "sqlite://fixture.db"},
		tea.WithInput(strings.NewReader("\r\r\r\r")),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if options.Capability != capabilityDatabase || options.Engine != engineSQLite ||
		options.DSN != "sqlite://fixture.db" {
		t.Fatalf("options = %#v", options)
	}
}

// TestAddWizardEngineSelectsTheDSN asserts an empty DSN answer takes the
// engine default, so the engine step decides it rather than a value seeded
// before the engine was known.
func TestAddWizardEngineSelectsTheDSN(t *testing.T) {
	for _, testcase := range []struct {
		engine string
		want   string
	}{
		{engine: engineSQLite, want: "sqlite://fixture.db"},
		{engine: enginePostgres, want: "postgres://fixture:fixture@127.0.0.1:5432/fixture?sslmode=disable"},
		{engine: engineMySQL, want: "mysql://fixture:fixture@tcp(127.0.0.1:3306)/fixture"},
	} {
		options := addOptions{Capability: capabilityDatabase, Engine: testcase.engine}
		if got := options.databaseDSN("fixture"); got != testcase.want {
			t.Fatalf("%s DSN = %q, want %q", testcase.engine, got, testcase.want)
		}
	}
	// An explicit answer still wins over the engine default.
	explicit := addOptions{Capability: capabilityDatabase, Engine: enginePostgres, DSN: "postgres://elsewhere/db"}
	if got := explicit.databaseDSN("fixture"); got != "postgres://elsewhere/db" {
		t.Fatalf("explicit DSN = %q", got)
	}
}

// TestAddDatabasePerEngine asserts pw add reaches the same per-engine file
// state pw init writes, including the import it cannot inject itself.
func TestAddDatabasePerEngine(t *testing.T) {
	root := declinedProject(t)
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planCapability(state, addOptions{Capability: capabilityDatabase, Engine: enginePostgres})
	if err != nil {
		t.Fatal(err)
	}
	// Version 1 creates nothing: it carries the shape of a migration for this
	// dialect, commented out, so adding a database does not also add a table.
	schema := plan.creates["migrations/00001_init.sql"]
	if !strings.Contains(schema, "-- +goose Up") || !strings.Contains(schema, "-- CREATE TABLE example (") {
		t.Fatalf("schema = %q", schema)
	}
	for _, statement := range []string{"\nCREATE TABLE", "\nDROP TABLE"} {
		if strings.Contains(schema, statement) {
			t.Fatalf("starter migration executes a statement: %q", schema)
		}
	}
	// The selected engine is imported by the entry point, and pw add plans that
	// edit rather than carrying every driver in the framework.
	if entry := plan.edits["cmd/fixture/main.go"]; !strings.Contains(entry, `_ "github.com/shibukawa/popcornweb/database/postgres"`) {
		t.Fatalf("the entry point does not link the engine:\n%s", entry)
	}
	if !strings.Contains(strings.Join(plan.summary(), "\n"), "edit    cmd/fixture/main.go") {
		t.Fatalf("the review screen does not name the entry point:\n%s", strings.Join(plan.summary(), "\n"))
	}
	// Generation reads the engine from popcornweb.toml, so pw add has to
	// record it there as pw init does.
	if project := plan.edits["popcornweb.toml"]; !strings.Contains(project, `database = "postgres"`) {
		t.Fatalf("plan does not record the engine:\n%s", project)
	}

	// Every engine now has a generated SQL path, so every engine gets the
	// example and the purpose that reads it.
	for _, engine := range []string{engineSQLite, engineMySQL} {
		enginePlan, err := planCapability(state, addOptions{Capability: capabilityDatabase, Engine: engine})
		if err != nil {
			t.Fatal(err)
		}
		if entry := enginePlan.edits["cmd/fixture/main.go"]; !strings.Contains(entry, databaseEngines[engine].DriverImport) {
			t.Fatalf("the %s entry point does not link its engine:\n%s", engine, entry)
		}
		if _, wrote := enginePlan.creates["queries/users.pw.sql"]; !wrote {
			t.Fatalf("%s plan wrote no query example", engine)
		}
		if project := enginePlan.edits["popcornweb.toml"]; !strings.Contains(project, `database = "`+engine+`"`) {
			t.Fatalf("%s plan does not record the engine:\n%s", engine, project)
		}
	}
}

// TestSetProjectDatabase covers the popcornweb.toml edit directly, including
// the shapes a hand-edited project can present.
func TestSetProjectDatabase(t *testing.T) {
	for _, testcase := range []struct {
		name     string
		document string
		want     string
	}{
		{
			name:     "appends to a project table followed by another",
			document: "[project]\nname = \"demo\"\n\n[generate]\nqueries = []\n",
			want:     "[project]\nname = \"demo\"\n\ndatabase = \"mysql\"\n[generate]\nqueries = []\n",
		},
		{
			name:     "appends at the end of the document",
			document: "[project]\nname = \"demo\"\n",
			want:     "[project]\nname = \"demo\"\ndatabase = \"mysql\"\n",
		},
		{
			name:     "replaces an existing key",
			document: "[project]\nname = \"demo\"\ndatabase = \"sqlite\"\nmain = \"./cmd/demo\"\n",
			want:     "[project]\nname = \"demo\"\ndatabase = \"mysql\"\nmain = \"./cmd/demo\"\n",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			got, err := setProjectDatabase(testcase.document, engineMySQL)
			if err != nil {
				t.Fatal(err)
			}
			if got != testcase.want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, testcase.want)
			}
		})
	}
	// A document with no [project] table is not one this edit can repair.
	if _, err := setProjectDatabase("[generate]\nqueries = []\n", engineMySQL); err == nil {
		t.Fatal("an edit without a [project] table was accepted")
	}
}

func TestAddWizardReviewListsTheFiles(t *testing.T) {
	root := declinedProject(t)
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	review := addReview(state, addOptions{Capability: capabilityDatabase, DSN: "sqlite://fixture.db"})
	joined := strings.Join(review, "\n")
	for _, want := range []string{
		"create  migrations/00001_init.sql",
		"create  queries/users.pw.sql",
		"append  config.dev.toml",
		"edit    popcornweb.toml",
		"then    pw migrate up",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("review is missing %q:\n%s", want, joined)
		}
	}
}

// Choosing a capability that depends on another one says so before anything is
// written, and the dependency is installed first.
func TestAddWizardReviewNamesTheRequiredCapability(t *testing.T) {
	state, err := loadProjectState(declinedProject(t))
	if err != nil {
		t.Fatal(err)
	}
	review := strings.Join(addReview(state, addOptions{Capability: capabilityAuth, DSN: "sqlite://fixture.db"}), "\n")
	if !strings.Contains(review, "auth needs database, which is added first") {
		t.Fatalf("review does not name the dependency:\n%s", review)
	}
}

func TestRunAddReportsCancellation(t *testing.T) {
	root := declinedProject(t)
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := state.missingCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	_, err = runAddWizard(state, missing, addOptions{Capability: capabilityRedis},
		tea.WithInput(strings.NewReader("\x03")),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
	)
	if !errors.Is(err, errWizardCanceled) {
		t.Fatalf("err = %v, want %v", err, errWizardCanceled)
	}
}

// A scripted run never sees the wizard, so the completion notice is where it
// learns that a declined capability is not permanent.
func TestInitReportsDeclinedCapabilities(t *testing.T) {
	for _, testcase := range []struct {
		name    string
		options initOptions
		want    string
	}{
		{
			name:    "everything declined",
			options: initOptions{Database: false, Redis: false, Tailwind: false, Auth: authNone},
			want:    "devbox,database,dynamo,firestore,redis-valkey,auth,tailwind",
		},
		{
			name:    "only Tailwind declined",
			options: initOptions{Devbox: true, Database: true, Dynamo: true, Firestore: true, Redis: true, Auth: authOIDC},
			want:    "tailwind",
		},
		{
			name:    "nothing declined",
			options: initOptions{Devbox: true, Database: true, Dynamo: true, Firestore: true, Redis: true, Tailwind: true, Auth: authOIDC},
			want:    "",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			if got := strings.Join(declinedCapabilities(testcase.options), ","); got != testcase.want {
				t.Fatalf("declined = %q, want %q", got, testcase.want)
			}
		})
	}
}

// Every declinable answer says how to change its mind, and the one that cannot
// be added later does not pretend otherwise.
func TestInitWizardNamesTheCommandThatEnablesADeclinedOption(t *testing.T) {
	steps := initWizardSteps(defaultInitOptions())
	described := map[string]string{}
	for _, step := range steps {
		choices, ok := unwrapStep(step).(*choiceStep[initOptions])
		if !ok {
			continue
		}
		for _, choice := range choices.choices {
			described[choices.name+"/"+choice.name] = choice.description
		}
	}
	for label, capability := range map[string]string{
		"Tailwind CSS/No":     capabilityTailwind,
		"Database/No":         capabilityDatabase,
		"Redis or Valkey/No":  capabilityRedis,
		"Authentication/None": capabilityAuth,
	} {
		if !strings.Contains(described[label], "pw add "+capability) {
			t.Errorf("%s does not name pw add %s: %q", label, capability, described[label])
		}
	}
	// The toolchain is not a capability; it is recorded in the project and pw
	// add cannot change it.
	if strings.Contains(described["TinyGo support/No"], "pw add") {
		t.Errorf("the toolchain answer claims pw add can change it: %q", described["TinyGo support/No"])
	}
}

// A project that declined the Devbox environment reaches Valkey through the
// dependency, because the Valkey answer writes nothing but a Devbox package.
func TestAddRedisInstallsDevboxFirst(t *testing.T) {
	root := writeScaffoldedProject(t, initOptions{Name: "fixture", TinyGo: true, Tailwind: true, Auth: authNone})
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	if chain := capabilityChain(state, capabilityRedis); strings.Join(chain, ",") != capabilityDevbox+","+capabilityRedis {
		t.Fatalf("chain = %v", chain)
	}
	plan, err := planCapability(state, addOptions{Capability: capabilityDevbox})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.apply(root); err != nil {
		t.Fatal(err)
	}
	devbox, err := os.ReadFile(filepath.Join(root, "devbox.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The environment carries what the project already decided elsewhere.
	for _, pkg := range []string{"go@latest", "tinygo@latest", tailwindDevboxPackage} {
		if !strings.Contains(string(devbox), pkg) {
			t.Fatalf("devbox.json is missing %s:\n%s", pkg, devbox)
		}
	}
}

// Without the environment there is no package list to pin, so the Tailwind
// version is printed instead of failing the install.
func TestAddTailwindWithoutDevboxPrintsTheToolchain(t *testing.T) {
	root := writeScaffoldedProject(t, initOptions{Name: "fixture", TinyGo: true, Auth: authNone})
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planCapability(state, addOptions{Capability: capabilityTailwind})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.apply(root); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.manual, "\n"), tailwindToolchainRequirement) {
		t.Fatalf("manual steps = %#v", plan.manual)
	}
	if _, err := os.Stat(filepath.Join(root, defaultTailwindInput)); err != nil {
		t.Fatalf("the CSS entry was not written: %v", err)
	}
}

// A declined Devbox environment takes the Valkey question off the list, so a
// seeded --redis answer cannot survive as an unreachable one.
func TestInitWizardSkipsValkeyWithoutDevbox(t *testing.T) {
	t.Chdir(t.TempDir())
	model := startWizard(t, presetManual, "demo", defaultInitOptions())
	model = answerHubRow(t, model, "Devbox environment", 2) // No
	if hubRow(model, "Redis or Valkey") {
		t.Fatalf("the Valkey row outlived the environment it installs into: rows are %v", hubLabels(model))
	}
	options := wizardResult(model, defaultInitOptions())
	if options.Devbox || options.Redis {
		t.Fatalf("options = %#v", options)
	}
}

// A project without the Devbox environment installs the CSS toolchain itself,
// so the scaffold says which one rather than leaving the first pw dev to fail
// on a missing binary.
func TestInitReportsTheTailwindToolchainWithoutDevbox(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	var output strings.Builder
	if err := runInit([]string{"fixture", "--tailwind", "--no-devbox", "--no-database"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "install "+tailwindToolchainRequirement) {
		t.Fatalf("output does not name the Tailwind toolchain:\n%s", output.String())
	}

	// With the environment, the same answer needs no notice: devbox.json pins it.
	output.Reset()
	if err := runInit([]string{"fixture2", "--tailwind", "--no-database"}, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "install "+tailwindToolchainRequirement) {
		t.Fatalf("a Devbox project was told to install Tailwind by hand:\n%s", output.String())
	}
}

// The hint has to name the way this project installs tools, or it sends the
// operator into a shell the scaffold never wrote.
func TestTailwindToolchainHintFollowsTheProject(t *testing.T) {
	root := t.TempDir()
	if hint := tailwindToolchainHint(root); !strings.Contains(hint, "install ") {
		t.Fatalf("hint = %q, want the manual instructions", hint)
	}
	if err := os.WriteFile(filepath.Join(root, "devbox.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if hint := tailwindToolchainHint(root); !strings.Contains(hint, "Devbox shell") {
		t.Fatalf("hint = %q, want the Devbox shell", hint)
	}
}

// The catalog's promise is that declining costs nothing, and an entry point one
// command writes and the other only talks about is the loudest way to break it:
// storage is opt-in by blank import, so the configuration pw add appends does
// nothing until the binary links what it names.
func TestAddAuthReachesTheScaffoldedEntryPoint(t *testing.T) {
	root := writeScaffoldedProject(t, initOptions{
		Name: "fixture", TinyGo: true, Devbox: true, Database: true, Auth: authNone,
	})
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planCapability(state, addOptions{Capability: capabilityAuth})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.apply(root); err != nil {
		t.Fatal(err)
	}
	added, err := os.ReadFile(filepath.Join(root, "cmd", "fixture", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`_ "github.com/shibukawa/popcornweb/sessionstore/sqlite"`,
		`_ "github.com/shibukawa/popcornweb/authstate/sqlite"`,
		"handlers.RegisterAccounts()",
	} {
		if !strings.Contains(string(added), want) {
			t.Fatalf("the entry point is missing %s:\n%s", want, added)
		}
	}
	// The edit is shown before it is made, which is what makes touching an
	// application-owned file acceptable at all.
	if !strings.Contains(strings.Join(plan.summary(), "\n"), "edit    cmd/fixture/main.go") {
		t.Fatalf("the review screen does not name the entry point:\n%s", strings.Join(plan.summary(), "\n"))
	}
	// A second run must not stack a duplicate import or a second call.
	second, err := withBlankImports(string(added),
		blankImport{"github.com/shibukawa/popcornweb/sessionstore/sqlite", "again"})
	if err != nil {
		t.Fatal(err)
	}
	if second != string(added) {
		t.Fatalf("a repeated import was added twice:\n%s", second)
	}
	if strings.Count(string(added), "handlers.RegisterAccounts()") != 1 {
		t.Fatalf("RegisterAccounts appears more than once:\n%s", added)
	}
}

// TestAddImagesReachesTheScaffoldedState covers the encoders being declared
// rather than discovered: taking the capability writes the configuration and
// the tool environment together, so a project cannot end up converting nothing
// because a package was never added.
func TestAddImagesReachesTheScaffoldedState(t *testing.T) {
	root := writeScaffoldedProject(t, initOptions{Name: "fixture", TinyGo: true, Devbox: true, Auth: authNone})
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planCapability(state, addOptions{Capability: capabilityImages})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.appends["popcornweb.toml"], "[assets.images]") {
		t.Errorf("popcornweb.toml append = %q", plan.appends["popcornweb.toml"])
	}
	devbox := plan.edits["devbox.json"]
	for _, pkg := range imageDevboxPackages {
		if !strings.Contains(devbox, pkg) {
			t.Errorf("devbox.json is missing %s:\n%s", pkg, devbox)
		}
	}
}

// TestAddImagesWithoutDevboxNamesTheTools is the other environment: a project
// installing its own toolchain gets the requirement in words, since a nixpkgs
// package name means nothing to someone using Homebrew.
func TestAddImagesWithoutDevboxNamesTheTools(t *testing.T) {
	root := writeScaffoldedProject(t, initOptions{Name: "fixture", TinyGo: true, Auth: authNone})
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planCapability(state, addOptions{Capability: capabilityImages})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plan.edits["devbox.json"]; ok {
		t.Error("a project with no devbox got a devbox edit")
	}
	if len(plan.manual) == 0 || !strings.Contains(strings.Join(plan.manual, " "), "cwebp") {
		t.Errorf("plan.manual = %v", plan.manual)
	}
}

// pw add auth offers the OAuth login in the same terms as pw init: the same
// section, no roster, and no development identity provider.
func TestAddAuthInstallsTheOAuthLogin(t *testing.T) {
	root := declinedProject(t)
	state, err := loadProjectState(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planCapability(state, addOptions{Capability: capabilityAuth, AuthMode: authOAuth, AuthEmulator: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, created := plan.creates[defaultIdPConfig]; created {
		t.Error("an OAuth login has no issuer for the development identity provider to emulate")
	}
	if _, appended := plan.appends["popcornweb.toml"]; appended {
		t.Error("dev.idp must not be enabled for an OAuth login")
	}
	section := plan.appends["config.dev.toml"]
	for _, expected := range []string{`mode = "oauth_only"`, "[auth.oauth]", `provider = "x"`} {
		if !strings.Contains(section, expected) {
			t.Errorf("the appended section is missing %s:\n%s", expected, section)
		}
	}
	if strings.Contains(section, "[auth.oidc]") {
		t.Errorf("the appended section carries an oidc section the mode refuses:\n%s", section)
	}
}
