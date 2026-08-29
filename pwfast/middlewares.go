package pwfast

import (
	"fmt"
	"net"
	"time"

	"github.com/shibukawa/popcornweb/internal/requestorigin"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// SecurityHeadersConfig and its parts are the shared leaf's, so a project
// configures one set of headers and both builds send it.
type (
	SecurityHeadersConfig = pwruntime.SecurityHeadersConfig
	HSTSConfig            = pwruntime.HSTSConfig
	CORSConfig            = pwruntime.CORSConfig
)

// DefaultSecurityHeaders returns the classic mode defaults.
func DefaultSecurityHeaders() SecurityHeadersConfig { return pwruntime.DefaultSecurityHeaders() }

// DefaultCORS returns the shipped cross-origin defaults.
func DefaultCORS() CORSConfig { return pwruntime.DefaultCORS() }

// SecurityHeadersOption configures SecurityHeaders.
type SecurityHeadersOption func(*securityHeadersOptions)

type securityHeadersOptions struct {
	trustedProxies []*net.IPNet
	cors           pwruntime.CORSConfig
	csrfHeader     string
}

// WithCORS gives the frame the cross-origin policy to answer as well. It is one
// frame on this transport for the reason it is one on the other: both halves
// are browser policy written before commitment, and the marking has to precede
// every refusal written below.
func WithCORS(config CORSConfig, csrfHeader string) SecurityHeadersOption {
	return func(o *securityHeadersOptions) {
		o.cors = config
		o.csrfHeader = csrfHeader
	}
}

// WithTrustedProxies accepts X-Forwarded-Proto from the listed networks when
// deciding whether a request arrived over HTTPS. Without it only a direct TLS
// connection counts as HTTPS.
func WithTrustedProxies(networks []*net.IPNet) SecurityHeadersOption {
	return func(o *securityHeadersOptions) { o.trustedProxies = networks }
}

// SecurityHeaders sets the policy headers on every response.
//
// The configuration is validated and reduced to a header set by the shared
// leaf, at construction, so a misconfiguration is an error before the port is
// bound and the two transports send the same headers rather than two
// computations that agree.
func SecurityHeaders(config SecurityHeadersConfig, option ...SecurityHeadersOption) (Middleware, error) {
	resolved, err := pwruntime.ResolveSecurityHeaders(config)
	if err != nil {
		return nil, err
	}
	options := securityHeadersOptions{}
	for _, apply := range option {
		apply(&options)
	}
	// Off means it sends nothing, which matters now that the cross-origin half
	// can install this frame on its own.
	if !config.Enabled {
		resolved = pwruntime.ResolvedSecurityHeaders{}
	}
	cors, err := pwruntime.ResolveCORS(options.cors, options.csrfHeader)
	if err != nil {
		return nil, err
	}
	proxies := requestorigin.FromNetworks(options.trustedProxies)
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(r *fasthttp.RequestCtx) {
			for _, entry := range resolved.Always {
				r.Response.Header.Set(entry.Name, entry.Value)
			}
			if resolved.HSTS != "" && requestIsHTTPS(r, proxies) {
				r.Response.Header.Set("Strict-Transport-Security", resolved.HSTS)
			}
			if cors.Enabled() {
				// This transport keeps the raw target only, so the decoded
				// path is what Path() returns and the raw form is what the URI
				// still holds. Both go in, because the two refusals need both:
				// an encoded separator is invisible once decoded, and a dot
				// segment is what the decoded form shows.
				path := string(r.URI().Path())
				decision := cors.Decide(path, string(r.URI().PathOriginal()), string(r.Method()),
					string(r.Request.Header.Peek("Origin")),
					string(r.Request.Header.Peek("Access-Control-Request-Method")),
					string(r.Request.Header.Peek("Access-Control-Request-Headers")))
				applyCORS(r, decision)
				cors.RecordCORSDecline(r, decision, path)
				if decision.Preflight {
					r.Response.SetStatusCode(fasthttp.StatusNoContent)
					return
				}
			}
			next(r)
		}
	}, nil
}

// applyCORS writes a decision onto a response. Vary is added rather than set,
// because the negotiation and the compression put their own values there.
func applyCORS(r *fasthttp.RequestCtx, decision pwruntime.CORSDecision) {
	for _, entry := range decision.Headers {
		r.Response.Header.Set(entry.Name, entry.Value)
	}
	for _, value := range decision.Vary {
		r.Response.Header.Add("Vary", value)
	}
}

// requestIsHTTPS reports whether the client's own hop was HTTPS.
//
// The rule is internal/requestorigin's, which is where every caller that asks
// this question answers it: a direct TLS connection is proof, and a forwarded
// header is evidence only from a declared peer, because anybody can send one.
// What this supplies is the three facts, read off this transport.
func requestIsHTTPS(r *fasthttp.RequestCtx, proxies requestorigin.Proxies) bool {
	// RemoteIP rather than RemoteAddr: this transport's RemoteAddr is whatever
	// net.Addr the listener produced, which for a non-TCP listener is a name
	// rather than an address, and an unparseable peer is never trusted. RemoteIP
	// is already the parsed address, and reports the unspecified address when
	// there is none — which no sane proxy network contains.
	return proxies.SchemeOf(r.IsTLS(), r.RemoteIP().String(),
		string(r.Request.Header.Peek("X-Forwarded-Proto"))) == "https"
}

// InjectResources publishes the process runtime resources — the loaded
// configuration, the logger, the database pool — on every request.
//
// The other half derives a context carrying them and hands it to the next
// handler; this one writes them into the request value. Everything downstream
// reads them the same way, because the request value answers Value from the
// store this writes to.
// The capsule is prepared once here rather than once per request, exactly as
// the other transport's frame prepares it: a capsule with no per-request state
// is shared rather than re-copied to the heap each time.
func InjectResources(resources pwruntime.Resources) Middleware {
	prepared := pwruntime.PrepareResources(resources)
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(r *fasthttp.RequestCtx) {
			prepared.StoreOn(r)
			next(r)
		}
	}
}

// AccessLog writes one completion record per request.
//
// The status and the size are read off the response rather than out of a
// tracking writer. That is not a shortcut: this transport buffers an ordinary
// response, so both are simply there once the handler returns, where the other
// half has to wrap the writer to observe them at all.
//
// A streamed response is the exception, and reading its size is not merely
// wrong there but fatal: Response.Body on a body stream drains the stream into
// memory to answer, so a live subscription — which by design does not end until
// its watchdog closes it — would be consumed here and never reach the client at
// all. The record says the size is unknown instead, which is the truth at the
// moment the handler returned.
func AccessLog() Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(r *fasthttp.RequestCtx) {
			start := time.Now()
			next(r)
			size := int64(-1)
			if !r.Response.IsBodyStream() {
				size = int64(len(r.Response.Body()))
			}
			pwruntime.ReadLogger(r).Log(r, pwruntime.LevelInfo, "request completed",
				pwruntime.String("method", string(r.Method())),
				pwruntime.String("path", string(r.Path())),
				pwruntime.Int("status", r.Response.StatusCode()),
				pwruntime.Int64("bytes", size),
				pwruntime.Duration("duration", time.Since(start)),
			)
		}
	}
}

// RequestTimeout bounds how long a request may take. A timeout of zero or less
// returns a pass-through middleware.
//
// This is the one frame whose meaning genuinely differs between the transports,
// and the difference is worth stating rather than smoothing over.
//
// The other half installs a deadline on the request context. Everything the
// handler does with that context — a query, an outbound call, an await boundary
// — observes it and stops, and the handler itself returns.
//
// Here the request value is the context, and a frame cannot give it a deadline
// it did not have. So the bound is the transport's own: the response is
// answered with 408 when the handler has not returned in time, and the handler
// goroutine keeps running until it finishes on its own. The request is bounded
// from the client's side, not from the handler's.
//
// A handler that needs its own work cancelled derives a context from the
// request and passes that down, which is portable and reads the same on both.
func RequestTimeout(timeout time.Duration) Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		if timeout <= 0 {
			return next
		}
		return fasthttp.TimeoutHandler(next, timeout,
			fasthttp.StatusMessage(fasthttp.StatusRequestTimeout))
	}
}

// PanicHandler writes the response for a panic recovered by Recover.
type PanicHandler func(r *fasthttp.RequestCtx, err error)

// Recover converts a panic into a response written by handler. A nil handler
// logs the panic and writes a plain 500.
//
// It recovers more completely than the other half can, and the difference is
// the transport rather than the code: this one buffers a response until the
// handler returns, so whatever a failed handler had written is still discardable
// and the 500 is the only thing the client sees. On net/http a response that has
// already committed can only be stopped, not taken back.
//
// What it does not reach is a panic inside a body stream writer, because that
// callback runs after the handler has returned and this frame with it.
func Recover(handler PanicHandler) Middleware {
	if handler == nil {
		handler = writePanicStatus
	}
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(r *fasthttp.RequestCtx) {
			defer func() {
				if recovered := recover(); recovered != nil {
					handler(r, fmt.Errorf("panic: %v", recovered))
				}
			}()
			next(r)
		}
	}
}

func writePanicStatus(r *fasthttp.RequestCtx, err error) {
	pwruntime.ReadLogger(r).Log(r, pwruntime.LevelError, "recovered panic",
		pwruntime.String("error", err.Error()))
	// The partial body of a failed handler is discarded rather than sent under a
	// 500, which would be a response describing one thing and carrying another.
	r.Response.ResetBody()
	r.Error(fasthttp.StatusMessage(fasthttp.StatusInternalServerError), fasthttp.StatusInternalServerError)
}

// MaxRequestBody refuses a request whose body is larger than limit.
//
// A non-positive limit disables the check, which is the same reading the other
// half gives it.
//
// The check is on the declared length rather than on a wrapped reader, because
// this transport has already read the body by the time a handler runs: the
// server's own MaxRequestBodySize is what stops an oversized one from being read
// at all, and this frame is the per-chain expression of the same limit.
func MaxRequestBody(limit int64) Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		if limit <= 0 {
			return next
		}
		return func(r *fasthttp.RequestCtx) {
			if int64(r.Request.Header.ContentLength()) > limit || int64(len(r.Request.Body())) > limit {
				WriteProblem(r, PayloadTooLarge("request body is too large"))
				return
			}
			next(r)
		}
	}
}
