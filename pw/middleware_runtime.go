package pw

import (
	"context"
	"github.com/shibukawa/popcornweb/storage"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwruntime"
)

func buildRuntimeHandler(handler http.Handler, server ServerConfig, security SecurityConfig, middleware MiddlewareConfig, resources pwruntime.Resources, tracing bool, publicFS ...fs.FS) (http.Handler, error) {
	trusted, err := compileTrustedProxies(server.TrustedProxies)
	if err != nil {
		return nil, err
	}
	// Published for the other runtime, which binds no configuration of its own
	// and would otherwise compose a chain out of zero values. The reduction is
	// pwconfig's, so the two transports build from one reading of the settings;
	// what this call adds is that it publishes the configuration this chain was
	// actually handed, which a test may have injected.
	pwruntime.PublishChainSettings(pwconfig.ChainSettings(server, security, middleware))
	// Every frame — a framework middleware, an extension, a middleware the
	// application registered — carries a slot on one number line, and the
	// chain is composed by that number alone: ascending, smallest outermost.
	// Only the track frame stays outside the line, because its metrics must
	// observe every positioned step.
	frames := []chainFrame{}
	// Above tracing, and installed on its own switch: an instrument counts every
	// request whatever the sampler kept, which is the whole reason the framework
	// has two signals rather than one.
	if resources.Metrics != nil {
		frames = append(frames, chainFrame{slot: SlotMetrics, name: "metrics", middleware: middlewares.Metrics(resources.Metrics)})
	}
	if tracing {
		// Tracing wraps every positioned frame, so the request root span
		// covers the whole chain and every record taken inside it correlates.
		// It is omitted when nothing exports, because an unsampled span is
		// pure cost.
		frames = append(frames, chainFrame{slot: SlotTracing, name: "otel", middleware: middlewares.Otel()})
	}
	frames = append(frames, chainFrame{slot: SlotResources, name: "resources", middleware: middlewares.InjectResources(resources)})
	// Always installed, because the value it records is what every downstream
	// bound counts against, and an unresolved one silently counts the proxy.
	// With no networks declared it records the peer, which is what those
	// callers read directly before this frame existed.
	frames = append(frames, chainFrame{slot: SlotClientAddress, name: "client_address", middleware: middlewares.ResolveClientAddress(trusted)})
	if middleware.RequestID {
		frames = append(frames, chainFrame{slot: SlotRequestID, name: "request_id", middleware: middlewares.RequestID()})
	}
	if middleware.AccessLog {
		frames = append(frames, chainFrame{slot: SlotAccessLog, name: "access_log", middleware: middlewares.AccessLog()})
	}
	if middleware.Recovery {
		frames = append(frames, chainFrame{slot: SlotRecover, name: "recover", middleware: middlewares.Recover(writePanicProblem)})
	}
	// One frame for both halves of the browser response policy. Either being
	// enabled installs it: a deployment can send no policy headers and still
	// admit another origin, or the reverse, and the position is the same
	// either way because both are written before anything below commits.
	if security.Headers.Enabled || security.CORS.Enabled {
		headers, err := middlewares.SecurityHeaders(security.Headers,
			middlewares.WithTrustedProxies(trusted),
			middlewares.WithCORS(security.CORS, security.CSRF.Header))
		if err != nil {
			return nil, err
		}
		frames = append(frames, chainFrame{slot: SlotSecurityHeaders, name: "security_headers", middleware: headers})
	}
	if middleware.RequestTimeout > 0 {
		frames = append(frames, chainFrame{slot: SlotRequestTimeout, name: "request_timeout", middleware: middlewares.RequestTimeout(middleware.RequestTimeout)})
	}
	if server.MaxRequestBody > 0 {
		frames = append(frames, chainFrame{slot: SlotMaxRequestBody, name: "max_request_body", middleware: middlewares.MaxRequestBody(server.MaxRequestBody)})
	}
	if server.Public.Enabled {
		var embedded fs.FS
		if len(publicFS) > 0 {
			embedded = publicFS[0]
		}
		assets, err := middlewares.PublicAssets(server.Public, embedded)
		if err != nil {
			return nil, err
		}
		frames = append(frames, chainFrame{slot: SlotPublicAssets, name: "public_assets", middleware: assets})
	}
	// A local storage bucket presigns URLs that point back here, so the
	// route that serves them is mounted only when such a bucket is
	// configured; a deployment on S3 or R2 hands out URLs to the store.
	if storage.SelfServed(context.Background()) {
		signed := storage.SignedHandler()
		frames = append(frames, chainFrame{slot: SlotSignedStorage, name: "signed_storage", middleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, storage.SignedPathPrefix) {
					signed.ServeHTTP(w, r)
					return
				}
				next.ServeHTTP(w, r)
			})
		}})
	}
	// The probes stay above everything that authenticates, and the
	// documentation endpoints go beneath the guard, so the session and the
	// authentication reach the OpenAPI document exactly as they reach an
	// application route. Both are handlers adapted to the middleware shape,
	// which is why their slots refuse registration.
	frames = append(frames, chainFrame{slot: SlotOperational, name: "operational", middleware: func(next http.Handler) http.Handler {
		return operationalEndpoints(next, server, resources)
	}})
	// Resolved here rather than inside the frame, so a catalog with nothing to
	// link refuses startup instead of serving a document that conforms to
	// nothing. The resolver is the shared leaf's, so the other transport
	// refuses the same configuration for the same reason.
	catalog, err := pwruntime.ResolveAPICatalog(pwruntime.APICatalogSettings{
		Enabled: server.APICatalog, Origin: server.APICatalogOrigin,
		OpenAPI: server.OpenAPI, APIDoc: server.APIDoc,
		APIDocPath: server.APIDocPath, Health: server.Health,
	})
	if err != nil {
		return nil, err
	}
	frames = append(frames, chainFrame{slot: SlotAPIDoc, name: "apidoc", middleware: func(next http.Handler) http.Handler {
		return documentationEndpoints(next, server, catalog)
	}})
	// Extensions see the same resources the request handler will, so a
	// disabled or misconfigured capability fails during startup rather than on
	// the first request.
	extensions, err := extensionFrames(pwruntime.WithResources(context.Background(), resources))
	if err != nil {
		return nil, err
	}
	frames = append(frames, extensions...)

	// Composed by the shared leaf, so this chain and the other transport's run
	// in one order rather than in two orders that happen to agree.
	composed := make([]pwruntime.Frame[http.Handler], 0, len(frames))
	for _, frame := range frames {
		composed = append(composed, pwruntime.Frame[http.Handler]{
			Slot: frame.slot, Name: frame.name, Middleware: frame.middleware,
		})
	}
	return middlewares.Track(pwruntime.Compose(handler, composed)), nil
}

func writePanicProblem(w http.ResponseWriter, r *http.Request, err error) {
	if responseCommitted(w) {
		LoggerContext(r.Context()).Log(r.Context(), LevelError, "panic after response commit", String("error", err.Error()))
		return
	}
	WriteProblem(w, r, InternalServerError(err))
}

// operationalEndpoints answers the framework assets and the two probes, above
// every extension. Health and readiness reveal only status and are reachable by
// anything that can reach the port, which is what a liveness probe needs and
// what keeps a dependency outage from turning into a restart loop.
func operationalEndpoints(next http.Handler, config ServerConfig, resources pwruntime.Resources) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case serveFrameworkScript(w, r):
			return
		case servePackageAsset(w, r):
			return
		// Below every handler that owns a route inside the reserved prefix, so
		// each gets its turn before the namespace is closed. The redraw route
		// used to sit here; it is answered at the page's own URL now, which is
		// what puts it behind the same authentication the page has.
		case serveReservedPath(w, r):
			return
		case config.Health != "" && r.URL.Path == config.Health:
			writeOperationalStatus(w, r, true)
			return
		case config.Readiness != "" && r.URL.Path == config.Readiness:
			writeOperationalStatus(w, r, databasesReady(r.Context(), resources))
			return
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// documentationEndpoints answers the generated OpenAPI document and the UI over
// it. Unlike the probes it is mounted beneath the extension chain, because an
// API description is a map of the whole application surface and belongs behind
// whatever protects the routes it describes. Reaching it therefore costs a
// session where the configuration says so, and a test that authenticates
// reaches it the same way its own routes are reached.
//
// A configuration that serves neither returns the handler unchanged, so the
// common case adds no frame to the chain.
func documentationEndpoints(next http.Handler, config ServerConfig, catalog pwruntime.ResolvedAPICatalog) http.Handler {
	apiDoc := apiDocUI(config.APIDoc, config.OpenAPI)
	if config.OpenAPI == "" && apiDoc == nil && !catalog.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case catalog.Enabled() && r.URL.Path == pwruntime.APICatalogPath:
			if !operationalMethod(w, r) {
				return
			}
			document := catalog.Document()
			header := w.Header()
			header.Set("Content-Type", pwruntime.APICatalogContentType)
			// Section 2 asks a HEAD to carry the relation. Sending it on both
			// costs nothing and answers a client that looked only at the
			// headers of a GET.
			header.Set("Link", pwruntime.APICatalogLinkHeader)
			// Readable from anywhere, for the reason
			// pwruntime.OpenAPIDocumentOrigin gives about the document this
			// points at. A catalog readable only from origins already known
			// defeats what a discovery endpoint is for.
			header.Set(pwruntime.OpenAPIDocumentOrigin.Name, pwruntime.OpenAPIDocumentOrigin.Value)
			// Set explicitly so a HEAD reports the length a GET would, which is
			// the question a HEAD is asking.
			header.Set("Content-Length", strconv.Itoa(len(document)))
			w.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				_, _ = w.Write(document)
			}
			return
		case config.OpenAPI != "" && r.URL.Path == config.OpenAPI:
			if !operationalMethod(w, r) {
				return
			}
			// Readable from anywhere, whatever the configured CORS policy is.
			// The reason is in pwruntime.OpenAPIDocumentOrigin: this is a
			// property of the document rather than a decision a deployment
			// should have to make about a description it already published.
			w.Header().Set(pwruntime.OpenAPIDocumentOrigin.Name, pwruntime.OpenAPIDocumentOrigin.Value)
			if r.Method == http.MethodHead {
				OpenAPIJSON(headResponseWriter{ResponseWriter: w}, r)
			} else {
				OpenAPIJSON(w, r)
			}
			return
		case apiDoc != nil && r.URL.Path == config.APIDocPath:
			if !operationalMethod(w, r) {
				return
			}
			apiDoc.ServeHTTP(w, r)
			return
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func writeOperationalStatus(w http.ResponseWriter, r *http.Request, healthy bool) {
	if !operationalMethod(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	status := http.StatusOK
	body := "ok\n"
	if !healthy {
		status, body = http.StatusServiceUnavailable, "unavailable\n"
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

func operationalMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	return false
}

type headResponseWriter struct{ http.ResponseWriter }

func (headResponseWriter) Write(body []byte) (int, error) { return len(body), nil }

// databasesReady pings every configured connection. A replica that cannot
// answer makes the instance unready, because the default group is the one the
// application reads from.
// databasesReady answers from the shared probe, so the two transports report
// the same readiness for the same process.
func databasesReady(parent context.Context, resources pwruntime.Resources) bool {
	return pwruntime.DatabasesReady(parent, resources)
}
