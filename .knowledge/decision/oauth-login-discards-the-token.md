---
id: decision:oauth-login-discards-the-token
type: decision
title: The OAuth Login Discards The Access Token
---
flow:oauth-provider-login keeps no access or refresh token past the callback, so the mode signs a person in and grants the application no delegated access.

```yaml
status: accepted
context:
  - the mode obtains an access token addressed to the provider's own API, which OIDC never asks the login to hold
  - flow:oidc-account-login already rules that tokens are not stored unless application functionality explicitly requires them
  - a login is the functionality here, and one profile request satisfies it
decision:
  - the token is used for one bounded profile request and dropped
  - no offline.access or equivalent scope is requested, because a refresh token would have no reader
  - data:session-record carries the provider account, never a provider credential
reason:
  - a credential kept with no reader is a breach with no purpose
  - the scopes a login asks for are what the consent screen shows the user, and asking for delegated access to sign somebody in overstates what the application will do
  - storing a token would pull in encryption at rest, refresh scheduling, revocation on logout, and failure handling for each, none of which a login needs
consequence:
  - an application that wants to act on the user's behalf runs its own authorization flow over requirement:contrib-oauth and owns that token
  - a session cannot be used to call the provider API, and nothing in the framework suggests it can
reopen_when:
  - a framework capability itself needs to call a provider API on a signed-in user's behalf
```
