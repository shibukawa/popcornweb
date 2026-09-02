package handlers

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/plugin/auth"
	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/testutil"
	_ "oidclogin"
	_ "oidclogin/templates"

	// Storage is opt-in by blank import. main.go carries these for the binary;
	// a test builds its own binary and has to link them itself.
	_ "github.com/shibukawa/popcornweb/authstate/sqlite"
	_ "github.com/shibukawa/popcornweb/database/sqlite"
	_ "github.com/shibukawa/popcornweb/sessionstore/sqlite"
)

// TestLoginProvisionsAnAccountAndSignsOut drives the framework endpoints
// against the same development identity provider pw dev starts. The
// application under test registers only its account resolver.
func TestLoginProvisionsAnAccountAndSignsOut(t *testing.T) {
	RegisterAccountResolver()
	// The redirect URI is fixed at client construction, so the test server
	// needs a port it can name in advance.
	port := reservePort(t)
	server := testutil.TestRun(t, Handlers(), func(config *testutil.Config) {
		config.Update(func(server *pw.ServerConfig) {
			server.Port = port
			server.Public.Enabled = false
		})
		config.Update(func(middleware *pw.MiddlewareConfig) {
			middleware.RDB = pw.RDBConfig{
				Enabled: true,
				Connections: []pw.RDBConnectionConfig{{
					DSN:            "sqlite://:memory:",
					ConnectTimeout: time.Second,
					MaxOpenConns:   1,
					MaxIdleConns:   1,
				}},
			}
		})
		config.Update(func(security *pw.SecurityConfig) {
			// The pages carry a logout form, and a rendered form needs the
			// token: the template emits a hidden field and the render fails
			// rather than shipping an unprotected one.
			security.CSRF.Enabled = true
			// The include list is spelled out because this config is built in
			// code: the default lives in a struct tag the file decoder parses,
			// and nothing parsed it on this path.
			security.CSRF.Include = []string{"/**"}
		})
		config.Update(func(session *pw.SessionConfig) {
			session.Enabled = true
			session.Backend = "rdb"
			session.Retention = time.Hour
			session.Cookie.Secure = false
			// Authentication registers a browser-protected slot, so the session
			// manager refuses to start without a keyring. A fixed value is right
			// here and only here: a test that generated one per run would still
			// pass while a project with no secret configured could not start.
			session.Keyring.Secret = base64.StdEncoding.EncodeToString(make([]byte, 32))
		})
		config.Update(func(settings *auth.Config) {
			settings.Enabled = true
			settings.Mode = "oidc_only"
			settings.PostLoginPath = "/"
			settings.Protection.Include = []string{"/mypage"}
			settings.Protection.Unauthenticated = "redirect"
			settings.OIDC.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/auth/callback", port)
			settings.OIDC.Scopes = []string{"profile", "email"}
			settings.OIDC.IdentityClaim = "employee_number"
			settings.OIDC.Admission = "authenticated"
			settings.OIDC.AutoProvision = true
			settings.OIDC.LogoutScope = auth.LogoutScopeGlobal
			settings.OIDC.AllowLoopbackHTTP = true
		})
	}, testutil.WithMigrations("../migrations"), testutil.WithIdentityProvider(
		testutil.WithIdPConfig("../devidp.toml"),
		testutil.WithLoginUser("hanako"),
		testutil.WithIdPBinding(func(config *testutil.Config, idp testutil.IdPInfo) {
			config.Update(func(settings *auth.Config) {
				settings.OIDC.Issuer = idp.Issuer
				settings.OIDC.ClientID = idp.ClientID
				settings.OIDC.ClientSecret = idp.ClientSecret
			})
		}),
	))

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	// The guard sends an anonymous request through the login first, and the
	// pre-selected roster user completes it without a browser.
	body := fetch(t, client, server.URL+"/mypage")
	for _, fragment := range []string{"Hanako Yamada", "employee_number", "EMP-0001"} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("protected page is missing %q: %s", fragment, body)
		}
	}

	// A browser form post carries Origin, and the endpoint requires it.
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/auth/logout",
		strings.NewReader(url.Values{}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", server.URL)
	var visited []string
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		visited = append(visited, request.URL.String())
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if response.Request.URL.Path != "/" {
		t.Fatalf("logout landed on %q", response.Request.URL)
	}
	// Logging out ends the provider session as well, so the browser passes
	// through the provider on its way back. Without that hop the provider
	// stays signed in and the next login returns the same user silently.
	issuer := server.IdPInfo().Issuer
	endSession := false
	for _, hop := range visited {
		if strings.HasPrefix(hop, issuer+"/end_session") {
			endSession = true
		}
	}
	if !endSession {
		t.Fatalf("logout never reached the provider: %v", visited)
	}

	// The session is gone: the protected page is only reachable by logging in
	// again, which the pre-selected user does silently.
	if body := fetch(t, client, server.URL+"/"); strings.Contains(body, "Hanako Yamada") {
		t.Fatalf("the session survived the logout: %s", body)
	}
}

// reservePort returns a loopback port that is free right now.
func reservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func fetch(t *testing.T, client *http.Client, target string) string {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A browser says what it wants back, and the CSRF middleware reads it: only
	// an HTML request is given a secret, because only an HTML response renders a
	// form. Without this header the page renders with no token, and a template
	// holding an unsafe form fails the render rather than shipping one
	// unprotected.
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %s", target, response.StatusCode, body)
	}
	return string(body)
}
