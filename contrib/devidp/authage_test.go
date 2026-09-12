package devidp_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/contrib/devidp"
	"github.com/shibukawa/popcornweb/contrib/oidc"
)

// browser keeps the provider's own session cookie across visits, which is what
// makes a second authorization behave like a returning user rather than a new
// one.
func browser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// visit follows one authorization request and returns the callback parameters.
func visit(t *testing.T, client *http.Client, target string) url.Values {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("expected a redirect, got %d", response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return location.Query()
}

// authTimeOf completes one authorization and returns the verified auth_time.
func authTimeOf(t *testing.T, party relyingParty, client *http.Client, options oidc.BeginOptions) (*time.Time, error) {
	t.Helper()
	authorizeURL, key, err := party.client.BeginAuthorization(t.Context(), options)
	if err != nil {
		t.Fatalf("begin authorization: %v", err)
	}
	query := visit(t, client, authorizeURL)
	if errorCode := query.Get("error"); errorCode != "" {
		return nil, errProvider(errorCode)
	}
	_, idToken, err := party.client.HandleCallback(t.Context(), key, oidc.Callback{
		Code:  query.Get("code"),
		State: query.Get("state"),
	}, oidc.CallbackOptions{RequireAuthTime: true})
	if err != nil {
		return nil, err
	}
	return idToken.AuthTime, nil
}

type errProvider string

func (e errProvider) Error() string { return "provider error: " + string(e) }

// This is the behaviour every real provider has and the reason auth_time is
// worth checking: the second token arrives now, and reports a proof from
// earlier, because the provider answered from its own session.
func TestASecondAuthorizationReportsTheEarlierAuthTime(t *testing.T) {
	clock := time.Now().Truncate(time.Second)
	now := func() time.Time { return clock }
	server := startProvider(t, devidp.Options{LoginUser: "admin", Now: now})
	party := newRelyingParty(t, server, now)
	client := browser(t)

	first, err := authTimeOf(t, party, client, oidc.BeginOptions{Scopes: []string{"openid"}})
	if err != nil || first == nil {
		t.Fatalf("first authorization: %v, %v", first, err)
	}
	clock = clock.Add(2 * time.Second)
	second, err := authTimeOf(t, party, client, oidc.BeginOptions{Scopes: []string{"openid"}})
	if err != nil || second == nil {
		t.Fatalf("second authorization: %v, %v", second, err)
	}
	if !second.Equal(*first) {
		t.Fatalf("auth_time moved from %v to %v; the provider re-authenticated when it should have answered from its session", first, second)
	}
}

// max_age is what a relying party sends to refuse that answer, and the provider
// honours it by authenticating again.
func TestMaxAgeForcesAFreshAuthentication(t *testing.T) {
	clock := time.Now().Truncate(time.Second)
	now := func() time.Time { return clock }
	server := startProvider(t, devidp.Options{LoginUser: "admin", Now: now})
	party := newRelyingParty(t, server, now)
	client := browser(t)

	first, err := authTimeOf(t, party, client, oidc.BeginOptions{Scopes: []string{"openid"}})
	if err != nil || first == nil {
		t.Fatalf("first authorization: %v, %v", first, err)
	}
	clock = clock.Add(2 * time.Second)
	zero := time.Duration(0)
	second, err := authTimeOf(t, party, client, oidc.BeginOptions{Scopes: []string{"openid"}, MaxAge: &zero})
	if err != nil || second == nil {
		t.Fatalf("second authorization: %v, %v", second, err)
	}
	if !second.After(*first) {
		t.Fatalf("auth_time stayed at %v under max_age=0; the provider ignored the request", first)
	}
}

// A max_age the session still satisfies is answered from it, so the parameter
// does not cost an interaction it did not need.
func TestAWideMaxAgeIsSatisfiedFromTheSession(t *testing.T) {
	server := startProvider(t, devidp.Options{LoginUser: "admin"})
	party := newRelyingParty(t, server)
	client := browser(t)

	first, err := authTimeOf(t, party, client, oidc.BeginOptions{Scopes: []string{"openid"}})
	if err != nil || first == nil {
		t.Fatalf("first authorization: %v, %v", first, err)
	}
	hour := time.Hour
	second, err := authTimeOf(t, party, client, oidc.BeginOptions{Scopes: []string{"openid"}, MaxAge: &hour})
	if err != nil || second == nil {
		t.Fatalf("second authorization: %v, %v", second, err)
	}
	if !second.Equal(*first) {
		t.Fatal("a max_age the session satisfied still triggered a re-authentication")
	}
}

func TestPromptLoginReauthenticatesAndPromptNoneRefuses(t *testing.T) {
	clock := time.Now().Truncate(time.Second)
	now := func() time.Time { return clock }
	server := startProvider(t, devidp.Options{LoginUser: "admin", Now: now})
	party := newRelyingParty(t, server, now)
	client := browser(t)

	first, err := authTimeOf(t, party, client, oidc.BeginOptions{Scopes: []string{"openid"}})
	if err != nil || first == nil {
		t.Fatalf("first authorization: %v, %v", first, err)
	}
	clock = clock.Add(2 * time.Second)
	relogin, err := authTimeOf(t, party, client, oidc.BeginOptions{Scopes: []string{"openid"}, Prompt: []string{"login"}})
	if err != nil || relogin == nil {
		t.Fatalf("prompt=login: %v, %v", relogin, err)
	}
	if !relogin.After(*first) {
		t.Fatal("prompt=login did not re-authenticate")
	}

	// prompt=none forbids interaction, so a provider with no session to answer
	// from must report login_required rather than ask.
	fresh := browser(t)
	if _, err := authTimeOf(t, party, fresh, oidc.BeginOptions{Scopes: []string{"openid"}, Prompt: []string{"none"}}); err == nil {
		t.Fatal("prompt=none completed without a session to answer from")
	} else if err.Error() != "provider error: login_required" {
		t.Fatalf("prompt=none error = %v", err)
	}
}

// Ending the provider session is what a global sign-out asks for, and the next
// authorization must therefore authenticate rather than answer from it.
func TestEndSessionClearsTheProviderSession(t *testing.T) {
	clock := time.Now().Truncate(time.Second)
	now := func() time.Time { return clock }
	server := startProvider(t, devidp.Options{LoginUser: "admin", Now: now})
	party := newRelyingParty(t, server, now)
	client := browser(t)

	first, err := authTimeOf(t, party, client, oidc.BeginOptions{Scopes: []string{"openid"}})
	if err != nil || first == nil {
		t.Fatalf("first authorization: %v, %v", first, err)
	}
	logout, err := party.client.EndSessionURL(oidc.EndSessionOptions{})
	if err != nil || logout == "" {
		t.Fatalf("end session url: %q, %v", logout, err)
	}
	response, err := client.Get(logout)
	if err != nil {
		t.Fatalf("end session: %v", err)
	}
	_ = response.Body.Close()

	clock = clock.Add(2 * time.Second)
	second, err := authTimeOf(t, party, client, oidc.BeginOptions{Scopes: []string{"openid"}})
	if err != nil || second == nil {
		t.Fatalf("second authorization: %v, %v", second, err)
	}
	if !second.After(*first) {
		t.Fatal("the provider answered from a session the logout should have ended")
	}
}
