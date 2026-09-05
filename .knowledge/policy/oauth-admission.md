---
id: policy:oauth-admission
type: policy
title: OAuth Admission Policy
---
flow:oauth-provider-login establishes who the provider says this is; admission separately decides whether that identity may enter, over a claim set that arrived from an endpoint rather than inside a signed token.

```yaml
relation: the modes and the claim_rule of policy:oidc-admission, applied to data:oauth-provider-profile instead of to a verified ID Token
what_differs:
  verification_precondition: policy:oidc-admission evaluates after issuer, audience, signature, nonce, time, and subject verification; here there is no signature and no audience to check, so the precondition is a PKCE-bound exchange this deployment started and a TLS response from the provider's own endpoint
  issuer: taken from the provider definition of requirement:contrib-oauthprofile, never from a field of the response, so one compromised provider cannot claim another's accounts
  claim_set: the decoded profile, which is a fixed set per provider rather than whatever an issuer chose to put in a token
  keys: auth.oauth.identity_claim, auth.oauth.claim, auth.oauth.registered_claims, and auth.oauth.auto_provision
  allowlist: the popcornweb_auth_allowlist table of policy:oidc-admission, unchanged, keyed by the provider issuer
identity_key:
  claim: auth.oauth.identity_claim, defaulting to sub, which every provider definition fills with that provider's own stable identifier
  refused_early: a claim name the provider definition does not report is refused at startup, because the reported set is known in advance; policy:oidc-admission cannot make the same check, since an issuer's claim set is whatever that issuer decides to send
  never_a_handle: a handle is renameable and reclaimable, so an account linked to one eventually resolves to a different person; the stability contract is data:external-identity
provisioning: controlled by auth.oauth.auto_provision and registration policy, as in policy:oidc-admission
rules:
  - apply admission on every login, including one that resolves to an existing account
  - deny a login whose profile lacks a usable value for the configured identity claim, rather than falling back to another claim
  - answer every admission failure with one enumeration-safe response
  - a profile endpoint that fails after a successful token exchange is an outage answered 503, never an admission denial
boundary:
  - the trust established here is transport trust, which is weaker than a signature and is the ordinary basis for this class of login
  - a deployment needing cryptographic proof of who authenticated needs an OIDC issuer, per decision:oauth-provider-definitions-are-built-in
```
