package pw

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
)

// wantCatalog is the document both transports must produce for these settings.
// It is written out rather than computed so that a transport re-serializing the
// Linkset on its own is caught here rather than agreeing with itself.
const wantCatalog = `{"linkset":[` +
	`{"anchor":"https://api.example.com/.well-known/api-catalog","item":[{"href":"https://api.example.com/"}]},` +
	`{"anchor":"https://api.example.com/",` +
	`"service-desc":[{"href":"https://api.example.com/openapi.json","type":"application/json"}],` +
	`"service-doc":[{"href":"https://api.example.com/docs","type":"text/html"}],` +
	`"status":[{"href":"https://api.example.com/healthz"}]}]}`

func catalogConfigs() (ServerConfig, SecurityConfig, MiddlewareConfig) {
	server, security, middleware := apiDocConfigs(APIDocScalar)
	server.Health = "/healthz"
	server.APICatalog, server.APICatalogOrigin = true, "https://api.example.com"
	return server, security, middleware
}

// defaultCatalogHandler builds the chain for the settings above, which is what
// every case that does not mutate them wants.
func defaultCatalogHandler(t *testing.T) http.Handler {
	t.Helper()
	server, security, middleware := catalogConfigs()
	return catalogHandler(t, server, security, middleware)
}

func catalogHandler(t *testing.T, server ServerConfig, security SecurityConfig, middleware MiddlewareConfig) http.Handler {
	t.Helper()
	handler, err := buildRuntimeHandler(http.NotFoundHandler(), server, security, middleware, pwruntime.Resources{}, false)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestAPICatalogEndpointAnswersTheWellKnownURI(t *testing.T) {
	handler := defaultCatalogHandler(t)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, pwruntime.APICatalogPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if body := response.Body.String(); body != wantCatalog {
		t.Errorf("document =\n%s\nwant\n%s", body, wantCatalog)
	}
	header := response.Header()
	// Section 4.2: the Linkset media type, and the profile that says this
	// particular set of links is an API catalog.
	if got := header.Get("Content-Type"); got != pwruntime.APICatalogContentType {
		t.Errorf("Content-Type = %q", got)
	}
	if !strings.Contains(header.Get("Content-Type"), `profile="https://www.rfc-editor.org/info/rfc9727"`) {
		t.Errorf("the profile parameter is missing from %q", header.Get("Content-Type"))
	}
	// Readable from anywhere, on the terms the OpenAPI document is: a catalog
	// readable only from origins already known defeats what it is for.
	if got := header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
}

// Section 2 asks a HEAD to answer with a Link header naming the relation.
func TestAPICatalogHeadCarriesTheRelationAndTheLength(t *testing.T) {
	handler := defaultCatalogHandler(t)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, pwruntime.APICatalogPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if body := response.Body.String(); body != "" {
		t.Errorf("a HEAD answered with a body: %q", body)
	}
	if got := response.Header().Get("Link"); got != `</.well-known/api-catalog>; rel="api-catalog"` {
		t.Errorf("Link = %q", got)
	}
	if got, want := response.Header().Get("Content-Length"), strconv.Itoa(len(wantCatalog)); got != want {
		t.Errorf("Content-Length = %q, want %q, which is what a HEAD is asking for", got, want)
	}
}

func TestAPICatalogRefusesOtherMethods(t *testing.T) {
	handler := defaultCatalogHandler(t)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, pwruntime.APICatalogPath, nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", response.Code)
	}
	if got := response.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q", got)
	}
}

func TestAPICatalogIsOffByDefault(t *testing.T) {
	server, security, middleware := catalogConfigs()
	server.APICatalog, server.APICatalogOrigin = false, ""
	handler := catalogHandler(t, server, security, middleware)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, pwruntime.APICatalogPath, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 while server.api_catalog is off", response.Code)
	}
}

// With no configured origin the links are absolute-path references, which
// resolve against whatever URI the client fetched. Nothing about the request is
// read to produce them, so a deployment that never names its origin still
// publishes a catalog every client resolves correctly.
func TestAPICatalogWithoutAnOriginIsRelative(t *testing.T) {
	server, security, middleware := catalogConfigs()
	server.APICatalogOrigin = ""
	handler := catalogHandler(t, server, security, middleware)

	request := httptest.NewRequest(http.MethodGet, pwruntime.APICatalogPath, nil)
	request.Host = "front.example.test"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	body := response.Body.String()
	if !strings.Contains(body, `"href":"/openapi.json"`) {
		t.Errorf("the links are not relative:\n%s", body)
	}
	if strings.Contains(body, "front.example.test") {
		t.Errorf("the request Host reached the document:\n%s", body)
	}
}

// The document depends on the settings and on nothing a caller sends, so a
// forged Host cannot make it name a host this deployment does not own -- with
// an origin configured or without one.
func TestAPICatalogIgnoresAForgedHost(t *testing.T) {
	for _, origin := range []string{"https://api.example.com", ""} {
		server, security, middleware := catalogConfigs()
		server.APICatalogOrigin = origin
		handler := catalogHandler(t, server, security, middleware)

		honest := httptest.NewRequest(http.MethodGet, pwruntime.APICatalogPath, nil)
		honest.Host = "api.example.com"
		honestResponse := httptest.NewRecorder()
		handler.ServeHTTP(honestResponse, honest)

		forged := httptest.NewRequest(http.MethodGet, pwruntime.APICatalogPath, nil)
		forged.Host = "attacker.example.net"
		forgedResponse := httptest.NewRecorder()
		handler.ServeHTTP(forgedResponse, forged)

		if honestResponse.Body.String() != forgedResponse.Body.String() {
			t.Errorf("origin %q: the Host changed the document:\n%s", origin, forgedResponse.Body.String())
		}
		if strings.Contains(forgedResponse.Body.String(), "attacker.example.net") {
			t.Errorf("origin %q: a forged Host reached the document", origin)
		}
	}
}

func TestValidateAPICatalogConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ServerConfig)
		want   string
	}{
		{name: "nothing to link", mutate: func(s *ServerConfig) {
			s.OpenAPI, s.APIDoc, s.APIDocPath = "", "", ""
		}, want: "neither server.openapi nor server.api_doc"},
		{name: "origin with a path", mutate: func(s *ServerConfig) {
			s.APICatalogOrigin = "https://api.example.com/v1"
		}, want: "carries a path"},
		{name: "origin without a scheme", mutate: func(s *ServerConfig) {
			s.APICatalogOrigin = "api.example.com"
		}, want: "must name http or https"},
		// The path is the standard's, so an application route on it is the
		// collision the operational endpoints already refuse.
		{name: "duplicate path", mutate: func(s *ServerConfig) {
			s.Health = pwruntime.APICatalogPath
		}, want: "server.api_catalog"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _, _ := catalogConfigs()
			test.mutate(&server)
			err := validateServerConfig(server)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}

	server, _, _ := catalogConfigs()
	if err := validateServerConfig(server); err != nil {
		t.Fatal(err)
	}

	mux := NewServeMux()
	mux.HandleFunc("GET "+pwruntime.APICatalogPath, func(http.ResponseWriter, *http.Request) {})
	err := validateOperationalEndpointCollisions(mux, server)
	if err == nil || !strings.Contains(err.Error(), "server.api_catalog collides") {
		t.Fatalf("error = %v", err)
	}
}
