// Package pwratelimit is the transport-free half of the rate limiter: what a
// deployment configures, where the counts are kept, and which bucket a request
// falls in.
//
// The frames themselves are not here. Each transport installs its own, because
// reading a path and writing a 429 is what a transport does. What both drive is
// one Limiter, and that is the point rather than a saving: a limiter that
// bucketed differently on the second transport would enforce a different policy
// under the same configuration, and nothing about either response would say so.
package pwratelimit

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/shibukawa/popcornweb/pwruntime"
)

// The configuration is the shared leaf's, beside the other settings both
// transports build a frame from. It is aliased rather than restated so this
// package reads as one vocabulary and the two names stay one type.
type (
	// Config bounds how often one caller, and the process as a whole, may
	// arrive within a window.
	Config = pwruntime.RateLimitConfig
	// RedisConfig addresses the shared counter server.
	RedisConfig = pwruntime.RateLimitRedisConfig
)

const (
	// BackendMemory counts inside this process. It is correct on one replica
	// and enforces N times the configured limit on N of them.
	BackendMemory = pwruntime.RateLimitBackendMemory
	// BackendRedis counts in a shared server, which is what a deployment
	// running more than one replica needs.
	BackendRedis = pwruntime.RateLimitBackendRedis
	// BackendCloudflareKV counts in a Workers KV namespace, which only a
	// Cloudflare Worker has; the count is an estimate there.
	BackendCloudflareKV = pwruntime.RateLimitBackendCloudflareKV
	// DefaultKeyPrefix namespaces the keys this limiter owns.
	DefaultKeyPrefix = pwruntime.DefaultRateLimitKeyPrefix
)

// DefaultConfig returns the shipped defaults: off, and bounded once on.
func DefaultConfig() Config { return pwruntime.DefaultRateLimit() }

// Counter is the whole storage interface: add one to the count under key for
// the window ending at expiry, and report the new total.
//
// A backend sets the expiry when it creates the key and never extends it,
// which is what makes the window fixed rather than sliding.
type Counter interface {
	Increment(ctx context.Context, key string, window time.Duration) (uint64, error)
}

// StoreFactory opens the counter store a configuration selects. The returned
// close runs during shutdown and may be nil.
type StoreFactory func(ctx context.Context, config Config) (Counter, func(context.Context) error, error)

var stores = struct {
	sync.RWMutex
	factories map[string]StoreFactory
}{}

// RegisterStore registers factory under name. A storage plugin calls it from
// init, so a blank import is what puts a backend in a binary:
//
//	import _ "github.com/shibukawa/popcornweb/ratelimitstore/redis"
//
// The in-process counter is built in, because it adds no dependency and a
// limiter that needs one before it starts is one nobody switches on.
//
// A duplicate or empty name panics: two backends answering one configuration
// value is a build mistake, not a runtime condition.
func RegisterStore(name string, factory StoreFactory) {
	if name == "" || factory == nil {
		panic("pwratelimit: a counter store needs a name and a factory")
	}
	stores.Lock()
	defer stores.Unlock()
	if stores.factories == nil {
		stores.factories = make(map[string]StoreFactory)
	}
	if _, taken := stores.factories[name]; taken {
		panic(fmt.Sprintf("pwratelimit: counter store %q is already registered", name))
	}
	stores.factories[name] = factory
}

// Stores lists the registered backend names in order, which is what an error
// message reports when a configuration names one that is missing.
func Stores() []string {
	stores.RLock()
	defer stores.RUnlock()
	names := make([]string, 0, len(stores.factories))
	for name := range stores.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func init() {
	RegisterStore(BackendMemory, func(context.Context, Config) (Counter, func(context.Context) error, error) {
		return NewMemoryCounter(), nil, nil
	})
}

// OpenStore resolves the configured backend, naming the blank import a
// deployment is missing rather than only reporting an unknown value.
func OpenStore(ctx context.Context, config Config) (Counter, func(context.Context) error, error) {
	name := config.Backend
	if name == "" {
		name = BackendMemory
	}
	stores.RLock()
	factory, ok := stores.factories[name]
	stores.RUnlock()
	if !ok {
		return nil, nil, fmt.Errorf("ratelimit.backend %q is not registered; linked backends are %v", name, Stores())
	}
	return factory(ctx, config)
}

// WindowReset is the end of the fixed window the given instant falls in, which
// is what a counter's own expiry is set to.
func WindowReset(now time.Time, window time.Duration) time.Time {
	if window <= 0 {
		return now
	}
	return now.Truncate(window).Add(window)
}
