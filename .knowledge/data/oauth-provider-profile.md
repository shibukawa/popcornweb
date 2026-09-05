---
id: data:oauth-provider-profile
type: data
title: OAuth Provider Profile
---
The account a provider reports for one access token, normalized by requirement:contrib-oauthprofile onto a vocabulary shared by every provider.

```yaml
fields:
  subject: the provider's own stable account identifier; the only claim a provider owes and the only one an account may be linked to
  username: the handle, where the provider has one; display and support material, not an account link
  name: display name
  avatar_url: profile image, when the provider returned one
  claims: the decoded profile as a claim set, with the identifier under sub
vocabulary:
  sub: the provider identifier, X's id
  preferred_username: the handle, X's username
  name: the display name
  picture: the profile image, X's profile_image_url
rules:
  - one name per value: a provider's own spelling is renamed rather than carried alongside, so admission has one thing to be configured against
  - a handle is renameable and, on X, reclaimable by another person, so an account linked to one eventually becomes somebody else's account
  - username, name, and avatar_url are copies taken at login and go stale when the user renames themselves
  - the plugin/auth slot of api:session-registry carries these beside the account summary of api:authentication-endpoints, and no provider credential
```
