package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/contrib/passkey"
	"github.com/shibukawa/popcornweb/contrib/passkey/passkeytest"
	_ "github.com/shibukawa/popcornweb/database/sqlite"
	"github.com/shibukawa/popcornweb/plugin/auth"
	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/testutil"
)

// TestPasskeyEnrollmentAndLogin drives the whole oidc_passkey story this sample
// is about: the provider bootstraps the account, the account enrolls a passkey,
// and the passkey alone signs it back in.
//
// The authenticator is contrib/passkey/passkeytest, which answers the ceremony
// with the JSON a browser would post, so no browser and no hardware is needed.
func TestPasskeyEnrollmentAndLogin(t *testing.T) {
	RegisterAccounts()
	port := reservePort(t)
	// A relying party is scoped to a domain, so the test reaches the server by
	// name. The 127.0.0.1 the listener reports could never be an RP ID.
	origin := fmt.Sprintf("http://localhost:%d", port)

	passkeyServer(t, port, origin)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	browser := &ceremony{t: t, client: client, origin: origin}

	// The provider bootstraps the account, exactly as it does in oidc_only.
	if body := fetch(t, client, origin+"/mypage"); !strings.Contains(body, "Hanako Yamada") {
		t.Fatalf("provider login did not reach the protected page: %s", body)
	}

	// The account adds a passkey. The authenticator stands in for the platform
	// one a real browser would use.
	authenticator, err := passkeytest.NewAuthenticator(passkeytest.WithOrigin(origin))
	if err != nil {
		t.Fatal(err)
	}
	var creation passkey.CreationOptions
	browser.postJSON("/auth/passkey/register/begin", nil, &creation)
	if creation.RP.ID != "localhost" {
		t.Fatalf("rp.id = %q, want the configured relying party", creation.RP.ID)
	}
	registration, err := authenticator.Create(creation)
	if err != nil {
		t.Fatal(err)
	}
	browser.post("/auth/passkey/register/finish", registration, http.StatusOK)

	// Sign out of everything, then sign back in with the credential alone.
	browser.logout()
	if body := fetch(t, client, origin+"/"); strings.Contains(body, "Hanako Yamada") {
		t.Fatalf("the session survived the logout: %s", body)
	}

	var request passkey.RequestOptions
	browser.postJSON("/auth/passkey/login/begin", nil, &request)
	assertion, err := authenticator.Get(request)
	if err != nil {
		t.Fatal(err)
	}
	browser.post("/auth/passkey/login/finish", assertion, http.StatusOK)

	// The passkey login reached the same account the provider created, which is
	// what lookupAccount is for.
	if body := fetch(t, client, origin+"/mypage"); !strings.Contains(body, "Hanako Yamada") {
		t.Fatalf("passkey login did not reach the account: %s", body)
	}
}

// TestForgedAssertionIsRefused proves the sample verifies the ceremony rather
// than trusting whatever the page posted.
func TestForgedAssertionIsRefused(t *testing.T) {
	RegisterAccounts()
	port := reservePort(t)
	origin := fmt.Sprintf("http://localhost:%d", port)
	passkeyServer(t, port, origin)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	browser := &ceremony{t: t, client: client, origin: origin}
	fetch(t, client, origin+"/mypage")

	authenticator, err := passkeytest.NewAuthenticator(passkeytest.WithOrigin(origin))
	if err != nil {
		t.Fatal(err)
	}
	var creation passkey.CreationOptions
	browser.postJSON("/auth/passkey/register/begin", nil, &creation)
	registration, err := authenticator.Create(creation)
	if err != nil {
		t.Fatal(err)
	}
	browser.post("/auth/passkey/register/finish", registration, http.StatusOK)
	browser.logout()

	for _, fault := range []passkeytest.Fault{
		passkeytest.FaultSignature,
		passkeytest.FaultOrigin,
		passkeytest.FaultChallenge,
	} {
		t.Run(string(fault), func(t *testing.T) {
			authenticator.SetFault(fault)
			defer authenticator.SetFault(passkeytest.FaultNone)
			var request passkey.RequestOptions
			browser.postJSON("/auth/passkey/login/begin", nil, &request)
			assertion, err := authenticator.Get(request)
			if err != nil {
				t.Fatal(err)
			}
			browser.post("/auth/passkey/login/finish", assertion, http.StatusForbidden)
		})
	}
}

// passkeyServer starts this sample in oidc_passkey mode against the same
// development identity provider pw dev runs.
// passkeyServer starts this sample in oidc_passkey mode. Each override runs
// after the defaults, so a test can move one piece such as the session backend
// without restating the rest.
func passkeyServer(t *testing.T, port int, origin string, overrides ...func(*testutil.Config)) *testutil.Server {
	t.Helper()
	return testutil.TestRun(t, Handlers(), func(config *testutil.Config) {
		config.Update(func(server *pw.ServerConfig) {
			server.Port = port
			server.Public.Enabled = false
		})
		config.Update(func(middleware *pw.MiddlewareConfig) {
			middleware.RDB = pw.RDBConfig{
				Enabled: true,
				Connections: []pw.RDBConnectionConfig{{
					DSN: "sqlite://:memory:", ConnectTimeout: time.Second,
					MaxOpenConns: 1, MaxIdleConns: 1,
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
			settings.Mode = auth.ModeOIDCPasskey
			settings.PostLoginPath = "/"
			settings.Protection.Include = []string{"/mypage"}
			settings.Protection.Unauthenticated = "redirect"
			settings.RecentAuthMaxAge = 5 * time.Minute
			settings.Recovery.Policy = auth.RecoveryOIDC
			settings.Passkey = auth.PasskeyConfig{
				Path: "/auth/passkey", RPID: "localhost", RPName: "Passkey login",
				Origins:          []string{origin},
				UserVerification: auth.UserVerificationRequired,
				Discoverable:     auth.DiscoverablePreferred,
			}
			settings.OIDC.RedirectURL = origin + "/auth/callback"
			settings.OIDC.Scopes = []string{"profile", "email"}
			settings.OIDC.IdentityClaim = "employee_number"
			settings.OIDC.Admission = auth.AdmissionAuthenticated
			settings.OIDC.AutoProvision = true
			settings.OIDC.AllowLoopbackHTTP = true
			// A logout ends the provider session too, which is what config.dev.toml
			// configures and what the assertion below checks: the default is
			// reconfirm, which leaves the provider signed in and never hops to it.
			settings.OIDC.LogoutScope = auth.LogoutScopeGlobal
		})
		for _, override := range overrides {
			override(config)
		}
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
}

// ceremony drives the ceremony endpoints the way the page's passkey.js does:
// same origin, JSON bodies, and a cookie jar.
type ceremony struct {
	t      *testing.T
	client *http.Client
	origin string
}

func (c *ceremony) post(path string, body any, want int) []byte {
	c.t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		c.t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(c.t.Context(), http.MethodPost, c.origin+path, bytes.NewReader(encoded))
	if err != nil {
		c.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", c.origin)
	response, err := c.client.Do(request)
	if err != nil {
		c.t.Fatal(err)
	}
	defer response.Body.Close()
	payload := new(bytes.Buffer)
	if _, err := payload.ReadFrom(response.Body); err != nil {
		c.t.Fatal(err)
	}
	if response.StatusCode != want {
		c.t.Fatalf("POST %s status = %d, want %d; body %q", path, response.StatusCode, want, payload)
	}
	return payload.Bytes()
}

func (c *ceremony) postJSON(path string, body any, target any) {
	c.t.Helper()
	payload := c.post(path, body, http.StatusOK)
	if err := json.Unmarshal(payload, target); err != nil {
		c.t.Fatalf("POST %s returned %q: %v", path, payload, err)
	}
}

func (c *ceremony) logout() {
	c.t.Helper()
	request, err := http.NewRequestWithContext(c.t.Context(), http.MethodPost, c.origin+"/auth/logout", nil)
	if err != nil {
		c.t.Fatal(err)
	}
	request.Header.Set("Origin", c.origin)
	response, err := c.client.Do(request)
	if err != nil {
		c.t.Fatal(err)
	}
	_ = response.Body.Close()
}
