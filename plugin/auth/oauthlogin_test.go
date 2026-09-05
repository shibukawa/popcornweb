package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/authstate/memory"
	"github.com/shibukawa/popcornweb/contrib/oauth"
	"github.com/shibukawa/popcornweb/contrib/oauthprofile"
	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/tinybind-go/configbind"
)

// oauthProviderFixture stands in for X. The real endpoints are constants of the
// build, so the definition rather than the configuration is what a test
// replaces, and everything it exercises above that point is the shipped code.
type oauthProviderFixture struct {
	server *httptest.Server

	mu sync.Mutex
	// profile is the body /me answers with, so one test can make the provider
	// disagree with itself.
	profile string
	// profileStatus lets a test make the account endpoint fail after a
	// successful token exchange, which is the failure mode this login has and
	// the OIDC one does not.
	profileStatus int
	// challenge is the PKCE challenge the authorization request carried.
	challenge  string
	tokenCalls int
}

func newOAuthProviderFixture(t *testing.T) *oauthProviderFixture {
	t.Helper()
	fixture := &oauthProviderFixture{
		profile:       `{"data":{"id":"2244994945","name":"X Dev","username":"XDevelopers","profile_image_url":"https://pbs.example/a.jpg"}}`,
		profileStatus: http.StatusOK,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
			http.Error(w, "pkce required", http.StatusBadRequest)
			return
		}
		fixture.mu.Lock()
		fixture.challenge = query.Get("code_challenge")
		fixture.mu.Unlock()
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
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		fixture.tokenCalls++
		fixture.mu.Unlock()
		if id, secret, ok := r.BasicAuth(); !ok || id != "client" || secret != "secret" {
			http.Error(w, "client authentication required", http.StatusUnauthorized)
			return
		}
		if r.PostFormValue("code_verifier") == "" {
			http.Error(w, "pkce required", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// X answers with a lowercase token type, which is what the exchange has
		// to accept.
		_, _ = io.WriteString(w, `{"access_token":"provider-access-token","token_type":"bearer","scope":"users.read tweet.read"}`)
	})
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer provider-access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fixture.mu.Lock()
		body, status := fixture.profile, fixture.profileStatus
		fixture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
	fixture.server = httptest.NewServer(mux)
	t.Cleanup(fixture.server.Close)
	return fixture
}

// provider is the fixture as a provider definition, spelled exactly as the X
// one is so that the mapping under test is the shipped mapping.
func (f *oauthProviderFixture) provider() oauthprofile.Provider {
	return oauthprofile.Provider{
		Name:                  "x",
		Issuer:                "https://x.com",
		AuthorizationEndpoint: f.server.URL + "/authorize",
		TokenEndpoint:         f.server.URL + "/token",
		ProfileEndpoint:       f.server.URL + "/me",
		Scopes:                []string{"users.read", "tweet.read"},
		AuthMethod:            oauth.AuthBasic,
		ProfileRoot:           "/data",
		Profile: map[string]string{
			"sub": "id", "preferred_username": "username",
			"name": "name", "picture": "profile_image_url",
		},
	}
}

func (f *oauthProviderFixture) setProfile(body string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profile, f.profileStatus = body, status
}

// oauthApplication builds the runtime and the server one OAuth login test
// drives. It returns the application server and the runtime, so a test can read
// what the login stored.
func oauthApplication(t *testing.T, fixture *oauthProviderFixture, tune func(*Config)) (*httptest.Server, *runtime) {
	t.Helper()
	registry := session.NewRegistry()
	if err := registry.Register[SessionData](sessionSlotKey, session.Private, nil); err != nil {
		t.Fatal(err)
	}
	keys, err := session.ParseKeyring("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := session.NewManager(registry, nil, session.Options{
		TTL:    time.Hour,
		Cookie: session.CookieOptions{Name: "pw_session", Path: "/"},
		Keys:   keys,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := memory.NewStore[oauth.Transaction](memory.Options{})
	if err != nil {
		t.Fatal(err)
	}
	config := baseConfig(ModeOAuthOnly)
	// The fixture serves loopback http, which is the same allowance a developer
	// running against the real provider from localhost uses.
	config.OAuth.AllowLoopbackHTTP = true
	config.OAuth.RedirectURL = ""
	if tune != nil {
		tune(&config)
	}
	instance := &runtime{
		config:        config,
		manager:       manager,
		stateStore:    store,
		oauthProvider: fixture.provider(),
		accounts:      newAccountGate(),
		stopPruning:   make(chan struct{}),
	}
	replaceRuntime(instance)
	t.Cleanup(func() { replaceRuntime(nil) })

	landing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := Session(r.Context())
		if !ok {
			_, _ = io.WriteString(w, "anonymous")
			return
		}
		_, _ = fmt.Fprintf(w, "signed-in:%s:%s:%s:%s:%s:%s",
			data.Provider, data.Subject, data.Username, data.DisplayName, data.AvatarURL, data.Method)
	})
	server := httptest.NewServer(manager.Middleware(nil)(httpFrame(instance.serve)(landing)))
	t.Cleanup(server.Close)
	return server, instance
}

func oauthBrowser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func readOAuthBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestOAuthLoginCompletesASession drives the whole flow: authorization with
// PKCE, the token exchange, the account request that stands in for an ID Token,
// and the session it lands in.
func TestOAuthLoginCompletesASession(t *testing.T) {
	fixture := newOAuthProviderFixture(t)
	app, _ := oauthApplication(t, fixture, nil)
	client := oauthBrowser(t)

	response, err := client.Get(app.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	body := readOAuthBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %q", response.StatusCode, body)
	}
	// The account is the numeric id, the handle is stored beside it for
	// display, and the method says how the identity was proved.
	if body != "signed-in:x:2244994945:XDevelopers:X Dev:https://pbs.example/a.jpg:oauth" {
		t.Fatalf("landing page = %q", body)
	}
	if response.Request.URL.Path != "/" {
		t.Fatalf("landed on %q, want the post-login path", response.Request.URL.Path)
	}

	fixture.mu.Lock()
	challenge, calls := fixture.challenge, fixture.tokenCalls
	fixture.mu.Unlock()
	if challenge == "" {
		t.Error("the authorization request carried no PKCE challenge")
	}
	if calls != 1 {
		t.Errorf("token endpoint called %d times, want once", calls)
	}

	// The correlation cookie is single use and must not survive the callback.
	callbackURL, _ := url.Parse(app.URL + "/auth/callback")
	for _, cookie := range client.Jar.Cookies(callbackURL) {
		if cookie.Name == "pw_session_txn" && cookie.Value != "" {
			t.Fatalf("transaction cookie survived the callback: %#v", cookie)
		}
	}
	// Nothing the provider issued is in the session. The access token answered
	// one question and was dropped.
	sessionURL, _ := url.Parse(app.URL + "/")
	for _, cookie := range client.Jar.Cookies(sessionURL) {
		if strings.Contains(cookie.Value, "provider-access-token") {
			t.Fatalf("the access token reached the browser: %#v", cookie)
		}
	}
}

// TestOAuthLoginAnswersAnUnreachableAccountEndpointAsAnOutage separates the two
// things a failure after the token exchange can mean. The provider vouched for
// the token and then could not answer for it, which is an outage; answering 403
// would tell somebody entitled to enter that they are not.
func TestOAuthLoginAnswersAnUnreachableAccountEndpointAsAnOutage(t *testing.T) {
	fixture := newOAuthProviderFixture(t)
	fixture.setProfile(`{"errors":[{"message":"over capacity"}]}`, http.StatusServiceUnavailable)
	app, _ := oauthApplication(t, fixture, nil)
	client := oauthBrowser(t)

	response, err := client.Get(app.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	readOAuthBody(t, response)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
}

// TestOAuthLoginRefusesAnIdentityAdmissionDoesNotAdmit uses claim admission,
// which is the same rule the OIDC login applies, over claims that arrived from
// an account endpoint rather than from a signed token.
func TestOAuthLoginRefusesAnIdentityAdmissionDoesNotAdmit(t *testing.T) {
	fixture := newOAuthProviderFixture(t)
	app, _ := oauthApplication(t, fixture, func(config *Config) {
		config.OAuth.Admission = AdmissionClaim
		config.OAuth.Claim = ClaimConfig{Path: "/preferred_username", Values: []string{"SomebodyElse"}, Match: MatchAny}
	})
	client := oauthBrowser(t)

	response, err := client.Get(app.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	readOAuthBody(t, response)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.StatusCode)
	}
}

// TestOAuthCallbackRefusesAReplayedCorrelation covers the state half: the
// transaction is consumed once, so the same callback cannot be presented twice.
func TestOAuthCallbackRefusesAReplayedCorrelation(t *testing.T) {
	fixture := newOAuthProviderFixture(t)
	app, _ := oauthApplication(t, fixture, nil)
	client := oauthBrowser(t)

	if _, err := client.Get(app.URL + "/auth/login"); err != nil {
		t.Fatal(err)
	}
	replay, err := client.Get(app.URL + "/auth/callback?code=authorization-code&state=stale")
	if err != nil {
		t.Fatal(err)
	}
	readOAuthBody(t, replay)
	if replay.StatusCode != http.StatusBadRequest {
		t.Fatalf("replayed callback status = %d, want 400", replay.StatusCode)
	}
}

// TestOAuthCallbackRefusesAProviderError keeps the provider's own description
// out of the response.
func TestOAuthCallbackRefusesAProviderError(t *testing.T) {
	fixture := newOAuthProviderFixture(t)
	app, _ := oauthApplication(t, fixture, nil)
	client := &http.Client{}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	// Start a login so the browser holds a correlation cookie, then answer the
	// callback with a denial instead of a code.
	begin, err := client.Get(app.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	readOAuthBody(t, begin)
	denied, err := client.Get(app.URL + "/auth/callback?error=access_denied&error_description=user+refused&state=x")
	if err != nil {
		t.Fatal(err)
	}
	body := readOAuthBody(t, denied)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("denied callback status = %d, want 403", denied.StatusCode)
	}
	if strings.Contains(body, "user refused") || strings.Contains(body, "access_denied") {
		t.Fatalf("the provider's description was echoed back: %q", body)
	}
}

// TestOAuthLogoutEndsTheLocalSessionOnly states the mode's cost. There is no
// provider session to end and no prompt to demand, so the logout is local, and
// it must not leave behind the reconfirm cookie only the OIDC login reads.
func TestOAuthLogoutEndsTheLocalSessionOnly(t *testing.T) {
	fixture := newOAuthProviderFixture(t)
	app, instance := oauthApplication(t, fixture, nil)
	client := oauthBrowser(t)

	if _, err := client.Get(app.URL + "/auth/login"); err != nil {
		t.Fatal(err)
	}
	response, err := postLogout(client, app.URL, app.URL)
	if err != nil {
		t.Fatal(err)
	}
	if body := readOAuthBody(t, response); body != "anonymous" {
		t.Fatalf("after logout the landing page = %q", body)
	}
	root, _ := url.Parse(app.URL + "/")
	for _, cookie := range client.Jar.Cookies(root) {
		if cookie.Name == instance.reconfirmCookieName() && cookie.Value != "" {
			t.Fatalf("a plain OAuth logout wrote the reconfirm cookie nothing reads: %#v", cookie)
		}
	}
}

// TestOAuthModeRefusesAReProofItCannotPerform is the alternative to a redirect
// loop. The mode has no way to prove an identity twice, so a guard that asks
// for a confirmation is a deployment error reported as one.
func TestOAuthModeRefusesAReProofItCannotPerform(t *testing.T) {
	fixture := newOAuthProviderFixture(t)
	app, instance := oauthApplication(t, fixture, nil)
	client := oauthBrowser(t)
	if _, err := client.Get(app.URL + "/auth/login"); err != nil {
		t.Fatal(err)
	}

	guarded := httptest.NewServer(instance.manager.Middleware(nil)(
		httpFrame(instance.serve)(Ensure(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "admitted")
		}, Confirmed(0)))))
	defer guarded.Close()

	// The session cookie was issued for the application server; the guard runs
	// on another port of the same loopback host, so the jar sends it along.
	request, err := http.NewRequest(http.MethodGet, guarded.URL+"/danger", nil)
	if err != nil {
		t.Fatal(err)
	}
	appURL, _ := url.Parse(app.URL + "/")
	for _, cookie := range client.Jar.Cookies(appURL) {
		request.AddCookie(cookie)
	}
	response, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body := readOAuthBody(t, response)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %q, want 503 rather than a redirect into a login that cannot re-prove", response.StatusCode, body)
	}

	// A window measured from the login is a different question and stays
	// answerable, because it rests on when the session was created.
	relaxed := httptest.NewServer(instance.manager.Middleware(nil)(
		httpFrame(instance.serve)(Ensure(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "admitted")
		}, MaxAge(time.Hour)))))
	defer relaxed.Close()
	request, err = http.NewRequest(http.MethodGet, relaxed.URL+"/ok", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range client.Jar.Cookies(appURL) {
		request.AddCookie(cookie)
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if body := readOAuthBody(t, response); body != "admitted" {
		t.Fatalf("a freshness window measured from the login = %q, want it satisfied", body)
	}
}

// TestIdentityFromProfileNamesTheProviderRatherThanTheResponse keeps the account
// namespace out of the provider's own answer.
func TestIdentityFromProfileNamesTheProviderRatherThanTheResponse(t *testing.T) {
	provider, _ := oauthprofile.Lookup(oauthprofile.ProviderX)
	profile := oauthprofile.Profile{
		Subject:  "2244994945",
		Username: "XDevelopers",
		Claims: map[string]json.RawMessage{
			"sub":                json.RawMessage(`"2244994945"`),
			"preferred_username": json.RawMessage(`"XDevelopers"`),
			// A response claiming another issuer changes nothing: the namespace
			// is the definition's.
			"iss": json.RawMessage(`"https://accounts.google.com"`),
		},
	}
	identity := identityFromProfile(provider, profile, ClaimSubject)
	if identity.Issuer != "https://x.com" || identity.Subject != "2244994945" ||
		identity.KeyClaim != ClaimSubject || identity.Key != "2244994945" {
		t.Fatalf("identity = %+v", identity)
	}

	// A deployment may link on the handle if it insists, and then the handle is
	// what the account key becomes.
	byHandle := identityFromProfile(provider, profile, "preferred_username")
	if byHandle.Key != "XDevelopers" || byHandle.Subject != "2244994945" {
		t.Fatalf("identity by handle = %+v", byHandle)
	}

	// A claim the profile does not carry leaves no key, which admission refuses
	// rather than falling back to the subject.
	missing := identityFromProfile(provider, oauthprofile.Profile{Subject: "1"}, "picture")
	if missing.Key != "" {
		t.Fatalf("a missing identity claim produced the key %q", missing.Key)
	}
	if _, err := admitIdentity(t.Context(), OAuthConfig{Admission: AdmissionAuthenticated}.admissionRule(), nil, missing); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("admission of a keyless identity = %v, want %v", err, ErrAccessDenied)
	}
}

// TestOAuthLoginRemembersTheHandleWhenThereIsNoAddress covers the sign-in hint
// under a provider that reports no email address. An empty login identifier
// would leave the login screen with only a name to render.
func TestOAuthLoginRemembersTheHandleWhenThereIsNoAddress(t *testing.T) {
	fixture := newOAuthProviderFixture(t)
	app, instance := oauthApplication(t, fixture, func(config *Config) {
		config.Assurance.Hint = HintConfig{
			Enabled: true, Name: "pw_hint", TTL: time.Hour,
			Secret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		}
	})
	jar, err := hintJar(instance.config.Assurance.Hint, instance.cookiePolicy)
	if err != nil {
		t.Fatal(err)
	}
	instance.hint = jar

	client := oauthBrowser(t)
	if _, err := client.Get(app.URL + "/auth/login"); err != nil {
		t.Fatal(err)
	}
	root, _ := url.Parse(app.URL + "/")
	var hint *http.Cookie
	for _, cookie := range client.Jar.Cookies(root) {
		if cookie.Name == "pw_hint" {
			hint = cookie
		}
	}
	if hint == nil {
		t.Fatal("no sign-in hint was written")
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(hint)
	remembered, err := jar.LoadFrom(HTTPExchange(nil, request))
	if err != nil {
		t.Fatalf("the hint could not be read back: %v", err)
	}
	if remembered.LoginID != "XDevelopers" || remembered.Issuer != "https://x.com" {
		t.Fatalf("hint = %+v", remembered)
	}
	// The sealed cookie is what holds it; the value the browser carries must
	// not read as the handle.
	if strings.Contains(hint.Value, "XDevelopers") {
		t.Fatalf("the hint cookie was not sealed: %q", hint.Value)
	}
}

// TestOAuthSettingsBindUnderTheDocumentedKeys pins the TOML prefix.
//
// Generation derives a key from the Go field name and splits it at every
// lower-to-upper boundary, so the OAuth field binds under auth.o_auth without
// the key tag that corrects it — silently, and identically in the generated
// file, so the drift check between the two would still pass. This asserts the
// prefix every document names.
func TestOAuthSettingsBindUnderTheDocumentedKeys(t *testing.T) {
	overlay := configbind.NewOverlay()
	for key, value := range map[string]string{
		"auth.enabled":              "true",
		"auth.mode":                 ModeOAuthOnly,
		"auth.oauth.provider":       "x",
		"auth.oauth.client_id":      "client",
		"auth.oauth.client_secret":  "secret",
		"auth.oauth.redirect_url":   "https://app.example/auth/callback",
		"auth.oauth.identity_claim": "preferred_username",
		"auth.oauth.admission":      AdmissionExisting,
		"auth.oauth.auto_provision": "false",
	} {
		overlay.Set(key, value, configbind.PlaceFile)
	}
	var config Config
	if err := applyConfigDefinition0(&config, overlay); err != nil {
		t.Fatal(err)
	}
	if config.Mode != ModeOAuthOnly || config.OAuth.Provider != "x" || config.OAuth.ClientID != "client" ||
		config.OAuth.ClientSecret != "secret" || config.OAuth.RedirectURL != "https://app.example/auth/callback" ||
		config.OAuth.IdentityClaim != "preferred_username" || config.OAuth.Admission != AdmissionExisting ||
		config.OAuth.AutoProvision {
		t.Fatalf("[auth.oauth] did not bind: %+v", config.OAuth)
	}
	if err := config.validate(); err != nil {
		t.Fatalf("the bound configuration was refused: %v", err)
	}
}
