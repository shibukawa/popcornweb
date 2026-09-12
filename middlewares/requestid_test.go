package middlewares

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
)

func TestRequestIDEchoesValidClientValue(t *testing.T) {
	var seen string
	handler := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Request-ID", "client-id")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("X-Request-ID") != "client-id" || seen != "client-id" {
		t.Fatalf("request ID = %q / %q", response.Header().Get("X-Request-ID"), seen)
	}
}

func TestRequestIDReplacesUnsafeValue(t *testing.T) {
	handler := RequestID()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header["X-Request-ID"] = []string{"unsafe\nrequest-id"}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	generated := response.Header().Get("X-Request-ID")
	if generated == "" || generated == "unsafe\nrequest-id" || !ValidRequestID(generated) {
		t.Fatalf("generated request ID = %q", generated)
	}
}

func TestRequestIDAppliesOptions(t *testing.T) {
	type key struct{}
	var bound any
	handler := RequestID(
		WithRequestIDHeader("X-Correlation-ID"),
		WithRequestIDGenerator(func() string { return "generated" }),
		WithRequestIDContext(func(ctx context.Context, id string) context.Context {
			return context.WithValue(ctx, key{}, id)
		}),
	)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		bound = r.Context().Value(key{})
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Header().Get("X-Correlation-ID") != "generated" || bound != "generated" {
		t.Fatalf("header = %q, context = %v", response.Header().Get("X-Correlation-ID"), bound)
	}
}

func TestRequestIDBindsRuntimeLoggerByDefault(t *testing.T) {
	sink := pwruntime.NewCaptureSink()
	backend := pwruntime.NewLogBackend(pwruntime.LevelInfo, sink)
	handler := InjectResources(pwruntime.Resources{Log: backend})(RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		pwruntime.ReadLogger(r.Context()).Info("handled")
	})))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	records := sink.Records()
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	// The correlation ID rides on the context as a stable attribute, so a
	// handler that logs nothing about it still produces a correlated record.
	if id := records[0].Text("request_id"); !ValidRequestID(id) {
		t.Fatalf("request_id = %q, want the generated correlation ID", id)
	}
}

func TestSequentialRequestIDsAreUnique(t *testing.T) {
	first, second := SequentialRequestID(), SequentialRequestID()
	if first == second {
		t.Fatalf("two sequential requests shared ID %q", first)
	}
	if !ValidRequestID(first) || !ValidRequestID(second) {
		t.Fatalf("generated IDs = %q, %q; both must be safe request IDs", first, second)
	}
}
