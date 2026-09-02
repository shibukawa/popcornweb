package pw

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	kzstd "github.com/klauspost/compress/zstd"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/configbind"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

func TestScaffoldsIncludeBuiltInDefinitions(t *testing.T) {
	toml, err := ScaffoldTOML()
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"[server]", "port = 8080", `read_header_timeout = "5s"`,
		`health = ""`, `readiness = ""`, `openapi = ""`,
		`public.enabled = true`, `public.mount = "/public"`,
		`headers.frame_options = "deny"`, "[observability]", "[middleware]",
		"access_log = true",
		`backend = "rdb"`, `cookie_store.name = "pw_session_data"`, `keyring.secret = ""`,
		`redis.key_prefix = "pw:session:"`, `redis.connect_timeout = "5s"`,
	} {
		if !strings.Contains(toml, fragment) {
			t.Fatalf("TOML scaffold missing %q:\n%s", fragment, toml)
		}
	}
	env, err := ScaffoldEnv()
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"PORT=8080", "SERVER_MAX_REQUEST_BODY=10485760",
		"SERVER_HEALTH=\"\"", "SECURITY_HEADERS_ENABLED=true",
		"OTEL_SERVICE_NAME=\"\"", "SESSION_ENABLED=false", "SESSION_COOKIE_SECURE=true",
		// The keyring is read from the environment rather than kept in the
		// configuration file, outside development.
		"SESSION_KEYRING_SECRET=\"\"", "SESSION_REDIS_DSN=\"\"",
	} {
		if !strings.Contains(env, fragment) {
			t.Fatalf("env scaffold missing %q:\n%s", fragment, env)
		}
	}
}

func TestMiddlewaresParseAndInjectConfiguration(t *testing.T) {
	SetConfigLoadOptions(configbind.LoadOptions{
		Vendor: "popcornweb-test", Tool: "pw-test", FileName: "missing.toml",
		Args: []string{"--port", "9090"}, Environ: []string{"PORT=7070"},
	})
	handler, err := Middlewares(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		config := ConfigContext[ServerConfig](r.Context())
		if config.Port != 9090 {
			t.Errorf("Port = %d", config.Port)
		}
		if !LoggerContext(r.Context()).Enabled(LevelError) {
			t.Error("nil logger")
		}
		w.WriteHeader(http.StatusNoContent)
	}), WithPublicFS(fstest.MapFS{".keep": {Data: nil}}))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Header().Get("X-Request-ID") == "" {
		t.Fatal("request ID was not added")
	}
}

func TestWriteHTMLBuffersAndWrites(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	builder := htmlbind.Builder[string]{}
	leaf := (&htmlbind.Plan[string]{Ops: []htmlbind.Op[string]{
		builder.Static("<h1>"),
		builder.Text(func(value string) string { return value }),
		builder.Static("</h1>"),
	}}).Bind("Hello")
	WriteHTML(recorder, request, leaf)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "<h1>Hello</h1>" {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestWriteHTMLChainNestsWrappers(t *testing.T) {
	type documentParams struct {
		Children htmlbind.Fragment
	}
	documentBuilder := htmlbind.Builder[documentParams]{}
	documentPlan := &htmlbind.Plan[documentParams]{Ops: []htmlbind.Op[documentParams]{
		documentBuilder.Static("<!doctype html><html><body>"),
		documentBuilder.Slot(func(params documentParams) htmlbind.Fragment { return params.Children }, nil),
		documentBuilder.Static("</body></html>"),
	}}
	document := documentPlan.BindWrapper(documentParams{}, func(params *documentParams, children htmlbind.Fragment) {
		params.Children = children
	})
	pageBuilder := htmlbind.Builder[struct{}]{}
	page := (&htmlbind.Plan[struct{}]{Ops: []htmlbind.Op[struct{}]{
		pageBuilder.Static("<main>page</main>"),
	}}).Bind(struct{}{})

	recorder := httptest.NewRecorder()
	WriteHTMLChain(recorder, httptest.NewRequest(http.MethodGet, "/", nil), []htmlbind.Wrapper{document}, page)
	if recorder.Code != http.StatusOK ||
		recorder.Body.String() != "<!doctype html><html><body><main>page</main></body></html>" {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestWriteHTMLUsesRegisteredDocument(t *testing.T) {
	type documentParams struct {
		Children htmlbind.Fragment
	}
	documentBuilder := htmlbind.Builder[documentParams]{}
	documentPlan := &htmlbind.Plan[documentParams]{Ops: []htmlbind.Op[documentParams]{
		documentBuilder.Static("<!doctype html><body>"),
		documentBuilder.Slot(func(params documentParams) htmlbind.Fragment { return params.Children }, nil),
		documentBuilder.Static("</body>"),
	}}
	document := documentPlan.BindWrapper(documentParams{}, func(params *documentParams, children htmlbind.Fragment) {
		params.Children = children
	})
	previous := pwruntime.SwapHTMLDocument([]HTMLWrapper{document})
	t.Cleanup(func() {
		pwruntime.SwapHTMLDocument(previous)
	})

	pageBuilder := htmlbind.Builder[struct{}]{}
	page := (&htmlbind.Plan[struct{}]{Ops: []htmlbind.Op[struct{}]{
		pageBuilder.Static("<main>page</main>"),
	}}).Bind(struct{}{})
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, httptest.NewRequest(http.MethodGet, "/", nil), page)
	if recorder.Body.String() != "<!doctype html><body><main>page</main></body>" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestWriteHTMLPreservesConfiguredZstdCompression(t *testing.T) {
	builder := htmlbind.Builder[struct{}]{}
	leaf := (&htmlbind.Plan[struct{}]{Ops: []htmlbind.Op[struct{}]{
		builder.Static("<main>compressed</main>"),
	}}).Bind(struct{}{})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Encoding", "zstd")
	request = request.WithContext(pwruntime.WithResources(request.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{
			reflect.TypeFor[MiddlewareConfig](): MiddlewareConfig{Compression: true},
		},
	}))
	recorder := httptest.NewRecorder()

	WriteHTML(recorder, request, leaf)

	if recorder.Header().Get("Content-Encoding") != "zstd" {
		t.Fatalf("Content-Encoding = %q", recorder.Header().Get("Content-Encoding"))
	}
	decoder, err := kzstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	body, err := decoder.DecodeAll(recorder.Body.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "<main>compressed</main>" {
		t.Fatalf("decoded body = %q", body)
	}
}

func TestUnsupportedStreamAcceptWrites406(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", "application/xml")
	// The callback must not run at all: negotiation fails before anything is
	// committed, which is the one stream failure that can still be a problem
	// response rather than a report.
	ran := false
	WriteStream(recorder, request, func(s *Stream[map[string]string]) error {
		ran = true
		return nil
	})
	if ran {
		t.Error("the callback ran after negotiation failed")
	}
	if recorder.Code != http.StatusNotAcceptable || !bytes.Contains(recorder.Body.Bytes(), []byte(`"code":"not_acceptable"`)) {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
}

// The framing a stream carries is the runtime's responsibility now: the
// callback returning is the close, so a JSON array document is terminated
// whether or not the handler thought about it. That is the defect the
// caller-held shape allowed and the reason this entry replaced it.
func TestWriteStreamClosesTheFramingWithoutTheCallerAskingTo(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", "application/json")
	WriteStream(recorder, request, func(s *Stream[map[string]string]) error {
		return s.Write(map[string]string{"value": "one"})
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.HasPrefix(body, "[") || !strings.HasSuffix(strings.TrimSpace(body), "]") {
		t.Errorf("the array framing was not terminated: %q", body)
	}
}
