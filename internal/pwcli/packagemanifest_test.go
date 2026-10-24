package pwcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePackageFixture writes a package-kind popcornweb.toml. A package has no
// entry point, so the fixture never carries project.main, and generate.queries
// stays empty because a generated query cannot be published for an unknown
// engine.
func writePackageFixture(t *testing.T, root, config string) {
	t.Helper()
	writeProjectFixture(t, root, config)
}

func TestLoadProjectConfigDefaultsToApplication(t *testing.T) {
	root := t.TempDir()
	writeProjectFixture(t, root, `[project]
name = "fixture"
main = "./cmd/fixture"
`)
	config, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	// Every project written before the key existed is an application, so the
	// absent key means that rather than an error.
	if config.Kind != kindApplication {
		t.Fatalf("kind = %q, want %q", config.Kind, kindApplication)
	}
	if len(config.Packages) != 0 {
		t.Fatalf("packages = %#v, want none", config.Packages)
	}
}

func TestLoadProjectConfigPackages(t *testing.T) {
	root := t.TempDir()
	writeProjectFixture(t, root, `[project]
name = "fixture"
main = "./cmd/fixture"

[[packages]]
module = "example.com/zeta"

[[packages]]
module = "example.com/alpha"
`)
	config, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	// The order is normalized so the generated bootstrap import block does not
	// move when someone reorders the declarations.
	want := []string{"example.com/alpha", "example.com/zeta"}
	if len(config.Packages) != len(want) {
		t.Fatalf("packages = %#v", config.Packages)
	}
	for index, module := range want {
		if config.Packages[index].Module != module {
			t.Fatalf("packages[%d] = %q, want %q", index, config.Packages[index].Module, module)
		}
	}
}

func TestLoadProjectConfigRejectsDuplicatePackage(t *testing.T) {
	root := t.TempDir()
	writeProjectFixture(t, root, `[project]
name = "fixture"
main = "./cmd/fixture"

[[packages]]
module = "example.com/one"

[[packages]]
module = "example.com/one"
`)
	_, err := loadProjectConfig(root)
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("err = %v, want a duplicate report", err)
	}
}

func TestLoadProjectConfigRejectsManifestInApplication(t *testing.T) {
	root := t.TempDir()
	writeProjectFixture(t, root, `[project]
name = "fixture"
main = "./cmd/fixture"

[package]
module = "example.com/widget"
`)
	_, err := loadProjectConfig(root)
	if err == nil || !strings.Contains(err.Error(), "project.kind") {
		t.Fatalf("err = %v, want the kind named", err)
	}
}

func TestLoadPackageProject(t *testing.T) {
	root := t.TempDir()
	writePackageFixture(t, root, `[project]
name = "widget"
kind = "package"

[package]
module = "example.com/widget"
summary = "A widget"
assets.declared = true
routes.register = "Register"

[package.requires]
capabilities = ["database"]
engines = ["sqlite"]

[package.generated_with]
pw = "v0.4.0"
tinybind = "v0.3.5"

[package.migrations]
dir = "migrations"
stem = "widget"
engines = ["sqlite"]
`)
	config, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if config.Kind != kindPackage {
		t.Fatalf("kind = %q", config.Kind)
	}
	if config.Package.Module != "example.com/widget" {
		t.Fatalf("module = %q", config.Package.Module)
	}
	if !config.Package.Assets || config.Package.Register != "Register" {
		t.Fatalf("manifest = %#v", config.Package)
	}
	if config.Package.Migrations.Stem != "widget" {
		t.Fatalf("stem = %q", config.Package.Migrations.Stem)
	}
	if config.Package.GeneratedWith.TinyBind != "v0.3.5" {
		t.Fatalf("tinybind = %q", config.Package.GeneratedWith.TinyBind)
	}
}

func TestLoadPackageProjectRejectsMain(t *testing.T) {
	root := t.TempDir()
	writePackageFixture(t, root, `[project]
name = "widget"
kind = "package"
main = "./cmd/widget"

[package]
module = "example.com/widget"
`)
	_, err := loadProjectConfig(root)
	if err == nil || !strings.Contains(err.Error(), "project.main") {
		t.Fatalf("err = %v, want project.main rejected", err)
	}
}

func TestLoadPackageProjectRejectsBuildOutput(t *testing.T) {
	root := t.TempDir()
	writePackageFixture(t, root, `[project]
name = "widget"
kind = "package"

[build]
output = "bin/widget"

[package]
module = "example.com/widget"
`)
	_, err := loadProjectConfig(root)
	if err == nil || !strings.Contains(err.Error(), "build.output") {
		t.Fatalf("err = %v, want build.output rejected", err)
	}
}

func TestLoadPackageProjectRequiresManifest(t *testing.T) {
	root := t.TempDir()
	writePackageFixture(t, root, `[project]
name = "widget"
kind = "package"
`)
	_, err := loadProjectConfig(root)
	if err == nil || !strings.Contains(err.Error(), "package.module") {
		t.Fatalf("err = %v, want the manifest required", err)
	}
}

func TestLoadPackageProjectRejectsQueries(t *testing.T) {
	root := t.TempDir()
	// The shared fixture appends an empty generate.queries, so this one is
	// written whole to give the purpose a directory.
	if err := os.MkdirAll(filepath.Join(root, "queries"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "popcornweb.toml"), `[project]
name = "widget"
kind = "package"

[package]
module = "example.com/widget"

[generate]
handlers = []
templates = []
queries = ["queries"]
config = []
`)
	_, err := loadProjectConfig(root)
	if err == nil || !strings.Contains(err.Error(), "generate.queries") {
		t.Fatalf("err = %v, want generated queries refused in a package", err)
	}
}

func TestPackageManifestRejectsUncoveredEngine(t *testing.T) {
	root := t.TempDir()
	writePackageFixture(t, root, `[project]
name = "widget"
kind = "package"

[package]
module = "example.com/widget"

[package.requires]
engines = ["sqlite", "postgres"]

[package.migrations]
dir = "migrations"
stem = "widget"
engines = ["sqlite"]
`)
	_, err := loadProjectConfig(root)
	if err == nil || !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("err = %v, want the uncovered engine named", err)
	}
}

func TestPackageManifestRejectsExportedComponents(t *testing.T) {
	root := t.TempDir()
	writePackageFixture(t, root, `[project]
name = "widget"
kind = "package"

[package]
module = "example.com/widget"
components.exported = ["Button"]
`)
	_, err := loadProjectConfig(root)
	if err == nil || !strings.Contains(err.Error(), "another module") {
		t.Fatalf("err = %v, want the unsupported promise refused", err)
	}
}

func TestPackageManifestRequiresStemBesideDir(t *testing.T) {
	root := t.TempDir()
	writePackageFixture(t, root, `[project]
name = "widget"
kind = "package"

[package]
module = "example.com/widget"

[package.migrations]
dir = "migrations"
`)
	_, err := loadProjectConfig(root)
	if err == nil || !strings.Contains(err.Error(), "stem") {
		t.Fatalf("err = %v, want the stem required", err)
	}
}

func TestValidMigrationStem(t *testing.T) {
	for _, stem := range []string{"widget", "my_widget", "w2"} {
		if !validMigrationStem(stem) {
			t.Errorf("validMigrationStem(%q) = false", stem)
		}
	}
	// The stem reaches a table name and a file name on every engine, so it stays
	// in the character set that needs no quoting anywhere.
	for _, stem := range []string{"", "Widget", "2widget", "_widget", "my-widget", "my widget"} {
		if validMigrationStem(stem) {
			t.Errorf("validMigrationStem(%q) = true", stem)
		}
	}
}

// TestPlanBootstrapLinkImportsDeclaredPackages proves the declaration is the
// install: a project with no document shell and no public.go still gets a
// bootstrap, and the only thing in it is the declared package.
func TestPlanBootstrapLinkImportsDeclaredPackages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(root, "cmd", "fixture", "main.go"), "package main\n\nfunc main() {}\n")
	config := projectConfig{Main: "./cmd/fixture"}
	declared := []resolvedPackage{
		{Module: "example.com/widget", Manifest: packageManifest{Module: "example.com/widget"}},
		// A package whose Go lives below the module root says so, and the import
		// follows the manifest rather than the module path.
		{Module: "example.com/kit", Manifest: packageManifest{Module: "example.com/kit", Import: "example.com/kit/ui"}},
	}
	changes, err := planBootstrapLink(root, config, declared, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %#v", changes)
	}
	source := string(changes[0].source)
	for _, expected := range []string{`_ "example.com/widget"`, `_ "example.com/kit/ui"`} {
		if !strings.Contains(source, expected) {
			t.Fatalf("bootstrap is missing %s:\n%s", expected, source)
		}
	}
}

// TestPlanBootstrapLinkRemovesBootstrapWhenNothingIsDeclared covers the other
// direction: removing the declaration removes the import on the next generation.
func TestPlanBootstrapLinkRemovesBootstrapWhenNothingIsDeclared(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "cmd", "fixture")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(mainDir, "main.go"), "package main\n\nfunc main() {}\n")
	writeTestFile(t, filepath.Join(mainDir, "popcornweb_bootstrap_pw_gen.go"), "package main\n")
	config := projectConfig{Main: "./cmd/fixture"}
	changes, err := planBootstrapLink(root, config, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || !changes[0].remove {
		t.Fatalf("changes = %#v, want the stale bootstrap removed", changes)
	}
}
