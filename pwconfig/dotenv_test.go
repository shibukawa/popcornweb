package pwconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/internal/dotenv"
	"github.com/shibukawa/tinybind-go/configbind"
)

// A value a dotenv file set is attributed to that file by its name rather than
// the path the load was handed, a value the process set stays the process's,
// and the process wins over every file.
func TestDotenvLoadAttributesValuesToTheFileThatSetThem(t *testing.T) {
	dir := t.TempDir()
	writeDotenv(t, dir, ".env", "PORT=9001\nSESSION_KEYRING_SECRET=from-base\n")
	writeDotenv(t, dir, ".env.stg.local", "PORT=9002\n")
	layer, err := dotenv.ReadLayer(dir, "stg", nil)
	if err != nil {
		t.Fatal(err)
	}
	options := configbind.LoadOptions{
		Vendor: "popcornweb-test", Tool: "pw-test", FileName: "config.toml", Args: []string{},
		ExtraConfigReadPaths: []string{filepath.Join(dir, "config.stg.toml")},
	}
	result, err := dotenv.Load(options, layer, []string{"APP_ENV=stg", "HTML_STREAMING=false"})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]struct {
		raw   string
		place configbind.Place
	}{
		"server.port":            {"9002", configbind.PlaceEnvFile + ".env.stg.local"},
		"session.keyring.secret": {"from-base", configbind.PlaceEnvFile + ".env"},
		"html.streaming":         {"false", configbind.PlaceEnv},
	} {
		entry, ok := result.Overlay.Get(key)
		if !ok {
			t.Errorf("%s was not loaded", key)
			continue
		}
		if entry.Raw != want.raw || entry.Place != want.place {
			t.Errorf("%s = %q from %q, want %q from %q", key, entry.Raw, entry.Place, want.raw, want.place)
		}
	}

	result, err = dotenv.Load(options, layer, []string{"APP_ENV=stg", "PORT=9003"})
	if err != nil {
		t.Fatal(err)
	}
	if entry, _ := result.Overlay.Get("server.port"); entry.Raw != "9003" || entry.Place != configbind.PlaceEnv {
		t.Fatalf("the process must win over both files: %+v", entry)
	}
	if len(result.EnvFiles) != 2 || result.EnvFiles[0].Path != ".env" || result.EnvFiles[0].Secret ||
		result.EnvFiles[1].Path != ".env.stg.local" || !result.EnvFiles[1].Secret {
		t.Fatalf("EnvFiles = %+v, want the names rather than the paths, with the local file secret", result.EnvFiles)
	}
}

// Nothing supplied Environ, so the process reads its own working directory: the
// token comes from the base file when the shell did not set it, and the files
// are what the load layers under the process environment.
func TestResolveLoadOptionsReadsTheWorkingDirectoryDotenvFiles(t *testing.T) {
	dir := t.TempDir()
	writeDotenv(t, dir, ".env", "APP_ENV=stg\n")
	writeDotenv(t, dir, ".env.stg.local", "PORT=9002\n")
	t.Chdir(dir)
	t.Setenv(EnvVar, "")

	plan, err := resolveLoadOptions(configbind.LoadOptions{Vendor: "popcornweb-test", Tool: "pw-test"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.env != EnvStaging || !plan.declared {
		t.Fatalf("env = %q declared = %v, want stg declared by .env", plan.env, plan.declared)
	}
	if plan.dotenv == nil || strings.Join(plan.dotenv.Names(), ",") != ".env,.env.stg.local" {
		t.Fatalf("dotenv layer = %+v", plan.dotenv)
	}
	if plan.options.ExtraConfigReadPaths[0] != "config.stg.toml" {
		t.Fatalf("the TOML candidates must follow the token the base file set: %v", plan.options.ExtraConfigReadPaths)
	}
	if !strings.Contains(strings.Join(plan.options.Environ, "\n"), "PORT=9002") {
		t.Fatal("the composed environment does not carry the file's value")
	}
}

// A caller that supplies Environ has said no filesystem stands behind it.
func TestResolveLoadOptionsReadsNoDotenvFileForASuppliedEnviron(t *testing.T) {
	dir := t.TempDir()
	writeDotenv(t, dir, ".env", "APP_ENV=stg\n")
	t.Chdir(dir)
	plan, err := resolveLoadOptions(configbind.LoadOptions{Vendor: "popcornweb-test", Tool: "pw-test", Environ: []string{"APP_ENV=prod"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.dotenv != nil || plan.env != EnvProduction {
		t.Fatalf("plan = %+v", plan)
	}
}

func writeDotenv(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Parse holds the registry lock while it calls the Loaded hook, and the startup
// summary reads the dotenv files from inside that hook. The two must not share
// a lock, or the first process to start with the summary on never starts.
func TestParseCanReportDotenvFilesFromTheLoadedHook(t *testing.T) {
	configState.Lock()
	previousOptions, previousHooks, previousParsed, previousErr := configState.options, configState.hooks, configState.parsed, configState.parseErr
	configState.parsed, configState.parseErr = false, nil
	configState.Unlock()
	defer func() {
		configState.Lock()
		configState.options, configState.hooks, configState.parsed, configState.parseErr = previousOptions, previousHooks, previousParsed, previousErr
		configState.Unlock()
	}()
	dir := t.TempDir()
	writeDotenv(t, dir, ".env", "PORT=9004\n")
	t.Chdir(dir)
	t.Setenv(EnvVar, "")

	var seen []string
	SetHooks(Hooks{Loaded: func(*configbind.LoadResult) { seen = DotenvFiles() }})
	SetLoadOptions(configbind.LoadOptions{Vendor: "popcornweb-test", Tool: "pw-test", Args: []string{}})
	done := make(chan error, 1)
	go func() { done <- Parse() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Parse did not return: the Loaded hook deadlocked on the registry lock")
	}
	if len(seen) != 1 || seen[0] != ".env" {
		t.Fatalf("the hook saw %v, want the base file", seen)
	}
	if Value[ServerConfig]().Port != 9004 {
		t.Fatalf("port = %d, want the dotenv value", Value[ServerConfig]().Port)
	}
}

// A value from a .local file or a secret mount is masked by origin, whatever
// the key is called, and a ${NAME} the TOML expands from one is masked too.
func TestDotenvSecretSourcesAreMaskedByOrigin(t *testing.T) {
	dir := t.TempDir()
	writeDotenv(t, dir, ".env", "PORT=9001\n")
	writeDotenv(t, dir, ".env.local", "PORT=9002\n")
	secrets := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	writeDotenv(t, secrets, "SERVICE_NAME", "from-mount\n")
	if err := os.WriteFile(filepath.Join(dir, "config.prod.toml"), []byte("[observability]\nservice_name = \"${SERVICE_NAME}\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	layer, err := dotenv.ReadLayer(".", "prod", []string{secrets})
	if err != nil {
		t.Fatal(err)
	}
	result, err := dotenv.Load(configbind.LoadOptions{
		Vendor: "popcornweb-test", Tool: "pw-test", FileName: "config.toml", Args: []string{},
		ExtraConfigReadPaths: []string{"config.prod.toml"},
	}, layer, []string{"APP_ENV=prod"})
	if err != nil {
		t.Fatal(err)
	}
	shown := map[string]configbind.ProvenanceEntry{}
	for _, entry := range result.Provenance() {
		shown[entry.Key] = entry
	}
	if port := shown["server.port"]; !port.Masked || port.Value == "9002" || port.Place != configbind.PlaceEnvFile+".env.local" {
		t.Fatalf("a .local value must be masked and attributed: %+v", port)
	}
	if raw, _ := result.Overlay.Get("server.port"); raw.Raw != "9002" {
		t.Fatalf("the bound value must still be the file's: %+v", raw)
	}
	if name := shown["observability.service_name"]; !name.Masked || name.Value == "from-mount" {
		t.Fatalf("a TOML value expanded from a mount must be masked: %+v", name)
	}
	if got := strings.Join(result.EnvSecretDirs, ","); got != secrets {
		t.Fatalf("EnvSecretDirs = %q", got)
	}
}
