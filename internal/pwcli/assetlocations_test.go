package pwcli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAssetLocationsRejectUnsafeOrOverlappingPaths(t *testing.T) {
	root := t.TempDir()
	for _, config := range []assetsConfig{
		{Public: "../public"}, {Derived: "/tmp/public"}, {Embed: "./public.go"},
		{Derived: "public"}, {Derived: "public/output"}, {Public: "dist/public/input"},
		{Derived: "dist/derived"}, {Derived: "dist/cache"},
		{Embed: "cmd/app/public.go"}, {Embed: "public/public.go"},
	} {
		if err := validateAssetLocations(root, config); err == nil {
			t.Errorf("accepted %+v", config)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "cmd")); err != nil {
		t.Fatal(err)
	}
	if err := validateAssetLocations(root, assetsConfig{Public: "cmd/app/public"}); err == nil {
		t.Fatal("accepted a symlinked ancestor")
	}
}

func TestExplicitDefaultAssetLocationsAreByteIdentical(t *testing.T) {
	root := derivedFixture(t)
	writeNestedTestFile(t, filepath.Join(root, "public", "app.css"), compressibleCSS)
	if _, err := buildDerivedAssets(root, assetsConfig{}); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, name := range []string{assetManifestFile, assetManifestJSON, "dist/public/app.css", "dist/public/app.css.br", "dist/public/app.css.zstd", "dist/public/app.css.gz"} {
		var err error
		before[name], err = os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := buildDerivedAssets(root, assetsConfig{Public: "public", Derived: "dist/public", Embed: "public.go"}); err != nil {
		t.Fatal(err)
	}
	for name, expected := range before {
		actual, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(actual, expected) {
			t.Errorf("%s changed with explicit defaults: %v", name, err)
		}
	}
}

func TestConfiguredAssetWatchIncludesInputsAndExcludesOutputs(t *testing.T) {
	root := t.TempDir()
	assets := assetsConfig{Public: "dist/authored", Derived: "build/assets"}
	writeNestedTestFile(t, filepath.Join(root, "dist/authored/app.css"), "body{}")
	writeNestedTestFile(t, filepath.Join(root, "build/assets/app.css"), "old")
	writeNestedTestFile(t, filepath.Join(root, "build/cache/generated.go"), "generated")
	state, err := snapshotAssetWatchFiles(root, assets, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(state) != 1 {
		t.Fatalf("watched generated output or missed source: %#v", state)
	}
	if _, ok := state[filepath.Join(root, "dist/authored/app.css")]; !ok {
		t.Fatal("configured authored asset not watched")
	}
}

func TestUnsafeAssetBuildPreservesAuthoredFiles(t *testing.T) {
	root := derivedFixture(t)
	name := filepath.Join(root, "public/app.css")
	writeTestFile(t, name, "keep")
	if _, err := buildDerivedAssets(root, assetsConfig{Derived: "public"}); err == nil {
		t.Fatal("accepted overlapping output")
	}
	if source, err := os.ReadFile(name); err != nil || string(source) != "keep" {
		t.Fatalf("authored tree was cleared: %q %v", source, err)
	}
}

func TestNestedApplicationPublicAssets(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the application and a Lambda artifact")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf("module example.test/fixture\n\ngo 1.27.0\n\nrequire github.com/shibukawa/popcornweb v0.0.0\nreplace github.com/shibukawa/popcornweb => %s\n", filepath.ToSlash(repository)))
	writeTestFile(t, filepath.Join(root, "fixture.go"), "package fixture\n")
	writeTestFile(t, filepath.Join(root, "popcornweb.toml"), `[project]
name = "app"
main = "./cmd/app"
toolchain = "go"
[generate]
handlers = []
templates = ["cmd/app/handlers"]
queries = []
config = []
[assets]
public = "cmd/app/public"
derived = "cmd/app/dist/public"
embed = "cmd/app/public.go"
[dev.idp]
enabled = false
[dev.otel]
enabled = false
[dev.logs]
enabled = false
[dev.console]
enabled = false
[dev.console.storybook]
enabled = false
[dev.console.data]
enabled = false
[migration]
auto = false
[seed]
auto = false
`)
	writeNestedTestFile(t, filepath.Join(root, "cmd/app/public.go"), `package main
import ("embed"; "io/fs")
//go:embed all:dist/public
var embeddedPublic embed.FS
func PublicFS() fs.FS { tree,err:=fs.Sub(embeddedPublic,"dist/public"); if err!=nil { panic(err) }; return tree }
`)
	writeNestedTestFile(t, filepath.Join(root, "cmd/app/dist/public/.keep"), "")
	writeNestedTestFile(t, filepath.Join(root, "cmd/app/public/app.css"), "body { color: red }\n")
	writeNestedTestFile(t, filepath.Join(root, "cmd/app/handlers/page.pw.html"), `export component Page(): html { <h1>Nested app</h1> }`)
	writeNestedTestFile(t, filepath.Join(root, "cmd/app/main.go"), `package main
import ("context"; "log"; "net/http"; "os"; "github.com/shibukawa/popcornweb/pw")
func main() {
 if err:=pw.ParseConfig(); err!=nil { log.Fatal(err) }
 if handled,err:=pw.RunFrameworkAction(); handled { if err!=nil { log.Fatal(err) }; return }
 if marker:=os.Getenv("RESOURCE_MARKER"); marker!="" { if err:=os.WriteFile(marker,[]byte("opened"),0600); err!=nil { log.Fatal(err) } }
 if err:=pw.Run(context.Background(),http.NotFoundHandler(),pw.WithPublicFS(PublicFS())); err!=nil { log.Fatal(err) }
}
`)
	command := exec.Command("go", "mod", "tidy")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("tidy: %v\n%s", err, output)
	}
	t.Chdir(root)
	var output bytes.Buffer
	if err := runGenerate(context.Background(), nil, &output, &output); err != nil {
		t.Fatalf("generate: %v\n%s", err, &output)
	}
	for _, name := range []string{"cmd/app/dist/public/app.css", "cmd/app/dist/manifest.json", "cmd/app/public_manifest_pw_gen.go"} {
		if _, err := os.Stat(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"public.go", "public_manifest_pw_gen.go", "public", "dist"} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("unexpected root asset %s: %v", name, err)
		}
	}
	manifest, _ := os.ReadFile("cmd/app/public_manifest_pw_gen.go")
	if !bytes.Contains(manifest, []byte("package main")) {
		t.Fatal("manifest uses the module-root package")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	_, port, _ := net.SplitHostPort(address)
	t.Setenv("PORT", port)
	t.Setenv("SERVER_HEALTH", "/health")
	t.Setenv("OBSERVABILITY_EXPORT_ENABLED", "false")
	t.Setenv("SERVER_PUBLIC_LOCAL_ROOT", "wrong/tree") // pw dev must override it.
	logFile, err := os.CreateTemp(root, "dev-log-")
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runDev(ctx, nil, logFile, logFile) }()
	stopped := false
	defer func() {
		if !stopped {
			cancel()
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Error("pw dev did not stop")
			}
		}
	}()
	waitForAsset(t, "http://"+address+"/public/app.css", "red", logFile.Name())
	// The derived tree is not watched. This must come from the configured
	// local mount, while the running binary still embeds the original red CSS.
	writeTestFile(t, "cmd/app/dist/public/app.css", "body { color: green }\n")
	waitForAsset(t, "http://"+address+"/public/app.css", "green", logFile.Name())
	writeTestFile(t, "cmd/app/public/app.css", "body { color: blue }\n")
	waitForAsset(t, "http://"+address+"/public/app.css", "blue", logFile.Name())
	cancel()
	select {
	case <-done:
		stopped = true
	case <-time.After(15 * time.Second):
		t.Fatal("pw dev did not stop")
	}
	if err := runBuild(context.Background(), nil, &output, &output); err != nil {
		t.Fatalf("build: %v\n%s", err, &output)
	}
	marker := filepath.Join(root, "resource-opened")
	command = exec.Command(filepath.Join(root, "app"), "--generate-config=toml")
	command.Env = append(os.Environ(), "RESOURCE_MARKER="+marker)
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("early framework action: %v\n%s", err, result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("config generation opened application resources")
	}
	app := exec.Command(filepath.Join(root, "app"))
	app.Stdout = logFile
	app.Stderr = logFile
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Process.Kill(); _ = app.Wait() }()
	waitForAsset(t, "http://"+address+"/public/app.css", "blue", logFile.Name())
	// The probe must run its action before opening application resources.
	command = exec.Command(filepath.Join(root, "app"), "healthcheck")
	command.Env = append(os.Environ(), "RESOURCE_MARKER="+marker)
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("healthcheck: %v\n%s", err, result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("healthcheck opened application resources")
	}
	if err := runBuild(context.Background(), []string{"--target=lambda"}, &output, &output); err != nil {
		t.Fatalf("lambda: %v\n%s", err, &output)
	}
	stage := filepath.Join(root, ".pw/build/lambda/nethttp")
	dockerfile, err := os.ReadFile(filepath.Join(stage, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(dockerfile, []byte("config.prod.toml")) {
		t.Fatalf("missing config referenced:\n%s", dockerfile)
	}
	binary, err := os.ReadFile(filepath.Join(stage, "bootstrap"))
	if err != nil || !bytes.HasPrefix(binary, []byte{0x7f, 'E', 'L', 'F'}) {
		t.Fatalf("Lambda binary: %v", err)
	}
	if err := runBuild(context.Background(), []string{"--target=vercel-go"}, &output, &output); err != nil {
		t.Fatalf("source deployment: %v\n%s", err, &output)
	}
	for _, name := range []string{"api/dist/public/app.css", "app/cmd/app/handlers/page_pw_gen.go"} {
		if _, err := os.Stat(filepath.Join(root, ".pw/build/vercel-go/nethttp", filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(root, "config.prod.toml"), "[server]\nport = 8080\n")
	if err := runBuild(context.Background(), []string{"--target=lambda"}, &output, &output); err != nil {
		t.Fatalf("lambda with config: %v\n%s", err, &output)
	}
	dockerfile, _ = os.ReadFile(filepath.Join(stage, "Dockerfile"))
	if !bytes.Contains(dockerfile, []byte("bootstrap config.prod.toml")) {
		t.Fatalf("present config not copied:\n%s", dockerfile)
	}
}

func waitForAsset(t *testing.T, address, contains, logfile string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(address)
		if err == nil {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && strings.Contains(string(body), contains) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	output, _ := os.ReadFile(logfile)
	t.Fatalf("%s never served %q\n%s", address, contains, output)
}
