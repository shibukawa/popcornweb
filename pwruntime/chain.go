package pwruntime

import "sort"

// Slot orders every frame of the request chain. A smaller slot runs earlier,
// which is to say outermost, so a guard always observes the session and
// authentication state established before it.
//
// Framework frames sit at multiples of ten, BASIC style, so a middleware can
// be inserted between any two by picking a number in the gap:
// SlotAccessLog - 5 runs after the request ID is minted and before the access
// log times the request.
//
// The numbers live here rather than in either runtime because the order is the
// part that must not differ. A chain whose frames run in a different order on
// the second transport is a different application: a guard that ran after the
// session on one and before it on the other would authorize differently, and
// nothing about the response would say so.
type Slot int

const (
	// SlotMetrics records the http.server instruments of one request.
	//
	// It is outside the tracing frame rather than inside it, so that a duration
	// counted here covers everything a client waited for including the span the
	// next frame opens, and so that the two are installed independently: a
	// deployment sampling almost no traces still counts every request.
	SlotMetrics Slot = 5
	// SlotTracing opens the request root span. The frame is installed only
	// when tracing has somewhere to export.
	SlotTracing Slot = 10
	// SlotResources injects the logger, configuration, and database clients.
	SlotResources Slot = 20
	// SlotClientAddress records the caller every downstream bound counts.
	SlotClientAddress Slot = 25
	// SlotRequestID mints or accepts the correlation identifier.
	SlotRequestID Slot = 30
	// SlotAccessLog times the request.
	SlotAccessLog Slot = 40
	// SlotRecover converts a panic into a response.
	SlotRecover Slot = 50
	// SlotSecurityHeaders sets the browser policy headers, answers a CORS
	// preflight, and marks a cross-origin response.
	//
	// It sits above every frame that can refuse because of the marking rather
	// than because of the headers. A response a browser will not hand to script
	// is a status nobody can read, so the frame that marks it has to have run
	// before the 429, the 413, the 401, the 403 and the 500 that frames below
	// write — and w.Header() is one map for the whole chain, so setting the
	// headers here puts them on every one of those.
	//
	// It was at 60 while it only set headers. The move gained the refusals
	// written between the two numbers, which is the process rate limit's 429
	// and the 503 beside it.
	SlotSecurityHeaders Slot = 52
	// SlotRateLimitProcess bounds work this process is already doing.
	SlotRateLimitProcess Slot = 55
	// SlotRequestTimeout bounds how long a request may take.
	SlotRequestTimeout Slot = 70
	// SlotMaxRequestBody refuses an oversized body.
	SlotMaxRequestBody Slot = 80
	// SlotPublicAssets answers static files before anything authenticates.
	SlotPublicAssets Slot = 90
	// SlotSignedStorage serves the URLs a self-serving storage backend
	// presigns, per requirement:object-storage, ahead of anything that
	// authenticates: the signature is the authorization.
	SlotSignedStorage Slot = 95
	// SlotOperational answers the framework assets and the two probes.
	SlotOperational Slot = 100
	// SlotStorage opens request-scoped storage.
	SlotStorage Slot = 110
	// SlotSession resolves the session.
	SlotSession Slot = 120
	// SlotAuthentication verifies the caller.
	SlotAuthentication Slot = 130
	// SlotRateLimit bounds a caller's requests.
	SlotRateLimit Slot = 135
	// SlotCSRF checks the cross-site request forgery defence.
	SlotCSRF Slot = 140
	// SlotGuard authorizes.
	SlotGuard Slot = 150
	// SlotAPIDoc answers the OpenAPI document and its UI beneath the guard.
	SlotAPIDoc Slot = 160
)

// Frame is one positioned step of the chain.
//
// It is generic over the handler because that is the one thing the two
// runtimes genuinely disagree about: net/http wraps an interface and fasthttp
// wraps a function. Everything else about a frame — where it sits, what it is
// called — is the same on both.
type Frame[Handler any] struct {
	Slot       Slot
	Name       string
	Middleware func(Handler) Handler
}

// Compose wraps handler in frames, outermost first by slot.
//
// The sort is stable and equal slots keep their append order, so two frames
// registered at one number run in registration order rather than in whatever
// order the sort happened to produce. Both runtimes call this rather than
// sorting for themselves, because the ordering rule is the thing that has to be
// identical and a second implementation of it is a second chance to differ.
func Compose[Handler any](handler Handler, frames []Frame[Handler]) Handler {
	ordered := make([]Frame[Handler], len(frames))
	copy(ordered, frames)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Slot < ordered[j].Slot })
	for index := len(ordered) - 1; index >= 0; index-- {
		if ordered[index].Middleware != nil {
			handler = ordered[index].Middleware(handler)
		}
	}
	return handler
}
