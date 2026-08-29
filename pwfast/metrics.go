package pwfast

import (
	"time"

	"github.com/shibukawa/popcornweb/contrib/otel"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// routeUserValueKey is where the matched route is stored on the request value.
//
// This transport has one request value rather than a writer and a request, so a
// frame changes what the rest of the chain sees by writing into it — the same
// mechanism the span uses. The key is unexported so nothing outside can write a
// route the metric would then believe.
type routeUserValueKey struct{}

// SetRoute is the counterpart of pw.SetRoute, collapsed to this transport's one
// argument, and it has nothing to do.
//
// The route is already recorded: this transport's ServeMux is the framework's
// own type, so it writes the matched pattern onto the request value at dispatch
// and every handler it reaches is keyed by its route without asking. The entry
// exists because a handler rewritten from the net/http source calls it, and a
// call that vanished would be a compile error in generated code.
//
// It is not a stub for a missing feature. It is the same feature arriving one
// layer earlier, which is why nothing here can improve on what the mux recorded.
func SetRoute(r *fasthttp.RequestCtx) {}

// routeOf reads the route recorded for this request.
func routeOf(r *fasthttp.RequestCtx) string {
	if r == nil {
		return ""
	}
	pattern, _ := r.UserValue(routeUserValueKey{}).(string)
	return pattern
}

// Metrics records the http.server instruments of one request.
//
// It is a frame of its own rather than a line inside the tracing frame, for the
// reason its net/http counterpart is: the two are installed independently, and a
// deployment sampling one trace in a thousand still counts every request.
func Metrics(metrics *pwruntime.Metrics) Middleware {
	if metrics == nil || metrics.RequestDuration == nil {
		return func(next fasthttp.RequestHandler) fasthttp.RequestHandler { return next }
	}
	// The concurrency attributes have tiny cardinality — the semconv verb set
	// against two schemes — and are identical for every request of one pair,
	// so they are built once here and looked up rather than allocated twice
	// per request, exactly as the other transport's frame builds them. The map
	// is never written again, so the lookup needs no lock.
	type methodScheme struct{ method, scheme string }
	activeFor := map[methodScheme][]otel.Attribute{}
	for _, method := range []string{
		"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS",
		"TRACE", "_OTHER",
	} {
		for _, scheme := range []string{"http", "https"} {
			activeFor[methodScheme{method, scheme}] = []otel.Attribute{
				otel.String("http.request.method", method),
				otel.String("url.scheme", scheme),
			}
		}
	}
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(r *fasthttp.RequestCtx) {
			started := time.Now()
			// Both labels are bounded before they reach an instrument: the method
			// is folded onto the semconv verb set and the scheme onto {http,https},
			// so a client cannot mint unbounded metric series with arbitrary method
			// tokens or a crafted scheme.
			scheme := pwruntime.NormalizeScheme(string(r.URI().Scheme()))
			method := pwruntime.NormalizeHTTPMethod(string(r.Method()))
			// The normalized pairs are the whole universe the seed above
			// enumerates, so the lookup always answers.
			active := activeFor[methodScheme{method, scheme}]
			metrics.ActiveRequests.Add(r, 1, active...)
			defer func() {
				metrics.ActiveRequests.Add(r, -1, active...)
				panicked := recover()
				status := r.Response.StatusCode()
				if panicked != nil {
					status = fasthttp.StatusInternalServerError
				}
				attributes := make([]otel.Attribute, 0, 4)
				attributes = append(attributes,
					otel.String("http.request.method", method),
					otel.String("url.scheme", scheme),
					otel.Int64("http.response.status_code", int64(status)),
				)
				if route := routeOf(r); route != "" {
					attributes = append(attributes, otel.String("http.route", route))
				}
				metrics.RequestDuration.Record(r, time.Since(started).Seconds(), attributes...)
				// Response.Body on a body stream drains the stream into memory
				// to answer, so a live subscription would be consumed here and
				// never reach the client. No record is the truth: the size is
				// unknown at the moment the handler returned.
				if !r.Response.IsBodyStream() {
					metrics.ResponseBodySize.Record(r, float64(len(r.Response.Body())), attributes...)
				}
				if length := r.Request.Header.ContentLength(); length > 0 {
					metrics.RequestBodySize.Record(r, float64(length), attributes...)
				}
				if panicked != nil {
					panic(panicked)
				}
			}()
			next(r)
		}
	}
}
