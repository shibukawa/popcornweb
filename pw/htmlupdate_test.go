package pw

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
	"github.com/shibukawa/tinybind-go/htmlbind/delta"
	"github.com/shibukawa/tinybind-go/htmlupdate"
)

func updateConfig() HTMLConfig {
	config := pwconfig.DefaultHTMLConfig()
	config.Update = HTMLUpdateConfig{Enabled: true, ValidatorKey: "test-validator-key-test-validator-key-0123456789", MaxManifestBytes: 8 << 10}
	return config
}

// updateRequest carries the render header a client sends to ask for a delta.
func updateRequest(t *testing.T, mode string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/search?q=go", nil)
	// The version is whatever the client writes and the response echoes it back;
	// since v0.3.5 the module compares the build rather than a version number, so
	// a mode with no version is a valid request.
	request.Header.Set("Pw-Render", mode)
	// An unstamped binary has no vcs.revision, so the module falls back to a
	// per-process identity. Reading the effective value is what a rendered page
	// would have carried.
	request.Header.Set("Pw-Build", updateOptions(updateConfig()).RuntimeConfig().Build)
	return request
}

// A request that asks for nothing gets the document it always got. That is what
// keeps a crawler, curl, and a browser without the runtime unaffected.
func TestNoRenderHeaderStillServesTheDocument(t *testing.T) {
	config := updateConfig()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/search?q=go", nil)
	if serveUpdate(recorder, request, nil, staticFragment(`<h1>hi</h1>`), config, nil, false, false) {
		t.Fatal("a request with no render header was answered as an update")
	}
}

// A page rendered by another build holds client state this binary cannot vouch
// for, and none of that is visible in a validator.
func TestAnotherBuildIsAnsweredWithTheDocument(t *testing.T) {
	config := updateConfig()
	request := updateRequest(t, "navigation")
	request.Header.Set("Pw-Build", "some-other-build")
	if serveUpdate(httptest.NewRecorder(), request, nil, staticFragment(`<h1>hi</h1>`), config, nil, false, false) {
		t.Fatal("a request from another build was answered as an update")
	}
}

// The live mode shares the header with the update modes, so the module resolves
// it to a complete document. Testing it before delegating is the one thing the
// shared header costs, and this is the check that it is paid.
func TestTheLiveTokenIsNotTakenAsAnUpdate(t *testing.T) {
	config := updateConfig()
	if serveUpdate(httptest.NewRecorder(), updateRequest(t, "live"), nil, staticFragment(`<h1>hi</h1>`), config, nil, false, false) {
		t.Fatal("the live token was answered as a navigation delta")
	}
}

func TestUpdatesOffLeaveEveryRequestOnTheDocumentPath(t *testing.T) {
	config := pwconfig.DefaultHTMLConfig()
	if config.Update.Enabled {
		t.Fatal("updates default to on")
	}
	if len(chainRenderOptions(config, "", false)) != 0 {
		t.Error("a project with updates off pays for the update render options")
	}
}

// The runtime reference and its configuration are contributed at the render
// call, so no application file carries them and no shell edit can drop them.
func TestTheRuntimeIsContributedRatherThanScaffolded(t *testing.T) {
	nodes := updateHeadNodes(updateConfig(), "token-value")
	if len(nodes) != 2 {
		t.Fatalf("head nodes = %d, want the meta and the script", len(nodes))
	}
	// A malformed node would fail the render before the first byte, so the
	// nodes have to be writable to be worth contributing.
	tags, err := htmlbind.RenderHeadNodes(nodes)
	if err != nil {
		t.Fatalf("the contributed nodes are not writable: %v", err)
	}
	joined := strings.Join(tags, "")
	if !strings.Contains(joined, RuntimeScriptURL()) {
		t.Errorf("no runtime reference:\n%s", joined)
	}
	if !strings.Contains(joined, `type="module"`) {
		t.Errorf("the reference is not a module script:\n%s", joined)
	}
	// The configuration is inert escaped metadata, never inline script: a
	// module cannot read its own tag, and a policy must not need a nonce.
	if !strings.Contains(joined, updateConfigMetaName) {
		t.Errorf("no runtime configuration:\n%s", joined)
	}
	if strings.Contains(joined, "<script>") {
		t.Errorf("inline script reached the head:\n%s", joined)
	}
}

// The server and the browser build the same configuration object, so the two
// cannot disagree about a name.
func TestTheRuntimeConfigurationNamesWhatTheServerUses(t *testing.T) {
	nodes := updateHeadNodes(updateConfig(), "token-value")
	tags, err := htmlbind.RenderHeadNodes(nodes)
	if err != nil {
		t.Fatal(err)
	}
	encoded := tags[0]
	start := strings.Index(encoded, `content="`) + len(`content="`)
	end := strings.LastIndex(encoded, `"`)
	raw := strings.NewReplacer("&#34;", `"`, "&amp;", "&", "&lt;", "<", "&gt;", ">", "&#39;", "'").Replace(encoded[start:end])
	var config htmlupdate.RuntimeConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("the configuration is not readable JSON: %v\n%s", err, raw)
	}
	if config.Header != UpdateHeaderPrefix {
		t.Errorf("header = %q, want %q", config.Header, UpdateHeaderPrefix)
	}
	if config.Attr != UpdateAttributePrefix {
		t.Errorf("attr = %q, want %q", config.Attr, UpdateAttributePrefix)
	}
	if config.Global != UpdateGlobalName {
		t.Errorf("global = %q, want %q", config.Global, UpdateGlobalName)
	}
	// The token travels here so the runtime can set the header on a mutating
	// request without the page having to carry it in script.
	if config.CSRF != "token-value" {
		t.Errorf("csrf = %q", config.CSRF)
	}
}

// An unkeyed digest of low-entropy content lets a guess be confirmed by
// comparing digests, so this is refused before the port is bound.
func TestUpdatesRequireAValidatorKey(t *testing.T) {
	config := updateConfig()
	config.Update.ValidatorKey = ""
	if err := validateUpdateConfig(config); err == nil {
		t.Fatal("updates were accepted with no validator key")
	}
	// A guessable key is equivalent to an unkeyed digest, so a short one is
	// refused the same way.
	config.Update.ValidatorKey = "short"
	if err := validateUpdateConfig(config); err != ErrUpdateKeyTooShort {
		t.Fatalf("a short validator key was accepted: %v", err)
	}
	// A base64 value is measured decoded: 32 random bytes encode to 44
	// characters and pass, while 16 bytes encode to 24 characters and fail
	// even though the string itself is longer than 32.
	config.Update.ValidatorKey = "AAAAAAAAAAAAAAAAAAAAAAAA" // 24 chars = 18 decoded bytes
	if err := validateUpdateConfig(config); err != ErrUpdateKeyTooShort {
		t.Fatalf("a short base64 validator key was accepted: %v", err)
	}
	if err := validateUpdateConfig(updateConfig()); err != nil {
		t.Fatalf("a keyed configuration was refused: %v", err)
	}
	off := pwconfig.DefaultHTMLConfig()
	if err := validateUpdateConfig(off); err != nil {
		t.Fatalf("updates off were refused: %v", err)
	}
}

// redrawRequest is what the browser sends: the page's own URL, the redraw mode,
// and the component named in headers rather than in the path.
func redrawRequest(t *testing.T, kind, instance, query string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/orders?"+query, nil)
	request.Header.Set("Pw-Render", "redraw")
	request.Header.Set("Pw-Kind", kind)
	request.Header.Set("Pw-Instance", instance)
	request.Header.Set("Pw-Build", updateOptions(updateConfig()).RuntimeConfig().Build)
	return request.WithContext(pwruntime.WithResources(request.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{reflect.TypeFor[HTMLConfig](): updateConfig()},
	}))
}

// cardParams is what generation would declare for a reloadable component: the
// instance id is a parameter, because that is where the boundary reads it from.
type cardParams struct {
	ID   string
	Page string
}

var cardOps = htmlbind.Builder[cardParams]{}

// cardPlan is the shape generation emits for a reloadable component. Since
// system:tinybind v0.4.4 the boundary is not optional here: a redraw answers with
// the region the request named, so the component has to be addressable at that
// id, and a registration assembled by hand that is not fails rather than
// answering with a response holding no operations.
var cardPlan = &htmlbind.Plan[cardParams]{
	Boundary: &htmlbind.Boundary[cardParams]{
		ComponentID: "pw.test.Card@v1",
		Attr:        "data-" + UpdateAttributePrefix + "-id",
		Instance:    func(p cardParams) string { return p.ID },
		Input:       func(p cardParams) string { return delta.CanonString(p.Page) },
	},
	Ops: []htmlbind.Op[cardParams]{
		cardOps.Static("<article"),
		cardOps.Attr("id", func(p cardParams) (string, bool) { return htmlbind.Escape(p.ID), true }),
		cardOps.BoundaryAttr(),
		cardOps.Static(">page "),
		cardOps.Text(func(p cardParams) string { return p.Page }),
		cardOps.Static("</article>"),
	},
}

func cardComponent(kind string) htmlupdate.Reloadable {
	return htmlupdate.Reloadable{
		KindID: kind,
		Render: func(_ context.Context, instanceID string, values url.Values) (htmlbind.Fragment, error) {
			return cardPlan.Bind(cardParams{ID: instanceID, Page: values.Get("page")}), nil
		},
	}
}

// withEmptyReloadableRegistry isolates one test from the process-wide set a
// generated init would have filled. The registry is global on purpose — it is
// what a package's init publishes into — so a test that adds to it has to put
// back what it found.
func withEmptyReloadableRegistry(t *testing.T) {
	t.Helper()
	registry, count, failure := pwruntime.ResetReloadableForTest()
	t.Cleanup(func() { pwruntime.RestoreReloadableForTest(registry, count, failure) })
}

// The escape hatch behind Redraw: a handler that publishes a set of its own
// rather than the one its page's markup reaches.
func TestRedrawComponentsAnswersAComponentTheHandlerNamed(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := redrawRequest(t, "fixture.card.Card", "card-1", "page=2")
	if !RedrawComponents(recorder, request, cardComponent("fixture.card.Card")) {
		t.Fatal("a redraw request was not answered")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "page 2") {
		t.Errorf("the redraw did not render the component: %s", body)
	}
}

// A redrawn component renders with the page's own options, which is the whole
// of what makes the two agree.
//
// The headline case is a form. htmlbind.Builder.CSRFField fails a render that
// supplied no token rather than emitting an unprotected field, so a reloadable
// component holding one answered 500 for as long as this framework passed the
// redraw entry no options — which it did until system:tinybind v0.4.6 gave the
// entry any to pass.
func TestARedrawnComponentGetsThePagesRenderOptions(t *testing.T) {
	secret, err := pwruntime.NewCSRFSecret(nil)
	if err != nil {
		t.Fatalf("NewCSRFSecret: %v", err)
	}
	request := redrawRequest(t, "fixture.form.Panel", "panel-1", "")
	request = request.WithContext(pwruntime.WithCSRFSecret(request.Context(), secret))
	recorder := httptest.NewRecorder()
	if !RedrawComponents(recorder, request, formComponent("fixture.form.Panel")) {
		t.Fatal("a redraw request was not answered")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("a component holding a form answered %d: %s", recorder.Code, recorder.Body.String())
	}
	// The markup travels inside a JSON record, so it is read back the way the
	// client reads it: a token that survives the encoding but not the decoding
	// would reject the submission the redraw exists to enable.
	var body struct {
		Ops []struct {
			HTML string `json:"html"`
		} `json:"ops"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("the redraw body did not decode: %v\n%s", err, recorder.Body.String())
	}
	if len(body.Ops) == 0 {
		t.Fatalf("the redraw carried no operations:\n%s", recorder.Body.String())
	}
	match := hiddenValue.FindStringSubmatch(body.Ops[0].HTML)
	if match == nil {
		t.Fatalf("the redrawn form carried no token:\n%s", body.Ops[0].HTML)
	}
	if !pwruntime.VerifyCSRFToken(secret, match[1]) {
		t.Errorf("the redrawn token does not verify against the session secret")
	}
}

// formComponent is a reloadable component holding an unsafe form, which is the
// case that cannot render without the page's token.
func formComponent(kind string) htmlupdate.Reloadable {
	ops := htmlbind.Builder[cardParams]{}
	plan := &htmlbind.Plan[cardParams]{
		Boundary: &htmlbind.Boundary[cardParams]{
			ComponentID: "pw.test.Form@v1",
			Attr:        "data-" + UpdateAttributePrefix + "-id",
			Instance:    func(p cardParams) string { return p.ID },
			Input:       func(p cardParams) string { return delta.CanonString(p.Page) },
		},
		Ops: []htmlbind.Op[cardParams]{
			ops.Static(`<form method="post" action="/orders"`),
			ops.Attr("id", func(p cardParams) (string, bool) { return htmlbind.Escape(p.ID), true }),
			ops.BoundaryAttr(),
			ops.Static(`>`),
			ops.CSRFField("_csrf"),
			ops.Static(`<button>buy</button></form>`),
		},
	}
	return htmlupdate.Reloadable{
		KindID: kind,
		Render: func(_ context.Context, instanceID string, values url.Values) (htmlbind.Fragment, error) {
			return plan.Bind(cardParams{ID: instanceID, Page: values.Get("page")}), nil
		},
	}
}

// A redraw is per-user content answered from the page's own URL, and since
// system:tinybind v0.4.7 what it may be stored under is nobody's decision but
// this framework's.
//
// no-store would be the lazy answer and the wrong one: the module still computes
// an entity tag, and no-store forbids the conditional request that tag exists
// for, so a browser that may not keep the bytes could never ask whether they
// changed.
func TestARedrawIsPrivateAndRevalidates(t *testing.T) {
	first := httptest.NewRecorder()
	if !RedrawComponents(first, redrawRequest(t, "fixture.card.Card", "card-1", "page=2"),
		cardComponent("fixture.card.Card")) {
		t.Fatal("a redraw request was not answered")
	}
	if control := first.Header().Get("Cache-Control"); control != redrawCacheControl {
		t.Errorf("redraw Cache-Control = %q, want %q", control, redrawCacheControl)
	}
	assertVary(t, "redraw", first, "Pw-Render", "Pw-Build", "Pw-Kind", "Pw-Instance")
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("a redraw carried no entity tag")
	}

	// And the answer to a client that already holds those bytes is this
	// framework's too, because a 304 is a cache decision and the module stopped
	// making them.
	second := httptest.NewRecorder()
	conditional := redrawRequest(t, "fixture.card.Card", "card-1", "page=2")
	conditional.Header.Set("If-None-Match", etag)
	if !RedrawComponents(second, conditional, cardComponent("fixture.card.Card")) {
		t.Fatal("a conditional redraw request was not answered")
	}
	if second.Code != http.StatusNotModified {
		t.Errorf("a redraw whose bytes the client holds = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("a 304 carried a body: %s", second.Body.String())
	}
}

// Naming the components is what bounds the surface. A page cannot be asked to
// render one it never shows, even though the process publishes it elsewhere.
func TestRedrawComponentsRefusesAComponentThisHandlerDidNotName(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := redrawRequest(t, "fixture.other.Panel", "panel-1", "")
	if !RedrawComponents(recorder, request, cardComponent("fixture.card.Card")) {
		t.Fatal("an unnamed component was not answered at all")
	}
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

// An ordinary page request passes straight through, which is what lets the call
// sit at the top of a handler that mostly renders documents.
func TestRedrawComponentsLeavesAnOrdinaryRequestAlone(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/orders", nil)
	request = request.WithContext(pwruntime.WithResources(request.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{reflect.TypeFor[HTMLConfig](): updateConfig()},
	}))
	if RedrawComponents(recorder, request, cardComponent("fixture.card.Card")) {
		t.Error("a document request was claimed as a redraw")
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("a document request had bytes written to it: %s", recorder.Body.String())
	}
}

// A page rendered by another build is not refused: at a page URL the right
// answer to a stale redraw is the page, which the caller is about to render.
func TestRedrawComponentsFromAnotherBuildFallsThroughToThePage(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := redrawRequest(t, "fixture.card.Card", "card-1", "")
	request.Header.Set("Pw-Build", "some-other-build")
	if RedrawComponents(recorder, request, cardComponent("fixture.card.Card")) {
		t.Error("a redraw from another build was answered rather than left to the page")
	}
}

// The page tree half: a generated route handler has no seam of its own, so the
// render entry answers from the process-wide published set.
func TestTheRenderEntryAnswersARegisteredRedraw(t *testing.T) {
	withEmptyReloadableRegistry(t)
	if err := RegisterReloadable(cardComponent("fixture.card.Card")); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := redrawRequest(t, "fixture.card.Card", "card-1", "page=5")
	if !serveRegisteredRedraw(recorder, request, updateConfig()) {
		t.Fatal("a registered redraw was not answered by the render entry")
	}
	if body := recorder.Body.String(); !strings.Contains(body, "page 5") {
		t.Errorf("the redraw did not render the component: %s", body)
	}
}

// A project publishing none leaves every request on the document path.
func TestTheRenderEntryIgnoresRedrawWithNoRegistry(t *testing.T) {
	withEmptyReloadableRegistry(t)
	recorder := httptest.NewRecorder()
	if serveRegisteredRedraw(recorder, redrawRequest(t, "any.Kind", "card-1", ""), updateConfig()) {
		t.Error("a project publishing nothing answered a redraw")
	}
}

// The kind covers a component's name, parameters, and markup but not its
// package, so two identical templates in different packages produce the same
// one. A generated init cannot answer that, which is why startup does.
func TestADuplicateRegistrationIsAStartupDiagnostic(t *testing.T) {
	withEmptyReloadableRegistry(t)
	component := cardComponent("fixture.card.Card")
	if err := RegisterReloadable(component); err != nil {
		t.Fatal(err)
	}
	if err := validateReloadableRegistration(); err != nil {
		t.Fatalf("a clean registration was reported as a failure: %v", err)
	}
	// The generated init discards this, which is exactly the case under test.
	_ = RegisterReloadable(component)
	err := validateReloadableRegistration()
	if err == nil {
		t.Fatal("a duplicate kind was accepted")
	}
	if !strings.Contains(err.Error(), "fixture.card.Card") {
		t.Errorf("the diagnostic does not name the component: %v", err)
	}
}

// The end this whole path exists for: one URL answers a delta to a client that
// asked, and the ordinary document to everyone else.
func TestANavigationRequestIsAnsweredWithADelta(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := updateRequest(t, "navigation")
	if !serveUpdate(recorder, request, nil, staticFragment(`<h1>results</h1>`), updateConfig(), nil, false, false) {
		t.Fatal("a navigation request was not answered as an update")
	}
	response := recorder.Result()
	// A delta is a record stream, not a document. A cache that handed one to a
	// document request would put JSON records on screen.
	if got := response.Header.Get("Content-Type"); strings.Contains(got, "text/html") {
		t.Errorf("Content-Type = %q, want the record stream", got)
	}
	// The served mode is echoed, so a proxy that substituted a body is
	// detectable rather than silently applied. The version echoed is the one the
	// request carried, and since v0.3.5 the client sends a bare mode token.
	if got := response.Header.Get("Pw-Render"); got != "navigation" {
		t.Errorf("Pw-Render = %q", got)
	}
	// Both representations of one URL vary on what selected them.
	vary := strings.Join(response.Header.Values("Vary"), ",")
	for _, name := range []string{"Pw-Render", "Pw-Build"} {
		if !strings.Contains(vary, name) {
			t.Errorf("Vary %q is missing %s", vary, name)
		}
	}
	if recorder.Body.Len() == 0 {
		t.Error("the delta carried no records")
	}
}

// The document path keeps every byte it had. Adopting updates must not change
// what a request that asks for none receives.
func TestTheDocumentPathIsUnchangedByEnablingUpdates(t *testing.T) {
	off := httptest.NewRecorder()
	WriteHTMLChain(off, httptest.NewRequest(http.MethodGet, "/", nil), nil, staticFragment(`<h1>home</h1>`))
	if !strings.Contains(off.Body.String(), "<h1>home</h1>") {
		t.Fatalf("the document did not render:\n%s", off.Body.String())
	}
}

// The response headers of every update mode are this framework's, because the
// wire is. These assert what each mode must carry whatever the module does or
// stops doing, which is the point of owning them here.
func TestUpdateResponseHeadersAreThisFrameworksToSet(t *testing.T) {
	config := updateConfig()

	// The address has to be one this process rendered, because the policy under
	// test is the one a served sequence carries and a refusal is deliberately
	// not served under it.
	address := aRenderedSequenceAddress(t)
	sequence := httptest.NewRequest(http.MethodGet, "/orders", nil)
	sequence.Header.Set("Pw-Render", "sequence")
	sequence.Header.Set("Pw-Sequence-Address", address)
	sequenceOut := httptest.NewRecorder()
	if !serveSequence(sequenceOut, sequence, measureConfig()) {
		t.Fatal("a sequence request was not answered by the sequence entry")
	}
	if sequenceOut.Code != http.StatusOK {
		t.Fatalf("a known sequence address was answered %d", sequenceOut.Code)
	}
	// A sequence is public, immutable, and a year long, and it is served from
	// the page's own URL. Without the Vary a cache stores it under that URL
	// alone and answers every later request for the page with a JSON body —
	// which is not a degraded page but no page at all, until the cache expires.
	// Found in a browser: after one sequence was fetched the page stopped
	// loading, and every navigation fell back.
	//
	// The address is an axis too, or one tree answers every request for another.
	// The build is not: a sequence is addressed by a digest of its own content,
	// so the same address across two builds is the same bytes, and holding it
	// through a deploy is the point of the immutable policy.
	assertVary(t, "sequence", sequenceOut, "Pw-Render", "Pw-Sequence-Address")
	if control := sequenceOut.Header().Get("Cache-Control"); control != sequenceCacheControl {
		t.Errorf("sequence Cache-Control = %q, want %q", control, sequenceCacheControl)
	}
	// And it says what it is. The client discards a body whose echo disagrees,
	// so a sequence claiming to be a navigation is not a cosmetic mismatch: every
	// tree is thrown away, every operation carrying values falls back, and every
	// in-page navigation becomes a full document. system:tinybind v0.4.7 echoed
	// the wrong one and v0.4.8 made the mode switch exhaustive, so this asserts
	// the value rather than that the two sides still agree.
	if got := sequenceOut.Header().Get("Pw-Render"); got != updateSequenceMode {
		t.Errorf("a sequence response says it is %q, want %q", got, updateSequenceMode)
	}

	// A refusal is answered under no policy at all. It says why one request
	// failed, so nothing else may be answered with it, whatever the mode it was
	// refused in would have been served under.
	unknown := httptest.NewRequest(http.MethodGet, "/orders", nil)
	unknown.Header.Set("Pw-Render", "sequence")
	unknown.Header.Set("Pw-Sequence-Address", "no-such-address")
	unknownOut := httptest.NewRecorder()
	if !serveSequence(unknownOut, unknown, config) {
		t.Fatal("an unknown sequence address was not answered by the sequence entry")
	}
	if unknownOut.Code != http.StatusNotFound {
		t.Errorf("an unknown sequence address was answered %d, want 404", unknownOut.Code)
	}
	if control := unknownOut.Header().Get("Cache-Control"); control != updateCacheControl {
		t.Errorf("a refused sequence Cache-Control = %q, want %q", control, updateCacheControl)
	}
	if got := unknownOut.Header().Get("Content-Type"); !strings.Contains(got, "problem+json") {
		t.Errorf("a refused sequence Content-Type = %q, want problem details", got)
	}
	// Even a refusal answers from the page's URL, so it still says what told it
	// apart from the page.
	assertVary(t, "refused sequence", unknownOut, "Pw-Render", "Pw-Build")

	// A document is answered from the same URL and must not carry the redraw's
	// headers in its Vary: varying on what it never reads fragments a cache for
	// nothing, and the render header already tells the two apart.
	document := httptest.NewRecorder()
	documentRequest := httptest.NewRequest(http.MethodGet, "/orders", nil)
	documentRequest = documentRequest.WithContext(pwruntime.WithResources(documentRequest.Context(),
		pwruntime.Resources{Configs: map[reflect.Type]any{reflect.TypeFor[HTMLConfig](): config}}))
	WriteHTMLChain(document, documentRequest, nil, staticFragment(`<h1>hi</h1>`))
	vary := strings.Join(document.Header().Values("Vary"), ", ")
	for _, unwanted := range []string{"Pw-Kind", "Pw-Instance"} {
		if strings.Contains(vary, unwanted) {
			t.Errorf("a document varies on %s: %q", unwanted, vary)
		}
	}
	if !strings.Contains(vary, "Pw-Render") {
		t.Errorf("a document does not vary on the render header: %q", vary)
	}
}

// aRenderedSequenceAddress drives a navigation that answers with values and
// returns the address of the tree filling them.
//
// Nothing else can produce one: a sequence is registered by rendering the
// template that owns it, so an address is a fact about what this process has
// done rather than a string a test can invent.
func aRenderedSequenceAddress(t *testing.T) string {
	t.Helper()
	warm, _ := serveMeasured("/orders?q=one", "seed", false)
	delta, _ := serveMeasured("/orders?q=two", clientManifest(warm), true)
	for _, line := range strings.Split(delta, "\n") {
		var record struct {
			Seq string `json:"seq"`
		}
		if json.Unmarshal([]byte(line), &record) == nil && record.Seq != "" {
			return record.Seq
		}
	}
	t.Fatal("no navigation record carried a sequence address")
	return ""
}

func assertVary(t *testing.T, mode string, recorder *httptest.ResponseRecorder, want ...string) {
	t.Helper()
	vary := strings.Join(recorder.Header().Values("Vary"), ", ")
	for _, name := range want {
		if !strings.Contains(vary, name) {
			t.Errorf("%s: Vary = %q, want it to name %s", mode, vary, name)
		}
	}
}

// markupFor renders one chain the way a browser asking for nothing would
// receive it, under the config given.
func markupFor(t *testing.T, config HTMLConfig) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/search?q=go", nil)
	ctx := pwruntime.WithResources(request.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{reflect.TypeFor[HTMLConfig](): config},
	})
	WriteHTMLChain(recorder, request.WithContext(ctx), nil,
		staticFragment(`<form method="get" action="/search"><input name="q" value="go"><button name="view" value="grid">Search</button></form>`))
	return recorder.Body.String()
}

// The whole design rests on the document path being the one every client can
// take, and a browser with scripting turned off is the plainest such client: it
// never loads the runtime, so a link is a link and a GET form writes its own
// query exactly as it did before any of this existed.
//
// That makes the fallback not a code path but the absence of one, and this is
// what says so: enabling updates may add to the head, and may change nothing in
// the markup a client renders and submits. The head contribution itself — an
// inert meta and a module script, with no inline script anywhere — is
// characterized by TestTheRuntimeIsContributedRatherThanScaffolded.
func TestAClientThatRunsNoScriptSubmitsTheSameMarkupEitherWay(t *testing.T) {
	off := markupFor(t, pwconfig.DefaultHTMLConfig())
	on := markupFor(t, updateConfig())

	if on != off {
		t.Errorf("enabling updates changed the markup a no-script client renders:\n on: %s\noff: %s", on, off)
	}
	// Named rather than left to the comparison above, because the two could
	// agree by both being wrong.
	for _, want := range []string{`method="get"`, `action="/search"`, `name="q"`, `name="view" value="grid"`} {
		if !strings.Contains(on, want) {
			t.Errorf("the GET form lost %s:\n%s", want, on)
		}
	}
	// Nothing the runtime needs may be load-bearing for a client that will never
	// run it: no inline handler may be what performs the submission, and no
	// attribute may gate it.
	for _, forbidden := range []string{"<script", "onsubmit", "onclick", "hidden"} {
		if strings.Contains(on, forbidden) {
			t.Errorf("the markup depends on script through %q:\n%s", forbidden, on)
		}
	}
	// Updates really are on for this render, so the comparison above was between
	// a page that has them and a page that does not, rather than between two
	// pages that both have nothing.
	if len(chainRenderOptions(updateConfig(), "token", false)) == 0 {
		t.Error("updates contributed no render options, so this compared the wrong two things")
	}
}
