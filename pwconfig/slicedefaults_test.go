package pwconfig

import (
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/tinybind-go/configbind"
)

func TestSecuritySliceDefaultsLoadUsablePatterns(t *testing.T) {
	// Use an isolated subprocess: Load writes the process-wide bindings.
	if os.Getenv("PW_CONFIG_SLICE_HELPER") == "" {
		command := exec.Command(os.Args[0], "-test.run=^TestSecuritySliceDefaultsLoadUsablePatterns$")
		command.Env = append(os.Environ(), "PW_CONFIG_SLICE_HELPER=1")
		command.Dir = t.TempDir()
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, output)
		}
		return
	}
	_, err := configbind.Load(configbind.LoadOptions{Vendor: "popcornweb", Tool: "test", FileName: "config.toml", Args: []string{}, Environ: []string{"SECURITY_CSRF_ENABLED=true"}})
	if err != nil {
		t.Fatal(err)
	}
	security := Value[SecurityConfig]()
	if !reflect.DeepEqual(security.CSRF.Include, []string{"/**"}) {
		t.Fatalf("CSRF include = %#v", security.CSRF.Include)
	}
	if !reflect.DeepEqual(security.CORS.Include, []string{"/**"}) || !reflect.DeepEqual(security.CORS.AllowedMethods, []string{"GET", "HEAD", "POST"}) {
		t.Fatalf("CORS defaults = %#v", security.CORS)
	}
	if len(security.CSRF.Exclude) != 0 {
		t.Fatalf("empty slice = %#v", security.CSRF.Exclude)
	}
	if _, err := middlewares.CSRF(security.CSRF, session.CookieOptions{}, http.SameSiteLaxMode, nil, nil); err != nil {
		t.Fatalf("CSRF default fails startup: %v", err)
	}
}
