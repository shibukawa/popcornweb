package pwcli

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// storybookFileName is the generated registration file, one per template
// package. It carries the pwdev build constraint, so api:cli-build emits and
// links none of it.
const storybookFileName = "popcornweb_storybook_pw_gen.go"

// storybookDirectory holds the generated harness main.
//
// The leading dot is load bearing. The go tool skips such directories when it
// expands a wildcard, so `go build ./...` and `go vet ./...` never see a main
// package whose only file is excluded by a build constraint — which would
// otherwise be an error in every project that has one. An explicit path still
// reaches it, which is how pw runs it.
const storybookDirectory = ".pwstorybook"

// storybookTemplate is one generated template function found in a package.
type storybookTemplate struct {
	Package string
	Name    string
	Params  string
	// Document marks the shell binder, which the harness registers separately
	// so a story can be rendered inside it.
	Document bool
}

func (t storybookTemplate) exported() bool {
	return t.Name != "" && t.Name[0] >= 'A' && t.Name[0] <= 'Z'
}

// plannedSources indexes what this generation run is about to write, so the
// storybook scan reads the output being produced rather than the output of the
// previous run. Check mode depends on this: it has to report drift without
// having written anything.
type plannedSources struct {
	byPath map[string][]byte
	// names groups the planned base names by directory, because the scans ask
	// per directory and filtering the whole change set for each one made them
	// O(directories × changes).
	names map[string][]string
	// removed holds the paths this run deletes. Such a file is still on disk
	// while the run is being planned, so a scan that only asked the disk would
	// go on registering a template whose source is already gone.
	removed map[string]bool
}

func planned(changes []fileChange) plannedSources {
	sources := plannedSources{byPath: map[string][]byte{}, names: map[string][]string{}, removed: map[string]bool{}}
	for _, change := range changes {
		if change.remove {
			sources.removed[change.path] = true
			continue
		}
		sources.byPath[change.path] = change.source
		directory := filepath.Dir(change.path)
		sources.names[directory] = append(sources.names[directory], filepath.Base(change.path))
	}
	return sources
}

// read returns the planned content of a file, or its content on disk when this
// run leaves it alone.
func (p plannedSources) read(path string) ([]byte, error) {
	if source, ok := p.byPath[path]; ok {
		return source, nil
	}
	return os.ReadFile(path)
}

// namesIn returns the base names this run plans to write into one directory.
func (p plannedSources) namesIn(directory string) []string { return p.names[directory] }

// scanStorybookTemplates reads the generated files of one package and reports
// the templates it binds.
//
// It reads the generated Go rather than the .pw.html source because what the
// harness has to call is the generated function and its parameter type, and
// those are facts about the output. A template whose generation failed is
// therefore absent rather than registered and broken.
func scanStorybookTemplates(directory string, sources plannedSources) ([]storybookTemplate, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			names[entry.Name()] = true
		}
	}
	// A file this run is creating is not on disk yet, so the planned set is
	// consulted for names as well as for content.
	for _, name := range sources.namesIn(directory) {
		names[name] = true
	}
	var templates []storybookTemplate
	packageName := ""
	for name := range names {
		if !strings.HasSuffix(name, "_pw_gen.go") || name == storybookFileName {
			continue
		}
		if sources.removed[filepath.Join(directory, name)] {
			continue
		}
		source, err := sources.read(filepath.Join(directory, name))
		if err != nil {
			continue
		}
		fileSet := token.NewFileSet()
		// Only top-level function declarations are read, so object resolution
		// would be work thrown away for every generated file of the package.
		file, err := parser.ParseFile(fileSet, name, source, parser.SkipObjectResolution)
		if err != nil {
			// A generated file that does not parse is a generation defect the
			// build will report far more clearly than this scan would.
			continue
		}
		packageName = file.Name.Name
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil {
				continue
			}
			if name, params, ok := templateBinder(function); ok {
				templates = append(templates, storybookTemplate{
					Package: packageName, Name: name, Params: params,
				})
				continue
			}
			if name, params, ok := documentBinder(function); ok {
				templates = append(templates, storybookTemplate{
					Package: packageName, Name: name, Params: params, Document: true,
				})
			}
		}
	}
	sort.Slice(templates, func(i, j int) bool { return templates[i].Name < templates[j].Name })
	return templates, nil
}

// templateBinder matches `func X(params XParams) htmlbind.Fragment`, which is
// the shape every generated template has.
func templateBinder(function *ast.FuncDecl) (name, params string, ok bool) {
	if !singleResult(function, "Fragment") || function.Type.Params == nil || len(function.Type.Params.List) != 1 {
		return "", "", false
	}
	identifier, ok := function.Type.Params.List[0].Type.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	return function.Name.Name, identifier.Name, true
}

// documentBinder matches `func BindX(params XParams) htmlbind.Wrapper`, the
// chain binder generated for a component with an unnamed slot.
func documentBinder(function *ast.FuncDecl) (name, params string, ok bool) {
	if !singleResult(function, "Wrapper") || function.Type.Params == nil || len(function.Type.Params.List) != 1 {
		return "", "", false
	}
	identifier, ok := function.Type.Params.List[0].Type.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	return function.Name.Name, identifier.Name, true
}

func singleResult(function *ast.FuncDecl, typeName string) bool {
	if function.Type.Results == nil || len(function.Type.Results.List) != 1 {
		return false
	}
	selector, ok := function.Type.Results.List[0].Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageIdent, ok := selector.X.(*ast.Ident)
	return ok && packageIdent.Name == "htmlbind" && selector.Sel.Name == typeName
}

// storybookRegistration is the file generated into a template package.
//
// It lives in the package rather than importing it because a generated fragment
// named for an unexported template is unreachable from anywhere else. Putting
// the registration inside means the storybook lists what the project generated
// rather than the subset it chose to export.
// The assembled source runs through the formatter before it is returned, as
// the other emitters' does, so the emitted layout and import order cannot
// drift from what gofmt would write into the same file.
func storybookRegistration(templates []storybookTemplate) ([]byte, error) {
	var out strings.Builder
	packageName := templates[0].Package
	out.WriteString("//go:build pwdev\n\n")
	out.WriteString("// Code generated by pw; DO NOT EDIT.\n\n")
	out.WriteString("package " + packageName + "\n\n")
	out.WriteString("import (\n")
	out.WriteString("\t\"github.com/shibukawa/popcornweb/pwstory\"\n")
	out.WriteString("\t\"github.com/shibukawa/tinybind-go/htmlbind\"\n")
	out.WriteString(")\n\n")
	out.WriteString("func init() {\n")
	for _, template := range templates {
		if template.Document {
			fmt.Fprintf(&out, "\tpwstory.RegisterDocument(%s(%s{}))\n", template.Name, template.Params)
			continue
		}
		fmt.Fprintf(&out, "\tpwstory.Register(pwstory.Template{\n")
		fmt.Fprintf(&out, "\t\tPackage:   %q,\n", template.Package)
		fmt.Fprintf(&out, "\t\tName:      %q,\n", template.Name)
		fmt.Fprintf(&out, "\t\tExported:  %t,\n", template.exported())
		fmt.Fprintf(&out, "\t\tNewParams: func() any { return new(%s) },\n", template.Params)
		fmt.Fprintf(&out, "\t\tRender:    func(params any) htmlbind.Fragment { return %s(*params.(*%s)) },\n",
			template.Name, template.Params)
		fmt.Fprintf(&out, "\t})\n")
	}
	out.WriteString("}\n")
	return format.Source([]byte(out.String()))
}

// storybookHarness is the generated main: a list of imports and one call, so
// everything it does lives in pwstory where it can be tested without generating
// a project first.
func storybookHarness(module string, packages []string) ([]byte, error) {
	var out strings.Builder
	out.WriteString("//go:build pwdev\n\n")
	out.WriteString("// Code generated by pw; DO NOT EDIT.\n\n")
	out.WriteString("package main\n\n")
	out.WriteString("import (\n")
	out.WriteString("\t\"log\"\n\n")
	out.WriteString("\t\"github.com/shibukawa/popcornweb/pwstory\"\n")
	for _, path := range packages {
		fmt.Fprintf(&out, "\t_ %q\n", module+"/"+path)
	}
	out.WriteString(")\n\n")
	out.WriteString("func main() {\n")
	out.WriteString("\tif err := pwstory.ListenAndServe(); err != nil {\n")
	out.WriteString("\t\tlog.Fatal(err)\n")
	out.WriteString("\t}\n")
	out.WriteString("}\n")
	return format.Source([]byte(out.String()))
}

// planStorybook adds the registration file of every template package and the
// harness main that imports them.
//
// It runs after the templates themselves are planned, because it reads the
// generated files to find what to register. A project with no template tree
// plans nothing here and gets no harness.
//
// It also removes what an earlier run wrote and this one would not. The
// registration carries the pwdev build constraint, so one left behind in a
// package whose last template was deleted names functions that no longer exist
// and nothing but pw dev compiles it: go build and go vet pass, and the
// project fails to start in the one mode that is used all day.
func planStorybook(root string, config projectConfig, changes []fileChange) ([]fileChange, error) {
	moduleSource, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	modulePath := modfile.ModulePath(moduleSource)
	if modulePath == "" {
		return nil, fmt.Errorf("go.mod does not declare a module path")
	}
	sources := planned(changes)
	// Every directory holding a generated template, from both purposes: a page
	// tree generates templates the same way a templates directory does.
	directories := map[string]bool{}
	// Every registration already on disk is stale until this run plans it.
	stale := map[string]bool{}
	for _, purpose := range [][]string{config.Generate.Templates, config.Generate.Pages} {
		err := walkSources(root, purpose, func(path string, entry fs.DirEntry) error {
			if strings.HasSuffix(entry.Name(), ".pw.html") {
				directories[filepath.Dir(path)] = true
			}
			if entry.Name() == storybookFileName {
				stale[path] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	var packages []string
	for directory := range directories {
		templates, err := scanStorybookTemplates(directory, sources)
		if err != nil || len(templates) == 0 {
			continue
		}
		registration, err := storybookRegistration(templates)
		if err != nil {
			return nil, err
		}
		target := filepath.Join(directory, storybookFileName)
		delete(stale, target)
		changes, err = appendIfChanged(changes, target, registration)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(root, directory)
		if err != nil {
			return nil, err
		}
		packages = append(packages, filepath.ToSlash(relative))
	}
	leftover := make([]string, 0, len(stale))
	for path := range stale {
		leftover = append(leftover, path)
	}
	sort.Strings(leftover)
	for _, path := range leftover {
		changes = append(changes, fileChange{path: path, remove: true})
	}
	harnessPath := filepath.Join(root, storybookDirectory, "main_pw_gen.go")
	if len(packages) == 0 {
		// The harness imports the packages that registered something, so with
		// none left it would import nothing and serve nothing.
		if _, err := os.Stat(harnessPath); err == nil {
			changes = append(changes, fileChange{path: harnessPath, remove: true})
		}
		return changes, nil
	}
	sort.Strings(packages)
	harness, err := storybookHarness(modulePath, packages)
	if err != nil {
		return nil, err
	}
	return appendIfChanged(changes, harnessPath, harness)
}
