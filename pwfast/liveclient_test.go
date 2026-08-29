package pwfast

import (
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// keyReporter answers with the admission key this request would be counted
// against, after a frame has resolved the caller the way the chain's own
// ResolveClientAddress does.
func keyReporter(resolved, subject string) fasthttp.RequestHandler {
	return Compose(func(r *fasthttp.RequestCtx) {
		_, _ = r.WriteString(liveClientKey(r))
	}, Frame{Slot: SlotClientAddress, Middleware: func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(r *fasthttp.RequestCtx) {
			pwruntime.StoreClientAddress(r, resolved)
			if subject != "" {
				pwruntime.StoreAuthentication(r, pwruntime.Authentication{
					Authenticated: true, Subject: subject})
			}
			next(r)
		}
	}})
}

// The live bound is per client, and behind a terminating proxy the peer is the
// proxy: counting against it would collapse every anonymous visitor into one
// bucket and refuse all of them from the fifth concurrent stream at the shipped
// default. The resolved caller is what the rate limiter of this same transport
// already counts against, and what the other transport's bound counts against.
func TestTheLiveBoundCountsTheResolvedCaller(t *testing.T) {
	_, _, body := serveRaw(t, keyReporter("203.0.113.9", ""), "/", "")
	if body != "remote:203.0.113.9" {
		t.Errorf("live admission key = %q, want the resolved caller", body)
	}
}

// An authenticated screen is counted against its subject, so one reader on two
// devices is two of one bound rather than two of an address's.
func TestTheLiveBoundCountsAnAuthenticatedSubject(t *testing.T) {
	_, _, body := serveRaw(t, keyReporter("203.0.113.9", "user-7"), "/", "")
	if body != "subject:user-7" {
		t.Errorf("live admission key = %q, want the subject", body)
	}
}

// A handler served outside the framework's own frames has nothing resolved, and
// the peer is the only caller there is to count against.
func TestTheLiveBoundFallsBackToThePeer(t *testing.T) {
	_, _, body := serveRaw(t, func(r *fasthttp.RequestCtx) {
		_, _ = r.WriteString(liveClientKey(r))
	}, "/", "")
	if !strings.HasPrefix(body, "remote:") || body == "remote:" {
		t.Errorf("live admission key with nothing resolved = %q, want the peer", body)
	}
}
