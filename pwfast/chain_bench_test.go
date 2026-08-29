package pwfast

import (
	"net"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// benchChain measures the composed frame stack alone: the handler is a bare
// 204, the request never crosses a connection, and the context is built fresh
// per iteration — which over-counts the transport's own header storage, since
// a served request reuses a pooled context, and therefore makes these numbers
// a ceiling on the chain's cost rather than the cost itself. What the rows are
// for is the difference between them, and between two runs of one row.
func benchChain(b *testing.B, settings pwruntime.ChainSettings, options RuntimeOptions) {
	b.Helper()
	previous, had := pwruntime.ResolvedChainSettings()
	pwruntime.PublishChainSettings(settings)
	b.Cleanup(func() {
		if had {
			pwruntime.PublishChainSettings(previous)
		}
	})
	handler, err := Middlewares(func(r *fasthttp.RequestCtx) {
		r.SetStatusCode(fasthttp.StatusNoContent)
	}, options)
	if err != nil {
		b.Fatal(err)
	}
	remote := &net.TCPAddr{IP: net.IPv4(203, 0, 113, 9), Port: 4711}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req fasthttp.Request
		req.Header.SetMethod("GET")
		req.SetRequestURI("http://example.test/orders/42")
		var ctx fasthttp.RequestCtx
		ctx.Init(&req, remote, nil)
		handler(&ctx)
	}
}

// The floor: only the two unconditional frames, the resources capsule and the
// client address.
func BenchmarkChainMinimal(b *testing.B) {
	benchChain(b, pwruntime.ChainSettings{}, RuntimeOptions{})
}

// The shipped shape of a small deployment: correlation, recovery, and the
// security header set.
func BenchmarkChainTypical(b *testing.B) {
	benchChain(b, pwruntime.ChainSettings{
		RequestID:       true,
		Recovery:        true,
		SecurityHeaders: DefaultSecurityHeaders(),
	}, RuntimeOptions{})
}

// The same deployment with a cross-origin policy, exercised by a same-origin
// request — which is what almost every request under a CORS policy is.
func BenchmarkChainWithCORS(b *testing.B) {
	cors := DefaultCORS()
	cors.Enabled = true
	cors.AllowedOrigins = []string{"https://app.example"}
	benchChain(b, pwruntime.ChainSettings{
		RequestID:       true,
		Recovery:        true,
		SecurityHeaders: DefaultSecurityHeaders(),
		CORS:            cors,
	}, RuntimeOptions{})
}
