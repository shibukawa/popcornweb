package pwfast

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pwratelimit"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

func enabledRateLimit() RateLimitConfig {
	config := pwratelimit.DefaultConfig()
	config.Enabled = true
	config.PerSubject = 3
	config.PerAddress = 2
	return config
}

// limited builds the chain a caller meets, with the client address already
// resolved the way the chain's own frame resolves it.
func limited(t *testing.T, config RateLimitConfig, deps RateLimitDeps, address, subject string) fasthttp.RequestHandler {
	t.Helper()
	if deps.Counter == nil {
		deps.Counter = pwratelimit.NewMemoryCounter()
	}
	limiter, err := RateLimiter(config, deps)
	if err != nil {
		t.Fatalf("RateLimiter: %v", err)
	}
	return Compose(
		func(r *fasthttp.RequestCtx) { r.SetStatusCode(fasthttp.StatusNoContent) },
		Frame{Slot: SlotClientAddress, Middleware: func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
			return func(r *fasthttp.RequestCtx) {
				pwruntime.StoreClientAddress(r, address)
				if subject != "" {
					pwruntime.StoreAuthentication(r, pwruntime.Authentication{
						Authenticated: true, Subject: subject})
				}
				next(r)
			}
		}},
		Frame{Slot: SlotRateLimit, Middleware: limiter},
	)
}

// statusesFor runs count arrivals through one chain and reports what each got.
func statusesFor(t *testing.T, handler fasthttp.RequestHandler, target string, count int) []int {
	t.Helper()
	out := make([]int, 0, count)
	for i := 0; i < count; i++ {
		status, _, _ := serve(t, handler, target)
		out = append(out, status)
	}
	return out
}

func TestRateLimitBoundsOneAddress(t *testing.T) {
	handler := limited(t, enabledRateLimit(), RateLimitDeps{}, "203.0.113.9", "")
	got := statusesFor(t, handler, "/orders", 3)
	for i, status := range got[:2] {
		if status != fasthttp.StatusNoContent {
			t.Errorf("arrival %d = %d, want 204", i+1, status)
		}
	}
	if got[2] != fasthttp.StatusTooManyRequests {
		t.Errorf("the third arrival = %d, want 429", got[2])
	}
}

// The two buckets have different populations, so an authenticated caller is
// counted by subject and gets the count that belongs to one.
func TestRateLimitCountsAnAuthenticatedCallerBySubject(t *testing.T) {
	handler := limited(t, enabledRateLimit(), RateLimitDeps{}, "203.0.113.9", "alice")
	got := statusesFor(t, handler, "/orders", 4)
	for i, status := range got[:3] {
		if status != fasthttp.StatusNoContent {
			t.Errorf("arrival %d = %d, want 204 under the per-subject count", i+1, status)
		}
	}
	if got[3] != fasthttp.StatusTooManyRequests {
		t.Errorf("the fourth arrival = %d, want 429", got[3])
	}
}

// A refusal has to say when to come back, or a client can only guess.
func TestRateLimitRefusalCarriesRetryMetadata(t *testing.T) {
	handler := limited(t, enabledRateLimit(), RateLimitDeps{}, "203.0.113.9", "")
	statusesFor(t, handler, "/orders", 2)
	status, header, body := serve(t, handler, "/orders")
	if status != fasthttp.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", status)
	}
	lower := strings.ToLower(header)
	for _, want := range []string{
		"cache-control: no-store", "retry-after:",
		"ratelimit-limit:", "ratelimit-remaining:", "ratelimit-reset:",
		"x-ratelimit-limit:", "x-ratelimit-remaining:", "x-ratelimit-reset:",
	} {
		if !strings.Contains(lower, want) {
			t.Errorf("the refusal carries no %q:\n%s", want, header)
		}
	}
	// The framework problem document, which is what the other transport writes.
	if !strings.Contains(lower, "application/problem+json") {
		t.Errorf("the refusal is not a problem document:\n%s", header)
	}
	if !strings.Contains(body, `"status":429`) {
		t.Errorf("the document does not carry the status:\n%s", body)
	}
}

// A probe arrives from the proxy on the same address as every anonymous caller
// and would exhaust that bucket by itself, so the endpoints the framework owns
// are never counted.
func TestRateLimitNeverCountsExemptPaths(t *testing.T) {
	handler := limited(t, enabledRateLimit(), RateLimitDeps{Exempt: []string{"/healthz"}}, "203.0.113.9", "")
	for i, status := range statusesFor(t, handler, "/healthz", 10) {
		if status != fasthttp.StatusNoContent {
			t.Fatalf("probe %d = %d; an exempt path was counted", i+1, status)
		}
	}
	// The bucket is untouched, so an ordinary path still has its whole count.
	if got := statusesFor(t, handler, "/orders", 2); got[0] != fasthttp.StatusNoContent || got[1] != fasthttp.StatusNoContent {
		t.Errorf("the exempt arrivals were counted after all: %v", got)
	}
}

// failingCounter is a store that cannot answer, which is the state the limiter
// has to survive rather than propagate.
type failingCounter struct{}

func (failingCounter) Increment(context.Context, string, time.Duration) (uint64, error) {
	return 0, errors.New("counter unreachable")
}

// A limiter that refuses when its store blinks converts a store incident into
// an outage of every limited route at once. It admits instead, and says so.
func TestRateLimitFailsOpenAndSaysSo(t *testing.T) {
	reported := 0
	handler := limited(t, enabledRateLimit(), RateLimitDeps{
		Counter:  failingCounter{},
		Degraded: func(*fasthttp.RequestCtx, error) { reported++ },
	}, "203.0.113.9", "")
	for i, status := range statusesFor(t, handler, "/orders", 5) {
		if status != fasthttp.StatusNoContent {
			t.Fatalf("arrival %d = %d; a store failure refused a request", i+1, status)
		}
	}
	if reported != 5 {
		t.Errorf("the degraded admissions were reported %d times, want 5", reported)
	}
}

// The ceiling is the only layer that sees a flood spread across many addresses,
// each of them staying under the per-address count by construction.
func TestProcessCeilingSeesADistributedFlood(t *testing.T) {
	config := enabledRateLimit()
	config.Process = 3
	ceiling, err := ProcessRateLimiter(config, RateLimitDeps{Counter: pwratelimit.NewMemoryCounter()})
	if err != nil {
		t.Fatal(err)
	}
	handler := Compose(func(r *fasthttp.RequestCtx) { r.SetStatusCode(fasthttp.StatusNoContent) },
		Frame{Slot: SlotRateLimitProcess, Middleware: ceiling})
	got := statusesFor(t, handler, "/orders", 4)
	for i, status := range got[:3] {
		if status != fasthttp.StatusNoContent {
			t.Errorf("arrival %d = %d, want 204 under the ceiling", i+1, status)
		}
	}
	if got[3] != fasthttp.StatusTooManyRequests {
		t.Errorf("the fourth arrival = %d, want 429", got[3])
	}
}

// An enabled limiter with no counter is refused rather than served without one.
// A limiter that admits everything is a control that looks installed.
func TestAnEnabledLimiterWithNoCounterIsRefused(t *testing.T) {
	if _, err := RateLimiter(enabledRateLimit(), RateLimitDeps{}); err == nil {
		t.Error("a limiter with no counter store was built")
	}
}

func TestRateLimitDisabledPassesEverything(t *testing.T) {
	config := pwratelimit.DefaultConfig()
	handler := limited(t, config, RateLimitDeps{}, "203.0.113.9", "")
	for i, status := range statusesFor(t, handler, "/orders", 20) {
		if status != fasthttp.StatusNoContent {
			t.Fatalf("arrival %d = %d with the limiter off", i+1, status)
		}
	}
}
