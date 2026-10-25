package pw

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	tinybind "github.com/shibukawa/tinybind-go/httpbind"
	"github.com/shibukawa/tinybind-go/jsonbind"
)

func TestProblemConstructorsCoverTinyBindStatuses(t *testing.T) {
	for _, tc := range []struct {
		problem Problem
		status  int
		code    string
	}{
		{BadRequest(), http.StatusBadRequest, "bad_request"},
		{Unauthorized(), http.StatusUnauthorized, "unauthorized"},
		{Forbidden(), http.StatusForbidden, "forbidden"},
		{NotFound(), http.StatusNotFound, "not_found"},
		{Conflict(), http.StatusConflict, "conflict"},
		{PayloadTooLarge(), http.StatusRequestEntityTooLarge, "payload_too_large"},
		{TooManyRequests(), http.StatusTooManyRequests, "rate_limit_exceeded"},
		{InternalServerError(), http.StatusInternalServerError, "internal"},
		{Validation(), http.StatusBadRequest, "validation_failed"},
	} {
		if tc.problem.Status != tc.status || tc.problem.Code != tc.code {
			t.Errorf("problem = %d %q, want %d %q", tc.problem.Status, tc.problem.Code, tc.status, tc.code)
		}
	}
}

func TestWriteProblemCarriesRateLimitHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/limited", nil)
	reset := time.Unix(1_800_000_000, 0)
	WriteProblem(recorder, request, RateLimited(RateLimit{
		Limit: 100, Remaining: 0, Reset: reset, RetryAfter: 1500 * time.Millisecond,
	}, "slow down"))

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", recorder.Code)
	}
	want := map[string]string{
		"Cache-Control":         "no-store",
		"Retry-After":           "2",
		"X-RateLimit-Limit":     "100",
		"X-RateLimit-Remaining": "0",
		"X-RateLimit-Reset":     "1800000000",
	}
	for name, value := range want {
		if got := recorder.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	if !strings.Contains(recorder.Body.String(), `"code":"rate_limit_exceeded"`) {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestWriteProblemOmitsInvalidRateLimitMetadata(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/limited", nil)
	WriteProblem(recorder, request, RateLimited(RateLimit{Limit: 1, Remaining: 2}, nil))

	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d headers=%v", recorder.Code, recorder.Header())
	}
	for _, name := range []string{"Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if got := recorder.Header().Get(name); got != "" {
			t.Errorf("%s = %q, want omitted", name, got)
		}
	}
}

func TestWriteProblemHTMLPreservesRateLimitHeaders(t *testing.T) {
	previous := registeredHTMLErrorPage()
	RegisterHTMLErrorPage(func(Problem) HTMLFragment { return staticFragment("<h1>Too Many Requests</h1>") })
	t.Cleanup(func() { RegisterHTMLErrorPage(previous) })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/limited", nil)
	request.Header.Set("Accept", "text/html")
	WriteProblem(recorder, request, RateLimited(RateLimit{Limit: 10, Remaining: 0}, nil))

	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("X-RateLimit-Limit") != "10" {
		t.Fatalf("response = %d headers=%v", recorder.Code, recorder.Header())
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestWriteProblemIncludesValidationFields(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	WriteProblem(recorder, request, Validation(
		Field("name", "body", "is required"),
		Field("page", "query", "must be positive"),
	))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
	var payload struct {
		Code   string              `json:"code"`
		Errors []map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "validation_failed" {
		t.Errorf("code = %q", payload.Code)
	}
	want := []map[string]string{
		{"field": "name", "location": "body", "message": "is required"},
		{"field": "page", "location": "query", "message": "must be positive"},
	}
	if !reflect.DeepEqual(payload.Errors, want) {
		t.Errorf("errors = %v, want %v", payload.Errors, want)
	}
}

func TestWriteProblemCarriesTinyBindFieldsAndHides5xxDetail(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	WriteProblem(recorder, request, tinybind.BindError("id", "path", "must be an integer"))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
	var bound struct {
		Errors []map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &bound); err != nil {
		t.Fatal(err)
	}
	if len(bound.Errors) != 1 || bound.Errors[0]["field"] != "id" {
		t.Fatalf("errors = %v", bound.Errors)
	}

	recorder = httptest.NewRecorder()
	problem := InternalServerError(errors.New("dsn=secret"))
	problem.Fields = []FieldError{Field("id", "path", "leaked")}
	WriteProblem(recorder, request, problem)
	var internal struct {
		Detail string              `json:"detail"`
		Errors []map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &internal); err != nil {
		t.Fatal(err)
	}
	if internal.Detail != "internal error" || internal.Errors != nil {
		t.Fatalf("5xx response leaked details: %q", recorder.Body.String())
	}
}

// statusPayload has a writer registered the way generation registers one: it
// answers 200 and lets WriteStatus rewrite the code, which is the contract the
// statusOverride wrapper holds.
type statusPayload struct {
	ID int `json:"id"`
}

// unencodablePayload deliberately has no registered encoder, standing in for a
// type that never reached pw generate.
type unencodablePayload struct{}

func TestWriteStatusAnswersTheStatusItWasGiven(t *testing.T) {
	// The encoder registry, not the writer registry: WriteStatus serializes
	// through jsonbind, which is what generation registers beside a
	// write-status call site.
	jsonbind.RegisterEncode[statusPayload](func(w io.Writer, v statusPayload) error {
		_, err := w.Write([]byte(`{"id":1}`))
		return err
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	WriteStatus(recorder, request, http.StatusCreated, statusPayload{ID: 1})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
	var payload struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ID != 1 {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestWriteStatusNoContentWritesNoBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/", nil)
	// unencodablePayload proves the encoder registry is never consulted: a 204
	// has no body by definition, so there is nothing to serialize.
	WriteStatus(recorder, request, http.StatusNoContent, unencodablePayload{})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", recorder.Body.String())
	}
}

// A type generation never saw has no encoder, and the library entry point has
// already committed the status by the time it finds that out. The response is
// the committed one with an empty body; the framework records the cause rather
// than writing a second header over it.
//
// This is a build mistake being reported, not a request failure: every type a
// pw.WriteStatus call site names gets its encoder from pw generate.
func TestWriteStatusWithNoEncoderKeepsTheCommittedResponse(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	WriteStatus(recorder, request, http.StatusCreated, unencodablePayload{})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want the committed %d", recorder.Code, http.StatusCreated)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", recorder.Body.String())
	}
}
