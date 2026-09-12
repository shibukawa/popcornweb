package pwconfig_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The point of this package is that a build can bind configuration without the
// net/http runtime, so the two things worth asserting are that it does not
// reach one and that a build which does not link one still gets settings.
//
// Both are checked by asking the toolchain rather than by reading imports:
// a dependency arrives through any package in the graph, and a file-level check
// would pass while the binary still carried it.

const (
	pwPackage       = "github.com/shibukawa/popcornweb/pw"
	databasePackage = "github.com/shibukawa/popcornweb/pwdatabase"
	sessionPackage  = "github.com/shibukawa/popcornweb/pwsession"
	observePackage  = "github.com/shibukawa/popcornweb/pwobservability"
	authPackage     = "github.com/shibukawa/popcornweb/plugin/auth"
	authFastPackage = "github.com/shibukawa/popcornweb/plugin/auth/authfast"
	fastPackage     = "github.com/shibukawa/popcornweb/pwfast"
	configPackage   = "github.com/shibukawa/popcornweb/pwconfig"
	runtimePackage  = "github.com/shibukawa/popcornweb/pwruntime"
	fastOnlyPackage = "github.com/shibukawa/popcornweb/internal/fastonly"
)

// TestNoSharedLayerReachesATransport is the containment these packages exist
// for. It is not a style rule: pwfast reads what they publish, so a dependency
// in the other direction would put the whole net/http stack in every build that
// wanted a configuration file, a database pool, or a session.
//
// requirement:alternate-http-backend-readiness names the four —  configuration
// binding, the database layer, session, and observability — so all four are
// asserted here rather than each in its own package: what matters is that the
// set holds, and a caller adding a fifth should have one place to add it.
func TestNoSharedLayerReachesATransport(t *testing.T) {
	for _, layer := range []string{configPackage, databasePackage, sessionPackage, observePackage} {
		for _, forbidden := range []string{pwPackage, fastPackage} {
			if dependsOn(t, layer, forbidden) {
				t.Errorf("%s depends on %s", layer, forbidden)
			}
		}
	}
}

// The layers stack one way, and the direction is the point: what a settings
// file asked for is decided before anything is opened, and what was opened is
// decided before a session is stored in it. A cycle would be a startup order
// nobody could state.
func TestTheSharedLayersStackOneWay(t *testing.T) {
	for _, edge := range []struct{ from, to string }{
		{databasePackage, configPackage},
		{sessionPackage, configPackage},
		{sessionPackage, databasePackage},
		{observePackage, configPackage},
	} {
		if !dependsOn(t, edge.from, edge.to) {
			t.Errorf("%s no longer depends on %s", edge.from, edge.to)
		}
		if dependsOn(t, edge.to, edge.from) {
			t.Errorf("%s depends back on %s", edge.to, edge.from)
		}
	}
}

// The second transport's runtime must not reach the first either. It is checked
// here rather than there because this is the package whose move made it true,
// and a regression would most likely arrive as a configuration read.
func TestTheSecondTransportReachesTheFirstOnlyThroughTheSharedLeaves(t *testing.T) {
	if dependsOn(t, fastPackage, pwPackage) {
		t.Errorf("%s depends on %s", fastPackage, pwPackage)
	}
	// It reaches the shared leaf, which is where the settings a chain is built
	// from arrive: this package publishes what it resolved and pwruntime carries
	// it.
	if !dependsOn(t, fastPackage, runtimePackage) {
		t.Errorf("%s no longer depends on %s", fastPackage, runtimePackage)
	}
	// It reaches this package too, and that is the lifecycle rather than the
	// chain. pwfast owns startup for its transport the way pw owns it for the
	// other — parsing the settings, opening the pool, building the session
	// manager — because pwfast.Run is what pw.Run rewrites to, and an import
	// rewrite maps one package onto one package.
	//
	// What that does not weaken is the property this clause used to assert:
	// pwfast.Middlewares still composes from published settings alone, which
	// lifecycle_test proves by publishing a reduction by hand and building a
	// chain with nothing parsed. A test seam, an end-to-end fixture and
	// internal/fastonly all rely on it.
	if !dependsOn(t, fastPackage, configPackage) {
		t.Errorf("%s no longer depends on %s, so it cannot own its own startup", fastPackage, configPackage)
	}
}

// The authentication plugin reaches no transport runtime either, and neither
// does its fasthttp half.
//
// It is the case the layer work was for. plugin/auth is transport-free almost
// everywhere — it reads settings, opens stores, verifies tokens, decides — and
// two lines of it were not: registering a frame of the net/http chain, and
// writing a problem over net/http. Those two put the whole runtime into every
// build that imported the plugin, including one serving on the other transport
// that would never call either. pwextension is where they went.
func TestTheAuthenticationPluginReachesNoTransportRuntime(t *testing.T) {
	for _, layer := range []string{authPackage, authFastPackage} {
		if dependsOn(t, layer, pwPackage) {
			t.Errorf("%s depends on %s", layer, pwPackage)
		}
	}
	// The net/http half is still net/http-shaped, and that is not the same
	// thing: what is worth not linking is the framework built on the protocol
	// library, not the library.
	if !dependsOn(t, authPackage, "net/http") {
		t.Errorf("%s no longer speaks net/http, which its own half is written in", authPackage)
	}
	if dependsOn(t, authFastPackage, fastPackage) != true {
		t.Errorf("%s does not depend on %s", authFastPackage, fastPackage)
	}
}

// A build with no net/http runtime in it parses a configuration file and
// serves a fasthttp request with what it read.
//
// This is the whole claim, run end to end against a real package rather than a
// fixture: what is asserted is a property of a linked binary, so the toolchain
// has to have resolved one. internal/fastonly is that binary.
func TestAFastHTTPBuildBindsConfigurationWithoutTheNetHTTPRuntime(t *testing.T) {
	if dependsOn(t, fastOnlyPackage, pwPackage) {
		t.Fatalf("%s depends on %s, so this proves nothing", fastOnlyPackage, pwPackage)
	}
	if testing.Short() {
		t.Skip("runs a program")
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, `
[server]
port = 9999
health = "/healthz"
public.enabled = false

[middleware]
request_id = false
access_log = false
`)

	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	output, err := run(t, root, "go", "run", "./internal/fastonly", path)
	if err != nil {
		t.Fatalf("the pw-free program failed: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(output); got != "port=9999 health=/healthz probe=200" {
		t.Fatalf("output = %q", got)
	}
}

// dependsOn reports whether one package reaches another through any path.
var dependencyCache = struct {
	sync.Mutex
	packages map[string]map[string]struct{}
}{packages: make(map[string]map[string]struct{})}

func dependsOn(t *testing.T, from, to string) bool {
	t.Helper()
	dependencyCache.Lock()
	packages, cached := dependencyCache.packages[from]
	dependencyCache.Unlock()
	if !cached {
		output, err := run(t, "", "go", "list", "-deps", from)
		if err != nil {
			t.Fatalf("go list -deps %s: %v\n%s", from, err, output)
		}
		packages = make(map[string]struct{})
		for _, line := range strings.Split(output, "\n") {
			if name := strings.TrimSpace(line); name != "" {
				packages[name] = struct{}{}
			}
		}
		dependencyCache.Lock()
		if previous, alreadyCached := dependencyCache.packages[from]; alreadyCached {
			packages = previous
		} else {
			dependencyCache.packages[from] = packages
		}
		dependencyCache.Unlock()
	}
	_, found := packages[to]
	return found
}

func run(t *testing.T, directory string, name string, args ...string) (string, error) {
	t.Helper()
	command := exec.Command(name, args...)
	if directory != "" {
		command.Dir = directory
	}
	output, err := command.CombinedOutput()
	return string(output), err
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
