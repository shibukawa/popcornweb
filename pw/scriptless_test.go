package pw

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// asyncChain is a leaf whose render opens an await boundary, which is the only
// shape any of this applies to.
func scriptlessRequest(t *testing.T, target string, cookie bool) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	// A browser with scripting disabled sends an ordinary User-Agent. That is
	// the whole problem: nothing in the header says it will not run the runtime.
	request.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Firefox/141.0")
	if cookie {
		request.AddCookie(&http.Cookie{Name: scriptlessCookieName, Value: "1"})
	}
	ctx := pwruntime.WithResources(request.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{reflect.TypeFor[HTMLConfig](): pwconfig.DefaultHTMLConfig()},
	})
	return request.WithContext(ctx)
}

// The block is contributed rather than scaffolded, so no application template
// carries it and no shell edit can drop it. It is also the whole mechanism: the
// target is this same page under the marker, so the redirect lands on the
// rendered document instead of away from it.
func TestTheProbeRedirectsToThisSamePageUnderTheMarker(t *testing.T) {
	request := scriptlessRequest(t, "/search?q=go&page=2", false)
	node := scriptlessProbeHead(request)

	rendered, err := htmlbind.RenderHeadNodes([]htmlbind.HeadNode{node})
	if err != nil {
		t.Fatalf("the contributed node is not writable: %v", err)
	}
	tags := strings.Join(rendered, "")
	if !strings.Contains(tags, "<noscript>") {
		t.Errorf("the block is not inside noscript, so every client would follow it:\n%s", tags)
	}
	if !strings.Contains(tags, `http-equiv="refresh"`) {
		t.Errorf("no refresh directive:\n%s", tags)
	}
	// The path must survive, and so must the parameters the reader arrived with:
	// redirecting to a bare path would answer a different page than the one they
	// asked for.
	for _, want := range []string{"/search?", "q=go", "page=2", scriptlessMarkerParam + "=1"} {
		if !strings.Contains(tags, want) {
			t.Errorf("the target lost %q:\n%s", want, tags)
		}
	}
	// Scheme and host are the browser's own. Naming them would send a reader
	// behind a TLS-terminating proxy to the scheme the application saw.
	if strings.Contains(tags, "http://") || strings.Contains(tags, "https://") {
		t.Errorf("the target is absolute:\n%s", tags)
	}
}

// The parameter alone is what makes this correct. A client that refuses cookies
// still reaches the buffered branch, which is what stops the redirect cycling.
func TestTheMarkerAloneSelectsTheBufferedBranch(t *testing.T) {
	recorder := httptest.NewRecorder()
	buffered, handled := resolveScriptless(recorder, scriptlessRequest(t, "/search?"+scriptlessMarkerParam+"=1", false))

	if handled {
		t.Fatal("a marked request was answered instead of rendered")
	}
	if !buffered {
		t.Error("a marked request did not select the buffered branch, so the redirect would cycle")
	}
	var marked *http.Cookie
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == scriptlessCookieName {
			marked = cookie
		}
	}
	if marked == nil {
		t.Fatal("the answer was not remembered, so every page would pay the round trip")
	}
	if !marked.HttpOnly {
		t.Error("the cookie is readable from script, which nothing needs")
	}
	if marked.SameSite == http.SameSiteStrictMode {
		t.Error("Strict withholds the answer from a reader arriving by an external link")
	}
}

// A cookie the reader already holds answers without a marker, which is what the
// cookie is for and the only thing it is for.
func TestAKnownClientNeedsNoMarker(t *testing.T) {
	buffered, handled := resolveScriptless(httptest.NewRecorder(), scriptlessRequest(t, "/search", true))
	if handled || !buffered {
		t.Errorf("a known scriptless client was not served buffered: buffered=%v handled=%v", buffered, handled)
	}
}

// Both halves present means the marker outlived the page that needed it — a
// bookmark, most likely. Cleaning the URL keeps it from being carried further.
func TestAMarkerOnTopOfTheCookieIsCleanedAway(t *testing.T) {
	recorder := httptest.NewRecorder()
	buffered, handled := resolveScriptless(recorder, scriptlessRequest(t, "/search?q=go&"+scriptlessMarkerParam+"=1", true))

	if !handled || buffered {
		t.Fatalf("the redundant marker was not cleaned away: buffered=%v handled=%v", buffered, handled)
	}
	response := recorder.Result()
	if response.StatusCode != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	location := response.Header.Get("Location")
	if strings.Contains(location, scriptlessMarkerParam) {
		t.Errorf("Location still carries the marker: %s", location)
	}
	if !strings.Contains(location, "q=go") {
		t.Errorf("Location dropped the reader's own parameters: %s", location)
	}
	// One reader's answer must not be replayed to the next from a cache.
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// A meta refresh re-issues a GET, so a non-GET response that emitted one would
// discard the validation errors or the receipt it had just rendered.
func TestANonGetResponseIsNeverAsked(t *testing.T) {
	if scriptlessSafeMethod(httptest.NewRequest(http.MethodPost, "/orders", nil)) {
		t.Error("a POST response would be asked, and its rendered result thrown away")
	}
	// The redirect is bounded by the same rule, so a marked POST renders rather
	// than being sent round.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/orders?"+scriptlessMarkerParam+"=1", nil)
	request.AddCookie(&http.Cookie{Name: scriptlessCookieName, Value: "1"})
	buffered, handled := resolveScriptless(recorder, request.WithContext(scriptlessRequest(t, "/", false).Context()))
	if handled {
		t.Error("a marked POST was redirected, losing its body")
	}
	if !buffered {
		t.Error("a marked POST did not render buffered")
	}
}

// An ordinary scripted browser must see none of this: no marker, no redirect,
// and no extra request. It is asked once, on the response that would otherwise
// be wrong for it, and the block costs it nothing because it never fires.
func TestAnUnknownClientIsAskedAndNothingElse(t *testing.T) {
	recorder := httptest.NewRecorder()
	buffered, handled := resolveScriptless(recorder, scriptlessRequest(t, "/search", false))

	if buffered || handled {
		t.Errorf("an unknown client was diverted: buffered=%v handled=%v", buffered, handled)
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Error("an unknown client was given a cookie before it answered anything")
	}
}

// scriptlessShell is the document a project's layout compiles to.
//
// MergedHead is what makes it usable here: it is the op a document shell places
// inside its head element, and it is where every contribution lands. The bare
// fragment fixtures the other tests in this package use have no shell at all, so
// they cannot observe a head contribution however it is made.
func scriptlessShell() HTMLWrapper {
	type documentParams struct {
		Children htmlbind.Fragment
	}
	builder := htmlbind.Builder[documentParams]{}
	plan := &htmlbind.Plan[documentParams]{Ops: []htmlbind.Op[documentParams]{
		builder.Static("<!doctype html><html><head>"),
		builder.MergedHead(),
		builder.Static("</head><body>"),
		builder.Slot(func(params documentParams) htmlbind.Fragment { return params.Children }, nil),
		builder.Static("</body></html>"),
	}}
	return plan.BindWrapper(documentParams{}, func(params *documentParams, children htmlbind.Fragment) {
		params.Children = children
	})
}

// The helpers above are only correct if the render entry actually calls them, so
// this drives the whole round trip the way a scriptless browser does: the
// streamed page carries the block, the redirect it names renders the settled
// document, and the reader ends up on the page they asked for.
func TestTheWholeRoundTripLeavesAScriptlessReaderOnTheSettledPage(t *testing.T) {
	streamed := httptest.NewRecorder()
	WriteHTMLChain(streamed, scriptlessRequest(t, "/search?q=go", false),
		[]HTMLWrapper{scriptlessShell()}, asyncPage(asyncPageParams{Body: Resolved("results")}))

	first := streamed.Body.String()
	if !strings.Contains(first, "<noscript>") {
		t.Fatalf("the streamed page did not ask, so a scriptless reader keeps the fallback:\n%s", first)
	}
	if !strings.Contains(first, scriptlessMarkerParam+"=1") {
		t.Errorf("the block names no marker target:\n%s", first)
	}
	if !strings.Contains(first, "<p>loading</p>") {
		t.Errorf("the streaming branch was not taken after all:\n%s", first)
	}
	// The marker cookie is a third representation of this URL, so a shared cache
	// must not hand one reader's answer to the next.
	if vary := strings.Join(streamed.Result().Header.Values("Vary"), ","); !strings.Contains(vary, "Cookie") {
		t.Errorf("Vary %q does not name Cookie", vary)
	}

	settled := httptest.NewRecorder()
	WriteHTMLChain(settled, scriptlessRequest(t, "/search?q=go&"+scriptlessMarkerParam+"=1", false),
		[]HTMLWrapper{scriptlessShell()}, asyncPage(asyncPageParams{Body: Resolved("results")}))

	second := settled.Body.String()
	if !strings.Contains(second, "<p>results</p>") {
		t.Fatalf("the marked request did not settle the boundary:\n%s", second)
	}
	if strings.Contains(second, "<p>loading</p>") {
		t.Errorf("the marked request still committed a fallback:\n%s", second)
	}
	// Asking again on the page that answered would be a redirect loop for a
	// reader whose browser also refuses the cookie.
	if strings.Contains(second, "<noscript>") {
		t.Errorf("the marked response asked again, which cycles without cookies:\n%s", second)
	}
}

// A scripted browser is the common case and must pay nothing at all for this.
// The block is inert for it, but a marker in its URL or a cookie it never
// answered for would not be.
func TestAScriptedBrowserStillStreams(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := scriptlessRequest(t, "/search", false)
	request.Header.Set("User-Agent", chromeUserAgent)
	WriteHTMLChain(recorder, request, []HTMLWrapper{scriptlessShell()},
		asyncPage(asyncPageParams{Body: Resolved("results")}))

	if body := recorder.Body.String(); !strings.Contains(body, "<p>loading</p>") {
		t.Errorf("a scripted browser lost progressive delivery:\n%s", body)
	}
	if cookies := recorder.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("a scripted browser was marked: %v", cookies)
	}
}

// Turning it off returns the streamed response to exactly what it was, which is
// what the key is for.
func TestDetectionOffAsksNothing(t *testing.T) {
	config := pwconfig.DefaultHTMLConfig()
	config.ScriptlessDetection = false
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/search", nil)
	request.Header.Set("User-Agent", chromeUserAgent)
	ctx := pwruntime.WithResources(request.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{reflect.TypeFor[HTMLConfig](): config},
	})
	WriteHTMLChain(recorder, request.WithContext(ctx), []HTMLWrapper{scriptlessShell()},
		asyncPage(asyncPageParams{Body: Resolved("results")}))

	body := recorder.Body.String()
	if strings.Contains(body, "<noscript>") {
		t.Errorf("the block was contributed with detection off:\n%s", body)
	}
	if vary := strings.Join(recorder.Result().Header.Values("Vary"), ","); strings.Contains(vary, "Cookie") {
		t.Errorf("Vary %q names Cookie with detection off, costing cache hits for nothing", vary)
	}
}

// A page with no await block has one representation and is already correct for
// every client, so none of this may appear on it.
func TestAPageWithNoBoundaryIsUntouched(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLChain(recorder, scriptlessRequest(t, "/about", false),
		[]HTMLWrapper{scriptlessShell()}, staticFragment(`<h1>about</h1>`))

	if body := recorder.Body.String(); strings.Contains(body, "<noscript>") {
		t.Errorf("a static page was asked:\n%s", body)
	}
	if vary := strings.Join(recorder.Result().Header.Values("Vary"), ","); strings.Contains(vary, "Cookie") {
		t.Errorf("Vary %q names Cookie on a page with one representation", vary)
	}
}
