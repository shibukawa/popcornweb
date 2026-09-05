---
id: requirement:contrib-oauthprofile
type: requirement
title: TinyGo OAuth Profile Client
---
contrib/oauthprofile resolves the account behind an OAuth 2.0 access token for providers that authenticate people and issue no ID Token, supplying what requirement:contrib-oauth leaves to the provider.

```yaml
package: contrib/oauthprofile
scope: provider endpoint constants, a declarative profile mapping, and one bounded account request
public_api:
  - Lookup(name) returns a copy of a built-in Provider definition
  - Names() lists the defined provider names in a stable order
  - Provider.Claims() lists the claim names a definition reports, sorted
  - Provider.OAuthConfig(client id, client secret, redirect uri) fills the endpoint half of an oauth.Config
  - Fetch(context, provider, access token, options) returns data:oauth-provider-profile
providers:
  x:
    issuer: https://x.com
    authorization_endpoint: https://x.com/i/oauth2/authorize
    token_endpoint: https://api.x.com/2/oauth2/token
    profile_endpoint: https://api.x.com/2/users/me
    scopes: users.read and tweet.read, which X grants only together
    offline_access: not requested, because decision:oauth-login-discards-the-token keeps no refresh token
    profile_root: /data
required:
  - endpoints are build constants, because such a provider publishes no metadata document to discover
  - a provider is described by a profile root and a claim-to-member mapping rather than by code
  - decoded claims use one vocabulary across providers, with the provider identifier under sub
  - a claim is read as a string or as the literal text of a whole number; every other shape is omitted
  - an absent, empty, or unusable member is omitted rather than carried, so a claim rule cannot match what the provider did not say
  - a profile with no subject is refused, because an account link needs one
  - the access token goes to the provider profile endpoint and nowhere else
  - redirects are disabled for the profile request
  - bounded response size, request duration, and copied display value
  - a display value over the bound is dropped rather than truncated, and the login still succeeds on the identifier
  - errors carry no response text
deferred:
  - refresh and revocation
  - any provider API beyond the endpoint reporting the account
  - application-registered provider definitions
security: policy:oauth-security through requirement:contrib-oauth
compatibility: policy:contrib-compatibility
```
