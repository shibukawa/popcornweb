package pw

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pwruntime"
)

func validRuntimeConfigs() (ServerConfig, SecurityConfig, MiddlewareConfig, ObservabilityConfig) {
	return ServerConfig{
			Port:              8080,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       2 * time.Minute,
			ShutdownTimeout:   10 * time.Second,
			MaxRequestBody:    1024,
			Health:            "/healthz",
			Readiness:         "/readyz",
			OpenAPI:           "",
		},
		SecurityConfig{Headers: SecurityHeadersConfig{
			Enabled:            true,
			ContentTypeOptions: true,
			FrameOptions:       "deny",
			ReferrerPolicy:     "strict-origin-when-cross-origin",
		}},
		MiddlewareConfig{Recovery: true, RequestID: true, RequestTimeout: time.Second},
		ObservabilityConfig{MinimumLevel: "info"}
}

func TestValidateRuntimeConfig(t *testing.T) {
	server, security, middleware, observability := validRuntimeConfigs()
	if err := validateRuntimeConfig(server, security, middleware, observability); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*ServerConfig, *SecurityConfig, *MiddlewareConfig, *ObservabilityConfig)
		want   string
	}{
		{name: "port", mutate: func(s *ServerConfig, _ *SecurityConfig, _ *MiddlewareConfig, _ *ObservabilityConfig) {
			s.Port = 65536
		}, want: "server.port"},
		{name: "duplicate endpoint", mutate: func(s *ServerConfig, _ *SecurityConfig, _ *MiddlewareConfig, _ *ObservabilityConfig) {
			s.Readiness = s.Health
		}, want: "duplicates"},
		{name: "cbor body cap", mutate: func(s *ServerConfig, _ *SecurityConfig, _ *MiddlewareConfig, _ *ObservabilityConfig) {
			s.CBORMaxBody = -1
		}, want: "server.cbor_max_body"},
		{name: "proxy", mutate: func(s *ServerConfig, _ *SecurityConfig, _ *MiddlewareConfig, _ *ObservabilityConfig) {
			s.TrustedProxies = []string{"not-a-network"}
		}, want: "trusted_proxies"},
		{name: "header injection", mutate: func(_ *ServerConfig, s *SecurityConfig, _ *MiddlewareConfig, _ *ObservabilityConfig) {
			s.Headers.ContentSecurityPolicy = "default-src 'self'\r\nX-Evil: yes"
		}, want: "control characters"},
		{name: "timeout", mutate: func(_ *ServerConfig, _ *SecurityConfig, m *MiddlewareConfig, _ *ObservabilityConfig) {
			m.RequestTimeout = -time.Second
		}, want: "request_timeout"},
		{name: "public mount", mutate: func(s *ServerConfig, _ *SecurityConfig, _ *MiddlewareConfig, _ *ObservabilityConfig) {
			s.Public = PublicConfig{Enabled: true, Mount: "/"}
		}, want: "public.mount"},
		{name: "public endpoint overlap", mutate: func(s *ServerConfig, _ *SecurityConfig, _ *MiddlewareConfig, _ *ObservabilityConfig) {
			s.Public = PublicConfig{Enabled: true, Mount: "/healthz/assets"}
		}, want: "overlaps"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, security, middleware, observability := validRuntimeConfigs()
			test.mutate(&server, &security, &middleware, &observability)
			err := validateRuntimeConfig(server, security, middleware, observability)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

// PW0410 at its startup phase. The same value is deliberate on a loopback
// development machine and a defect in a deployment, so the environment decides;
// a cross-site cookie without Secure is refused everywhere, because no browser
// accepts it and the login would fail in dev too.
func TestValidateSessionConfigJudgesTheCookieByEnvironment(t *testing.T) {
	insecure := SessionConfig{Enabled: true, Cookie: SessionCookieConfig{SameSite: "lax"}}
	if err := validateSessionConfig(insecure, EnvDevelopment, true); err != nil {
		t.Fatalf("dev refused the loopback exception: %v", err)
	}
	err := validateSessionConfig(insecure, EnvStaging, false)
	if err == nil {
		t.Fatal("staging started with an insecure session cookie")
	}
	if !strings.Contains(err.Error(), "session.cookie.secure") {
		t.Fatalf("staging error = %v, want the key named", err)
	}
	crossSite := SessionConfig{Enabled: true, Cookie: SessionCookieConfig{SameSite: "none"}}
	if err := validateSessionConfig(crossSite, EnvDevelopment, true); err == nil {
		t.Fatal("a cross-site cookie without Secure was accepted in dev")
	}
	secure := SessionConfig{Enabled: true, Cookie: SessionCookieConfig{Secure: true, SameSite: "none"}}
	if err := validateSessionConfig(secure, EnvProduction, false); err != nil {
		t.Fatalf("a secure cookie was refused: %v", err)
	}
	// A project without sessions has no cookie policy to judge.
	if err := validateSessionConfig(SessionConfig{}, EnvProduction, false); err != nil {
		t.Fatalf("a disabled session was judged: %v", err)
	}
}

func TestValidateSessionConfigRestrictsDevelopmentIntentModes(t *testing.T) {
	for _, backend := range []string{SessionBackendDevVolatile, SessionBackendDevPersist} {
		config := SessionConfig{Enabled: true, Backend: backend, Cookie: SessionCookieConfig{Secure: true}}
		if err := validateSessionConfig(config, EnvDevelopment, true); err != nil {
			t.Fatalf("development refused %s: %v", backend, err)
		}
		if err := validateSessionConfig(config, EnvProduction, false); err == nil || !strings.Contains(err.Error(), "session.backend") {
			t.Fatalf("production %s error = %v", backend, err)
		}
	}
}

func TestHTTPServerUsesConfiguredTimeouts(t *testing.T) {
	server, _, _, _ := validRuntimeConfigs()
	instance := newHTTPServer(server, http.NotFoundHandler())
	if instance.Addr != ":8080" ||
		instance.ReadHeaderTimeout != server.ReadHeaderTimeout ||
		instance.ReadTimeout != server.ReadTimeout ||
		instance.WriteTimeout != server.WriteTimeout ||
		instance.IdleTimeout != server.IdleTimeout {
		t.Fatalf("server = %#v", instance)
	}
}

func TestOperationalEndpointCollisionDetection(t *testing.T) {
	server, _, _, _ := validRuntimeConfigs()
	mux := NewServeMux()
	mux.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	err := validateOperationalEndpointCollisions(mux, server)
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("error = %v", err)
	}

	fallback := NewServeMux()
	fallback.HandleFunc("/", func(http.ResponseWriter, *http.Request) {})
	if err := validateOperationalEndpointCollisions(fallback, server); err != nil {
		t.Fatalf("catch-all route should not mask an operational endpoint: %v", err)
	}
}

func TestPublicMountCollisionDetection(t *testing.T) {
	server, _, _, _ := validRuntimeConfigs()
	server.Public = PublicConfig{Enabled: true, Mount: "/public"}
	mux := NewServeMux()
	mux.HandleFunc("GET /public/", func(http.ResponseWriter, *http.Request) {})
	err := validateOperationalEndpointCollisions(mux, server)
	if err == nil || !strings.Contains(err.Error(), "public.mount collides") {
		t.Fatalf("error = %v", err)
	}
}

func TestRuntimeHandlerOperationalEndpointsAndMiddleware(t *testing.T) {
	server, security, middleware, _ := validRuntimeConfigs()
	server.TrustedProxies = []string{"10.0.0.0/8"}
	security.Headers.HSTS = HSTSConfig{
		Enabled: true, MaxAge: time.Hour, IncludeSubdomains: true,
	}
	handler, err := buildRuntimeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("request deadline was not installed")
		}
		_, _ = io.WriteString(w, "application")
	}), server, security, middleware, pwruntime.Resources{Log: pwruntime.NewLogBackend(pwruntime.LevelInfo, pwruntime.NewSlogSink(slog.NewTextHandler(io.Discard, nil)))}, false)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://example.test/healthz", nil)
	request.RemoteAddr = "10.1.2.3:1234"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header["X-Request-ID"] = []string{"unsafe\nrequest-id"}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "ok\n" {
		t.Fatalf("health response = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Request-ID") == "" ||
		response.Header().Get("X-Request-ID") == "unsafe\nrequest-id" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" ||
		response.Header().Get("X-Frame-Options") != "DENY" ||
		response.Header().Get("Strict-Transport-Security") != "max-age=3600; includeSubDomains" {
		t.Fatalf("headers = %#v", response.Header())
	}

	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "http://example.test/readyz", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("HEAD response = %d %q", head.Code, head.Body.String())
	}

	method := httptest.NewRecorder()
	handler.ServeHTTP(method, httptest.NewRequest(http.MethodPost, "http://example.test/healthz", nil))
	if method.Code != http.StatusMethodNotAllowed || method.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("method response = %d %#v", method.Code, method.Header())
	}
}

func TestRuntimeHandlerLimitsBody(t *testing.T) {
	server, security, middleware, _ := validRuntimeConfigs()
	server.MaxRequestBody = 4
	handler, err := buildRuntimeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), server, security, middleware, pwruntime.Resources{}, false)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("12345")))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestRecoveryDoesNotRewriteCommittedResponse(t *testing.T) {
	server, security, middleware, _ := validRuntimeConfigs()
	handler, err := buildRuntimeHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		panic("after commit")
	}), server, security, middleware, pwruntime.Resources{Log: pwruntime.NewLogBackend(pwruntime.LevelInfo, pwruntime.NewSlogSink(slog.NewTextHandler(io.Discard, nil)))}, false)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestRecoveryWritesProblemBeforeCommit(t *testing.T) {
	server, security, middleware, _ := validRuntimeConfigs()
	handler, err := buildRuntimeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("before commit")
	}), server, security, middleware, pwruntime.Resources{Log: pwruntime.NewLogBackend(pwruntime.LevelInfo, pwruntime.NewSlogSink(slog.NewTextHandler(io.Discard, nil)))}, false)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusInternalServerError ||
		!strings.Contains(response.Body.String(), `"code":"internal"`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestRequestContextCancellationIsPropagated(t *testing.T) {
	server, security, middleware, _ := validRuntimeConfigs()
	middleware.RequestTimeout = time.Millisecond
	handler, err := buildRuntimeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		if r.Context().Err() != context.DeadlineExceeded {
			t.Errorf("context error = %v", r.Context().Err())
		}
		w.WriteHeader(http.StatusNoContent)
	}), server, security, middleware, pwruntime.Resources{}, false)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestRuntimeCleanupsRunOnceInReverseOrder(t *testing.T) {
	var order []string
	cleanups := []*runtimeCleanup{
		{name: "first", fn: func(context.Context) error {
			order = append(order, "first")
			return nil
		}},
		{name: "second", fn: func(context.Context) error {
			order = append(order, "second")
			return errors.New("close failed")
		}},
	}
	err := runRuntimeCleanups(context.Background(), cleanups)
	if err == nil || !strings.Contains(err.Error(), "close second") {
		t.Fatalf("error = %v", err)
	}
	if strings.Join(order, ",") != "second,first" {
		t.Fatalf("order = %v", order)
	}
	if err := runRuntimeCleanups(context.Background(), cleanups); err == nil {
		t.Fatal("cleanup result was not stable")
	}
	if strings.Join(order, ",") != "second,first" {
		t.Fatalf("cleanup ran more than once: %v", order)
	}
}
