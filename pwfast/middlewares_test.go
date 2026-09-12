package pwfast

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

func TestSecurityHeadersSendsTheResolvedSet(t *testing.T) {
	middleware, err := SecurityHeaders(DefaultSecurityHeaders())
	if err != nil {
		t.Fatal(err)
	}
	_, header, _ := serve(t, Chain(func(*fasthttp.RequestCtx) {}, middleware), "/")

	for _, want := range []string{
		"X-Content-Type-Options: nosniff",
		"X-Frame-Options: DENY",
		"Referrer-Policy: strict-origin-when-cross-origin",
		"Content-Security-Policy: script-src 'self'",
	} {
		if !strings.Contains(header, want) {
			t.Errorf("missing %q in:\n%s", want, header)
		}
	}
}

func TestAnInvalidSecurityConfigurationIsAnErrorBeforeServing(t *testing.T) {
	if _, err := SecurityHeaders(SecurityHeadersConfig{FrameOptions: "sometimes"}); err == nil {
		t.Fatal("an unsupported frame_options was accepted")
	}
}

// HSTS must not be turned on by a header anybody can send. Without a trusted
// proxy list, a forwarded claim of HTTPS counts for nothing.
func TestHSTSIgnoresAForwardedClaimFromAnUntrustedPeer(t *testing.T) {
	config := DefaultSecurityHeaders()
	config.HSTS = HSTSConfig{Enabled: true, MaxAge: 365 * 24 * time.Hour}
	middleware, err := SecurityHeaders(config)
	if err != nil {
		t.Fatal(err)
	}
	handler := Chain(func(*fasthttp.RequestCtx) {}, middleware)

	_, header, _ := serveRaw(t, handler, "/", "X-Forwarded-Proto: https\r\n")
	if strings.Contains(header, "Strict-Transport-Security") {
		t.Errorf("HSTS was sent on an untrusted forwarded claim:\n%s", header)
	}

	// The in-memory connection reports 0.0.0.0, so trusting everything is what
	// makes the positive case reachable here.
	_, everything, _ := net.ParseCIDR("0.0.0.0/0")
	trusting, err := SecurityHeaders(config, WithTrustedProxies([]*net.IPNet{everything}))
	if err != nil {
		t.Fatal(err)
	}
	_, header, _ = serveRaw(t, Chain(func(*fasthttp.RequestCtx) {}, trusting), "/", "X-Forwarded-Proto: https\r\n")
	if !strings.Contains(header, "Strict-Transport-Security: max-age=31536000") {
		t.Errorf("HSTS was not sent for a trusted forwarded claim:\n%s", header)
	}
}

func TestRecoverAnswers500AndDiscardsThePartialBody(t *testing.T) {
	handler := Chain(func(r *fasthttp.RequestCtx) {
		_, _ = r.WriteString("half a page")
		panic("handler exploded")
	}, Recover(nil))

	status, _, body := serve(t, handler, "/")
	if status != fasthttp.StatusInternalServerError {
		t.Errorf("status = %d, want 500", status)
	}
	// This transport buffers, so the partial body is still discardable. Sending
	// it under a 500 would be a response describing one thing and carrying
	// another.
	if strings.Contains(body, "half a page") {
		t.Errorf("the partial body survived the panic: %q", body)
	}
}

func TestMaxRequestBodyRefusesAnOversizedBody(t *testing.T) {
	handler := Chain(func(r *fasthttp.RequestCtx) { _, _ = r.WriteString("accepted") }, MaxRequestBody(8))

	if _, _, body := serveForm(t, handler, "/", "a=1"); body != "accepted" {
		t.Errorf("a small body was refused: %q", body)
	}
	status, _, _ := serveForm(t, handler, "/", "a=123456789012345")
	if status != fasthttp.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", status)
	}
}

func TestMaxRequestBodyWithNoLimitAddsNothing(t *testing.T) {
	handler := Chain(func(r *fasthttp.RequestCtx) { _, _ = r.WriteString("accepted") }, MaxRequestBody(0))
	if _, _, body := serveForm(t, handler, "/", "a=123456789012345"); body != "accepted" {
		t.Errorf("a disabled limit still refused: %q", body)
	}
}

// InjectResources is what makes the request-scoped accessors answer at all, so
// the assertion is that a value put on the chain reaches a reader downstream.
func TestInjectResourcesReachesTheDownstreamReaders(t *testing.T) {
	captured := &captureSink{}
	resources := pwruntime.Resources{Log: pwruntime.NewLogBackend(pwruntime.LevelInfo, captured)}

	handler := Chain(func(r *fasthttp.RequestCtx) {
		pwruntime.ReadLogger(r).Log(r, pwruntime.LevelInfo, "from the handler")
	}, InjectResources(resources))
	serve(t, handler, "/")

	if !captured.sawMessage("from the handler") {
		t.Error("the injected logger did not reach the handler")
	}
}

func TestAccessLogRecordsTheCompletedRequest(t *testing.T) {
	captured := &captureSink{}
	resources := pwruntime.Resources{Log: pwruntime.NewLogBackend(pwruntime.LevelInfo, captured)}

	handler := Chain(func(r *fasthttp.RequestCtx) {
		r.SetStatusCode(fasthttp.StatusTeapot)
		_, _ = r.WriteString("body")
	}, InjectResources(resources), AccessLog())
	serve(t, handler, "/orders")

	if !captured.sawMessage("request completed") {
		t.Fatal("no completion record was written")
	}
}

func TestRequestTimeoutPassesThroughWhenDisabled(t *testing.T) {
	handler := Chain(func(r *fasthttp.RequestCtx) { _, _ = r.WriteString("served") }, RequestTimeout(0))
	if _, _, body := serve(t, handler, "/"); body != "served" {
		t.Errorf("a disabled timeout changed the response: %q", body)
	}
}

func TestRequestTimeoutAnswers408WhenTheHandlerOverruns(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	handler := Chain(func(r *fasthttp.RequestCtx) {
		<-release
		_, _ = r.WriteString("too late")
	}, RequestTimeout(50*time.Millisecond))

	status, _, body := serve(t, handler, "/")
	if status != fasthttp.StatusRequestTimeout {
		t.Errorf("status = %d, want 408", status)
	}
	if strings.Contains(body, "too late") {
		t.Errorf("the overrunning handler's body was sent: %q", body)
	}
}

// captureSink collects records so a test can assert one was written without
// reaching into the logger.
type captureSink struct {
	mu       sync.Mutex
	messages []string
}

func (s *captureSink) Emit(_ context.Context, record pwruntime.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, record.Message)
}

func (s *captureSink) sawMessage(want string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, message := range s.messages {
		if message == want {
			return true
		}
	}
	return false
}
