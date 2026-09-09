package dotenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseReadsTheCommonGrammar(t *testing.T) {
	entries, err := Parse(".env", []byte(`# comment
PLAIN=value
export EXPORTED=yes
TRAILING=value # a comment
DOUBLE="a \"quoted\" line\n"
SINGLE='no \n escapes'
EMPTY=
  SPACED  =  padded
TWICE=first
TWICE=second
`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PLAIN": "value", "EXPORTED": "yes", "TRAILING": "value",
		"DOUBLE": "a \"quoted\" line\n", "SINGLE": `no \n escapes`, "EMPTY": "", "SPACED": "padded",
		"TWICE": "second",
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %d, want %d: %+v", len(entries), len(want), entries)
	}
	for index, entry := range entries {
		if value, ok := want[entry.Name]; !ok || value != entry.Value {
			t.Errorf("%s = %q, want %q", entry.Name, entry.Value, value)
		}
		if index > 0 && entries[index-1].Name > entry.Name {
			t.Errorf("entries are not in name order: %s after %s", entry.Name, entries[index-1].Name)
		}
	}
}

func TestParseReportsTheFileAndLine(t *testing.T) {
	for _, source := range []string{
		"GOOD=1\nnot an assignment\n",
		"GOOD=1\nOPEN=\"unterminated\n",
	} {
		_, err := Parse(".env.dev", []byte(source))
		if err == nil {
			t.Errorf("no error for %q", source)
			continue
		}
		if !strings.Contains(err.Error(), ".env.dev:2:") {
			t.Errorf("error does not name the file and line: %v", err)
		}
	}
}

func TestReadLayerLayersBaseUnderTheTokenFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".env", "SHARED=base\nONLY_BASE=1\n")
	write(t, dir, ".env.local", "SHARED=local\n")
	write(t, dir, ".env.stg", "SHARED=staging\nAPP_ENV=prod\n")
	write(t, dir, ".env.stg.local", "SHARED=staging-local\n")
	write(t, dir, ".env.example", "SHARED=template\n")
	layer, err := ReadLayer(dir, "stg", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(layer.Names(), ","); got != ".env,.env.local,.env.stg,.env.stg.local" {
		t.Fatalf("files = %s", got)
	}
	// The order Vite reads the family in: the environment's file outranks a
	// machine-wide local one, and the environment's local file outranks all.
	environ := layer.Environ([]string{"ONLY_BASE=process"})
	if got := strings.Join(environ, " "); got != "ONLY_BASE=1 SHARED=base SHARED=local SHARED=staging SHARED=staging-local ONLY_BASE=process" {
		t.Fatalf("environ = %q", got)
	}
	if len(layer.Warnings) != 1 || !strings.Contains(layer.Warnings[0], ".env.stg: APP_ENV is ignored") {
		t.Fatalf("warnings = %v", layer.Warnings)
	}
}

func TestReadLayerSkipsAbsentFilesAndRefusesUnreadableOnes(t *testing.T) {
	dir := t.TempDir()
	layer, err := ReadLayer(dir, "dev", nil)
	if err != nil || len(layer.Files) != 0 {
		t.Fatalf("absent files: layer = %+v, err = %v", layer, err)
	}
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	write(t, dir, ".env", "A=1\n")
	if err := os.Chmod(filepath.Join(dir, ".env"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLayer(dir, "dev", nil); err == nil {
		t.Fatal("a present file that cannot be read must fail rather than fall back")
	}
}

func TestResolveTakesTheTokenFromTheProcessThenTheBaseFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".env", "APP_ENV=prod\n")
	write(t, dir, ".env.local", "APP_ENV=stg\n")
	write(t, dir, ".env.stg", "FROM=stg\n")
	write(t, dir, ".env.prod", "FROM=prod\n")

	layer, env, declared, err := Resolve(dir, []string{"PATH=/usr/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if env != "stg" || !declared {
		t.Fatalf("env = %q declared = %v, want stg from the local base file over the shared one", env, declared)
	}
	if got := strings.Join(layer.Names(), ","); got != ".env,.env.local,.env.stg" {
		t.Fatalf("files = %s", got)
	}

	layer, env, _, err = Resolve(dir, []string{"APP_ENV=prod"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if env != "prod" || strings.Join(layer.Names(), ",") != ".env,.env.local,.env.prod" {
		t.Fatalf("the process must win: env = %q files = %v", env, layer.Names())
	}

	write(t, dir, ".env.local", "APP_ENV=../etc\n")
	if _, _, _, err := Resolve(dir, nil, nil); err == nil {
		t.Fatal("an invalid token in the base file must fail the way an invalid variable does")
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A secret mount is read after the files: each regular file is one variable,
// a dot-prefixed entry is skipped, and the summary names the directory as one.
func TestReadLayerNotesSecretDirectoriesThatExist(t *testing.T) {
	dir := t.TempDir()
	secrets := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, secrets, "DATABASE_URL", "postgres://app:pw@db/app\n")
	write(t, secrets, ".hidden", "no\n")
	write(t, dir, ".env", "DATABASE_URL=from-file\n")
	layer, err := ReadLayer(dir, "prod", []string{secrets, filepath.Join(dir, "absent")})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(layer.Names(), ","); got != ".env,"+secrets+string(os.PathSeparator) {
		t.Fatalf("names = %q", got)
	}
	environ := layer.Environ(nil)
	if got := strings.Join(environ, " "); got != "DATABASE_URL=from-file DATABASE_URL=postgres://app:pw@db/app" {
		t.Fatalf("environ = %q", got)
	}
}
