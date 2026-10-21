package authfaste2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/shibukawa/popcornweb/contrib/devidp"
	"github.com/shibukawa/popcornweb/internal/pwmigrate"
	"github.com/shibukawa/popcornweb/plugin/auth"
	"github.com/shibukawa/popcornweb/plugin/auth/authfast"
	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/pwfast"
	"github.com/shibukawa/popcornweb/sessionstore"
	"github.com/shibukawa/tinybind-go/configbind"
	"github.com/shibukawa/tinygodriver/fasthttp"

	_ "github.com/shibukawa/popcornweb/sessionstore/sqlite"

	// Storage is opt-in by blank import: the sessions, the single-use ceremony
	// records, and the driver the DSN names.
	_ "github.com/shibukawa/popcornweb/authstate/sqlite"
	_ "github.com/shibukawa/tinygodriver/database/sql/sqlite"
)

// deployment is the one application this test binary may build. Both listeners
// serve it, over one authentication runtime.
type deployment struct {
	// fast is the origin of the fasthttp listener, and the one the OIDC
	// redirect URL and the passkey origin allowlist name. The login round trip
	// therefore completes on the transport under test.
	fast string
	// slow is the origin of the net/http listener, kept so a test can ask both
	// the same question. It is a declared origin too, so its own endpoints pass
	// the same-origin check.
	slow string
}

var (
	once    sync.Once
	shared  *deployment
	buildEr error
)

func start(t *testing.T) *deployment {
	t.Helper()
	once.Do(func() { shared, buildEr = build() })
	if buildEr != nil {
		t.Fatalf("deployment: %v", buildEr)
	}
	return shared
}

func build() (*deployment, error) {
	directory, err := os.MkdirTemp("", "authfaste2e")
	if err != nil {
		return nil, err
	}
	provider, err := devidp.Start(context.Background(), "127.0.0.1:0", devidp.Config{
		Users: []devidp.User{{
			Key: "alice", Subject: "alice-subject", DisplayName: "Alice Example",
			Claims: map[string]any{"email": "alice@example.com"},
		}},
	}, devidp.Options{LoginUser: "alice-subject"})
	if err != nil {
		return nil, fmt.Errorf("identity provider: %w", err)
	}

	// The fasthttp listener is bound before the configuration is written,
	// because the configuration has to name its address: an OIDC redirect URL
	// is a whole URL, and the browser has to arrive back on this transport for
	// the callback to be the one under test.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	// An RP ID is a domain, so the deployment reaches its own listener by name.
	// The 127.0.0.1 the listener reports could never be one.
	fastOrigin := "http://localhost:" + port(listener.Addr())

	var slowHandler http.Handler
	var handlerMu sync.RWMutex
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerMu.RLock()
		current := slowHandler
		handlerMu.RUnlock()
		if current == nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		current.ServeHTTP(w, r)
	}))
	slowOrigin := strings.Replace(slow.URL, "127.0.0.1", "localhost", 1)

	database := filepath.ToSlash(filepath.Join(directory, "auth.db"))
	if err := applyFrameworkMigrations(directory, database); err != nil {
		return nil, err
	}
	credentials, err := provider.RegisterClient(devidp.ClientSpec{LoopbackRedirects: true})
	if err != nil {
		return nil, err
	}
	configPath, err := writeConfig(directory, provider.Issuer(), fastOrigin, slowOrigin, database, credentials)
	if err != nil {
		return nil, err
	}
	pw.SetConfigLoadOptions(configbind.LoadOptions{
		Vendor:             "popcornweb-authfast-e2e",
		Tool:               "authfast-e2e",
		ExplicitConfigPath: configPath,
		Args:               []string{},
		// The fixture serves plain http on a loopback address, which is the
		// development exception the cookie policy allows. It has to say so: an
		// unset APP_ENV no longer buys the development relaxations.
		Environ: []string{"APP_ENV=dev"},
	})

	// Framework startup. It parses the configuration, opens the database, and
	// builds the session manager, none of which is transport-shaped, and it
	// installs the authentication runtime both chains then serve.
	built, err := pw.Middlewares(netApplication())
	if err != nil {
		return nil, fmt.Errorf("framework initialization: %w", err)
	}
	handlerMu.Lock()
	slowHandler = built
	handlerMu.Unlock()

	fastHandler, err := pwfast.Middlewares(fastApplication(),
		authfast.Installed().Apply(pwfast.RuntimeOptions{Session: pw.SessionManager()}))
	if err != nil {
		return nil, fmt.Errorf("fasthttp chain: %w", err)
	}
	go func() { _ = fasthttp.Serve(listener, fastHandler) }()

	return &deployment{fast: fastOrigin, slow: slowOrigin}, nil
}

func port(address net.Addr) string {
	_, value, err := net.SplitHostPort(address.String())
	if err != nil {
		return ""
	}
	return value
}

// netApplication and fastApplication answer the same two routes, so a test can
// put the same question to both listeners and compare what came back.
func netApplication() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /whoami", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, whoami(pw.RequestAuthentication(r).Authenticated,
			pw.RequestAuthentication(r).Method, r.Context()))
	})
	mux.HandleFunc("GET /private", func(w http.ResponseWriter, r *http.Request) {
		user, _ := auth.User(r.Context())
		_, _ = fmt.Fprintf(w, "private:%s", user.AccountID)
	})
	return mux
}

func fastApplication() fasthttp.RequestHandler {
	mux := pwfast.NewServeMux()
	mux.HandleFunc("GET /whoami", func(r *fasthttp.RequestCtx) {
		_, _ = r.WriteString(whoami(pwfast.RequestAuthentication(r).Authenticated,
			pwfast.RequestAuthentication(r).Method, r))
	})
	mux.HandleFunc("GET /private", func(r *fasthttp.RequestCtx) {
		user, _ := auth.User(r)
		_, _ = r.WriteString("private:" + user.AccountID)
	})
	return mux.Handler
}

// whoami is the body both applications answer with, so a difference between the
// transports is a difference in what the framework recorded rather than in what
// two handlers chose to print.
func whoami(authenticated bool, method string, ctx context.Context) string {
	if !authenticated {
		return "anonymous"
	}
	user, _ := auth.User(ctx)
	return method + ":" + user.AccountID
}

// browser drives one listener the way a browser does: a cookie jar, an Origin
// header on every state-changing request, and redirects followed.
type browser struct {
	t      *testing.T
	client *http.Client
	base   string
}

// newBrowser returns an anonymous browser against the fasthttp listener, which
// is the transport under test.
func newBrowser(t *testing.T) *browser {
	t.Helper()
	return browserAt(t, start(t).fast)
}

func browserAt(t *testing.T, base string) *browser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &browser{t: t, client: &http.Client{Jar: jar}, base: base}
}

// login completes the whole OIDC round trip: this listener, the provider, and
// back. The fixture's provider signs the one user in without a form, so what
// this exercises is every hop except the click.
// The redirects are followed whatever the browser was told about them, because
// this is a precondition rather than a subject: a test whose point is one
// redirect still has to be able to sign in first.
func (b *browser) login() {
	b.t.Helper()
	stop := b.client.CheckRedirect
	b.client.CheckRedirect = nil
	defer func() { b.client.CheckRedirect = stop }()

	response, err := b.client.Get(b.base + "/auth/login")
	if err != nil {
		b.t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		b.t.Fatalf("login landed on %d", response.StatusCode)
	}
}

// navigationAccept is what a browser sends when it opens a page. The guard sends
// a request to the login only when it asks for a page, so a browser that
// navigates has to say so the way a real one does.
const navigationAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

// get opens a page: the request a browser makes when the address changes.
func (b *browser) get(path string) (*http.Response, []byte) {
	b.t.Helper()
	request, err := http.NewRequest(http.MethodGet, b.base+path, nil)
	if err != nil {
		b.t.Fatal(err)
	}
	request.Header.Set("Accept", navigationAccept)
	return b.do(request)
}

// fetch asks for something that is not a page, with the cookies the browser
// holds. accept and dest are what that kind of request names — an icon names
// image types and a destination of image — and an empty value sends no header,
// which is also what a tool that never said what it wanted looks like.
func (b *browser) fetch(path, accept, dest string) (*http.Response, []byte) {
	b.t.Helper()
	request, err := http.NewRequest(http.MethodGet, b.base+path, nil)
	if err != nil {
		b.t.Fatal(err)
	}
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	if dest != "" {
		request.Header.Set("Sec-Fetch-Dest", dest)
	}
	return b.do(request)
}

// reconfirmPending reports whether the browser holds the mark a reconfirm
// logout leaves behind. The mark is scoped to the login path, so it is asked of
// the jar for that path and no other.
func (b *browser) reconfirmPending() bool {
	b.t.Helper()
	target, err := url.Parse(b.base + "/auth/login")
	if err != nil {
		b.t.Fatal(err)
	}
	for _, cookie := range b.client.Jar.Cookies(target) {
		if strings.HasSuffix(cookie.Name, "_reconfirm") {
			return true
		}
	}
	return false
}

var (
	pickerAction = regexp.MustCompile(`<form method="post" action="([^"]*)">`)
	pickerAuth   = regexp.MustCompile(`name="auth" value="([^"]*)"`)
	pickerCSRF   = regexp.MustCompile(`name="csrf" value="([^"]*)"`)
)

// chooseAccount answers the provider's account picker the way a person does, by
// pressing the button of one of the accounts it lists. page is the response the
// picker arrived in, which says where its form is submitted from; the redirects
// that follow end back on this listener, signed in.
func (b *browser) chooseAccount(page *http.Response, body []byte, subject string) (*http.Response, []byte) {
	b.t.Helper()
	action, auth, csrf := pickerAction.FindSubmatch(body), pickerAuth.FindSubmatch(body), pickerCSRF.FindSubmatch(body)
	if action == nil || auth == nil || csrf == nil {
		b.t.Fatalf("the provider did not show an account picker: %.300q", body)
	}
	target, err := page.Request.URL.Parse(html.UnescapeString(string(action[1])))
	if err != nil {
		b.t.Fatal(err)
	}
	form := url.Values{
		"auth":    {html.UnescapeString(string(auth[1]))},
		"csrf":    {html.UnescapeString(string(csrf[1]))},
		"subject": {subject},
	}
	request, err := http.NewRequest(http.MethodPost, target.String(), strings.NewReader(form.Encode()))
	if err != nil {
		b.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return b.do(request)
}

func (b *browser) post(path string, body any) (*http.Response, []byte) {
	b.t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		if encoded, err = json.Marshal(body); err != nil {
			b.t.Fatal(err)
		}
	}
	request, err := http.NewRequest(http.MethodPost, b.base+path, bytes.NewReader(encoded))
	if err != nil {
		b.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	// A browser sets this on every state-changing fetch, and the endpoint
	// refuses the request without a matching one.
	request.Header.Set("Origin", b.base)
	return b.do(request)
}

// postForm submits the shape an ordinary logout button sends.
func (b *browser) postForm(path, body string) (*http.Response, []byte) {
	b.t.Helper()
	request, err := http.NewRequest(http.MethodPost, b.base+path, strings.NewReader(body))
	if err != nil {
		b.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", b.base)
	return b.do(request)
}

func (b *browser) do(request *http.Request) (*http.Response, []byte) {
	b.t.Helper()
	response, err := b.client.Do(request)
	if err != nil {
		b.t.Fatal(err)
	}
	defer response.Body.Close()
	payload := new(bytes.Buffer)
	if _, err := payload.ReadFrom(response.Body); err != nil {
		b.t.Fatal(err)
	}
	return response, payload.Bytes()
}

func (b *browser) postJSON(path string, body any, target any) {
	b.t.Helper()
	response, payload := b.post(path, body)
	if response.StatusCode != http.StatusOK {
		b.t.Fatalf("POST %s status = %d, want 200; body %q", path, response.StatusCode, payload)
	}
	if target == nil {
		return
	}
	if err := json.Unmarshal(payload, target); err != nil {
		b.t.Fatalf("POST %s returned %q: %v", path, payload, err)
	}
}

func (b *browser) whoami() string {
	b.t.Helper()
	_, payload := b.get("/whoami")
	return string(payload)
}

// noRedirect stops the client at the first response, for a test whose subject
// is the redirect itself.
func (b *browser) noRedirect() *browser {
	b.client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return b
}

func applyFrameworkMigrations(directory, database string) error {
	migrations := filepath.Join(directory, "migrations")
	if err := os.MkdirAll(migrations, 0o750); err != nil {
		return err
	}
	sessionMigration, err := sessionstore.MigrationSQL("sqlite", "popcornweb_session")
	if err != nil {
		return err
	}
	authMigration, err := auth.MigrationSQL("sqlite")
	if err != nil {
		return err
	}
	for version, migration := range map[int]struct{ name, content string }{
		1: {sessionstore.MigrationName, sessionMigration},
		2: {auth.MigrationName, authMigration},
	} {
		name := fmt.Sprintf("%05d_%s.sql", version, migration.name)
		if err := os.WriteFile(filepath.Join(migrations, name), []byte(migration.content), 0o600); err != nil {
			return err
		}
	}
	sources, err := pwmigrate.Sources(migrations)
	if err != nil {
		return err
	}
	target, err := pwmigrate.Open("sqlite://" + database)
	if err != nil {
		return err
	}
	defer target.Close()
	_, err = pwmigrate.Apply(context.Background(), target, sources, pwmigrate.ActionUp, 0)
	return err
}

func writeConfig(directory, issuer, fastOrigin, slowOrigin, database string, credentials devidp.Credentials) (string, error) {
	content := fmt.Sprintf(`
[server]
public.enabled = false

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
keyring.secret = "2wcqk/sZ2troHw/eW31LrA8RKvZgUxUy6kgZ5ISdXGU="

[auth]
enabled = true
session.ttl = "1h"
session.idle_timeout = "30m"
mode = "oidc_passkey"
post_login_path = "/whoami"
recent_auth_max_age = "5m"
recovery.policy = "oidc"
# The icons a browser asks for unprompted are protected too, as they are in any
# deployment that protects the whole site, so the guard meets them.
protection.include = ["/private", "/favicon.ico", "/apple-touch-icon.png", "/apple-touch-icon-precomposed.png"]
protection.unauthenticated = "redirect"

[auth.assurance.presence]
enabled = true
absent_after = "10m"

[auth.passkey]
rp_id = "localhost"
rp_name = "Auth Fast End To End"
# Both listeners are declared, so each one's own endpoints pass the
# same-origin check while only the first is where a login lands.
origins = ["%s", "%s"]
user_verification = "required"
discoverable = "preferred"

[auth.oidc]
issuer = "%s"
client_id = "%s"
client_secret = "%s"
redirect_url = "%s/auth/callback"
admission = "authenticated"
allow_loopback_http = true
`, database, fastOrigin, slowOrigin, issuer, credentials.ID, credentials.Secret, fastOrigin)
	path := filepath.Join(directory, "config.toml")
	return path, os.WriteFile(path, []byte(content), 0o600)
}
