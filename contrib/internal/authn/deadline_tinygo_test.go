//go:build tinygo

package authn

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeadlineTransportReturnsOnADoneContext(t *testing.T) {
	// The round trip beneath cannot be cancelled on this runtime, which is the
	// reason the wrapper exists. What must not happen is the caller waiting on
	// it: that is a request handler held open by a peer that never answers.
	release := make(chan struct{})
	closed := make(chan struct{})
	base := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		<-release
		return &http.Response{Body: bodyCloser{Reader: strings.NewReader(""), closed: closed}}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://issuer.example/jwks", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := deadlineTransport{base: base}.RoundTrip(request)
		done <- err
	}()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RoundTrip after cancel = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RoundTrip did not return after its context was done")
	}

	// The abandoned round trip is drained, so its body and connection are
	// released rather than held until the process ends.
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the abandoned response body was never closed")
	}
}

func TestDeadlineTransportPassesAFinishedRoundTripThrough(t *testing.T) {
	base := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://issuer.example/jwks", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := deadlineTransport{base: base}.RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

type bodyCloser struct {
	io.Reader
	closed chan struct{}
}

func (b bodyCloser) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

// TestEnforceDeadlinesIsIdempotent keeps a client that passes through two
// packages, each promising a request timeout, from carrying two wrappers.
func TestEnforceDeadlinesIsIdempotent(t *testing.T) {
	once := EnforceDeadlines(&http.Client{})
	if _, wrapped := once.Transport.(deadlineTransport); !wrapped {
		t.Fatal("a plain client was not wrapped")
	}
	twice := EnforceDeadlines(once)
	if twice != once {
		t.Fatal("an already wrapped client was wrapped again")
	}
}
