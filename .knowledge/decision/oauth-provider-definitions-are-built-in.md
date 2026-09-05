---
id: decision:oauth-provider-definitions-are-built-in
type: decision
title: OAuth Providers Are Named, Not Configured
---
auth.oauth names one provider whose endpoints, scopes, and profile shape come from requirement:contrib-oauthprofile, rather than a deployment configuring the endpoints itself.

```yaml
status: accepted
context:
  - a provider that issues no ID Token publishes no metadata document, so nothing can be discovered
  - the four facts a login needs — authorization endpoint, token endpoint, profile endpoint, and the shape of the profile — always move together
  - a profile shape cannot be expressed as a URL, so configuring endpoints alone would still leave the deployment unable to say what the response means
decision:
  - a deployment states auth.oauth.provider and its own credentials, and nothing else about the provider
  - a definition is declarative: a profile root as a JSON Pointer and a claim-to-member mapping, so adding a provider is data rather than code
  - auth.oauth.identity_claim and registered_claims are checked at startup against Provider.Claims
  - x is the first and currently only definition
reason:
  - four settings that must agree are four chances for a deployment to get one wrong, and the failure is a login that authenticates against one provider and reads a profile from another
  - the same table is what makes a claim name checkable before anybody logs in; free-form endpoints would leave the claim set unknown until the first response
  - one name per value across providers is what lets policy:oidc-admission, the account resolver, and the session slot be written once
alternatives_rejected:
  - configured authorization, token, and profile endpoints, for the reasons above
  - carrying each provider's own claim spelling beside the shared one, which would give one value two names and two ways to configure admission
scope:
  mode: one standalone auth.mode value rather than a combination with OIDC or passkey
  reason: each combination multiplies the mode enum and the login paths, and neither has been asked for; decision:authentication-bootstrap-strategy already fixes one bootstrap per deployment
reopen_when:
  - an application needs a provider the framework has not defined, which turns Provider into a registered extension point rather than a lookup
  - a deployment needs two providers at once, which needs a login path per provider before anything else
```
