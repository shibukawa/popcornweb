//go:build js && wasm

// Package cloudflarekv counts rate limit arrivals in a Workers KV namespace,
// per requirement:cloudflare-kv-backends.
//
// A Worker runs the program per request, so the in-process counter counts
// nothing across requests; KV is the store the host offers that survives
// them. The count is an estimate: KV has no atomic increment, so two arrivals
// that read the same value write the same value, and a namespace propagates
// in tens of seconds. That is the precision a rate limit tolerates.
//
//	import _ "github.com/shibukawa/popcornweb/ratelimitstore/cloudflarekv"
//
// The generated Cloudflare Workers entry links this package, so a project
// selects it by configuration alone.
package cloudflarekv

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/pwratelimit"
	"github.com/syumai/workers/cloudflare/kv"
)

func init() {
	pwratelimit.RegisterStore(pwratelimit.BackendCloudflareKV, open)
}

// minimumTTL is the shortest expiration KV accepts, in seconds. A window
// shorter than that keeps its count for a minute, which over-limits slightly
// rather than never expiring.
const minimumTTL = 60

func open(ctx context.Context, config pwratelimit.Config) (pwratelimit.Counter, func(context.Context) error, error) {
	binding := strings.TrimSpace(config.CloudflareKV.Binding)
	if binding == "" {
		return nil, nil, errors.New(`ratelimit.backend = "cloudflarekv" requires ratelimit.cloudflarekv.binding`)
	}
	prefix := config.CloudflareKV.KeyPrefix
	if prefix == "" {
		prefix = pwratelimit.DefaultKeyPrefix
	}
	return &Counter{binding: binding, prefix: prefix}, nil, nil
}

// Counter counts in one namespace. The namespace is opened per call, because
// the Worker env it lives on is per request.
type Counter struct {
	binding string
	prefix  string
}

// KeyPrefix reports the key space the counter writes under.
func (c *Counter) KeyPrefix() string { return c.prefix }

// Increment adds one arrival under key and reports the estimate. The
// expiration is set on every write rather than on the first, because KV
// cannot tell the two apart without a second round trip; a window therefore
// measures from the last arrival rather than the first, which is the sliding
// behaviour a limiter can live with.
func (c *Counter) Increment(ctx context.Context, key string, window time.Duration) (uint64, error) {
	namespace, err := kv.NewNamespace(c.binding)
	if err != nil {
		return 0, err
	}
	full := c.prefix + key
	current, err := namespace.GetString(full, nil)
	if err != nil {
		return 0, err
	}
	count, _ := strconv.ParseUint(strings.TrimSpace(current), 10, 64)
	count++
	ttl := int(window / time.Second)
	if ttl < minimumTTL {
		ttl = minimumTTL
	}
	if err := namespace.PutString(full, strconv.FormatUint(count, 10), &kv.PutOptions{ExpirationTTL: ttl}); err != nil {
		return 0, err
	}
	return count, nil
}
