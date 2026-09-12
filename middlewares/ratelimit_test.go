package middlewares

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pwruntime"
)

func enabledRateLimit() RateLimitConfig {
	config := DefaultRateLimit()
	config.Enabled = true
	config.PerSubject = 3
	config.PerAddress = 2
	return config
}

func rateLimitChain(t *testing.T, config RateLimitConfig, deps RateLimitDeps) http.Handler {
	t.Helper()
	if deps.Counter == nil {
		deps.Counter = NewMemoryRateLimitCounter()
	}
	middleware, err := RateLimit(config, deps)
	if err != nil {
		t.Fatalf("RateLimit: %v", err)
	}
	return middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
}

// anonymous builds a request the client-address frame already resolved.
func anonymous(address string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/orders", nil)
	return r.WithContext(pwruntime.WithClientAddress(r.Context(), address))
}

func authenticated(subject string) *http.Request {
	r := anonymous("203.0.113.9")
	ctx := pwruntime.WithAuthentication(r.Context(),
		pwruntime.Authentication{Authenticated: true, Subject: subject})
	return r.WithContext(ctx)
}

func statuses(t *testing.T, handler http.Handler, build func() *http.Request, count int) []int {
	t.Helper()
	codes := make([]int, 0, count)
	for i := 0; i < count; i++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, build())
		codes = append(codes, response.Code)
	}
	return codes
}

func TestRateLimitBoundsOneAddress(t *testing.T) {
	handler := rateLimitChain(t, enabledRateLimit(), RateLimitDeps{})
	codes := statuses(t, handler, func() *http.Request { return anonymous("203.0.113.9") }, 3)
	want := []int{http.StatusNoContent, http.StatusNoContent, http.StatusTooManyRequests}
	for i, code := range codes {
		if code != want[i] {
			t.Fatalf("request %d: status = %d, want %d (all: %v)", i+1, code, want[i], codes)
		}
	}
	// Once one address is exhausted, another gets its own full allowance.
	second := statuses(t, handler, func() *http.Request { return anonymous("198.51.100.4") }, 3)
	for i, code := range second {
		if code != want[i] {
			t.Fatalf("second caller request %d: status = %d, want %d (all: %v)", i+1, code, want[i], second)
		}
	}
}

// An authenticated caller is counted by subject and against its own, larger
// allowance, even when several of them share one address.
func TestRateLimitCountsAnAuthenticatedCallerBySubject(t *testing.T) {
	handler := rateLimitChain(t, enabledRateLimit(), RateLimitDeps{})
	codes := statuses(t, handler, func() *http.Request { return authenticated("user-1") }, 4)
	want := []int{http.StatusNoContent, http.StatusNoContent, http.StatusNoContent, http.StatusTooManyRequests}
	for i, code := range codes {
		if code != want[i] {
			t.Fatalf("request %d: status = %d, want %d (all: %v)", i+1, code, want[i], codes)
		}
	}
	// A different subject on the same address is a different bucket.
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticated("user-2"))
	if response.Code != http.StatusNoContent {
		t.Fatalf("a second subject on one address got status %d", response.Code)
	}
}

func TestRateLimitZeroPerSubjectDisablesThatBucketOnly(t *testing.T) {
	config := enabledRateLimit()
	config.PerSubject = 0
	handler := rateLimitChain(t, config, RateLimitDeps{})
	for i, code := range statuses(t, handler, func() *http.Request { return authenticated("user-1") }, 5) {
		if code != http.StatusNoContent {
			t.Fatalf("authenticated request %d: status = %d, want the bucket disabled", i+1, code)
		}
	}
	// The address bucket, which has no off position, still binds.
	codes := statuses(t, handler, func() *http.Request { return anonymous("198.51.100.4") }, 3)
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("anonymous statuses = %v, want the address bucket still bound", codes)
	}
}

func TestRateLimitRefusalCarriesRetryMetadata(t *testing.T) {
	handler := rateLimitChain(t, enabledRateLimit(), RateLimitDeps{})
	statuses(t, handler, func() *http.Request { return anonymous("203.0.113.9") }, 2)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, anonymous("203.0.113.9"))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", response.Code)
	}
	header := response.Header()
	if got := header.Get("X-RateLimit-Limit"); got != "2" {
		t.Errorf("X-RateLimit-Limit = %q, want 2", got)
	}
	if got := header.Get("X-RateLimit-Remaining"); got != "0" {
		t.Errorf("X-RateLimit-Remaining = %q, want 0", got)
	}
	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	retry, err := strconv.Atoi(header.Get("Retry-After"))
	if err != nil || retry <= 0 {
		t.Errorf("Retry-After = %q, want a positive delay", header.Get("Retry-After"))
	}
	if header.Get("X-RateLimit-Reset") == "" {
		t.Error("X-RateLimit-Reset was not reported")
	}
}

// The carve-out that cannot be optional: a readiness probe arrives from the
// proxy on the same address as every anonymous caller, and one page view
// fetches many assets.
func TestRateLimitNeverCountsExemptPaths(t *testing.T) {
	handler := rateLimitChain(t, enabledRateLimit(), RateLimitDeps{Exempt: []string{"/healthz", "/public/**"}})
	probe := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		return r.WithContext(pwruntime.WithClientAddress(r.Context(), "10.0.0.7"))
	}
	for i, code := range statuses(t, handler, probe, 10) {
		if code != http.StatusNoContent {
			t.Fatalf("probe %d: status = %d, want an exempt path never counted", i+1, code)
		}
	}
	asset := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/public/app.css", nil)
		return r.WithContext(pwruntime.WithClientAddress(r.Context(), "10.0.0.7"))
	}
	for i, code := range statuses(t, handler, asset, 10) {
		if code != http.StatusNoContent {
			t.Fatalf("asset %d: status = %d, want an exempt path never counted", i+1, code)
		}
	}
	// And the exemption did not spend the caller's own budget.
	codes := statuses(t, handler, func() *http.Request { return anonymous("10.0.0.7") }, 2)
	for i, code := range codes {
		if code != http.StatusNoContent {
			t.Fatalf("request %d after exempt traffic: status = %d, want the budget untouched", i+1, code)
		}
	}
}

type brokenCounter struct{}

func (brokenCounter) Increment(context.Context, string, time.Duration) (uint64, error) {
	return 0, errors.New("counter store unreachable")
}

// Fail open: the edge still has its own limits, and refusing here would turn a
// store incident into an outage of every limited route at once.
func TestRateLimitFailsOpenAndSaysSo(t *testing.T) {
	degraded := 0
	handler := rateLimitChain(t, enabledRateLimit(), RateLimitDeps{
		Counter:  brokenCounter{},
		Degraded: func(*http.Request, error) { degraded++ },
	})
	for i, code := range statuses(t, handler, func() *http.Request { return anonymous("203.0.113.9") }, 5) {
		if code != http.StatusNoContent {
			t.Fatalf("request %d: status = %d, want the request admitted", i+1, code)
		}
	}
	if degraded != 5 {
		t.Fatalf("degraded admissions observed = %d, want 5; a limiter that is silently not limiting is the worst state", degraded)
	}
}

// The layer that sees a flood no per-address count can: every source stays
// under its own bucket, and the total still has to be refused.
func TestRateLimitProcessCeilingSeesADistributedFlood(t *testing.T) {
	config := enabledRateLimit()
	config.Process = 4
	middleware, err := RateLimitProcess(config, RateLimitDeps{Counter: NewMemoryRateLimitCounter()})
	if err != nil {
		t.Fatalf("RateLimitProcess: %v", err)
	}
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	source := 0
	next := func() *http.Request {
		source++
		return anonymous("203.0.113." + strconv.Itoa(source))
	}
	codes := statuses(t, handler, next, 5)
	for i := 0; i < 4; i++ {
		if codes[i] != http.StatusNoContent {
			t.Fatalf("arrival %d: status = %d, want it admitted (all: %v)", i+1, codes[i], codes)
		}
	}
	if codes[4] != http.StatusTooManyRequests {
		t.Fatalf("the fifth arrival from a fifth address = %d, want the ceiling to refuse it", codes[4])
	}
}

func TestRateLimitProcessIsOffAtZero(t *testing.T) {
	config := enabledRateLimit()
	config.Process = 0
	middleware, err := RateLimitProcess(config, RateLimitDeps{Counter: NewMemoryRateLimitCounter()})
	if err != nil {
		t.Fatalf("RateLimitProcess: %v", err)
	}
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for i, code := range statuses(t, handler, func() *http.Request { return anonymous("203.0.113.9") }, 20) {
		if code != http.StatusNoContent {
			t.Fatalf("arrival %d: status = %d, want no ceiling", i+1, code)
		}
	}
}

func TestRateLimitDisabledPassesEverything(t *testing.T) {
	handler := rateLimitChain(t, DefaultRateLimit(), RateLimitDeps{})
	for i, code := range statuses(t, handler, func() *http.Request { return anonymous("203.0.113.9") }, 20) {
		if code != http.StatusNoContent {
			t.Fatalf("request %d: status = %d, want 204", i+1, code)
		}
	}
}
