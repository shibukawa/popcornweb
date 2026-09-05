package auth

import (
	"errors"
	"net/http"

	"github.com/shibukawa/popcornweb/contrib/oauth"
	"github.com/shibukawa/popcornweb/contrib/oauthprofile"
	"github.com/shibukawa/popcornweb/pwruntime"
)

// The login of ModeOAuthOnly: the same two endpoints the OIDC flow serves, over
// a provider that issues no ID Token.
//
// What differs is one step and its consequences. The callback receives an
// access token addressed to the provider's own API rather than a signed
// assertion about a person, so who signed in is settled by asking the provider,
// over one request that carries the token and nothing else. Everything after
// that point — admission, the account resolver, rotation, the landing path — is
// the OIDC flow's, unchanged, because by then both have the same thing in hand.
//
// What the mode does not have is anything that rests on the provider reporting
// when it last authenticated somebody: step-up re-authentication, the
// select_account and login prompts a reconfirm logout sets, and the
// RP-initiated logout that ends the provider's own session. None of them exists
// in plain OAuth, and Config.reprovable, logoutScope, and the routing below
// each refuse rather than approximate one.

// oauthClient builds the authorization code client for this request. It needs
// no discovery: the endpoints come from the provider definition, which is a
// constant of the build rather than a document fetched at runtime.
func (rt *runtime) oauthClient(x Exchange) (*oauth.Client, error) {
	redirectURI, err := rt.oauthRedirectURI(x)
	if err != nil {
		return nil, err
	}
	config := rt.oauthProvider.OAuthConfig(rt.config.OAuth.ClientID, rt.config.OAuth.ClientSecret, redirectURI)
	config.AllowLoopbackHTTP = rt.config.OAuth.AllowLoopbackHTTP
	return oauth.NewClient(config, oauth.Options{StateStore: rt.stateStore})
}

// oauthRedirectURI returns the configured absolute URL, or derives one from the
// request authority under the explicit loopback development allowance.
func (rt *runtime) oauthRedirectURI(x Exchange) (string, error) {
	return resolveProviderRedirectURI(rt.config.OAuth.RedirectURL, rt.config.CallbackPath,
		rt.config.OAuth.AllowLoopbackHTTP, rt.scheme(x), x.Host(), "auth.oauth")
}

// oauthScopes are the deployment's list where it stated one, and otherwise the
// provider's own minimum. A stated list replaces rather than extends: a
// deployment that names its scopes owns them, and silently adding to what it
// asked for would put a consent screen in front of the user that its
// configuration does not describe.
func (rt *runtime) oauthScopes() []string {
	if len(rt.config.OAuth.Scopes) > 0 {
		return rt.config.OAuth.Scopes
	}
	return rt.oauthProvider.Scopes
}

func (rt *runtime) handleOAuthLogin(x Exchange) {
	if !allowMethod(x, http.MethodGet, http.MethodHead) {
		return
	}
	returnPath := localReturnPath(x.Query("next"))
	// A max_age arriving here is a guard asking for a re-proof this mode cannot
	// perform. It is dropped rather than honored or refused: Config.reprovable
	// already stops such a guard from sending anybody here, so a request
	// carrying one is a hand-written URL, and answering it with an ordinary
	// login is both harmless and what the browser is asking for.
	if authenticated(x) {
		rt.redirect(x, rt.landingPath(returnPath))
		return
	}
	client, err := rt.oauthClient(x)
	if err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelError, "oauth client unavailable", pwruntime.Err(err))
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	authorizationURL, key, err := client.BeginAuthorization(x.Context(), oauth.BeginOptions{Scopes: rt.oauthScopes()})
	if err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelError, "oauth authorization request failed", pwruntime.Err(err))
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	rt.writeTransactionCookie(x, encodeTransaction(key, false, 0, returnPath))
	rt.redirect(x, authorizationURL)
}

func (rt *runtime) handleOAuthCallback(x Exchange) {
	if !allowMethod(x, http.MethodGet) {
		return
	}
	key, _, _, returnPath, ok := rt.takeTransaction(x)
	if !ok {
		x.Problem(pwruntime.BadRequest())
		return
	}
	if providerError := x.Query("error"); providerError != "" {
		// The provider rejected the request; its description is not echoed back
		// to the browser.
		logger(x).Log(x.Context(), pwruntime.LevelWarn, "oauth provider returned an error",
			pwruntime.String("provider", rt.oauthProvider.Name),
			pwruntime.String("error", providerError))
		x.Problem(pwruntime.Forbidden())
		return
	}
	client, err := rt.oauthClient(x)
	if err != nil {
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	tokens, err := client.HandleCallback(x.Context(), key, oauth.Callback{
		State: x.Query("state"),
		Code:  x.Query("code"),
	})
	if err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelWarn, "oauth callback rejected", pwruntime.Err(err))
		x.Problem(pwruntime.Forbidden())
		return
	}
	// The access token lives exactly as long as this call. It is not written to
	// the session, not stored, and not logged: this mode signs a person in, and
	// a token kept past the moment it answered that question is a credential
	// with no reader and a breach with no purpose.
	profile, err := oauthprofile.Fetch(x.Context(), rt.oauthProvider, tokens.AccessToken, oauthprofile.Options{})
	if err != nil {
		// A provider that cannot answer for its own token is an outage rather
		// than a rejected user, and 503 is what says so. Answering 403 would
		// tell somebody who is entitled to enter that they are not.
		logger(x).Log(x.Context(), pwruntime.LevelError, "oauth profile could not be read",
			pwruntime.String("provider", rt.oauthProvider.Name), pwruntime.Err(err))
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	identity := identityFromProfile(rt.oauthProvider, profile, rt.config.OAuth.IdentityClaim)
	account, err := admitIdentity(x.Context(), rt.admissionFor(), rt.allowlist, identity)
	if err != nil {
		if !errors.Is(err, ErrAccessDenied) {
			logger(x).Log(x.Context(), pwruntime.LevelError, "account resolution failed", pwruntime.Err(err))
			x.Problem(pwruntime.InternalServerError(err))
			return
		}
		// One response shape for every admission failure keeps the endpoint from
		// reporting whether an account exists.
		x.Problem(pwruntime.Forbidden())
		return
	}
	data := SessionData{
		AccountID:   account.ID,
		Issuer:      identity.Issuer,
		Subject:     identity.Subject,
		KeyClaim:    identity.KeyClaim,
		Key:         identity.Key,
		DisplayName: account.DisplayName,
		Email:       account.Email,
		Provider:    rt.oauthProvider.Name,
		Username:    profile.Username,
		AvatarURL:   profile.AvatarURL,
	}
	// ProviderAuthTime stays zero. The provider reported no authentication time,
	// and inventing one from the moment the callback landed would make every
	// freshness window measure the redirect rather than the proof.
	if err := rt.establish(x, data, MethodOAuth); err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelError, "session creation failed", pwruntime.Err(err))
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	rt.rememberSignIn(x, data)
	rt.redirect(x, rt.landingPath(returnPath))
}
