package oauthprofile

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/contrib/internal/authn"
	"github.com/shibukawa/popcornweb/contrib/oauth"
)

var (
	// ErrUnknownProvider names a provider this package has no definition for.
	ErrUnknownProvider = errors.New("oauthprofile: unknown provider")
	// ErrProfile reports a user endpoint that answered with something this
	// package will not read an account out of. It carries no provider text:
	// an error string is a place a response body leaks from.
	ErrProfile = errors.New("oauthprofile: invalid profile response")
	// ErrHTTP reports a failed request. Transport detail is deliberately not
	// wrapped, because a custom transport puts URLs and headers in its errors.
	ErrHTTP = errors.New("oauthprofile: HTTP request failed")
	// ErrLimitExceeded reports a response larger than the configured bound.
	ErrLimitExceeded = errors.New("oauthprofile: response limit exceeded")
	// ErrInvalidToken rejects an access token that cannot go in a header.
	ErrInvalidToken = errors.New("oauthprofile: invalid access token")
)

// Provider names are the values a deployment writes into configuration.
const (
	// ProviderX is X, formerly Twitter.
	ProviderX = "x"
)

const (
	defaultMaxResponseBytes = 64 << 10
	defaultRequestTimeout   = 30 * time.Second
	maxAccessTokenBytes     = 16 << 10
	// maxProfileValueBytes bounds one copied display value. A provider is free
	// to answer with a name of any length; what is stored in a session is not.
	maxProfileValueBytes = 512
)

// A Provider is one authorization server this package knows the shape of.
//
// The endpoints are constants rather than something discovered: an OAuth
// provider that issues no ID Token generally publishes no metadata document
// either, so there is nothing to fetch and nothing whose freshness could
// surprise a running deployment.
type Provider struct {
	// Name is the configured provider name.
	Name string
	// Issuer namespaces this provider's accounts. It is not a protocol value —
	// nothing verifies it — but an account link needs to say which provider a
	// subject came from, and two providers may both count from 1.
	Issuer string
	// AuthorizationEndpoint and TokenEndpoint are what an oauth.Client is
	// configured with.
	AuthorizationEndpoint string
	TokenEndpoint         string
	// ProfileEndpoint answers who the access token belongs to.
	ProfileEndpoint string
	// Scopes are what this provider needs for ProfileEndpoint to answer at
	// all. A deployment may ask for more; asking for fewer breaks the login.
	Scopes []string
	// AuthMethod is how the token request authenticates this client.
	AuthMethod string
	// ProfileRoot is the JSON Pointer to the object inside the response that
	// holds the account, or empty when the response is that object. X wraps
	// every v2 payload in "data", so its root is "/data".
	ProfileRoot string
	// Profile maps each claim this package reports to the member of that object
	// it is read from, so a provider is described rather than coded.
	//
	// The mapping exists because every provider spells the same handful of
	// facts differently, and one vocabulary is what lets admission, the account
	// resolver, and a session slot be written once. "sub" is required and is
	// the provider's own stable identifier.
	Profile map[string]string
}

// Claims lists the claim names this provider reports, in a stable order.
//
// It exists so that a configured claim name can be checked before anybody logs
// in, and so that the refusal can say what the alternatives are. A deployment
// that copied a name out of the provider's own API reference — X's "username"
// rather than "preferred_username" — would otherwise see every login refused
// with nothing to point at.
func (p Provider) Claims() []string {
	names := make([]string, 0, len(p.Profile))
	for name := range p.Profile {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Profile is the account a provider reports for an access token. Claims is the
// flat, provider-neutral view admission and account linking read; the named
// fields are the ones a session renders.
type Profile struct {
	// Subject is the provider's own stable account identifier.
	Subject string
	// Username is the handle, where the provider has one. It is display and
	// lookup material, never an account link: a handle can be renamed and,
	// on X, released and claimed by somebody else.
	Username string
	// Name is the display name.
	Name string
	// AvatarURL is the profile image, when the provider returned one.
	AvatarURL string
	// Claims is the decoded profile as a claim set, with the provider's
	// identifier under "sub".
	Claims map[string]json.RawMessage
}

// Options bounds one profile request.
type Options struct {
	HTTPClient *http.Client
	// MaxResponseBytes defaults to 64 KiB.
	MaxResponseBytes int
	// RequestTimeout defaults to 30 seconds.
	RequestTimeout time.Duration
}

// Lookup returns the built-in definition of a provider name.
func Lookup(name string) (Provider, bool) {
	provider, ok := providers[name]
	if !ok {
		return Provider{}, false
	}
	provider.Scopes = append([]string(nil), provider.Scopes...)
	provider.Profile = maps.Clone(provider.Profile)
	return provider, true
}

// Names lists every provider name this package defines, so a configuration
// error can say what the alternatives are.
func Names() []string { return append([]string(nil), providerNames...) }

var providers = map[string]Provider{ProviderX: x}

// providerNames is fixed rather than ranged over a map, so the order an error
// message lists is the same on every run.
var providerNames = []string{ProviderX}

// Fetch reads the account behind an access token.
//
// It sends the token to the provider's own user endpoint and to nowhere else,
// and it returns no response text on failure.
func Fetch(ctx context.Context, provider Provider, accessToken string, options Options) (Profile, error) {
	if ctx == nil || provider.ProfileEndpoint == "" || provider.Profile["sub"] == "" {
		return Profile{}, ErrUnknownProvider
	}
	if !validBearerToken(accessToken) || len(accessToken) > maxAccessTokenBytes {
		return Profile{}, ErrInvalidToken
	}
	maxResponse := options.MaxResponseBytes
	if maxResponse <= 0 {
		maxResponse = defaultMaxResponseBytes
	}
	timeout := options.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	} else {
		copied := *client
		client = &copied
	}
	// A profile request carries a bearer token, so it must never follow the
	// provider's redirect to somewhere else holding it.
	client.CheckRedirect = authn.RejectRedirect
	client = authn.EnforceDeadlines(client)

	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, provider.ProfileEndpoint, nil)
	if err != nil {
		return Profile{}, ErrHTTP
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := client.Do(request)
	if err != nil {
		return Profile{}, ErrHTTP
	}
	defer response.Body.Close()
	body, err := authn.ReadBounded(response.Body, int64(maxResponse))
	if err != nil {
		if errors.Is(err, authn.ErrLimitExceeded) {
			return Profile{}, ErrLimitExceeded
		}
		return Profile{}, ErrHTTP
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Profile{}, ErrProfile
	}
	if err := authn.ValidateJSON(body, authn.JSONOptions{MaxBytes: maxResponse, MaxDepth: 8, MaxMembers: 128}); err != nil {
		return Profile{}, ErrProfile
	}
	claims, err := provider.claims(body)
	if err != nil {
		return Profile{}, err
	}
	return profileFrom(claims)
}

// claims reads the account object out of one provider's response and renames
// its members onto the shared vocabulary.
//
// A member that is absent, empty, or of a shape that is not an identifier is
// left out rather than carried as an empty value, so a claim rule cannot match
// on something the provider did not say. A number is read as its literal text,
// because several providers issue numeric account identifiers, and a fraction
// or an exponent is refused rather than normalized: two systems must not be
// able to disagree about what the value is.
func (p Provider) claims(body []byte) (map[string]json.RawMessage, error) {
	object, err := objectAt(body, p.ProfileRoot)
	if err != nil {
		return nil, err
	}
	claims := make(map[string]json.RawMessage, len(p.Profile))
	for claim, member := range p.Profile {
		raw, ok := object[member]
		if !ok {
			continue
		}
		var text string
		switch {
		case json.Unmarshal(raw, &text) == nil:
			if text == "" {
				continue
			}
		case isIntegerLiteral(raw):
			text = string(raw)
		default:
			continue
		}
		encoded, err := json.Marshal(text)
		if err != nil {
			return nil, ErrProfile
		}
		claims[claim] = encoded
	}
	return claims, nil
}

// objectAt resolves the profile root, which is a JSON Pointer of plain member
// names. It is not a general pointer implementation: a provider envelope is one
// or two members deep, and every definition here is written in this file.
func objectAt(body []byte, pointer string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil || object == nil {
		return nil, ErrProfile
	}
	if pointer == "" {
		return object, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, ErrProfile
	}
	for _, name := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		raw, ok := object[name]
		if !ok {
			return nil, ErrProfile
		}
		object = nil
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return nil, ErrProfile
		}
	}
	return object, nil
}

// isIntegerLiteral reports whether raw is a JSON number with no fraction and no
// exponent, whose text is therefore an unambiguous identifier.
func isIntegerLiteral(raw []byte) bool {
	if len(raw) == 0 || len(raw) > 64 {
		return false
	}
	index := 0
	if raw[0] == '-' {
		index++
	}
	if index == len(raw) {
		return false
	}
	for ; index < len(raw); index++ {
		if raw[index] < '0' || raw[index] > '9' {
			return false
		}
	}
	return true
}

// profileFrom copies the display fields out of a decoded claim set and
// enforces the one invariant every provider owes: a non-empty subject.
func profileFrom(claims map[string]json.RawMessage) (Profile, error) {
	profile := Profile{Claims: claims}
	subject, ok := boundedString(claims, "sub")
	if !ok || subject == "" {
		return Profile{}, ErrProfile
	}
	profile.Subject = subject
	profile.Username, _ = boundedString(claims, "preferred_username")
	profile.Name, _ = boundedString(claims, "name")
	profile.AvatarURL, _ = boundedString(claims, "picture")
	return profile, nil
}

// boundedString reads one claim as a display string, refusing a value too long
// to belong in a session and any shape that is not a string.
func boundedString(claims map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := claims[name]
	if !ok {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || len(value) > maxProfileValueBytes {
		return "", false
	}
	return value, true
}

// OAuthConfig fills in the endpoint half of an oauth.Config, leaving the
// deployment's own credentials and redirect URI to the caller.
func (p Provider) OAuthConfig(clientID, clientSecret, redirectURI string) oauth.Config {
	return oauth.Config{
		AuthorizationEndpoint: p.AuthorizationEndpoint,
		TokenEndpoint:         p.TokenEndpoint,
		ClientID:              clientID,
		ClientSecret:          clientSecret,
		RedirectURI:           redirectURI,
		AuthMethod:            p.AuthMethod,
	}
}

// validBearerToken rejects the bytes an Authorization header cannot carry.
// OAuth access tokens are opaque, so this is a transport rule rather than a
// format one.
func validBearerToken(value string) bool {
	if value == "" {
		return false
	}
	for index := range len(value) {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}
