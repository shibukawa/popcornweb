package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/shibukawa/popcornweb/authstate/sqlite"
	"github.com/shibukawa/popcornweb/internal/pwmigrate"
	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/sessionstore"
	_ "github.com/shibukawa/popcornweb/sessionstore/sqlite"
	httpbind "github.com/shibukawa/tinybind-go"
	"github.com/shibukawa/tinybind-go/configbind"
)

// identityProvider is a minimal OpenID Provider fixture. Its authorization
// endpoint logs the caller in immediately, which is also how contrib/devidp
// behaves once a developer picks a user.
type identityProvider struct {
	server   *httptest.Server
	key      *rsa.PrivateKey
	clientID string

	mu     sync.Mutex
	nonce  string
	claims map[string]any
}

func newIdentityProvider(t *testing.T, clientID string) *identityProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	provider := &identityProvider{
		key:      key,
		clientID: clientID,
		claims: map[string]any{
			"sub":             "user",
			"name":            "Example User",
			"email":           "user@example.com",
			"roles":           []string{"staff"},
			"employee_number": "E-10231",
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		issuer := provider.issuer()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(
			`{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q,"userinfo_endpoint":%q}`,
			issuer, issuer+"/authorize", issuer+"/token", issuer+"/keys", issuer+"/userinfo"))
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, jwksJSON(provider.key))
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
			http.Error(w, "pkce required", http.StatusBadRequest)
			return
		}
		provider.mu.Lock()
		provider.nonce = query.Get("nonce")
		provider.mu.Unlock()
		redirect, err := url.Parse(query.Get("redirect_uri"))
		if err != nil {
			http.Error(w, "bad redirect_uri", http.StatusBadRequest)
			return
		}
		values := redirect.Query()
		values.Set("code", "authorization-code")
		values.Set("state", query.Get("state"))
		redirect.RawQuery = values.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusSeeOther)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		provider.mu.Lock()
		nonce, claims := provider.nonce, provider.claims
		provider.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(
			`{"access_token":"access","token_type":"Bearer","id_token":%q}`,
			provider.signIDToken(t, claims, nonce)))
	})
	provider.server = httptest.NewServer(mux)
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *identityProvider) issuer() string { return p.server.URL }

func (p *identityProvider) setClaims(claims map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.claims = claims
}

func (p *identityProvider) signIDToken(t *testing.T, extra map[string]any, nonce string) string {
	t.Helper()
	now := time.Now()
	claims := map[string]any{
		"iss":   p.issuer(),
		"aud":   p.clientID,
		"exp":   now.Add(time.Hour).Unix(),
		"iat":   now.Unix(),
		"nonce": nonce,
	}
	for key, value := range extra {
		claims[key] = value
	}
	encode := base64.RawURLEncoding.EncodeToString
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "key-1"})
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := encode(header) + "." + encode(body)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + encode(signature)
}

func jwksJSON(key *rsa.PrivateKey) string {
	encode := base64.RawURLEncoding.EncodeToString
	return `{"keys":[{"kty":"RSA","kid":"key-1","use":"sig","alg":"RS256","n":"` +
		encode(key.N.Bytes()) + `","e":"` + encode(big.NewInt(int64(key.PublicKey.E)).Bytes()) + `"}]}`
}

// application is the sample-shaped handler used by the end-to-end test.
func application() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		user, ok := User(r.Context())
		if !ok {
			_, _ = io.WriteString(w, "anonymous")
			return
		}
		_, _ = io.WriteString(w, "home:"+user.DisplayName)
	})
	mux.HandleFunc("GET /mypage", func(w http.ResponseWriter, r *http.Request) {
		user, _ := User(r.Context())
		authentication := pw.RequestAuthentication(r)
		_, _ = io.WriteString(w, fmt.Sprintf("mypage:%s:%s:%s:%s",
			user.Subject, user.Email, authentication.Method, authentication.Subject))
	})
	return mux
}

// TestOIDCLoginEndToEnd drives one browser-shaped login against a fixture
// provider. Configuration is parsed once per process, so this package keeps a
// single framework-level test.
func TestOIDCLoginEndToEnd(t *testing.T) {
	provider := newIdentityProvider(t, "example-client")

	// The application URL is needed before its handler exists, because the
	// redirect URL is configuration.
	var handler http.Handler
	var handlerMu sync.RWMutex
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerMu.RLock()
		current := handler
		handlerMu.RUnlock()
		if current == nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		current.ServeHTTP(w, r)
	}))
	defer app.Close()

	database := filepath.ToSlash(filepath.Join(t.TempDir(), "auth-test.db"))
	applyFrameworkMigrations(t, database)
	configPath := writeConfig(t, provider.issuer(), app.URL, database)
	pw.SetConfigLoadOptions(configbind.LoadOptions{
		Vendor:             "popcornweb-auth-test",
		Tool:               "auth-test",
		ExplicitConfigPath: configPath,
		Args:               []string{},
		// The fixture serves plain http on a loopback address, which is the
		// development exception the cookie policy allows. It has to say so:
		// an unset APP_ENV no longer buys the development relaxations.
		Environ: []string{"APP_ENV=dev"},
	})
	built, err := pw.Middlewares(application())
	if err != nil {
		t.Fatalf("framework initialization: %v", err)
	}
	handlerMu.Lock()
	handler = built
	handlerMu.Unlock()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	// A browser names a page when it asks for one, and the guard sends only a
	// request that does so to the login.
	client := &http.Client{Jar: jar, Transport: navigating{http.DefaultTransport}}

	// An anonymous request to a protected path is redirected into the login
	// flow, which completes at the originally requested page.
	response, err := client.Get(app.URL + "/mypage")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %q", response.StatusCode, body)
	}
	if response.Request.URL.Path != "/mypage" {
		t.Fatalf("landed on %q, want /mypage", response.Request.URL.Path)
	}
	if !strings.HasPrefix(body, "mypage:user:user@example.com:oidc:") {
		t.Fatalf("authenticated page = %q", body)
	}

	sessionCookie := findCookie(t, jar, app.URL, "pw_session")
	if sessionCookie == nil {
		t.Fatal("no session cookie was issued")
	}
	// The jar keeps only name and value, which is exactly what the browser
	// sends back: an opaque token, never account data.
	if len(sessionCookie.Value) != 43 || strings.Contains(sessionCookie.Value, "user") {
		t.Fatalf("session cookie value = %q", sessionCookie.Value)
	}
	// The correlation cookie is single use and must not survive the callback.
	if txn := findCookie(t, jar, app.URL+"/auth/callback", "pw_session_txn"); txn != nil {
		t.Fatalf("transaction cookie survived the callback: %#v", txn)
	}

	// The session is now readable on a public page too.
	response, err = client.Get(app.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if body := readBody(t, response); body != "home:Example User" {
		t.Fatalf("home page = %q", body)
	}

	// The OpenAPI document is mounted beneath the extension chain rather than
	// beside the probes, so the guard reaches it exactly as it reaches an
	// application route. An anonymous request is sent into the login flow
	// instead of being answered with a map of the whole surface.
	anonymous := &http.Client{Transport: navigating{http.DefaultTransport},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	document, err := anonymous.Get(app.URL + "/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, document)
	if document.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous openapi status = %d, want a redirect into the login flow", document.StatusCode)
	}
	if location := document.Header.Get("Location"); !strings.HasPrefix(location, "/auth/login") {
		t.Fatalf("anonymous openapi redirected to %q, want the login path", location)
	}

	// The session that the protected page created reads it. Assembly needs at
	// least one fragment, which generated code contributes in a real project.
	httpbind.RegisterOpenAPIFragment("auth-test", []byte(`{"openapi":"3.1.0","paths":{"/mypage":{}}}`))
	document, err = client.Get(app.URL + "/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(t, document)
	if document.StatusCode != http.StatusOK {
		t.Fatalf("authenticated openapi status = %d body = %q", document.StatusCode, body)
	}
	if !strings.Contains(body, "/mypage") {
		t.Fatalf("openapi document = %q", body)
	}

	// A callback replayed with the consumed correlation cookie is rejected.
	replay, err := client.Get(app.URL + "/auth/callback?code=authorization-code&state=stale")
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, replay)
	if replay.StatusCode != http.StatusBadRequest {
		t.Fatalf("replayed callback status = %d", replay.StatusCode)
	}

	// Logout requires POST and a same-origin submission.
	getLogout, err := client.Get(app.URL + "/auth/logout")
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, getLogout)
	if getLogout.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET logout status = %d", getLogout.StatusCode)
	}
	crossOrigin, err := postLogout(client, app.URL, "https://evil.example")
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, crossOrigin)
	if crossOrigin.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin logout status = %d", crossOrigin.StatusCode)
	}
	loggedOut, err := postLogout(client, app.URL, app.URL)
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, loggedOut)
	if loggedOut.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d", loggedOut.StatusCode)
	}
	if cookie := findCookie(t, jar, app.URL, "pw_session"); cookie != nil {
		t.Fatalf("session cookie survived logout: %#v", cookie)
	}

	// The logout left the mark that makes the next sign-in ask again, and
	// nothing short of a completed sign-in may spend it: not a browser fetching
	// the icon it asks for unprompted, which the guard used to send to the login
	// with the mark's cookie in hand, and not a login page opened and left.
	if findCookie(t, jar, app.URL+"/auth/login", "pw_session_reconfirm") == nil {
		t.Fatal("logout left no reconfirm mark")
	}
	stray := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	icon, err := stray.Get(app.URL + "/apple-touch-icon.png")
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, icon)
	if icon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an icon fetch to a protected path answered %d, want 401", icon.StatusCode)
	}
	if location := icon.Header.Get("Location"); location != "" {
		t.Fatalf("an icon fetch was sent to %q", location)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		begin, err := stray.Get(app.URL + "/auth/login")
		if err != nil {
			t.Fatal(err)
		}
		readBody(t, begin)
		if begin.StatusCode != http.StatusSeeOther {
			t.Fatalf("login start %d answered %d, want the provider redirect", attempt, begin.StatusCode)
		}
		authorization, err := url.Parse(begin.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if prompt := authorization.Query().Get("prompt"); prompt != "select_account login" {
			t.Fatalf("authorization request %d carried prompt %q, want the account picker and a login", attempt, prompt)
		}
		if findCookie(t, jar, app.URL+"/auth/login", "pw_session_reconfirm") == nil {
			t.Fatalf("authorization request %d spent the reconfirm mark before any sign-in completed", attempt)
		}
	}

	// After logout the protected page is anonymous again, and a login that
	// the admission rule rejects does not create a session.
	provider.setClaims(map[string]any{"sub": "other", "name": "Outsider", "roles": []string{"guest"},
		"employee_number": "E-99999"})
	denied, err := client.Get(app.URL + "/mypage")
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, denied)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("denied login status = %d", denied.StatusCode)
	}
	if cookie := findCookie(t, jar, app.URL, "pw_session"); cookie != nil {
		t.Fatal("a denied login created a session")
	}
	// A sign-in the admission rule refused proved nobody, so the mark outlives it.
	if findCookie(t, jar, app.URL+"/auth/login", "pw_session_reconfirm") == nil {
		t.Fatal("a refused sign-in spent the reconfirm mark")
	}

	// A token that no longer carries the configured identity claim cannot
	// identify an account, so the login is refused instead of falling back to
	// the subject and creating a second account for the same person.
	provider.setClaims(map[string]any{"sub": "user", "name": "Example User", "roles": []string{"staff"}})
	withoutKey, err := client.Get(app.URL + "/mypage")
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, withoutKey)
	if withoutKey.StatusCode != http.StatusForbidden {
		t.Fatalf("login without the identity claim = %d", withoutKey.StatusCode)
	}

	// The sign-in that completes ends the mark, because the intent was that one
	// sign-in ask, and this is it.
	provider.setClaims(map[string]any{"sub": "user", "name": "Example User", "email": "user@example.com",
		"roles": []string{"staff"}, "employee_number": "E-10231"})
	completed, err := client.Get(app.URL + "/mypage")
	if err != nil {
		t.Fatal(err)
	}
	if body := readBody(t, completed); completed.StatusCode != http.StatusOK || !strings.HasPrefix(body, "mypage:user:") {
		t.Fatalf("completing sign-in = %d %q", completed.StatusCode, body)
	}
	if findCookie(t, jar, app.URL+"/auth/login", "pw_session_reconfirm") != nil {
		t.Fatal("a completed sign-in left the reconfirm mark")
	}
}

// navigating makes a request look like the one a browser sends to open a page:
// it names text/html unless the request already named something. The guard
// sends a request to the login only when it asks for a page, and a bare Go
// client asks for nothing.
type navigating struct{ next http.RoundTripper }

func (n navigating) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Header.Get("Accept") == "" {
		request = request.Clone(request.Context())
		request.Header.Set("Accept", navigationAccept)
	}
	return n.next.RoundTrip(request)
}

func postLogout(client *http.Client, appURL, origin string) (*http.Response, error) {
	request, err := http.NewRequest(http.MethodPost, appURL+"/auth/logout", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Origin", origin)
	return client.Do(request)
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func findCookie(t *testing.T, jar *cookiejar.Jar, rawURL, name string) *http.Cookie {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range jar.Cookies(parsed) {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

// applyFrameworkMigrations runs the migration files the framework publishes,
// through the same goose runner a project uses, because startup now verifies
// those tables instead of creating them.
func applyFrameworkMigrations(t *testing.T, database string) {
	t.Helper()
	directory := t.TempDir()
	// The versions here are this fixture's, not the packages': a project picks
	// whatever is free when the file is written.
	for version, migration := range map[int]struct{ name, content string }{
		1: {sessionstore.MigrationName, mustSessionMigration()},
		2: {MigrationName, mustAuthMigration()},
	} {
		name := fmt.Sprintf("%05d_%s.sql", version, migration.name)
		if err := os.WriteFile(filepath.Join(directory, name), []byte(migration.content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := pwmigrate.Sources(directory)
	if err != nil {
		t.Fatal(err)
	}
	target, err := pwmigrate.Open("sqlite://" + database)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if _, err := pwmigrate.Apply(t.Context(), target, sources, pwmigrate.ActionUp, 0); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, issuer, appURL, database string) string {
	t.Helper()
	directory := t.TempDir()
	content := fmt.Sprintf(`
[server]
public.enabled = false
openapi = "/openapi.json"

[middleware.rdb]
enabled = true

[[middleware.rdb.connections]]
group = "default"
dsn = "sqlite://%s"
connect_timeout = "5s"
max_open_conns = 1
max_idle_conns = 1

[session]
enabled = true
backend = "rdb"
cookie.name = "pw_session"
cookie.secure = false
keyring.secret = "3Sz80mOuKVzj3ZqyFelHvyAi6GoS27IKuMSrhDOrfRQ="

[auth]
enabled = true
session.ttl = "1h"
session.idle_timeout = "30m"
mode = "oidc_only"
post_login_path = "/"
protection.include = ["/mypage", "/openapi.json", "/apple-touch-icon.png"]

[auth.oidc]
issuer = "%s"
client_id = "example-client"
client_secret = "example-secret"
redirect_url = "%s/auth/callback"
scopes = ["profile", "email"]
identity_claim = "employee_number"
admission = "claim"
claim.path = "/roles"
claim.values = ["staff"]
claim.match = "any"
allow_loopback_http = true
`, database, issuer, appURL)
	path := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// mustSessionMigration and mustAuthMigration are the SQLite migrations the
// scaffold writes, which is the dialect these fixtures use.
func mustSessionMigration() string {
	migration, err := sessionstore.MigrationSQL("sqlite", "popcornweb_session")
	if err != nil {
		panic(err)
	}
	return migration
}

func mustAuthMigration() string {
	migration, err := MigrationSQL("sqlite")
	if err != nil {
		panic(err)
	}
	return migration
}
