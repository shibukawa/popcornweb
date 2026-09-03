//go:build !(js && wasm)

// Package cloudflarekv counts rate limit arrivals in a Workers KV namespace.
// This is the host build: the namespace exists only inside a Worker, so the
// backend registers under its name and refuses to open, naming the reason,
// rather than being an unknown value on the host.
package cloudflarekv

import (
	"context"
	"errors"

	"github.com/shibukawa/popcornweb/pwratelimit"
)

// ErrOutsideWorker is returned when the backend is selected on a host that is
// not a Cloudflare Worker.
var ErrOutsideWorker = errors.New(`ratelimit.backend = "cloudflarekv" counts only inside a Cloudflare Worker; use "memory" or "redis" here`)

func init() {
	pwratelimit.RegisterStore(pwratelimit.BackendCloudflareKV, func(context.Context, pwratelimit.Config) (pwratelimit.Counter, func(context.Context) error, error) {
		return nil, nil, ErrOutsideWorker
	})
}
