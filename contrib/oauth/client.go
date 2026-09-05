package oauth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/authstate"
	"github.com/shibukawa/popcornweb/contrib/internal/authn"
)

func NewClient(config Config, options Options) (*Client, error) {
	if config.AuthorizationEndpoint == "" || config.TokenEndpoint == "" ||
		config.ClientID == "" || len(config.ClientID) > maxClientValueBytes ||
		len(config.ClientSecret) > maxClientValueBytes || config.RedirectURI == "" ||
		(config.AuthMethod != "" && config.AuthMethod != AuthBasic && config.AuthMethod != AuthPost) {
		return nil, ErrInvalidConfig
	}
	authorizationEndpoint, err := authn.ParseEndpoint(config.AuthorizationEndpoint, config.AllowLoopbackHTTP)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	tokenEndpoint, err := authn.ParseEndpoint(config.TokenEndpoint, config.AllowLoopbackHTTP)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	if config.EndpointValidator != nil {
		authorizationCandidate := *authorizationEndpoint
		if err := config.EndpointValidator(&authorizationCandidate); err != nil {
			return nil, ErrInvalidConfig
		}
		tokenCandidate := *tokenEndpoint
		if err := config.EndpointValidator(&tokenCandidate); err != nil {
			return nil, ErrInvalidConfig
		}
	}
	if !validRedirectURI(config.RedirectURI, config.AllowLoopbackHTTP) {
		return nil, ErrInvalidConfig
	}
	if config.AuthMethod == "" {
		config.AuthMethod = AuthBasic
	}
	if config.ClientSecret == "" {
		return nil, ErrInvalidConfig
	}
	if options.StateTTL < 0 || options.StateTTL > maxStateTTL ||
		options.MaxResponseBytes < 0 || options.MaxResponseBytes > maxMaxResponseBytes ||
		options.RequestTimeout < 0 || options.RequestTimeout > maxRequestTimeout {
		return nil, ErrInvalidOptions
	}
	if options.StateStore == nil {
		return nil, ErrInvalidOptions
	}
	clock := options.Clock
	if clock == nil {
		clock = time.Now
	}
	random := options.Random
	if random == nil {
		random = rand.Reader
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	} else {
		copy := *client
		client = &copy
	}
	// Token exchange must never follow an attacker-selected redirect.
	client.CheckRedirect = authn.RejectRedirect
	// RequestTimeout is applied as a context deadline, and TinyGo's net/http
	// ignores those. The exchange runs on a request handler and talks to a host
	// this application does not control, so the deadline has to be real there
	// too. A caller that supplied an already wrapped client — which contrib/oidc
	// does — is handed it back unchanged.
	client = authn.EnforceDeadlines(client)
	maxResponse := options.MaxResponseBytes
	if maxResponse == 0 {
		maxResponse = defaultMaxResponseBytes
	}
	stateTTL := options.StateTTL
	if stateTTL == 0 {
		stateTTL = defaultStateTTL
	}
	requestTimeout := options.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = defaultRequestTimeout
	}
	return &Client{
		config: config, clock: clock, stateTTL: stateTTL, maxResponse: maxResponse,
		requestTimeout: requestTimeout, random: random, httpClient: client,
		store: options.StateStore, transactionValidator: options.TransactionValidator,
	}, nil
}

func validRedirectURI(raw string, allowLoopbackHTTP bool) bool {
	if len(raw) == 0 || len(raw) > 4096 {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" || !allowLoopbackHTTP {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// BeginAuthorization creates one-time state and PKCE material, stores it,
// and returns the browser authorization URL and transaction key.
func (c *Client) BeginAuthorization(ctx context.Context, options BeginOptions) (string, string, error) {
	if ctx == nil || c == nil {
		return "", "", ErrInvalidOptions
	}
	if len(options.Scopes) > 64 || len(options.Params) > 64 {
		return "", "", ErrInvalidOptions
	}
	if len(options.Nonce) > 256 {
		return "", "", ErrInvalidOptions
	}
	for _, scope := range options.Scopes {
		if !validScope(scope) {
			return "", "", ErrInvalidOptions
		}
	}
	for key, value := range options.Params {
		if len(key) == 0 || len(key) > 256 || len(value) > 2048 {
			return "", "", ErrInvalidOptions
		}
	}
	state, err := authn.GenerateSecret(c.random, 32)
	if err != nil {
		return "", "", err
	}
	verifier, err := authn.GeneratePKCEVerifier(c.random)
	if err != nil {
		return "", "", err
	}
	challenge, err := authn.PKCEChallengeS256(verifier)
	if err != nil {
		return "", "", err
	}
	expiresAt := c.clock().Add(c.stateTTL)
	transaction := Transaction{State: state, Verifier: verifier, Nonce: options.Nonce, RedirectURI: c.config.RedirectURI, ExpiresAt: expiresAt}
	for range 3 {
		key, keyErr := authn.GenerateSecret(c.random, 32)
		if keyErr != nil {
			return "", "", keyErr
		}
		if keyErr = c.store.Put(ctx, key, transaction, expiresAt); keyErr == nil {
			return c.authorizationURL(options, state, challenge), key, nil
		} else if !errors.Is(keyErr, authstate.ErrAlreadyExists) {
			return "", "", keyErr
		}
	}
	return "", "", authstate.ErrAlreadyExists
}

// validScope accepts the RFC 6749 scope-token grammar. In particular, a
// scope item cannot contain whitespace, quotes, backslash, or control bytes;
// otherwise one caller-provided item could silently become multiple scopes.
func validScope(scope string) bool {
	if len(scope) == 0 || len(scope) > 256 {
		return false
	}
	for _, char := range []byte(scope) {
		if char == 0x21 || (char >= 0x23 && char <= 0x5b) || (char >= 0x5d && char <= 0x7e) {
			continue
		}
		return false
	}
	return true
}

func (c *Client) authorizationURL(options BeginOptions, state, challenge string) string {
	endpoint, _ := url.Parse(c.config.AuthorizationEndpoint)
	query := endpoint.Query()
	query.Set("response_type", "code")
	query.Set("client_id", c.config.ClientID)
	query.Set("redirect_uri", c.config.RedirectURI)
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	if options.Nonce != "" {
		query.Set("nonce", options.Nonce)
	}
	if len(options.Scopes) > 0 {
		query.Set("scope", strings.Join(options.Scopes, " "))
	}
	for key, value := range options.Params {
		if key != "response_type" && key != "client_id" && key != "redirect_uri" && key != "state" && key != "code_challenge" && key != "code_challenge_method" && key != "nonce" && key != "scope" {
			query.Set(key, value)
		}
	}
	endpoint.RawQuery = query.Encode()
	return endpoint.String()
}

// HandleCallback validates and consumes the transaction before exchanging a
// code. Both successful and error callbacks must present the expected state.
func (c *Client) HandleCallback(ctx context.Context, key string, callback Callback) (TokenSet, error) {
	set, _, err := c.HandleCallbackWithTransaction(ctx, key, callback)
	return set, err
}

// HandleCallbackWithTransaction is the callback form for protocol wrappers
// that need the atomically consumed correlation record (for example OIDC's
// nonce validation).
func (c *Client) HandleCallbackWithTransaction(ctx context.Context, key string, callback Callback) (TokenSet, Transaction, error) {
	var zero TokenSet
	if ctx == nil || c == nil || key == "" || callback.State == "" || len(callback.State) > 256 || len(callback.Code) > 8192 || len(callback.Error) > 256 || len(callback.ErrorDescription) > 2048 || len(callback.ErrorURI) > 2048 {
		return zero, Transaction{}, ErrInvalidCallback
	}
	transaction, err := c.store.Take(ctx, key)
	if err != nil {
		return zero, Transaction{}, ErrState
	}
	if err := c.validateTransaction(transaction); err != nil {
		return zero, Transaction{}, ErrState
	}
	if !authn.EqualSecret(transaction.State, callback.State) {
		return zero, Transaction{}, ErrState
	}
	if c.transactionValidator != nil {
		if err := c.transactionValidator(transaction); err != nil {
			// Validators may be application supplied; never expose their error
			// strings because they can accidentally contain transaction secrets.
			return zero, Transaction{}, ErrState
		}
	}
	if callback.Error != "" {
		return zero, transaction, &AuthorizationError{Code: callback.Error, Description: callback.ErrorDescription, URI: callback.ErrorURI}
	}
	if callback.Code == "" {
		return zero, transaction, ErrInvalidCallback
	}
	set, err := c.exchange(ctx, transaction, callback.Code)
	return set, transaction, err
}

func (c *Client) validateTransaction(transaction Transaction) error {
	if c == nil || transaction.State == "" || transaction.RedirectURI != c.config.RedirectURI || transaction.ExpiresAt.IsZero() {
		return ErrState
	}
	state, err := authn.DecodeBase64URL(transaction.State, 256, 64)
	if err != nil || len(state) < 16 {
		return ErrState
	}
	if err := authn.ValidatePKCEVerifier(transaction.Verifier); err != nil {
		return ErrState
	}
	if err := authn.RequireUnexpired(c.clock(), transaction.ExpiresAt); err != nil {
		return ErrState
	}
	return nil
}

func (c *Client) exchange(ctx context.Context, transaction Transaction, code string) (TokenSet, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", transaction.RedirectURI)
	form.Set("client_id", c.config.ClientID)
	form.Set("code_verifier", transaction.Verifier)
	requestCtx := ctx
	var cancel context.CancelFunc
	if c.requestTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}
	if c.config.AuthMethod == AuthBasic {
		// Credentials are sent using HTTP Basic, never as a query parameter.
	} else {
		form.Set("client_secret", c.config.ClientSecret)
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.config.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, ErrHTTP
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if c.config.AuthMethod == AuthBasic {
		req.SetBasicAuth(c.config.ClientID, c.config.ClientSecret)
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		// Do not expose transport details: custom transports may include URL,
		// request fields, or other sensitive values in their error strings.
		return TokenSet{}, ErrHTTP
	}
	defer response.Body.Close()
	body, err := authn.ReadBounded(response.Body, int64(c.maxResponse))
	if err != nil {
		if errors.Is(err, authn.ErrLimitExceeded) {
			return TokenSet{}, ErrLimitExceeded
		}
		return TokenSet{}, ErrHTTP
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return TokenSet{}, &TokenError{StatusCode: response.StatusCode}
	}
	set, err := parseTokenSet(body, c.maxResponse)
	if err != nil {
		return TokenSet{}, err
	}
	return set, nil
}

// AuthorizationError is safe to inspect and intentionally does not include
// authorization codes or tokens.
type AuthorizationError struct{ Code, Description, URI string }

func (e *AuthorizationError) Error() string { return ErrAuthorization.Error() }
func (e *AuthorizationError) Unwrap() error { return ErrAuthorization }

type TokenError struct{ StatusCode int }

func (e *TokenError) Error() string { return ErrToken.Error() }
func (e *TokenError) Unwrap() error { return ErrToken }

func parseTokenSet(data []byte, maxBytes int) (TokenSet, error) {
	if err := authn.ValidateJSON(data, authn.JSONOptions{MaxBytes: maxBytes, MaxDepth: 8, MaxMembers: 128}); err != nil {
		if errors.Is(err, authn.ErrLimitExceeded) {
			return TokenSet{}, ErrLimitExceeded
		}
		return TokenSet{}, ErrMalformed
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return TokenSet{}, ErrMalformed
	}
	var set TokenSet
	set.Raw = make(map[string]json.RawMessage)
	for key, value := range raw {
		switch key {
		case "access_token", "token_type", "refresh_token", "scope", "expires_in":
		default:
			set.Raw[key] = append(json.RawMessage(nil), value...)
		}
	}
	if err := decodeRequiredString(raw, "access_token", &set.AccessToken); err != nil {
		return TokenSet{}, err
	}
	if err := decodeRequiredString(raw, "token_type", &set.TokenType); err != nil {
		return TokenSet{}, err
	}
	if err := decodeOptionalString(raw, "refresh_token", &set.RefreshToken); err != nil {
		return TokenSet{}, err
	}
	if err := decodeOptionalString(raw, "scope", &set.Scope); err != nil {
		return TokenSet{}, err
	}
	if value, ok := raw["expires_in"]; ok {
		var expires int64
		if err := json.Unmarshal(value, &expires); err != nil || expires < 0 {
			return TokenSet{}, ErrMalformed
		}
		set.ExpiresIn = &expires
	}
	return set, nil
}

func decodeRequiredString(raw map[string]json.RawMessage, key string, target *string) error {
	if err := decodeOptionalString(raw, key, target); err != nil {
		return err
	}
	if *target == "" {
		return ErrMalformed
	}
	return nil
}
func decodeOptionalString(raw map[string]json.RawMessage, key string, target *string) error {
	if value, ok := raw[key]; ok {
		if err := json.Unmarshal(value, target); err != nil {
			return ErrMalformed
		}
		if len(*target) > maxTokenValueBytes {
			return ErrLimitExceeded
		}
	}
	return nil
}
