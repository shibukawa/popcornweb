package pwfast

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/shibukawa/popcornweb/internal/apidoc"
	"github.com/shibukawa/popcornweb/internal/requestorigin"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// Slot orders every frame of the request chain, and the numbers are the shared
// leaf's, so a chain assembled here runs in the order the other transport's
// does.
//
// That the numbers are shared is the point rather than a tidiness: a chain
// whose frames run in a different order on the second transport is a different
// application. A guard running after the session on one and before it on the
// other would authorize differently, and nothing about either response would
// say so.
type Slot = pwruntime.Slot

const (
	SlotMetrics          = pwruntime.SlotMetrics
	SlotTracing          = pwruntime.SlotTracing
	SlotResources        = pwruntime.SlotResources
	SlotClientAddress    = pwruntime.SlotClientAddress
	SlotRequestID        = pwruntime.SlotRequestID
	SlotAccessLog        = pwruntime.SlotAccessLog
	SlotRecover          = pwruntime.SlotRecover
	SlotRateLimitProcess = pwruntime.SlotRateLimitProcess
	SlotSecurityHeaders  = pwruntime.SlotSecurityHeaders
	SlotRequestTimeout   = pwruntime.SlotRequestTimeout
	SlotMaxRequestBody   = pwruntime.SlotMaxRequestBody
	SlotPublicAssets     = pwruntime.SlotPublicAssets
	SlotOperational      = pwruntime.SlotOperational
	SlotStorage          = pwruntime.SlotStorage
	SlotSession          = pwruntime.SlotSession
	SlotAuthentication   = pwruntime.SlotAuthentication
	SlotRateLimit        = pwruntime.SlotRateLimit
	SlotCSRF             = pwruntime.SlotCSRF
	SlotGuard            = pwruntime.SlotGuard
	SlotAPIDoc           = pwruntime.SlotAPIDoc
)

// Frame is one positioned step of the chain.
type Frame = pwruntime.Frame[fasthttp.RequestHandler]

// Compose wraps handler in frames, outermost first by slot, through the shared
// ordering rule.
func Compose(handler fasthttp.RequestHandler, frames ...Frame) fasthttp.RequestHandler {
	return pwruntime.Compose(handler, frames)
}

// ResolveClientAddress records the caller every downstream bound counts
// against.
//
// The walk through the forwarded chain is the shared one, so this transport and
// the other name the same caller for the same request. Getting that wrong is
// quiet: an unresolved address counts the proxy, and every rate limit and live
// bound then applies to one address for every visitor.
func ResolveClientAddress(trustedProxies []*net.IPNet) Middleware {
	proxies := requestorigin.FromNetworks(trustedProxies)
	var undeclared sync.Once
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(r *fasthttp.RequestCtx) {
			if proxies.Empty() {
				if forwardedBy(r) {
					undeclared.Do(func() {
						pwruntime.ReadLogger(r).Log(r, pwruntime.LevelWarn,
							"request carries forwarding headers but no trusted proxy is configured")
					})
				}
				// With nothing trusted the client is the peer, so the
				// forwarded chain is not even collected.
				pwruntime.StoreClientAddress(r, r.RemoteIP().String())
				next(r)
				return
			}
			pwruntime.StoreClientAddress(r, proxies.ClientAddressOf(
				r.RemoteIP().String(), forwardedFor(r)))
			next(r)
		}
	}
}

func forwardedBy(r *fasthttp.RequestCtx) bool {
	return len(r.Request.Header.Peek("X-Forwarded-For")) > 0 ||
		len(r.Request.Header.Peek("X-Forwarded-Proto")) > 0
}

// forwardedFor collects every X-Forwarded-For line, because the header
// legitimately repeats and only reading the first would drop hops. PeekAll
// rather than a walk: visiting every header allocated a string per name on
// every request just to compare it.
func forwardedFor(r *fasthttp.RequestCtx) []string {
	matched := r.Request.Header.PeekAll("X-Forwarded-For")
	if len(matched) == 0 {
		return nil
	}
	lines := make([]string, len(matched))
	for index, value := range matched {
		lines[index] = string(value)
	}
	return lines
}

// RuntimeOptions are what Middlewares needs that configuration does not carry.
type RuntimeOptions struct {
	// Resources is the capsule every request is served with. A zero value
	// serves requests with the process defaults.
	Resources pwruntime.Resources
	// TrustedProxies are the networks whose forwarding headers this deployment
	// reads.
	TrustedProxies []*net.IPNet
	// Tracing installs the request root span. It is a runtime option rather
	// than a setting because whether a span has anywhere to go is decided by
	// the exporter this process built, not by a configuration key.
	Tracing bool
	// PublicFS is the embedded public tree. It is supplied rather than read
	// from configuration because an embed is a compile-time fact of the
	// application binary rather than something a settings file can name.
	PublicFS fs.FS
	// Session is the manager the session frame resolves through, or nil where a
	// deployment disabled session storage. It is supplied rather than built
	// here because building one is startup work — a registry, a keyring, a
	// backend — and startup belongs to whichever runtime binds configuration.
	Session *session.Manager
	// SessionCookie and SessionSameSite describe the cookie the CSRF companion
	// is issued beside, so the two travel with the same policy.
	SessionCookie   session.CookieOptions
	SessionSameSite http.SameSite
	// Guard is the authorization policy. It is supplied rather than derived
	// because deciding which paths are protected belongs to an authentication
	// plugin, and this half applies what that plugin resolved.
	Guard GuardPolicy
	// RateLimitCounter is the storage the limiter counts in, or nil where a
	// deployment left the limiter off. Like the session manager it is supplied
	// rather than opened here, because opening it is startup work: a Redis
	// counter dials a server and refuses to start against one it cannot reach.
	//
	// A configuration that enables the limiter and supplies no counter is
	// refused rather than served without one, per policy:absent-rather-than-stubbed:
	// a limiter that admits everything is a control that looks installed.
	RateLimitCounter RateLimitCounter
	// Extra frames are installed alongside the framework's, positioned by their
	// own slots.
	Extra []Frame
}

// Middlewares builds the framework chain around handler.
//
// It is the second transport's counterpart to pw.Middlewares, and it is
// deliberately smaller than that one. The other builds the whole chain and also
// performs framework initialization — configuration parsing, database startup,
// observability, the validations that must fail before a port is bound. None of
// that is transport-shaped and none of it is duplicated here: a deployment runs
// it once, on whichever runtime owns startup, and what this assembles is the
// request path.
//
// # The authentication frames
//
// They are not here, and their absence is now a choice rather than a gap.
// popcornweb/plugin/auth/authfast supplies them, as frames positioned by their
// own slots and a guard policy this takes as an argument, because there is no
// extension registry on this transport: an imported capability cannot install a
// frame here, and every frame this chain gains from a plugin is one the
// application named.
//
// What the application registers for itself is the exception, and a deliberate
// one: RegisterMiddleware writes into a process list this reads, so the
// registration reads the same on both builds. The frame is still one the
// application's own source names — its main called the function — which is the
// property the argument-assembled chain was protecting.
//
// What is still absent is absent rather than stubbed, so a build that needs one
// fails to name it rather than serving requests with a frame that silently does
// nothing — which for a guard would be an authorization check that looks
// installed.
func Middlewares(handler fasthttp.RequestHandler, options RuntimeOptions) (fasthttp.RequestHandler, error) {
	if handler == nil {
		return nil, errors.New("popcornweb: nil handler")
	}
	settings, ok := pwruntime.ResolvedChainSettings()
	if !ok {
		// Composing from zero values would produce a chain with no recovery
		// frame, no request ID and no security headers, which serves requests
		// and looks like a chain. Refusing names the actual problem.
		return nil, errors.New("popcornweb: no chain settings published; the runtime that binds configuration has not run")
	}
	trusted := options.TrustedProxies
	if len(trusted) == 0 {
		if compiled, err := compileTrustedProxies(settings.TrustedProxies); err == nil {
			trusted = compiled
		} else {
			return nil, err
		}
	}

	frames := []Frame{
		{Slot: SlotResources, Name: "resources", Middleware: InjectResources(options.Resources)},
		{Slot: SlotClientAddress, Name: "client_address", Middleware: ResolveClientAddress(trusted)},
	}
	if settings.RequestID {
		frames = append(frames, Frame{Slot: SlotRequestID, Name: "request_id", Middleware: RequestID()})
	}
	if settings.AccessLog {
		frames = append(frames, Frame{Slot: SlotAccessLog, Name: "access_log", Middleware: AccessLog()})
	}
	if settings.Recovery {
		frames = append(frames, Frame{Slot: SlotRecover, Name: "recover", Middleware: Recover(writePanicProblem)})
	}
	// One frame for both halves, installed by either, at the position both
	// wanted: above every frame below that can refuse.
	if settings.SecurityHeaders.Enabled || settings.CORS.Enabled {
		headers, err := SecurityHeaders(settings.SecurityHeaders,
			WithTrustedProxies(trusted), WithCORS(settings.CORS, settings.CSRF.Header))
		if err != nil {
			return nil, err
		}
		frames = append(frames, Frame{Slot: SlotSecurityHeaders, Name: "security_headers", Middleware: headers})
	}
	if settings.RequestTimeout > 0 {
		frames = append(frames, Frame{Slot: SlotRequestTimeout, Name: "request_timeout", Middleware: RequestTimeout(settings.RequestTimeout)})
	}
	if settings.MaxRequestBody > 0 {
		frames = append(frames, Frame{Slot: SlotMaxRequestBody, Name: "max_request_body", Middleware: MaxRequestBody(settings.MaxRequestBody)})
	}
	if options.PublicFS != nil && settings.Public.Enabled {
		assets, err := PublicAssets(settings.Public, options.PublicFS)
		if err != nil {
			return nil, err
		}
		frames = append(frames, Frame{Slot: SlotPublicAssets, Name: "public_assets", Middleware: assets})
	}
	if options.Session != nil {
		frames = append(frames, Frame{Slot: SlotSession, Name: "session",
			Middleware: Session(options.Session, nil)})
	}
	if settings.RateLimit.Enabled {
		limits := settings.RateLimit
		exempt := rateLimitExemptions(settings)
		deps := RateLimitDeps{Counter: options.RateLimitCounter, Exempt: exempt,
			Degraded: logRateLimitDegraded}
		if settings.RateLimit.Process > 0 {
			// The ceiling belongs in the outer stack where a refusal has cost
			// least; the identity bucket below authentication where the subject
			// exists. Neither can be the other's slot.
			ceiling, err := ProcessRateLimiter(limits, deps)
			if err != nil {
				return nil, err
			}
			frames = append(frames, Frame{Slot: SlotRateLimitProcess, Name: "ratelimit.process", Middleware: ceiling})
		}
		limiter, err := RateLimiter(limits, deps)
		if err != nil {
			return nil, err
		}
		frames = append(frames, Frame{Slot: SlotRateLimit, Name: "ratelimit", Middleware: limiter})
	}
	if settings.CSRF.Enabled {
		// The check is built even when the session frame is absent, and refuses
		// rather than passing: with no session there is nothing a request could
		// present that would be valid, and letting it through would be the one
		// failure direction this check must not have.
		check, err := CSRF(settings.CSRF, options.SessionCookie, options.SessionSameSite, nil, trusted)
		if err != nil {
			return nil, err
		}
		frames = append(frames, Frame{Slot: SlotCSRF, Name: "csrf", Middleware: check})
	}
	frames = append(frames, Frame{Slot: SlotOperational, Name: "operational",
		Middleware: OperationalEndpoints(settings.Health, settings.Readiness, options.Resources)})
	// The framework's own browser assets, at the same slot the probes answer
	// from and above every application route: the prefix is reserved, so it is
	// answered and closed before anything else sees it.
	frames = append(frames, Frame{Slot: SlotOperational, Name: "framework_assets",
		Middleware: FrameworkAssets()})
	// Resolved before the chain is composed, so a catalog with nothing to link
	// refuses startup here exactly as it does on the other transport: the
	// resolver is the shared leaf's and neither transport validates its own.
	catalog, err := pwruntime.ResolveAPICatalog(pwruntime.APICatalogSettings{
		Enabled: settings.APICatalog, Origin: settings.APICatalogOrigin,
		OpenAPI: settings.OpenAPI, APIDoc: settings.APIDoc,
		APIDocPath: settings.APIDocPath, Health: settings.Health,
	})
	if err != nil {
		return nil, err
	}
	frames = append(frames, Frame{Slot: SlotAPIDoc, Name: "apidoc",
		Middleware: DocumentationEndpoints(settings.OpenAPI, settings.APIDoc, settings.APIDocPath, catalog)})
	if options.Guard.Protected != nil {
		frames = append(frames, Frame{Slot: SlotGuard, Name: "guard", Middleware: Guard(options.Guard)})
	}
	if options.Tracing {
		// Outermost of everything positioned, so the request root span covers
		// the whole chain and every record taken inside it correlates. It is
		// opt-in because a span with nowhere to export is pure cost.
		frames = append(frames, Frame{Slot: SlotTracing, Name: "otel", Middleware: Otel()})
	}
	// Above tracing, and on its own switch: an instrument counts every request
	// whatever the sampler kept, which is why the two are never one frame.
	if options.Resources.Metrics != nil {
		frames = append(frames, Frame{Slot: SlotMetrics, Name: "metrics", Middleware: Metrics(options.Resources.Metrics)})
	}
	frames = append(frames, options.Extra...)
	// The application's own middleware last, so a frame sharing a number with a
	// framework frame or a plugin's runs inside it. This is read here rather
	// than by Run alone, so the chain a test assembles through this call is the
	// chain Run serves; a registry only the entry point consulted would let the
	// two differ with nothing to say so.
	frames = append(frames, registeredMiddleware()...)
	return Compose(handler, frames...), nil
}

// writePanicProblem answers a recovered panic with the framework problem
// document, which is what the other half answers with.
func writePanicProblem(r *fasthttp.RequestCtx, err error) {
	pwruntime.ReadLogger(r).Log(r, pwruntime.LevelError, "recovered panic",
		pwruntime.String("error", err.Error()))
	r.Response.ResetBody()
	WriteProblem(r, InternalServerError(err))
}

// ListenAndServe builds the chain, binds address, and serves until ctx is
// cancelled.
//
// It owns the port and nothing else. Startup — parsing the configuration,
// opening the pool, building the session manager — belongs to whichever runtime
// does it, and a caller that wants this transport to do all of it calls Run
// instead. This is the entry for a caller that has already done startup its own
// way, which is what a test and an embedding application both need.
func ListenAndServe(ctx context.Context, address string, handler fasthttp.RequestHandler, options RuntimeOptions) error {
	if ctx == nil {
		return errors.New("popcornweb: nil context")
	}
	wrapped, err := Middlewares(handler, options)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	return Serve(ctx, listener, wrapped)
}

// Serve runs an already-built handler on a listener the caller owns, which is
// what a test and an application embedding this framework both need.
func Serve(ctx context.Context, listener net.Listener, handler fasthttp.RequestHandler) error {
	server := &fasthttp.Server{Handler: handler}
	failed := make(chan error, 1)
	go func() { failed <- server.Serve(listener) }()
	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
		// Shutdown closes the listener and waits for in-flight requests, so a
		// cancelled context ends the process without cutting a response in half.
		if err := server.Shutdown(); err != nil {
			return err
		}
		<-failed
		return nil
	}
}

// compileTrustedProxies turns configured addresses and CIDR blocks into the
// trust set, naming the offending value when one does not parse.
func compileTrustedProxies(values []string) ([]*net.IPNet, error) {
	proxies, err := requestorigin.Compile(values)
	if err != nil {
		return nil, err
	}
	return proxies.Networks(), nil
}

// OperationalEndpoints answers the liveness and readiness probes above
// everything that authenticates.
//
// The probes reveal only status and are reachable by anything that can reach
// the port, which is what a liveness probe needs and what keeps a dependency
// outage from turning into a restart loop. Readiness is the shared probe, so
// the same process reports the same readiness whichever transport asked.
//
// A path left empty installs nothing for it, which is how a deployment turns
// one off.
func OperationalEndpoints(health, readiness string, resources pwruntime.Resources) Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		if health == "" && readiness == "" {
			return next
		}
		return func(r *fasthttp.RequestCtx) {
			switch path := string(r.Path()); {
			case health != "" && path == health:
				writeOperationalStatus(r, true)
			case readiness != "" && path == readiness:
				writeOperationalStatus(r, pwruntime.DatabasesReady(r, resources))
			default:
				next(r)
			}
		}
	}
}

// writeOperationalStatus answers a probe.
//
// Only GET and HEAD are answered, because a probe endpoint that accepts any
// method is one an arbitrary caller can POST to, and the reply says nothing but
// costs a database round trip on the readiness path.
func writeOperationalStatus(r *fasthttp.RequestCtx, healthy bool) {
	if !operationalMethod(r) {
		return
	}
	method := string(r.Method())
	r.Response.Header.Set("Cache-Control", "no-store")
	r.Response.Header.SetContentType("text/plain; charset=utf-8")
	status, body := fasthttp.StatusOK, "ok\n"
	if !healthy {
		status, body = fasthttp.StatusServiceUnavailable, "unavailable\n"
	}
	r.SetStatusCode(status)
	if method != fasthttp.MethodHead {
		_, _ = r.WriteString(body)
	}
}

// DocumentationEndpoints answers the OpenAPI document and the UI over it.
//
// Unlike the probes it belongs beneath whatever protects the routes it
// describes: an API description is a map of the whole application surface, so
// reaching it costs a session where the configuration says so. That is why its
// slot is below the guard rather than beside the operational frame, and why
// this returns a frame rather than being folded into the one above it.
//
// A configuration naming neither returns the handler unchanged, so the common
// case adds nothing to the chain.
func DocumentationEndpoints(openAPIPath, docKind, docPath string,
	catalog pwruntime.ResolvedAPICatalog) Middleware {
	page, hasPage := apidoc.Build(docKind, openAPIPath)
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		if openAPIPath == "" && !hasPage && !catalog.Enabled() {
			return next
		}
		return func(r *fasthttp.RequestCtx) {
			switch path := string(r.Path()); {
			case catalog.Enabled() && path == pwruntime.APICatalogPath:
				if !operationalMethod(r) {
					return
				}
				document := catalog.Document()
				header := &r.Response.Header
				header.SetContentType(pwruntime.APICatalogContentType)
				// Section 2 asks a HEAD to carry the relation; sending it on
				// both costs nothing and answers a client that read only the
				// headers of a GET.
				header.Set("Link", pwruntime.APICatalogLinkHeader)
				// Readable from anywhere, for the reason
				// pwruntime.OpenAPIDocumentOrigin gives about the document this
				// points at, and identically on both transports because the
				// value is declared once there.
				header.Set(pwruntime.OpenAPIDocumentOrigin.Name, pwruntime.OpenAPIDocumentOrigin.Value)
				if string(r.Method()) == fasthttp.MethodHead {
					// The length is the answer a HEAD is asking for, so it is
					// reported rather than left at zero.
					header.SetContentLength(len(document))
					return
				}
				_, _ = r.Write(document)
				return
			case openAPIPath != "" && path == openAPIPath:
				if !operationalMethod(r) {
					return
				}
				// Readable from anywhere, for the reason
				// pwruntime.OpenAPIDocumentOrigin gives, and identically on
				// both transports because the value is declared once there.
				r.Response.Header.Set(pwruntime.OpenAPIDocumentOrigin.Name, pwruntime.OpenAPIDocumentOrigin.Value)
				if string(r.Method()) == fasthttp.MethodHead {
					// The document is assembled either way, because its length
					// is the answer a HEAD is asking for.
					OpenAPIJSON(r)
					r.Response.ResetBody()
					return
				}
				OpenAPIJSON(r)
			case hasPage && docPath != "" && path == docPath:
				if !operationalMethod(r) {
					return
				}
				writeAPIDocPage(r, page)
			default:
				next(r)
			}
		}
	}
}

// writeAPIDocPage sends the composed page under the policy it needs.
//
// The policy replaces the application's rather than widening it, and only where
// one is already set: the security header frame wraps this endpoint, so the
// configured policy is on the response while it is still uncommitted, and
// widening the configured value instead would carry the CDN and inline
// allowances into every response the application sends.
func writeAPIDocPage(r *fasthttp.RequestCtx, page apidoc.Page) {
	if page.CSP != "" {
		for _, name := range apidoc.RelaxedPolicyNames {
			if len(r.Response.Header.Peek(name)) > 0 {
				r.Response.Header.Set(name, page.CSP)
			}
		}
	}
	r.Response.Header.SetContentType("text/html; charset=utf-8")
	r.SetStatusCode(fasthttp.StatusOK)
	if string(r.Method()) != fasthttp.MethodHead {
		_, _ = r.WriteString(page.HTML)
	}
}

// operationalMethod refuses a method these endpoints do not answer, reporting
// whether the caller may continue.
func operationalMethod(r *fasthttp.RequestCtx) bool {
	if method := string(r.Method()); method == fasthttp.MethodGet || method == fasthttp.MethodHead {
		return true
	}
	r.Response.Header.Set("Allow", "GET, HEAD")
	r.SetStatusCode(fasthttp.StatusMethodNotAllowed)
	return false
}

// rateLimitExemptions are the endpoints the framework itself owns and routes.
//
// They are not a deployment setting. A readiness probe arrives from the proxy
// on the same address as every anonymous caller and would exhaust that bucket
// by itself, and one page view fetches many assets; counting either turns the
// limit into an outage on the first deploy.
//
// The list is derived from the same settings the frames that serve those
// endpoints are built from, so a probe moved to another path is exempt at its
// new one without anything else being told.
func rateLimitExemptions(settings pwruntime.ChainSettings) []string {
	exempt := make([]string, 0, 6)
	for _, path := range []string{settings.Health, settings.Readiness, settings.OpenAPI, settings.APIDoc} {
		if path = strings.TrimSpace(path); path != "" && strings.HasPrefix(path, "/") {
			exempt = append(exempt, path)
		}
	}
	if settings.APIDoc != "" && settings.APIDocPath != "" && strings.HasPrefix(settings.APIDocPath, "/") {
		exempt = append(exempt, settings.APIDocPath, strings.TrimSuffix(settings.APIDocPath, "/")+"/**")
	}
	if settings.Public.Enabled && strings.HasPrefix(settings.Public.Mount, "/") {
		exempt = append(exempt, strings.TrimSuffix(settings.Public.Mount, "/")+"/**")
	}
	return exempt
}

// logRateLimitDegraded records an admission made without a working store.
//
// The request was admitted on purpose: the edge still has its own limits, and
// refusing here would convert a store incident into an outage of every limited
// route at once. Silently not limiting is the state worth knowing about.
func logRateLimitDegraded(r *fasthttp.RequestCtx, err error) {
	pwruntime.ReadLogger(r).Log(r, pwruntime.LevelError,
		"rate limit admitted a request without counting it",
		pwruntime.String("error", err.Error()), pwruntime.String("path", string(r.Path())))
}
