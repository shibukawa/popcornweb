//go:build tinygo

package authn

import "net/http"

// enforceDeadlines wraps the client's transport so a done context returns to
// the caller even though the underlying round trip cannot be cancelled.
//
// It is idempotent. A client can pass through two packages that both promise a
// request timeout — contrib/oidc builds one and hands it to contrib/oauth — and
// a second wrapper would cost a goroutine and a select per request to enforce a
// deadline the first one already enforces.
func enforceDeadlines(client *http.Client) *http.Client {
	if client != nil {
		if _, wrapped := client.Transport.(deadlineTransport); wrapped {
			return client
		}
	}
	copied := &http.Client{}
	if client != nil {
		*copied = *client
	}
	base := copied.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copied.Transport = deadlineTransport{base: base}
	return copied
}

// deadlineTransport returns when the request context is done, whether or not
// the round trip beneath it has finished.
//
// It cannot cancel that round trip: TinyGo's client holds no cancellation seam,
// which is the whole reason this exists. What it can do is stop the caller
// waiting on it, which is what a deadline on a request handler is for. The
// abandoned round trip is drained in the background so its response body and
// connection are released when the peer finally answers or the socket fails.
//
// The cost of a hung peer is therefore a goroutine and a connection until the
// runtime gives up on the socket, rather than a stalled request handler.
type deadlineTransport struct{ base http.RoundTripper }

type roundTripOutcome struct {
	response *http.Response
	err      error
}

func (t deadlineTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx := request.Context()
	if ctx == nil || ctx.Done() == nil {
		return t.base.RoundTrip(request)
	}
	// Buffered, so the goroutine below never blocks on a caller that has left.
	done := make(chan roundTripOutcome, 1)
	go func() {
		response, err := t.base.RoundTrip(request)
		done <- roundTripOutcome{response: response, err: err}
	}()
	select {
	case outcome := <-done:
		return outcome.response, outcome.err
	case <-ctx.Done():
		go func() {
			if outcome := <-done; outcome.response != nil {
				_ = outcome.response.Body.Close()
			}
		}()
		return nil, ctx.Err()
	}
}
