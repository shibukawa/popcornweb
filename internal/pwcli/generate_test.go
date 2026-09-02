package pwcli

import (
	"bytes"
	"context"
	"fmt"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/internal/pwgen"
	"github.com/shibukawa/tinybind-go/generator"
	"github.com/shibukawa/tinybind-go/templates/sqlbind"
)

// allPurposes is the scope of a directory every purpose lists, which is what
// most planDirectory tests are about.
var allPurposes = generationPurposes{handlers: true, templates: true, queries: true, config: true}

func TestPlanDirectoryGeneratesV015Artifacts(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, filepath.Join(directory, "go.mod"), "module fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(directory, "home.pw.html"), `package fixture

export component Home(name: string): html {
<h1>Hello, {name}</h1>
}
`)
	writeTestFile(t, filepath.Join(directory, "users.pw.sql"), `package fixture

type User {
  id: int
  name: string
}

export statement FindUser(id: int): sql.one<User> {
SELECT id, name FROM users WHERE id = {id}
}
`)

	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	runner := generator.New(options)
	changes, err := planDirectory(context.Background(), runner, directory, allPurposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := changesByBase(changes)
	// The template runtime lives in the htmlbind module, so generation emits one
	// artifact per source and no shared per-package runtime file.
	for _, name := range []string{"home_pw_gen.go", "users_pw_gen.go"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("missing generated artifact %s; got %v", name, mapKeys(byName))
		}
	}
	home := string(byName["home_pw_gen.go"].source)
	if !strings.Contains(home, "func Home(params HomeParams) htmlbind.Fragment") {
		t.Errorf("HTML artifact does not use Fragment API:\n%s", home)
	}
	sql := string(byName["users_pw_gen.go"].source)
	if !strings.Contains(sql, "func FindUser(ctx context.Context, id int) (User, error)") {
		t.Errorf("SQL artifact does not use context-only API:\n%s", sql)
	}
	if strings.Contains(sql, "FindUserContext") ||
		strings.Contains(sql, "func FindUser(ctx context.Context, db SQLQuerier") {
		t.Errorf("SQL artifact exposes the legacy executor API:\n%s", sql)
	}

	if err := applyFileChanges(changes); err != nil {
		t.Fatal(err)
	}
	changes, err = planDirectory(context.Background(), runner, directory, allPurposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("generation is not deterministic: %#v", changes)
	}

	stale := filepath.Join(directory, "obsolete_pw_gen.go")
	writeTestFile(t, stale, "package fixture\n")
	changes, err = planDirectory(context.Background(), runner, directory, allPurposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName = changesByBase(changes)
	if !byName["obsolete_pw_gen.go"].remove {
		t.Fatalf("stale generated file was not scheduled for removal: %#v", changes)
	}
	if err := applyFileChanges(changes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale generated file still exists: %v", err)
	}
}

// A live source is a template-level declaration the framework never sees
// directly: what reaches pw is a plan flagged live and a boundary that keeps
// delivering. This holds the generation half of that path, so a template a
// project writes today produces what api:live-delivery-protocol serves.
// TestPlanDirectoryPlacesProducedAssetsInTheDerivedDirectory drives the seam
// between generation and the asset build, which nothing covered: the unit tests
// on either side call buildScriptEntry and buildDerivedAssets directly, and no
// project in this repository enables the script build.
//
// What that hid is that the artifact API returns produced files rather than
// writing them, so every one was grouped with the Go artifacts and written as
// app.<hash>.js_pw_gen.go — a Go file holding JavaScript, which the next run
// refused to parse — while the bundle the rewritten reference named was never
// written at all.
func TestPlanDirectoryPlacesProducedAssetsInTheDerivedDirectory(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module fixture\n\ngo 1.26.0\n")
	writeNestedTestFile(t, filepath.Join(root, "public", "js", "app.ts"),
		"const greet = (name: string): string => `hi ${name}`;\nconsole.log(greet(\"world\"));\n")
	writeTestFile(t, filepath.Join(root, "home.pw.html"), `package fixture

export component Home(): html {
<head>
  <script type="module" src="/public/js/app.ts"></script>
</head>
<h1>hi</h1>
}
`)

	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	derived := filepath.Join(root, "dist", "derived")
	options.ReferenceHooks = assetReferenceHooks(root, assetsConfig{Scripts: true})
	options.DerivedAssetDir = derived
	options.ConversionCacheDir = filepath.Join(root, "dist", "cache")
	runner := generator.New(options)

	changes, err := planDirectory(context.Background(), runner, root, allPurposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	produced := map[string]bool{}
	for _, change := range changes {
		paths = append(paths, change.path)
		if strings.HasPrefix(change.path, derived) {
			produced[filepath.Base(change.path)] = true
		}
		// The bundle and its map are not Go, and nothing about them belongs in a
		// package. A _pw_gen.go named after one is the defect itself.
		if strings.HasSuffix(change.path, "_pw_gen.go") && strings.Contains(filepath.Base(change.path), "app.") {
			t.Errorf("a produced asset was planned as Go: %s", change.path)
		}
	}
	if len(produced) == 0 {
		t.Fatalf("no produced asset was planned into %s: %v", derived, paths)
	}
	// The bundle is what the rewritten reference names, so its absence is a 404
	// on every page that loads the script.
	bundled := false
	for name := range produced {
		if strings.HasPrefix(name, "app.") && strings.HasSuffix(name, ".js") {
			bundled = true
		}
	}
	if !bundled {
		t.Errorf("the bundle was not placed: %v", produced)
	}
	home := ""
	for _, change := range changes {
		if filepath.Base(change.path) == "home_pw_gen.go" {
			home = string(change.source)
		}
	}
	if !strings.Contains(home, "/public/js/app.") || strings.Contains(home, "app.ts") {
		t.Errorf("the reference was not rewritten to the bundle:\n%s", home)
	}

	// Applying and re-planning must report nothing, or --check would call a tree
	// it just built stale.
	if err := applyFileChanges(changes); err != nil {
		t.Fatal(err)
	}
	changes, err = planDirectory(context.Background(), runner, root, allPurposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range changes {
		if strings.HasPrefix(change.path, derived) {
			t.Errorf("a produced asset was replanned after it was written: %s", change.path)
		}
	}
}

func TestPlanDirectoryGeneratesLiveBoundaries(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, filepath.Join(directory, "go.mod"), "module fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(directory, "gauge.pw.html"), `package fixture

type Point {
  label: string
  value: int
}

external live WatchMetrics(id: string): Point

export component Gauge(id: string): html {
{await point = WatchMetrics(id)}
  <p>{point.label}: {point.value}</p>
{fallback}
  <p>waiting</p>
{/await}
}
`)

	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := planDirectory(context.Background(), generator.New(options), directory, allPurposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	gauge := string(changesByBase(changes)["gauge_pw_gen.go"].source)
	// The flag is what decision:automatic-async-render-selection probes to decide
	// whether the document ends by inviting a live connection.
	if !strings.Contains(strings.Join(strings.Fields(gauge), " "), "HasLiveBlock: true") {
		t.Errorf("generated plan does not carry the live flag:\n%s", gauge)
	}
	if !strings.Contains(gauge, "Ops.Live(") {
		t.Errorf("generated plan does not open a live boundary:\n%s", gauge)
	}
	// The leading context is mandatory for a live source, because a source with
	// no context has nothing to make it return when the subscription ends.
	if !strings.Contains(gauge, "WatchMetrics(ctx,") {
		t.Errorf("generated call does not pass the subscription context:\n%s", gauge)
	}
}

func TestMergeArtifactsProducesOneValidGoFile(t *testing.T) {
	source, err := mergeArtifacts([]generator.Artifact{
		{
			PackageName: "fixture",
			Content: []byte(`package fixture

import "fmt"

func first() { fmt.Print("first") }
`),
		},
		{
			PackageName: "fixture",
			Content: []byte(`package fixture

import "strings"

func second() { _ = strings.Builder{} }
`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if strings.Count(text, "package fixture") != 1 ||
		!strings.Contains(text, `"fmt"`) ||
		!strings.Contains(text, `"strings"`) ||
		!strings.Contains(text, "func first()") ||
		!strings.Contains(text, "func second()") {
		t.Fatalf("unexpected merged source:\n%s", text)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "merged.go", source, parser.AllErrors); err != nil {
		t.Fatalf("merged source is invalid: %v\n%s", err, text)
	}
}

func TestPlanDirectoryRegistersDocumentShell(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, filepath.Join(directory, "go.mod"), "module fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(directory, "document.pw.html"), `package fixture

export component Document(children: html?): html {
<!doctype html><html><body><slot /></body></html>
}
`)
	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := planDirectory(context.Background(), generator.New(options), directory, allPurposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	document := string(changesByBase(changes)["document_pw_gen.go"].source)
	for _, fragment := range []string{
		// The shared leaf, so the file belongs to both builds: the registry is
		// one process-wide table, and naming a runtime here would register the
		// document in the half that is not serving.
		`"github.com/shibukawa/popcornweb/pwruntime"`,
		"func init()",
		"pwruntime.RegisterHTMLDocument(BindDocument(DocumentParams{}))",
	} {
		if !strings.Contains(document, fragment) {
			t.Fatalf("document artifact is missing %q:\n%s", fragment, document)
		}
	}
}

// A component carrying the annotation is published, and one beside it that does
// not is left alone. Both are asserted from one directory, because the failure
// worth catching is a scan that registers every component it can see.
func TestPlanDirectoryRegistersReloadableComponents(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, filepath.Join(directory, "go.mod"), "module fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(directory, "card.pw.html"), `package fixture

@reloadable
export component Card(id: string, page: int): html {
<article>{page}</article>
}

export component Plain(label: string): html {
<span>{label}</span>
}
`)
	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := planDirectory(context.Background(), generator.New(options), directory, allPurposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	card := string(changesByBase(changes)["card_pw_gen.go"].source)
	for _, fragment := range []string{
		`"github.com/shibukawa/popcornweb/pwruntime"`,
		"var CardReloadable = htmlupdate.Reloadable{",
		"pwruntime.RegisterReloadable(CardReloadable)",
	} {
		if !strings.Contains(card, fragment) {
			t.Fatalf("reloadable artifact is missing %q:\n%s", fragment, card)
		}
	}
	if strings.Contains(card, "PlainReloadable") {
		t.Fatalf("a component without the annotation was published:\n%s", card)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "card.go", card, parser.AllErrors); err != nil {
		t.Fatalf("generated source is invalid: %v\n%s", err, card)
	}
}

// The set a page hands to pw.Redraw is folded from what its markup can actually
// contain, so an author never enumerates it and cannot forget an entry.
func TestPlanDirectoryFoldsTheReloadableSetOfEachComponent(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, filepath.Join(directory, "go.mod"), "module fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(directory, "page.pw.html"), `package fixture

@reloadable
export component Card(id: string, page: int): html {
<article>{page}</article>
}

export component Sidebar(): html {
<aside><Card id="card-1" page={1} /></aside>
}

export component Page(): html {
<main><Sidebar /></main>
}

export component Plain(label: string): html {
<span>{label}</span>
}
`)
	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := planDirectory(context.Background(), generator.New(options), directory, allPurposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	page := string(changesByBase(changes)["page_pw_gen.go"].source)
	// Page calls Sidebar calls Card, so the fold is transitive rather than one
	// level deep. Card names itself, because redrawing the region you are already
	// looking at is the ordinary case.
	for _, fragment := range []string{
		"func (PageParams) PwReloadables() []htmlupdate.Reloadable",
		"func (SidebarParams) PwReloadables() []htmlupdate.Reloadable",
		"func (CardParams) PwReloadables() []htmlupdate.Reloadable",
	} {
		if !strings.Contains(page, fragment) {
			t.Errorf("missing %q:\n%s", fragment, page)
		}
	}
	// A component whose markup can contain none publishes no set, so a handler
	// cannot hand out a list that promises something it never renders.
	if strings.Contains(page, "PlainParams) PwReloadables") {
		t.Errorf("a component reaching no reloadable one published a set:\n%s", page)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "page.go", page, parser.AllErrors); err != nil {
		t.Fatalf("generated source is invalid: %v\n%s", err, page)
	}
}

// A template declaring none regenerates exactly as it did before, so turning the
// scan on does not rewrite every generated file in an existing project.
func TestPlanDirectoryLeavesPlainTemplatesUnregistered(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, filepath.Join(directory, "go.mod"), "module fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(directory, "plain.pw.html"), `package fixture

export component Plain(label: string): html {
<span>{label}</span>
}
`)
	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := planDirectory(context.Background(), generator.New(options), directory, allPurposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	plain := string(changesByBase(changes)["plain_pw_gen.go"].source)
	if strings.Contains(plain, "RegisterReloadable") {
		t.Fatalf("a plain template registered a redraw endpoint:\n%s", plain)
	}
}

func TestPlanBootstrapLinkGeneratesRuntimeRegistrationImports(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(root, "public.go"), "package publicassets\n")
	writeTestFile(t, filepath.Join(root, "cmd", "fixture", "main.go"), "package main\n\nfunc main() {}\n")
	writeTestFile(t, filepath.Join(root, "templates", "document.pw.html"), `package templates

export component Document(children: html?): html {
<html><body><slot /></body></html>
}
`)
	config := projectConfig{Main: "./cmd/fixture", Generate: generationScope{Templates: []string{"templates"}}}
	changes, err := planBootstrapLink(root, config, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %#v", changes)
	}
	source := string(changes[0].source)
	for _, expected := range []string{
		`_ "example.test/fixture"`,
		`_ "example.test/fixture/templates"`,
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("link source is missing %q:\n%s", expected, source)
		}
	}
}

func writeTestFile(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func changesByBase(changes []fileChange) map[string]fileChange {
	out := make(map[string]fileChange, len(changes))
	for _, change := range changes {
		out[filepath.Base(change.path)] = change
	}
	return out
}

func mapKeys(values map[string]fileChange) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

// Generation reads the configured directories and nothing else, so a template
// kept outside them produces no artifact and is reported instead.
func TestRunGenerateStopsAtConfiguredSources(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"handlers", filepath.Join("cmd", "fixture"), "samples"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(root, "popcornweb.toml"),
		"[project]\nname = \"fixture\"\nmain = \"./cmd/fixture\"\n\n[generate]\n"+
			"handlers = [\"handlers\"]\ntemplates = [\"handlers\"]\nqueries = []\nconfig = []\n")
	writeTestFile(t, filepath.Join(root, "cmd", "fixture", "main.go"), "package main\n\nfunc main() {}\n")
	page := `package %s

export component Home(name: string): html {
<h1>Hello, {name}</h1>
}
`
	writeTestFile(t, filepath.Join(root, "handlers", "home.pw.html"), fmt.Sprintf(page, "handlers"))
	writeTestFile(t, filepath.Join(root, "samples", "home.pw.html"), fmt.Sprintf(page, "samples"))

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)

	var output strings.Builder
	if err := generateCode(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "handlers", "home_pw_gen.go")); err != nil {
		t.Fatalf("listed source was not generated from: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "samples", "home_pw_gen.go")); !os.IsNotExist(err) {
		t.Fatalf("unlisted source was generated from: %v", err)
	}
	if !strings.Contains(output.String(), "samples/home.pw.html is outside generate.templates") {
		t.Fatalf("unlisted source was not reported:\n%s", output.String())
	}
}

// A project migrating from whole-project discovery keeps generated files in
// directories the list no longer covers, and they are named rather than left to
// register stale content at runtime.
func TestRunGenerateReportsStaleArtifactsOutsideSources(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"handlers", filepath.Join("cmd", "fixture")} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(root, "popcornweb.toml"),
		"[project]\nname = \"fixture\"\nmain = \"./cmd/fixture\"\n\n[generate]\n"+
			"handlers = [\"handlers\"]\ntemplates = [\"handlers\"]\nqueries = []\nconfig = []\n")
	writeTestFile(t, filepath.Join(root, "cmd", "fixture", "main.go"), "package main\n\nfunc main() {}\n")
	writeTestFile(t, filepath.Join(root, "tinybind_openapi_pw_gen.go"), "package fixture\n")
	writeTestFile(t, filepath.Join(root, "cmd", "fixture", "popcornweb_bootstrap_pw_gen.go"), "package main\n")
	writeTestFile(t, filepath.Join(root, assetManifestFile), "package fixture\n")

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)

	var output strings.Builder
	if err := generateCode(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "tinybind_openapi_pw_gen.go was generated outside every generate purpose") {
		t.Fatalf("stale artifact was not reported:\n%s", output.String())
	}
	if strings.Contains(output.String(), "popcornweb_bootstrap_pw_gen.go was generated outside") {
		t.Fatalf("the bootstrap linker is written on purpose and must not be reported:\n%s", output.String())
	}
	// The asset manifest is written by the asset build, at the root, into the
	// package public.go declares there. It belongs to no generate purpose and
	// cannot, so reporting it made every rebuild after the first print a
	// warning about a file the build had just written deliberately.
	if strings.Contains(output.String(), assetManifestFile+" was generated outside") {
		t.Fatalf("the asset manifest is written on purpose and must not be reported:\n%s", output.String())
	}
}

// A directory listed for one purpose contributes only that purpose's artifacts,
// which is what keeps a query package from being analyzed for routes and a
// handler package from generating renderers for a template it only stores.
func TestRunGeneratePurposesSelectArtifactsPerDirectory(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"handlers", "queries", filepath.Join("cmd", "fixture")} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/fixture\n\ngo 1.26.0\n")
	// handlers holds a page template, but only the query purpose lists queries,
	// so the template beside the handler is generated and the one in queries is
	// not.
	writeTestFile(t, filepath.Join(root, "popcornweb.toml"),
		"[project]\nname = \"fixture\"\nmain = \"./cmd/fixture\"\n\n[generate]\n"+
			"handlers = [\"handlers\"]\ntemplates = [\"handlers\"]\nqueries = [\"queries\"]\nconfig = []\n")
	writeTestFile(t, filepath.Join(root, "cmd", "fixture", "main.go"), "package main\n\nfunc main() {}\n")
	writeTestFile(t, filepath.Join(root, "handlers", "home.pw.html"), `package handlers

export component Home(name: string): html {
<h1>Hello, {name}</h1>
}
`)
	writeTestFile(t, filepath.Join(root, "queries", "users.pw.sql"), `package queries

type User {
  id: int
  name: string
}

export statement FindUser(id: int): sql.one<User> {
SELECT id, name FROM users WHERE id = {id}
}
`)
	writeTestFile(t, filepath.Join(root, "queries", "note.pw.html"), `package queries

export component Note(): html {
<p>stored beside the queries, generated by nobody</p>
}
`)

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)

	var output strings.Builder
	if err := generateCode(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, generated := range []string{
		filepath.Join("handlers", "home_pw_gen.go"),
		filepath.Join("queries", "users_pw_gen.go"),
	} {
		if _, err := os.Stat(filepath.Join(root, generated)); err != nil {
			t.Fatalf("%s was not generated: %v", generated, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "queries", "note_pw_gen.go")); !os.IsNotExist(err) {
		t.Fatalf("a template outside generate.templates was generated: %v", err)
	}
	if !strings.Contains(output.String(), "queries/note.pw.html is outside generate.templates") {
		t.Fatalf("the unlisted template was not reported:\n%s", output.String())
	}
}

// The data pane lists a declared statement only if the package registering it
// is linked, and a statement declared before the handler that will call it is
// linked from nowhere. The development-only import is what puts it in reach.
func TestRunGenerateLinksQueryPackagesForDevelopment(t *testing.T) {
	root := queryFixtureProject(t)
	t.Chdir(root)
	if err := generateCode(context.Background(), &strings.Builder{}); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "cmd", "fixture", queryLinkFileName)
	source, err := os.ReadFile(link)
	if err != nil {
		t.Fatalf("the queries package was never linked: %v", err)
	}
	if !strings.HasPrefix(string(source), "//go:build pwdev") {
		t.Fatalf("the link must be absent from a release build:\n%s", source)
	}
	if !strings.Contains(string(source), `_ "example.test/fixture/queries"`) {
		t.Fatalf("the queries package is not imported:\n%s", source)
	}
}

// Generating twice must plan nothing the second time. api:cli-generate reports
// what it wrote and --check fails on what is left to write, so a planner that
// plans every file it could produce makes a project stale the moment it is
// generated.
func TestRunGenerateIsIdempotent(t *testing.T) {
	root := queryFixtureProject(t)
	t.Chdir(root)
	if err := generateCode(context.Background(), &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	var second strings.Builder
	if err := runCheck(context.Background(), nil, &second); err != nil {
		t.Fatalf("a freshly generated project was reported stale: %v\n%s", err, second.String())
	}
}

// A project generates from a clean checkout, where the packages its handlers
// import do not exist yet.
//
// Generated Go is not committed, so a fresh clone holds declarations and no
// output. Analysing a handler package type-checks it, which loads the query
// package this same run is about to write — so the two halves have an order,
// and the writing half has to reach disk before the analysing half looks. In
// one alphabetical pass "handlers" came first, failed to load "queries", and
// stopped the run before anything was written, which left running it again in
// exactly the same position.
func TestRunGenerateStartsFromNothingGenerated(t *testing.T) {
	root := queryFixtureProject(t)
	writeTestFile(t, filepath.Join(root, "handlers", "home.go"), `package handlers

import "example.test/fixture/queries"

var _ = queries.FindUser
`)
	t.Chdir(root)
	if err := generateCode(context.Background(), &strings.Builder{}); err != nil {
		t.Fatalf("a project with nothing generated could not be generated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "queries", "users_pw_gen.go")); err != nil {
		t.Fatalf("the query package the handlers import was never written: %v", err)
	}
}

// queryFixtureProject is a project holding both registries the development
// panes read: a template beside a handler, and a declared statement.
func queryFixtureProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"handlers", "queries", filepath.Join("cmd", "fixture")} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(root, "popcornweb.toml"),
		"[project]\nname = \"fixture\"\nmain = \"./cmd/fixture\"\n\n[generate]\n"+
			"handlers = [\"handlers\"]\ntemplates = [\"handlers\"]\nqueries = [\"queries\"]\nconfig = []\n")
	writeTestFile(t, filepath.Join(root, "cmd", "fixture", "main.go"), "package main\n\nfunc main() {}\n")
	writeTestFile(t, filepath.Join(root, "handlers", "home.pw.html"), `package handlers

export component Home(name: string): html {
<h1>Hello, {name}</h1>
}
`)
	writeTestFile(t, filepath.Join(root, "queries", "users.pw.sql"), `package queries

type User {
  id: int
  name: string
}

export statement FindUser(id: int): sql.one<User> {
SELECT id, name FROM users WHERE id = {id}
}
`)
	return root
}

// An absolute path buries the interesting part of a generation log, so the
// prefix the operator is already standing in comes off.
func TestChangePathsShortenAgainstTheWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "handlers"), 0o755); err != nil {
		t.Fatal(err)
	}
	changes := []fileChange{
		{path: filepath.Join(root, "handlers", "home_pw_gen.go")},
		{path: filepath.Join(root, "templates", "document_pw_gen.go")},
		{path: filepath.Join(t.TempDir(), "elsewhere_pw_gen.go")},
	}

	t.Chdir(root)
	paths := changePaths(root, changes)
	for _, want := range []string{"handlers/home_pw_gen.go", "templates/document_pw_gen.go"} {
		if !slices.Contains(paths, want) {
			t.Fatalf("paths = %#v, want %q relative to the project root", paths, want)
		}
	}
	// A file the operator could not find from here keeps its absolute form.
	if !slices.ContainsFunc(paths, filepath.IsAbs) {
		t.Fatalf("paths = %#v, want the outside file left absolute", paths)
	}

	// From a subdirectory, its own files shorten to bare names and the rest
	// still shortens against the root.
	t.Chdir(filepath.Join(root, "handlers"))
	paths = changePaths(root, changes)
	if !slices.Contains(paths, "home_pw_gen.go") {
		t.Fatalf("paths = %#v, want a bare name for the working directory", paths)
	}
	if !slices.Contains(paths, "templates/document_pw_gen.go") {
		t.Fatalf("paths = %#v, want the root-relative form for a sibling", paths)
	}
}

// dynamoFixture is a package with a tagged type, a call that directs the codec,
// and one access-pattern declaration.
func writeDynamoFixture(t *testing.T, directory string) {
	t.Helper()
	writeFixtureModule(t, directory,
		"github.com/shibukawa/tinybind-go",
		"github.com/shibukawa/tinygodriver")
	writeTestFile(t, filepath.Join(directory, "reading.go"), `package fixture

import (
	"context"

	"github.com/shibukawa/tinybind-go/dynamobind"
)

type Reading struct {
	Sensor  string  `+"`dynamo:\"sensor,partitionkey\"`"+`
	At      int64   `+"`dynamo:\"at,sortkey\"`"+`
	Celsius float64 `+"`dynamo:\"celsius\"`"+`
}

func Store(ctx context.Context, reading Reading) error {
	return dynamobind.Store(ctx, "reading", reading)
}
`)
	// A declaration file carries no package line, unlike .pw.sql: the package
	// is the directory it sits in.
	writeTestFile(t, filepath.Join(directory, "readings.pw.dynamo"), `export statement ReadingsSince(sensor: string, from: int64): dynamo.many<Reading> {
  table reading
  key sensor = {sensor} and at > {from}
}
`)
	resolveFixtureModule(t, directory)
}

func TestPlanDirectoryGeneratesDynamoArtifacts(t *testing.T) {
	directory := t.TempDir()
	writeDynamoFixture(t, directory)

	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	purposes := generationPurposes{handlers: true, dynamo: true}
	changes, err := planDirectory(context.Background(), generator.New(options), directory, purposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var generated string
	for _, change := range changes {
		if strings.Contains(string(change.source), "ReadingsSince") {
			generated = string(change.source)
		}
	}
	if generated == "" {
		t.Fatalf("no artifact carried the declared query; planned %d files", len(changes))
	}
	// The whole point of the declaration: the call site names neither the
	// client nor the table, and no attribute string survives into it.
	if !strings.Contains(generated, "func ReadingsSince(ctx context.Context, sensor string, from int64") {
		t.Fatalf("generated signature is not context-first:\n%s", generated)
	}
	// A reserved word cannot reach an expression, because every attribute is
	// aliased whether or not it needs to be.
	if !strings.Contains(generated, `"#k0": "sensor"`) {
		t.Fatalf("attributes must be aliased unconditionally:\n%s", generated)
	}
}

func TestPlanDirectoryLeavesDynamoSourcesUnreadWithoutThePurpose(t *testing.T) {
	directory := t.TempDir()
	writeDynamoFixture(t, directory)
	// The declaration names a table and a type that exist, so anything the run
	// produces for it would be valid. It must still produce nothing: an
	// unlisted source is not parsed, which is what keeps a deliberate sample
	// beside the code from being generated from.
	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := planDirectory(context.Background(), generator.New(options), directory,
		generationPurposes{handlers: true}, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range changes {
		if strings.Contains(string(change.source), "ReadingsSince") {
			t.Fatalf("a directory outside generate.dynamo generated a query:\n%s", change.source)
		}
	}
}

func TestPlanDirectoryRegistersTheGeneratedTable(t *testing.T) {
	directory := t.TempDir()
	writeDynamoFixture(t, directory)

	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := planDirectory(context.Background(), generator.New(options), directory,
		generationPurposes{handlers: true, dynamo: true}, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var registration string
	for _, change := range changes {
		if strings.Contains(string(change.source), "RegisterTable") {
			registration = string(change.source)
		}
	}
	if registration == "" {
		t.Fatal("a generated table definition must register itself, or pw migrate creates nothing")
	}
	// The declared name is the snake_case of the type, which is what a
	// .pw.dynamo table clause and an item call both name.
	if !strings.Contains(registration, `dynamo.RegisterTable("reading", ReadingTable)`) {
		t.Fatalf("registration does not name the declared table:\n%s", registration)
	}
}

func TestDeclaredTableNameIsSnakeCase(t *testing.T) {
	for typeName, want := range map[string]string{
		"Reading":       "reading",
		"UserSession":   "user_session",
		"OAuthState":    "o_auth_state",
		"ID":            "id",
		"HTTPRequestID": "http_request_id",
	} {
		if got := declaredTableName(typeName); got != want {
			t.Errorf("declaredTableName(%q) = %q, want %q", typeName, got, want)
		}
	}
}

// writeFixtureModule writes a go.mod for a fixture package, requiring the
// modules it imports at the versions this repository does.
//
// A fixture whose imports do not resolve is one where no call site can be
// discovered, so a usage-directed generator sees nothing to generate. That is
// not a property of the code under test. The versions are read from this module
// rather than pinned here, so the fixture cannot drift from it.
func writeFixtureModule(t *testing.T, directory string, modules ...string) {
	t.Helper()
	own, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	source := "module fixture\n\ngo 1.26.0\n"
	for _, module := range modules {
		version := ""
		for _, line := range strings.Split(string(own), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == module {
				version = fields[1]
				break
			}
		}
		if version == "" {
			t.Fatalf("go.mod does not require %s", module)
		}
		source += "\nrequire " + module + " " + version + "\n"
	}
	writeTestFile(t, filepath.Join(directory, "go.mod"), source)
}

// resolveFixtureModule fills in the fixture's go.sum.
//
// It runs after the sources exist, because tidy prunes a requirement no file
// imports: run against an empty directory it would remove everything
// writeFixtureModule just asked for. Everything is already in the module cache,
// because this repository requires it, so resolution needs no network.
func resolveFixtureModule(t *testing.T, directory string) {
	t.Helper()
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = directory
	tidy.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy in the fixture: %v\n%s", err, output)
	}
}

// firestoreFixture is a package with a firestore-tagged type, a call that
// directs the codec, and one access-pattern declaration.
//
// The type carries a ttl tag, because the fact this store has to publish and
// the DynamoDB one does not is which property a deployment points its expiry
// policy at.
func writeFirestoreFixture(t *testing.T, directory string) {
	t.Helper()
	// The module has to resolve its imports, or no call site is discoverable
	// and the analysis reports that rather than quietly generating the read
	// half of a codec.
	writeFixtureModule(t, directory,
		"github.com/shibukawa/tinybind-go",
		"github.com/shibukawa/tinygodriver")
	writeTestFile(t, filepath.Join(directory, "reading.go"), `package fixture

import (
	"context"
	"time"

	"github.com/shibukawa/tinybind-go/firestorebind"
	"github.com/shibukawa/tinygodriver/nosql/datastore"
)

type Reading struct {
	ID        string    `+"`firestore:\"-,name\"`"+`
	Sensor    string    `+"`firestore:\"sensor\"`"+`
	At        time.Time `+"`firestore:\"at\"`"+`
	Celsius   float64   `+"`firestore:\"celsius\"`"+`
	ExpiresAt time.Time `+"`firestore:\"expires_at,ttl\"`"+`
}

func Load(ctx context.Context, key datastore.Key) (Reading, error) {
	return firestorebind.Load[Reading](ctx, key)
}

func Store(ctx context.Context, reading Reading) (datastore.Key, error) {
	return firestorebind.Store(ctx, reading)
}
`)
	// A declaration file carries no package line, and it names no kind: the
	// result type names the Go type, and that type's Kind method is the kind.
	writeTestFile(t, filepath.Join(directory, "readings.pw.firestore"), `export statement ReadingsBySensor(sensor: string): firestore.many<Reading> {
  where sensor == {sensor}
}
`)
	resolveFixtureModule(t, directory)
}

func TestPlanDirectoryGeneratesFirestoreArtifacts(t *testing.T) {
	directory := t.TempDir()
	writeFirestoreFixture(t, directory)

	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	// A records directory is a Firestore directory and nothing else, which is
	// what the scaffold writes and what lets the whole codec be emitted.
	purposes := generationPurposes{firestore: true}
	changes, err := planDirectory(context.Background(), generator.New(options), directory, purposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The codec and the query are separate artifacts, because each is named
	// after the source it came from, so what is asserted is the run rather than
	// one file.
	var generated string
	for _, change := range changes {
		generated += string(change.source)
	}
	// The whole point of the declaration: the call site names neither the
	// client nor the kind, and no property string survives into it.
	if !strings.Contains(generated, "func ReadingsBySensor(ctx context.Context, sensor string") {
		t.Fatalf("generated signature is not context-first:\n%s", generated)
	}
	// The codec comes from the same run: a declared query over a type with no
	// codec would compile into nothing that can decode a result.
	if !strings.Contains(generated, "func (v Reading) EncodeEntity() datastore.Entity") {
		t.Fatalf("the entity codec was not generated:\n%s", generated)
	}
	// The key is lifted out of the properties: Datastore keeps identity beside
	// the entity, so writing it as a property too would store it twice.
	if !strings.Contains(generated, "func (v Reading) EntityKey() datastore.Key") {
		t.Fatalf("no key builder was generated:\n%s", generated)
	}
	// A ttl tag changes no bytes and produces one fact, which is what the
	// published policy list is built from.
	if !strings.Contains(generated, `func (v Reading) ExpiryProperty() (string, bool) { return "expires_at", true }`) {
		t.Fatalf("the expiry property was not declared:\n%s", generated)
	}
}

// A generated kind registers itself, because the list a deployment applies its
// TTL policies from is only knowable from the linked code.
func TestGeneratedFirestoreKindsRegisterThemselves(t *testing.T) {
	directory := t.TempDir()
	writeFirestoreFixture(t, directory)

	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := planDirectory(context.Background(), generator.New(options), directory,
		generationPurposes{firestore: true}, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var registration string
	for _, change := range changes {
		if strings.Contains(string(change.source), "RegisterKind") {
			registration = string(change.source)
		}
	}
	if registration == "" {
		t.Fatal("a generated kind must register itself, or the published policy list omits it")
	}
	if !strings.Contains(registration, "firestore.RegisterKind(Reading{})") {
		t.Fatalf("registration does not name the kind:\n%s", registration)
	}
	if strings.Count(registration, "RegisterKind(Reading{})") != 1 {
		t.Fatalf("a kind registered more than once:\n%s", registration)
	}
}

// An unlisted source is not parsed. That is what keeps a deliberate sample
// beside the code from being generated from, and it has to hold per store: a
// project that lists a directory for one has not listed it for the other.
func TestPlanDirectoryLeavesFirestoreSourcesUnreadWithoutThePurpose(t *testing.T) {
	directory := t.TempDir()
	writeFirestoreFixture(t, directory)

	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := planDirectory(context.Background(), generator.New(options), directory,
		generationPurposes{handlers: true, dynamo: true}, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range changes {
		if strings.Contains(string(change.source), "ReadingsBySensor") {
			t.Fatalf("an unlisted declaration was generated from:\n%s", change.source)
		}
	}
}

// constrainNetHTTP decides per file and is safe to run over its own output. The
// build constraint it writes has to be one the toolchain recognizes, which is a
// question about placement and the blank line after it rather than about the
// text, so the assertion goes through go/build/constraint rather than comparing
// strings.
func TestConstrainNetHTTPMarksOnlyTransportFilesAndOnlyOnce(t *testing.T) {
	transport := []byte("// Code generated by Popcorn Web via TinyBind; DO NOT EDIT.\n\npackage fixture\n\nimport \"net/http\"\n\nfunc h(w http.ResponseWriter, r *http.Request) {}\n")
	plain := []byte("// Code generated by Popcorn Web via TinyBind; DO NOT EDIT.\n\npackage fixture\n\nimport \"strconv\"\n\nvar _ = strconv.Itoa\n")

	// A project that declared no second build gets its bytes back untouched,
	// whatever the file imports.
	for _, source := range [][]byte{transport, plain} {
		got, err := constrainNetHTTP(source, false)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, source) {
			t.Error("a project without the fasthttp build had its generated source rewritten")
		}
	}

	marked, err := constrainNetHTTP(transport, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(marked, []byte(netHTTPConstraint)) {
		t.Fatalf("net/http file was not constrained:\n%s", marked)
	}
	// Idempotence is what makes pw check meaningful: the file on disk
	// already carries the constraint, and planning must produce those same bytes
	// rather than stacking a second one onto them.
	again, err := constrainNetHTTP(marked, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, marked) {
		t.Error("constraining an already-constrained file changed it")
	}

	untouched, err := constrainNetHTTP(plain, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(untouched, plain) {
		t.Error("a generated file importing no net/http was constrained out of the fasthttp build")
	}

	// The result must still be Go, and its first line must be a build
	// constraint the toolchain reads rather than a comment that looks like one.
	if _, err := parser.ParseFile(token.NewFileSet(), "marked.go", marked, parser.AllErrors); err != nil {
		t.Fatalf("constrained source no longer parses: %v", err)
	}
	first, _, _ := bytes.Cut(marked, []byte("\n"))
	if !constraint.IsGoBuild(string(first)) {
		t.Fatalf("first line is not a build constraint: %q", first)
	}
	expression, err := constraint.Parse(string(first))
	if err != nil {
		t.Fatal(err)
	}
	if expression.Eval(func(tag string) bool { return tag == "fasthttp" }) {
		t.Error("the constraint admits the file into the fasthttp build")
	}
	if !expression.Eval(func(string) bool { return false }) {
		t.Error("the constraint excludes the file from an ordinary net/http build")
	}
}

// The generator emits the constraint itself once a backend is selected, and it
// writes it below the generated-code header rather than above it. Adding a
// second one produces a file with two //go:build lines, which does not compile,
// so this framework defers to whatever is already there.
func TestAConstraintTheGeneratorAlreadyEmittedIsNotDuplicated(t *testing.T) {
	// The upstream layout: header first, constraint second. A check for a
	// leading constraint passes this and then breaks the file.
	upstream := []byte("// Code generated by tinybind; DO NOT EDIT.\n\n//go:build !fasthttp\n\npackage fixture\n\nimport \"net/http\"\n\nfunc h(w http.ResponseWriter, r *http.Request) {}\n")
	got, err := constrainNetHTTP(upstream, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, upstream) {
		t.Errorf("the source was rewritten:\n%s", got)
	}
	if n := bytes.Count(got, []byte("//go:build")); n != 1 {
		t.Errorf("file carries %d build constraints, want 1:\n%s", n, got)
	}
	// The same is true of the tag for the other side of the pair, which a
	// leading-prefix check would also have missed.
	including := bytes.Replace(upstream, []byte("!fasthttp"), []byte("fasthttp"), 1)
	got, err = constrainNetHTTP(including, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, including) {
		t.Errorf("a file constrained to the other build was rewritten:\n%s", got)
	}
}

// buildConstraint reads the header and stops at the package clause, so
// something further down that looks like a constraint is not mistaken for one.
func TestBuildConstraintReadsOnlyTheHeader(t *testing.T) {
	body := []byte("// Code generated; DO NOT EDIT.\n\npackage fixture\n\n// //go:build fasthttp\nconst s = \"//go:build fasthttp\"\n")
	if found, ok := buildConstraint(body); ok {
		t.Errorf("found a constraint in the body: %q", found)
	}
	header := []byte("//go:build !fasthttp\n\npackage fixture\n")
	found, ok := buildConstraint(header)
	if !ok || found != "//go:build !fasthttp" {
		t.Errorf("buildConstraint = %q, %v", found, ok)
	}
}

// Merging rebuilds a file from its declarations, which loses the header the
// artifacts arrived with. A constraint in that header has to survive, or a file
// the other backend supplies for itself lands in both builds.
func TestMergingCarriesTheConstraintTheArtifactsArrivedWith(t *testing.T) {
	artifacts := []generator.Artifact{
		{PackageName: "fixture", Content: []byte("// Code generated by tinybind; DO NOT EDIT.\n\n//go:build !fasthttp\n\npackage fixture\n\nimport \"net/http\"\n\nfunc a(w http.ResponseWriter) {}\n")},
		{PackageName: "fixture", Content: []byte("// Code generated by tinybind; DO NOT EDIT.\n\n//go:build !fasthttp\n\npackage fixture\n\nfunc b() {}\n")},
	}
	merged, err := mergeArtifacts(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	found, ok := buildConstraint(merged)
	if !ok || found != "//go:build !fasthttp" {
		t.Fatalf("merged file lost its constraint (%q, %v):\n%s", found, ok, merged)
	}
	if n := bytes.Count(merged, []byte("//go:build")); n != 1 {
		t.Errorf("merged file carries %d constraints, want 1:\n%s", n, merged)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "merged.go", merged, parser.AllErrors); err != nil {
		t.Fatalf("merged file does not parse: %v\n%s", err, merged)
	}
	// And constraining it again is a no-op, so the write-if-changed comparison
	// stays stable rather than rewriting the file on every run.
	again, err := constrainNetHTTP(merged, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, merged) {
		t.Error("constraining an already-constrained merge changed it")
	}
}

// Two artifacts constrained to different builds do not belong in one file, and
// silently picking one would produce a file that is wrong under the other tag.
func TestMergingArtifactsWithDisagreeingConstraintsIsRefused(t *testing.T) {
	artifacts := []generator.Artifact{
		{PackageName: "fixture", Content: []byte("//go:build !fasthttp\n\npackage fixture\n\nfunc a() {}\n")},
		{PackageName: "fixture", Content: []byte("//go:build fasthttp\n\npackage fixture\n\nfunc b() {}\n")},
	}
	if _, err := mergeArtifacts(artifacts); err == nil {
		t.Fatal("artifacts for two different builds were merged into one file")
	} else if !strings.Contains(err.Error(), "build constraint") {
		t.Errorf("the error does not name the cause: %v", err)
	}
}

// A component declaring a script block produces both the reference and the file
// it names. The generated component records the asset URL either way, so
// dropping the bytes serves a page whose module answers 404 — which is the
// failure planProducedAssets already existed to have stopped for a conversion,
// reached by a second route.
func TestComponentScriptBlockWritesItsExtractedFile(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, filepath.Join(directory, "go.mod"), "module fixture\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(directory, "counter.pw.html"), `package fixture

export component Counter(label: string): html {
<script component>
export function setup(el) {
	return () => {};
}
</script>
<div>{label}</div>
}
`)

	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	runner := generator.New(withExtractedAssetDirs(options, directory))
	changes, err := planDirectory(context.Background(), runner, directory, allPurposes, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var script string
	for _, change := range changes {
		if strings.HasSuffix(change.path, ".js") {
			script = change.path
		}
	}
	if script == "" {
		var paths []string
		for _, change := range changes {
			paths = append(paths, filepath.Base(change.path))
		}
		t.Fatalf("no extracted script was planned; planned %v", paths)
	}
	// Under the public tree the reference resolves against, not beside the Go.
	wanted := filepath.Join(directory, filepath.FromSlash(extractedAssetDir))
	if filepath.Dir(script) != wanted {
		t.Errorf("script written to %s, want it under %s", filepath.Dir(script), wanted)
	}

	generated, ok := changesByBase(changes)["counter_pw_gen.go"]
	if !ok {
		t.Fatal("no component was generated")
	}
	// The reference and the file agree, which is the whole point: the URL the
	// component records must be the file that was written.
	if !strings.Contains(string(generated.source), filepath.Base(script)) {
		t.Errorf("the component references no file named %s:\n%s", filepath.Base(script), generated.source)
	}
}

// cacheKeyFixtureDirectory holds a handler that passes a marked struct to the
// data cache. It lives in the main module rather than in a temporary one,
// because discovery needs every import of the call site to resolve and a
// standalone fixture module would have to carry the framework's whole
// dependency closure to offer that.
const cacheKeyFixtureDirectory = "../pwgen/testdata/wrappers"

// A pw.Memo call is what makes a marked struct a key type, so pw generate has
// to emit its method. Until this was wired the type compiled only after
// somebody wrote the method by hand, which is the work generation removes.
func TestAMemoCallGeneratesItsKeyMethod(t *testing.T) {
	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := planDirectory(context.Background(), generator.New(options), cacheKeyFixtureDirectory,
		generationPurposes{handlers: true}, nil, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var emitted string
	for _, change := range changes {
		if filepath.Base(change.path) == cacheKeyFileName {
			emitted = string(change.source)
		}
	}
	if emitted == "" {
		t.Fatalf("no %s was planned; a key type reached the cache and got no method", cacheKeyFileName)
	}
	for _, want := range []string{
		"func (v itemSummary) CacheKey() string",
		"var _ cachekeybind.CacheKey = itemSummary{}",
		"cachekeybind.KeyString(v.ItemID)",
		"cachekeybind.KeyInt(v.Page)",
	} {
		if !strings.Contains(emitted, want) {
			t.Errorf("emitted method does not contain %q:\n%s", want, emitted)
		}
	}
	// Marking is opt-in, so an unmarked field is the result rather than the
	// query and stays out of the key.
	for _, payload := range []string{"v.Title", "v.Total"} {
		if strings.Contains(emitted, payload) {
			t.Errorf("unmarked field %s reached the key:\n%s", payload, emitted)
		}
	}
}

// Planning must not write. A --check run reports staleness and leaves the tree
// alone, and cache keys are the one pass whose generator generates by writing a
// file, so it is planned through a temporary directory it discards.
func TestPlanningCacheKeysWritesNothing(t *testing.T) {
	options, err := pwgen.Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cacheKeyFixtureDirectory, cacheKeyFileName)
	if _, err := planDirectory(context.Background(), generator.New(options), cacheKeyFixtureDirectory,
		generationPurposes{handlers: true}, nil, nil, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		os.Remove(target)
		t.Fatalf("planning wrote %s into the source tree", target)
	}
}

// A directory whose sources never name an entry point must not pay for the
// second package load the analysis costs.
func TestADirectoryWithNoMemoCallPlansNoCacheKeys(t *testing.T) {
	directory := t.TempDir()
	writeFirestoreFixture(t, directory)

	mentions, err := mentionsCacheEntryPoint(directory)
	if err != nil {
		t.Fatal(err)
	}
	if mentions {
		t.Fatal("a directory with no cache call was reported as having one")
	}
}
