package pw

import (
	"context"
	"fmt"

	"github.com/shibukawa/popcornweb/pwextension"
	"github.com/shibukawa/popcornweb/pwruntime"
)

// Middleware is the standard net/http middleware shape used by framework
// extensions.
type Middleware = pwextension.Middleware

// Slot orders every frame of the request chain, and the numbers are the
// shared leaf's so both runtimes compose in one order.
//
// Framework frames sit at multiples of ten, BASIC style, so a middleware can
// be inserted between any two by picking a number in the gap:
// pw.SlotAccessLog - 5 runs after the request ID is minted and before the
// access log times the request.
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
	SlotSignedStorage    = pwruntime.SlotSignedStorage
	SlotSession          = pwruntime.SlotSession
	SlotAuthentication   = pwruntime.SlotAuthentication
	SlotRateLimit        = pwruntime.SlotRateLimit
	SlotCSRF             = pwruntime.SlotCSRF
	SlotGuard            = pwruntime.SlotGuard
	SlotAPIDoc           = pwruntime.SlotAPIDoc
)

// Extension is one imported framework capability. Setup runs once during
// framework initialization, after configuration parsing and database startup,
// and returns the middleware to install. Returning a nil middleware installs
// nothing, which is how a disabled extension opts out.
//
// The registry lives in popcornweb/pwextension rather than here, so a plugin
// declaring a frame of this chain does not have to link this runtime to do it.
// A plugin that also serves the other transport would otherwise carry the whole
// net/http stack into a build that never calls any of it.
type Extension = pwextension.Extension

// RegisterExtension adds one extension to the framework chain. Imported
// packages call it from an init function so that only linked capabilities
// contribute configuration and code.
func RegisterExtension(extension Extension) { pwextension.Register(extension) }

// The two answers a plugin writes over net/http are this runtime's, and it says
// so here so that a plugin can write them without importing it.
func init() {
	pwextension.PublishResponders(pwextension.Responders{
		Problem:  WriteProblem,
		Redirect: Redirect,
	})
}

// RegisterMiddleware adds one application middleware to the request chain at
// slot. A smaller slot runs earlier, so pw.SlotAccessLog - 5 observes the
// request ID and appears in the access log's timing, and pw.SlotGuard + 1 runs
// only for requests the guard admitted.
//
// Call it from main, before Run or Middlewares builds the chain, exactly as
// RegisterSessionStore requires: the chain is composed once, and a middleware
// registered after that joins nothing.
//
// The two fixed frames, SlotOperational and SlotAPIDoc, are handlers rather
// than middleware and refuse registration at their exact number; register one
// position to either side instead. A duplicate name and a nil middleware are
// each a panic at registration rather than a silent gap in the chain.
func RegisterMiddleware(slot Slot, name string, middleware Middleware) {
	if middleware == nil {
		panic("popcornweb: middleware " + name + " is nil")
	}
	if slot == SlotOperational || slot == SlotAPIDoc {
		panic(fmt.Sprintf(
			"popcornweb: middleware %s registered at fixed frame %d; pick a neighboring slot relative to pw.SlotOperational or pw.SlotAPIDoc",
			name, slot))
	}
	captured := middleware
	RegisterExtension(Extension{
		Name: name,
		Slot: slot,
		Setup: func(context.Context) (Middleware, error) {
			return captured, nil
		},
	})
}

// chainFrame is one positioned frame of the request chain: a framework
// middleware, a registered application middleware, or one of the fixed
// handler frames adapted to the middleware shape.
type chainFrame struct {
	slot       Slot
	name       string
	middleware Middleware
}

// extensionFrames runs every registered Setup and returns the resulting
// frames. Setup runs in ascending slot order so an extension may depend on
// state prepared by an earlier slot; composing the chain is the caller's job.
func extensionFrames(ctx context.Context) ([]chainFrame, error) {
	ordered := pwextension.Registered()
	frames := make([]chainFrame, 0, len(ordered))
	for _, extension := range ordered {
		middleware, err := extension.Setup(ctx)
		if err != nil {
			return nil, fmt.Errorf("popcornweb: %s: %w", extension.Name, err)
		}
		if extension.Close != nil {
			registerCleanup(extension.Name, extension.Close)
		}
		// A nil middleware installs nothing, which is how a disabled
		// capability opts out.
		if middleware == nil {
			continue
		}
		frames = append(frames, chainFrame{slot: extension.Slot, name: extension.Name, middleware: middleware})
	}
	return frames, nil
}

// registerCleanup adds a shutdown hook that Run executes in reverse order.
// Repeated framework initialization, which tests perform, keeps one hook per
// extension name.
func registerCleanup(name string, fn func(context.Context) error) {
	runtimeState.Lock()
	defer runtimeState.Unlock()
	for _, existing := range runtimeState.cleanups {
		if existing.name == name {
			return
		}
	}
	runtimeState.cleanups = append(runtimeState.cleanups, &runtimeCleanup{name: name, fn: fn})
}
