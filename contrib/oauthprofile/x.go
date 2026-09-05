package oauthprofile

import "github.com/shibukawa/popcornweb/contrib/oauth"

// x is the OAuth 2.0 login of X, formerly Twitter.
//
// X issues no ID Token and publishes no discovery document, so the endpoints
// below are the whole of what a relying party can know without asking. They are
// the x.com names; the twitter.com ones still redirect, and a redirect is
// exactly what a token request must never follow.
var x = Provider{
	Name:                  ProviderX,
	Issuer:                "https://x.com",
	AuthorizationEndpoint: "https://x.com/i/oauth2/authorize",
	TokenEndpoint:         "https://api.x.com/2/oauth2/token",
	ProfileEndpoint:       "https://api.x.com/2/users/me?user.fields=profile_image_url",
	// users.read is what the profile endpoint needs, and X grants it only
	// together with tweet.read. offline.access is deliberately absent: it buys
	// a refresh token, and this login keeps no token past the callback.
	Scopes:     []string{"users.read", "tweet.read"},
	AuthMethod: oauth.AuthBasic,
	// X wraps every v2 payload in "data".
	ProfileRoot: "/data",
	// The account link is the numeric id, under "sub". The handle is display
	// and support material only: X lets a handle be renamed, and lets a
	// released one be claimed by somebody else, so linking an account to it
	// would eventually hand one person another person's account.
	Profile: map[string]string{
		"sub":                "id",
		"preferred_username": "username",
		"name":               "name",
		"picture":            "profile_image_url",
	},
}
