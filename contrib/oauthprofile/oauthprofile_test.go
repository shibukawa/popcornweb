package oauthprofile

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// roundTripHandler serves a handler without opening a listener, so the fixed
// provider endpoints can be answered exactly as configured.
type roundTripHandler struct{ handler http.Handler }

func (r roundTripHandler) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	r.handler.ServeHTTP(recorder, request)
	result := recorder.Result()
	result.Body = io.NopCloser(bytes.NewReader(recorder.Body.Bytes()))
	return result, nil
}

func clientServing(handler http.HandlerFunc) *http.Client {
	return &http.Client{Transport: roundTripHandler{handler: handler}}
}

func TestLookupReturnsACopy(t *testing.T) {
	provider, ok := Lookup(ProviderX)
	if !ok {
		t.Fatalf("Lookup(%q) reported no provider", ProviderX)
	}
	provider.Scopes[0] = "mutated"
	provider.Profile["sub"] = "mutated"
	again, _ := Lookup(ProviderX)
	if again.Scopes[0] == "mutated" || again.Profile["sub"] == "mutated" {
		t.Fatal("Lookup handed out the shared definition")
	}
	if _, ok := Lookup("twitter"); ok {
		t.Fatal("Lookup accepted a provider it has no definition for")
	}
	if got := Names(); len(got) != 1 || got[0] != ProviderX {
		t.Fatalf("Names() = %v, want [%s]", got, ProviderX)
	}
}

// TestXProviderDefinition pins the endpoints and scopes, because they are
// constants a deployment cannot correct from configuration.
func TestXProviderDefinition(t *testing.T) {
	provider, _ := Lookup(ProviderX)
	for name, pair := range map[string][2]string{
		"issuer":        {provider.Issuer, "https://x.com"},
		"authorization": {provider.AuthorizationEndpoint, "https://x.com/i/oauth2/authorize"},
		"token":         {provider.TokenEndpoint, "https://api.x.com/2/oauth2/token"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s endpoint = %q, want %q", name, pair[0], pair[1])
		}
	}
	if !strings.HasPrefix(provider.ProfileEndpoint, "https://api.x.com/2/users/me") {
		t.Errorf("profile endpoint = %q", provider.ProfileEndpoint)
	}
	if strings.Join(provider.Scopes, " ") != "users.read tweet.read" {
		t.Errorf("scopes = %v; users.read needs tweet.read beside it, and offline.access is not asked for", provider.Scopes)
	}
	config := provider.OAuthConfig("client", "secret", "https://app.example/auth/callback")
	if config.TokenEndpoint != provider.TokenEndpoint || config.ClientID != "client" ||
		config.RedirectURI != "https://app.example/auth/callback" || config.AuthMethod != provider.AuthMethod {
		t.Errorf("OAuthConfig did not carry the endpoints and credentials: %+v", config)
	}
}

func TestFetchReadsAnXProfile(t *testing.T) {
	var seen *http.Request
	client := clientServing(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"id":"2244994945","name":"X Dev","username":"XDevelopers","profile_image_url":"https://pbs.example/avatar.jpg"}}`)
	})
	provider, _ := Lookup(ProviderX)
	profile, err := Fetch(context.Background(), provider, "token-value", Options{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Subject != "2244994945" || profile.Username != "XDevelopers" ||
		profile.Name != "X Dev" || profile.AvatarURL != "https://pbs.example/avatar.jpg" {
		t.Fatalf("profile = %+v", profile)
	}
	if got := string(profile.Claims["sub"]); got != `"2244994945"` {
		t.Errorf("sub claim = %s", got)
	}
	// The X spellings are normalized away rather than carried alongside, so
	// there is one name per value for admission to be configured against.
	for _, name := range []string{"id", "username", "profile_image_url"} {
		if _, ok := profile.Claims[name]; ok {
			t.Errorf("claim %q leaked X's own spelling into the claim set", name)
		}
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer token-value" {
		t.Errorf("Authorization = %q", got)
	}
}

// TestFetchOmitsClaimsAProviderLeftEmpty keeps an absent value absent, rather
// than turning it into an empty string a claim rule could match.
func TestFetchOmitsClaimsAProviderLeftEmpty(t *testing.T) {
	client := clientServing(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"id":"7","username":"handle"}}`)
	})
	provider, _ := Lookup(ProviderX)
	profile, err := Fetch(context.Background(), provider, "token", Options{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "" || profile.AvatarURL != "" {
		t.Fatalf("profile = %+v", profile)
	}
	if _, ok := profile.Claims["name"]; ok {
		t.Error("an absent display name became a claim")
	}
}

func TestFetchRefusesUnusableResponses(t *testing.T) {
	provider, _ := Lookup(ProviderX)
	for name, response := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"error status":  {http.StatusUnauthorized, `{"data":{"id":"1"}}`, ErrProfile},
		"no data":       {http.StatusOK, `{"errors":[{"message":"not found"}]}`, ErrProfile},
		"no id":         {http.StatusOK, `{"data":{"username":"handle"}}`, ErrProfile},
		"not json":      {http.StatusOK, `<html>`, ErrProfile},
		"id a fraction": {http.StatusOK, `{"data":{"id":2244994945.5}}`, ErrProfile},
		"id empty":      {http.StatusOK, `{"data":{"id":"","username":"handle"}}`, ErrProfile},
	} {
		t.Run(name, func(t *testing.T) {
			client := clientServing(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(response.status)
				_, _ = io.WriteString(w, response.body)
			})
			if _, err := Fetch(context.Background(), provider, "token", Options{HTTPClient: client}); !errors.Is(err, response.want) {
				t.Fatalf("err = %v, want %v", err, response.want)
			}
		})
	}
}

// TestFetchBoundsTheResponse refuses a provider answer larger than the limit
// rather than reading it into memory.
func TestFetchBoundsTheResponse(t *testing.T) {
	client := clientServing(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"id":"1","name":"`+strings.Repeat("a", 4096)+`"}}`)
	})
	provider, _ := Lookup(ProviderX)
	if _, err := Fetch(context.Background(), provider, "token", Options{HTTPClient: client, MaxResponseBytes: 256}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("err = %v, want %v", err, ErrLimitExceeded)
	}
}

// TestFetchRefusesADisplayValueTooLargeForASession drops an over-long name
// instead of storing it; the login still succeeds on the identifier.
func TestFetchRefusesADisplayValueTooLargeForASession(t *testing.T) {
	client := clientServing(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"id":"1","name":"`+strings.Repeat("a", maxProfileValueBytes+1)+`"}}`)
	})
	provider, _ := Lookup(ProviderX)
	profile, err := Fetch(context.Background(), provider, "token", Options{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Subject != "1" || profile.Name != "" {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestFetchRefusesATokenAHeaderCannotCarry(t *testing.T) {
	provider, _ := Lookup(ProviderX)
	called := false
	client := clientServing(func(http.ResponseWriter, *http.Request) { called = true })
	for _, token := range []string{"", "with space", "with\nnewline", strings.Repeat("a", maxAccessTokenBytes+1)} {
		if _, err := Fetch(context.Background(), provider, token, Options{HTTPClient: client}); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("token %q: err = %v, want %v", token, err, ErrInvalidToken)
		}
	}
	if called {
		t.Error("an unusable token still reached the provider")
	}
}

// TestClaimsListsWhatAProviderReports pins the vocabulary a deployment
// configures an identity claim against, in the stable order an error prints.
func TestClaimsListsWhatAProviderReports(t *testing.T) {
	provider, _ := Lookup(ProviderX)
	if got := strings.Join(provider.Claims(), " "); got != "name picture preferred_username sub" {
		t.Fatalf("Claims() = %q", got)
	}
	if provider.Profile["sub"] != "id" || provider.ProfileRoot != "/data" {
		t.Fatalf("X profile mapping = %v at %q", provider.Profile, provider.ProfileRoot)
	}
}

// TestFetchReadsANumericIdentifier covers the providers that answer with a JSON
// number where X answers with a string. The literal text is the claim, and a
// value that is not a whole number is left out rather than normalized.
func TestFetchReadsANumericIdentifier(t *testing.T) {
	numeric := Provider{
		Name: "fixture", Issuer: "https://fixture.example",
		ProfileEndpoint: "https://fixture.example/user",
		Profile:         map[string]string{"sub": "id", "preferred_username": "login"},
	}
	client := clientServing(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":42,"login":"octocat"}`)
	})
	profile, err := Fetch(context.Background(), numeric, "token", Options{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Subject != "42" || profile.Username != "octocat" {
		t.Fatalf("profile = %+v", profile)
	}

	fractional := clientServing(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":4.2,"login":"octocat"}`)
	})
	if _, err := Fetch(context.Background(), numeric, "token", Options{HTTPClient: fractional}); !errors.Is(err, ErrProfile) {
		t.Fatalf("a fractional identifier: err = %v, want %v", err, ErrProfile)
	}
}

// TestFetchRefusesAnEnvelopeThatIsNotThere keeps a provider whose response
// shape changed from silently reporting an account of nobody.
func TestFetchRefusesAnEnvelopeThatIsNotThere(t *testing.T) {
	provider, _ := Lookup(ProviderX)
	for name, body := range map[string]string{
		"root missing":     `{"id":"1"}`,
		"root not object":  `{"data":"1"}`,
		"response is list": `[{"id":"1"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			client := clientServing(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			})
			if _, err := Fetch(context.Background(), provider, "token", Options{HTTPClient: client}); !errors.Is(err, ErrProfile) {
				t.Fatalf("err = %v, want %v", err, ErrProfile)
			}
		})
	}
}

func TestFetchRefusesAnUndefinedProvider(t *testing.T) {
	if _, err := Fetch(context.Background(), Provider{}, "token", Options{}); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("err = %v, want %v", err, ErrUnknownProvider)
	}
}

// TestFetchNeverFollowsARedirect keeps the access token from reaching a host
// the provider names at request time.
//
// The refusal arrives as ErrHTTP where the client evaluates CheckRedirect, and
// as ErrProfile on a runtime that does not follow redirects at all and hands
// the 302 back as an unusable response. Both are refusals; what the test is
// about is that the token stayed with the provider either way.
func TestFetchNeverFollowsARedirect(t *testing.T) {
	elsewhere := false
	client := clientServing(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "api.x.com" {
			elsewhere = true
			_, _ = io.WriteString(w, `{"data":{"id":"1"}}`)
			return
		}
		http.Redirect(w, r, "https://attacker.example/me", http.StatusFound)
	})
	provider, _ := Lookup(ProviderX)
	_, err := Fetch(context.Background(), provider, "token", Options{HTTPClient: client})
	if !errors.Is(err, ErrHTTP) && !errors.Is(err, ErrProfile) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if elsewhere {
		t.Fatal("the bearer token followed a redirect off the provider")
	}
}
