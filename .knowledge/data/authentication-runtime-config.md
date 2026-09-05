---
id: data:authentication-runtime-config
type: data
title: Authentication Runtime Config
---
The `[auth]` binding selects the login method — OIDC, passkey, plain OAuth provider, or bearer token — and the bootstrap, linking, registration, and recovery policy around it, through shared dotted prefixes.

```yaml
registration: popcornweb/plugin/auth registers this binding when imported
fields:
  enabled: bool
  backend: rdb or dynamo, default rdb, selecting the storage of all four framework-owned authentication stores, per decision:auth-backend-selection
  mode: oidc_passkey, oidc_only, passkey_only, oauth_only, or jwt_only
  login_path: path
  callback_path: path
  protection.include: path pattern list
  protection.exclude: path pattern list
  protection.unauthenticated: redirect or unauthorized
  registration.policy: disabled, oidc, invite, administrator, or open
  recovery.policy: oidc, administrator, or application
  recent_auth_max_age: duration
  session.ttl: absolute session lifetime, moved here by decision:session-lifetime-owned-by-auth; the auth.session prefix is its own binding, whose struct lives in popcornweb/sessionconfig so pw can enforce it without importing this package
  session.idle_timeout: optional inactivity lifetime, never later than session.ttl
  session.renewal_interval: how often an active session is touched
  shared_device: bool, default false, per policy:shared-device-mode
  bootstrap.issue_ttl: duration an issued secret stays redeemable, measured from issuance
  bootstrap.enrollment_ttl: duration the enrollment stays open, measured from redemption
  bootstrap.max_attempts: positive integer
  oidc.issuer: URL
  oidc.client_id: string
  oidc.client_secret: secret
  oidc.redirect_url: URL
  oidc.scopes: string list
  oidc.identity_claim: claim name
  oidc.admission: existing, claim, registered, or authenticated
  oidc.auto_provision: bool
  oidc.claim.path: JSON Pointer
  oidc.claim.values: string list
  oidc.claim.match: any or all
  oidc.allow_loopback_http: bool
  oidc.registered_claims: string list
  oidc.logout_scope: enum of reconfirm or global, default reconfirm, per policy:provider-session-scope
  oidc.allow_global_logout_request: bool, default false, permitting a logout request to escalate to global
  oidc.provider_logout: removed; a configuration still carrying it fails startup, because configbind would otherwise ignore it silently
  logout_path: path
  post_login_path: path
  oauth.provider: name of a requirement:contrib-oauthprofile definition, no default, per decision:oauth-provider-definitions-are-built-in
  oauth.client_id: string
  oauth.client_secret: secret
  oauth.redirect_url: URL
  oauth.scopes: string list; empty asks for the provider's own minimum login set, and a stated list replaces rather than extends it
  oauth.identity_claim: claim name, default sub, refused unless the provider definition reports it
  oauth.admission: existing, claim, registered, or authenticated, default authenticated
  oauth.auto_provision: bool, default true
  oauth.claim.path: JSON Pointer
  oauth.claim.values: string list
  oauth.claim.match: any or all
  oauth.registered_claims: string list, each refused unless the provider definition reports it
  oauth.allow_loopback_http: bool, development only, permitting a request-relative redirect URL on loopback
  passkey.path: base path of api:passkey-endpoints
  passkey.rp_id: domain
  passkey.rp_name: string
  passkey.origins: URL list
  passkey.user_verification: required, preferred, or discouraged
  passkey.discoverable: required or preferred
  jwt.issuer: URL of the one authorization server this API trusts
  jwt.discovery: openid, oauth, or manual, selecting which metadata document supplies jwks_uri
  jwt.jwks_uri: URL, read only for manual discovery
  jwt.audience: non-empty string list naming this API
  jwt.audience_match: any or all, default any
  jwt.algorithms: string list, default RS256
  jwt.required_token_type: typ header value, default at+jwt, empty to accept an absent typ
  jwt.required_scopes: string list, its own field for the reason policy:access-token-verification gives
  jwt.leeway: duration, default 30s, bounded
  jwt.max_token_lifetime: duration, required; it bounds the exp minus iat sanity check and the lifetime of a subject-form revocation entry
  jwt.max_token_bytes: positive integer, bounded
  jwt.jwks_refresh_cooldown: duration bounding unknown-kid refreshes
  jwt.allow_loopback_http: bool, development only
  jwt.identity_claim: claim name, default sub
  jwt.admission: existing, claim, registered, or authenticated; no default, because the permissive answer would be the silent one
  jwt.auto_provision: bool, default false
  jwt.claim.path: JSON Pointer
  jwt.claim.values: string list
  jwt.claim.match: any or all
  jwt.revocation.mode: off, token, subject, or both, with no default; it replaces the enabled bool and the forms list of the first draft, because two fields could disagree and one cannot, and because stating off is what makes running without a revocation path a decision on the page rather than an omission
  jwt.revocation.on_unavailable: refuse or admit, default refuse
  jwt.revocation.max_propagation_delay: duration bounding a per-process cache, default zero, and refused entirely when the mode is off
  jwt.revocation.storage: not a field of its own; the list is data:revoked-token-record in a table rule:framework-owned-tables creates, so there is no second selector to keep consistent with the allowlist beside it, and it follows the auth.backend answer like every other framework-owned store, per decision:auth-backend-selection
  jwt.dev.trust_unverified_tokens: bool, default false, per policy:dev-token-relaxation; a binary built without the pwdev mode fails startup on its presence rather than ignoring it
mode_validation:
  principle: validate and read only the fields the selected mode uses, and refuse a field the mode cannot honor, because a silently ignored provider setting reads as configured security
  oidc_only:
    required: the oidc issuer, client_id, client_secret, and redirect_url set today
    refused: every passkey field, because no ceremony endpoint exists to use it
  oidc_passkey:
    required: the oidc_only set, plus passkey.rp_id and passkey.origins
    required_policy: recovery.policy, because two login methods make credential loss recoverable in more than one way
    defaulted: registration.policy oidc, because the OIDC login is the account bootstrap
  passkey_only:
    required: passkey.rp_id and passkey.origins
    required_policy: registration.policy and recovery.policy, both explicit, because no identity provider can stand in for either
    required_bootstrap: bootstrap.issue_ttl, bootstrap.enrollment_ttl, and bootstrap.max_attempts when registration.policy is administrator or invite
    bootstrap_naming: issue_ttl rather than credential_ttl, because the two durations bound consecutive phases and the name should say which; a leading noun also kept it out of the secret-redaction match
    refused: every oidc field, so a leftover AUTH_OIDC_ISSUER cannot suggest a provider is in the loop
  oauth_only:
    flow: flow:oauth-provider-login
    required: oauth.provider, oauth.client_id, oauth.client_secret, and oauth.redirect_url unless oauth.allow_loopback_http
    claim_names: oauth.identity_claim and oauth.registered_claims are checked against the provider definition, because a definition reports a fixed claim set and a name that will never arrive would refuse every login instead of failing at startup
    scopes: each entry must be an RFC 6749 scope token, so a space-separated list written as one string is refused rather than sent as one scope no provider grants
    admission: policy:oauth-admission
    refused: every oidc and passkey field, shared_device, which couples a global sign-out and a non-silent login that this protocol has neither of, and a typed logout_scope or allow_global_logout_request, per policy:provider-session-scope
    logout: reaches the local session only; what the mode does at runtime is flow:oauth-provider-login
  jwt_only:
    requirement: requirement:jwt-only-api-authentication, reachable only by writing this section, per decision:jwt-only-mode-not-scaffolded
    required: jwt.issuer, a non-empty jwt.audience, jwt.algorithms, jwt.admission, jwt.max_token_lifetime, and jwt.revocation.mode
    required_reason: each has a permissive answer that would otherwise arrive as a default nobody typed, and the mode refuses to supply one
    required_conditional: jwt.jwks_uri for manual discovery, and jwt.claim for claim admission
    algorithms_note: it is required rather than defaulted to RS256, partly because which signatures a deployment trusts is not a question to inherit an answer to, and partly because configbind refuses a default on a slice at all
    protection: refuses protection.unauthenticated redirect, because there is no login path to send a bearer client to
    csrf: refuses security.csrf.enabled, because the check needs a secret in a session slot and this mode creates no session; an exemption keyed on the Authorization header was rejected as a bypass in any deployment that also authenticates by cookie
    forced: protection.unauthenticated must be unauthorized, because there is nowhere to redirect a client the framework did not authenticate
    refused: every oidc and passkey field, plus login_path, callback_path, logout_path, post_login_path, and the registration, recovery, and bootstrap policies, because this mode runs no ceremony and creates no account
    session_fields: refused as well; session.ttl, session.idle_timeout, and session.renewal_interval bound a login this mode does not perform, and a token carries its own expiry
    revocation_backend: cookie is refused, per policy:token-revocation
    development: jwt.dev is accepted only under the locks of policy:dev-token-relaxation, and jwt.issuer, jwt.audience, and the rest stay required even then, so turning the relaxation off leaves a configuration that still serves
  shared:
    - session.idle_timeout must not exceed session.ttl, and session.renewal_interval must be shorter than both
    - a guard window of api:assurance-guard longer than session.ttl is refused, because a requirement no live session can satisfy is a configuration error rather than a permanent challenge loop
    - passkey.rp_id must be a registrable domain or localhost, never an IP literal, because an IP cannot be an RP ID
    - every passkey.origins entry must be https, or loopback http under the same allowance oidc.allow_loopback_http already carries
    - every passkey.origins entry must have passkey.rp_id as its registrable suffix
    - recent_auth_max_age must be positive whenever enrollment is reachable
implemented:
  package: popcornweb/plugin/auth registered through api:framework-extension
  mode: oidc_only, oidc_passkey, passkey_only, and oauth_only
  endpoints: login_path begins authorization, callback_path completes it, logout_path revokes the local session and ends the provider session
  logout_method: POST only, same-origin checked, because a logout reachable by link or prefetch is a denial-of-service surface
  correlation: the opaque transaction key rides a short-lived cookie scoped to callback_path
  discovery: deferred to the first login and not cached on failure
  storage: requires session backend rdb over sqlite, because correlation records use requirement:contrib-auth-state-sqlite
  tables: rule:framework-owned-tables migrations, verified at startup
binding_implemented:
  fields: registration, recovery, recent_auth_max_age, bootstrap, and the whole passkey prefix are bound and validated
  validation: mode_validation above is enforced, so a passkey mode is refused for a bad relying-party registration before anything serves
  tables: popcornweb_passkey_credential and popcornweb_auth_bootstrap exist under rule:framework-owned-tables
  modes: all three browser modes serve; there is no remaining implementation gate
  jwt_only: designed, per requirement:jwt-only-api-authentication; the whole jwt prefix and its mode_validation entry are specified and not yet bound
  reason: the rules outlive the implementation status, so they were written and tested before the endpoints that needed them
planned:
  testing: decision:test-authentication-seams
  assurance: data:session-assurance-state adds the auth.assurance prefix, and recent_auth_max_age becomes its default rather than a passkey-only field
  failure_thresholds:
    keys: how many failed attempts one account or one session may accumulate, and the period they are counted over
    why_here: policy:reauthentication requires the bound and only this plugin knows what a failure is; the counter it is evaluated against comes from data:rate-limit-runtime-config, which declares no threshold of its own
    shape: a long period and a small count, unlike the arrival rate that binding declares, which is why the two are not one key group
deferred:
  - policy:csrf-protection, until then api:passkey-endpoints relies on same-origin and a required JSON content type
loopback_development:
  field: oidc.allow_loopback_http
  effect: permits an http issuer whose host is loopback
  restriction: development only, and paired with session cookie secure false
development_injection:
  source: api:cli-dev starting requirement:contrib-devidp
  binding: the injected names are the ones plugin/auth already reads, so no development TOML edit is required
  variables:
    - AUTH_OIDC_ISSUER
    - AUTH_OIDC_CLIENT_ID
    - AUTH_OIDC_CLIENT_SECRET
  precedence: data:loaded-configuration ranks environment above TOML
  issuer_scheme: oidc.allow_loopback_http is required, because the development issuer is loopback http
rules:
  - one mode per application; a browser login and jwt_only are not combined, because two ways to become authenticated make every guard ambiguous
  - validate and read only the section the selected mode uses; the oidc, oauth, and jwt sections declare the same admission settings, so one accessor resolves which one is in force
  - decision:authentication-bootstrap-strategy defines mode behavior
  - policy:authenticated-path-protection defines pattern matching and middleware behavior
  - redirect response targets the local login_path
  - unauthorized response returns HTTP 401 without navigation
  - validate only provider and policy fields used by the selected mode
  - existing admission requires auto_provision false
  - claim admission requires a non-empty path and values
  - authenticated admission with auto_provision permits every verified issuer subject to create an account
  - require data:session-runtime-config when provider flow needs login continuity; that binding places the storage and this one bounds its lifetime, per concept:session-storage-boundary
  - supply the session.ttl, session.idle_timeout, and session.renewal_interval durations to api:session-manager at construction, so the session package holds no default of its own
  - redact provider secrets
  - derive oidc.redirect_url from the request only under the development_injection conditions, never from a forwarded or non-loopback host
  - verified request results become data:request-authentication
  - passkey-only mode requires explicit registration and policy:account-recovery settings
  - administrator registration and recovery require bounded bootstrap settings
flows:
  - flow:oidc-account-login
  - flow:oauth-provider-login
  - flow:passkey-enrollment
  - flow:passkey-login
  - flow:passkey-only-registration
security:
  - policy:account-linking
  - policy:account-recovery
  - policy:bootstrap-credential-security
  - policy:oidc-admission
  - policy:oauth-admission
  - policy:authenticated-path-protection
  - policy:access-token-verification
  - policy:bearer-admission
  - policy:token-revocation
```
