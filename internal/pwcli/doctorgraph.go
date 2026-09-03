package pwcli

import (
	"bytes"
	"context"
	"os/exec"
	"slices"
	"strings"
)

// importGraph is the set of packages the application main package reaches,
// including blank imports. Registration in this framework is a property of the
// linked binary rather than of any file, so this set is what makes a missing
// plugin or driver import visible without building or running anything.
type importGraph struct {
	packages map[string]bool
	// Err records why the graph is unavailable, so the checks that need it are
	// reported as not run rather than as passing.
	Err error
}

func (g importGraph) available() bool { return g.Err == nil && len(g.packages) > 0 }

// links reports whether the application reaches the given import path.
func (g importGraph) links(path string) bool { return g.packages[path] }

// linksPrefix reports whether the application reaches any package below path.
func (g importGraph) linksPrefix(prefix string) bool {
	for path := range g.packages {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// resolveImportGraph lists the transitive imports of the main package. go list
// resolves the same package set the compiler would, so a renamed or blank
// import is an edge like any other.
func resolveImportGraph(ctx context.Context, root, mainPackage string) importGraph {
	command := exec.CommandContext(ctx, "go", "list", "-deps", "-f", "{{.ImportPath}}", mainPackage)
	command.Dir = root
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		// A project that does not build is exactly when doctor is run, so the
		// packages go list did resolve are kept and the failure is reported.
		graph := importGraph{packages: map[string]bool{}, Err: goListError(err, stderr.String())}
		for _, line := range strings.Split(stdout.String(), "\n") {
			if path := strings.TrimSpace(line); path != "" {
				graph.packages[path] = true
			}
		}
		if len(graph.packages) > 0 {
			return graph
		}
		return importGraph{Err: graph.Err}
	}
	packages := map[string]bool{}
	for _, line := range strings.Split(stdout.String(), "\n") {
		if path := strings.TrimSpace(line); path != "" {
			packages[path] = true
		}
	}
	return importGraph{packages: packages}
}

func goListError(err error, stderr string) error {
	message := strings.TrimSpace(stderr)
	if message == "" {
		return err
	}
	lines := strings.Split(message, "\n")
	if len(lines) > 3 {
		lines = lines[:3]
	}
	return &listError{detail: strings.Join(lines, "; ")}
}

type listError struct{ detail string }

func (e *listError) Error() string { return e.detail }

// Framework and plugin packages the checks reason about. A third-party plugin
// is not in this list and is not reported as missing: the checks only speak
// about wiring they can name a remedy for.
const (
	authPluginPackage     = "github.com/shibukawa/popcornweb/plugin/auth"
	rdbSessionPackage     = "github.com/shibukawa/popcornweb/sessionstore/sqlite"
	devIdPPackagePrefix   = "github.com/shibukawa/popcornweb/contrib/devidp"
	sqliteDriverPackage   = "github.com/shibukawa/popcornweb/database/sqlite"
	postgresDriverPackage = "github.com/shibukawa/popcornweb/database/postgres"
	mysqlDriverPackage    = "github.com/shibukawa/popcornweb/database/mysql"
	// d1DriverPackage is linked by the generated Cloudflare Workers entry, so
	// an application naming a d1:// connection needs no import of its own;
	// the table still names it, for a project that reads the report on the
	// host.
	d1DriverPackage = "github.com/shibukawa/popcornweb/database/d1"
)

// sessionBackendPackage names the plugin that registers a backend, or "" for a
// backend no plugin in this list owns.
//
// It answers from the same table pw init scaffolds the import from, which is
// what keeps the two from drifting: a map of its own listed rdb and nothing
// else, so every project that took redis, DynamoDB or Firestore was told its
// backend was unregistered while its main package was importing the plugin. The
// engine matters for the same reason it does at scaffold time — a SQL store is
// one package per engine, and no engine reads another's DDL — so a Postgres
// project was told to link the SQLite one.
func sessionBackendPackage(backend, engine string) string {
	return sessionBackendPlugin(backend, engine)
}

// driverPackages maps a DSN scheme to the driver package that answers it.
var driverPackages = map[string]string{
	"sqlite":     sqliteDriverPackage,
	"sqlite3":    sqliteDriverPackage,
	"postgres":   postgresDriverPackage,
	"postgresql": postgresDriverPackage,
	"mysql":      mysqlDriverPackage,
	"d1":         d1DriverPackage,
}

// configPrefixOwners maps a configuration prefix to the package that must be
// linked for the prefix to mean anything. A prefix absent here belongs to the
// framework, which every application links through pw.
var configPrefixOwners = map[string]string{
	"auth": authPluginPackage,
}

// frameworkPrefixes are the bindings pw registers on its own.
var frameworkPrefixes = []string{"server", "security", "session", "observability", "middleware", "html"}

func sortStrings(values []string) { slices.Sort(values) }
