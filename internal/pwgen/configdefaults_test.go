package pwgen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibukawa/tinybind-go/configbind/codegen"
)

func TestStringSliceDefaultsRejectArraySyntax(t *testing.T) {
	for _, value := range []string{"[]", `["/**"]`, `["GET","POST"]`} {
		fields := []codegen.Field{{Key: "csrf", Nested: []codegen.Field{{Key: "include", Kind: codegen.FieldStringSlice, Default: value}}}}
		err := validateStringSliceDefaults("security", fields)
		if err == nil || !strings.Contains(err.Error(), "security.csrf.include") {
			t.Fatalf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"", "/**", "GET,HEAD,POST"} {
		if err := validateStringSliceDefaults("app", []codegen.Field{{Key: "values", Kind: codegen.FieldStringSlice, Default: value}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigDefaultValidationReadsRegisteredTypes(t *testing.T) {
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	module := fmt.Sprintf("module example.test/configdefault\n\ngo 1.27.0\nrequire github.com/shibukawa/popcornweb v0.0.0\nreplace github.com/shibukawa/popcornweb => %s\n", filepath.ToSlash(repository))
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	source := "package main\nimport \"github.com/shibukawa/popcornweb/pw\"\ntype Paths []string\ntype Nested struct { Include Paths `default:\"[]\"` }\ntype AppConfig struct { Nested Nested }\nfunc main() { pw.RegisterConfig[AppConfig](\"app\") }\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "mod", "tidy")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	options, err := Options("sqlite")
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateConfigDefaults(root, options)
	if err == nil || !strings.Contains(err.Error(), "app.nested.include") {
		t.Fatalf("unsupported registered default: %v", err)
	}
}
