package pwruntime_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
)

func fullCatalogSettings() pwruntime.APICatalogSettings {
	return pwruntime.APICatalogSettings{
		Enabled: true, Origin: "https://api.example.com",
		OpenAPI: "/openapi.json", APIDoc: "scalar", APIDocPath: "/docs", Health: "/healthz",
	}
}

// The document has to satisfy RFC 9264 before anything else: a Linkset a
// conforming parser rejects is not an API catalog whatever it links to.
func TestAPICatalogDocumentIsAConformingLinkset(t *testing.T) {
	document := mustResolve(t, fullCatalogSettings()).Document()

	var parsed struct {
		Linkset []map[string]json.RawMessage `json:"linkset"`
	}
	if err := json.Unmarshal(document, &parsed); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, document)
	}
	if len(parsed.Linkset) != 2 {
		t.Fatalf("want two link contexts, got %d: %s", len(parsed.Linkset), document)
	}
	for index, context := range parsed.Linkset {
		anchor, ok := context["anchor"]
		if !ok {
			t.Errorf("context %d carries no anchor", index)
			continue
		}
		var value string
		if err := json.Unmarshal(anchor, &value); err != nil {
			t.Errorf("context %d anchor is not a string: %v", index, err)
		}
		// Section 4.2.2 requires a relation member per context, and section
		// 4.2.1 asks that neither anchor nor href be a relative reference.
		if len(context) < 2 {
			t.Errorf("context %d carries an anchor and no relation", index)
		}
		if !strings.HasPrefix(value, "https://") {
			t.Errorf("context %d anchor %q is a relative reference", index, value)
		}
		for relation, raw := range context {
			if relation == "anchor" {
				continue
			}
			var targets []struct {
				HRef *string `json:"href"`
				Type string  `json:"type"`
			}
			// The array is the point: RFC 9727 section 5.1 writes a bare
			// string here and Appendix A.4 writes the array RFC 9264 requires.
			if err := json.Unmarshal(raw, &targets); err != nil {
				t.Errorf("context %d relation %q is not an array of link targets: %v", index, relation, err)
				continue
			}
			if len(targets) == 0 {
				t.Errorf("context %d relation %q is an empty array", index, relation)
			}
			for _, target := range targets {
				switch {
				case target.HRef == nil:
					t.Errorf("context %d relation %q has a target with no href", index, relation)
				case !strings.HasPrefix(*target.HRef, "https://"):
					t.Errorf("context %d relation %q href %q is a relative reference", index, relation, *target.HRef)
				}
			}
		}
	}
}

// Every link is a projection of a setting, so an unset setting removes its
// member rather than leaving an empty array RFC 9264 forbids.
func TestAPICatalogLinksOnlyConfiguredEndpoints(t *testing.T) {
	settings := fullCatalogSettings()
	settings.APIDoc, settings.APIDocPath, settings.Health = "", "", ""
	document := string(mustResolve(t, settings).Document())

	for _, want := range []string{
		`"anchor":"https://api.example.com/.well-known/api-catalog"`,
		`"item":[{"href":"https://api.example.com/"}]`,
		`"service-desc":[{"href":"https://api.example.com/openapi.json","type":"application/json"}]`,
	} {
		if !strings.Contains(document, want) {
			t.Errorf("document is missing %s\n%s", want, document)
		}
	}
	for _, unwanted := range []string{"service-doc", "status", "[]"} {
		if strings.Contains(document, unwanted) {
			t.Errorf("document should not carry %q\n%s", unwanted, document)
		}
	}
}

// The probe answers with a status code and a word, so a declared media type
// would be a claim about bytes a HEAD never sends.
func TestAPICatalogStatusLinkCarriesNoType(t *testing.T) {
	document := string(mustResolve(t, fullCatalogSettings()).Document())
	if want := `"status":[{"href":"https://api.example.com/healthz"}]`; !strings.Contains(document, want) {
		t.Errorf("want %s\n%s", want, document)
	}
}

// A configured origin is the deployment's answer to RFC 9264 wanting URIs that
// are not relative references, and it makes the document self-contained.
func TestAPICatalogConfiguredOriginIsAbsolute(t *testing.T) {
	document := string(mustResolve(t, fullCatalogSettings()).Document())
	for _, want := range []string{
		`"anchor":"https://api.example.com/.well-known/api-catalog"`,
		`"href":"https://api.example.com/openapi.json"`,
	} {
		if !strings.Contains(document, want) {
			t.Errorf("want %s\n%s", want, document)
		}
	}
}

// An unnamed origin writes absolute-path references rather than guessing one.
// They resolve against whatever URI the client fetched, which is the only flow
// RFC 9727 describes, and they can never name a host this deployment does not
// own -- which is what a guess taken from the request Host could do.
func TestAPICatalogWithoutAnOriginIsRelative(t *testing.T) {
	settings := fullCatalogSettings()
	settings.Origin = ""
	document := string(mustResolve(t, settings).Document())
	for _, want := range []string{
		`"anchor":"/.well-known/api-catalog"`,
		`"item":[{"href":"/"}]`,
		`"href":"/openapi.json"`,
	} {
		if !strings.Contains(document, want) {
			t.Errorf("want %s\n%s", want, document)
		}
	}
	if strings.Contains(document, "//") {
		t.Errorf("a relative reference gained an authority\n%s", document)
	}
}

// A trailing slash on the configured origin must not double in the joins.
func TestAPICatalogOriginTrailingSlashDoesNotDouble(t *testing.T) {
	settings := fullCatalogSettings()
	settings.Origin = "http://localhost:8080/"
	document := string(mustResolve(t, settings).Document())
	if want := `"item":[{"href":"http://localhost:8080/"}]`; !strings.Contains(document, want) {
		t.Errorf("want %s\n%s", want, document)
	}
	if strings.Contains(document, "//openapi.json") {
		t.Errorf("the origin's trailing slash doubled\n%s", document)
	}
}

// The runtime refuses rather than serving a document that meets section 4.1 on
// no reading, which is what makes PW0425 an error rather than a warning.
func TestResolveAPICatalogRefusesWithNothingToLink(t *testing.T) {
	settings := fullCatalogSettings()
	settings.OpenAPI, settings.APIDoc, settings.APIDocPath = "", "", ""
	if _, err := pwruntime.ResolveAPICatalog(settings); err == nil {
		t.Fatal("want a refusal for a catalog with no API document to link")
	}
	// A UI alone still describes an API, so it is enough.
	settings.APIDoc, settings.APIDocPath = "scalar", "/docs"
	if _, err := pwruntime.ResolveAPICatalog(settings); err != nil {
		t.Fatalf("an api_doc alone should be enough to link: %v", err)
	}
}

func TestResolveAPICatalogDisabledIsInert(t *testing.T) {
	resolved, err := pwruntime.ResolveAPICatalog(pwruntime.APICatalogSettings{})
	if err != nil {
		t.Fatalf("a disabled catalog should validate nothing: %v", err)
	}
	if resolved.Enabled() {
		t.Error("a disabled catalog reports itself enabled")
	}
	if document := resolved.Document(); document != nil {
		t.Errorf("a disabled catalog produced a document: %s", document)
	}
}

func TestValidateAPICatalogOrigin(t *testing.T) {
	for _, valid := range []struct{ in, want string }{
		{"https://api.example.com", "https://api.example.com"},
		{"https://api.example.com/", "https://api.example.com"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"  https://api.example.com  ", "https://api.example.com"},
		{"", ""},
	} {
		got, err := pwruntime.ValidateAPICatalogOrigin(valid.in)
		if err != nil {
			t.Errorf("%q: %v", valid.in, err)
		} else if got != valid.want {
			t.Errorf("%q: got %q, want %q", valid.in, got, valid.want)
		}
	}
	for _, invalid := range []string{
		"api.example.com",                 // no scheme
		"ftp://api.example.com",           // not a web origin
		"https://",                        // no host
		"https://api.example.com/v1",      // a path
		"https://api.example.com?a=b",     // a query
		"https://api.example.com#f",       // a fragment
		"https://user:pw@api.example.com", // userinfo
	} {
		if got, err := pwruntime.ValidateAPICatalogOrigin(invalid); err == nil {
			t.Errorf("%q was accepted as %q", invalid, got)
		}
	}
}

func mustResolve(t *testing.T, settings pwruntime.APICatalogSettings) pwruntime.ResolvedAPICatalog {
	t.Helper()
	resolved, err := pwruntime.ResolveAPICatalog(settings)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return resolved
}
