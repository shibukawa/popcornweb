// Package oauthprofile resolves the account behind an OAuth 2.0 access token,
// for the providers that sign a person in without ever issuing an ID Token.
//
// It supplies the two things plain OAuth leaves to the provider: where a
// provider's authorization and token endpoints are, and how to read the
// account out of whatever shape that provider answers its own user endpoint
// with. Everything about the exchange itself belongs to contrib/oauth.
package oauthprofile
