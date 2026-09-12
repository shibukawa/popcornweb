package devidp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/authstate/memory"
	"github.com/shibukawa/popcornweb/contrib/devidp"
	"github.com/shibukawa/popcornweb/contrib/oauth"
	"github.com/shibukawa/popcornweb/contrib/oidc"
)

const roster = `
[idp]
valid_scopes = ["admin"]

[users.admin]
display_name = "Administrator"
extra_scopes = ["admin"]
[users.admin.claims]
email = "admin@example.com"
role = "admin"
employee_id = 42

[users.guest]
display_name = "Guest User"
[users.guest.claims]
email = "guest@example.com"
`

// relyingParty is the contrib/oidc client wired to a running provider.
type relyingParty struct {
	client   *oidc.Client
	redirect string
}

func startProvider(t *testing.T, options devidp.Options) *devidp.Server {
	t.Helper()
	config, err := devidp.ParseConfig([]byte(roster), t.TempDir())
	if err != nil {
		t.Fatalf("parse roster: %v", err)
	}
	if options.Logf == nil {
		options.Logf = t.Logf
	}
	server, err := devidp.Start(t.Context(), "127.0.0.1:0", config, options)
	if err != nil {
		t.Fatalf("start provider: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func newRelyingParty(t *testing.T, server *devidp.Server, clocks ...func() time.Time) relyingParty {
	t.Helper()
	var clock func() time.Time
	if len(clocks) > 0 {
		clock = clocks[0]
	}
	credentials, err := server.RegisterClient(devidp.ClientSpec{LoopbackRedirects: true})
	if err != nil {
		t.Fatalf("register client: %v", err)
	}
	provider, err := oidc.Discover(t.Context(), server.Issuer(), oidc.DiscoverOptions{AllowLoopbackHTTP: true, Clock: clock})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	store, err := memory.NewStore[oauth.Transaction](memory.Options{Now: clock})
	if err != nil {
		t.Fatalf("state store: %v", err)
	}
	redirect := "http://127.0.0.1:65000/auth/callback"
	client, err := oidc.NewClient(provider, oidc.Config{
		ClientID:          credentials.ID,
		ClientSecret:      credentials.Secret,
		RedirectURI:       redirect,
		AllowLoopbackHTTP: true,
	}, oidc.Options{Clock: clock, OAuth: oauth.Options{StateStore: store, Clock: clock}})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return relyingParty{client: client, redirect: redirect}
}

// browse performs one request without following redirects, the way a browser
// hands the callback back to the application.
func browse(t *testing.T, method, target string, form url.Values) *http.Response {
	t.Helper()
	var request *http.Request
	var err error
	if form == nil {
		request, err = http.NewRequestWithContext(t.Context(), method, target, nil)
	} else {
		request, err = http.NewRequestWithContext(t.Context(), method, target, strings.NewReader(form.Encode()))
		if err == nil {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// locationOf reads the redirect target of a response.
func locationOf(t *testing.T, response *http.Response) *url.URL {
	t.Helper()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("expected a redirect, got %d", response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	return location
}

// callbackOf reads the redirect a completed authorization produced.
func callbackOf(t *testing.T, response *http.Response) url.Values {
	t.Helper()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("expected a redirect, got %d", response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	return location.Query()
}

func TestAutomaticLoginCompletesAuthorizationCodeFlow(t *testing.T) {
	server := startProvider(t, devidp.Options{LoginUser: "admin"})
	party := newRelyingParty(t, server)

	authorizeURL, key, err := party.client.BeginAuthorization(t.Context(), oidc.BeginOptions{
		Scopes: []string{"openid", "profile", "email", "admin"},
	})
	if err != nil {
		t.Fatalf("begin authorization: %v", err)
	}
	query := callbackOf(t, browse(t, http.MethodGet, authorizeURL, nil))
	if query.Get("code") == "" {
		t.Fatalf("expected a code, got %v", query)
	}

	tokens, _, err := party.client.HandleCallback(t.Context(), key, oidc.Callback{
		Code:  query.Get("code"),
		State: query.Get("state"),
	}, oidc.CallbackOptions{})
	if err != nil {
		t.Fatalf("handle callback: %v", err)
	}
	if tokens.TokenType != "Bearer" {
		t.Fatalf("token_type = %q", tokens.TokenType)
	}
	raw, ok := tokens.Raw["id_token"]
	if !ok {
		t.Fatalf("expected an id_token in %v", tokens.Raw)
	}
	var idTokenValue string
	if err := json.Unmarshal(raw, &idTokenValue); err != nil {
		t.Fatalf("decode id_token: %v", err)
	}
	idToken, err := party.client.VerifyIDToken(t.Context(), idTokenValue)
	if err != nil {
		t.Fatalf("verify id token: %v", err)
	}
	if subject, _ := idToken.Claims.String("sub"); subject != "admin" {
		t.Fatalf("sub = %q", subject)
	}
	if email, _ := idToken.Claims.String("email"); email != "admin@example.com" {
		t.Fatalf("email = %q", email)
	}
	if role, _ := idToken.Claims.String("role"); role != "admin" {
		t.Fatalf("role = %q", role)
	}

	claims, err := party.client.UserInfoWithSubject(t.Context(), tokens.AccessToken, "admin")
	if err != nil {
		t.Fatalf("userinfo: %v", err)
	}
	if _, ok := claims["email"]; !ok {
		t.Fatalf("expected email in userinfo, got %v", claims)
	}
}

func TestUserSelectionScreenIssuesTheSameClaims(t *testing.T) {
	server := startProvider(t, devidp.Options{})
	party := newRelyingParty(t, server)

	authorizeURL, key, err := party.client.BeginAuthorization(t.Context(), oidc.BeginOptions{
		Scopes: []string{"openid", "email"},
	})
	if err != nil {
		t.Fatalf("begin authorization: %v", err)
	}
	page := browse(t, http.MethodGet, authorizeURL, nil)
	if page.StatusCode != http.StatusOK {
		t.Fatalf("expected the login screen, got %d", page.StatusCode)
	}
	body := readAll(t, page)
	if strings.Contains(body, `type="password"`) {
		t.Fatal("the login screen must not contain a password field")
	}
	for _, expected := range []string{"Administrator", "Guest User", "no password is checked"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected %q on the login screen", expected)
		}
	}
	auth, csrf := formValues(t, body)

	query := callbackOf(t, browse(t, http.MethodPost, server.Issuer()+"/login", url.Values{
		"auth":    {auth},
		"csrf":    {csrf},
		"subject": {"guest"},
	}))
	tokens, _, err := party.client.HandleCallback(t.Context(), key, oidc.Callback{
		Code:  query.Get("code"),
		State: query.Get("state"),
	}, oidc.CallbackOptions{})
	if err != nil {
		t.Fatalf("handle callback: %v", err)
	}
	claims, err := party.client.UserInfoWithSubject(t.Context(), tokens.AccessToken, "guest")
	if err != nil {
		t.Fatalf("userinfo: %v", err)
	}
	var email string
	if err := json.Unmarshal(claims["email"], &email); err != nil {
		t.Fatalf("decode email: %v", err)
	}
	if email != "guest@example.com" {
		t.Fatalf("email = %q", email)
	}
}

func TestSelectionRejectsAMismatchedCSRFToken(t *testing.T) {
	server := startProvider(t, devidp.Options{})
	party := newRelyingParty(t, server)
	authorizeURL, _, err := party.client.BeginAuthorization(t.Context(), oidc.BeginOptions{Scopes: []string{"openid"}})
	if err != nil {
		t.Fatalf("begin authorization: %v", err)
	}
	auth, _ := formValues(t, readAll(t, browse(t, http.MethodGet, authorizeURL, nil)))

	response := browse(t, http.MethodPost, server.Issuer()+"/login", url.Values{
		"auth":    {auth},
		"csrf":    {"forged"},
		"subject": {"admin"},
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.StatusCode)
	}
}

func TestCancelReturnsAccessDenied(t *testing.T) {
	server := startProvider(t, devidp.Options{})
	party := newRelyingParty(t, server)
	authorizeURL, key, err := party.client.BeginAuthorization(t.Context(), oidc.BeginOptions{Scopes: []string{"openid"}})
	if err != nil {
		t.Fatalf("begin authorization: %v", err)
	}
	auth, csrf := formValues(t, readAll(t, browse(t, http.MethodGet, authorizeURL, nil)))

	query := callbackOf(t, browse(t, http.MethodPost, server.Issuer()+"/login", url.Values{
		"auth":   {auth},
		"csrf":   {csrf},
		"cancel": {"1"},
	}))
	if query.Get("error") != "access_denied" {
		t.Fatalf("error = %q", query.Get("error"))
	}
	if _, _, err := party.client.HandleCallback(t.Context(), key, oidc.Callback{
		State: query.Get("state"),
		Error: query.Get("error"),
	}, oidc.CallbackOptions{}); err == nil {
		t.Fatal("expected the relying party to reject a cancelled login")
	}
}

func TestCodeIsSingleUse(t *testing.T) {
	server := startProvider(t, devidp.Options{LoginUser: "admin"})
	party := newRelyingParty(t, server)
	authorizeURL, key, err := party.client.BeginAuthorization(t.Context(), oidc.BeginOptions{Scopes: []string{"openid"}})
	if err != nil {
		t.Fatalf("begin authorization: %v", err)
	}
	query := callbackOf(t, browse(t, http.MethodGet, authorizeURL, nil))
	callback := oidc.Callback{Code: query.Get("code"), State: query.Get("state")}
	if _, _, err := party.client.HandleCallback(t.Context(), key, callback, oidc.CallbackOptions{}); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	if _, _, err := party.client.HandleCallback(t.Context(), key, callback, oidc.CallbackOptions{}); err == nil {
		t.Fatal("expected the replayed code to fail")
	}
}

func TestTokenEndpointRejectsAWrongVerifierAndSecret(t *testing.T) {
	server := startProvider(t, devidp.Options{LoginUser: "admin"})
	party := newRelyingParty(t, server)
	credentials, err := server.RegisterClient(devidp.ClientSpec{LoopbackRedirects: true})
	if err != nil {
		t.Fatalf("register client: %v", err)
	}

	authorizeURL, _, err := party.client.BeginAuthorization(t.Context(), oidc.BeginOptions{Scopes: []string{"openid"}})
	if err != nil {
		t.Fatalf("begin authorization: %v", err)
	}
	query := callbackOf(t, browse(t, http.MethodGet, authorizeURL, nil))

	// A valid code with a verifier that never produced its challenge.
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {query.Get("code")},
		"redirect_uri":  {party.redirect},
		"code_verifier": {strings.Repeat("a", 43)},
		"client_id":     {credentials.ID},
		"client_secret": {credentials.Secret},
	}
	response := browse(t, http.MethodPost, server.Issuer()+"/token", form)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a mismatched verifier, got %d", response.StatusCode)
	}

	form.Set("client_secret", "wrong")
	response = browse(t, http.MethodPost, server.Issuer()+"/token", form)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a wrong secret, got %d", response.StatusCode)
	}
}

func TestAuthorizeRejectsUnregisteredAndNonLoopbackRedirects(t *testing.T) {
	server := startProvider(t, devidp.Options{LoginUser: "admin"})
	credentials, err := server.RegisterClient(devidp.ClientSpec{LoopbackRedirects: true})
	if err != nil {
		t.Fatalf("register client: %v", err)
	}
	declared, err := server.RegisterClient(devidp.ClientSpec{
		RedirectURIs: []string{"http://127.0.0.1:9000/callback"},
	})
	if err != nil {
		t.Fatalf("register declared client: %v", err)
	}

	authorize := func(clientID, redirect string) int {
		target := server.Issuer() + "/authorize?" + url.Values{
			"client_id":             {clientID},
			"redirect_uri":          {redirect},
			"response_type":         {"code"},
			"scope":                 {"openid"},
			"state":                 {"state-value"},
			"code_challenge":        {strings.Repeat("a", 43)},
			"code_challenge_method": {"S256"},
		}.Encode()
		return browse(t, http.MethodGet, target, nil).StatusCode
	}
	if status := authorize(credentials.ID, "http://evil.example/callback"); status != http.StatusBadRequest {
		t.Fatalf("expected a non-loopback redirect to be rejected, got %d", status)
	}
	if status := authorize(credentials.ID, "http://127.0.0.1:1234/anything"); status != http.StatusFound {
		t.Fatalf("expected a loopback redirect to be accepted, got %d", status)
	}
	if status := authorize(declared.ID, "http://127.0.0.1:9001/callback"); status != http.StatusBadRequest {
		t.Fatalf("expected an exact-match client to reject another port, got %d", status)
	}
	if status := authorize(declared.ID, "http://127.0.0.1:9000/callback"); status != http.StatusFound {
		t.Fatalf("expected the registered redirect to be accepted, got %d", status)
	}
}

func TestAuthorizeRequiresPKCEAndOpenIDScope(t *testing.T) {
	server := startProvider(t, devidp.Options{LoginUser: "admin"})
	credentials, err := server.RegisterClient(devidp.ClientSpec{LoopbackRedirects: true})
	if err != nil {
		t.Fatalf("register client: %v", err)
	}
	base := url.Values{
		"client_id":     {credentials.ID},
		"redirect_uri":  {"http://127.0.0.1:9100/callback"},
		"response_type": {"code"},
		"scope":         {"openid"},
		"state":         {"state-value"},
	}
	for name, mutate := range map[string]func(url.Values){
		"missing challenge": func(values url.Values) { values.Set("code_challenge_method", "S256") },
		"plain method": func(values url.Values) {
			values.Set("code_challenge_method", "plain")
			values.Set("code_challenge", strings.Repeat("a", 43))
		},
		"missing openid": func(values url.Values) {
			values.Set("scope", "profile")
			values.Set("code_challenge_method", "S256")
			values.Set("code_challenge", strings.Repeat("a", 43))
		},
		"wrong response type": func(values url.Values) {
			values.Set("response_type", "token")
			values.Set("code_challenge_method", "S256")
			values.Set("code_challenge", strings.Repeat("a", 43))
		},
	} {
		values := url.Values{}
		for key, value := range base {
			values[key] = value
		}
		mutate(values)
		query := callbackOf(t, browse(t, http.MethodGet, server.Issuer()+"/authorize?"+values.Encode(), nil))
		if query.Get("error") == "" {
			t.Fatalf("%s: expected an error redirect, got %v", name, query)
		}
		if query.Get("state") != "state-value" {
			t.Fatalf("%s: state = %q", name, query.Get("state"))
		}
	}
}

func TestScopesAreIntersectedWithTheRoster(t *testing.T) {
	server := startProvider(t, devidp.Options{LoginUser: "guest"})
	party := newRelyingParty(t, server)
	authorizeURL, key, err := party.client.BeginAuthorization(t.Context(), oidc.BeginOptions{
		Scopes: []string{"openid", "admin"},
	})
	if err != nil {
		t.Fatalf("begin authorization: %v", err)
	}
	query := callbackOf(t, browse(t, http.MethodGet, authorizeURL, nil))
	tokens, _, err := party.client.HandleCallback(t.Context(), key, oidc.Callback{
		Code:  query.Get("code"),
		State: query.Get("state"),
	}, oidc.CallbackOptions{})
	if err != nil {
		t.Fatalf("handle callback: %v", err)
	}
	if strings.Contains(tokens.Scope, "admin") {
		t.Fatalf("guest must not receive the admin scope, got %q", tokens.Scope)
	}
}

func TestSetLoginUserRejectsAnUnknownSubject(t *testing.T) {
	server := startProvider(t, devidp.Options{})
	if err := server.SetLoginUser("nobody"); err == nil {
		t.Fatal("expected an unknown subject to fail")
	}
	if err := server.SetLoginUser("guest"); err != nil {
		t.Fatalf("set login user: %v", err)
	}
	if server.LoginUser() != "guest" {
		t.Fatalf("login user = %q", server.LoginUser())
	}
}

func TestStartRejectsANonLoopbackBind(t *testing.T) {
	config, err := devidp.ParseConfig([]byte(roster), t.TempDir())
	if err != nil {
		t.Fatalf("parse roster: %v", err)
	}
	if _, err := devidp.Start(t.Context(), "0.0.0.0:0", config, devidp.Options{}); err == nil {
		t.Fatal("expected a non-loopback bind to be refused")
	}
}

func TestStartRefusesAProductionEnvironment(t *testing.T) {
	config, err := devidp.ParseConfig([]byte(roster), t.TempDir())
	if err != nil {
		t.Fatalf("parse roster: %v", err)
	}
	// The constructor shares this guard, so one Start-level case is enough to
	// keep the public listener entry point covered without repeating its aliases.
	t.Setenv("APP_ENV", "production")
	if _, err := devidp.Start(t.Context(), "127.0.0.1:0", config, devidp.Options{}); err == nil {
		t.Fatal("Start accepted a production environment")
	}
}

// Handler is exported, so an application can mount the provider on its own mux
// without ever calling Start. The environment lock has to be on the constructor
// for that path to carry it.
func TestNewRefusesOutsideDevelopment(t *testing.T) {
	config, err := devidp.ParseConfig([]byte(roster), t.TempDir())
	if err != nil {
		t.Fatalf("parse roster: %v", err)
	}
	config.Issuer = "https://idp.example"
	for _, environment := range []string{"production", "preview"} {
		t.Setenv("APP_ENV", environment)
		if _, err := devidp.New(config, devidp.Options{}); err == nil {
			t.Fatalf("New built a provider under APP_ENV=%q", environment)
		}
	}
	for _, environment := range []string{"dev", ""} {
		t.Setenv("APP_ENV", environment)
		if _, err := devidp.New(config, devidp.Options{}); err != nil {
			t.Fatalf("New refused APP_ENV=%q: %v", environment, err)
		}
	}
}

func TestDiscoveryAdvertisesOnlyImplementedBehavior(t *testing.T) {
	server := startProvider(t, devidp.Options{})
	response := browse(t, http.MethodGet, server.Issuer()+"/.well-known/openid-configuration", nil)
	var document struct {
		Issuer                 string   `json:"issuer"`
		GrantTypes             []string `json:"grant_types_supported"`
		ResponseTypes          []string `json:"response_types_supported"`
		ChallengeMethods       []string `json:"code_challenge_methods_supported"`
		SigningAlgorithms      []string `json:"id_token_signing_alg_values_supported"`
		TokenEndpointAuthTypes []string `json:"token_endpoint_auth_methods_supported"`
		DeviceEndpoint         string   `json:"device_authorization_endpoint"`
		EndSessionEndpoint     string   `json:"end_session_endpoint"`
	}
	if err := json.Unmarshal([]byte(readAll(t, response)), &document); err != nil {
		t.Fatalf("decode discovery: %v", err)
	}
	if document.Issuer != server.Issuer() {
		t.Fatalf("issuer = %q, want %q", document.Issuer, server.Issuer())
	}
	if len(document.GrantTypes) != 2 || document.GrantTypes[0] != "authorization_code" || document.GrantTypes[1] != oauth.DeviceGrantType {
		t.Fatalf("grant_types_supported = %v", document.GrantTypes)
	}
	assertOnly(t, "response_types_supported", document.ResponseTypes, "code")
	assertOnly(t, "code_challenge_methods_supported", document.ChallengeMethods, "S256")
	assertOnly(t, "id_token_signing_alg_values_supported", document.SigningAlgorithms, "RS256")
	if len(document.TokenEndpointAuthTypes) != 3 || document.TokenEndpointAuthTypes[0] != oauth.AuthNone {
		t.Fatalf("token_endpoint_auth_methods_supported = %v", document.TokenEndpointAuthTypes)
	}
	if document.DeviceEndpoint != server.Issuer()+"/device_authorization" {
		t.Fatalf("device_authorization_endpoint = %q", document.DeviceEndpoint)
	}
	if document.EndSessionEndpoint != server.Issuer()+"/end_session" {
		t.Fatalf("end_session_endpoint = %q", document.EndSessionEndpoint)
	}
}

func TestPublicDeviceAuthorizationFlow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	server := startProvider(t, devidp.Options{Now: func() time.Time { return now }})
	credentials, err := server.RegisterPublicDeviceClient(devidp.PublicDeviceClientSpec{ValidScopes: []string{"admin"}})
	if err != nil {
		t.Fatalf("register public device client: %v", err)
	}
	if credentials.ID == "" || credentials.Secret != "" {
		t.Fatalf("public credentials = %#v", credentials)
	}
	provider, err := oidc.Discover(t.Context(), server.Issuer(), oidc.DiscoverOptions{AllowLoopbackHTTP: true, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	client, err := oidc.NewDeviceClient(provider, oidc.DeviceConfig{ClientID: credentials.ID, AllowLoopbackHTTP: true}, oidc.DeviceOptions{
		Clock: func() time.Time { return now },
		OAuth: oauth.DeviceOptions{Clock: func() time.Time { return now }, Wait: func(_ context.Context, duration time.Duration) error {
			now = now.Add(duration)
			return nil
		}},
	})
	if err != nil {
		t.Fatalf("new device client: %v", err)
	}
	authorization, err := client.Begin(t.Context(), oidc.DeviceBeginOptions{Scopes: []string{"profile", "email", "admin"}})
	if err != nil {
		t.Fatalf("begin device authorization: %v", err)
	}
	if authorization.UserCode == "" || authorization.VerificationURIComplete == "" {
		t.Fatalf("authorization = %#v", authorization)
	}
	page := readAll(t, browse(t, http.MethodGet, authorization.VerificationURIComplete, nil))
	csrf := hiddenValue(t, page, "csrf")
	userCode := hiddenValue(t, page, "user_code")
	response := browse(t, http.MethodPost, server.Issuer()+"/device", url.Values{
		"user_code": {userCode}, "csrf": {csrf}, "subject": {"admin"},
	})
	if response.StatusCode != http.StatusOK || !strings.Contains(readAll(t, response), "device is authorized") {
		t.Fatalf("approval status = %d", response.StatusCode)
	}
	tokens, idToken, err := client.Poll(t.Context(), authorization)
	if err != nil {
		t.Fatalf("poll device token: %v", err)
	}
	if tokens.AccessToken == "" || idToken.Claims.Subject != "admin" || idToken.Nonce != "" {
		t.Fatalf("tokens=%#v id_token=%#v", tokens, idToken)
	}
}

func TestDevicePollingSlowDownDenialAndReplay(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	server := startProvider(t, devidp.Options{Now: func() time.Time { return now }})
	credentials, err := server.RegisterPublicDeviceClient(devidp.PublicDeviceClientSpec{ID: "public-device"})
	if err != nil {
		t.Fatal(err)
	}
	begin := browse(t, http.MethodPost, server.Issuer()+"/device_authorization", url.Values{
		"client_id": {credentials.ID}, "scope": {"openid profile"},
	})
	var authorization struct {
		DeviceCode              string `json:"device_code"`
		VerificationURIComplete string `json:"verification_uri_complete"`
	}
	if err := json.Unmarshal([]byte(readAll(t, begin)), &authorization); err != nil {
		t.Fatal(err)
	}
	poll := func() string {
		response := browse(t, http.MethodPost, server.Issuer()+"/token", url.Values{
			"client_id": {credentials.ID}, "grant_type": {oauth.DeviceGrantType}, "device_code": {authorization.DeviceCode},
		})
		var result struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(readAll(t, response)), &result); err != nil {
			t.Fatal(err)
		}
		return result.Error
	}
	if got := poll(); got != "slow_down" {
		t.Fatalf("early poll = %q", got)
	}
	now = now.Add(10 * time.Second)
	if got := poll(); got != "authorization_pending" {
		t.Fatalf("pending poll = %q", got)
	}
	page := readAll(t, browse(t, http.MethodGet, authorization.VerificationURIComplete, nil))
	response := browse(t, http.MethodPost, server.Issuer()+"/device", url.Values{
		"user_code": {hiddenValue(t, page, "user_code")}, "csrf": {hiddenValue(t, page, "csrf")}, "deny": {"1"},
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("deny status = %d", response.StatusCode)
	}
	now = now.Add(10 * time.Second)
	if got := poll(); got != "access_denied" {
		t.Fatalf("denied poll = %q", got)
	}
	if got := poll(); got != "expired_token" {
		t.Fatalf("replay poll = %q", got)
	}
}

func TestCloseDestroysProviderState(t *testing.T) {
	server := startProvider(t, devidp.Options{LoginUser: "admin"})
	if err := server.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := server.RegisterClient(devidp.ClientSpec{LoopbackRedirects: true}); err == nil {
		t.Fatal("expected registration to fail after close")
	}
	if err := server.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func assertOnly(t *testing.T, field string, values []string, expected string) {
	t.Helper()
	if len(values) != 1 || values[0] != expected {
		t.Fatalf("%s = %v, want [%s]", field, values, expected)
	}
}

func readAll(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

// formValues extracts the pending authorization key and CSRF token the login
// screen embedded, the way a browser would submit them.
func formValues(t *testing.T, body string) (string, string) {
	t.Helper()
	return hiddenValue(t, body, "auth"), hiddenValue(t, body, "csrf")
}

func hiddenValue(t *testing.T, body, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	index := strings.Index(body, marker)
	if index < 0 {
		t.Fatalf("no %s field on the login screen", name)
	}
	rest := body[index+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		t.Fatalf("unterminated %s field", name)
	}
	return rest[:end]
}

// completeLogin drives an automatic-login authorization and returns the tokens.
func completeLogin(t *testing.T, server *devidp.Server, party relyingParty) oidc.TokenSet {
	t.Helper()
	authorizeURL, key, err := party.client.BeginAuthorization(t.Context(), oidc.BeginOptions{
		Scopes: []string{"openid", "profile", "email"},
	})
	if err != nil {
		t.Fatalf("begin authorization: %v", err)
	}
	query := callbackOf(t, browse(t, http.MethodGet, authorizeURL, nil))
	tokens, _, err := party.client.HandleCallback(t.Context(), key, oidc.Callback{
		Code:  query.Get("code"),
		State: query.Get("state"),
	}, oidc.CallbackOptions{})
	if err != nil {
		t.Fatalf("handle callback: %v", err)
	}
	return tokens
}

func rawIDToken(t *testing.T, tokens oidc.TokenSet) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(tokens.Raw["id_token"], &value); err != nil {
		t.Fatalf("decode id_token: %v", err)
	}
	return value
}

func TestEndSessionRedirectsAndRevokesTokens(t *testing.T) {
	server := startProvider(t, devidp.Options{LoginUser: "admin"})
	party := newRelyingParty(t, server)
	tokens := completeLogin(t, server, party)

	if _, err := party.client.UserInfo(t.Context(), tokens.AccessToken); err != nil {
		t.Fatalf("userinfo before logout: %v", err)
	}

	target := server.Issuer() + "/end_session?" + url.Values{
		"id_token_hint":            {rawIDToken(t, tokens)},
		"post_logout_redirect_uri": {"http://127.0.0.1:65000/signed-out"},
		"state":                    {"logout-state"},
	}.Encode()
	query := callbackOf(t, browse(t, http.MethodGet, target, nil))
	if query.Get("state") != "logout-state" {
		t.Fatalf("state = %q", query.Get("state"))
	}
	// The session is over, so the tokens it produced stop working.
	if _, err := party.client.UserInfo(t.Context(), tokens.AccessToken); err == nil {
		t.Fatal("the access token survived the logout")
	}
}

func TestEndSessionRejectsForgedAndNonLocalInput(t *testing.T) {
	server := startProvider(t, devidp.Options{LoginUser: "admin"})
	party := newRelyingParty(t, server)
	tokens := completeLogin(t, server, party)
	hint := rawIDToken(t, tokens)

	forged := hint[:len(hint)-4] + "AAAA"
	status := browse(t, http.MethodGet, server.Issuer()+"/end_session?"+url.Values{
		"id_token_hint": {forged},
	}.Encode(), nil).StatusCode
	if status != http.StatusBadRequest {
		t.Fatalf("forged hint status = %d", status)
	}

	status = browse(t, http.MethodGet, server.Issuer()+"/end_session?"+url.Values{
		"id_token_hint":            {hint},
		"post_logout_redirect_uri": {"http://evil.example/signed-out"},
	}.Encode(), nil).StatusCode
	if status != http.StatusBadRequest {
		t.Fatalf("non-loopback post_logout_redirect_uri status = %d", status)
	}

	status = browse(t, http.MethodGet, server.Issuer()+"/end_session", nil).StatusCode
	if status != http.StatusBadRequest {
		t.Fatalf("missing client status = %d", status)
	}
}

func TestEndSessionWithoutARedirectRendersAPage(t *testing.T) {
	server := startProvider(t, devidp.Options{LoginUser: "admin"})
	party := newRelyingParty(t, server)
	tokens := completeLogin(t, server, party)

	response := browse(t, http.MethodPost, server.Issuer()+"/end_session", url.Values{
		"id_token_hint": {rawIDToken(t, tokens)},
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if body := readAll(t, response); !strings.Contains(body, "signed out") {
		t.Fatalf("body = %q", body)
	}
}

func TestEndSessionAcceptsAnUnregisteredLocalRedirect(t *testing.T) {
	server := startProvider(t, devidp.Options{LoginUser: "admin"})
	party := newRelyingParty(t, server)
	tokens := completeLogin(t, server, party)
	hint := rawIDToken(t, tokens)

	// A declared client lists its callback only. A real provider would demand
	// the post-logout URL be registered too; this one does not, so a developer
	// never has to declare it.
	declared, err := server.RegisterClient(devidp.ClientSpec{
		ID:           "declared",
		RedirectURIs: []string{"http://127.0.0.1:9000/callback"},
	})
	if err != nil {
		t.Fatalf("register declared client: %v", err)
	}
	for _, redirect := range []string{
		"http://127.0.0.1:9100/somewhere-else",
		"http://localhost:3000/signed-out",
		"http://app.localhost:8443/signed-out",
	} {
		location := locationOf(t, browse(t, http.MethodGet, server.Issuer()+"/end_session?"+url.Values{
			"client_id":                {declared.ID},
			"post_logout_redirect_uri": {redirect},
			"state":                    {"logout-state"},
		}.Encode(), nil))
		if location.Query().Get("state") != "logout-state" {
			t.Fatalf("state = %q", location.Query().Get("state"))
		}
		location.RawQuery = ""
		if location.String() != redirect {
			t.Fatalf("redirected to %q, want %q", location, redirect)
		}
	}

	// A non-local target is still refused: this provider must not become an
	// open redirect for anything off the machine.
	status := browse(t, http.MethodGet, server.Issuer()+"/end_session?"+url.Values{
		"id_token_hint":            {hint},
		"post_logout_redirect_uri": {"http://evil.example/signed-out"},
	}.Encode(), nil).StatusCode
	if status != http.StatusBadRequest {
		t.Fatalf("non-local post_logout_redirect_uri status = %d", status)
	}
}
