---
id: policy:session-downgrade
type: policy
title: Session Downgrade and Logout Presentation
---
An ended session may leave a non-authoritative hint of who was signed in, which shortens the next login while carrying no authority at all.

```yaml
status: implemented
implemented:
  storage: a sealed api:cookie-jar over its own keyring, so the payload is server-readable only and may therefore hold a login identifier
  config: the auth.assurance.hint prefix, off by default, refused beside policy:shared-device-mode
  write: a completed login stores display name, login identifier, issuer, and the login time
  read: auth.Hint returns it, and a hint past either bound is cleared rather than shown
  forget: a POST-only same-origin endpoint under the logout path, needing no session, which is the not-me control
  masking: auth.MaskIdentifier keeps the first character and the domain and replaces the rest with a fixed run, so the mask does not disclose the length either
  explicit_logout: clears the hint, because a user who signed out asked to be forgotten
application_surface: a login screen calls auth.Hint and auth.MaskIdentifier; no handler branches on the level itself
division_of_memory:
  provider_supplies: which account at that issuer, through the select_account prompt of policy:reauthentication, which works on every device and cannot go stale
  hint_supplies: which issuer was used last, which no provider can supply because an issuer knows nothing about the other issuers a deployment offers
  single_issuer: the provider covers the whole of the identified level, and the hint adds only an avatar, so it is close to redundant
  multiple_issuers: the login screen must otherwise show an issuer picker on every visit, and only the hint can skip it, so the hint carries the part of identity the protocol cannot
  passkey_only: no provider exists, so the hint supplies the login identifier the ceremony asks the user to type
  layered: a returning browser skips the issuer picker from the hint and then skips the account picker from the prompt, each supplying what the other cannot
  condition: the provider half requires the provider session to survive the logout, which the reconfirm mode of policy:provider-session-scope preserves and the global mode destroys
  priority: the prompt path first, because it needs no stored state; the hint once a deployment offers more than one issuer or runs passkey_only
hint:
  contains: what the login screen needs to shorten the next sign-in, which is a display label, an avatar reference, the login identifier a passkey deployment asks the user to type, and the issuer of the last successful login
  form: a separate cookie sealed under policy:cookie-value-protection, never the session cookie
  readable_by: the server only, because sealing is encryption; the constraint on identifiers therefore belongs to what the login screen renders, not to what the cookie carries
  authority: none; data:request-authentication stays unauthenticated and policy:authenticated-path-protection still denies
config:
  auth.assurance.hint.enabled: bool, default false
  auth.assurance.hint.name: cookie name
  auth.assurance.hint.ttl: absolute duration, bounded well below the account lifetime
  auth.assurance.hint.idle_timeout: duration since the last successful login, after which the hint is discarded
two_axis_expiry:
  reason: a hint outlives a session by design, so it needs its own bounds rather than inheriting the ones policy:session-security applies to a session
  shape: absolute and idle, the same pair a session uses and for the same reasons
  example: a consumer deployment keeps the hint for months absolute and two weeks since the last login, so a returning user is recognized and a departed one is forgotten
  transition: exceeding either bound drops the browser from the identified level of concept:assurance-axes to anonymous, where the login screen offers no account and no issuer
sources:
  expiry: absolute or idle expiry may leave a hint
  explicit_logout: leaves no hint, because a user who signed out asked to be forgotten
  opt_in: an explicit remember-me choice at login is the only way an explicit logout keeps one
removal:
  - a not-me control on the login screen clears the hint, needing no session and no authentication
  - clearing is idempotent
provider_interaction:
  fact: what a logout does to the provider session is auth.oidc.logout_scope, whose default reconfirm leaves that session alive, per policy:provider-session-scope
  consequence: under the default the provider still knows the user, so the hint and the provider's own account picker agree, and the hint shortens a sign-in the provider will still demand proof for
  under_global: the provider has forgotten the user too, so a local hint is the only identified level available and also the least useful one
  caution: a hint surviving a global logout can read as still signed in, so its presentation states signed out
  no_provider: flow:oauth-provider-login reaches no provider session at all, so the hint is the only identified level there whatever a deployment writes
risks:
  shared_device: a hint discloses the last user of a browser to the next one
  enumeration: the hint is read only from the cookie and never resolved from a request parameter, so it cannot probe for accounts
  issuer_disclosure:
    problem: a remembered issuer names an affiliation, and on a multi-issuer login screen it is disclosed by which button is offered
    not_maskable: an address renders masked and an issuer does not, because the button either appears or does not, so partial rendering has no meaning
    control: the lifetime and the enabled flag, not the presentation
privacy_dial:
  question: how long a browser may remember who used it, answered by one number
  zero_or_disabled: the browser leaves the active level for anonymous directly, never passing through identified, and the login screen offers no account and no issuer
  default: disabled, so a deployment that has not considered shared devices already behaves this way
  raising_it: a deliberate choice that a deployment makes about the devices its users actually have
  insufficient_alone: disabling the hint does not stop the provider from naming the previous user at the next login, so policy:shared-device-mode couples it with the settings that do
rules:
  - the hint prefills a choice the provider still verifies; it never selects an account for a login
  - the hint is the identified level of concept:assurance-axes and grants nothing
  - a hint whose account was disabled or deleted is discarded at the next login rather than reported
  - the hint carries nothing derived from a session token, a token hash, or a CSRF secret
  - a rendered identifier is masked, because the disclosure risk is what the next person on the device reads rather than what the sealed cookie holds
  - the hint is discarded rather than refreshed when a login lands on a different account, so one browser does not accumulate a history of who used it
  - a downgrade to the hint revokes the server record exactly as logout does; a cookie backend cannot, per decision:cookie-session-storage
```
