package auth

import (
	"net/http"
	"net/url"

	"github.com/shibukawa/popcornweb/internal/pathpattern"
	"github.com/shibukawa/popcornweb/pwruntime"
)

// Rules are the resolved path-protection policy, in a form that names no
// transport and no identity provider.
//
// It is a value rather than a middleware because the frame that applies it is
// each transport's own — the second one ships pwfast.Guard, which takes exactly
// these four — while deciding which paths are protected and where an
// unauthenticated visitor is sent is this package's. A deployment without an
// authentication plugin writes one of these by hand.
type Rules struct {
	// Protected reports whether a path requires an authenticated caller. A nil
	// value protects nothing.
	Protected func(path string) bool
	// LoginURL is where an unauthenticated browser is sent, carrying a
	// validated local return path. An empty result answers 401 instead.
	LoginURL func(path string) string
	// Redirect sends a request that asks for a page to LoginURL. Every other
	// unauthenticated request is answered 401, and so is every request when it
	// is false.
	//
	// A redirect is an answer to a person. An icon, an image, or a script is
	// fetched by a browser that follows it all the same, and the request that
	// follows reaches the login endpoint on the way to a login page nobody sees.
	Redirect bool
	// BearerRealm names the scheme a 401 asks for, per RFC 6750. Empty sends no
	// challenge.
	BearerRealm string
}

// Protection returns the resolved path-protection policy of the running
// deployment, or the zero value when no authentication runtime is installed or
// it protects nothing.
//
// It is how a transport that assembles its own chain reaches the same decision
// the net/http guard makes, rather than reading the configuration a second time.
func Protection() Rules {
	instance := activeRuntime()
	if instance == nil || len(instance.include) == 0 {
		return Rules{}
	}
	return instance.rules()
}

func (rt *runtime) rules() Rules {
	rules := Rules{
		Protected: rt.protected,
		LoginURL:  rt.loginURL,
		Redirect:  rt.config.Protection.Unauthenticated != UnauthenticatedUnauthorized,
	}
	if !rules.Redirect && rt.config.usesJWT() {
		// RFC 6750 asks a protected resource to name the scheme it accepts, so a
		// client that sent nothing learns what to send.
		rules.BearerRealm = rt.bearerRealm()
	}
	return rules
}

// guard requires an authenticated request on every protected path. Protection
// is opt-in: a path is protected when it matches at least one include pattern
// and no exclude pattern.
func (rt *runtime) guard(x Exchange, next func()) {
	rules := rt.rules()
	path, ok := pathpattern.CanonicalPathOf(x.Path(), x.RawPath())
	if !ok {
		x.Problem(pwruntime.BadRequest())
		return
	}
	if !rules.Protected(path) || authenticated(x) {
		next()
		return
	}
	// Never cached: the answer depends on who is asking and on what they asked
	// for, and a shared cache holding one would show a signed-in page to a
	// stranger or a login redirect to a signed-in reader.
	x.SetHeader("Cache-Control", "no-store")
	// Only a request that asks for a page is sent to the login. The others are
	// refused where they stand, because a browser follows a redirect for an icon
	// as readily as for a page — Safari does it for the icons it asks for
	// unprompted — and the login endpoint it lands on does what a visit there
	// does: it begins an authorization, and it uses up the one-time mark a
	// sign-out left so that the next login would ask again.
	if rules.Redirect && announcesPage(x) {
		if target := rules.LoginURL(path); target != "" {
			x.Redirect(target, http.StatusSeeOther)
			return
		}
	}
	if rules.BearerRealm != "" {
		x.SetHeader("WWW-Authenticate", `Bearer realm="`+rules.BearerRealm+`"`)
	}
	x.Problem(pwruntime.Unauthorized())
}

// announcesPage is this transport's header read over the shared decision, so
// the two builds cannot drift on which requests are sent to the login.
func announcesPage(x Exchange) bool {
	return pwruntime.AnnouncesPage(x.Header("Sec-Fetch-Dest"), x.Header("Accept"),
		x.Header(pwruntime.UpdateRenderHeader), x.Header(pwruntime.ResponseModeHeader))
}

// protected applies exclude-over-include precedence. The framework's own login,
// callback, and logout paths always stay reachable.
func (rt *runtime) protected(path string) bool {
	switch path {
	case rt.config.LoginPath, rt.config.CallbackPath, rt.config.LogoutPath:
		return false
	}
	if pathpattern.MatchAny(rt.exclude, path) {
		return false
	}
	return pathpattern.MatchAny(rt.include, path)
}

// loginURL sends the browser to the local login path, carrying only a validated
// local return path.
func (rt *runtime) loginURL(path string) string {
	target := rt.config.LoginPath
	if returnPath := localReturnPath(path); returnPath != "" {
		target += "?" + url.Values{"next": []string{returnPath}}.Encode()
	}
	return target
}
