package middlewares

import (
	"net/http"

	"github.com/shibukawa/popcornweb/pwruntime"
)

// InjectResources publishes the process runtime resources, such as loaded
// configuration, the logger, and the database pool, on every request context.
//
// The capsule is prepared once here rather than once per request: the frame
// hands every request the same value, and a capsule with no per-request state
// — which is most deployments' — is shared rather than re-copied to the heap
// each time.
func InjectResources(resources pwruntime.Resources) Middleware {
	prepared := pwruntime.PrepareResources(resources)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(prepared.Attach(r.Context())))
		})
	}
}
