package pw

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// formFragment is the shape generation emits for a component holding an unsafe
// form: the hidden field is the first child, and no author wrote it.
func formFragment() HTMLFragment {
	builder := htmlbind.Builder[struct{}]{}
	return (&htmlbind.Plan[struct{}]{
		Ops: []htmlbind.Op[struct{}]{
			builder.Static(`<form method="post" action="/orders">`),
			builder.CSRFField("_csrf"),
			builder.Static(`<button>buy</button></form>`),
		},
	}).Bind(struct{}{})
}

var hiddenValue = regexp.MustCompile(`name="_csrf" value="([^"]*)"`)

func sessionRequest(secret string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/orders/new", nil)
	r = protectedRequest(r)
	if secret == "" {
		return r
	}
	return r.WithContext(pwruntime.WithCSRFSecret(r.Context(), secret))
}

// protectedRequest is a request in a project that turned the check on, which is
// what makes an absent secret a misconfiguration rather than a decision.
//
// The two are different renders and they were not always distinguishable here:
// a bare context reads the zero SecurityConfig, where the check is off, so a
// test meaning "no session" was also saying "no protection wanted".
func protectedRequest(r *http.Request) *http.Request {
	security := SecurityConfig{}
	security.CSRF.Enabled = true
	return r.WithContext(pwruntime.WithResources(r.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{reflect.TypeFor[SecurityConfig](): security},
	}))
}

// The render path supplies the token, so a page carrying a form gets one
// without the handler, the template, or the author naming it.
func TestWriteHTMLChainPutsAVerifyingTokenInAnUnsafeForm(t *testing.T) {
	secret, err := pwruntime.NewCSRFSecret(nil)
	if err != nil {
		t.Fatalf("NewCSRFSecret: %v", err)
	}
	recorder := httptest.NewRecorder()
	WriteHTMLChain(recorder, sessionRequest(secret), nil, formFragment())
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", recorder.Code, recorder.Body.String())
	}
	match := hiddenValue.FindStringSubmatch(recorder.Body.String())
	if match == nil {
		t.Fatalf("no hidden field in:\n%s", recorder.Body.String())
	}
	if !pwruntime.VerifyCSRFToken(secret, match[1]) {
		t.Errorf("the emitted token does not verify against the session secret")
	}
}

// Two renders of one page emit different bytes, which is what denies a
// compression oracle anything to accumulate.
func TestRenderedTokensDifferBetweenResponses(t *testing.T) {
	secret, err := pwruntime.NewCSRFSecret(nil)
	if err != nil {
		t.Fatalf("NewCSRFSecret: %v", err)
	}
	seen := map[string]bool{}
	for range 2 {
		recorder := httptest.NewRecorder()
		WriteHTMLChain(recorder, sessionRequest(secret), nil, formFragment())
		match := hiddenValue.FindStringSubmatch(recorder.Body.String())
		if match == nil {
			t.Fatalf("no hidden field in:\n%s", recorder.Body.String())
		}
		seen[match[1]] = true
	}
	if len(seen) != 2 {
		t.Error("two responses carried the same token bytes")
	}
}

// A request with no session fails the render rather than emitting an empty
// field. htmlbind.WithoutCSRFToken would have rendered one, which is right for a
// mail body and wrong for a response: it would put an unprotected form on screen
// and say nothing.
func TestUnsafeFormWithoutASessionFailsInsteadOfRenderingEmpty(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLChain(recorder, sessionRequest(""), nil, formFragment())
	if recorder.Code == http.StatusOK {
		t.Fatalf("status = 200, want a failure\n%s", recorder.Body.String())
	}
	if match := hiddenValue.FindStringSubmatch(recorder.Body.String()); match != nil {
		t.Errorf("an unprotected field reached the response: %q", match[1])
	}
}

// A deployment that turned the check off gets the form it asked for.
//
// The render cannot fail there: generation writes the hidden field whatever the
// setting says, because the mode is not threaded into a page tree's compile, so
// refusing would mean a project with no CSRF cannot use a form action at all.
func TestUnsafeFormRendersWhenTheCheckIsOff(t *testing.T) {
	recorder := httptest.NewRecorder()
	// No SecurityConfig on the request, which is a project that turned nothing
	// on — the state a scaffold without a browser login is left in.
	WriteHTMLChain(recorder, httptest.NewRequest(http.MethodGet, "/orders/new", nil), nil, formFragment())
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want the form to render\n%s", recorder.Code, recorder.Body.String())
	}
	// The field is present and empty, because the compiler wrote it. Removing it
	// needs the generation-time mode, which the route tree does not forward.
	if match := hiddenValue.FindStringSubmatch(recorder.Body.String()); match == nil {
		t.Error("the hidden field vanished, which this path does not control")
	} else if match[1] != "" {
		t.Errorf("a token was rendered with the check off: %q", match[1])
	}
}

// A page with no unsafe form renders the same with or without a session, so
// adopting this costs nothing to a project that has none.
func TestPageWithNoFormIsUnaffected(t *testing.T) {
	withSession := httptest.NewRecorder()
	secret, err := pwruntime.NewCSRFSecret(nil)
	if err != nil {
		t.Fatalf("NewCSRFSecret: %v", err)
	}
	WriteHTMLChain(withSession, sessionRequest(secret), nil, staticFragment(`<h1>home</h1>`))
	without := httptest.NewRecorder()
	WriteHTMLChain(without, sessionRequest(""), nil, staticFragment(`<h1>home</h1>`))
	if withSession.Body.String() != without.Body.String() {
		t.Errorf("a page with no form differed:\n%q\n%q", withSession.Body.String(), without.Body.String())
	}
}

// The runtime reads the token when it issues a request, not when the page
// loaded. A value fixed at render could not survive a rotation, and the live
// connection is exactly the long-lived case where one happens.
func TestBoundaryRuntimeSendsTheTokenItReadsFromTheCookie(t *testing.T) {
	for _, fragment := range []string{
		`const csrfCookieName = "` + CSRFCookieName + `";`,
		`const csrfHeaderName = "X-CSRF-Token";`,
		// Read per request, so a rotation reaches an open screen.
		"function csrfToken() {",
		// Every request this runtime issues goes through one helper, so a call
		// site cannot omit the header by being written without it.
		"function withCSRF(headers) {",
		`const headers = withCSRF({ [modeHeader]: liveMode });`,
	} {
		if !strings.Contains(boundaryRuntimeScript, fragment) {
			t.Errorf("the runtime is missing %q", fragment)
		}
	}
}

// The name is a contract between the Go side and the script, so the two must
// not be able to drift: a module script cannot read it off its own tag.
func TestCSRFCookieNameIsOneValueOnBothSides(t *testing.T) {
	if CSRFCookieName == "" {
		t.Fatal("the cookie name is empty")
	}
	if !strings.Contains(boundaryRuntimeScript, `"`+CSRFCookieName+`"`) {
		t.Errorf("the script does not name %q", CSRFCookieName)
	}
}

// A configured field name generated forms will not carry is refused at startup.
//
// The failure it replaces is a 403 on every form submission with the reason in
// the log only, which reads as a broken request rather than as a setting.
func TestCSRFFieldNameMustMatchWhatGenerationEmits(t *testing.T) {
	if err := checkCSRFFieldName(""); err != nil {
		t.Errorf("an unset field name was refused: %v", err)
	}
	if err := checkCSRFFieldName(generatedCSRFField); err != nil {
		t.Errorf("the default was refused: %v", err)
	}
	err := checkCSRFFieldName("authenticity_token")
	if err == nil {
		t.Fatal("a field name no generated form carries was accepted")
	}
	// The message has to name both halves, or it says a setting is wrong without
	// saying what it disagrees with.
	if !strings.Contains(err.Error(), "authenticity_token") || !strings.Contains(err.Error(), generatedCSRFField) {
		t.Errorf("the message names only one side: %v", err)
	}
}
