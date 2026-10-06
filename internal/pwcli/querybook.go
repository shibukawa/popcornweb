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
	"unicode"

	"github.com/shibukawa/popcornweb/pwdata"
	"golang.org/x/mod/modfile"
)

// queryRegistryFileName is the generated registration file, one per queries
// package. It carries the pwdev build constraint, so api:cli-build emits and
// links none of it.
const queryRegistryFileName = "popcornweb_pwdata_pw_gen.go"

// queryLinkFileName is the file that pulls every queries package into the
// development binary.
//
// The registration above runs from package initialisation, which only happens
// if something links the package. Handlers usually do — but a statement
// declared before the handler that will call it is exactly the one worth trying
// on the data pane, and that one links from nowhere. This file is the link, and
// it carries the pwdev constraint so api:cli-build still sees none of it.
const queryLinkFileName = "popcornweb_pwdata_link_pw_gen.go"

// declaredQuery is one generated statement builder found in a package.
type declaredQuery struct {
	Package string
	// Name is the statement name as the .pw.sql source wrote it.
	Name string
	// Builder is the exported or unexported function that returns the built
	// statement. Its spelling follows from Name, and it is confirmed to exist
	// before anything is generated for it.
	Builder string
	Params  []queryParam
}

type queryParam struct {
	Name string
	Kind string
}

func (q declaredQuery) exported() bool {
	return q.Name != "" && unicode.IsUpper(rune(q.Name[0]))
}

// supported reports whether a form can produce every argument. A statement
// taking a type no field can express is still listed, so a developer looking
// for it finds it rather than wondering whether generation missed it.
func (q declaredQuery) supported() bool {
	for _, param := range q.Params {
		if _, ok := pwdata.SupportedArgKinds[param.Kind]; !ok {
			return false
		}
	}
	return true
}

// scanDeclaredQueries reads the generated files of one package and reports the
// statements it builds.
//
// The internal builder carries the statement name exactly as the source wrote
// it, along with the parameter list; the callable builder's name follows from
// that name and is confirmed present rather than assumed.
func scanDeclaredQueries(directory string, sources plannedSources) ([]declaredQuery, error) {
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
	for _, name := range sources.namesIn(directory) {
		names[name] = true
	}
	var queries []declaredQuery
	for name := range names {
		if !strings.HasSuffix(name, "_pw_gen.go") || name == queryRegistryFileName {
			continue
		}
		// A file this run deletes is still on disk while the run is planned,
		// and registering the builders in it would name functions that are
		// gone by the time anything compiles.
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
			continue
		}
		declared := map[string]bool{}
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil {
				declared[function.Name.Name] = true
			}
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil {
				continue
			}
			statement, params, ok := internalBuilder(function)
			if !ok {
				continue
			}
			builder := callableBuilder(statement)
			if !declared[builder] {
				// The naming rule did not hold for this statement, so nothing
				// is generated for it rather than generating a call to a
				// function that does not exist.
				continue
			}
			queries = append(queries, declaredQuery{
				Package: file.Name.Name, Name: statement, Builder: builder, Params: params,
			})
		}
	}
	sort.Slice(queries, func(i, j int) bool { return queries[i].Name < queries[j].Name })
	return queries, nil
}

// internalBuilderPrefix names the generated function that writes the statement
// into a builder. Its suffix is the statement name, unaltered, which is the one
// place the original spelling survives.
const internalBuilderPrefix = "_tinybindBuild"

func internalBuilder(function *ast.FuncDecl) (string, []queryParam, bool) {
	name, ok := strings.CutPrefix(function.Name.Name, internalBuilderPrefix)
	if !ok || name == "" || function.Type.Params == nil || len(function.Type.Params.List) == 0 {
		return "", nil, false
	}
	// The first parameter is the builder itself; the rest are the statement's.
	var params []queryParam
	for _, field := range function.Type.Params.List[1:] {
		kind := typeName(field.Type)
		for _, ident := range field.Names {
			params = append(params, queryParam{Name: ident.Name, Kind: kind})
		}
	}
	return name, params, true
}

// callableBuilder is the name of the function that returns the built statement.
// Generation exports it for an exported statement and keeps it unexported
// otherwise, capitalising the name it appends either way.
func callableBuilder(statement string) string {
	if statement == "" {
		return ""
	}
	runes := []rune(statement)
	if unicode.IsUpper(runes[0]) {
		return "Build" + statement
	}
	runes[0] = unicode.ToUpper(runes[0])
	return "build" + string(runes)
}

// typeName renders a parameter type as its source spelling, which is what the
// converter table is keyed by.
func typeName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		if pkg, ok := typed.X.(*ast.Ident); ok {
			return pkg.Name + "." + typed.Sel.Name
		}
	case *ast.StarExpr:
		return "*" + typeName(typed.X)
	case *ast.ArrayType:
		return "[]" + typeName(typed.Elt)
	}
	return "?"
}

// queryRegistration is the file generated into a queries package.
//
// It lives in the package for the same reason the storybook registration does:
// a builder named for an unexported statement is unreachable from anywhere
// else, and an unexported statement is one a developer particularly wants to
// try without writing a handler for it.
// The assembled source runs through the formatter before it is returned, as
// the other emitters' does, so the emitted layout and import order cannot
// drift from what gofmt would write into the same file.
func queryRegistration(queries []declaredQuery) ([]byte, error) {
	var out strings.Builder
	out.WriteString("//go:build pwdev\n\n")
	out.WriteString("// Code generated by pw; DO NOT EDIT.\n\n")
	out.WriteString("package " + queries[0].Package + "\n\n")
	out.WriteString("import (\n")
	out.WriteString("\t\"github.com/shibukawa/popcornweb/pwdata\"\n")
	out.WriteString("\t_tinybindsql \"github.com/shibukawa/tinybind-go/sqlbind\"\n")
	out.WriteString(")\n\n")
	out.WriteString("func init() {\n")
	for _, query := range queries {
		writeQueryRegistration(&out, query)
	}
	out.WriteString("}\n")
	return format.Source([]byte(out.String()))
}

func writeQueryRegistration(out *strings.Builder, query declaredQuery) {
	fmt.Fprintf(out, "\tpwdata.RegisterQuery(pwdata.Query{\n")
	fmt.Fprintf(out, "\t\tPackage:  %q,\n", query.Package)
	fmt.Fprintf(out, "\t\tName:     %q,\n", query.Name)
	fmt.Fprintf(out, "\t\tExported: %t,\n", query.exported())
	fmt.Fprintf(out, "\t\tParams: []pwdata.Param{")
	for index, param := range query.Params {
		if index > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "{Name: %q, Kind: %q}", param.Name, param.Kind)
	}
	out.WriteString("},\n")

	if !query.supported() {
		// The statement is listed so it can be found, and says why it cannot be
		// run, rather than being silently absent from a list a developer is
		// scanning for it.
		fmt.Fprintf(out, "\t\tBuild: func([]string) (_tinybindsql.Statement, error) {\n")
		fmt.Fprintf(out, "\t\t\treturn _tinybindsql.Statement{}, pwdata.ErrUnsupportedParams\n")
		fmt.Fprintf(out, "\t\t},\n\t})\n")
		return
	}
	fmt.Fprintf(out, "\t\tBuild: func(args []string) (_tinybindsql.Statement, error) {\n")
	arguments := make([]string, len(query.Params))
	for index, param := range query.Params {
		converter := pwdata.SupportedArgKinds[param.Kind]
		name := fmt.Sprintf("arg%d", index)
		arguments[index] = name
		fmt.Fprintf(out, "\t\t\t%s, err := pwdata.%s(%q, args[%d])\n", name, converter, param.Name, index)
		fmt.Fprintf(out, "\t\t\tif err != nil {\n\t\t\t\treturn _tinybindsql.Statement{}, err\n\t\t\t}\n")
	}
	fmt.Fprintf(out, "\t\t\treturn %s(%s)\n", query.Builder, strings.Join(arguments, ", "))
	fmt.Fprintf(out, "\t\t},\n\t})\n")
}

// planQueryRegistry adds the registration file of every queries package, and
// the one file that links them.
//
// There is no harness here. The application is the process that serves
// requirement:dev-data-pane, so registering from package initialisation is
// enough — once something links the package, which is what the link file is
// for.
//
// It also removes what an earlier run wrote and this one would not, for the
// reason planStorybook does. The registration carries the pwdev constraint, so
// one left in a package whose last statement was deleted names builders that
// no longer exist and only pw dev compiles it: go build passes, and the project
// stops starting in the mode it is developed in.
func planQueryRegistry(root string, config projectConfig, changes []fileChange) ([]fileChange, error) {
	sources := planned(changes)
	directories := map[string]bool{}
	// Every registration already on disk is stale until this run plans it.
	stale := map[string]bool{}
	err := walkSources(root, config.Generate.Queries, func(path string, entry fs.DirEntry) error {
		if strings.HasSuffix(entry.Name(), ".pw.sql") {
			directories[filepath.Dir(path)] = true
		}
		if entry.Name() == queryRegistryFileName {
			stale[path] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var registered []string
	for directory := range directories {
		queries, err := scanDeclaredQueries(directory, sources)
		if err != nil || len(queries) == 0 {
			continue
		}
		registration, err := queryRegistration(queries)
		if err != nil {
			return nil, err
		}
		target := filepath.Join(directory, queryRegistryFileName)
		delete(stale, target)
		changes, err = appendIfChanged(changes, target, registration)
		if err != nil {
			return nil, err
		}
		registered = append(registered, directory)
	}
	leftover := make([]string, 0, len(stale))
	for path := range stale {
		leftover = append(leftover, path)
	}
	sort.Strings(leftover)
	for _, path := range leftover {
		changes = append(changes, fileChange{path: path, remove: true})
	}
	return planQueryLink(root, config, registered, changes)
}

// removeQueryLink plans the removal of a link file that has nothing left to
// link, when one is on disk.
func removeQueryLink(target string, changes []fileChange) ([]fileChange, error) {
	if _, err := os.Stat(target); err == nil {
		return append(changes, fileChange{path: target, remove: true}), nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return changes, nil
}

// planQueryLink writes, or removes, the development-only import of the queries
// packages in the application's main package.
func planQueryLink(root string, config projectConfig, directories []string, changes []fileChange) ([]fileChange, error) {
	mainDirectory := filepath.Clean(filepath.Join(root, filepath.FromSlash(config.Main)))
	target := filepath.Join(mainDirectory, queryLinkFileName)
	if len(directories) == 0 {
		return removeQueryLink(target, changes)
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
	for _, directory := range directories {
		relative, err := filepath.Rel(root, directory)
		if err != nil {
			return nil, err
		}
		path := modulePath
		if relative != "." {
			path += "/" + filepath.ToSlash(relative)
		}
		// The main package cannot import itself, and a queries declaration at
		// the module root is already linked by definition.
		if path == modulePath && mainDirectory == filepath.Clean(root) {
			continue
		}
		imports = append(imports, path)
	}
	sort.Strings(imports)
	imports = slicesCompact(imports)
	if len(imports) == 0 {
		// Every registering package turned out to be the main package itself,
		// so a link an earlier layout needed has nothing to import now.
		return removeQueryLink(target, changes)
	}
	packageName, err := goPackageName(mainDirectory)
	if err != nil {
		return nil, err
	}
	var generated strings.Builder
	fmt.Fprintf(&generated, `//go:build pwdev

// Code generated by Popcorn Web; DO NOT EDIT.

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
	return appendIfChanged(changes, target, source)
}
