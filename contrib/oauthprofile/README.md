# contrib/oauthprofile

`oauthprofile` answers the question plain OAuth 2.0 leaves open: who does this
access token belong to? It carries the endpoint constants and the response
shape of each provider that signs a person in without ever issuing an ID Token,
and normalizes what comes back onto one claim vocabulary.

```go
provider, ok := oauthprofile.Lookup(oauthprofile.ProviderX)
client, err := oauth.NewClient(
	provider.OAuthConfig(clientID, clientSecret, "https://app.example/auth/callback"),
	oauth.Options{StateStore: store})
// … begin authorization, receive the callback, exchange the code …
profile, err := oauthprofile.Fetch(ctx, provider, tokens.AccessToken, oauthprofile.Options{})
// profile.Subject is the provider's own stable identifier.
```

The exchange itself belongs to [`contrib/oauth`](../oauth), which this package
does not wrap: `OAuthConfig` fills in the endpoint half of an `oauth.Config` and
leaves the deployment's credentials and redirect URI to the caller.

## Providers

| Name | Issuer | Scopes | Claims |
| --- | --- | --- | --- |
| `x` | `https://x.com` | `users.read tweet.read` | `sub`, `preferred_username`, `name`, `picture` |

Endpoints are constants rather than something discovered. A provider that
issues no ID Token generally publishes no metadata document either, so there is
nothing to fetch, and nothing whose freshness could surprise a running
deployment.

A provider is described rather than coded. `ProfileRoot` is the JSON Pointer to
the object holding the account — `/data` for X, which wraps every v2 payload —
and `Profile` renames that object's members onto the shared vocabulary:

```go
ProfileRoot: "/data",
Profile: map[string]string{
	"sub": "id", "preferred_username": "username",
	"name": "name", "picture": "profile_image_url",
},
```

Each provider's own spelling is normalized away rather than carried alongside,
so there is one name per value. `Provider.Claims()` lists what a definition
reports, in a stable order, so a caller can refuse a claim name at startup
instead of at the login that would not have carried it.

A member is read as a string, or as the literal text of a whole number, since
several providers issue numeric account identifiers. A fraction, an object, an
empty string, and an absent member are all left out rather than normalized: a
claim rule must not be able to match on something the provider did not say.

`sub` is the only claim a provider owes, and the only one an account may be
linked to. A handle is display and support material: X lets one be renamed, and
lets a released one be claimed by somebody else, so an account linked to a
handle eventually becomes somebody else's account.

## Bounds

The profile request sends the token to the provider's own endpoint and nowhere
else. Redirects are disabled, the response is bounded to 64 KiB by default, the
request to 30 seconds, and one copied display value to 512 bytes — a provider
may answer with a name of any length, but what enters a session is bounded. A
display value over that bound is dropped rather than truncated, and the login
still succeeds on the identifier. Errors carry no response text.

The package intentionally excludes refresh, revocation, and every provider API
beyond the one endpoint that reports the account. Nothing here retains a token.
