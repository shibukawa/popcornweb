package petitweb_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	petitweb "github.com/shibukawa/popcornweb"
)

func TestAppComposesStandardHandlersAndMiddleware(t *testing.T) {
	app := petitweb.New()
	var order []string
	app.Use(
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "first-before")
				next.ServeHTTP(w, r)
				order = append(order, "first-after")
			})
		},
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "second")
				next.ServeHTTP(w, r)
			})
		},
	)
	app.HandleFunc("GET /hello/{name}", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, r.PathValue("name"))
		w.WriteHeader(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/hello/petitweb", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	want := []string{"first-before", "second", "petitweb", "first-after"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("middleware order = %v, want %v", order, want)
	}
}

func TestOperationalEndpoints(t *testing.T) {
	ready := false
	config := petitweb.DefaultServerConfig()
	config.Health, config.Readiness, config.OpenAPI = "/healthz", "/readyz", "/openapi.json"
	app := petitweb.New(
		petitweb.WithServerConfig(config),
		petitweb.WithOpenAPI([]byte(`{"openapi":"3.1.0"}`)),
		petitweb.WithReadinessCheck(func(context.Context) error {
			if !ready {
				return errors.New("not ready")
			}
			return nil
		}),
	)
	handler := app.Handler()

	assertStatus(t, handler, http.MethodGet, "/healthz", http.StatusOK)
	assertStatus(t, handler, http.MethodHead, "/healthz", http.StatusOK)
	assertStatus(t, handler, http.MethodGet, "/readyz", http.StatusServiceUnavailable)
	ready = true
	assertStatus(t, handler, http.MethodGet, "/readyz", http.StatusOK)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"openapi":"3.1.0"}` {
		t.Fatalf("openapi response = %d %q", recorder.Code, recorder.Body.String())
	}
	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/openapi.json", nil))
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD body = %q", head.Body.String())
	}
}

func TestAppRejectsInvalidStartupConfiguration(t *testing.T) {
	config := petitweb.DefaultServerConfig()
	config.Health = "relative"
	app := petitweb.New(petitweb.WithServerConfig(config))
	if err := app.Validate(); err == nil || !strings.Contains(err.Error(), "health path") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestShutdownClosesResourcesInReverseOrder(t *testing.T) {
	var order []int
	app := petitweb.New(
		petitweb.WithCloser(func(context.Context) error { order = append(order, 1); return nil }),
		petitweb.WithCloser(func(context.Context) error { order = append(order, 2); return nil }),
	)
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []int{2, 1}) {
		t.Fatalf("closer order = %v", order)
	}
}

func assertStatus(t *testing.T, handler http.Handler, method, path string, want int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	if recorder.Code != want {
		t.Fatalf("%s %s status = %d, want %d", method, path, recorder.Code, want)
	}
}
