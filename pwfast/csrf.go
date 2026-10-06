package pwfast

import (
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/shibukawa/popcornweb/internal/pathpattern"
	"github.com/shibukawa/popcornweb/internal/requestorigin"
	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/tinybind-go/fasthttpupdate"
	"github.com/shibukawa/tinybind-go/htmlbind"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// CSRFConfig is the shared configuration, so one setting protects both builds.
type CSRFConfig = pwruntime.CSRFConfig

// CSRFSecret is the per-session secret the check validates against, the same
// slot type the other half registers.
type CSRFSecret = middlewares.CSRFSecret

// CSRFSecretSlot is the registration key of that slot.
const CSRFSecretSlot = middlewares.CSRFSecretSlot

// CSRFRejection answers a request the check refused.
type CSRFRejection func(r *fasthttp.RequestCtx, reason error)

// CSRF refuses a cross-site request that carries no proof it came from a page
// this deployment served.
//
// Every decision it makes is the shared one. The origin comparison, the path
// canonicalisation, the safe-method set and the token derivation all live
// outside this file, and this supplies the request facts each of them reads.
// That split is the whole reason the check is portable: what is left here is
// which header to peek at, and a wrong answer there fails loudly, where a wrong
// answer in any of the shared parts would fail by accepting a request it should
// have refused.
//
// Two checks rather than one, in this order. The origin comparison is what
// stops a cross-site form post, and it runs first because it needs no session
// and refuses the cheapest attack before anything is allocated. The token
// comparison is what stops a request from a page this deployment served but a
// different browser state — most importantly a token minted before a sign-in
// and presented after one.
func CSRF(config CSRFConfig, cookie session.CookieOptions, sameSite http.SameSite,
	reject CSRFRejection, trustedProxies []*net.IPNet) (Middleware, error) {
	include, err := pathpattern.Compile(config.Include)
	if err != nil {
		return nil, err
	}
	exclude, err := pathpattern.Compile(config.Exclude)
	if err != nil {
		return nil, err
	}
	if reject == nil {
		reject = writeCSRFStatus
	}
	// The module owns both halves of the token's name: generation writes the
	// field and this reads it, so taking the reader from there is what keeps
	// the two from disagreeing.
	options := fasthttpupdate.Options{CSRFFieldName: config.FormField, CSRFHeaderName: config.Header}
	trusted := requestorigin.Set(config.TrustedOrigins...)
	proxies := requestorigin.FromNetworks(trustedProxies)
	ttl := config.TTL
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	runtimeCookie := cookie
	runtimeCookie.Name = config.CookieName
	if runtimeCookie.Name == "" {
		runtimeCookie.Name = pwruntime.CSRFCookieName
	}
	secrets := &csrfSecret{cookie: runtimeCookie, sameSite: sameSite, ttl: ttl}

	// A render asks this issuer for the secret of a request the safe branch
	// below let through without one. A check that is off publishes none, so a
	// later build of the chain does not answer from an earlier one's.
	if config.Enabled {
		csrfIssuer.Store(secrets)
	} else {
		csrfIssuer.Store(nil)
	}
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		if !config.Enabled {
			return next
		}
		return func(r *fasthttp.RequestCtx) {
			if safeMethod(string(r.Method())) {
				// Only an HTML response needs a token to render an unsafe form.
				// API reads and asset requests stay session-free.
				if csrfHTMLRequest(r) {
					secrets.ensure(r)
				}
				next(r)
				return
			}
			// rawPath, not the whole request target: an encoded slash in a
			// query value is not an ambiguous path. See its doc comment.
			path, ok := pathpattern.CanonicalPathOf(string(r.Path()), rawPath(r))
			if !ok {
				// A path that cannot be matched unambiguously could select a
				// different routed target than the one this decided about.
				reject(r, errCSRFPath)
				return
			}
			if !pathpattern.Protected(include, exclude, path) {
				next(r)
				return
			}
			if !requestorigin.MatchesOrigin(
				proxies.OriginOf(string(r.Host()), scheme(r, proxies)),
				string(r.Request.Header.Peek("Origin")),
				string(r.Request.Header.Peek("Referer")), trusted) {
				reject(r, errCSRFOrigin)
				return
			}
			secrets.ensure(r)
			secret, ok := pwruntime.CSRFSecret(r)
			if !ok {
				// Nothing issued a secret, so there is nothing this request
				// could present that would be valid.
				reject(r, errCSRFNoSession)
				return
			}
			// The presented token carries its own pad, so the value to compare
			// against is rebuilt from it rather than being the stored secret.
			presented := options.CSRFToken(r)
			if err := options.VerifyCSRF(r, pwruntime.ExpectedCSRFToken(secret, presented)); err != nil {
				reject(r, err)
				return
			}
			next(r)
		}
	}, nil
}

// csrfSecret issues and reads the secret, and keeps the companion cookie the
// browser runtime reads in step with it.
type csrfSecret struct {
	cookie   session.CookieOptions
	sameSite http.SameSite
	ttl      time.Duration
}

// ensure records a CSRF secret on the request, minting one when the browser has
// none.
//
// Every failure here leaves the request without a secret and the check that
// follows refuses it, which is the safe direction: a request that could not be
// given a secret is one whose token cannot be verified.
func (c *csrfSecret) ensure(r *fasthttp.RequestCtx) (string, bool) {
	handle, ok := session.Value[CSRFSecret](r)
	if !ok {
		return "", false
	}
	held, present := handle.Get()
	minted := false
	if !present || held.Secret == "" {
		secret, err := pwruntime.NewCSRFSecret(nil)
		if err != nil {
			return "", false
		}
		if err := handle.Set(CSRFSecret{Secret: secret}); err != nil {
			return "", false
		}
		held, minted = CSRFSecret{Secret: secret}, true
	}
	// The runtime reads its token from an ordinary cookie, so a newly minted
	// secret needs one written beside it. A lost token cookie is rewritten too,
	// which is what keeps the pair self-healing after a rotation.
	if minted || len(r.Request.Header.Cookie(c.cookie.Name)) == 0 {
		c.writeRuntimeCookie(r, held.Secret)
	}
	// Recorded on the request, so whoever asks next reads it from there and
	// the cookie above is written once however many renders one request runs.
	pwruntime.StoreCSRFSecret(r, held.Secret)
	return held.Secret, true
}

// csrfIssuer is the issuer of the check this process built, published so that
// a render can ask it for the secret a request was not handed. It is the other
// transport's arrangement, for the reason given there: what a render needs
// from the check is the cookie policy it was built with, which belongs to no
// request, and carrying it on every request would charge an API read for
// something only a page render asks.
var csrfIssuer atomic.Pointer[csrfSecret]

// csrfRenderToken derives the token every unsafe form of a render carries.
//
// The secret is the one the check recorded on the request, when it did. The
// check hands one to the safe requests that announce a page and to nobody
// else, so a render reached by any other request — a script's fetch, a swap
// library's, a command line client's — asks the issuer here instead of
// reaching its forms with nothing.
//
// A request with no secret to be had yields nothing, which fails a render that
// reaches a form rather than emitting a field nothing could verify.
func csrfRenderToken(r *fasthttp.RequestCtx) string {
	secret, ok := pwruntime.CSRFSecret(r)
	if !ok {
		if csrfDisabled(r) {
			return ""
		}
		issuer := csrfIssuer.Load()
		if issuer == nil {
			return ""
		}
		if secret, ok = issuer.ensure(r); !ok {
			return ""
		}
	}
	token, err := pwruntime.CSRFToken(secret, nil)
	if err != nil {
		return ""
	}
	return token
}

// csrfDisabled reports a deployment that turned the check off, which is the
// setting rather than the absence of a secret.
func csrfDisabled(r *fasthttp.RequestCtx) bool {
	return !ConfigContext[pwconfig.SecurityConfig](r).CSRF.Enabled
}

// appendCSRFOption gives one render what it has to be told about the token.
//
// Every render entry of this runtime calls it. None of them did: the check
// recorded a secret and verified submissions against it, and nothing ever
// handed a render the token, so this build answered 500 for any page, fragment
// or region holding an unsafe form. The rule itself is the shared leaf's, so
// the two builds of one application cannot answer it differently.
func appendCSRFOption(options []htmlbind.Option, r *fasthttp.RequestCtx) []htmlbind.Option {
	return pwruntime.AppendCSRFOption(options, csrfRenderToken(r), csrfDisabled(r))
}

// writeRuntimeCookie hands the browser runtime a masked token.
//
// The value is masked like every other emission: the cookie is not in a
// compressed body, so it is not what a compression oracle reads, but sending
// the bare secret would put the thing verification compares against into a
// place script can read.
func (c *csrfSecret) writeRuntimeCookie(r *fasthttp.RequestCtx, secret string) {
	if c.cookie.Name == "" || secret == "" {
		return
	}
	token, err := pwruntime.CSRFToken(secret, nil)
	if err != nil || token == "" {
		return
	}
	Carrier{ctx: r}.SetCookie(&http.Cookie{
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

// scheme resolves what the client actually reached this deployment over,
// through the shared rule.
func scheme(r *fasthttp.RequestCtx, proxies requestorigin.Proxies) string {
	return proxies.SchemeOf(r.IsTLS(), r.RemoteIP().String(),
		string(r.Request.Header.Peek("X-Forwarded-Proto")))
}

// safeMethod and csrfHTMLRequest are this transport's header reads over the
// shared decisions, so the two builds cannot drift on either answer. TRACE was
// once in this build's safe set and the other's not — exactly the divergence
// the shared function exists to prevent.
func safeMethod(method string) bool {
	return pwruntime.CSRFSafeMethod(method)
}

func csrfHTMLRequest(r *fasthttp.RequestCtx) bool {
	header := &r.Request.Header
	return pwruntime.CSRFRendersHTML(string(header.Peek("Sec-Fetch-Dest")), string(header.Peek("Accept")),
		string(header.Peek(pwruntime.UpdateRenderHeader)), string(header.Peek(pwruntime.ResponseModeHeader)))
}

// writeCSRFStatus answers a refused request.
//
// The reason is logged rather than sent. A caller learning which of the checks
// refused it learns how to satisfy that check, and the body says only that the
// request was refused.
func writeCSRFStatus(r *fasthttp.RequestCtx, reason error) {
	pwruntime.ReadLogger(r).Log(r, pwruntime.LevelWarn, "csrf check refused a request",
		pwruntime.String("reason", reason.Error()),
		pwruntime.String("path", string(r.Path())))
	r.Response.Header.Set("Cache-Control", "no-store")
	WriteProblem(r, Forbidden("request refused by the cross-site request check"))
}

var (
	errCSRFPath      = csrfError("request path cannot be matched unambiguously")
	errCSRFOrigin    = csrfError("request origin is not this deployment")
	errCSRFNoSession = csrfError("request carries no session secret")
)

type csrfError string

func (e csrfError) Error() string { return "csrf: " + string(e) }
