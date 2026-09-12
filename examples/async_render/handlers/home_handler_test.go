package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pw"

	// The generated document shell registers itself during init, the way the
	// generated bootstrap links it into the binary.
	_ "async_render/templates"
)

const chromeUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

// browserRequest is a request from a client that will run the boundary runtime.
// A streaming assertion needs one: httptest.NewRequest sends no User-Agent, and
// an absent header classifies as a bot, which is the buffered branch these
// tests are not looking at.
func browserRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("User-Agent", chromeUserAgent)
	return request
}

// TestHomeStreamsFallbacksBeforeCompletions asserts the property the example
// exists to show: the fallbacks are on the wire before the work behind them
// settles, and each completion follows in its own right.
func TestHomeStreamsFallbacksBeforeCompletions(t *testing.T) {
	recorder := httptest.NewRecorder()
	profile(recorder, browserRequest("/profile"))

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Header().Get("Content-Length") != "" {
		t.Error("a streamed response must not declare a Content-Length")
	}
	fallback := strings.Index(body, "Loading orders")
	orders := strings.Index(body, "A-1043")
	if fallback < 0 || orders < 0 || orders < fallback {
		t.Fatalf("fallback did not precede its completion: %q", body)
	}
	if !strings.Contains(body, `<tb-apply for="tb-1"></tb-apply>`) {
		t.Errorf("boundary framing missing: %q", body)
	}
	wantRuntime := `<script type="module" src="` + pw.RuntimeScriptURL() + `">`
	if !strings.Contains(body, wantRuntime) {
		t.Errorf("document does not reference %q", wantRuntime)
	}
	// The profile is an ordinary parameter, so it belongs to the first pass
	// rather than to a boundary.
	if profile := strings.Index(body, "Ada Lovelace"); profile < 0 || profile > fallback {
		t.Error("settled data should render before the fallbacks")
	}
}

// TestRecommendationFailureStaysServerSide drives the whole handler rather than
// the template alone, so the assertion covers the route a reader will actually
// visit to see the recover clause.
func TestRecommendationFailureStaysServerSide(t *testing.T) {
	recorder := httptest.NewRecorder()
	profile(recorder, browserRequest("/profile?fail=recommendation"))

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("a boundary failure changed the committed status to %d", recorder.Code)
	}
	if !strings.Contains(body, "unavailable right now") {
		t.Fatalf("the recover clause did not render: %q", body)
	}
	if strings.Contains(body, "503") {
		t.Fatal("the raw error reached the page")
	}
}

// TestUnhandledFailureReplacesTheDocument is the case the escalation exists
// for: the orders boundary declares no recover clause, so the page is given up
// on rather than left claiming to load.
func TestUnhandledFailureReplacesTheDocument(t *testing.T) {
	recorder := httptest.NewRecorder()
	profile(recorder, browserRequest("/profile?fail=orders"))

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want the already committed 200", recorder.Code)
	}
	if !strings.Contains(body, "<tb-apply-document></tb-apply-document>") {
		t.Fatalf("document replacement missing: %q", body)
	}
	if !strings.Contains(body, "declared no recover clause") {
		t.Error("the registered error template was not used")
	}
	if strings.Contains(body, "503") {
		t.Fatal("the raw error reached the page")
	}
}

func TestIndexLinksEveryVariant(t *testing.T) {
	recorder := httptest.NewRecorder()
	index(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	body := recorder.Body.String()
	for _, want := range []string{`href="/profile"`, `href="/profile?fail=recommendation"`, `href="/profile?fail=orders"`} {
		if !strings.Contains(body, want) {
			t.Errorf("index does not link %s", want)
		}
	}
}
