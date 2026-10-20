package pwgen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibukawa/tinybind-go/generator"
	"github.com/shibukawa/tinybind-go/templates/sqlbind"
)

func TestOptionsAreValid(t *testing.T) {
	options, err := Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	if !options.SQLContextOnlyAPI || options.SQLExecutorResolver == nil {
		t.Fatal("SQL context-only resolver is not configured")
	}
	if options.HTMLTemplatePattern != "*.pw.html" || options.SQLTemplatePattern != "*.pw.sql" {
		t.Fatal("Popcorn Web template generation profile is not configured")
	}
}

func TestPWCallsDriveTinyBindGeneration(t *testing.T) {
	options, err := Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := generator.New(options).GenerateArtifacts(context.Background(), generator.GenerateRequest{
		Dir: filepath.Join("testdata", "wrappers"), OpenAPI: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[generator.ArtifactKind]string{}
	for _, artifact := range artifacts {
		byKind[artifact.Kind] += string(artifact.Content)
	}
	assertContains(t, "binding", byKind[generator.ArtifactBinding],
		"RegisterBind[request]", "RegisterWrite[response]",
		// The write-status response goes through a registered writer too,
		// which is what pw.WriteStatus serializes with at runtime.
		"RegisterWrite[created]",
		// A socket needs one codec per direction, recovered from the two type
		// arguments of a call that spells neither. Without them the connection
		// is accepted and then fails on its first message.
		"jsonbind.RegisterDecode[inbound]", "jsonbind.RegisterEncode[outbound]",
	)
	// Each direction gets its own codec and not the other's, so a socket adds no
	// dead decoder or encoder to a binary that will never run it.
	if strings.Contains(byKind[generator.ArtifactBinding], "jsonbind.RegisterEncode[inbound]") {
		t.Fatal("the inbound socket type got an encoder, which nothing writes")
	}
	if strings.Contains(byKind[generator.ArtifactBinding], "jsonbind.RegisterDecode[outbound]") {
		t.Fatal("the outbound socket type got a decoder, which nothing reads")
	}
	assertContains(t, "configbind", byKind[generator.ArtifactConfigBind],
		"Register[AppConfig]",
		"RegisterSubCommand[ImportCommand]",
		`Name:      "import"`,
		`Help:      "import data"`,
	)
	// The write-status call contributes its static status: the created schema
	// answers under a 201, and under no other success code.
	assertContains(t, "openapi", byKind[generator.ArtifactOpenAPI], `/items/{id}`, `\"get\"`, `\"post\"`, `\"201\"`)
	if strings.Contains(byKind[generator.ArtifactOpenAPI], `\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/created\"`) {
		t.Fatal("the write-status operation also documents a 200 for its response schema")
	}
}

// TestAMemoCallDrivesCacheKeyGeneration proves the registration in Options
// reaches a key type: nothing in the fixture spells itemSummary as a type
// argument, so the only way its method can be emitted is through the key role
// reading the argument beside the cached result.
func TestAMemoCallDrivesCacheKeyGeneration(t *testing.T) {
	options, err := Options(sqlbind.DialectPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path, err := generator.New(options).GenerateCacheKeys(filepath.Join("testdata", "wrappers"), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	emitted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(emitted)
	assertContains(t, "cachekeybind", content,
		"func (v itemSummary) CacheKey() string",
		// The identity is derived from the package path and type name, so two
		// key types holding equal field values cannot reach one entry and no
		// author restates a name.
		"/internal/pwgen/testdata/wrappers.itemSummary",
		"cachekeybind.AppendKeyString(b, v.ItemID)",
		"cachekeybind.AppendKeyInt(b, v.Page)",
		// The per-type assertion, so a drifting interface breaks the build
		// rather than the cache.
		"var _ cachekeybind.CacheKey = itemSummary{}",
	)
	// Marking is opt-in: the fields that are the result rather than the query
	// stay out, which is what lets an entity be passed to the cache as-is.
	for _, payload := range []string{"v.Title", "v.Total"} {
		if strings.Contains(content, payload) {
			t.Errorf("%s reached the key; only marked fields belong in it:\n%s", payload, content)
		}
	}
}

func assertContains(t *testing.T, name, content string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(content, fragment) {
			t.Fatalf("%s does not contain %q:\n%s", name, fragment, content)
		}
	}
}
