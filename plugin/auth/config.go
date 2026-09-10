package auth

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/contrib/oauthprofile"
	"github.com/shibukawa/popcornweb/internal/pathpattern"
	"github.com/shibukawa/popcornweb/internal/requestorigin"
	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/popcornweb/sessionconfig"
)

// Authentication modes.
//
// ModeJWTOnly is deliberately absent from the api:cli-init capability catalog:
// it authenticates an API caller that already holds a token from an
// authorization server this framework does not run, so scaffolding it would
// scaffold a dependency the project does not have. See
// decision:jwt-only-mode-not-scaffolded.
const (
	ModeOIDCOnly    = "oidc_only"
	ModeOIDCPasskey = "oidc_passkey"
	ModePasskeyOnly = "passkey_only"
	ModeOAuthOnly   = "oauth_only"
	ModeJWTOnly     = "jwt_only"
)

// Revocation modes of ModeJWTOnly. There is no default: a deployment states
// whether it can revoke a token, because the permissive answer must not arrive
// as one nobody typed.
const (
	// RevocationOff accepts every verified token until it expires.
	RevocationOff = "off"
	// RevocationToken revokes one token by its jti claim.
	RevocationToken = "token"
	// RevocationSubject revokes every token issued to an identity before a
	// stamp, which is what a compromised account needs and what enumerating
	// jti values cannot do.
	RevocationSubject = "subject"
	// RevocationBoth is the ordinary answer; neither form substitutes for the
	// other.
	RevocationBoth = "both"
)

// What a revocation lookup does when the store cannot answer.
const (
	// RevocationRefuse fails closed, which is the default.
	RevocationRefuse = "refuse"
	// RevocationAdmit keeps serving while the store is down, which makes
	// revocation advisory for the duration. It is an incident lever rather
	// than a deployment posture.
	RevocationAdmit = "admit"
)

// How the signing keys of the issuer are found.
const (
	// DiscoveryOIDC reads /.well-known/openid-configuration.
	DiscoveryOIDC = "oidc"
	// DiscoveryOAuth reads the RFC 8414 authorization server metadata.
	DiscoveryOAuth = "oauth"
	// DiscoveryManual takes auth.jwt.jwks_uri and fetches no metadata.
	DiscoveryManual = "manual"
)

// Admission policies applied to a verified OIDC identity.
const (
	// AdmissionAuthenticated admits every identity the configured issuer
	// verifies.
	AdmissionAuthenticated = "authenticated"
	// AdmissionClaim admits an identity whose configured claim matches.
	AdmissionClaim = "claim"
	// AdmissionExisting admits only an identity an application resolver
	// already knows. It forbids provisioning.
	AdmissionExisting = "existing"
	// AdmissionRegistered admits only an identity a deployment registered in
	// advance in the framework-owned allowlist table. It is the mode for a
	// closed deployment whose users are known before their first login.
	AdmissionRegistered = "registered"
)

// Responses to an unauthenticated request on a protected path.
const (
	UnauthenticatedRedirect     = "redirect"
	UnauthenticatedUnauthorized = "unauthorized"
)

// Claim match modes.
const (
	MatchAny = "any"
	MatchAll = "all"
)

// Ceremony user verification levels, as WebAuthn names them.
const (
	UserVerificationRequired    = "required"
	UserVerificationPreferred   = "preferred"
	UserVerificationDiscouraged = "discouraged"
)

// Discoverable credential requirements.
const (
	DiscoverableRequired  = "required"
	DiscoverablePreferred = "preferred"
)

// Registration policies. They decide how an account comes into existence in a
// deployment that does not get one from an identity provider.
const (
	RegistrationDisabled      = "disabled"
	RegistrationOIDC          = "oidc"
	RegistrationInvite        = "invite"
	RegistrationAdministrator = "administrator"
	RegistrationOpen          = "open"
)

// Recovery authorities. A deployment names one before it enables registration,
// because an unrecoverable account is a support incident rather than a policy.
const (
	RecoveryOIDC          = "oidc"
	RecoveryAdministrator = "administrator"
	RecoveryApplication   = "application"
)

// SessionLifetimeConfig is the [auth.session] binding, declared in
// popcornweb/sessionconfig so that pw can read it without importing this
// package.
//
// It is bound here rather than there because a lifetime is authentication's
// statement: linking this package is what makes the keys exist, and a
// deployment with no authentication has no framework session lifetime at all.
// The alias must stay an alias, because the configuration registry is keyed by
// reflect.Type and a defined type would be a different one.
type SessionLifetimeConfig = sessionconfig.SessionLifetimeConfig

// Config is the [auth] runtime binding. It is registered when this package is
// imported.
type Config struct {
	Enabled bool `default:"false"`
	// Backend names the storage of the four stores this package owns: the
	// ceremony records, the admission allowlist, the passkey credentials, and
	// the issued bootstrap credentials. They move together, because they are
	// one deployment's authentication state and splitting them across two
	// engines gains nothing.
	Backend string `default:"rdb" enum:"rdb,dynamo" dependon:".enabled" help:"storage backend of the authentication tables: rdb or dynamo"`
	// Mode selects which login methods this deployment offers, and with them
	// which of the OIDC, Passkey, and JWT sections below are in force. The enum
	// is what makes those sections' conditions checkable: a mistyped mode there
	// would hide a whole subtree from the startup summary silently and forever,
	// and generation rejects a value that is not listed here.
	Mode string `default:"oidc_only" enum:"oidc_only,oidc_passkey,passkey_only,oauth_only,jwt_only" dependon:".enabled" help:"oidc_only, oidc_passkey, passkey_only, oauth_only, or jwt_only"`
	// LoginPath starts the provider flow.
	LoginPath    string `default:"/auth/login" dependon:".enabled" help:"path that starts the provider flow"`
	CallbackPath string `default:"/auth/callback" dependon:".enabled"`
	LogoutPath   string `default:"/auth/logout" dependon:".enabled"`
	// PostLoginPath is the local path a completed login lands on.
	PostLoginPath string `default:"/" dependon:".enabled" help:"path a completed login lands on"`
	// RecentAuthMaxAge bounds how long a completed authentication still counts
	// as recent enough to add or remove a login method.
	RecentAuthMaxAge time.Duration `default:"5m" dependon:".enabled" summary:"omit" help:"how recently a request must have authenticated to change a login method"`
	// SharedDevice declares that the browsers reaching this deployment are
	// shared, which couples the settings that would otherwise leave one user
	// visible to the next. It fixes the logout scope to global and withholds
	// the select_account prompt, because that prompt exists to surface exactly
	// what this mode hides.
	//
	// Any one of those alone achieves nothing: with the local hint disabled
	// but the provider session alive, the next visitor still sees the previous
	// account in the provider's own account picker.
	//
	// It reduces disclosure and does not eliminate it. The common end of a
	// session on a shared device is abandonment rather than logout, and no
	// relying party can end a provider session it was not asked to end.
	SharedDevice bool               `default:"false" dependon:".enabled" help:"declare that browsers are shared, coupling the settings that hide one user from the next"`
	Assurance    AssuranceConfig    `dependon:".enabled"`
	Protection   ProtectionConfig   `dependon:".enabled"`
	Registration RegistrationConfig `dependon:".enabled"`
	Recovery     RecoveryConfig     `dependon:".enabled"`
	Bootstrap    BootstrapConfig    `dependon:".enabled" summary:"omit"`
	// The three login-method sections name the modes they belong to, so a
	// summary reports the methods this deployment offers rather than all of
	// them. The lists restate usesOIDC, usesPasskey, and usesJWT below; those
	// predicates decide what is built, and these decide what is reported, so
	// the two have to agree. The enabled switch is not repeated: Mode answers
	// to it, and a condition on Mode inherits that gate transitively.
	OIDC OIDCConfig `dependon:".mode=oidc_only,oidc_passkey" help:"The three login-method sections name the modes they belong to, so a summary reports the methods this deployment offers rather than all of them. The lists restate usesOIDC, usesPasskey, and usesJWT below; those predicates decide what is built, and these decide what is reported, so the two have to agree. The enabled switch is not repeated: Mode answers to it, and a condition on Mode inherits that gate transitively"`
	// The key tag is load-bearing. Generation derives a TOML key from the field
	// name and splits it at every lower-to-upper boundary, which turns OAuth
	// into o_auth — OIDC survives only because it is all caps. The setting a
	// deployment writes is [auth.oauth].
	OAuth   OAuthConfig   `key:"oauth" dependon:".mode=oauth_only" help:"The key tag is load-bearing. Generation derives a TOML key from the field name and splits it at every lower-to-upper boundary, which turns OAuth into o_auth — OIDC survives only because it is all caps. The setting a deployment writes is [auth.oauth]"`
	Passkey PasskeyConfig `dependon:".mode=oidc_passkey,passkey_only"`
	JWT     JWTConfig     `dependon:".mode=jwt_only"`
}

// JWTConfig is the bearer-token binding of ModeJWTOnly. It describes one
// authorization server this deployment trusts and one resource this deployment
// is.
//
// Several fields carry no default on purpose. Each of them has a permissive
// answer, and a permissive answer that arrives as a default is one nobody
// decided: the audience would be "any resource", the admission rule "everyone
// the issuer knows", and the revocation mode "cannot revoke". Startup names the
// missing key instead.
type JWTConfig struct {
	// Issuer is the exact iss claim this deployment accepts. Key discovery
	// starts here, so it is also where the trust in a signing key comes from.
	Issuer string `env:"AUTH_JWT_ISSUER" help:"exact iss claim value this deployment accepts"`
	// Audience is what this API is called by the authorization server. It has
	// no default: a token verified without an audience check was minted for
	// some other service and would be accepted here anyway.
	Audience []string `help:"aud value naming this API; required"`
	// AudienceMatch decides how a multi-valued aud is compared. any is
	// ordinary, because an access token names every resource it may reach.
	AudienceMatch string `default:"any" enum:"any,all" key:"audience_match" help:"any or all"`
	// Algorithms is the exact verification allowlist. It never comes from the
	// token header. An HMAC algorithm is refused outright: the verification key
	// arrives from a public JWKS, so accepting one would let a published key be
	// used as a shared secret.
	//
	// It is required rather than defaulted. Which signatures this deployment
	// trusts is not a question to inherit an answer to, and the answer is one
	// line: algorithms = ["RS256"].
	Algorithms []string `help:"exact verification algorithm allowlist; required, e.g. [\"RS256\"]"`
	// RequiredTokenType is the typ header this deployment demands. RFC 9068
	// names at+jwt, and demanding it is what keeps an ID Token from being
	// replayed here as an access token.
	//
	// Setting it empty accepts an absent typ, for an issuer predating RFC 9068.
	// That is an explicit act with a cost: the audience becomes the only thing
	// separating the two token kinds, so it must be one the issuer does not put
	// in its ID Tokens.
	RequiredTokenType string `default:"at+jwt" key:"required_token_type" help:"typ header to demand; empty accepts an absent typ"`
	// RequiredScopes are the scope values every request must carry. It is its
	// own field rather than a claim rule because scope is a space-delimited
	// string and a generic claim comparison would match the whole value.
	RequiredScopes []string `key:"required_scopes" help:"scope values every request must carry"`
	// Discovery selects where the signing keys are found.
	Discovery string `default:"oidc" enum:"oidc,oauth,manual" help:"oidc, oauth, or manual"`
	// JWKSURI is read only under DiscoveryManual.
	JWKSURI string `key:"jwks_uri" help:"signing key set, for manual discovery"`
	// Leeway absorbs clock skew between this host and the issuer.
	Leeway time.Duration `default:"30s" help:"clock skew allowance"`
	// MaxTokenLifetime bounds exp minus iat. It is required, because it is also
	// how long a subject-form revocation entry must be kept: this application
	// cannot know how long the issuer mints for, so the deployment says.
	MaxTokenLifetime time.Duration `key:"max_token_lifetime" help:"longest exp-minus-iat accepted; required"`
	// MaxTokenBytes bounds the compact token before it is decoded.
	MaxTokenBytes int `default:"8192" key:"max_token_bytes" help:"largest compact token accepted"`
	// JWKSRefreshCooldown bounds how often an unknown kid may cause a refresh,
	// so a stream of forged kid values cannot be amplified into traffic against
	// the issuer.
	JWKSRefreshCooldown time.Duration `default:"1m" key:"jwks_refresh_cooldown" help:"shortest interval between unknown-kid refreshes"`
	// AllowLoopbackHTTP permits an http issuer on loopback and relaxes the
	// same-origin rule on the discovered key set. Development only.
	AllowLoopbackHTTP bool `default:"false" key:"allow_loopback_http" help:"permit an http loopback issuer during development"`
	// IdentityClaim names the verified claim that identifies a local account.
	IdentityClaim string `default:"sub" key:"identity_claim" help:"verified claim that identifies a local account"`
	// Admission decides whether a verified identity may enter this application.
	// Verification proves who the caller is; this decides whether they belong.
	Admission string `help:"authenticated, claim, registered, or existing; required"`
	// AutoProvision permits an unknown verified identity to create an account.
	// It defaults false here and true under OIDC, because a browser login is a
	// person arriving and a bearer request is a machine already running.
	AutoProvision bool `default:"false" key:"auto_provision" help:"permit an unknown verified identity to create an account"`
	// Claim is the admission rule applied when Admission is claim.
	Claim ClaimConfig `help:"admission rule applied when admission is claim"`
	// RegisteredClaims names the claims compared against the allowlist table
	// under AdmissionRegistered.
	RegisteredClaims []string `key:"registered_claims" help:"claims compared against the allowlist; defaults to identity_claim"`
	Revocation       JWTRevocationConfig
	Dev              JWTDevConfig
}

// JWTRevocationConfig decides whether a token can be withdrawn before it
// expires, and what happens when the store that knows cannot be reached.
type JWTRevocationConfig struct {
	// Mode is off, token, subject, or both, and carries no default. Selecting a
	// form is what turns its requirements on: the token form makes jti
	// mandatory, rather than a second switch that can disagree with it.
	Mode string `help:"off, token, subject, or both; required in jwt_only"`
	// OnUnavailable is refuse or admit. It defaults to refuse, because a store
	// that cannot answer has not said the token is valid.
	OnUnavailable string `default:"refuse" enum:"refuse,admit" key:"on_unavailable" help:"refuse or admit when the store cannot answer"`
	// MaxPropagationDelay bounds a per-process cache of revocation answers. It
	// defaults to zero, which is no cache: a revocation that takes effect at
	// the next request is the answer nobody has to reason about.
	MaxPropagationDelay time.Duration `key:"max_propagation_delay" help:"how stale a cached revocation answer may be; zero disables the cache"`
}

// JWTDevConfig turns off token verification under `pw dev`.
//
// This is the one setting that turns authentication off, so it is reachable
// only when four independent locks are open at once: the pwdev build mode, a
// runtime environment that is not staging or production, this field, and a
// request that arrived from loopback. A binary built without the pwdev mode
// refuses to start when it sees the field rather than ignoring it, because a
// security setting that is silently dropped reads as configured security.
type JWTDevConfig struct {
	TrustUnverifiedTokens bool `default:"false" key:"trust_unverified_tokens" help:"development only: admit a token without verifying it"`
}

// RegistrationConfig names how a deployment admits a new account. It carries no
// default, because a mode that needs it must not inherit an answer nobody chose.
type RegistrationConfig struct {
	Policy string `help:"disabled, oidc, invite, administrator, or open"`
}

// RecoveryConfig names the authority that restores access to an account whose
// credentials are gone.
type RecoveryConfig struct {
	Policy string `help:"oidc, administrator, or application"`
}

// BootstrapConfig bounds the issued credential that opens a first passkey
// enrollment. See policy:bootstrap-credential-security.
//
// The two durations bound consecutive phases and are deliberately different
// lengths: one covers a person receiving a secret out of band, the other covers
// them finishing a ceremony at the keyboard. Neither is a secret, so both are
// shown in the startup summary despite sitting under a credential heading.
type BootstrapConfig struct {
	// IssueTTL is how long an issued secret stays redeemable, measured from
	// issuance. It spans delivery, so it is the longer of the two.
	IssueTTL time.Duration `default:"24h" secret:"show" help:"how long an issued secret stays redeemable"`
	// EnrollmentTTL is how long the enrollment stays open, measured from a
	// successful redemption. It spans one ceremony, so it is short.
	//
	// It is not a session lifetime: what a redemption grants is a ticket that
	// authorizes exactly one registration, and the request stays unauthenticated
	// until that registration finishes.
	EnrollmentTTL time.Duration `default:"10m" secret:"show" help:"how long an enrollment stays open after a redemption"`
	// MaxAttempts bounds how many redemptions may be tried before the
	// credential is spent, whether or not any of them was close.
	MaxAttempts int `default:"5" secret:"show" help:"redemption attempts before the credential is spent"`
}

// PasskeyConfig is the WebAuthn relying-party registration of this deployment.
type PasskeyConfig struct {
	// Path is the base path of the ceremony endpoints. The five endpoints hang
	// off it, so one setting keeps them consistent and keeps them all reachable
	// past the guard.
	Path string `default:"/auth/passkey" help:"base path of the ceremony endpoints"`
	// RPID scopes every credential. It is a domain, never an IP literal,
	// because an IP address cannot be an RP ID.
	RPID    string   `key:"rp_id" help:"relying party domain; localhost during development"`
	RPName  string   `key:"rp_name" help:"relying party display name"`
	Origins []string `help:"origin the browser reaches this deployment on"`
	// UserVerification and Discoverable are the ceremony requirements the
	// relying party asks the authenticator for.
	UserVerification string `default:"required" help:"required, preferred, or discouraged"`
	Discoverable     string `default:"preferred" help:"required or preferred"`
}

// ProtectionConfig selects the paths that require an authenticated request.
type ProtectionConfig struct {
	Include []string `help:"protected path pattern"`
	Exclude []string `help:"public path pattern"`
	// Unauthenticated is redirect or unauthorized.
	Unauthenticated string `default:"redirect" help:"redirect or unauthorized"`
}

// OIDCConfig describes the relying-party registration and admission policy.
type OIDCConfig struct {
	Issuer       string `env:"AUTH_OIDC_ISSUER"`
	ClientID     string `env:"AUTH_OIDC_CLIENT_ID"`
	ClientSecret string `secret:"mask" env:"AUTH_OIDC_CLIENT_SECRET"`
	RedirectURL  string
	Scopes       []string
	// EndpointHosts restricts which hosts the issuer's discovery document may
	// point its endpoints at. Empty accepts whatever the document names.
	//
	// The document decides where this deployment sends the authorization code
	// and its client secret, and the only value checked against configuration is
	// the issuer field the document reports about itself. Naming the hosts turns
	// that into something the deployment asserts.
	//
	// It is empty by default because federated endpoints are ordinary rather than
	// suspicious — Google's issuer is accounts.google.com while its token
	// endpoint is oauth2.googleapis.com — so a same-origin rule would refuse a
	// working provider. The issuer's own host never needs listing, and a host is
	// matched exactly, without wildcards.
	EndpointHosts []string `help:"hosts the discovery document may point endpoints at; empty accepts any"`
	// IdentityClaim names the verified claim that identifies a local account.
	// It defaults to "sub".
	//
	// A deployment that provisions users in advance usually cannot know a
	// subject yet, and instead adds its own stable identifier to the
	// directory, such as an employee number. That claim must be stable for the
	// life of the account and unique within the issuer: it becomes the account
	// link, so a reissued or reused value hands one person another person's
	// account.
	IdentityClaim string `default:"sub" help:"verified claim that identifies a local account"`
	Admission     string `default:"authenticated" help:"authenticated, claim, registered, or existing"`
	// AutoProvision permits an unknown verified identity to create an account
	// through the registered account resolver.
	AutoProvision bool `default:"true" help:"AutoProvision permits an unknown verified identity to create an account through the registered account resolver"`
	// Claim is the admission rule applied when Admission is claim.
	Claim ClaimConfig `help:"admission rule applied when admission is claim"`
	// RegisteredClaims names the verified claims compared against the
	// allowlist table under AdmissionRegistered. It defaults to IdentityClaim
	// alone, because that is the value a deployment registers in advance.
	RegisteredClaims []string `help:"claims compared against the allowlist; defaults to identity_claim"`
	// LogoutScope decides what a logout does to the session the provider
	// holds, which is not the session this application owns.
	//
	// reconfirm revokes the local session, sends the provider nothing, and
	// marks the next authorization to carry prompt, so the provider still
	// demands proof while every other relying party sharing it is untouched.
	//
	// global additionally ends the provider session through the discovered end
	// session endpoint, which signs the user out of every application sharing
	// that provider. It is what a shared device wants and what a personal one
	// rarely does.
	//
	// There is no local-only value: revoking only the local session leaves the
	// next login silent, so the sign-out looks like it did nothing. That was
	// the failure reconfirm exists to fix.
	LogoutScope string `default:"reconfirm" enum:"reconfirm,global" help:"what a logout does to the provider session: reconfirm or global"`
	// ProviderLogout is removed and survives only to fail loudly. configbind
	// ignores a key no field declares, so deleting the field outright would
	// leave every scaffolded project silently running reconfirm while its
	// configuration still read as a global sign-out.
	//
	// The default is inverted to false so presence is detectable: an untouched
	// project binds false and starts, and one carrying provider_logout = true
	// is refused with LogoutScope named. A leftover false meant the rejected
	// local scope, whose nearest surviving behavior is the new default.
	//
	// Delete this once no configuration in the wild carries the key.
	ProviderLogout bool `default:"false" help:"removed; use auth.oidc.logout_scope"`
	// AllowGlobalLogoutRequest lets the logout request escalate to global, for
	// a deployment offering both a sign-out and a sign-out-everywhere control.
	// A request may only escalate: a forced downgrade would leave the provider
	// session alive after the user asked to leave it.
	AllowGlobalLogoutRequest bool `default:"false" help:"permit a logout request to escalate to a global sign-out"`
	// AllowLoopbackHTTP permits an http issuer on localhost. It exists for
	// local development against a loopback identity provider and must stay
	// false everywhere else.
	AllowLoopbackHTTP bool `default:"false" help:"permit an http loopback issuer during development"`
}

// OAuthConfig is the [auth.oauth] binding of ModeOAuthOnly: a login through a
// provider that authenticates a person over plain OAuth 2.0 and issues no ID
// Token at all.
//
// The difference from OIDCConfig is what is missing rather than what is added.
// There is no issuer to discover, because such a provider publishes no metadata
// document; there is no token to verify, because the only thing the callback
// receives is an access token addressed to the provider's own API; and there is
// no logout scope, because the provider offers no way to end the session it
// holds. What the deployment states instead is one provider name, which selects
// the endpoints, the scopes, and the shape of the account response together.
//
// Who the access token belongs to is settled by asking the provider, over a
// request that carries the token and nothing else. That is one round trip more
// than OIDC needs and one signature fewer to check, and it is the whole of the
// protocol difference between the two modes.
type OAuthConfig struct {
	// Provider names the built-in definition. It has no default: a provider is
	// the one thing this mode cannot infer, and defaulting to whichever one
	// happened to be written first would make the choice for a deployment that
	// never made it.
	Provider     string `enum:"x" help:"OAuth login provider; x is X, formerly Twitter"`
	ClientID     string `env:"AUTH_OAUTH_CLIENT_ID"`
	ClientSecret string `secret:"mask" env:"AUTH_OAUTH_CLIENT_SECRET"`
	// RedirectURL is the absolute callback URL registered with the provider.
	RedirectURL string `help:"RedirectURL is the absolute callback URL registered with the provider"`
	// Scopes replaces the provider's own defaults rather than adding to them,
	// so a deployment that states this list owns it. Empty asks for exactly
	// what the provider needs to report an account and nothing more, which is
	// what a login should ask for.
	Scopes []string `help:"scopes to request; empty asks for the provider's minimum login set"`
	// IdentityClaim names the claim of the fetched profile that identifies a
	// local account, and defaults to "sub", which every provider definition
	// fills with that provider's own stable identifier.
	//
	// A handle is the tempting alternative and the wrong one. X lets a handle
	// be renamed, and lets a released one be claimed by somebody else, so an
	// account linked to a handle eventually becomes somebody else's account.
	// Only a name the provider definition actually emits is accepted, so a
	// claim copied out of the provider's own API reference is refused at
	// startup rather than at every login.
	IdentityClaim string `default:"sub" help:"profile claim that identifies a local account; sub is the provider's own identifier"`
	Admission     string `default:"authenticated" help:"authenticated, claim, registered, or existing"`
	// AutoProvision permits an unknown profile to create an account through the
	// registered account resolver.
	AutoProvision bool `default:"true" help:"permit an unknown profile to create an account through the registered account resolver"`
	// Claim is the admission rule applied when Admission is claim.
	Claim ClaimConfig `help:"admission rule applied when admission is claim"`
	// RegisteredClaims names the profile claims compared against the allowlist
	// table under AdmissionRegistered, and defaults to IdentityClaim alone.
	RegisteredClaims []string `help:"claims compared against the allowlist; defaults to identity_claim"`
	// AllowLoopbackHTTP permits a request-relative redirect URL on loopback, so
	// a developer can run the login against the real provider from localhost
	// without registering a URL per machine. The provider endpoints themselves
	// stay https whatever this says.
	AllowLoopbackHTTP bool `default:"false" help:"permit an http loopback redirect URL during development"`
}

// AssuranceConfig holds the named freshness windows a handler requires by
// name, so the same handler code serves a consumer deployment with a long
// window and an internal one with a short window.
type AssuranceConfig struct {
	// Policy is an array of tables rather than a map, because configbind binds
	// statically and a map key is not a declared field:
	//
	//	[[auth.assurance.policy]]
	//	name = "admin"
	//	max_age = "15m"
	Policy   []AssurancePolicy `help:"named freshness windows a handler can require by name"`
	Hint     HintConfig        `help:"what the login screen may remember about the last user of a browser"`
	Presence PresenceConfig    `help:"end a session when nobody is at the keyboard, rather than when no request arrives"`
}

// PresenceConfig turns on the endpoint a browser reports human presence to.
//
// Idle expiry otherwise measures time since the last HTTP request, which is a
// proxy for presence that fails in both directions: a page holding a live
// connection reconnects on its own and keeps an unattended browser signed in,
// while a person reading one page for longer than the timeout issues no request
// at all and is signed out mid-work.
type PresenceConfig struct {
	Enabled bool `default:"false" help:"accept presence reports from the browser"`
	// Interval is how often the browser is expected to report. It bounds the
	// endpoint's rate and sets the pace the scaffolded script ticks at.
	Interval time.Duration `default:"1m" dependon:".enabled" help:"how often a browser reports"`
	// AbsentAfter ends the session once no interaction has been reported for
	// this long. It measures a person rather than a request, which is the whole
	// point of the signal.
	AbsentAfter time.Duration `default:"30m" dependon:".enabled" help:"end the session after this long with no interaction"`
}

// HintConfig controls whether an ended session leaves a non-authoritative note
// of who was signed in, so the next sign-in is shorter.
//
// It is off by default. A deployment that has not thought about shared devices
// therefore drops a browser straight from signed-in to anonymous, where the
// login screen offers no account and no issuer.
//
// The hint grants nothing. A request carrying one is unauthenticated, and the
// path guard denies it exactly as it denies any other.
type HintConfig struct {
	Enabled bool   `default:"false" help:"remember who last signed in, to shorten the next sign-in"`
	Name    string `default:"pw_hint" dependon:".enabled" help:"cookie name"`
	// Secret seals the cookie. The contents never reach the client, so the
	// hint may hold a login identifier; what must not leak is what the login
	// screen renders, which is masked instead.
	Secret string `secret:"mask" env:"AUTH_HINT_SECRET" dependon:".enabled" help:"base64 secret of at least 256 bits that seals the hint"`
	// PreviousSecrets keep a rotation readable.
	PreviousSecrets []string `secret:"mask" dependon:".enabled" help:"retired secrets kept readable during a rotation"`
	// TTL is the absolute bound and IdleTimeout the one measured from the last
	// successful login. The pair is the session's own shape, for the same
	// reasons, and is deliberately not inherited from it: a hint outlives a
	// session by design.
	//
	// Setting TTL to zero is a valid answer. It means this browser may
	// remember nothing, which is what a shared terminal wants and what
	// SharedDevice sets for a whole deployment.
	TTL         time.Duration `default:"720h" dependon:".enabled" help:"how long a hint may live at all"`
	IdleTimeout time.Duration `default:"336h" dependon:".enabled" help:"how long since the last successful login a hint survives"`
}

// AssurancePolicy names one freshness window. A zero MaxAge is meaningful and
// means prove again for this operation; it is not an unset field.
type AssurancePolicy struct {
	Name   string        `help:"name a handler passes to auth.Policy"`
	MaxAge time.Duration `help:"how old a proof may be; zero means prove again for this operation"`
	// Confirm refuses to count the login that started the session, so only a
	// re-proof this guard asked for satisfies the policy.
	//
	// Signing in and confirming an operation are different acts. Without this,
	// a window wide enough to be usable lets a sign-in stand in for the
	// confirmation: someone who signed in a minute ago to read their dashboard
	// would reach a transfer without ever being asked about it.
	Confirm bool `default:"false" help:"require a re-proof this guard asked for; a login never counts"`
}

// ClaimConfig is the admission rule of AdmissionClaim. Its keys hang off
// auth.enabled rather than the admission policy: falsy names the single value
// that means off, and here every policy except claim would have to be named.
type ClaimConfig struct {
	// Path is a JSON Pointer into the verified ID Token claims.
	Path   string `help:"JSON Pointer into verified claims"`
	Values []string
	Match  string `default:"any" help:"any or all"`
}

func (c Config) validate() error { return c.validateShape() }

// validateShape applies the rules that outlive the current implementation
// status. It validates only what the selected mode uses and refuses what that
// mode cannot honor, because a silently ignored setting reads as configured
// security.
func (c Config) validateShape() error {
	switch c.Mode {
	case ModeOIDCOnly, ModeOIDCPasskey, ModePasskeyOnly, ModeOAuthOnly, ModeJWTOnly:
	default:
		return fmt.Errorf("auth.mode must be %q, %q, %q, %q, or %q",
			ModeOIDCOnly, ModeOIDCPasskey, ModePasskeyOnly, ModeOAuthOnly, ModeJWTOnly)
	}
	// An unknown backend names what is linked rather than what exists, because
	// the difference between the two is an import line a deployment can add.
	if _, linked := backendFactory(c.backendName()); !linked {
		return fmt.Errorf("auth.backend = %q is not linked; registered backends are %s",
			c.backendName(), strings.Join(registeredBackends(), ", "))
	}
	if c.usesJWT() && c.backendName() != BackendRDB && c.JWT.readsAStore() {
		// jwt_only reads the allowlist and the revocation list directly rather
		// than through the backend, and neither has a non-relational
		// implementation. Refusing the pair is the alternative to accepting a
		// key that would silently do nothing.
		return fmt.Errorf(
			"auth.backend = %q is not implemented for auth.mode %q; its registered allowlist and revocation list are relational, so use %q or turn both off",
			c.backendName(), ModeJWTOnly, BackendRDB)
	}
	paths := map[string]string{
		"auth.login_path":      c.LoginPath,
		"auth.callback_path":   c.CallbackPath,
		"auth.logout_path":     c.LogoutPath,
		"auth.post_login_path": c.PostLoginPath,
	}
	if c.usesJWT() {
		// No ceremony is mounted, so none of those paths mean anything. They
		// keep their defaults and are not validated as routes this mode serves.
		paths = nil
	}
	if c.usesPasskey() {
		paths["auth.passkey.path"] = c.Passkey.Path
	}
	for key, value := range paths {
		if !strings.HasPrefix(value, "/") || strings.Contains(value, "//") ||
			strings.ContainsAny(value, "\\\x00") {
			return fmt.Errorf("%s must be a rooted local path, got %q", key, value)
		}
	}
	switch c.Protection.Unauthenticated {
	case UnauthenticatedRedirect, UnauthenticatedUnauthorized:
	default:
		return fmt.Errorf("auth.protection.unauthenticated must be %q or %q",
			UnauthenticatedRedirect, UnauthenticatedUnauthorized)
	}
	if _, err := pathpattern.Compile(c.Protection.Include); err != nil {
		return fmt.Errorf("auth.protection.include: %w", err)
	}
	if _, err := pathpattern.Compile(c.Protection.Exclude); err != nil {
		return fmt.Errorf("auth.protection.exclude: %w", err)
	}
	if err := c.validateAssurance(); err != nil {
		return err
	}
	if err := c.validateOIDCUse(); err != nil {
		return err
	}
	if err := c.validateOAuthUse(); err != nil {
		return err
	}
	if err := c.validatePasskeyUse(); err != nil {
		return err
	}
	return c.validateJWTUse()
}

// validateAssurance rejects a policy table a handler could not resolve, so a
// name that does not exist fails at startup rather than at the request that
// needed it. It also refuses the settings a shared-device deployment cannot
// honor, rather than overriding them: a configuration file that reads as one
// behavior while the deployment runs another is the failure this whole mode
// exists to avoid.
func (c Config) validateAssurance() error {
	seen := make(map[string]bool, len(c.Assurance.Policy))
	for i, policy := range c.Assurance.Policy {
		if policy.Name == "" {
			return fmt.Errorf("auth.assurance.policy[%d].name must be set", i)
		}
		if seen[policy.Name] {
			return fmt.Errorf("auth.assurance.policy name %q is declared twice", policy.Name)
		}
		seen[policy.Name] = true
		if policy.MaxAge < 0 {
			return fmt.Errorf("auth.assurance.policy %q: max_age must not be negative", policy.Name)
		}
	}
	if c.RecentAuthMaxAge < 0 {
		return errors.New("auth.recent_auth_max_age must not be negative")
	}
	if c.OIDC.ProviderLogout {
		return fmt.Errorf("auth.oidc.provider_logout is removed; set auth.oidc.logout_scope = %q for the same behavior, or %q to keep the provider session and re-confirm at the next login",
			LogoutScopeGlobal, LogoutScopeReconfirm)
	}
	if c.SharedDevice && c.usesOIDC() && c.OIDC.LogoutScope != LogoutScopeGlobal {
		return fmt.Errorf("auth.shared_device requires auth.oidc.logout_scope %q, got %q",
			LogoutScopeGlobal, c.OIDC.LogoutScope)
	}
	if !c.usesOIDC() && !c.usesJWT() {
		// Only the OIDC login can reach the session a provider holds. A mode
		// that cannot is refused the settings that say it will, rather than
		// binding them inert: a configuration reading "sign out everywhere"
		// while the logout stops at the local session is the silently ignored
		// security setting the mode rules exist to prevent.
		//
		// The default is not refused, because every mode binds it. Only a value
		// somebody typed is, which is what makes the refusal legible.
		if c.OIDC.LogoutScope == LogoutScopeGlobal {
			return fmt.Errorf("auth.mode %q reaches no provider session, so auth.oidc.logout_scope = %q cannot be honored; leave it at %q",
				c.Mode, LogoutScopeGlobal, LogoutScopeReconfirm)
		}
		if c.OIDC.AllowGlobalLogoutRequest {
			return fmt.Errorf("auth.mode %q reaches no provider session, so auth.oidc.allow_global_logout_request has nothing to escalate to", c.Mode)
		}
	}
	if c.SharedDevice && c.usesOAuth() {
		// The mode couples the settings that hide one user from the next, and
		// the load-bearing half of that is what happens at the provider: a
		// global sign-out, and a login the provider may not answer silently.
		// Plain OAuth offers neither, so the local half alone would leave the
		// next visitor one click from the previous visitor's account while the
		// configuration read as a shared-device deployment.
		return fmt.Errorf("auth.shared_device is not available under auth.mode %q: a plain OAuth provider offers no global sign-out and no way to demand a fresh login, so only the local half of the mode could be honored", ModeOAuthOnly)
	}
	if c.SharedDevice && c.Assurance.Hint.Enabled {
		return errors.New("auth.shared_device forbids auth.assurance.hint.enabled: remembering the last user is what the mode exists to prevent")
	}
	if err := c.Assurance.Hint.validate(); err != nil {
		return err
	}
	return c.Assurance.Presence.validate()
}

func (p PresenceConfig) validate() error {
	if !p.Enabled {
		return nil
	}
	if p.Interval <= 0 {
		return errors.New("auth.assurance.presence.interval must be positive")
	}
	if p.AbsentAfter <= 0 {
		return errors.New("auth.assurance.presence.absent_after must be positive")
	}
	if p.AbsentAfter <= p.Interval {
		// One missed tick would otherwise end the session, which a slow network
		// produces as readily as an empty chair.
		return errors.New("auth.assurance.presence.absent_after must exceed interval, so a single late report does not end a session")
	}
	return nil
}

// validate checks the hint settings of a deployment that turned it on. An
// unusable secret is refused rather than downgraded, because a hint that cannot
// be sealed would have to be written in the clear or silently dropped, and both
// are worse than refusing to start.
func (h HintConfig) validate() error {
	if !h.Enabled {
		return nil
	}
	if h.Name == "" {
		return errors.New("auth.assurance.hint.name must be set")
	}
	if h.TTL < 0 || h.IdleTimeout < 0 {
		return errors.New("auth.assurance.hint.ttl and idle_timeout must not be negative")
	}
	if h.IdleTimeout > h.TTL && h.TTL > 0 {
		return errors.New("auth.assurance.hint.idle_timeout must not exceed ttl, which already bounds it")
	}
	if _, err := session.ParseKeyring(append([]string{h.Secret}, h.PreviousSecrets...)...); err != nil {
		// The error names the key and never repeats the value.
		return fmt.Errorf("auth.assurance.hint.secret: %w", err)
	}
	return nil
}

func (c Config) usesOIDC() bool    { return ModeUsesOIDC(c.Mode) }
func (c Config) usesPasskey() bool { return ModeUsesPasskey(c.Mode) }
func (c Config) usesOAuth() bool   { return ModeUsesOAuth(c.Mode) }
func (c Config) usesJWT() bool     { return ModeUsesJWT(c.Mode) }

// The mode predicates are exported because pw doctor asks the same question
// this package answers at startup — which provider section a mode reads — and
// a second list of the modes in the CLI is a list that drifts. The dependon
// tags on the sections above restate these for the configuration summary.

// ModeUsesOIDC reports whether mode signs in through an OpenID Provider and
// reads [auth.oidc].
func ModeUsesOIDC(mode string) bool { return mode == ModeOIDCOnly || mode == ModeOIDCPasskey }

// ModeUsesPasskey reports whether mode serves passkeys and reads [auth.passkey].
func ModeUsesPasskey(mode string) bool { return mode == ModeOIDCPasskey || mode == ModePasskeyOnly }

// ModeUsesOAuth reports whether mode signs in through a plain OAuth provider
// and reads [auth.oauth].
func ModeUsesOAuth(mode string) bool { return mode == ModeOAuthOnly }

// ModeUsesJWT reports whether mode verifies bearer tokens and reads [auth.jwt].
func ModeUsesJWT(mode string) bool { return mode == ModeJWTOnly }

// admission is the admission policy of the selected mode. It exists so that
// nothing outside it has to know which section a mode reads: the three sections
// declare the same four settings, and a caller reaching for one of them by name
// would silently apply the OIDC policy in an OAuth deployment.
func (c Config) admission() admissionRule {
	switch {
	case c.usesJWT():
		return c.JWT.admissionRule()
	case c.usesOAuth():
		return c.OAuth.admissionRule()
	default:
		return c.OIDC.admissionRule()
	}
}

// reprovable reports whether this mode can prove an identity a second time
// during a session, which is what a zero-window or confirmed assurance
// requirement rests on.
//
// Only the OIDC login can. Re-proof is not the redirect: it is max_age going
// out and a verified auth_time coming back, and a provider that issues no ID
// Token reports neither. Sending the browser round such a provider would return
// it authorized, unable to say whether anybody was asked anything, and treating
// that as a confirmation would be the guard admitting on a redirect. A mode that
// cannot answer says so, per decision:assurance-scope-oidc-only.
func (c Config) reprovable() bool { return c.usesOIDC() }

// trustedOrigins are the origins the state-changing endpoints of this package
// accept besides the one they reconstruct from the request itself.
//
// Both sources are origins this deployment already had to declare for another
// reason: the passkey allowlist, which policy:passkey-security requires to be
// explicit and HTTPS, and the OIDC redirect URL, which the provider will only
// send a browser back to because it was registered there. Neither is a new
// setting, and neither is inferred from a header a caller can send.
//
// This is what keeps a deployment behind a TLS-terminating proxy working: such
// a request arrives without r.TLS, so its own origin reconstructs as http while
// the browser reports https, and the origin it declared is the one that matches.
func (c Config) trustedOrigins() map[string]bool {
	origins := append([]string(nil), c.Passkey.Origins...)
	for _, redirect := range []string{c.OIDC.RedirectURL, c.OAuth.RedirectURL} {
		if redirect != "" {
			origins = append(origins, redirect)
		}
	}
	return requestorigin.Set(origins...)
}

// revokesTokens and revokesSubjects report which lookups a verified token faces.
func (r JWTRevocationConfig) revokesTokens() bool {
	return r.Mode == RevocationToken || r.Mode == RevocationBoth
}

func (r JWTRevocationConfig) revokesSubjects() bool {
	return r.Mode == RevocationSubject || r.Mode == RevocationBoth
}

func (r JWTRevocationConfig) enabled() bool { return r.Mode != "" && r.Mode != RevocationOff }

// issuesBootstrapCredentials reports whether this deployment ever hands out a
// login ID and temporary secret, which is what the bootstrap table stores.
func (c Config) issuesBootstrapCredentials() bool {
	if !c.usesPasskey() {
		return false
	}
	switch c.Registration.Policy {
	case RegistrationInvite, RegistrationAdministrator:
		return true
	}
	// Administrator-reviewed recovery issues one even when registration does
	// not, because that is how a locked-out account gets back in.
	return c.Recovery.Policy == RecoveryAdministrator
}

// validateOIDCUse validates the provider settings in the modes that use them
// and refuses them everywhere else, so a leftover AUTH_OIDC_ISSUER cannot
// suggest that a provider is in the loop.
func (c Config) validateOIDCUse() error {
	if c.usesOIDC() {
		if err := c.OIDC.validate(); err != nil {
			return err
		}
		return c.validateOIDCRedirect()
	}
	for key, value := range map[string]string{
		"auth.oidc.issuer":        c.OIDC.Issuer,
		"auth.oidc.client_id":     c.OIDC.ClientID,
		"auth.oidc.client_secret": c.OIDC.ClientSecret,
		"auth.oidc.redirect_url":  c.OIDC.RedirectURL,
	} {
		if value != "" {
			return fmt.Errorf("auth.mode %q reads no OIDC setting, but %s is set", c.Mode, key)
		}
	}
	return nil
}

// validateOIDCRedirect accepts a request-relative redirect only for the
// explicit loopback development mode. The callback path is mounted separately,
// so a path-only redirect must name that same endpoint.
func (c Config) validateOIDCRedirect() error {
	raw := c.OIDC.RedirectURL
	if raw == "" {
		if !c.OIDC.AllowLoopbackHTTP {
			return errors.New("auth.oidc.redirect_url may be omitted only when auth.oidc.allow_loopback_http is set")
		}
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("auth.oidc.redirect_url is invalid: %w", err)
	}
	if parsed.IsAbs() {
		return nil
	}
	if !c.OIDC.AllowLoopbackHTTP {
		return errors.New("a path-only auth.oidc.redirect_url requires auth.oidc.allow_loopback_http")
	}
	if parsed.Path != raw || !strings.HasPrefix(raw, "/") || strings.Contains(raw, "//") ||
		strings.ContainsAny(raw, "\\\x00") {
		return fmt.Errorf("auth.oidc.redirect_url must be an absolute URL or a rooted local path, got %q", raw)
	}
	if raw != c.CallbackPath {
		return fmt.Errorf("auth.oidc.redirect_url path %q must match auth.callback_path %q", raw, c.CallbackPath)
	}
	return nil
}

// validateOAuthUse validates the provider login of ModeOAuthOnly, and refuses
// every one of its settings in a mode that mounts no such login: a provider
// credential a running deployment ignores reads as configured security.
func (c Config) validateOAuthUse() error {
	if c.usesOAuth() {
		if err := c.OAuth.validate(); err != nil {
			return err
		}
		return c.validateOAuthRedirect()
	}
	for key, value := range map[string]string{
		"auth.oauth.provider":      c.OAuth.Provider,
		"auth.oauth.client_id":     c.OAuth.ClientID,
		"auth.oauth.client_secret": c.OAuth.ClientSecret,
		"auth.oauth.redirect_url":  c.OAuth.RedirectURL,
	} {
		if value != "" {
			return fmt.Errorf("auth.mode %q reads no OAuth setting, but %s is set", c.Mode, key)
		}
	}
	return nil
}

// validateOAuthRedirect accepts a request-relative redirect only under the
// explicit loopback development allowance, exactly as the OIDC one does. The
// callback path is mounted separately, so a path-only redirect must name that
// same endpoint.
func (c Config) validateOAuthRedirect() error {
	raw := c.OAuth.RedirectURL
	if raw == "" {
		if !c.OAuth.AllowLoopbackHTTP {
			return errors.New("auth.oauth.redirect_url may be omitted only when auth.oauth.allow_loopback_http is set")
		}
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("auth.oauth.redirect_url is invalid: %w", err)
	}
	if parsed.IsAbs() {
		return nil
	}
	if !c.OAuth.AllowLoopbackHTTP {
		return errors.New("a path-only auth.oauth.redirect_url requires auth.oauth.allow_loopback_http")
	}
	if parsed.Path != raw || !strings.HasPrefix(raw, "/") || strings.Contains(raw, "//") ||
		strings.ContainsAny(raw, "\\\x00") {
		return fmt.Errorf("auth.oauth.redirect_url must be an absolute URL or a rooted local path, got %q", raw)
	}
	if raw != c.CallbackPath {
		return fmt.Errorf("auth.oauth.redirect_url path %q must match auth.callback_path %q", raw, c.CallbackPath)
	}
	return nil
}

// validatePasskeyUse validates the relying-party registration and the account
// lifecycle policies the selected mode needs.
func (c Config) validatePasskeyUse() error {
	if !c.usesPasskey() {
		for key, value := range map[string]string{
			"auth.passkey.rp_id":   c.Passkey.RPID,
			"auth.passkey.rp_name": c.Passkey.RPName,
		} {
			if value != "" {
				return fmt.Errorf("auth.mode %q mounts no passkey endpoint, but %s is set", c.Mode, key)
			}
		}
		if len(c.Passkey.Origins) != 0 {
			return fmt.Errorf("auth.mode %q mounts no passkey endpoint, but auth.passkey.origins is set", c.Mode)
		}
		return nil
	}

	if err := c.Passkey.validate(); err != nil {
		return err
	}
	// Enrollment is reachable in every passkey mode, and it is gated on how
	// recently the request proved its identity.
	if c.RecentAuthMaxAge <= 0 {
		return errors.New("auth.recent_auth_max_age must be positive when a passkey may be enrolled")
	}
	// A deployment with two login methods still has to say how a lost
	// credential is restored; one with a single method has to say both.
	if err := validateChoice("auth.recovery.policy", c.Recovery.Policy,
		RecoveryOIDC, RecoveryAdministrator, RecoveryApplication); err != nil {
		return err
	}
	if c.Mode == ModeOIDCPasskey {
		if c.Registration.Policy == "" {
			return nil
		}
		return validateChoice("auth.registration.policy", c.Registration.Policy,
			RegistrationDisabled, RegistrationOIDC, RegistrationInvite, RegistrationAdministrator, RegistrationOpen)
	}
	if c.Recovery.Policy == RecoveryOIDC {
		return fmt.Errorf("auth.recovery.policy %q needs an identity provider, which auth.mode %q has none of",
			RecoveryOIDC, ModePasskeyOnly)
	}
	if err := validateChoice("auth.registration.policy", c.Registration.Policy,
		RegistrationDisabled, RegistrationInvite, RegistrationAdministrator, RegistrationOpen); err != nil {
		return err
	}
	switch c.Registration.Policy {
	case RegistrationInvite, RegistrationAdministrator:
		return c.Bootstrap.validate()
	}
	return nil
}

// validate bounds the issued credential that opens a first enrollment.
func (b BootstrapConfig) validate() error {
	if b.IssueTTL <= 0 {
		return errors.New("auth.bootstrap.issue_ttl must be positive")
	}
	if b.EnrollmentTTL <= 0 {
		return errors.New("auth.bootstrap.enrollment_ttl must be positive")
	}
	if b.MaxAttempts <= 0 {
		return errors.New("auth.bootstrap.max_attempts must be positive")
	}
	return nil
}

func (p PasskeyConfig) validate() error {
	if !validRPID(p.RPID) {
		return fmt.Errorf("auth.passkey.rp_id %q must be a domain such as example.com or localhost, never an IP address", p.RPID)
	}
	if p.RPName == "" {
		return errors.New("auth.passkey.rp_name is required, because an authenticator shows it to the user")
	}
	if len(p.Origins) == 0 {
		return errors.New("auth.passkey.origins requires at least one origin")
	}
	for _, origin := range p.Origins {
		if err := validateOrigin(origin, p.RPID); err != nil {
			return err
		}
	}
	if err := validateChoice("auth.passkey.user_verification", p.UserVerification,
		UserVerificationRequired, UserVerificationPreferred, UserVerificationDiscouraged); err != nil {
		return err
	}
	return validateChoice("auth.passkey.discoverable", p.Discoverable,
		DiscoverableRequired, DiscoverablePreferred)
}

func validateChoice(key, value string, allowed ...string) error {
	if slices.Contains(allowed, value) {
		return nil
	}
	if value == "" {
		return fmt.Errorf("%s is required; choose one of %s", key, strings.Join(allowed, ", "))
	}
	return fmt.Errorf("%s must be one of %s, got %q", key, strings.Join(allowed, ", "), value)
}

// validRPID accepts a registrable domain shape. An IP literal is refused
// because WebAuthn scopes a credential to a domain, so an address can never be
// an RP ID even when it reaches a working server.
func validRPID(value string) bool {
	if value == "" || len(value) > 253 || net.ParseIP(value) != nil {
		return false
	}
	for label := range strings.SplitSeq(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := range len(label) {
			c := label[index]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
			default:
				return false
			}
		}
	}
	return true
}

// validateOrigin requires an https origin, or a loopback http origin for local
// development, whose host is the RP ID or a subdomain of it. A credential is
// scoped by the RP ID, so an origin outside that scope could never use it.
func validateOrigin(raw, rpID string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("auth.passkey.origins entry %q must be a bare scheme and host", raw)
	}
	host := parsed.Hostname()
	switch parsed.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(host) {
			return fmt.Errorf("auth.passkey.origins entry %q must be https unless its host is loopback", raw)
		}
	default:
		return fmt.Errorf("auth.passkey.origins entry %q must be https or loopback http", raw)
	}
	if host != rpID && !strings.HasSuffix(host, "."+rpID) {
		return fmt.Errorf("auth.passkey.origins entry %q is outside auth.passkey.rp_id %q", raw, rpID)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (o OIDCConfig) validate() error {
	if o.Issuer == "" || o.ClientID == "" || o.ClientSecret == "" {
		return fmt.Errorf("auth.oidc requires issuer, client_id, and client_secret")
	}
	if !strings.HasPrefix(o.Issuer, "https://") && !o.AllowLoopbackHTTP {
		return fmt.Errorf("auth.oidc.issuer must be https unless auth.oidc.allow_loopback_http is set")
	}
	if !validClaimName(o.IdentityClaim) {
		return fmt.Errorf("auth.oidc.identity_claim %q is not a usable claim name", o.IdentityClaim)
	}
	switch o.Admission {
	case AdmissionAuthenticated:
	case AdmissionRegistered:
		for _, claim := range o.RegisteredClaims {
			if !validClaimName(claim) {
				return fmt.Errorf("auth.oidc.registered_claims contains an invalid claim name %q", claim)
			}
		}
	case AdmissionExisting:
		if o.AutoProvision {
			return fmt.Errorf("auth.oidc.admission %q requires auto_provision = false", AdmissionExisting)
		}
	case AdmissionClaim:
		if o.Claim.Path == "" || len(o.Claim.Values) == 0 {
			return fmt.Errorf("auth.oidc.admission %q requires claim.path and claim.values", AdmissionClaim)
		}
		if o.Claim.Match != MatchAny && o.Claim.Match != MatchAll {
			return fmt.Errorf("auth.oidc.claim.match must be %q or %q", MatchAny, MatchAll)
		}
	default:
		return fmt.Errorf("auth.oidc.admission must be %q, %q, %q, or %q",
			AdmissionAuthenticated, AdmissionClaim, AdmissionRegistered, AdmissionExisting)
	}
	return nil
}

// validate checks the provider login of ModeOAuthOnly.
//
// The claim names are checked against the provider definition rather than only
// for shape, which the OIDC side cannot do: an issuer's claim set is whatever
// that issuer decides to put in a token, while a provider definition here emits
// a fixed list. Where the list is known, a claim that will never arrive is a
// configuration error, and finding it at startup is the difference between a
// deployment that does not start and one that refuses every login.
func (o OAuthConfig) validate() error {
	if o.Provider == "" {
		return fmt.Errorf("auth.oauth.provider must be set; the providers this build defines are %s",
			strings.Join(oauthprofile.Names(), ", "))
	}
	provider, ok := oauthprofile.Lookup(o.Provider)
	if !ok {
		return fmt.Errorf("auth.oauth.provider %q is not defined; the providers this build defines are %s",
			o.Provider, strings.Join(oauthprofile.Names(), ", "))
	}
	if o.ClientID == "" || o.ClientSecret == "" {
		return fmt.Errorf("auth.oauth requires client_id and client_secret for provider %q", o.Provider)
	}
	for _, scope := range o.Scopes {
		if !validOAuthScope(scope) {
			return fmt.Errorf("auth.oauth.scopes contains %q, which is not a scope token; list each scope separately rather than as one space-separated string", scope)
		}
	}
	if err := providerClaim("auth.oauth.identity_claim", o.IdentityClaim, provider); err != nil {
		return err
	}
	switch o.Admission {
	case AdmissionAuthenticated:
	case AdmissionRegistered:
		for _, claim := range o.RegisteredClaims {
			if err := providerClaim("auth.oauth.registered_claims", claim, provider); err != nil {
				return err
			}
		}
	case AdmissionExisting:
		if o.AutoProvision {
			return fmt.Errorf("auth.oauth.admission %q requires auto_provision = false", AdmissionExisting)
		}
	case AdmissionClaim:
		if o.Claim.Path == "" || len(o.Claim.Values) == 0 {
			return fmt.Errorf("auth.oauth.admission %q requires claim.path and claim.values", AdmissionClaim)
		}
		if o.Claim.Match != MatchAny && o.Claim.Match != MatchAll {
			return fmt.Errorf("auth.oauth.claim.match must be %q or %q", MatchAny, MatchAll)
		}
	default:
		return fmt.Errorf("auth.oauth.admission must be %q, %q, %q, or %q",
			AdmissionAuthenticated, AdmissionClaim, AdmissionRegistered, AdmissionExisting)
	}
	return nil
}

// providerClaim refuses a claim name the selected provider never emits, and
// names the ones it does. The list is short enough to print, and printing it is
// what turns a rejected login into a corrected line of configuration.
func providerClaim(key, claim string, provider oauthprofile.Provider) error {
	if !validClaimName(claim) {
		return fmt.Errorf("%s %q is not a usable claim name", key, claim)
	}
	reported := provider.Claims()
	if slices.Contains(reported, claim) {
		return nil
	}
	return fmt.Errorf("%s %q is not reported by provider %q, whose profile carries %s",
		key, claim, provider.Name, strings.Join(reported, ", "))
}

// validOAuthScope accepts the RFC 6749 scope-token grammar, which excludes the
// space that separates two of them. A deployment writing "users.read tweet.read"
// as one list entry would otherwise have it percent-encoded into a single scope
// no provider grants, and see the failure at the provider rather than here.
func validOAuthScope(scope string) bool {
	if scope == "" || len(scope) > 256 {
		return false
	}
	for index := range len(scope) {
		char := scope[index]
		if char == 0x21 || (char >= 0x23 && char <= 0x5b) || (char >= 0x5d && char <= 0x7e) {
			continue
		}
		return false
	}
	return true
}

// validClaimName accepts the shape of a top-level claim name. A claim compared
// as an account key is looked up by exact name, so a nested pointer, an empty
// name, or unprintable bytes are configuration mistakes.
func validClaimName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index := range len(value) {
		c := value[index]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '_', c == '-', c == '.':
		default:
			return false
		}
	}
	return true
}
