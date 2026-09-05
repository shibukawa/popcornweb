package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/shibukawa/popcornweb/contrib/jwt"
	"github.com/shibukawa/popcornweb/contrib/oauthprofile"
)

var (
	// ErrAccessDenied rejects a verified identity that admission or account
	// state does not admit. It is answered with one enumeration-safe response.
	ErrAccessDenied = errors.New("auth: access denied")
	// ErrUnknownIdentity is returned by an account resolver that found no local
	// account for a verified identity.
	ErrUnknownIdentity = errors.New("auth: unknown identity")
)

// Identity is a verified external identity. Every field comes from a verified
// ID Token; the mutable display claims are copied for rendering and never
// identify the account link.
type Identity struct {
	Issuer  string
	Subject string
	// KeyClaim names the claim a deployment identifies accounts by, and Key is
	// its verified value. They default to the "sub" claim and the subject.
	//
	// A directory that issues its own stable identifier, such as an employee
	// number, usually knows that value before anyone logs in, which the
	// subject never is. auth.oidc.identity_claim selects it.
	KeyClaim string
	Key      string
	Claims   Claims
	// TokenID is the verified jti of a bearer access token, and empty for a
	// browser login. It names one token, which is what the token form of
	// policy:token-revocation withdraws.
	TokenID string
	// IssuedAt and ExpiresAt are the verified iat and exp of a bearer access
	// token, and zero for a browser login.
	//
	// IssuedAt is what a subject-form revocation compares against: a token
	// minted after the identity was revoked is admitted, so revoking an
	// identity ends its outstanding tokens without ending the identity.
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// ClaimSubject is the default identity claim and the only one OpenID Connect
// guarantees is stable and unique for an issuer.
const ClaimSubject = "sub"

// maxLookupValueBytes bounds one compared claim value, so a provider cannot
// turn an account lookup into an unbounded query parameter.
const maxLookupValueBytes = 512

// Claims is the read-only verified claim set of an ID Token.
type Claims struct {
	raw map[string]json.RawMessage
}

// String returns a string claim by name.
func (c Claims) String(name string) (string, bool) {
	value, ok := c.raw[name]
	if !ok {
		return "", false
	}
	var result string
	if json.Unmarshal(value, &result) != nil {
		return "", false
	}
	return result, true
}

// Raw returns the undecoded JSON of one claim.
func (c Claims) Raw(name string) (json.RawMessage, bool) {
	value, ok := c.raw[name]
	return value, ok
}

// claimLookupValue reads one claim as an account lookup value. The subject is
// taken from the verified sub claim rather than from the copied claim map.
//
// A string is used as it is. An integer is used as its literal text, because a
// directory that issues numeric employee identifiers often emits them as JSON
// numbers. Every other shape, including a fractional number, is refused rather
// than normalized, so two systems cannot disagree about what the value is.
func claimLookupValue(claim string, identity Identity) (string, bool) {
	if claim == "" {
		return "", false
	}
	value := ""
	switch claim {
	case ClaimSubject:
		value = identity.Subject
	default:
		raw, ok := identity.Claims.raw[claim]
		if !ok {
			return "", false
		}
		var text string
		switch {
		case json.Unmarshal(raw, &text) == nil:
			value = text
		case isIntegerLiteral(raw):
			value = string(raw)
		default:
			return "", false
		}
	}
	if value == "" || len(value) > maxLookupValueBytes {
		return "", false
	}
	return value, true
}

// isIntegerLiteral reports whether raw is a JSON number without a fraction or
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

// Account is the local account a verified identity resolved to. Applications
// own account storage; this is only what the session needs.
type Account struct {
	// ID is the stable opaque application identifier. It must not be an email
	// address.
	ID          string
	DisplayName string
	Email       string
	// Suspended blocks session creation for an account that exists but may not
	// log in.
	Suspended bool
}

// AccountResolver resolves or provisions the local account of a verified
// identity. Returning ErrUnknownIdentity means no local account exists, which
// admission then interprets according to policy.
//
// provision reports whether policy permits creating an account during this
// login.
type AccountResolver func(ctx context.Context, identity Identity, provision bool) (Account, error)

var resolverState struct {
	sync.RWMutex
	resolve AccountResolver
}

// SetAccountResolver installs the application account resolver. Call it from
// main before pw.Run. Without a resolver the framework derives a deterministic
// account identifier from the verified issuer and subject, which suits a
// deployment that keeps no local account table.
func SetAccountResolver(resolve AccountResolver) {
	resolverState.Lock()
	defer resolverState.Unlock()
	resolverState.resolve = resolve
}

// AccountLookup returns the account of a stable identifier. A passkey login
// resolves a credential to an account ID, which AccountResolver cannot answer
// because it starts from a verified external identity instead.
type AccountLookup func(ctx context.Context, accountID string) (Account, error)

var lookupState struct {
	sync.RWMutex
	lookup AccountLookup
}

// SetAccountLookup installs the application account lookup. Without one a
// passkey session carries the account identifier alone, which is enough to
// authenticate and authorize but shows the user no name.
func SetAccountLookup(lookup AccountLookup) {
	lookupState.Lock()
	defer lookupState.Unlock()
	lookupState.lookup = lookup
}

func lookupAccount(ctx context.Context, accountID string) (Account, error) {
	lookupState.RLock()
	lookup := lookupState.lookup
	lookupState.RUnlock()
	if lookup == nil {
		return Account{ID: accountID}, nil
	}
	return lookup(ctx, accountID)
}

// installedAccountResolver returns the application's resolver, or nil when it
// installed none. It is distinct from accountResolver, which substitutes the
// derived account: a caller that needs to know whether the application answered
// cannot use a function that always answers.
func installedAccountResolver() AccountResolver {
	resolverState.RLock()
	defer resolverState.RUnlock()
	return resolverState.resolve
}

func accountResolver() AccountResolver {
	resolverState.RLock()
	defer resolverState.RUnlock()
	if resolverState.resolve != nil {
		return resolverState.resolve
	}
	return derivedAccount
}

// derivedAccount is the default resolver. It never stores anything, so every
// verified identity resolves to the same stable identifier across restarts.
func derivedAccount(_ context.Context, identity Identity, _ bool) (Account, error) {
	sum := sha256.Sum256([]byte(identity.Issuer + "\x00" + identity.KeyClaim + "\x00" + identity.Key))
	account := Account{ID: base64.RawURLEncoding.EncodeToString(sum[:16])}
	if name, ok := identity.Claims.String("name"); ok {
		account.DisplayName = name
	} else if name, ok := identity.Claims.String("preferred_username"); ok {
		account.DisplayName = name
	}
	if email, ok := identity.Claims.String("email"); ok {
		account.Email = email
	}
	return account, nil
}

// SessionData is the payload stored in the login session. It holds no token
// body, no provider secret, and no raw cookie material.
//
// It is one registered session slot, stored exactly like an application's own:
// the session package holds the bytes and this package holds their meaning.
// Everything about how well and how recently the subject was proved lives here
// rather than on the session record, because a record holding a shopping cart
// for an anonymous browser has no authentication time to report.
type SessionData struct {
	AccountID string `json:"account_id"`
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"`
	// AuthenticatedAt is when the current authentication strength was
	// established. A rotation after a login or a re-proof refreshes it.
	AuthenticatedAt time.Time `json:"authenticated_at"`
	// Method is the unordered label of what proved this session, such as oidc
	// or passkey. Nothing ranks one above another: the framework cannot order
	// the methods it mounts, so an ordering would be a deployment claim.
	Method string `json:"method,omitempty"`
	// KeyClaim and Key record which verified claim identified the account and
	// its value, so a handler can show or audit the link without repeating the
	// configuration.
	KeyClaim    string `json:"key_claim,omitempty"`
	Key         string `json:"key,omitempty"`
	DisplayName string `json:"name,omitempty"`
	Email       string `json:"email,omitempty"`
	// Provider is the OAuth login provider that authenticated this session,
	// such as "x". It is empty for every other login method, which is what
	// makes the three fields below readable without a second flag: they are
	// present exactly when this one is.
	//
	// Subject is that provider's own account identifier — the X user id, for
	// the x provider — and Issuer is the provider's namespace.
	Provider string `json:"provider,omitempty"`
	// Username is the handle the provider reported, such as an X @name without
	// the @. It is stored for display and support, and is not the account link:
	// a handle can be renamed, and on X a released one can be claimed by
	// somebody else, so it identifies nobody durably. It is a copy taken at
	// login and goes stale the moment the user renames themselves.
	Username string `json:"username,omitempty"`
	// AvatarURL is the profile image the provider reported, when it reported
	// one. Like Username it is a copy taken at login.
	AvatarURL string `json:"avatar_url,omitempty"`
	// ProviderAuthTime is the verified auth_time of the identity provider: the
	// moment it last actively authenticated this person, which is not the
	// moment the login landed here. A provider may satisfy an authorization
	// request from a single sign-on session established much earlier, so
	// freshness is measured from this and only falls back to the session's own
	// AuthenticatedAt when the provider reported nothing.
	//
	// It lives here rather than on the session record because it is an OpenID
	// Connect concept: a passkey-only deployment has no provider at all, and
	// the generic session package must not learn what an issuer is.
	ProviderAuthTime int64 `json:"provider_auth_time,omitempty"`
	// StepUpAt records that a zero-window re-proof completed, which is the only
	// thing that can satisfy a per-operation requirement.
	//
	// It lives in the session rather than in a cookie so it cannot be forged. It
	// is spent by the guard that accepts it, under one lock with the read that
	// accepted it, so two operations arriving together cannot share one proof —
	// see zeroWindowAdmission. The short window is the backstop for a proof
	// nobody spent, such as one the user abandoned, rather than the mechanism.
	StepUpAt int64 `json:"step_up_at,omitempty"`
}

// provenAt reports when this session's identity was last actually proved,
// preferring what the provider reported over when the login arrived.
func (d SessionData) provenAt() time.Time {
	if d.ProviderAuthTime > 0 {
		return time.Unix(d.ProviderAuthTime, 0).UTC()
	}
	return d.AuthenticatedAt
}

// identityFrom builds the verified identity and resolves the configured lookup
// key. A configured claim that the token does not carry, or carries in an
// unusable shape, leaves Key empty and is refused by admit.
func identityFrom(claims jwt.Claims, identityClaim string) Identity {
	if identityClaim == "" {
		identityClaim = ClaimSubject
	}
	identity := Identity{
		Issuer:   claims.Issuer,
		Subject:  claims.Subject,
		KeyClaim: identityClaim,
		Claims:   Claims{raw: claims.Raw},
	}
	if key, ok := claimLookupValue(identityClaim, identity); ok {
		identity.Key = key
	}
	return identity
}

// identityFromProfile builds the verified identity of a provider login that
// carries no ID Token.
//
// Verified means something weaker here than it does for an ID Token, and the
// difference is worth stating: nothing about this profile is signed. What
// stands behind it is that the access token was obtained through a PKCE-bound
// exchange this deployment started, and that the profile was read from the
// provider's own endpoint over TLS using that token. The provider is trusted to
// answer truthfully about its own users, which is the same trust an ID Token
// signature ultimately certifies, established by transport rather than by
// signature.
//
// The issuer is the provider definition's, not something a response said. It is
// the namespace of the account link, and letting a response name its own
// namespace would let one compromised provider claim another's accounts.
func identityFromProfile(provider oauthprofile.Provider, profile oauthprofile.Profile, identityClaim string) Identity {
	if identityClaim == "" {
		identityClaim = ClaimSubject
	}
	identity := Identity{
		Issuer:   provider.Issuer,
		Subject:  profile.Subject,
		KeyClaim: identityClaim,
		Claims:   Claims{raw: profile.Claims},
	}
	if key, ok := claimLookupValue(identityClaim, identity); ok {
		identity.Key = key
	}
	return identity
}

// admissionRule is the admission policy of whichever mode is asking.
//
// OIDC admission and bearer admission are the same decision reached by
// different proofs: verification establishes who the caller is, and this decides
// whether that identity may enter this application. Sharing the rule is what
// keeps a deployment from having two answers to the same question.
type admissionRule struct {
	Admission        string
	Claim            ClaimConfig
	RegisteredClaims []string
	AutoProvision    bool
}

func (c OIDCConfig) admissionRule() admissionRule {
	return admissionRule{
		Admission:        c.Admission,
		Claim:            c.Claim,
		RegisteredClaims: c.RegisteredClaims,
		AutoProvision:    c.AutoProvision,
	}
}

func (o OAuthConfig) admissionRule() admissionRule {
	return admissionRule{
		Admission:        o.Admission,
		Claim:            o.Claim,
		RegisteredClaims: o.RegisteredClaims,
		AutoProvision:    o.AutoProvision,
	}
}

func (j JWTConfig) admissionRule() admissionRule {
	return admissionRule{
		Admission:        j.Admission,
		Claim:            j.Claim,
		RegisteredClaims: j.RegisteredClaims,
		AutoProvision:    j.AutoProvision,
	}
}

// admit applies policy:oidc-admission to an already verified identity. It runs
// on every login, including one that resolves to an existing account.
func admit(ctx context.Context, config OIDCConfig, allowlist AllowlistStore, identity Identity) (Account, error) {
	return admitIdentity(ctx, config.admissionRule(), allowlist, identity)
}

// admitIdentity is the mode-neutral admission decision.
func admitIdentity(ctx context.Context, config admissionRule, allowlist AllowlistStore, identity Identity) (Account, error) {
	if identity.Issuer == "" || identity.Subject == "" || identity.Key == "" {
		// No usable lookup key means no account can be identified, so nothing
		// downstream may treat this login as a known person.
		return Account{}, ErrAccessDenied
	}
	switch config.Admission {
	case AdmissionClaim:
		if !claimAdmits(config.Claim, identity.Claims) {
			return Account{}, ErrAccessDenied
		}
	case AdmissionRegistered:
		candidates := allowlistCandidates(config.RegisteredClaims, identity)
		if len(candidates) == 0 {
			// Nothing to compare is a non-match rather than an error, and the
			// store is never asked an empty question.
			return Account{}, ErrAccessDenied
		}
		if allowlist == nil {
			return Account{}, errors.New("auth: allowlist is not available")
		}
		registered, err := allowlist.Registered(ctx, identity.Issuer, candidates)
		if err != nil {
			// A lookup failure is an error, never a denial, so an outage
			// cannot silently reopen or close a deployment.
			return Account{}, err
		}
		if !registered {
			return Account{}, ErrAccessDenied
		}
	}
	provision := config.AutoProvision && config.Admission != AdmissionExisting
	account, err := accountResolver()(ctx, identity, provision)
	switch {
	case errors.Is(err, ErrUnknownIdentity):
		return Account{}, ErrAccessDenied
	case err != nil:
		return Account{}, err
	case account.ID == "":
		return Account{}, ErrAccessDenied
	case account.Suspended:
		return Account{}, ErrAccessDenied
	}
	return account, nil
}

// claimAdmits evaluates the configured claim rule. Matching is exact and
// case-sensitive, and an unexpected value shape is a non-match.
func claimAdmits(rule ClaimConfig, claims Claims) bool {
	value, ok := claimAt(rule.Path, claims)
	if !ok || len(rule.Values) == 0 {
		return false
	}
	present := claimStrings(value)
	if len(present) == 0 {
		return false
	}
	if rule.Match == MatchAll {
		for _, want := range rule.Values {
			if !containsString(present, want) {
				return false
			}
		}
		return true
	}
	for _, want := range rule.Values {
		if containsString(present, want) {
			return true
		}
	}
	return false
}

// claimAt resolves a JSON Pointer into the verified claim set.
func claimAt(pointer string, claims Claims) (json.RawMessage, bool) {
	if pointer == "" || !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	tokens := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	name := unescapePointerToken(tokens[0])
	current, ok := claims.raw[name]
	if !ok {
		return nil, false
	}
	for _, token := range tokens[1:] {
		var object map[string]json.RawMessage
		if json.Unmarshal(current, &object) != nil {
			return nil, false
		}
		current, ok = object[unescapePointerToken(token)]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func unescapePointerToken(token string) string {
	token = strings.ReplaceAll(token, "~1", "/")
	return strings.ReplaceAll(token, "~0", "~")
}

// claimStrings accepts one string or an array of strings and rejects every
// other shape.
func claimStrings(value json.RawMessage) []string {
	var single string
	if json.Unmarshal(value, &single) == nil {
		return []string{single}
	}
	var many []string
	if json.Unmarshal(value, &many) == nil {
		return many
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
