package oauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/authstate/memory"
	"github.com/shibukawa/popcornweb/contrib/internal/authn"
)

type roundTripHandler struct{ handler http.Handler }

func (r roundTripHandler) RoundTrip(req *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	r.handler.ServeHTTP(recorder, req)
	result := recorder.Result()
	result.Body = io.NopCloser(bytes.NewReader(recorder.Body.Bytes()))
	return result, nil
}

type contextBlockingTransport struct{}

func (contextBlockingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

type capturingStore struct{ transaction Transaction }

func (s *capturingStore) Put(_ context.Context, _ string, value Transaction, _ time.Time) error {
	s.transaction = value
	return nil
}

func (s *capturingStore) Take(_ context.Context, _ string) (Transaction, error) {
	return s.transaction, nil
}

type secretErrorTransport struct{}

func (secretErrorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("transport failed: access-secret=should-not-escape")
}

func newOAuthFixture(t *testing.T, handler http.Handler) (*Client, *http.Client) {
	t.Helper()
	store, err := memory.NewStore[Transaction](memory.Options{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{
		AuthorizationEndpoint: "https://authorization.example/authorize",
		TokenEndpoint:         "https://authorization.example/token",
		ClientID:              "client", ClientSecret: "secret", RedirectURI: "https://app.example/callback",
	}, Options{StateStore: store, HTTPClient: &http.Client{Transport: roundTripHandler{handler: handler}}})
	if err != nil {
		t.Fatal(err)
	}
	return client, client.httpClient
}

func TestBeginAndHandleCallbackConsumesState(t *testing.T) {
	var receivedVerifier string
	client, _ := newOAuthFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		basicID, basicSecret, basicOK := r.BasicAuth()
		if !basicOK || basicID != "client" || basicSecret != "secret" || r.Form.Get("client_secret") != "" {
			t.Errorf("client_secret_basic request leaked or was missing: auth=%q/%q/%t form=%v", basicID, basicSecret, basicOK, r.Form)
		}
		receivedVerifier = r.Form.Get("code_verifier")
		if r.Form.Get("code") != "code" || r.Form.Get("redirect_uri") != "https://app.example/callback" {
			t.Errorf("form = %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access","token_type":"Bearer","expires_in":60}`)
	}))
	if client.requestTimeout != defaultRequestTimeout {
		t.Fatalf("default request timeout = %s, want %s", client.requestTimeout, defaultRequestTimeout)
	}
	urlValue, key, err := client.BeginAuthorization(context.Background(), BeginOptions{Scopes: []string{"openid", "email"}})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(urlValue)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("state") == "" || query.Get("code_challenge_method") != "S256" || query.Get("redirect_uri") != "https://app.example/callback" {
		t.Fatalf("query = %v", query)
	}
	set, err := client.HandleCallback(context.Background(), key, Callback{State: query.Get("state"), Code: "code"})
	if err != nil {
		t.Fatal(err)
	}
	if set.AccessToken != "access" || set.TokenType != "Bearer" || receivedVerifier == "" {
		t.Fatalf("set = %#v verifier=%q", set, receivedVerifier)
	}
	if _, err := client.HandleCallback(context.Background(), key, Callback{State: query.Get("state"), Code: "code"}); !errors.Is(err, ErrState) {
		t.Fatalf("replay = %v", err)
	}
	if err := authn.ValidatePKCEVerifier(receivedVerifier); err != nil {
		t.Fatal(err)
	}
	wantChallenge, err := authn.PKCEChallengeS256(receivedVerifier)
	if err != nil {
		t.Fatal(err)
	}
	if query.Get("code_challenge") != wantChallenge {
		t.Fatalf("code_challenge = %q, want S256(verifier) %q", query.Get("code_challenge"), wantChallenge)
	}
}

func TestAuthorizationParamsCannotOverrideManagedParameters(t *testing.T) {
	client, _ := newOAuthFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	value, _, err := client.BeginAuthorization(context.Background(), BeginOptions{
		Scopes: []string{"openid"}, Nonce: "nonce",
		Params: map[string]string{
			"response_type":         "token",
			"client_id":             "attacker-client",
			"redirect_uri":          "https://attacker.example/callback",
			"state":                 "attacker-state",
			"code_challenge":        "attacker-challenge",
			"code_challenge_method": "plain",
			"nonce":                 "attacker-nonce",
			"scope":                 "admin",
			"prompt":                "login",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	managed := map[string]string{
		"response_type":         "code",
		"client_id":             "client",
		"redirect_uri":          "https://app.example/callback",
		"state":                 parsed.Query().Get("state"),
		"code_challenge":        parsed.Query().Get("code_challenge"),
		"code_challenge_method": "S256",
		"nonce":                 "nonce",
		"scope":                 "openid",
	}
	for key, want := range managed {
		if got := parsed.Query().Get(key); got != want {
			t.Fatalf("managed %s = %q, want %q", key, got, want)
		}
	}
	if got := parsed.Query().Get("prompt"); got != "login" {
		t.Fatalf("custom prompt = %q, want login", got)
	}
}

func TestBeginAuthorizationRejectsInvalidScopeTokens(t *testing.T) {
	client, _ := newOAuthFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, scope := range []string{"openid email", "open\tيد", "open\"id", "open" + string([]byte{0x7f}) + "id", strings.Repeat("x", 257)} {
		if _, _, err := client.BeginAuthorization(context.Background(), BeginOptions{Scopes: []string{scope}}); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("scope %q error = %v", scope, err)
		}
	}
}

func TestTwoIndependentAuthorizationServerFixtures(t *testing.T) {
	newServer := func(accessToken string) *Client {
		client, _ := newOAuthFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/token" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"`+accessToken+`","token_type":"Bearer"}`)
		}))
		return client
	}
	first := newServer("first-server-token")
	second := newServer("second-server-token")
	for _, fixture := range []struct {
		client *Client
		code   string
		want   string
	}{
		{client: first, code: "first-code", want: "first-server-token"},
		{client: second, code: "second-code", want: "second-server-token"},
	} {
		urlValue, key, err := fixture.client.BeginAuthorization(context.Background(), BeginOptions{})
		if err != nil {
			t.Fatal(err)
		}
		state := mustQuery(t, urlValue, "state")
		set, err := fixture.client.HandleCallback(context.Background(), key, Callback{State: state, Code: fixture.code})
		if err != nil {
			t.Fatal(err)
		}
		if set.AccessToken != fixture.want {
			t.Fatalf("access token for %q = %q, want %q", fixture.code, set.AccessToken, fixture.want)
		}
	}
}

func TestCallbackStateMismatchConsumesTransaction(t *testing.T) {
	client, _ := newOAuthFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("token endpoint called") }))
	_, key, err := client.BeginAuthorization(context.Background(), BeginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.HandleCallback(context.Background(), key, Callback{State: "wrong", Code: "code"}); !errors.Is(err, ErrState) {
		t.Fatalf("error = %v", err)
	}
	if _, err := client.HandleCallback(context.Background(), key, Callback{State: "wrong", Code: "code"}); !errors.Is(err, ErrState) {
		t.Fatalf("replay = %v", err)
	}
}

func TestCallbackRejectsMalformedStoredTransaction(t *testing.T) {
	store := &capturingStore{}
	client, err := NewClient(Config{
		AuthorizationEndpoint: "https://authorization.example/authorize",
		TokenEndpoint:         "https://authorization.example/token",
		ClientID:              "client", ClientSecret: "secret", RedirectURI: "https://app.example/callback",
	}, Options{StateStore: store, HTTPClient: &http.Client{Transport: roundTripHandler{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("malformed transaction reached token endpoint")
	})}}})
	if err != nil {
		t.Fatal(err)
	}
	urlValue, key, err := client.BeginAuthorization(context.Background(), BeginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	store.transaction.Verifier = "invalid"
	if _, err := client.HandleCallback(context.Background(), key, Callback{State: mustQuery(t, urlValue, "state"), Code: "code"}); !errors.Is(err, ErrState) {
		t.Fatalf("malformed transaction = %v", err)
	}
	store.transaction.Verifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~abcdefghijklmnopqrstuvwxyz"
	store.transaction.State = "not-canonical"
	if _, err := client.HandleCallback(context.Background(), key, Callback{State: "not-canonical", Code: "code"}); !errors.Is(err, ErrState) {
		t.Fatalf("non-canonical state transaction = %v", err)
	}
}

func TestTransactionValidatorRunsBeforeExchange(t *testing.T) {
	store := &capturingStore{}
	client, err := NewClient(Config{
		AuthorizationEndpoint: "https://authorization.example/authorize",
		TokenEndpoint:         "https://authorization.example/token",
		ClientID:              "client", ClientSecret: "secret", RedirectURI: "https://app.example/callback",
	}, Options{
		StateStore:           store,
		TransactionValidator: func(Transaction) error { return errors.New("validator secret=should-not-escape") },
		HTTPClient: &http.Client{Transport: roundTripHandler{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("transaction validator ran after token exchange")
		})}},
	})
	if err != nil {
		t.Fatal(err)
	}
	urlValue, key, err := client.BeginAuthorization(context.Background(), BeginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.HandleCallback(context.Background(), key, Callback{State: mustQuery(t, urlValue, "state"), Code: "code"}); !errors.Is(err, ErrState) || strings.Contains(err.Error(), "should-not-escape") {
		t.Fatalf("validator error = %v", err)
	}
}

func TestTransactionValidatorDoesNotRunBeforeStateCorrelation(t *testing.T) {
	called := false
	client, _ := newOAuthFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"access","token_type":"Bearer"}`)
	}))
	client.transactionValidator = func(Transaction) error {
		called = true
		return nil
	}
	_, key, err := client.BeginAuthorization(context.Background(), BeginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.HandleCallback(context.Background(), key, Callback{State: "wrong-state", Code: "code"}); !errors.Is(err, ErrState) {
		t.Fatalf("mismatch error = %v", err)
	}
	if called {
		t.Fatal("transaction validator ran before state correlation")
	}
}

func TestErrorCallbackRequiresState(t *testing.T) {
	client, _ := newOAuthFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("token endpoint called") }))
	urlValue, key, err := client.BeginAuthorization(context.Background(), BeginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	state := mustQuery(t, urlValue, "state")
	_, err = client.HandleCallback(context.Background(), key, Callback{State: state, Error: "access_denied", ErrorDescription: "user cancelled"})
	var authErr *AuthorizationError
	if !errors.As(err, &authErr) || authErr.Code != "access_denied" {
		t.Fatalf("error = %#v", err)
	}
}

func TestClientSecretPostAndTokenErrorAreBounded(t *testing.T) {
	var sawSecret bool
	client, _ := func() (*Client, *http.Client) {
		return newOAuthFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/token" {
				return
			}
			_ = r.ParseForm()
			sawSecret = r.Form.Get("client_secret") == "secret" && r.Header.Get("Authorization") == ""
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
		}))
	}()
	client.config.AuthMethod = AuthPost
	urlValue, key, err := client.BeginAuthorization(context.Background(), BeginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	state := mustQuery(t, urlValue, "state")
	if _, err := client.HandleCallback(context.Background(), key, Callback{State: state, Code: "code"}); !errors.Is(err, ErrToken) {
		t.Fatalf("token error = %v", err)
	}
	if !sawSecret {
		t.Fatal("client_secret_post was not used")
	}
}

func TestTokenStringFieldsHaveHardBounds(t *testing.T) {
	client, _ := newOAuthFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"`+strings.Repeat("x", maxTokenValueBytes+1)+`","token_type":"Bearer"}`)
	}))
	urlValue, key, err := client.BeginAuthorization(context.Background(), BeginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.HandleCallback(context.Background(), key, Callback{State: mustQuery(t, urlValue, "state"), Code: "code"}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversized token field error = %v", err)
	}
}

func TestRequestTimeoutCancelsTokenExchange(t *testing.T) {
	store, err := memory.NewStore[Transaction](memory.Options{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{
		AuthorizationEndpoint: "https://authorization.example/authorize",
		TokenEndpoint:         "https://authorization.example/token",
		ClientID:              "client", ClientSecret: "secret", RedirectURI: "https://app.example/callback",
	}, Options{
		StateStore: store, RequestTimeout: time.Millisecond,
		HTTPClient: &http.Client{Transport: contextBlockingTransport{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	urlValue, key, err := client.BeginAuthorization(context.Background(), BeginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.HandleCallback(context.Background(), key, Callback{State: mustQuery(t, urlValue, "state"), Code: "code"})
	if !errors.Is(err, ErrHTTP) {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestTransportErrorDoesNotExposeDetails(t *testing.T) {
	store, err := memory.NewStore[Transaction](memory.Options{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{
		AuthorizationEndpoint: "https://authorization.example/authorize",
		TokenEndpoint:         "https://authorization.example/token",
		ClientID:              "client", ClientSecret: "secret", RedirectURI: "https://app.example/callback",
	}, Options{StateStore: store, HTTPClient: &http.Client{Transport: secretErrorTransport{}}})
	if err != nil {
		t.Fatal(err)
	}
	urlValue, key, err := client.BeginAuthorization(context.Background(), BeginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.HandleCallback(context.Background(), key, Callback{State: mustQuery(t, urlValue, "state"), Code: "code"})
	if !errors.Is(err, ErrHTTP) || strings.Contains(err.Error(), "access-secret") {
		t.Fatalf("transport error = %v", err)
	}
}

func TestOversizedCallbackDoesNotConsumeState(t *testing.T) {
	client, _ := newOAuthFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"access","token_type":"Bearer"}`)
	}))
	urlValue, key, err := client.BeginAuthorization(context.Background(), BeginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	state := mustQuery(t, urlValue, "state")
	if _, err := client.HandleCallback(context.Background(), key, Callback{State: state, ErrorDescription: strings.Repeat("x", 2049)}); !errors.Is(err, ErrInvalidCallback) {
		t.Fatalf("oversized callback error = %v", err)
	}
	if _, err := client.HandleCallback(context.Background(), key, Callback{State: state, Code: "code"}); err != nil {
		t.Fatalf("state was consumed by oversized callback: %v", err)
	}
}

func TestTokenResponseRejectsDuplicateMembers(t *testing.T) {
	if _, err := parseTokenSet([]byte(`{"access_token":"a","access_token":"b","token_type":"Bearer"}`), 4096); !errors.Is(err, ErrMalformed) {
		t.Fatalf("error = %v", err)
	}
}

func TestTokenResponseRejectsInvalidExpiry(t *testing.T) {
	for _, response := range []string{
		`{"access_token":"a","token_type":"Bearer","expires_in":-1}`,
		`{"access_token":"a","token_type":"Bearer","expires_in":"3600"}`,
	} {
		if _, err := parseTokenSet([]byte(response), 4096); !errors.Is(err, ErrMalformed) {
			t.Fatalf("response %s error = %v", response, err)
		}
	}
}

func TestBeginAuthorizationBoundsCorrelationInputs(t *testing.T) {
	client, _ := newOAuthFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if _, _, err := client.BeginAuthorization(context.Background(), BeginOptions{Nonce: string(make([]byte, 257))}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("nonce bound error = %v", err)
	}
}

func TestRejectsRedirectAndInvalidEndpoint(t *testing.T) {
	store, _ := memory.NewStore[Transaction](memory.Options{})
	validated := 0
	client, err := NewClient(Config{AuthorizationEndpoint: "https://example.com/a", TokenEndpoint: "https://example.com/t", ClientID: "c", ClientSecret: "s", RedirectURI: "https://app.example/cb", EndpointValidator: func(endpoint *url.URL) error {
		validated++
		if endpoint.Hostname() != "example.com" {
			return errors.New("unexpected endpoint secret")
		}
		endpoint.Host = "attacker.example"
		endpoint.Path = "/rewritten"
		return nil
	}}, Options{StateStore: store})
	if err != nil || validated != 2 {
		t.Fatalf("endpoint validator err=%v calls=%d", err, validated)
	}
	if client.config.AuthorizationEndpoint != "https://example.com/a" || client.config.TokenEndpoint != "https://example.com/t" {
		t.Fatalf("validator rewrote configured endpoints: auth=%q token=%q", client.config.AuthorizationEndpoint, client.config.TokenEndpoint)
	}
	if _, err := NewClient(Config{AuthorizationEndpoint: "https://example.com/a", TokenEndpoint: "https://example.com/t", ClientID: "c", ClientSecret: "s", RedirectURI: "https://app.example/cb", EndpointValidator: func(*url.URL) error {
		return errors.New("endpoint trust secret=hidden")
	}}, Options{StateStore: store}); !errors.Is(err, ErrInvalidConfig) || strings.Contains(err.Error(), "hidden") {
		t.Fatalf("endpoint validator sanitization = %v", err)
	}
	if _, err := NewClient(Config{AuthorizationEndpoint: "http://example.com/a", TokenEndpoint: "https://example.com/t", ClientID: "c", RedirectURI: "https://app.example/cb"}, Options{StateStore: store}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("endpoint = %v", err)
	}
	if _, err := NewClient(Config{AuthorizationEndpoint: "https://example.com/a", TokenEndpoint: "https://example.com/t", ClientID: "c", ClientSecret: "s", RedirectURI: "http://127.0.0.1/cb", AllowLoopbackHTTP: true}, Options{StateStore: store}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(Config{AuthorizationEndpoint: "https://example.com/a", TokenEndpoint: "https://example.com/t", ClientID: "c", ClientSecret: "s", RedirectURI: "http://127.evil/cb", AllowLoopbackHTTP: true}, Options{StateStore: store}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("non-loopback redirect = %v", err)
	}
	if _, err := NewClient(Config{AuthorizationEndpoint: "https://:443/a", TokenEndpoint: "https://example.com/t", ClientID: "c", ClientSecret: "s", RedirectURI: "https://app.example/cb"}, Options{StateStore: store}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty-host endpoint = %v", err)
	}
	if _, err := NewClient(Config{AuthorizationEndpoint: "https://example.com/a", TokenEndpoint: "https://example.com/t", ClientID: string(make([]byte, maxClientValueBytes+1)), ClientSecret: "s", RedirectURI: "https://app.example/cb"}, Options{StateStore: store}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("client id bound = %v", err)
	}
	if _, err := NewClient(Config{AuthorizationEndpoint: "https://example.com/a", TokenEndpoint: "https://example.com/t", ClientID: "c", ClientSecret: string(make([]byte, maxClientValueBytes+1)), RedirectURI: "https://app.example/cb"}, Options{StateStore: store}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("client secret bound = %v", err)
	}
}

func TestPublicClientWithoutSecretIsUnsupported(t *testing.T) {
	store, err := memory.NewStore[Transaction](memory.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewClient(Config{
		AuthorizationEndpoint: "https://example.com/a", TokenEndpoint: "https://example.com/t",
		ClientID: "public", RedirectURI: "https://app.example/callback",
	}, Options{StateStore: store})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("public client error = %v", err)
	}
}

func TestExpiryWithInjectedClock(t *testing.T) {
	now := time.Unix(100, 0)
	store, err := memory.NewStore[Transaction](memory.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{AuthorizationEndpoint: "https://authorization.example/a", TokenEndpoint: "https://authorization.example/t", ClientID: "c", ClientSecret: "s", RedirectURI: "https://app.example/cb"}, Options{StateStore: store, Clock: func() time.Time { return now }, StateTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	urlValue, key, err := client.BeginAuthorization(context.Background(), BeginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := client.HandleCallback(context.Background(), key, Callback{State: mustQuery(t, urlValue, "state"), Code: "x"}); !errors.Is(err, ErrState) {
		t.Fatalf("expired state = %v", err)
	}
}

func mustQuery(t *testing.T, raw, key string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query().Get(key)
}
