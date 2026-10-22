package pwcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/shibukawa/popcornweb/internal/pwgen"
	"github.com/shibukawa/tinybind-go/generator"
	tbparser "github.com/shibukawa/tinybind-go/parser"
	"github.com/shibukawa/tinybind-go/routetree"
	"golang.org/x/mod/modfile"
)

const checkUsage = "usage: pw check"

// runCheck reports generated Go that is stale or missing and writes nothing.
//
// It verifies less than pw generate writes, and deliberately: the asset tree
// and the stylesheet are excluded from version control, so there is no
// committed content to compare them against. Passing here means the generated
// Go matches its sources, not that the tree compiles.
//
// It takes no flags. A project kind is not one either — a package project is
// where this command is the release gate, so it runs there like anywhere else.
func runCheck(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("check: unexpected argument %q; %s", args[0], checkUsage)
	}
	// Invoked directly, the path list is the whole answer, so it is printed.
	_, err := generateProject(ctx, true, stdout, true)
	return err
}

// generateProject runs generation and reports how many files it wrote.
// listPaths false means the caller has its own report: a generated path names a
// build input the operator never opens, so a command that ends by saying what it
// did should name the sources it wrote instead and count these. Diagnostics go
// to stdout either way, because a warning is never what a report is hiding.
func generateProject(ctx context.Context, check bool, stdout io.Writer, listPaths bool) (int, error) {
	root, err := projectRoot(".")
	if err != nil {
		return 0, err
	}
	config, err := loadProjectConfig(root)
	if err != nil {
		return 0, err
	}
	directories, err := packageDirectories(root, config.Generate)
	if err != nil {
		return 0, err
	}
	// data:route-table is filled from the analysis each directory's generation
	// already performs, and written once at the end.
	collected := newRouteCollector(root)
	if err := reportSourcesOutsideScope(root, config, stdout); err != nil {
		return 0, err
	}
	// The message package is generated before anything compiles a template,
	// because a reference is type-checked against its symbols and the
	// diagnostic degrades to an unresolved name if they are not there yet.
	messageChanges, messages, err := planMessages(root, config, nil, stdout)
	if err != nil {
		return 0, err
	}
	// The page trees are generated first because their output is planned as part
	// of the directory it lands in, and a tree root may hold no source the walk
	// above would have found.
	pageArtifacts, pageActions, err := planPageTreeFiles(root, config, messages)
	if err != nil {
		return 0, err
	}
	directories, err = withPageDirectories(directories, pageArtifacts)
	if err != nil {
		return 0, err
	}
	options, err := pwgen.Options(engineFor(config.Database).SQLDialect)
	if err != nil {
		return 0, err
	}
	// requirement:template-source-positions. Read from the project rather than
	// from a flag, so every run of every command produces the same bytes and
	// api:cli-check has something stable to compare against.
	options.TemplateLineDirectives = config.LineDirectives
	// requirement:api-cbor-representation, read from the project on the same
	// terms. The profile is part of the generation fingerprint downstream, so
	// two machines disagreeing on it show up as drift rather than as two
	// protocols.
	options.EnableCBORHTTP = config.CBOR.Enabled
	options.CBORHTTPProfile = generator.CBORHTTPProfile{
		RejectFloats:      config.CBOR.RejectFloats,
		RequireSortedKeys: config.CBOR.SortedKeys,
	}
	// The implicit bindings are declared whether or not the project has a
	// catalog, because a binding no template reads generates byte-identical Go
	// and making the list conditional would make output differ by whether i18n
	// had been adopted yet.
	pwgen.ApplyMessages(&options, messages.Symbols, messages.ImportPath)
	// A conversion produces files and rewrites the reference that names them,
	// so it belongs to generation rather than to the asset build that runs
	// after it. The produced files are staged outside the served tree, which is
	// what lets that tree be cleared and rebuilt without deleting them.
	if config.Assets.Scripts {
		// Before a single file is generated: the build emits a module, and a
		// module under a classic script tag is a page that renders and silently
		// loses its script. It runs here rather than in the asset build so that
		// pw generate reports it too, and so that a --check run sees it.
		if err := verifyScriptModuleTags(root); err != nil {
			return 0, err
		}
	}
	options = withExtractedAssetDirs(options, root, config.Assets)
	if hooks := assetReferenceHooks(root, config.Assets); len(hooks) > 0 {
		staging := filepath.Join(root, filepath.FromSlash(config.Assets.locations().stage))
		if !check {
			// The staging directory is cleared first, because the asset build
			// copies everything it finds there into the served tree. A file
			// produced for a source that has since been deleted would otherwise
			// keep being shipped, with a manifest entry and a URL, forever.
			//
			// Clearing it costs nothing: the conversion cache is separate, so a
			// run replays its outcomes instead of re-encoding them.
			if err := os.RemoveAll(staging); err != nil {
				return 0, fmt.Errorf("clear %s: %w", derivedStageDir, err)
			}
		}
		options.ReferenceHooks = hooks
		options.DerivedAssetDir = staging
		options.ConversionCacheDir = filepath.Join(root, filepath.FromSlash(config.Assets.locations().cache))
		options.ConversionWorkers = conversionWorkers()
	}
	// The derivation is opt-in and rides the same options the generation does, so
	// a project that declared no second build runs none of it, and a project that
	// did cannot have the two disagree about what a pw call means or about which
	// files are this run's own output.
	if config.FastHTTP {
		transform := pwgen.FastTransform(options.Calls.Set)
		options.Transform = &transform
	}
	runner := generator.New(options)
	changes := messageChanges
	for index, stage := range splitByAnalysis(root, config.Generate, directories) {
		if len(stage) == 0 {
			continue
		}
		// What a stage is about to type-check has to be on disk, not in this
		// slice: loading a handler package reads the query package from the file
		// system, and a plan nobody has written is invisible to it.
		//
		// Check mode writes nothing and plans every stage against the tree as it
		// stands, which is the right answer there: a tree missing its generated
		// files is stale, and saying so is what check mode is for.
		if index > 0 && !check && len(changes) > 0 {
			if err := applyFileChanges(changes); err != nil {
				return 0, err
			}
		}
		planned, err := planStage(ctx, runner, root, config, stage, pageArtifacts, pageActions, collected)
		if err != nil {
			return 0, err
		}
		changes = append(changes, planned...)
	}
	// The second transport's page tree reads the derived handlers the stages
	// above produced, so it is planned after them and against the tree on disk.
	changes, err = planSecondBuildPages(root, config, check, changes, messages)
	if err != nil {
		return 0, err
	}
	// Declaring a package is what links it, so the declarations are resolved
	// before the bootstrap is planned. A declaration the module graph cannot
	// resolve stops generation here rather than producing a bootstrap that does
	// not compile.
	declared, err := resolvePackages(ctx, root, config.Packages)
	if err != nil {
		return 0, err
	}
	if err := checkPackageCompatibility(config, declared, nil); err != nil {
		return 0, err
	}
	changes, err = planBootstrapLink(root, config, declared, changes)
	if err != nil {
		return 0, err
	}
	// The storybook registers what the template generation produced, so it
	// reads the planned output rather than the disk. Reading the disk would
	// mean applying first, and check mode has to be able to report drift
	// without writing anything.
	changes, err = planStorybook(root, config, changes)
	if err != nil {
		return 0, err
	}
	changes, err = planQueryRegistry(root, config, changes)
	if err != nil {
		return 0, err
	}
	// Stable, because two directories referencing one image plan the same
	// derived asset path: equal paths must keep their planned order, so the
	// applied winner is the same one a serial run chose.
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].path < changes[j].path })
	if check && len(changes) > 0 {
		drift := changePaths(root, changes)
		return 0, fmt.Errorf("generated files are stale:\n  %s", strings.Join(drift, "\n  "))
	}
	if err := applyFileChanges(changes); err != nil {
		return 0, err
	}
	// After the writes, and not as one of them: the table is a build product
	// nothing links, like the asset manifest's JSON copy, so it is neither
	// compared by --check nor listed as a generated path.
	if err := writeRouteTable(root, config, collected); err != nil {
		return 0, err
	}
	paths := changePaths(root, changes)
	if listPaths {
		for _, path := range paths {
			fmt.Fprintln(stdout, path)
		}
	}
	return len(paths), nil
}

// withExtractedAssetDirs points the generator at where this project writes the
// style and script blocks a component declares, and at the URL they are
// referenced by.
//
// The URL is the generator's own default, which is this layout's public/generated
// tree under the default public mount, so a reference resolves with nothing
// configured. The directory is made absolute because generation runs per
// directory and the write happens far from the root.
//
// Both are set because the generator requires it: neither is derived from the
// other, so configuring one alone would leave the other on a default that no
// longer matches it.
//
// It is a function rather than two lines at the one call site because the
// fixture regeneration builds its own runner, and a setting present in only one
// of them is a fixture that stops driving the path it claims to drive.
func withExtractedAssetDirs(options generator.Options, root string, configs ...assetsConfig) generator.Options {
	var assets assetsConfig
	if len(configs) > 0 {
		assets = configs[0]
	}
	options.PublicDir = filepath.Join(root, filepath.FromSlash(assets.locations().extracted))
	options.PublicURLBase = generator.DefaultPublicURLBase
	return options
}

func planBootstrapLink(root string, config projectConfig, declared []resolvedPackage, changes []fileChange) ([]fileChange, error) {
	var documents []string
	err := walkSources(root, config.Generate.Templates, func(path string, entry fs.DirEntry) error {
		if entry.Name() == "document.pw.html" {
			documents = append(documents, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(documents) > 1 {
		return nil, fmt.Errorf("multiple default documents: %s", strings.Join(documents, ", "))
	}
	mainDirectory := filepath.Clean(filepath.Join(root, filepath.FromSlash(config.Main)))
	target := filepath.Join(mainDirectory, "popcornweb_bootstrap_pw_gen.go")
	filtered := changes[:0]
	for _, change := range changes {
		if change.path != target {
			filtered = append(filtered, change)
		}
	}
	hasPublic := false
	if info, err := os.Stat(filepath.Join(root, "public.go")); err == nil {
		hasPublic = !info.IsDir()
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if len(documents) == 0 && !hasPublic && len(declared) == 0 {
		if _, err := os.Stat(target); err == nil {
			filtered = append(filtered, fileChange{path: target, remove: true})
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		return filtered, nil
	}
	moduleSource, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	modulePath := modfile.ModulePath(moduleSource)
	if modulePath == "" {
		return nil, fmt.Errorf("go.mod does not declare a module path")
	}
	var imports []string
	if len(documents) == 1 {
		relativeDocumentDir, err := filepath.Rel(root, filepath.Dir(documents[0]))
		if err != nil {
			return nil, err
		}
		documentImport := modulePath
		if relativeDocumentDir != "." {
			documentImport += "/" + filepath.ToSlash(relativeDocumentDir)
		}
		imports = append(imports, documentImport)
	}
	if hasPublic && mainDirectory != filepath.Clean(root) {
		imports = append(imports, modulePath)
	}
	// A declared package is linked by this import and nothing else, which is
	// what makes the declaration the install. For a package the application also
	// imports itself — to call its Register — this is a harmless duplicate in a
	// separate file rather than the mechanism.
	for _, pkg := range declared {
		imports = append(imports, pkg.ImportPath())
	}
	sort.Strings(imports)
	imports = slicesCompact(imports)
	if len(imports) == 0 {
		if _, err := os.Stat(target); err == nil {
			filtered = append(filtered, fileChange{path: target, remove: true})
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		return filtered, nil
	}
	packageName, err := goPackageName(mainDirectory)
	if err != nil {
		return nil, err
	}
	var generated strings.Builder
	fmt.Fprintf(&generated, `// Code generated by Popcorn Web; DO NOT EDIT.

package %s

import (
`, packageName)
	for _, path := range imports {
		fmt.Fprintf(&generated, "\t_ %q\n", path)
	}
	generated.WriteString(")\n")
	source, err := format.Source([]byte(generated.String()))
	if err != nil {
		return nil, err
	}
	source, err = constrainNetHTTP(source, config.FastHTTP)
	if err != nil {
		return nil, err
	}
	current, readErr := os.ReadFile(target)
	if readErr == nil && bytes.Equal(current, source) {
		return filtered, nil
	}
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, readErr
	}
	return append(filtered, fileChange{path: target, source: source}), nil
}

func slicesCompact(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func goPackageName(directory string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") ||
			strings.HasSuffix(entry.Name(), "_pw_gen.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
		if err != nil {
			return "", err
		}
		return file.Name.Name, nil
	}
	return "", fmt.Errorf("no handwritten Go source in main package %s", directory)
}

// walkSources visits every file under the configured source directories. The
// generator reads nothing else in the project, so where generated code comes
// from is the popcornweb.toml list and not the shape of the directory tree.
func walkSources(root string, sources []string, visit func(path string, entry fs.DirEntry) error) error {
	for _, source := range sources {
		directory := filepath.Join(root, filepath.FromSlash(source))
		err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				switch entry.Name() {
				case ".git", ".pw", "vendor", "node_modules", ".devbox":
					return filepath.SkipDir
				}
				return nil
			}
			return visit(path, entry)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// generationInput reports whether a file name is something the generator reads.
func generationInput(name string) bool {
	return (strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")) ||
		strings.HasSuffix(name, ".pw.html") || strings.HasSuffix(name, ".pw.sql") ||
		strings.HasSuffix(name, ".pw.dynamo") || strings.HasSuffix(name, ".pw.firestore")
}

// directoryPurposes reports which generation purposes list a directory. A
// directory usually serves more than one, because a page template lives beside
// the handler that renders it.
//
// root and directory must be spelled the same way, which in practice means both
// absolute: a directory that resolves to no purpose is one whose generated files
// are swept as stale.
func directoryPurposes(root string, scope generationScope, directory string) generationPurposes {
	within := func(sources []string) bool {
		for _, source := range sources {
			if pathWithin(filepath.Join(root, filepath.FromSlash(source)), directory) {
				return true
			}
		}
		return false
	}
	return generationPurposes{
		handlers:  within(scope.Handlers),
		templates: within(scope.Templates),
		queries:   within(scope.Queries),
		config:    within(scope.Config),
		pages:     within(scope.Pages),
		dynamo:    within(scope.Dynamo),
		firestore: within(scope.Firestore),
	}
}

// generationPurposes is the set of purposes one directory belongs to.
type generationPurposes struct {
	handlers  bool
	templates bool
	queries   bool
	config    bool
	// pages marks a directory inside a page tree root. Its templates are
	// compiled by the tree run rather than the flat one, so what this enables
	// here is the request binding a server action needs.
	pages bool
	// dynamo marks a directory whose dynamo-tagged types and .pw.dynamo
	// declarations are generated for.
	dynamo bool
	// firestore marks the same for firestore-tagged types and .pw.firestore
	// declarations.
	firestore bool
}

// sources narrows the set to the purposes that read a template source, which
// is the half pwgen.StrayTemplateMessage decides on.
func (p generationPurposes) sources() pwgen.SourcePurposes {
	return pwgen.SourcePurposes{
		Templates: p.templates,
		Queries:   p.queries,
		Dynamo:    p.dynamo,
		Firestore: p.firestore,
		Pages:     p.pages,
	}
}

// any reports whether the directory serves any purpose at all.
func (p generationPurposes) any() bool {
	return p.handlers || p.templates || p.queries || p.config || p.pages || p.dynamo || p.firestore
}

// keeps maps an artifact back to the purpose that may produce it. Go analysis
// runs for the whole directory, so binding and configuration artifacts are
// selected here rather than at discovery.
func (p generationPurposes) keeps(kind generator.ArtifactKind) bool {
	switch kind {
	case generator.ArtifactBinding, generator.ArtifactTransport, generator.ArtifactTransportBinding:
		// A page tree gets binders so a server action can read a typed request,
		// but no OpenAPI: a rendered page is not a published contract, and an
		// action endpoint is one page's implementation detail.
		//
		// The second build's two halves follow the binders rather than the
		// handlers purpose alone, because a server action is a transport handler
		// living in a page tree and needs both for the same reason.
		return p.handlers || p.pages
	case generator.ArtifactTransportRoutes:
		// The routes an application registers itself. The authored wiring that
		// registers them — a mux built in an init, a HandleFunc per handler —
		// is net/http-shaped and excluded from the second build, so without
		// this that build compiles and serves nothing.
		//
		// A page tree is not this: it brings its own registry, emitted beside
		// the tree onto pwfastpage.Router. The two coexist for the same reason
		// they do on the first transport.
		return p.handlers
	case generator.ArtifactOpenAPI:
		return p.handlers
	case generator.ArtifactHTMLTemplate:
		return p.templates
	case generator.ArtifactSQLTemplate:
		return p.queries
	case generator.ArtifactConfigBind:
		return p.config
	case generator.ArtifactDynamoItem, generator.ArtifactDynamoQuery:
		return p.dynamo
	case generator.ArtifactFirestoreEntity, generator.ArtifactFirestoreQuery:
		return p.firestore
	case generator.ArtifactDerivedAsset:
		// A conversion is a consequence of compiling a template, so it belongs
		// to the purpose that reads templates. Dropping it here would write the
		// rewritten reference and discard the file it names.
		return p.templates || p.pages
	case generator.ArtifactStylesheet, generator.ArtifactScript:
		// A component's extracted style and script blocks, for the same reason
		// and with the same failure: the generated component records the asset
		// URL either way, so dropping the bytes here serves a page that
		// references a stylesheet and a module which both answer 404.
		//
		// They were missing rather than excluded. Extraction needs no
		// configuration — the generator defaults write under public/generated,
		// which is where this framework's public tree already looks — so the
		// only thing between a declared block and a working asset was this
		// switch not naming its kind.
		return p.templates || p.pages
	default:
		return false
	}
}

func packageDirectories(root string, scope generationScope) ([]string, error) {
	// The purpose lists overlap — handlers and templates commonly name the
	// same tree — and only the union of directories matters here, so the
	// roots are deduplicated and each walked once instead of once per purpose.
	seen := map[string]bool{}
	var roots []string
	for _, sources := range [][]string{
		scope.Handlers, scope.Templates, scope.Queries, scope.Config, scope.Pages, scope.Dynamo, scope.Firestore,
	} {
		for _, source := range sources {
			if !seen[source] {
				seen[source] = true
				roots = append(roots, source)
			}
		}
	}
	found := map[string]bool{}
	err := walkSources(root, roots, func(path string, entry fs.DirEntry) error {
		if generationInput(entry.Name()) {
			found[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(found))
	for directory := range found {
		out = append(out, directory)
	}
	sort.Strings(out)
	return out, nil
}

// splitByAnalysis divides the directories into the stages a run generates in.
// Each stage is written to disk before the next one is planned, because a later
// stage type-checks Go an earlier one produced.
//
// That order is what makes a clean checkout generate at all. A handler package
// imports the query package this run is about to produce, and analysing the
// handlers loads that import — so with one pass in alphabetical order,
// "handlers" is planned before "queries", finds a package holding no Go files,
// and fails. The run stops there, "queries" is never reached, and running it
// again changes nothing, because nothing was written. Only a working tree that
// still holds output from an earlier generation gets past it, which is why this
// survives on a machine that has built the project once and not on a fresh
// clone.
//
// A page tree is the same relationship one level up, and the one that reaches a
// project on its first day: a tree root holds templates and no Go at all, and
// the Register that mounts it is generated. The main package calls that
// Register, so analysing it loads a page package nothing has written yet. A
// scaffolded project is exactly this shape, so the failure lands on the first
// pw dev rather than on some later clean checkout.
func splitByAnalysis(root string, scope generationScope, directories []string) [][]string {
	var writes, pages, analyses []string
	for _, directory := range directories {
		purposes := directoryPurposes(root, scope, directory)
		switch {
		// A page tree publishes a package its callers import, so it is planned
		// before them and after the declaration purposes a page handler reads.
		case purposes.pages:
			pages = append(pages, directory)
		case purposes.handlers || purposes.config:
			analyses = append(analyses, directory)
		// The data-access purposes read their own declaration files and emit Go
		// without loading any, so they are the stage that can always run first.
		default:
			writes = append(writes, directory)
		}
	}
	return [][]string{writes, pages, analyses}
}

// strayReport is one source the purpose that owns its kind does not list.
type strayReport struct {
	path    string
	message string
}

// reportSourcesOutsideScope warns about framework sources their own purpose
// leaves out. They are reported rather than generated from, because a project
// may keep samples or fixtures beside its code on purpose; a Go file is never
// reported, since ordinary Go code lives throughout a project and a call site
// outside its purpose simply has no generated binding.
func reportSourcesOutsideScope(root string, config projectConfig, stdout io.Writer) error {
	bootstrap := filepath.Join(
		filepath.Clean(filepath.Join(root, filepath.FromSlash(config.Main))),
		"popcornweb_bootstrap_pw_gen.go",
	)
	var stray []strayReport
	// The purposes of a directory are the same for every file in it, so they
	// are computed once per directory rather than once per file of the walk.
	purposesByDir := map[string]generationPurposes{}
	purposesOf := func(directory string) generationPurposes {
		if cached, ok := purposesByDir[directory]; ok {
			return cached
		}
		computed := directoryPurposes(root, config.Generate, directory)
		purposesByDir[directory] = computed
		return computed
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".pw", "vendor", "node_modules", ".devbox":
				return filepath.SkipDir
			}
			// Another checkout of the project holds a copy of everything this
			// walk looks for, and none of it is listed in this configuration.
			if nestedWorktree(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if path == bootstrap {
			return nil
		}
		name := entry.Name()
		purposes := purposesOf(filepath.Dir(path))
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			relative = path
		}
		relative = filepath.ToSlash(relative)
		if message, isStray := pwgen.StrayTemplateMessage(relative, name, purposes.sources()); isStray {
			stray = append(stray, strayReport{relative, "pw: " + message})
			return nil
		}
		switch {
		case strings.HasSuffix(name, "_pw_gen.go") && !purposes.any():
			// The storybook harness is generated into a directory of its own
			// and belongs to no purpose by design, so it is not stray.
			if strings.HasPrefix(filepath.ToSlash(relative), storybookDirectory+"/") {
				return nil
			}
			// The asset manifest belongs to no purpose either, and cannot: it
			// is written by the asset build rather than by a generation run,
			// and it has to sit at the root because it is part of the package
			// public.go declares there. Reporting it made every rebuild print
			// a warning about a file the build had just written on purpose.
			if relative == config.Assets.locations().manifestGo {
				return nil
			}
			// The message package belongs to no purpose for the same reason:
			// i18n.catalog addresses its directory directly rather than being
			// discovered by a walk, so there is no purpose to list it under and
			// no way for it to become stale without the catalog changing.
			if config.I18n.Enabled() &&
				filepath.ToSlash(relative) == config.I18n.Catalog+"/"+messagesGeneratedName {
				return nil
			}
			stray = append(stray, strayReport{relative, fmt.Sprintf(
				"pw: %s was generated outside every generate purpose and is now stale; delete it or list its directory", relative)})
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(stray, func(i, j int) bool { return stray[i].path < stray[j].path })
	for _, report := range stray {
		fmt.Fprintln(stdout, report.message)
	}
	return nil
}

type fileChange struct {
	path   string
	source []byte
	remove bool
}

// appendIfChanged plans a write only where it would change the file.
//
// api:cli-generate reports what it wrote, and --check fails on anything left to
// write. A planner that plans every file it could produce, produced or not,
// makes both of those say a project is stale immediately after generating it.
func appendIfChanged(changes []fileChange, path string, source []byte) ([]fileChange, error) {
	// A requirement:template-source-positions directive restoring the
	// generated file's own position names a placeholder until the file has a
	// name, and only this layer has one: the generator states a suggested base
	// and the caller decides the suffix. Skipping this leaves every scaffolding
	// error pointing at a file that does not exist.
	//
	// It is a no-op on content carrying no directive, so it sits on the one
	// funnel every generated write already passes through rather than being
	// repeated at each of them.
	source = generator.ResolveTemplatePositions(source, filepath.Base(path))

	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, source) {
		return changes, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return append(changes, fileChange{path: path, source: source}), nil
}

// disabledTemplatePattern matches no file, and switches off one template kind
// for a directory the purpose that owns it does not list. Discovery is skipped
// rather than filtered afterwards, so an unlisted template is never parsed.
const disabledTemplatePattern = "*.not-a-generation-source"

// planDirectory plans every generated file of one directory. extra carries the
// artifacts another run produced for it, which today means the compiled
// components, decoder, and registry of a page tree: they are planned here so
// one directory has one staleness sweep, and so a component and a binder that
// derive the same base name merge into one file rather than deleting each
// other.
// planStage plans every directory of one stage.
//
// Directories with the config purpose run first, serially: the generator's
// help backfill writes tags back into authored config sources while it plans,
// and a handler package importing one could otherwise type-check a file
// mid-write. The remaining handler directories — where the type checking that
// dominates a generate run happens — plan concurrently, which the generator's
// documented contract admits for distinct directories; nothing else in a
// stage writes, because a stage's changes are applied between stages.
//
// Each directory's changes land in a slot of its own and are appended in the
// stage's order, so what a run produces does not depend on scheduling — two
// directories referencing one image both plan the derived asset, and the
// order of those duplicate changes must stay the serial order.
func planStage(ctx context.Context, runner *generator.Generator, root string, config projectConfig,
	stage []string, pageArtifacts map[string][]generator.Artifact,
	pageActions map[string][]routetree.Action, collected *routeCollector,
) ([]fileChange, error) {
	plan := func(directory string, set *generator.PackageSet) ([]fileChange, error) {
		return planDirectory(ctx, runner, directory, directoryPurposes(root, config.Generate, directory),
			pageArtifacts[directory], pageActions[directory], config.FastHTTP, collected, set)
	}
	var serial, concurrent []string
	for _, directory := range stage {
		purposes := directoryPurposes(root, config.Generate, directory)
		if purposes.handlers && !purposes.config {
			concurrent = append(concurrent, directory)
		} else {
			serial = append(serial, directory)
		}
	}
	var changes []fileChange
	for _, directory := range serial {
		planned, err := plan(directory, nil)
		if err != nil {
			return nil, err
		}
		changes = append(changes, planned...)
	}
	if len(concurrent) == 0 {
		return changes, nil
	}
	// One load for the whole batch — the dominant cost of a generate run was
	// each handler directory re-type-checking the framework closure it shares
	// with every other one. Built after the serial phase on purpose: the
	// config directories above backfill help tags into sources these packages
	// import, and a set built earlier would hand the batch types from before
	// that write. A directory the load could not place is simply absent from
	// the set and loads alone, with today's diagnostics.
	set, err := generator.LoadPackages(ctx, concurrent)
	if err != nil {
		return nil, err
	}
	results := make([][]fileChange, len(concurrent))
	failures := make([]error, len(concurrent))
	workers := min(conversionWorkers(), len(concurrent))
	jobs := make(chan int, len(concurrent))
	for index := range concurrent {
		jobs <- index
	}
	close(jobs)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range jobs {
				results[index], failures[index] = plan(concurrent[index], set)
			}
		}()
	}
	group.Wait()
	// The first failure in stage order, so a broken tree reports the same
	// directory however the work was scheduled.
	for _, err := range failures {
		if err != nil {
			return nil, err
		}
	}
	for _, planned := range results {
		changes = append(changes, planned...)
	}
	return changes, nil
}

func planDirectory(ctx context.Context, runner *generator.Generator, directory string, purposes generationPurposes, extra []generator.Artifact, typed []routetree.Action, fastHTTP bool, collected *routeCollector, set *generator.PackageSet) ([]fileChange, error) {
	goSources, err := hasGoSources(directory)
	if err != nil {
		return nil, err
	}
	if len(typed) > 0 {
		// A typed action's wrapper is emitted by this phase, because it names
		// the encoder this phase generates and the function it wraps. What
		// routetree read travels here rather than being read twice, so the
		// registration and the emitted entry point cannot name two symbols.
		local := *runner
		// The selection already happened, by directory; this one is by the
		// relative directory routetree reported, and they have to be the same
		// answer. Taking it from the actions themselves is what makes it one.
		local.Options = local.Options.WithServerActionsFor(typed, typed[0].RelDir)
		runner = &local
	}
	request := generator.GenerateRequest{
		Dir:                 directory,
		Packages:            set,
		OpenAPI:             goSources && purposes.handlers,
		SQLContextOnlyAPI:   true,
		HTMLTemplatePattern: disabledTemplatePattern,
		SQLTemplatePattern:  disabledTemplatePattern,
	}
	if purposes.templates {
		request.HTMLTemplatePattern = ""
	}
	if purposes.queries {
		request.SQLTemplatePattern = ""
	}
	if !purposes.dynamo || !purposes.firestore {
		// A request carries no store pattern, so an unlisted directory is kept
		// from being parsed by running against a copy of the generator whose
		// glob matches nothing. Filtering the artifacts afterwards would still
		// have read and type-checked the declaration.
		local := *runner
		if !purposes.dynamo {
			local.Options.DynamoTemplatePattern = disabledTemplatePattern
		}
		if !purposes.firestore {
			local.Options.FirestoreTemplatePattern = disabledTemplatePattern
		}
		runner = &local
	}
	// A source that does not parse is reported here rather than handed to the
	// generator. The developer loop regenerates the moment a file appears, so
	// it routinely sees one an editor has created and not yet written into, and
	// the generator walks such a file into a nil position.
	// The parser error already names the file, the line, and the column.
	if reason := firstUnparsableSource(directory); reason != nil {
		return nil, reason
	}
	if purposes.config {
		if err := pwgen.ValidateConfigDefaults(directory, runner.Options); err != nil {
			return nil, err
		}
	}
	artifacts, routes, err := generateArtifacts(ctx, runner, request)
	collected.add(directory, routes)
	if err != nil && !errors.Is(err, generator.ErrNothingToGenerate) {
		// A page tree route package usually holds no request model at all, so
		// finding nothing is the ordinary outcome rather than a failure.
		//
		// A refusal is the one failure worth rewording, because part of the
		// remedy is this framework's rather than the application's.
		return nil, transportRefusal(directory, err)
	}

	grouped := make(map[string][]generator.Artifact)
	// A page tree hands its output in as somebody else's, and not all of it is
	// Go: a template declaring a script block returns the extracted file too.
	// Grouping that with the rest would name it a _pw_gen.go and write
	// JavaScript into a Go file, which is the shape planProducedAssets exists to
	// have stopped.
	var extraAssets []generator.Artifact
	for _, artifact := range extra {
		if artifact.Destination == generator.DestinationPublicAsset {
			extraAssets = append(extraAssets, artifact)
			continue
		}
		target := filepath.Join(directory, artifact.OutputBase+"_pw_gen.go")
		grouped[target] = append(grouped[target], artifact)
		// A page tree's own templates are compiled by the tree run rather than
		// the flat one, so they reach here as somebody else's output. A
		// component declared reloadable in one still publishes an endpoint.
		if registration, declares := reloadableRegistrationArtifact(artifact); declares {
			grouped[target] = append(grouped[target], registration)
		}
	}
	// A produced file is placed before anything is grouped, because it is not Go
	// and the grouping below names every target it is handed a _pw_gen.go.
	produced, err := planProducedAssets(runner.Options.DerivedAssetDir, runner.Options.PublicDir, purposes,
		append(append([]generator.Artifact(nil), artifacts...), extraAssets...))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", directory, err)
	}
	for _, artifact := range artifacts {
		if !purposes.keeps(artifact.Kind) || artifact.Destination == generator.DestinationPublicAsset {
			continue
		}
		target := filepath.Join(directory, artifact.OutputBase+"_pw_gen.go")
		grouped[target] = append(grouped[target], artifact)
		if artifact.Kind == generator.ArtifactHTMLTemplate &&
			filepath.Base(artifact.SourcePath) == "document.pw.html" {
			grouped[target] = append(grouped[target], documentRegistrationArtifact(artifact.PackageName))
		}
		if registration, declares := reloadableRegistrationArtifact(artifact); declares {
			grouped[target] = append(grouped[target], registration)
		}
		if artifact.Kind == generator.ArtifactDynamoItem {
			if registration, declares := dynamoRegistrationArtifact(artifact); declares {
				grouped[target] = append(grouped[target], registration)
			}
		}
		if artifact.Kind == generator.ArtifactFirestoreEntity {
			if registration, declares := firestoreRegistrationArtifact(artifact); declares {
				grouped[target] = append(grouped[target], registration)
			}
		}
	}
	expected := make(map[string]bool, len(grouped))
	changes := produced
	for target, group := range grouped {
		expected[target] = true
		source, err := mergeArtifacts(group)
		if err != nil {
			return nil, err
		}
		// Before the comparison below, not after: the file on disk carries the
		// constraint, so a source without it would read as changed on every run
		// and --check would call a freshly generated project stale.
		kinds := make([]generator.ArtifactKind, 0, len(group))
		for _, artifact := range group {
			kinds = append(kinds, artifact.Kind)
		}
		source, err = constrainNetHTTP(source, fastHTTP, kinds...)
		if err != nil {
			return nil, err
		}
		// Here for the same reason, and for a second one: an artifact states a
		// suggested base and this layer chooses the name, so this is the first
		// point at which a restore directive can name the file it is in.
		source = generator.ResolveTemplatePositions(source, filepath.Base(target))
		current, readErr := os.ReadFile(target)
		if readErr == nil && bytes.Equal(current, source) {
			continue
		}
		if readErr != nil && !os.IsNotExist(readErr) {
			return nil, readErr
		}
		changes = append(changes, fileChange{path: target, source: source})
	}
	// Cache key methods are planned here rather than merged above, because they
	// arrive as finished source instead of as an artifact. Registering the
	// target in expected is what keeps the sweep below from deleting it, and
	// what makes it delete the file once the last key type is gone.
	cacheKeys, err := planCacheKeys(runner, directory)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", directory, err)
	}
	if len(cacheKeys) > 0 {
		target := filepath.Join(directory, cacheKeyFileName)
		expected[target] = true
		current, readErr := os.ReadFile(target)
		if readErr != nil && !os.IsNotExist(readErr) {
			return nil, readErr
		}
		if readErr != nil || !bytes.Equal(current, cacheKeys) {
			changes = append(changes, fileChange{path: target, source: cacheKeys})
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_pw_gen.go") {
			continue
		}
		// The storybook registration is generated by a later step, which has to
		// run after this one because it reads what this one produced. Sweeping
		// it here would delete on every run what that step writes on every run.
		// The second transport's page tree is the same relationship: it reads the
		// derived handlers planned above, so its own step sweeps it.
		if entry.Name() == storybookFileName || entry.Name() == queryRegistryFileName ||
			entry.Name() == queryLinkFileName || isFastPagePath(entry.Name()) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		if !expected[path] {
			changes = append(changes, fileChange{path: path, remove: true})
		}
	}
	return changes, nil
}

// planProducedAssets turns the files a conversion produced into writes into the
// derived asset directory.
//
// The generator has two APIs and they divide this differently. The one that
// writes files places these itself; the one this project uses returns artifacts,
// and taking artifacts means taking the placement too. Nothing said so, and the
// caller grouped them with the Go artifacts by output base: a bundle was written
// as templates/js/app.<hash>.js_pw_gen.go, a Go file holding JavaScript that the
// next run refused to parse, while the file the rewritten reference names was
// never written at all. A page referencing a TypeScript entry served a script
// URL that answered 404.
//
// The bytes are compared before a write is planned, exactly as a generated Go
// file is, so a --check run on a tree that is already built reports nothing.
func planProducedAssets(derivedDir, publicDir string, purposes generationPurposes, artifacts []generator.Artifact) ([]fileChange, error) {
	var changes []fileChange
	planned := map[string]bool{}
	for _, artifact := range artifacts {
		if artifact.Destination != generator.DestinationPublicAsset || !purposes.keeps(artifact.Kind) {
			continue
		}
		// Two shapes reach here and they differ in both halves. A conversion
		// owns its own naming, so its output base carries the extension already
		// and it is staged outside the served tree. An extracted block is named
		// by the generator as a base and an extension, and it belongs in the
		// public tree its URL was computed against.
		directory, name := derivedDir, filepath.FromSlash(artifact.OutputBase)
		if artifact.Kind == generator.ArtifactStylesheet || artifact.Kind == generator.ArtifactScript {
			directory = publicDir
			if artifact.Extension != "" {
				name += "." + artifact.Extension
			}
		}
		if directory == "" {
			// Discarding it would leave the rewritten reference naming nothing,
			// which is the failure this function exists to have stopped.
			return nil, fmt.Errorf("a conversion produced %s but no output directory is set", artifact.OutputBase)
		}
		target := filepath.Join(directory, name)
		// One conversion is replayed for every occurrence of the value it
		// converted, so the same file arrives once per reference.
		if planned[target] {
			continue
		}
		planned[target] = true
		current, err := os.ReadFile(target)
		if err == nil && bytes.Equal(current, artifact.Content) {
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		changes = append(changes, fileChange{path: target, source: artifact.Content})
	}
	return changes, nil
}

// cacheKeyFileName carries the cache key methods of one package. It takes this
// framework's generated suffix rather than the module's own default, so the
// doctor scan, the staleness sweep, and the stray report all recognize it the
// way they recognize every other generated file.
const cacheKeyFileName = "cachekey_pw_gen.go"

// planCacheKeys returns the cache key methods for directory, or nothing when no
// type in it is passed to the cache.
//
// The generator writes its output while this package plans it, and the
// difference matters: a --check run reports staleness and must leave the tree
// alone. So the run is pointed at a temporary directory and the bytes are read
// back out of it. That costs one file write nobody reads, and it buys the file
// the same comparison, change report, and staleness sweep every other generated
// file gets.
//
// The text pre-check is not an optimization detail. Cache key analysis loads
// the package a second time, which the develop loop would otherwise pay on
// every keystroke in every directory; a call site has to name one of the entry
// points, and every one of them contains "Memo", so a directory whose sources
// never spell it cannot hold one.
func planCacheKeys(runner *generator.Generator, directory string) ([]byte, error) {
	mentions, err := mentionsCacheEntryPoint(directory)
	if err != nil || !mentions {
		return nil, err
	}
	out, err := os.MkdirTemp("", "pw-cachekey")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(out)
	path, err := runner.GenerateCacheKeys(directory, out, cacheKeyFileName)
	if err != nil || path == "" {
		return nil, err
	}
	return os.ReadFile(path)
}

// mentionsCacheEntryPoint reports whether any Go source in directory spells a
// cache entry point. An import alias renames the package and never the
// function, so the name itself is always present at the call site.
func mentionsCacheEntryPoint(directory string) (bool, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_pw_gen.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return false, err
		}
		if bytes.Contains(source, []byte("Memo")) {
			return true, nil
		}
	}
	return false, nil
}

// firstUnparsableSource names the first Go file in directory that does not
// parse, with the reason. Only the package clause is read: anything further is
// the compiler's to report, and a file that is being written into right now
// fails at the very first token anyway.
func firstUnparsableSource(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	fileset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		path := filepath.Join(directory, name)
		if _, err := parser.ParseFile(fileset, path, nil, parser.PackageClauseOnly); err != nil {
			return err
		}
	}
	return nil
}

// generateArtifacts runs one generation request and turns a panic inside the
// generator into an error. The developer loop is meant to survive a
// half-finished edit, and a panic escaping from here would take the loop, the
// application it supervises, and the services it started down with it.
func generateArtifacts(ctx context.Context, runner *generator.Generator, request generator.GenerateRequest) (artifacts []generator.Artifact, routes *tbparser.Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("the generator panicked on this directory, which is a defect rather than "+
				"something to fix in the sources: %v", recovered)
		}
	}()
	// The route half comes back from the run that was happening anyway. It is
	// the analysis api:cli-generate already performs for the OpenAPI document,
	// and taking it here is what keeps data:route-table from being a second
	// one, per rule:static-route-discovery.
	return runner.GenerateArtifactsWithRoutes(ctx, request)
}

func documentRegistrationArtifact(packageName string) generator.Artifact {
	return generator.Artifact{
		Kind:        generator.ArtifactHTMLTemplate,
		OutputBase:  "document",
		PackageName: packageName,
		// The shared leaf rather than either runtime. The registry is one
		// process-wide table both transports read, so a file naming pw here
		// would put the net/http runtime into the second build and register
		// the document in the half that is not serving.
		Content: []byte("package " + packageName + `

import "github.com/shibukawa/popcornweb/pwruntime"

func init() {
	pwruntime.RegisterHTMLDocument(BindDocument(DocumentParams{}))
}
`),
	}
}

// reloadableValue matches the registration value tinybind emits for a component
// carrying the @reloadable annotation. It is that declaration's one durable
// marker: the kind constant beside it is emitted for other reasons too, and the
// typed query decoder is a closure with no name of its own.
var reloadableValue = regexp.MustCompile(`(?m)^var ([A-Za-z0-9_]+Reloadable) = htmlupdate\.Reloadable\{`)

// reloadableRegistrationArtifact emits the init that publishes a generated
// component as a redraw endpoint. Without it a project would generate a
// registration value nothing ever reads, and every redraw would answer 404.
//
// It is derived from the generated source rather than from a second reading of
// the template, so what publishes an endpoint is exactly what the annotation
// produced. The declaration in the .pw.html source is the deliberate act;
// nothing here decides to publish anything.
//
// The init runs because something linked the package in, which is the right
// condition rather than a gap: a reloadable component is reached from a page or
// a handler that imports it, and one no build ever links is one no rendered
// element carries the kind of, so nothing could address it anyway.
//
// The registration error is discarded at the call site because an init has
// nowhere to return one to, and panicking would end the process before there is
// any logging to say which component collided. pw keeps it and answers it in
// startup validation instead. The explanation lives here rather than in the
// emitted body: merging artifacts rebuilds the file from declarations, and a
// comment inside a function body does not survive that.
func reloadableRegistrationArtifact(artifact generator.Artifact) (generator.Artifact, bool) {
	if artifact.Kind != generator.ArtifactHTMLTemplate {
		return generator.Artifact{}, false
	}
	matches := reloadableValue.FindAllStringSubmatch(string(artifact.Content), -1)
	if len(matches) == 0 {
		return generator.Artifact{}, false
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match[1])
	}
	sets := reloadableSets(artifact)
	var body strings.Builder
	// The shared leaf rather than either runtime, for the same reason the
	// document registration names it: one registry, read by both.
	body.WriteString("package " + artifact.PackageName + "\n\nimport (\n\t\"github.com/shibukawa/popcornweb/pwruntime\"\n")
	if len(sets) > 0 {
		body.WriteString("\t\"github.com/shibukawa/tinybind-go/htmlupdate\"\n")
	}
	body.WriteString(")\n")
	for _, set := range sets {
		fmt.Fprintf(&body, `
// PwReloadables is every reloadable component %s can render, itself included
// when it is one. It is what Redraw reads, so a handler names the page and
// never a list that could fall out of step with the markup.
func (%s) PwReloadables() []htmlupdate.Reloadable {
	return []htmlupdate.Reloadable{%s}
}
`, set.component, set.params, strings.Join(set.reloadables, ", "))
	}
	fmt.Fprintf(&body, `
func init() {
	_ = pwruntime.RegisterReloadable(%s)
}
`, strings.Join(names, ", "))
	return generator.Artifact{
		Kind:        generator.ArtifactHTMLTemplate,
		SourcePath:  artifact.SourcePath,
		OutputBase:  artifact.OutputBase,
		PackageName: artifact.PackageName,
		Content:     []byte(body.String()),
	}, true
}

// reloadableSet is one component and the reloadable components its markup can
// contain, transitively.
type reloadableSet struct {
	component   string
	params      string
	reloadables []string
}

// componentBinder matches the binder tinybind emits per component, which is the
// one declaration that names every component this file holds.
var componentBinder = regexp.MustCompile(`(?m)^func ([A-Za-z0-9_]+)\(params ([A-Za-z0-9_]+)\) htmlbind\.Fragment`)

// reloadableSets folds each component's call graph down to the reloadable
// components it can render.
//
// The fold is possible at all because a component call resolves inside its own
// .pw.html file: tinybind refuses `<Card/>` when Card is declared elsewhere, so
// composition across files happens through the layout chain rather than through
// a call. One generated file therefore holds the whole graph, and walking it
// needs no type information — a component call is emitted as a plain identifier
// applied to its parameter struct, which nothing else in the file looks like.
//
// This is the same shape system:tinybind already folds twice, for a component's
// transitive head and its transitive assets. It is done here rather than asked
// for upstream because the marker is durable and the answer is wanted now;
// requirement:tinybind-update-composition-seams records the accessor that would
// replace it.
//
// A file whose components reach no reloadable one produces nothing, so a project
// that declares none regenerates byte for byte.
func reloadableSets(artifact generator.Artifact) []reloadableSet {
	source := string(artifact.Content)
	reloadable := map[string]bool{}
	for _, match := range reloadableValue.FindAllStringSubmatch(source, -1) {
		reloadable[strings.TrimSuffix(match[1], "Reloadable")] = true
	}
	if len(reloadable) == 0 {
		return nil
	}
	components := map[string]bool{}
	params := map[string]string{}
	for _, match := range componentBinder.FindAllStringSubmatch(source, -1) {
		components[match[1]] = true
		params[match[1]] = match[2]
	}
	file, err := parser.ParseFile(token.NewFileSet(), "artifact.go", source, 0)
	if err != nil {
		// The artifact is about to be written and compiled, so a parse failure
		// here is reported by the build with a position. Emitting no set keeps
		// this from turning that into a confusing second error.
		return nil
	}
	calls := map[string][]string{}
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok || generic.Tok != token.VAR {
			continue
		}
		for _, spec := range generic.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 {
				continue
			}
			owner, ok := planOwner(value.Names[0].Name, components)
			if !ok {
				continue
			}
			ast.Inspect(spec, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				// A component call is an identifier; every helper the plan uses
				// is qualified by a package or a builder value.
				if named, ok := call.Fun.(*ast.Ident); ok && components[named.Name] {
					calls[owner] = append(calls[owner], named.Name)
				}
				return true
			})
		}
	}
	var sets []reloadableSet
	for _, component := range sortedNames(components) {
		reached := reachableReloadables(component, calls, reloadable)
		if len(reached) == 0 {
			continue
		}
		names := make([]string, 0, len(reached))
		for _, name := range reached {
			names = append(names, name+"Reloadable")
		}
		sets = append(sets, reloadableSet{component: component, params: params[component], reloadables: names})
	}
	return sets
}

// planOwner reads the component a generated plan or op builder belongs to.
func planOwner(name string, components map[string]bool) (string, bool) {
	trimmed, ok := strings.CutPrefix(name, "plan")
	if !ok {
		return "", false
	}
	for _, suffix := range []string{"Plan", "Ops", "Boundary"} {
		if owner, ok := strings.CutSuffix(trimmed, suffix); ok && components[owner] {
			return owner, true
		}
	}
	return "", false
}

// reachableReloadables walks the call graph from one component, itself included.
func reachableReloadables(start string, calls map[string][]string, reloadable map[string]bool) []string {
	seen := map[string]bool{}
	found := map[string]bool{}
	var visit func(string)
	visit = func(current string) {
		if seen[current] {
			return
		}
		seen[current] = true
		if reloadable[current] {
			found[current] = true
		}
		for _, called := range calls[current] {
			visit(called)
		}
	}
	visit(start)
	return sortedNames(found)
}

func sortedNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// tableConstructor matches the definition constructor tinybind emits for a type
// declaring a partition key. It is the one durable marker of "this type owns a
// table": the codec methods are usage-directed and may be absent, and this is
// not.
var tableConstructor = regexp.MustCompile(`(?m)^func ([A-Za-z0-9_]+)Table\(name string\) dynamodb\.TableDefinition \{`)

// dynamoRegistrationArtifact emits the init that puts a generated table into the
// desired schema of requirement:dynamodb-migration. Without it a project would
// generate a definition nothing ever reads, and pw migrate would create nothing.
//
// It is derived from the generated source rather than from a second analysis of
// the package, so a type whose table constructor was suppressed registers
// nothing and needs no separate flag to say so.
func dynamoRegistrationArtifact(artifact generator.Artifact) (generator.Artifact, bool) {
	matches := tableConstructor.FindAllStringSubmatch(string(artifact.Content), -1)
	if len(matches) == 0 {
		return generator.Artifact{}, false
	}
	var body strings.Builder
	fmt.Fprintf(&body, "package %s\n\nimport \"github.com/shibukawa/popcornweb/database/dynamo\"\n\nfunc init() {\n", artifact.PackageName)
	for _, match := range matches {
		fmt.Fprintf(&body, "\tdynamo.RegisterTable(%q, %sTable)\n", declaredTableName(match[1]), match[1])
	}
	body.WriteString("}\n")
	return generator.Artifact{
		Kind:        generator.ArtifactDynamoItem,
		SourcePath:  artifact.SourcePath,
		OutputBase:  artifact.OutputBase,
		PackageName: artifact.PackageName,
		Content:     []byte(body.String()),
	}, true
}

// kindMethod matches the generated Kind method, which every bound Firestore
// type has: a kind belongs to the type, so the generator emits it whether or
// not anything calls it.
var kindMethod = regexp.MustCompile(`(?m)^func \([a-zA-Z0-9_]+ ([A-Za-z0-9_]+)\) Kind\(\) string \{`)

// firestoreRegistrationArtifact emits the init that publishes a generated kind.
//
// It is not what the DynamoDB counterpart is. Nothing here creates a kind and
// no migration reads the registry: what it feeds is the list a deployment is
// handed, which names every kind and the property its TTL policy expires on. An
// application kind carrying a ttl tag and missing from that list is a policy
// nobody applies, and records that never expire.
//
// Every kind registers rather than only the expiring ones, so Kinds means kinds
// rather than kinds-that-need-a-policy. The cost is one init per bound type,
// which is what decision:dynamodb-table-registry already accepted for the other
// store.
//
// It is derived from the generated source rather than from a second analysis of
// the package, so a type the generator did not bind registers nothing.
func firestoreRegistrationArtifact(artifact generator.Artifact) (generator.Artifact, bool) {
	matches := kindMethod.FindAllStringSubmatch(string(artifact.Content), -1)
	if len(matches) == 0 {
		return generator.Artifact{}, false
	}
	seen := make(map[string]bool, len(matches))
	var body strings.Builder
	fmt.Fprintf(&body, "package %s\n\nimport \"github.com/shibukawa/popcornweb/database/firestore\"\n\nfunc init() {\n", artifact.PackageName)
	registered := 0
	for _, match := range matches {
		if seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		fmt.Fprintf(&body, "\tfirestore.RegisterKind(%s{})\n", match[1])
		registered++
	}
	body.WriteString("}\n")
	if registered == 0 {
		return generator.Artifact{}, false
	}
	return generator.Artifact{
		Kind:        generator.ArtifactFirestoreEntity,
		SourcePath:  artifact.SourcePath,
		OutputBase:  artifact.OutputBase,
		PackageName: artifact.PackageName,
		Content:     []byte(body.String()),
	}, true
}

// declaredTableName is the snake_case of a Go type name, which is the name a
// .pw.dynamo table clause and an item call both use.
//
// A run of capitals is one word, so HTTPRequestID becomes http_request_id
// rather than a letter-by-letter spelling. The derived name is what an author
// has to type in a table clause, so it has to be one a person would write.
func declaredTableName(typeName string) string {
	runes := []rune(typeName)
	var out strings.Builder
	for index, r := range runes {
		upper := r >= 'A' && r <= 'Z'
		if upper && index > 0 {
			previous := runes[index-1]
			previousIsUpper := previous >= 'A' && previous <= 'Z'
			nextIsLower := index+1 < len(runes) && runes[index+1] >= 'a' && runes[index+1] <= 'z'
			// A boundary is either the end of a lowercase run, or the last
			// capital of an acronym that a new word follows.
			if !previousIsUpper || nextIsLower {
				out.WriteByte('_')
			}
		}
		if upper {
			out.WriteRune(r - 'A' + 'a')
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func hasGoSources(directory string) (bool, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") &&
			!strings.HasSuffix(name, "_pw_gen.go") {
			return true, nil
		}
	}
	return false, nil
}

// netHTTPConstraint excludes a generated file from the fasthttp build. It is
// written above the generated-code header rather than below it, which is where
// a reader looks for a build constraint, and the blank line after it is what
// keeps it a constraint instead of an ordinary comment.
const netHTTPConstraint = "//go:build !fasthttp\n\n"

// constrainNetHTTP marks a generated file as belonging to the net/http build
// when the project declares a fasthttp build too.
//
// Two things trigger it. A file naming net/http is one the other build has to
// supply for itself. So is a binder, whatever it imports: the second build
// emits the whole binder set for the package into one file of its own, so a
// per-source binder left unconstrained is the same functions declared twice.
//
// The import test alone was the rule until 2026-08-11, and it held for as long
// as every binder named a transport. A socket's codecs are the first that do
// not — they decode and encode a message rather than a request — so the file
// holding them read as shared by both builds and collided with the second
// build's copy. The failure is a package that does not compile under the
// fasthttp tag, which no net/http run ever sees.
//
// Deciding it per file is what keeps the constraint off the config and SQL that
// have no transport in them and no second copy either.
//
// A project that declared no second build gets its source back untouched, so
// nothing changes for it, per policy:generated-artifacts making output a
// function of declared configuration.
func constrainNetHTTP(source []byte, fastHTTP bool, kinds ...generator.ArtifactKind) ([]byte, error) {
	if !fastHTTP {
		return source, nil
	}
	// Whatever the generator already decided wins, wherever in the header it
	// put it. From system:tinybind v0.4.9 a run with a backend selected emits
	// the constraint itself, one line below the generated-code header rather
	// than above it; a check for a leading constraint would miss that and add a
	// second, and a file carrying two //go:build lines does not compile.
	//
	// Deferring rather than reconciling is what makes the two coexist, and they
	// do coexist: the derived handlers arrive already carrying //go:build
	// fasthttp and are left alone, while a per-source binder does not, because
	// the artifact API this project generates through emits the constraint on
	// neither half. Which is why this is still the only thing marking the
	// net/http side, and is decided per file rather than per run.
	if _, ok := buildConstraint(source); ok {
		return source, nil
	}
	for _, kind := range kinds {
		if kind == generator.ArtifactBinding {
			return append([]byte(netHTTPConstraint), source...), nil
		}
	}
	file, err := parser.ParseFile(token.NewFileSet(), "generated.go", source, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("popcornweb: read generated imports: %w", err)
	}
	for _, item := range file.Imports {
		if item.Path.Value != `"net/http"` {
			continue
		}
		return append([]byte(netHTTPConstraint), source...), nil
	}
	return source, nil
}

// buildConstraint returns the //go:build expression a generated file carries,
// and whether it carries one at all.
//
// Only the header is read, which is the whole of where a constraint may legally
// appear: the first line at or after the package clause ends the search, so a
// string or comment further down that happens to look like one is not mistaken
// for it.
func buildConstraint(source []byte) (string, bool) {
	for line := range bytes.Lines(source) {
		text := strings.TrimSpace(string(line))
		if strings.HasPrefix(text, "package ") {
			return "", false
		}
		if constraint.IsGoBuild(text) {
			return text, true
		}
	}
	return "", false
}

func mergeArtifacts(artifacts []generator.Artifact) ([]byte, error) {
	if len(artifacts) == 0 {
		return nil, fmt.Errorf("popcornweb: no artifacts to merge")
	}
	if len(artifacts) == 1 {
		return artifacts[0].Content, nil
	}
	packageName := artifacts[0].PackageName
	// A merged file is rebuilt from declarations, which loses the header the
	// artifacts arrived with. That header may carry a build constraint the
	// generator put there, so it is collected before the rebuild and written
	// back afterwards; dropping it would put a file the other backend supplies
	// for itself into both builds.
	//
	// Disagreement is refused rather than resolved. Two artifacts constrained
	// to different builds do not belong in one file, and merging them would
	// produce a file that compiles under one tag and is wrong under the other.
	constrained := ""
	for _, artifact := range artifacts {
		found, ok := buildConstraint(artifact.Content)
		if !ok {
			continue
		}
		if constrained != "" && constrained != found {
			return nil, fmt.Errorf("popcornweb: artifacts merged into one file disagree on their build constraint: %q and %q", constrained, found)
		}
		constrained = found
	}
	fset := token.NewFileSet()
	imports := make(map[string]*ast.ImportSpec)
	var declarations []ast.Decl
	for index, artifact := range artifacts {
		if artifact.PackageName != packageName {
			return nil, fmt.Errorf("popcornweb: artifact package mismatch %q and %q", packageName, artifact.PackageName)
		}
		file, err := parser.ParseFile(fset, fmt.Sprintf("artifact-%d.go", index), artifact.Content, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s artifact: %w", artifact.Kind, err)
		}
		for _, item := range file.Imports {
			alias := ""
			if item.Name != nil {
				alias = item.Name.Name
			}
			spec := &ast.ImportSpec{
				Path: &ast.BasicLit{Kind: token.STRING, Value: item.Path.Value},
			}
			if alias != "" {
				spec.Name = ast.NewIdent(alias)
			}
			imports[alias+"\x00"+item.Path.Value] = spec
		}
		for _, declaration := range file.Decls {
			if generated, ok := declaration.(*ast.GenDecl); ok && generated.Tok == token.IMPORT {
				continue
			}
			declarations = append(declarations, declaration)
		}
	}
	keys := make([]string, 0, len(imports))
	for key := range imports {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		specs := make([]ast.Spec, 0, len(keys))
		for _, key := range keys {
			specs = append(specs, imports[key])
		}
		declarations = append([]ast.Decl{&ast.GenDecl{Tok: token.IMPORT, Specs: specs}}, declarations...)
	}
	file := &ast.File{Name: ast.NewIdent(packageName), Decls: declarations}
	var output bytes.Buffer
	output.WriteString("// Code generated by Popcorn Web via TinyBind; DO NOT EDIT.\n\n")
	if constrained != "" {
		output.WriteString(constrained + "\n\n")
	}
	if err := format.Node(&output, fset, file); err != nil {
		return nil, err
	}
	output.WriteByte('\n')
	source, err := format.Source(output.Bytes())
	if err != nil {
		return nil, err
	}
	return source, nil
}

// changePaths renders the touched files for the operator. A generated path is
// absolute because the walk started from the project root, and printing it that
// way buries the interesting part of the line; the operator is standing in the
// project, so the prefix they are already in comes off.
func changePaths(root string, changes []fileChange) []string {
	prefixes := []string{}
	if working, err := os.Getwd(); err == nil {
		prefixes = append(prefixes, working+string(filepath.Separator))
	}
	// The working directory may be below the root, in which case a file
	// elsewhere in the project still shortens against the root.
	prefixes = append(prefixes, root+string(filepath.Separator))
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, shortenPath(change.path, prefixes))
	}
	sort.Strings(paths)
	return paths
}

// shortenPath cuts the first matching prefix. A path under none of them is left
// absolute, because a shortened form would name a file the operator cannot find
// from where they are.
func shortenPath(path string, prefixes []string) string {
	for _, prefix := range prefixes {
		if shortened, ok := strings.CutPrefix(path, prefix); ok {
			return shortened
		}
	}
	return path
}

func applyFileChanges(changes []fileChange) error {
	type stagedFile struct {
		target string
		temp   string
	}
	var staged []stagedFile
	cleanup := func() {
		for _, file := range staged {
			_ = os.Remove(file.temp)
		}
	}
	for _, change := range changes {
		if change.remove {
			continue
		}
		// Every other generated file lands beside a source that already exists,
		// so the directory is there. The storybook harness is the exception: it
		// is the only thing in a directory pw creates.
		if err := os.MkdirAll(filepath.Dir(change.path), 0o755); err != nil {
			cleanup()
			return err
		}
		file, err := os.CreateTemp(filepath.Dir(change.path), "."+filepath.Base(change.path)+".*")
		if err != nil {
			cleanup()
			return err
		}
		temp := file.Name()
		staged = append(staged, stagedFile{target: change.path, temp: temp})
		if err := file.Chmod(0o644); err != nil {
			_ = file.Close()
			cleanup()
			return err
		}
		if _, err := file.Write(change.source); err != nil {
			_ = file.Close()
			cleanup()
			return err
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			cleanup()
			return err
		}
		if err := file.Close(); err != nil {
			cleanup()
			return err
		}
	}
	for _, file := range staged {
		if err := os.Rename(file.temp, file.target); err != nil {
			cleanup()
			return err
		}
	}
	for _, change := range changes {
		if change.remove {
			if err := os.Remove(change.path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
