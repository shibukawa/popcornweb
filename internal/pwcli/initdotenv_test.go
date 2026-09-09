package pwcli

import (
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/internal/dotenv"
)

// The template names what the selected capabilities read from the environment
// and nothing else, so a project that declined a capability is never told to
// supply a secret nothing binds.
func TestDotenvTemplateFollowsTheSelectedCapabilities(t *testing.T) {
	minimal := scaffoldFiles(initOptions{Name: "fixture", Auth: authNone, Router: routerRegistered})[".env.example"]
	for _, absent := range []string{"DATABASE_URL", "SESSION_KEYRING_SECRET", "AUTH_OIDC_CLIENT_SECRET"} {
		if strings.Contains(minimal, absent) {
			t.Errorf("a project without the capability is told to set %s:\n%s", absent, minimal)
		}
	}
	full := scaffoldFiles(initOptions{
		Name: "fixture", Database: true, Auth: authOIDC, Session: sessionRedis, Router: routerRegistered,
	})[".env.example"]
	for _, present := range []string{"DATABASE_URL=", "SESSION_KEYRING_SECRET=", "SESSION_REDIS_DSN=", "AUTH_OIDC_ISSUER=", "AUTH_OIDC_CLIENT_ID=", "AUTH_OIDC_CLIENT_SECRET="} {
		if !strings.Contains(full, "\n"+present+"\n") {
			t.Errorf("the template does not name %s with an empty value:\n%s", present, full)
		}
	}
}

// The template is committed, so it must parse as the loader parses a copy of it
// and must assign no value: a value here is exactly what PW0438 reports.
func TestDotenvTemplateParsesAndAssignsNothing(t *testing.T) {
	template := scaffoldFiles(initOptions{
		Name: "fixture", Database: true, Auth: authOIDC, Router: routerRegistered,
	})[".env.example"]
	entries, err := dotenv.Parse(".env.example", []byte(template))
	if err != nil {
		t.Fatalf("the template does not parse as a dotenv file: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the template names no variable")
	}
	for _, entry := range entries {
		if entry.Value != "" {
			t.Errorf("%s is assigned %q in the template", entry.Name, entry.Value)
		}
	}
	if !strings.Contains(template, "# APP_ENV=dev") {
		t.Error("the template does not show how to pin the checkout's environment")
	}
}
