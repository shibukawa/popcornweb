package middlewares

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/session"
)

// CSRFSecret is the per-session secret the check validates against, held in a
// registered session slot like any other piece of per-browser state.
//
// One slot serves both populations. A visitor with no login gets the secret in
// the sealed cookie a session.Private slot rides while it is anonymous, so no
// server record is written for a crawler that merely loads a page with a form.
// The login rotation moves the same slot onto the configured backend and mints
// a fresh secret with it, which is what stops a token minted before a sign-in
// from being presented after one.
//
// The alternative was a signed cookie for anonymous visitors beside a record
// field for authenticated ones. That split existed to avoid minting a stored
// session per anonymous visitor; a private slot costs no server row while
// anonymous, so the reason for two mechanisms went away.
type CSRFSecret struct {
	Secret string `json:"secret"`
}

// CSRFSecretSlot is the registration key of that slot. The framework declares
// it only where the check is turned on, so a project with CSRF off carries no
// slot and needs no keyring on its account.
const CSRFSecretSlot = "pw_csrf_secret"

// csrfSecrets issues and reads the secret, and keeps the companion cookie the
// browser runtime reads in step with it.
type csrfSecret struct {
	cookie   session.CookieOptions
	sameSite http.SameSite
	ttl      time.Duration
}

// ensure returns the request carrying a CSRF secret, minting one when the
// browser has none.
//
// It runs for protected unsafe requests and for safe requests that say they
// will render HTML, because those are the requests known up front to validate
// or render a form token. A render the request did not announce asks through
// ResolveCSRFSecret instead.
func (c *csrfSecret) ensure(w http.ResponseWriter, r *http.Request) *http.Request {
	secret, ok := c.resolve(w, r)
	if !ok {
		return r
	}
	return r.WithContext(pwruntime.WithCSRFSecret(r.Context(), secret))
}

// resolve reads the secret out of the session slot, minting one when the
// browser has none, and keeps the companion cookie in step with it.
//
// Every failure leaves the caller without a secret, which is the safe
// direction for both of them: the check refuses a request it could not give
// one to, and a render that reaches a form fails rather than emitting a field
// nothing can verify.
func (c *csrfSecret) resolve(w http.ResponseWriter, r *http.Request) (string, bool) {
	handle, ok := session.Value[CSRFSecret](r.Context())
	if !ok {
		// No session middleware, or the slot was not declared.
		return "", false
	}
	held, present := handle.Get()
	minted := false
	if !present || held.Secret == "" {
		// A secret minted now reaches the browser as two cookies, and a
		// response that has already started cannot carry them. The form would
		// hold a token for a secret the browser was never given, so its
		// submission would be refused; no secret is the honest answer.
		if Committed(w) {
			return "", false
		}
		secret, err := pwruntime.NewCSRFSecret(nil)
		if err != nil {
			return "", false
		}
		if err := handle.Set(CSRFSecret{Secret: secret}); err != nil {
			// An oversized or unwritable slot leaves the request without a
			// secret.
			return "", false
		}
		held, minted = CSRFSecret{Secret: secret}, true
	}
	// The runtime reads its token from an ordinary cookie, so a newly minted
	// secret needs one written beside it. A lost token cookie is rewritten too,
	// which is what keeps the pair self-healing after a rotation.
	//
	// It is written once per response. The check asks once, but a render asks
	// each time it runs, and a page that failed and then rendered its error
	// page would otherwise send the same cookie twice under two masks.
	if (minted || !hasCookie(r, c.cookie.Name)) && !Committed(w) && !setsCookie(w.Header(), c.cookie.Name) {
		c.writeRuntimeCookie(w, held.Secret)
	}
	return held.Secret, true
}

// csrfIssuer is the issuer of the check this process built, published so that
// a render can ask it for the secret a request was not handed.
//
// It is process state for the reason the resolved chain settings are: one
// process serves one chain, and what a render needs from it is the cookie
// policy the check was constructed with, which is not a property of any
// request. Carrying it on every request instead would cost an API read an
// allocation for something only a page render ever asks.
var csrfIssuer atomic.Pointer[csrfSecret]

// ResolveCSRFSecret returns the request's CSRF secret for a render that turned
// out to need one, minting it when the browser holds none. It reports false
// when the check is not installed or the session has nowhere to keep one.
//
// The check decides up front which safe requests receive a secret, from what a
// request says about itself, so that an API read never touches the session.
// That is a prediction, and it is wrong about every client that renders a page
// without announcing it: a script's fetch, a swap library's request, a command
// line client, another server. Each of those reached the templates with no
// secret, and a page holding an unsafe form answered them 500 — or, where the
// session already held a secret, simply was not shown it.
//
// Asking here is what makes the prediction an optimization rather than a
// requirement. The render is the one place that knows it needs a token, so it
// is the place that asks, and a request that renders nothing still pays for
// nothing.
func ResolveCSRFSecret(w http.ResponseWriter, r *http.Request) (string, bool) {
	issuer := csrfIssuer.Load()
	if issuer == nil || w == nil || r == nil {
		return "", false
	}
	return issuer.resolve(w, r)
}

// writeRuntimeCookie hands the browser runtime a masked token.
//
// The value is masked like every other emission: the cookie is not in a
// compressed body, so it is not what a compression oracle reads, but sending
// the bare secret would put the thing verification compares against into a
// place script can read.
func (c *csrfSecret) writeRuntimeCookie(w http.ResponseWriter, secret string) {
	if c.cookie.Name == "" || secret == "" {
		return
	}
	token, err := pwruntime.CSRFToken(secret, nil)
	if err != nil || token == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:   c.cookie.Name,
		Value:  token,
		Path:   c.cookie.Path,
		Domain: c.cookie.Domain,
		MaxAge: int(c.ttl.Seconds()),
		Secure: c.cookie.Secure,
		// Never HttpOnly: the runtime reads this one.
		HttpOnly: false,
		SameSite: c.sameSite,
	})
}

func hasCookie(r *http.Request, name string) bool {
	cookie, err := r.Cookie(name)
	return err == nil && cookie != nil && cookie.Value != ""
}

// setsCookie reports whether a response already carries a cookie of this name.
func setsCookie(header http.Header, name string) bool {
	for _, line := range header["Set-Cookie"] {
		if len(line) > len(name) && line[len(name)] == '=' && line[:len(name)] == name {
			return true
		}
	}
	return false
}
