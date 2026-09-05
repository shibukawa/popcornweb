package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/contrib/oauth"
	"github.com/shibukawa/popcornweb/contrib/oidc"
	"github.com/shibukawa/popcornweb/internal/pathpattern"
	"github.com/shibukawa/popcornweb/pwruntime"
)

const (
	// transactionCookieSuffix names the short-lived cookie holding the opaque
	// key of the pending OAuth transaction. The transaction itself, including
	// its state, nonce, and PKCE verifier, never reaches the browser.
	transactionCookieSuffix = "_txn"
	transactionCookieMaxAge = 600
	// returnPathSeparator splits the transaction key from the local path the
	// completed login returns to. The key is base64url and cannot contain it.
	returnPathSeparator = ":"
	// reconfirmCookieSuffix names the cookie carrying the one thing a
	// reconfirm logout leaves behind: that the next authorization request must
	// not be answered silently.
	//
	// It needs no integrity protection. Its only effect is to add prompt,
	// which can add an interaction and never remove one, so a forged value
	// costs a redundant confirmation and grants nothing.
	reconfirmCookieSuffix = "_reconfirm"
	// reconfirmCookieMaxAge outlives a browser sitting on the landing page
	// before signing in again, without becoming a lasting setting.
	reconfirmCookieMaxAge = 3600
	// logoutScopeField lets a logout form escalate to a global sign-out where
	// the deployment permits it.
	logoutScopeField = "scope"
)

// Logout scopes decide what a logout does to the provider session. There is no
// local-only scope: see OIDCConfig.LogoutScope.
const (
	LogoutScopeReconfirm = "reconfirm"
	LogoutScopeGlobal    = "global"
)

// serve finalizes the request authentication and then serves the login
// endpoints, calling next for every request neither of those answered.
//
// Deriving the authentication is this package's job rather than the session
// package's: a session is storage, and only what is in this package's own slot
// says that the browser holding it is a signed-in account. SlotAuthentication
// is where that is settled, which is what the slot is named for.
//
// It takes a continuation rather than a handler because continuing a chain is
// the one thing the two transports do not spell alike: one hands a derived
// request to the next handler and the other writes into the request value it
// already has. Everything before that point is the same text.
func (rt *runtime) serve(x Exchange, next func()) {
	if data, ok := Session(x.Context()); ok {
		// The account is re-read here, not only where the session was created:
		// suspending an account is how a deployment answers a compromise, and
		// the session it needs to reach is one that already exists. rt.accounts
		// bounds how often that read happens.
		switch rt.accounts.admit(x.Context(), data.AccountID) {
		case accountEnded:
			// The account may no longer act, so the session goes with it and the
			// request continues as an anonymous one. Destroying it here means the
			// browser stops carrying a session nothing will honour rather than
			// presenting it until it expires.
			if err := rt.manager.DestroyOn(x.Context()); err != nil {
				logger(x).Log(x.Context(), pwruntime.LevelError,
					"session of an ended account could not be destroyed", pwruntime.Err(err))
			}
		case accountUnknown:
			// The credential was not judged. Refusing with 503 rather than 401
			// says the request may succeed on a retry and keeps a suspension from
			// being conditional on the account store being reachable, which is the
			// same trade policy:token-revocation settles the same way.
			logger(x).Log(x.Context(), pwruntime.LevelError,
				"account behind a session could not be read", pwruntime.String("account", data.AccountID))
			x.Problem(pwruntime.ServiceUnavailable())
			return
		default:
			x.RecordAuthentication(pwruntime.Authentication{
				Authenticated:   true,
				Subject:         data.AccountID,
				Method:          data.Method,
				Principal:       data,
				AuthenticatedAt: data.AuthenticatedAt,
			})
		}
	}
	rt.endpoints(x, next)
}

// endpoints owns the login, callback, and logout paths and passes every other
// request through.
func (rt *runtime) endpoints(x Exchange, next func()) {
	path, ok := pathpattern.CanonicalPathOf(x.Path(), x.RawPath())
	if !ok {
		x.Problem(pwruntime.BadRequest())
		return
	}
	// A ceremony path is claimed before the provider paths so that a mode without
	// a provider never falls through to a login it cannot serve.
	if suffix, mounted := rt.passkeyPaths[path]; mounted {
		rt.handlePasskey(x, suffix)
		return
	}
	switch {
	case rt.config.usesOIDC() && path == rt.config.LoginPath:
		rt.handleLogin(x)
	case rt.config.usesOIDC() && path == rt.config.CallbackPath:
		rt.handleCallback(x)
	case rt.config.usesOAuth() && path == rt.config.LoginPath:
		rt.handleOAuthLogin(x)
	case rt.config.usesOAuth() && path == rt.config.CallbackPath:
		rt.handleOAuthCallback(x)
	case path == rt.config.LogoutPath:
		rt.handleLogout(x)
	case rt.hint != nil && path == rt.forgetPath():
		rt.handleForget(x)
	case rt.config.Assurance.Presence.Enabled && path == rt.presencePath():
		rt.handlePresence(x)
	default:
		next()
	}
}

func (rt *runtime) handleLogin(x Exchange) {
	if !allowMethod(x, http.MethodGet, http.MethodHead) {
		return
	}
	returnPath := localReturnPath(x.Query("next"))
	// A step-up arrives already authenticated and asks to prove again, so the
	// usual shortcut of sending an authenticated browser straight to the landing
	// path would defeat it.
	stepUp, stepUpWindow := rt.pendingStepUp(x)
	if authenticated(x) && !stepUp {
		rt.redirect(x, rt.landingPath(returnPath))
		return
	}
	client, err := rt.oidcClient(x)
	if err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelError, "oidc discovery failed", pwruntime.Err(err))
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	begin := oidc.BeginOptions{Scopes: rt.config.OIDC.Scopes}
	if stepUp {
		// max_age is the load-bearing half: the provider must return auth_time
		// when it is sent, so the answer can be checked. prompt=login only
		// improves the odds of an interactive confirmation and reports nothing.
		begin.MaxAge = &stepUpWindow
		begin.Prompt = []string{"login"}
	} else if rt.takeReconfirm(x) {
		// select_account names who the provider knows, which is the account
		// picker a returning user expects, and login stops it from being
		// answered from a single sign-on session the logout left alive.
		//
		// A shared-device deployment withholds select_account: it exists to
		// surface exactly what that mode is trying to hide.
		if rt.config.SharedDevice {
			begin.Prompt = []string{"login"}
		} else {
			begin.Prompt = []string{"select_account", "login"}
		}
	}
	authorizationURL, key, err := client.BeginAuthorization(x.Context(), begin)
	if err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelError, "oidc authorization request failed", pwruntime.Err(err))
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	rt.writeTransactionCookie(x, encodeTransaction(key, stepUp, stepUpWindow, returnPath))
	rt.redirect(x, authorizationURL)
}

// stepUpQueryField carries the requested window into the login endpoint. It is
// not trusted: a larger value only weakens the request, and the guard that sent
// the browser here re-evaluates its own requirement when it returns, so a
// tampered window fails that check instead of passing it.
const stepUpQueryField = "max_age"

// pendingStepUp reports whether this login is a re-proof of the session the
// browser already holds, and the window it must satisfy.
func (rt *runtime) pendingStepUp(x Exchange) (bool, time.Duration) {
	raw := x.Query(stepUpQueryField)
	if raw == "" || !authenticated(x) {
		return false, 0
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 || seconds > int(maxStepUpWindow/time.Second) {
		return false, 0
	}
	return true, time.Duration(seconds) * time.Second
}

// maxStepUpWindow bounds the window a redirect may name, so a hostile link
// cannot ask for one so large that the re-proof is satisfied by anything.
const maxStepUpWindow = 24 * time.Hour

// stepUpPath builds the local login URL that re-proves the current session and
// returns to the operation that was refused.
func (rt *runtime) stepUpPath(x Exchange, window time.Duration) string {
	values := url.Values{}
	values.Set(stepUpQueryField, strconv.Itoa(int(window/time.Second)))
	if next := localReturnPath(x.Target()); next != "" {
		values.Set("next", next)
	}
	return rt.config.LoginPath + "?" + values.Encode()
}

func (rt *runtime) handleCallback(x Exchange) {
	if !allowMethod(x, http.MethodGet) {
		return
	}
	key, stepUp, stepUpWindow, returnPath, ok := rt.takeTransaction(x)
	if !ok {
		x.Problem(pwruntime.BadRequest())
		return
	}
	if providerError := x.Query("error"); providerError != "" {
		// The provider rejected the request; its description is not echoed back
		// to the browser.
		logger(x).Log(x.Context(), pwruntime.LevelWarn, "oidc provider returned an error",
			pwruntime.String("error", providerError))
		x.Problem(pwruntime.Forbidden())
		return
	}
	client, err := rt.oidcClient(x)
	if err != nil {
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	_, idToken, err := client.HandleCallback(x.Context(), key, oidc.Callback{
		State: x.Query("state"),
		Code:  x.Query("code"),
	}, oidc.CallbackOptions{RequireAuthTime: stepUp})
	if err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelWarn, "oidc callback rejected", pwruntime.Err(err))
		x.Problem(pwruntime.Forbidden())
		return
	}
	identity := identityFrom(idToken.Claims, rt.config.OIDC.IdentityClaim)
	if stepUp {
		rt.completeStepUp(x, identity, idToken, stepUpWindow, returnPath)
		return
	}
	account, err := admit(x.Context(), rt.config.OIDC, rt.allowlist, identity)
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
	// Rotation, rather than creation, revokes any session the browser already
	// held before this authentication.
	data := SessionData{
		AccountID:   account.ID,
		Issuer:      identity.Issuer,
		Subject:     identity.Subject,
		KeyClaim:    identity.KeyClaim,
		Key:         identity.Key,
		DisplayName: account.DisplayName,
		Email:       account.Email,
	}
	// A provider that reported auth_time without being asked still reported the
	// truth about when it authenticated this person, and that is what freshness
	// is measured from.
	if idToken.AuthTime != nil {
		data.ProviderAuthTime = idToken.AuthTime.Unix()
	}
	if err := rt.establish(x, data, MethodOIDC); err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelError, "session creation failed", pwruntime.Err(err))
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	rt.rememberSignIn(x, data)
	rt.redirect(x, rt.landingPath(returnPath))
}

// completeStepUp refreshes the assurance of the session the browser already
// holds. It never provisions, never links, and never changes which account is
// signed in: a step-up that accepted any successful login would be an account
// swap with the previous account's guarded operation already staged.
func (rt *runtime) completeStepUp(x Exchange, identity Identity, idToken oidc.IDToken, window time.Duration, returnPath string) {
	view, ok := Session(x.Context())
	if !ok {
		// The session ended while the browser was at the provider. Nothing is
		// left to refresh, and silently creating one would turn a re-proof into a
		// login the guard never asked for.
		x.Problem(pwruntime.Forbidden())
		return
	}
	if identity.Issuer != view.Issuer || identity.KeyClaim != view.KeyClaim || identity.Key != view.Key || identity.Key == "" {
		logger(x).Log(x.Context(), pwruntime.LevelWarn, "step-up completed by a different identity")
		x.Problem(pwruntime.Forbidden())
		return
	}
	// Admission still applies, so an identity that lost it fails the re-proof
	// rather than refreshing a session it may no longer have.
	if _, err := admit(x.Context(), rt.config.OIDC, rt.allowlist, identity); err != nil {
		if !errors.Is(err, ErrAccessDenied) {
			logger(x).Log(x.Context(), pwruntime.LevelError, "step-up admission failed", pwruntime.Err(err))
			x.Problem(pwruntime.InternalServerError(err))
			return
		}
		x.Problem(pwruntime.Forbidden())
		return
	}
	data := view
	if idToken.AuthTime != nil {
		data.ProviderAuthTime = idToken.AuthTime.Unix()
	}
	if window == 0 {
		data.StepUpAt = time.Now().Unix()
	}
	// Rotation, because an assurance change is an authentication-strength
	// change: the previous token is revoked and the CSRF secret turns with it.
	if err := rt.establish(x, data, MethodOIDC); err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelError, "step-up session rotation failed", pwruntime.Err(err))
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	rt.redirect(x, rt.landingPath(returnPath))
}

func (rt *runtime) handleLogout(x Exchange) {
	// Logout changes server state, so it is a POST and is checked for
	// same-origin submission.
	if !allowMethod(x, http.MethodPost) {
		return
	}
	if !rt.sameOrigin(x) {
		x.Problem(pwruntime.Forbidden())
		return
	}
	// An explicit sign-out asks to be forgotten, so the hint goes with the
	// session. Only expiry leaves one behind.
	rt.forgetSignIn(x)
	// The local session goes first and unconditionally, whatever the selected
	// scope does afterward.
	if err := rt.endSession(x); err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelError, "session deletion failed", pwruntime.Err(err))
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	if rt.logoutScope(x) == LogoutScopeGlobal {
		if target := rt.endSessionURL(x); target != "" {
			rt.redirect(x, target)
			return
		}
		// A provider advertising no end session endpoint degrades to reconfirm
		// rather than to a silent logout: the next login must still ask.
	}
	// Reconfirm asks the provider for nothing. It records that the next
	// authorization request carries prompt, which is what stops the provider
	// from answering silently from a single sign-on session this logout could
	// not reach.
	//
	// Only OIDC can say it. A plain OAuth authorization request has no prompt
	// parameter, so ModeOAuthOnly writes no cookie here rather than one nothing
	// will read, and its logout is local by nature: the provider session
	// survives, and the next login may well be answered from it without a word.
	// That is the mode's cost, and it is stated in the guide rather than papered
	// over here.
	if rt.config.usesOIDC() {
		rt.markReconfirm(x)
	}
	rt.redirect(x, "/")
}

// forgetPath is the not-me control of a login screen. It hangs off the logout
// path because it is the same act at a lower level: one ends a session, the
// other ends the memory of one.
func (rt *runtime) forgetPath() string { return rt.config.LogoutPath + "/forget" }

// handleForget clears the sign-in hint. It requires no session and no
// authentication, because the person pressing it is by definition not signed
// in, and it grants nothing: clearing a hint can only make the next sign-in
// longer.
//
// It is still POST and same-origin, for the reason the logout endpoint is: a
// control reachable by a link or a prefetch is one anything that fetches URLs
// can trigger on the user's behalf.
func (rt *runtime) handleForget(x Exchange) {
	if !allowMethod(x, http.MethodPost) {
		return
	}
	if !rt.sameOrigin(x) {
		x.Problem(pwruntime.Forbidden())
		return
	}
	rt.forgetSignIn(x)
	rt.redirect(x, "/")
}

// logoutScope resolves the configured scope, allowing a request to escalate to
// global and never to downgrade. Escalation costs the user extra sign-outs;
// a forced downgrade would leave the provider session alive after the user
// asked to leave it.
func (rt *runtime) logoutScope(x Exchange) string {
	if !rt.config.usesOIDC() {
		// Only an OIDC provider advertises a way to end its own session. Every
		// other mode's logout reaches the local session and stops there.
		return LogoutScopeReconfirm
	}
	if rt.config.OIDC.LogoutScope == LogoutScopeGlobal {
		return LogoutScopeGlobal
	}
	if rt.config.OIDC.AllowGlobalLogoutRequest && x.FormValue(logoutScopeField) == LogoutScopeGlobal {
		return LogoutScopeGlobal
	}
	return LogoutScopeReconfirm
}

// endSessionURL builds the RP-initiated logout request, or returns an empty
// string when the provider advertises no end session endpoint, discovery is
// unavailable, or the request cannot be built. Each of those falls back to the
// local logout rather than stranding the browser.
//
// No id_token_hint is sent: SessionData deliberately holds no token body, and
// client_id with a post_logout_redirect_uri identifies the relying party well
// enough for a provider that accepts either.
func (rt *runtime) endSessionURL(x Exchange) string {
	client, err := rt.oidcClient(x)
	if err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelWarn, "provider logout unavailable", pwruntime.Err(err))
		return ""
	}
	target, err := client.EndSessionURL(oidc.EndSessionOptions{
		PostLogoutRedirectURI: rt.postLogoutRedirectURI(x),
	})
	if err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelWarn, "provider logout request rejected", pwruntime.Err(err))
		return ""
	}
	return target
}

// postLogoutRedirectURI is the absolute form of the local landing path, because
// a provider needs a full URL to return the browser to.
//
// The scheme comes from the same resolution the origin comparison uses. This
// function used to read X-Forwarded-Proto with no trusted-proxy gate, which
// made it the one caller in the tree that answered "is this https" from a value
// any client could assert.
func (rt *runtime) postLogoutRedirectURI(x Exchange) string {
	if x.Host() == "" {
		return ""
	}
	return (&url.URL{Scheme: rt.scheme(x), Host: x.Host(), Path: "/"}).String()
}

// oidcClient discovers the provider on first use and builds a client bound to
// this request's redirect URI. Discovery failures are not cached, so a
// provider that starts later still works without restarting the application.
func (rt *runtime) oidcClient(x Exchange) (*oidc.Client, error) {
	redirectURI, err := rt.oidcRedirectURI(x)
	if err != nil {
		return nil, err
	}
	rt.discovery.Lock()
	defer rt.discovery.Unlock()
	if rt.provider == nil {
		discoveryCtx, cancel := context.WithTimeout(x.Context(), 30*time.Second)
		defer cancel()
		rt.provider, err = oidc.Discover(discoveryCtx, rt.config.OIDC.Issuer, oidc.DiscoverOptions{
			AllowLoopbackHTTP: rt.config.OIDC.AllowLoopbackHTTP,
			EndpointHosts:     rt.config.OIDC.EndpointHosts,
		})
		if err != nil {
			rt.provider = nil
			return nil, err
		}
	}
	return oidc.NewClient(rt.provider, oidc.Config{
		ClientID:          rt.config.OIDC.ClientID,
		ClientSecret:      rt.config.OIDC.ClientSecret,
		RedirectURI:       redirectURI,
		AllowLoopbackHTTP: rt.config.OIDC.AllowLoopbackHTTP,
	}, oidc.Options{
		OAuth: oauth.Options{StateStore: rt.stateStore},
	})
}

// oidcRedirectURI returns the configured absolute URL, or derives one from the
// request authority in the explicitly enabled loopback development mode.
func (rt *runtime) oidcRedirectURI(x Exchange) (string, error) {
	return resolveProviderRedirectURI(rt.config.OIDC.RedirectURL, rt.config.CallbackPath,
		rt.config.OIDC.AllowLoopbackHTTP, rt.scheme(x), x.Host(), "auth.oidc")
}

// resolveProviderRedirectURI is shared by both provider flows. What may stand in
// for a registered redirect URL is one rule, and two copies of it would be two
// chances for one of them to accept a host the other refuses.
//
// setting names the configuration prefix so the error says which key to fix.
func resolveProviderRedirectURI(raw, callbackPath string, allowLoopbackHTTP bool, scheme, host, setting string) (string, error) {
	if parsed, err := url.Parse(raw); err == nil && parsed.IsAbs() {
		return raw, nil
	}
	if !allowLoopbackHTTP {
		return "", fmt.Errorf("request-relative redirect requires %s.allow_loopback_http", setting)
	}
	authority, err := url.Parse("//" + host)
	if err != nil || authority.Host == "" || authority.Host != host || authority.User != nil ||
		authority.Path != "" || authority.RawQuery != "" || authority.Fragment != "" ||
		!isLoopbackHost(strings.ToLower(authority.Hostname())) {
		return "", fmt.Errorf("request-relative %s redirect requires a loopback Host, got %q", setting, host)
	}
	path := raw
	if path == "" {
		path = callbackPath
	}
	return (&url.URL{Scheme: scheme, Host: authority.Host, Path: path}).String(), nil
}

func (rt *runtime) writeTransactionCookie(x Exchange, value string) {
	x.SetCookie(&http.Cookie{
		Name:     rt.transactionCookieName(),
		Value:    value,
		Path:     rt.config.CallbackPath,
		MaxAge:   transactionCookieMaxAge,
		Secure:   rt.cookieSecure(),
		HttpOnly: true,
		// The provider redirect is a cross-site top-level navigation, so the
		// cookie must survive it.
		SameSite: http.SameSiteLaxMode,
	})
}

// encodeTransaction packs the correlation key, whether this is a re-proof of an
// existing session, its window, and the local return path into one value.
//
// The key is base64url and the window is digits, so neither can contain the
// separator and only the trailing return path can.
func encodeTransaction(key string, stepUp bool, window time.Duration, returnPath string) string {
	marker := "0"
	if stepUp {
		marker = strconv.Itoa(int(window/time.Second) + 1)
	}
	return key + returnPathSeparator + marker + returnPathSeparator + returnPath
}

// takeTransaction reads and immediately expires the pending transaction cookie,
// so one callback can consume it only once.
//
// stepUp reports a re-proof of the session the browser already holds, and
// window is what it must satisfy. Both come from the server-set cookie rather
// than from the callback query, so the requirement a login started with is the
// one its callback enforces.
func (rt *runtime) takeTransaction(x Exchange) (key string, stepUp bool, window time.Duration, returnPath string, ok bool) {
	rawKey, rest, found := rt.takeTransactionCookie(x)
	if !found {
		return "", false, 0, "", false
	}
	marker, path, split := strings.Cut(rest, returnPathSeparator)
	if !split {
		return "", false, 0, "", false
	}
	value, err := strconv.Atoi(marker)
	if err != nil || value < 0 {
		return "", false, 0, "", false
	}
	if value > 0 {
		// The marker is the window plus one, so zero stays available to mean
		// "not a step-up" while a zero-second window is still expressible.
		return rawKey, true, time.Duration(value-1) * time.Second, localReturnPath(path), true
	}
	return rawKey, false, 0, localReturnPath(path), true
}

func (rt *runtime) takeTransactionCookie(x Exchange) (key, returnPath string, ok bool) {
	value, present := requestCookie(x, rt.transactionCookieName())
	x.SetCookie(&http.Cookie{
		Name:     rt.transactionCookieName(),
		Value:    "",
		Path:     rt.config.CallbackPath,
		MaxAge:   -1,
		Secure:   rt.cookieSecure(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	if !present {
		return "", "", false
	}
	key, rest, found := strings.Cut(value, returnPathSeparator)
	if !found || key == "" {
		return "", "", false
	}
	return key, rest, true
}

func (rt *runtime) transactionCookieName() string {
	return rt.manager.CookieName() + transactionCookieSuffix
}

func (rt *runtime) reconfirmCookieName() string {
	return rt.manager.CookieName() + reconfirmCookieSuffix
}

// markReconfirm records that the next authorization request must carry prompt.
// This is the whole of a reconfirm logout: nothing is sent to the provider, and
// the provider session of every other relying party is untouched.
func (rt *runtime) markReconfirm(x Exchange) {
	x.SetCookie(&http.Cookie{
		Name:     rt.reconfirmCookieName(),
		Value:    "1",
		Path:     rt.config.LoginPath,
		MaxAge:   reconfirmCookieMaxAge,
		Secure:   rt.cookieSecure(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// takeReconfirm reports whether the pending reconfirmation is set and clears
// it, so the intent survives only until the next authorization request rather
// than becoming a permanent setting.
func (rt *runtime) takeReconfirm(x Exchange) bool {
	if _, pending := requestCookie(x, rt.reconfirmCookieName()); !pending {
		return false
	}
	x.SetCookie(&http.Cookie{
		Name:     rt.reconfirmCookieName(),
		Value:    "",
		Path:     rt.config.LoginPath,
		MaxAge:   -1,
		Secure:   rt.cookieSecure(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return true
}

// cookieSecure mirrors the session cookie policy, so the correlation cookie is
// never weaker than the session it protects.
func (rt *runtime) cookieSecure() bool {
	return rt.cookiePolicy.Secure
}

// landingPath prefers a validated local return path and otherwise uses the
// configured post-login path.
func (rt *runtime) landingPath(returnPath string) string {
	if returnPath != "" {
		return returnPath
	}
	return rt.config.PostLoginPath
}

func (rt *runtime) redirect(x Exchange, location string) {
	x.SetHeader("Cache-Control", "no-store")
	x.Redirect(location, http.StatusSeeOther)
}

// localReturnPath accepts only a rooted same-site path. An absolute or
// protocol-relative value would turn login into an open redirect. Backslashes
// are refused outright: the WHATWG URL parser treats them as slashes, so a
// browser resolves "/\evil.example" as the protocol-relative "//evil.example"
// even though Go's parser sees a plain rooted path.
func localReturnPath(value string) string {
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
		strings.ContainsAny(value, "\\\x00") {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Opaque != "" {
		return ""
	}
	if strings.Contains(value, returnPathSeparator) {
		return ""
	}
	for _, segment := range strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/") {
		if segment == "." || segment == ".." {
			return ""
		}
	}
	return value
}
