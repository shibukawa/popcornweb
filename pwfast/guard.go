package pwfast

import (
	"github.com/shibukawa/popcornweb/internal/pathpattern"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// rawPath is the request path as it arrived, before this transport normalized
// and percent-decoded it. It is the second argument every path-scoped policy
// passes to the shared canonical check.
//
// It is PathOriginal rather than RequestURI, which is what these frames used
// to pass. RequestURI is the whole request target, query string included, so
// "/admin?next=%2Fdashboard" — an ordinary request with an encoded slash in a
// parameter, which is what a return path looks like — was refused as if its
// path were ambiguous. The check is about the path, so it reads the path.
func rawPath(r *fasthttp.RequestCtx) string { return string(r.URI().PathOriginal()) }

// GuardPolicy is what a guard needs to decide, in a form that names no
// transport and no identity provider.
//
// It is supplied rather than derived because deciding is not this package's
// job: which paths are protected and where an unauthenticated visitor is sent
// are an authentication plugin's, and this half applies what that plugin
// resolved. The same separation lets an application without one write a policy
// of its own.
type GuardPolicy struct {
	// Protected reports whether a path requires an authenticated caller. A nil
	// value protects nothing, which is the shape a deployment with no
	// protection configured has.
	Protected func(path string) bool
	// LoginURL is where an unauthenticated browser is sent. An empty result
	// answers 401 instead, which is also what Redirect being false does.
	LoginURL func(path string) string
	// Redirect sends a request that asks for a page to LoginURL. Every other
	// unauthenticated request is answered 401, and so is every request when it
	// is false. An API-only deployment wants the second for all of them, and a
	// redirect there would answer a fetch with a login page.
	//
	// A redirect is an answer to a person. An icon, an image, or a script is
	// fetched by a browser that follows it all the same, and the request that
	// follows reaches the login endpoint on the way to a login page nobody sees.
	Redirect bool
	// BearerRealm names the scheme a 401 asks for, per RFC 6750, so a client
	// that sent nothing learns what to send. Empty sends no challenge.
	BearerRealm string
}

// announcesPage is this transport's header read over the shared decision, so
// the two builds cannot drift on which requests are sent to the login.
func announcesPage(r *fasthttp.RequestCtx) bool {
	header := &r.Request.Header
	return pwruntime.AnnouncesPage(string(header.Peek("Sec-Fetch-Dest")), string(header.Peek("Accept")),
		string(header.Peek(pwruntime.UpdateRenderHeader)), string(header.Peek(pwruntime.ResponseModeHeader)))
}

// Guard requires an authenticated caller on every protected path.
//
// The path is canonicalised through the shared check first and a request whose
// path cannot be matched unambiguously is refused, because such a path could
// select a different routed target than the one this decided about. That is the
// same refusal the CSRF check makes, for the same reason, from the same
// function.
func Guard(policy GuardPolicy) Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		if policy.Protected == nil {
			return next
		}
		return func(r *fasthttp.RequestCtx) {
			path, ok := pathpattern.CanonicalPathOf(string(r.Path()), rawPath(r))
			if !ok {
				WriteProblem(r, BadRequest())
				return
			}
			if !policy.Protected(path) || Authenticated(r) {
				next(r)
				return
			}
			// Never cached: the answer depends on who is asking and on what they
			// asked for, and a shared cache holding one would show a signed-in
			// page to a stranger or a login redirect to a signed-in reader.
			r.Response.Header.Set("Cache-Control", "no-store")
			// Only a request that asks for a page is sent to the login, for the
			// reason the net/http half states: a browser follows a redirect for an
			// icon as readily as for a page, and the login endpoint it lands on
			// uses up the one-time mark a sign-out left behind.
			if policy.Redirect && policy.LoginURL != nil && announcesPage(r) {
				if target := policy.LoginURL(path); target != "" {
					// Through Redirect rather than by hand, so the target is
					// refused unless a browser can follow it without running
					// script — a login URL carries a return path taken from the
					// request.
					Redirect(r, target, fasthttp.StatusSeeOther)
					return
				}
			}
			if policy.BearerRealm != "" {
				r.Response.Header.Set("WWW-Authenticate", `Bearer realm="`+policy.BearerRealm+`"`)
			}
			WriteProblem(r, Unauthorized())
		}
	}
}
