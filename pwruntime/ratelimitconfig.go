package pwruntime

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The rate limiter's configuration lives here for the same reason CSRFConfig
// does: it is what a deployment writes, both transports build a frame from it,
// and neither of them owns it. The counters, the store registry and the
// bucketing are popcornweb/pwratelimit's, which this package must not name.

// Rate limit backend names a deployment selects with ratelimit.backend.
const (
	// RateLimitBackendMemory counts inside this process. It is correct on one replica
	// and enforces N times the configured limit on N of them.
	RateLimitBackendMemory = "memory"
	// RateLimitBackendRedis counts in a shared server, which is what a deployment
	// running more than one replica needs.
	RateLimitBackendRedis = "redis"
	// RateLimitBackendCloudflareKV counts in a Workers KV namespace, as an
	// estimate, per requirement:cloudflare-kv-backends.
	RateLimitBackendCloudflareKV = "cloudflarekv"
)

// DefaultRateLimitKeyPrefix namespaces the keys this limiter owns.
const DefaultRateLimitKeyPrefix = "pw:ratelimit:"

// Config bounds how often one caller, and the process as a whole, may arrive
// within a window.
//
// It deliberately declares no per-route rules. A per-operation quota belongs to
// an API gateway, and a pattern grammar with a precedence order would be most
// of the cost of this middleware for a capability the boundary in front of a
// normal deployment already sells.
type RateLimitConfig struct {
	Enabled bool   `default:"false"`
	Backend string `default:"memory" enum:"memory,redis,cloudflarekv" dependon:".enabled" help:"counter storage: memory, redis, or cloudflarekv"`
	// Window is the period every count below is measured over. It is also the
	// burst granularity, because the algorithm is a fixed window, and it is
	// what X-RateLimit-Reset reports.
	Window time.Duration `default:"1m" dependon:".enabled" help:"period every count is measured over"`
	// PerSubject bounds one authenticated caller. Zero disables this bucket,
	// because such a caller is accountable and revocable by other means.
	PerSubject int `default:"600" dependon:".enabled" help:"requests one authenticated subject may make in a window; zero disables"`
	// PerAddress bounds one caller with no session, and has no off position.
	// It is the only bucket an unauthenticated flood meets, so an unlimited
	// value here is an absent control rather than a permissive one.
	PerAddress int `default:"300" dependon:".enabled" help:"requests one caller with no session may make in a window"`
	// Process is the total arrival ceiling, unkeyed. It is the only layer that
	// sees a distributed flood, since such a flood keeps every source under
	// PerAddress by construction.
	//
	// It defaults to zero rather than to a guess: the right value follows from
	// what a deployment can serve, and one set below real capacity refuses
	// legitimate traffic globally.
	Process int `default:"0" dependon:".enabled" help:"total arrivals allowed in a window, unkeyed; zero leaves only the identity buckets"`
	// Redis names the backend it belongs to, so a memory-counted deployment
	// reports no counter server. Backend already answers to Enabled, so the
	// switch is not repeated here.
	Redis RateLimitRedisConfig `dependon:".backend=redis"`
	// CloudflareKV names the KV namespace binding a Worker counts in, per
	// requirement:cloudflare-kv-backends. The count is an estimate there: KV
	// has no atomic increment and propagates in tens of seconds, which is the
	// precision a rate limit tolerates and a session does not.
	CloudflareKV RateLimitCloudflareKVConfig `key:"cloudflarekv" dependon:".backend=cloudflarekv"`
}

// RateLimitCloudflareKVConfig addresses the KV namespace a Worker counts in.
type RateLimitCloudflareKVConfig struct {
	Binding   string `help:"KV namespace binding the Worker env carries"`
	KeyPrefix string `default:"pw:ratelimit:" help:"key space this limiter owns"`
}

// RedisConfig addresses the shared counter server.
type RateLimitRedisConfig struct {
	DSN            string        `secret:"mask" env:"RATELIMIT_REDIS_DSN" help:"redis:// or rediss:// counter server"`
	KeyPrefix      string        `default:"pw:ratelimit:" help:"key space this limiter owns"`
	ConnectTimeout time.Duration `default:"5s" help:"bounds the startup ping and per-command deadlines"`
}

// DefaultConfig returns the shipped defaults: off, and bounded once on.
func DefaultRateLimit() RateLimitConfig {
	return RateLimitConfig{
		Backend:    RateLimitBackendMemory,
		Window:     time.Minute,
		PerSubject: 600,
		PerAddress: 300,
		Redis: RateLimitRedisConfig{
			KeyPrefix:      DefaultRateLimitKeyPrefix,
			ConnectTimeout: 5 * time.Second,
		},
	}
}

// Validate rejects a configuration whose limits cannot bind.
func (c RateLimitConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	switch c.Backend {
	case "", RateLimitBackendMemory, RateLimitBackendRedis, RateLimitBackendCloudflareKV:
	default:
		return fmt.Errorf("ratelimit.backend %q is not memory, redis, or cloudflarekv", c.Backend)
	}
	if c.Window <= 0 {
		return errors.New("ratelimit.window must be positive")
	}
	if c.PerSubject < 0 {
		return errors.New("ratelimit.per_subject must not be negative")
	}
	if c.Process < 0 {
		return errors.New("ratelimit.process must not be negative")
	}
	// The one count with no off position: it is what an unauthenticated flood
	// meets, and nothing else does.
	if c.PerAddress <= 0 {
		return errors.New("ratelimit.per_address must be positive; it is the only bucket an unauthenticated caller meets")
	}
	if c.Process > 0 {
		// A caller allowed more than the total describes a limit that can
		// never bind, which is a configuration mistake rather than a policy.
		if c.PerAddress > c.Process {
			return fmt.Errorf("ratelimit.per_address %d exceeds ratelimit.process %d", c.PerAddress, c.Process)
		}
		if c.PerSubject > c.Process {
			return fmt.Errorf("ratelimit.per_subject %d exceeds ratelimit.process %d", c.PerSubject, c.Process)
		}
	}
	if c.Backend != RateLimitBackendRedis && strings.TrimSpace(c.Redis.DSN) != "" {
		return errors.New(`ratelimit.redis.dsn is set while ratelimit.backend is not "redis"`)
	}
	if c.Backend == RateLimitBackendRedis && strings.TrimSpace(c.Redis.DSN) == "" {
		return errors.New(`ratelimit.backend = "redis" requires ratelimit.redis.dsn`)
	}
	return nil
}
