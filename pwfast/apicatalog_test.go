package pwfast

import (
	"strconv"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// wantCatalog is the same literal the net/http transport's test asserts. Both
// are written out rather than computed from the builder, so a transport that
// serialized a Linkset of its own would disagree with the other here rather
// than agree with itself.
const wantCatalog = `{"linkset":[` +
	`{"anchor":"https://api.example.com/.well-known/api-catalog","item":[{"href":"https://api.example.com/"}]},` +
	`{"anchor":"https://api.example.com/",` +
	`"service-desc":[{"href":"https://api.example.com/openapi.json","type":"application/json"}],` +
	`"service-doc":[{"href":"https://api.example.com/docs","type":"text/html"}],` +
	`"status":[{"href":"https://api.example.com/healthz"}]}]}`

func catalogSettings() pwruntime.ChainSettings {
	return pwruntime.ChainSettings{
		OpenAPI: "/openapi.json", APIDoc: "scalar", APIDocPath: "/docs", Health: "/healthz",
		APICatalog: true, APICatalogOrigin: "https://api.example.com",
	}
}

func catalogHandler(t *testing.T, settings pwruntime.ChainSettings) fasthttp.RequestHandler {
	t.Helper()
	publishChainSettings(t, settings)
	handler, err := Middlewares(func(r *fasthttp.RequestCtx) {
		_, _ = r.WriteString("application")
	}, RuntimeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestAPICatalogEndpointAnswersTheWellKnownURI(t *testing.T) {
	handler := catalogHandler(t, catalogSettings())

	status, header, body := serve(t, handler, pwruntime.APICatalogPath)
	if status != fasthttp.StatusOK {
		t.Fatalf("status = %d, want 200: %s", status, body)
	}
	if body != wantCatalog {
		t.Errorf("document =\n%s\nwant\n%s", body, wantCatalog)
	}
	// Section 4.2: the Linkset media type carrying the profile that says this
	// set of links is an API catalog.
	if !strings.Contains(header, "Content-Type: "+pwruntime.APICatalogContentType) {
		t.Errorf("the Linkset media type or its profile is missing:\n%s", header)
	}
	if !strings.Contains(header, "Access-Control-Allow-Origin: *") {
		t.Errorf("the catalog was not readable cross-origin:\n%s", header)
	}
	if _, _, body := serve(t, handler, "/"); body != "application" {
		t.Errorf("an ordinary path was taken by the catalog: %q", body)
	}
}

func TestAPICatalogHeadCarriesTheRelationAndTheLength(t *testing.T) {
	handler := catalogHandler(t, catalogSettings())

	status, header, body := serveRequest(t, handler, "HEAD", pwruntime.APICatalogPath, "", "")
	if status != fasthttp.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body != "" {
		t.Errorf("a HEAD answered with a body: %q", body)
	}
	// Section 2 asks the HEAD answer to name the relation.
	if !strings.Contains(header, `Link: </.well-known/api-catalog>; rel="api-catalog"`) {
		t.Errorf("the Link header is missing:\n%s", header)
	}
	if want := "Content-Length: " + strconv.Itoa(len(wantCatalog)); !strings.Contains(header, want) {
		t.Errorf("want %q, which is what a HEAD is asking for:\n%s", want, header)
	}
}

func TestAPICatalogRefusesOtherMethods(t *testing.T) {
	handler := catalogHandler(t, catalogSettings())

	status, header, _ := serveRequest(t, handler, "POST", pwruntime.APICatalogPath, "", "")
	if status != fasthttp.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", status)
	}
	if !strings.Contains(header, "Allow: GET, HEAD") {
		t.Errorf("Allow is missing:\n%s", header)
	}
}

func TestAPICatalogIsOffByDefault(t *testing.T) {
	settings := catalogSettings()
	settings.APICatalog, settings.APICatalogOrigin = false, ""
	handler := catalogHandler(t, settings)

	if _, _, body := serve(t, handler, pwruntime.APICatalogPath); body != "application" {
		t.Errorf("the catalog answered while server.api_catalog is off: %q", body)
	}
}

// An unset origin writes absolute-path references on this transport too, and
// the harness's Host reaches none of them.
func TestAPICatalogWithoutAnOriginIsRelative(t *testing.T) {
	settings := catalogSettings()
	settings.APICatalogOrigin = ""
	handler := catalogHandler(t, settings)

	_, _, body := serve(t, handler, pwruntime.APICatalogPath)
	if !strings.Contains(body, `"href":"/openapi.json"`) {
		t.Errorf("the links are not relative:\n%s", body)
	}
	if strings.Contains(body, "example.test") {
		t.Errorf("the request Host reached the document:\n%s", body)
	}
}

// The refusal is the shared resolver's, so this transport rejects the same
// configuration the other one does rather than serving a document that meets
// section 4.1 on no reading.
func TestAPICatalogRefusesStartupWithNothingToLink(t *testing.T) {
	settings := catalogSettings()
	settings.OpenAPI, settings.APIDoc, settings.APIDocPath = "", "", ""
	publishChainSettings(t, settings)

	_, err := Middlewares(func(*fasthttp.RequestCtx) {}, RuntimeOptions{})
	if err == nil || !strings.Contains(err.Error(), "neither server.openapi nor server.api_doc") {
		t.Fatalf("error = %v, want a refusal naming what is missing", err)
	}
}
