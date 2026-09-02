package testutil

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/shibukawa/popcornweb/authstate/memory"
	"github.com/shibukawa/popcornweb/contrib/oauth"
	"github.com/shibukawa/popcornweb/contrib/oidc"
	"github.com/shibukawa/popcornweb/pw"
)

const testRoster = `
[users.admin]
display_name = "Administrator"
[users.admin.claims]
email = "admin@example.com"
role = "admin"

[users.guest]
display_name = "Guest User"
[users.guest.claims]
email = "guest@example.com"
`

// loginApp is a minimal relying party: it starts an OIDC login on /login and
// reports the verified subject on the callback. It resolves its own redirect
// URI from the request host, which is the pattern the ephemeral loopback client
// exists to support.
type loginApp struct {
	info IdPInfo

	once   sync.Once
	setup  error
	mu     sync.Mutex
	client *oidc.Client
	key    string
}

func (app *loginApp) build(host string) error {
	app.once.Do(func() {
		// Background, not the request context: the discovered provider caches
		// its key set for the lifetime of the application.
		provider, err := oidc.Discover(context.Background(), app.info.Issuer, oidc.DiscoverOptions{AllowLoopbackHTTP: true})
		if err != nil {
			app.setup = err
			return
		}
		store, err := memory.NewStore[oauth.Transaction](memory.Options{})
		if err != nil {
			app.setup = err
			return
		}
		app.client, app.setup = oidc.NewClient(provider, oidc.Config{
			ClientID:          app.info.ClientID,
			ClientSecret:      app.info.ClientSecret,
			RedirectURI:       "http://" + host + "/auth/callback",
			AllowLoopbackHTTP: true,
		}, oidc.Options{OAuth: oauth.Options{StateStore: store}})
	})
	return app.setup
}

func (app *loginApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := app.build(r.Host); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch r.URL.Path {
	case "/login":
		target, key, err := app.client.BeginAuthorization(r.Context(), oidc.BeginOptions{
			Scopes: []string{"openid", "profile", "email"},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		app.mu.Lock()
		app.key = key
		app.mu.Unlock()
		http.Redirect(w, r, target, http.StatusFound)
	case "/auth/callback":
		app.mu.Lock()
		key := app.key
		app.mu.Unlock()
		tokens, _, err := app.client.HandleCallback(r.Context(), key, oidc.Callback{
			Code:  r.URL.Query().Get("code"),
			State: r.URL.Query().Get("state"),
			Error: r.URL.Query().Get("error"),
		}, oidc.CallbackOptions{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		claims, err := app.client.UserInfo(r.Context(), tokens.AccessToken)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		var subject, email string
		_ = decodeJSONString(claims["sub"], &subject)
		_ = decodeJSONString(claims["email"], &email)
		_, _ = io.WriteString(w, "signed in as "+subject+" <"+email+">")
	default:
		http.NotFound(w, r)
	}
}

func TestWithIdentityProviderCompletesLoginWithoutABrowser(t *testing.T) {
	app := &loginApp{}
	server := TestRun(t, app, disablePublicAssets, WithIdentityProvider(
		WithIdPRoster(testRoster),
		WithLoginUser("admin"),
		WithIdPBinding(func(_ *Config, info IdPInfo) { app.info = info }),
	))

	if server.IdP() == nil {
		t.Fatal("expected a running identity provider")
	}
	if !strings.HasPrefix(server.IdPInfo().Issuer, "http://127.0.0.1:") {
		t.Fatalf("issuer = %q", server.IdPInfo().Issuer)
	}
	if server.IdPInfo().ClientSecret == "" {
		t.Fatal("expected generated client credentials")
	}

	if body := get(t, server.URL+"/login"); body != "signed in as admin <admin@example.com>" {
		t.Fatalf("body = %q", body)
	}

	server.LoginAs(t, "guest")
	if body := get(t, server.URL+"/login"); body != "signed in as guest <guest@example.com>" {
		t.Fatalf("body = %q", body)
	}
}

func TestWithIdentityProviderRejectsAnUnknownLoginUser(t *testing.T) {
	failures := &recordingT{TestingT: t}
	TestRun(failures, http.NotFoundHandler(), disablePublicAssets, WithIdentityProvider(
		WithIdPRoster(testRoster),
		WithLoginUser("nobody"),
	))
	if !strings.Contains(failures.failure, "unknown user") {
		t.Fatalf("failure = %q", failures.failure)
	}
}

func TestWithIdentityProviderNeedsExactlyOneRosterSource(t *testing.T) {
	failures := &recordingT{TestingT: t}
	TestRun(failures, http.NotFoundHandler(), disablePublicAssets, WithIdentityProvider(WithLoginUser("admin")))
	if !strings.Contains(failures.failure, "exactly one of WithIdPConfig") {
		t.Fatalf("failure = %q", failures.failure)
	}
}

// disablePublicAssets keeps these tests focused on the login flow; the
// fixture application registers no public filesystem.
func disablePublicAssets(config *Config) {
	config.Update(func(value *pw.ServerConfig) { value.Public.Enabled = false })
}

func decodeJSONString(raw json.RawMessage, into *string) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, into)
}

func get(t *testing.T, target string) string {
	t.Helper()
	response, err := http.Get(target) //nolint:noctx // the test server is loopback
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %s", target, response.StatusCode, strings.TrimSpace(string(body)))
	}
	return string(body)
}
