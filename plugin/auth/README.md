# plugin/auth

Browser authentication and bearer-token API authentication. Importing the
package registers the `[auth]` configuration binding and the framework
extensions that authenticate requests and guard protected paths.

| Slot | Middleware |
| --- | --- |
| `pw.SlotSession` | resolves the session cookie |
| `pw.SlotAuthentication` | serves the login, callback, and logout paths |
| `pw.SlotGuard` | rejects unauthenticated requests to protected paths |

```go
import _ "github.com/shibukawa/popcornweb/plugin/auth"
```

Nothing is installed unless `auth.enabled` is true, so an imported but disabled
package costs one configuration binding.

## Modes

- `oidc_only` uses Authorization Code with S256 PKCE, state, and nonce against
  one configured issuer.
- `oidc_passkey` uses OIDC to establish the account and permits later passkey
  login.
- `passkey_only` bootstraps the first passkey from an administrator-issued
  one-time credential.
- `oauth_only` signs a person in through a provider that speaks OAuth 2.0 and
  issues no ID Token, such as X. See [Provider login without an ID
  Token](#provider-login-without-an-id-token).
- `jwt_only` verifies an access token from `Authorization: Bearer …` on every
  request. It mounts no login endpoint and creates no session or cookie. This
  is the API-server mode.

`jwt_only` is not one of the browser-oriented `pw init --auth` values. Use
`pw init --preset=api-server`, or configure an existing project by hand. See
the [authentication guide](../../website/src/content/docs/guides/backend/authentication.md#jwt-only-api-servers).

## Tables

The tables this package owns are prefixed `popcornweb_` and are created by the
migration `MigrationSQL` publishes, which a project carries under
`MigrationName` at whatever version was free when the file was written:

- `popcornweb_authstate` — single-use state, nonce, and PKCE verifier of a
  pending login, consumed by the callback; each provider flow keeps its records
  under its own namespace
- `popcornweb_auth_allowlist` — identities registered before their first
  login, consulted only under `registered` admission

`sessionstore/sqlite` owns `popcornweb_session` through its own migration.
Startup verifies every one of them and refuses to serve when one is missing,
naming the migration to apply, so a forgotten migration fails immediately
rather than during a login.

## Flow

This is the OIDC flow. `oauth_only` mounts the same paths and differs at one
step; see [Provider login without an ID
Token](#provider-login-without-an-id-token).

`GET /auth/login` begins authorization and stores the opaque transaction key in
a short-lived cookie scoped to the callback path. The state, nonce, and PKCE
verifier never reach the browser.

`GET /auth/callback` consumes that cookie once, exchanges the code, verifies
the ID Token, applies admission, resolves the account, and rotates the session.
Every admission failure produces one response shape, so the endpoint does not
report whether an account exists.

`POST /auth/logout` revokes the stored session and expires the cookie. It
requires a same-origin submission. What it then does to the provider session is
`auth.oidc.logout_scope`: `reconfirm`, the default, sends the provider nothing
and marks the next authorization request to carry `prompt`, so the provider
still demands proof while every other relying party sharing it is untouched;
`global` additionally ends the provider session through RP-initiated logout.
There is no local-only scope — clearing only the local cookie leaves the next
login silent, which is the failure `reconfirm` exists to fix. A mode reaching no
provider session refuses a typed scope rather than binding it inert.

Discovery runs on the first login rather than at startup, so the application
starts even when the provider is not up yet, and a failed discovery is not
cached.

## Provider login without an ID Token

`oauth_only` is the mode for a provider that authenticates people but never
issues an ID Token. It states one provider name, which selects the endpoints,
the scopes, and the shape of the account response together:

```toml
[auth]
enabled = true
mode = "oauth_only"

[auth.oauth]
provider = "x"
redirect_url = "https://app.example/auth/callback"
```

`AUTH_OAUTH_CLIENT_ID` and `AUTH_OAUTH_CLIENT_SECRET` carry the credentials.
`contrib/oauthprofile` holds the provider definitions; `x` is the only one
today.

It serves the same `login_path` and `callback_path` the OIDC mode does, and one
step differs. The callback receives an access token addressed to the provider's
own API rather than a signed assertion about a person, so who signed in is
settled by asking the provider over one request carrying that token. Everything
after that point — admission, the account resolver, rotation, the landing path
— is the OIDC flow's, unchanged.

The access token is not stored. It answers one question during the callback and
is dropped, so the mode asks for no `offline.access` scope and keeps no
credential it has no reader for.

The session carries the provider account beside the local one: `Provider` names
the provider, `Subject` is that provider's own identifier — the X user id — and
`Username` and `AvatarURL` are copies taken at login for display. `DisplayName`
and `Email` come from the account resolver, as they do after an OIDC login; the
default resolver reads the provider's name, and X reports no address.

```go
if data, ok := auth.Session(ctx); ok && data.Provider == "x" {
	// data.Subject is the X user id; data.Username is the handle.
}
```

`auth.oauth.identity_claim` selects the account link, defaulting to `sub`. A
handle is the tempting alternative and the wrong one: X lets a handle be
renamed, and lets a released one be claimed by somebody else, so an account
linked to a handle eventually becomes somebody else's account. A claim name the
provider definition does not report is refused at startup, naming the ones it
does, so a name copied out of the provider's own API reference fails there
rather than at every login.

Three things this mode does not have, because the protocol does not:

- **A global sign-out.** There is no end session endpoint, so `logout_path`
  revokes the local session and stops. The provider stays signed in, and the
  next login may be answered from that session without a word.
- **Step-up re-authentication.** Re-proof is `max_age` going out and a verified
  `auth_time` coming back, and a provider issuing no ID Token reports neither.
  `auth.Confirmed` and a zero window answer 503 and log the mode rather than
  redirecting into a login that cannot converge. `auth.MaxAge`, measured from
  the login, works normally.
- **`auth.shared_device`.** It couples a global sign-out with a login the
  provider may not answer silently, and neither exists here, so it is refused
  rather than half-honored.

## Which claim identifies an account

`auth.oidc.identity_claim` names the verified claim that identifies a local
account. It defaults to `sub`, the only claim OpenID Connect guarantees is
stable and unique per issuer.

A subject is generated by the provider, so a deployment that provisions people
in advance rarely knows one. Directories are commonly given their own stable
identifier for exactly this reason — an employee number, a staff id — and that
claim is what an operator can register, hand out, and reconcile against an HR
system:

```toml
[auth.oidc]
identity_claim = "employee_number"
```

`auth.Identity` then carries `KeyClaim` and `Key`, which the account resolver
links on and which the login session records. The subject stays available.

The chosen claim becomes the account link, so it must be **stable for the life
of the account and unique within the issuer**. A value that is reissued to
another person hands them the first person's account, and changing the setting
after accounts exist orphans every account linked by the previous claim.

A login whose token does not carry the configured claim, or carries it in an
unusable shape, is refused rather than falling back to the subject: a silent
fallback would create a second account for the same person. A string is used as
it is, an integer as its literal text, and every other JSON shape — including a
fractional number — is refused rather than normalized.

## Admission and accounts

`auth.oidc.admission` decides whether a verified identity may enter:
`authenticated` admits every identity the issuer verifies, `claim` admits a
verified claim match, `registered` admits an identity listed in
`popcornweb_auth_allowlist`, and `existing` admits only an identity the
resolver already knows and forbids provisioning.

`registered` is the closed-deployment mode. An operator inserts one row per
permitted identity, naming a claim and its expected value:

```sql
INSERT INTO popcornweb_auth_allowlist (issuer, claim, value, note)
VALUES ('https://issuer.example', 'employee_number', 'E-10231', 'first operator');
```

`auth.oidc.registered_claims` selects the compared claims and defaults to the
configured `identity_claim` alone, because that is the value a deployment knows
in advance. List further claims to also recognize someone registered by another
attribute, such as an email address during a migration. A lookup failure is
reported as an error rather than a denial, so a database outage cannot silently
change who may log in.

`SetAccountResolver` installs the application resolver. It receives the verified
identity — issuer, lookup claim and value, subject, and claims — plus whether
policy permits provisioning, and returns the local account or
`ErrUnknownIdentity`. Without a resolver, a stable opaque identifier is derived
from the issuer and the lookup claim and value.

## Development-only settings

`auth.oidc.allow_loopback_http` permits an `http` issuer on loopback, which is
what [`contrib/devidp`](../../contrib/devidp/README.md) serves. Together with
`session.cookie.secure = false` it belongs to development configuration only.

`pw dev` starts that provider and injects `AUTH_OIDC_ISSUER`,
`AUTH_OIDC_CLIENT_ID`, and `AUTH_OIDC_CLIENT_SECRET`, so a project commits no
issuer or credential.

See [examples/oidclogin](../../examples/oidclogin) for a working application.
