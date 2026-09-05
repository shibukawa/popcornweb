---
id: flow:oauth-provider-login
type: flow
title: OAuth Provider Login Without An ID Token
---
The oauth_only mode of data:authentication-runtime-config signs a person in through a provider that issues no ID Token, by asking that provider who its own access token belongs to.

```yaml
difference_from_oidc: flow:oidc-account-login verifies a signed assertion about a person; this reads an account from the provider over a request carrying the token
flow:
  - begin requirement:contrib-oauth authorization code with S256 PKCE and state; no nonce, because nothing will carry one back
  - consume callback state through requirement:contrib-auth-state, under a namespace of this flow's own
  - exchange the code for an access token addressed to the provider's own API
  - read data:oauth-provider-profile through requirement:contrib-oauthprofile
  - build the identity with the issuer of the provider definition, never one the response named
  - apply policy:oauth-admission to that identity and its claims
  - resolve or provision through the account resolver of api:authentication-endpoints
  - create or rotate api:session-manager session, recording the provider account beside the local one
  - record data:request-authentication method as oauth
rules:
  - trust here is transport rather than signature: the token came from a PKCE-bound exchange this deployment started, and the profile was read from the provider over TLS with it
  - the access token is discarded after the profile request, per decision:oauth-login-discards-the-token
  - a profile endpoint that fails after a successful exchange is an outage, answered 503, never a denial
  - every admission failure produces one response shape, so the endpoint reports nothing about whether an account exists
absent_by_protocol:
  provider_logout: no end session endpoint exists, so policy:provider-session-scope has no global scope here and the logout reaches the local session only
  reconfirm: no prompt parameter exists, so the logout writes no reconfirm cookie and the next login may be answered from the provider session without a word
  step_up: no max_age goes out and no auth_time comes back, so flow:step-up-reauthentication cannot run and a guard requiring one answers 503 rather than redirecting; see decision:assurance-scope-oidc-only
  refused_configuration: data:authentication-runtime-config states which keys are refused for these reasons before anything serves
```
