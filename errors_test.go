package petitweb_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	petitweb "github.com/shibukawa/popcornweb"
	"github.com/shibukawa/popcornweb/pwruntime"
	httpbind "github.com/shibukawa/tinybind-go/httpbind"
)

func TestWriteErrorNegotiatesHTML(t *testing.T) {
	app := petitweb.New(petitweb.WithErrorRenderer(func(w http.ResponseWriter, _ *http.Request, page petitweb.ErrorPage) error {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(page.Status)
		_, err := w.Write([]byte("<h1>" + page.Title + "</h1>"))
		return err
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", "text/html,application/problem+json;q=0.5")
	app.WriteError(recorder, request, httpbind.NotFound(httpbind.Problem{Code: "missing", Message: "Not here"}))
	if recorder.Code != http.StatusNotFound || recorder.Body.String() != "<h1>Not Found</h1>" {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestWriteErrorCarriesRateLimitHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/limited", nil)
	petitweb.WriteError(recorder, request, pwruntime.RateLimited(pwruntime.RateLimit{
		Limit: 20, Remaining: 0, Reset: time.Unix(1_800_000_000, 0), RetryAfter: time.Minute,
	}, nil))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", recorder.Code)
	}
	for name, want := range map[string]string{
		"Cache-Control": "no-store", "Retry-After": "60", "X-RateLimit-Limit": "20",
		"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1800000000",
	} {
		if got := recorder.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestWriteErrorProblemDetailsAndSanitization(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", "application/problem+json")
	err := httpbind.Validation(httpbind.Field("name", "body", "is required"))
	petitweb.WriteError(recorder, request, err)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
	for _, fragment := range []string{`"code":"validation_failed"`, `"field":"name"`, `"location":"body"`} {
		if !strings.Contains(recorder.Body.String(), fragment) {
			t.Fatalf("problem %q does not contain %q", recorder.Body.String(), fragment)
		}
	}

	internal := httptest.NewRecorder()
	petitweb.WriteError(internal, request, httpbind.Internal(errors.New("secret DSN password")))
	if strings.Contains(internal.Body.String(), "secret") || !strings.Contains(internal.Body.String(), `"detail":"internal error"`) {
		t.Fatalf("unsafe internal body = %q", internal.Body.String())
	}
}

func TestFailedHTMLRendererFallsBackSafely(t *testing.T) {
	handler := petitweb.ErrorHandler{Renderer: func(http.ResponseWriter, *http.Request, petitweb.ErrorPage) error {
		return errors.New("template failed")
	}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", "text/html")
	handler.WriteError(recorder, request, httpbind.NotFound(httpbind.Problem{Message: "missing"}))
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "template failed") {
		t.Fatalf("fallback = %d %q", recorder.Code, recorder.Body.String())
	}
}
