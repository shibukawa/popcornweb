package pw

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// scopedFragment is a component carrying one of the three scope states. An
// undeclared one is staticFragment, which is the shape most components have.
func scopedFragment(markup string, private, public bool, source string) HTMLFragment {
	builder := htmlbind.Builder[struct{}]{}
	return (&htmlbind.Plan[struct{}]{
		DeclaresPrivate: private,
		DeclaresPublic:  public,
		PrivateSource:   source,
		Ops:             []htmlbind.Op[struct{}]{builder.Static(markup)},
	}).Bind(struct{}{})
}

// scopedShell is a document shell carrying a scope declaration. A wrapper is
// what can say a whole document is shared, because it contains everything below
// it, so the public half of these tests has to be written here rather than on a
// leaf.
func scopedShell(private, public bool, source string) HTMLWrapper {
	type shellParams struct {
		Children htmlbind.Fragment
	}
	builder := htmlbind.Builder[shellParams]{}
	plan := &htmlbind.Plan[shellParams]{
		DeclaresPrivate: private,
		DeclaresPublic:  public,
		PrivateSource:   source,
		Ops: []htmlbind.Op[shellParams]{
			builder.Static("<body>"),
			builder.Slot(func(params shellParams) htmlbind.Fragment { return params.Children }, nil),
			builder.Static("</body>"),
		},
	}
	return plan.BindWrapper(shellParams{},
		func(params *shellParams, children htmlbind.Fragment) { params.Children = children })
}

// TestUndeclaredDocumentIsPrivate is the default the whole feature exists for. A
// project that writes no annotation anywhere is the login-gated one whose pages
// are least examined, so the answer it gets without asking has to be the safe
// one.
func TestUndeclaredDocumentIsPrivate(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLChain(recorder, httptest.NewRequest(http.MethodGet, "/account", nil),
		nil, staticFragment(`<main>balance</main>`))

	if got := recorder.Header().Get("Cache-Control"); got != privateCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, privateCacheControl)
	}
}

// TestDocumentShellDeclaringPublicWritesNoPolicy covers the shared answer and
// the shape of it. The framework stops asserting rather than asserting
// something else: a lifetime is a deployment's to choose, and one invented here
// would be wrong on every page that did not ask for it.
func TestDocumentShellDeclaringPublicWritesNoPolicy(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLChain(recorder, httptest.NewRequest(http.MethodGet, "/pricing", nil),
		[]HTMLWrapper{scopedShell(false, true, "")}, staticFragment(`<main>plans</main>`))

	if got := recorder.Header().Get("Cache-Control"); got != "" {
		t.Errorf("Cache-Control = %q, want the framework to say nothing about a shared page", got)
	}
}

// TestDeclaredPrivateBeneathAPublicShellStaysPrivate is the combination
// generation cannot catch. The refusal upstream walks a call graph, and a chain
// this framework assembles at run time never appeared in one, so private has to
// win here or the assertion in the source would decide a page the author never
// composed.
func TestDeclaredPrivateBeneathAPublicShellStaysPrivate(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLChain(recorder, httptest.NewRequest(http.MethodGet, "/pricing", nil),
		[]HTMLWrapper{scopedShell(false, true, "")},
		scopedFragment(`<main>your plan</main>`, true, false, "pages/account.pw.html:PlanSummary"))

	if got := recorder.Header().Get("Cache-Control"); got != privateCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, privateCacheControl)
	}
}

// TestUndeclaredFragmentIsPrivate covers the swap target. A fragment answers
// with no wrapper, and a wrapper is the only thing that can declare a document
// shared, so an undeclared fragment has nothing above it to inherit from.
func TestUndeclaredFragmentIsPrivate(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLFragment(recorder, httptest.NewRequest(http.MethodGet, "/rows/7", nil),
		staticFragment(`<tr><td>7</td></tr>`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Cache-Control"); got != privateCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, privateCacheControl)
	}
}

// TestPrivateComponentIsKeyedByTheRequestIdentity is the half of the feature the
// header does not cover. A scoped entry that ignored the identity would be a
// shared entry with a private label on the response around it, which is worse
// than either alone: the header says the browser may not share it while the
// server hands the next reader the same bytes.
func TestPrivateComponentIsKeyedByTheRequestIdentity(t *testing.T) {
	first := renderScopedGreeting(t, "account-1", "hello account-1")
	if !strings.Contains(first, "hello account-1") {
		t.Fatalf("first render = %q", first)
	}
	// Same component, same parameters, different reader. A hit here would be the
	// leak: the body was rendered for somebody else.
	second := renderScopedGreeting(t, "account-2", "hello account-2")
	if strings.Contains(second, "account-1") {
		t.Fatalf("the second reader was served the first one's entry: %q", second)
	}
	// And the scope is the only thing that changed, so returning to the first
	// reader has to reach the entry that reader already has. Without this the
	// test would pass on a cache that simply never stores.
	if again := renderScopedGreeting(t, "account-1", "goodbye account-1"); !strings.Contains(again, "hello account-1") {
		t.Errorf("the first reader missed their own entry: %q", again)
	}
}

// TestPrivateComponentWithNoIdentityStoresNothing covers the fallback. An
// anonymous request has no scope to key by, and an entry written under an empty
// one would be a shared entry wearing a private label.
func TestPrivateComponentWithNoIdentityStoresNothing(t *testing.T) {
	if first := renderScopedGreeting(t, "", "first anonymous"); !strings.Contains(first, "first anonymous") {
		t.Fatalf("first render = %q", first)
	}
	// A second anonymous render must run the body again rather than replay the
	// first, which is what an entry under an empty scope would have done.
	if second := renderScopedGreeting(t, "", "second anonymous"); !strings.Contains(second, "second anonymous") {
		t.Errorf("an anonymous request was served a stored entry: %q", second)
	}
}

// scopedGreetingID keys the one cached component these tests share. It stands in
// for the identity plus plan fingerprint generation emits, and it is constant so
// that two renders reach the same entry whenever their scope agrees.
const scopedGreetingID = "pw_test:ScopedGreeting:cachescope"

// renderScopedGreeting renders one storing private component as the given
// reader and returns the bytes that reached the client. Body is the text this
// call would render on a miss, so a hit is visible as the previous call's text.
func renderScopedGreeting(t *testing.T, subject, body string) string {
	t.Helper()
	type greetingParams struct{ Body string }
	builder := htmlbind.Builder[greetingParams]{}
	plan := &htmlbind.Plan[greetingParams]{
		DeclaresPrivate: true,
		PrivateSource:   "pw_test:ScopedGreeting",
		Cache: &htmlbind.CachePolicy[greetingParams]{
			ID:     scopedGreetingID,
			TTL:    time.Minute,
			Key:    func(greetingParams) string { return "" },
			Scoped: true,
		},
		Ops: []htmlbind.Op[greetingParams]{
			builder.Static("<p>"),
			builder.Text(func(p greetingParams) string { return p.Body }),
			builder.Static("</p>"),
		},
	}

	request := httptest.NewRequest(http.MethodGet, "/greeting", nil)
	ctx := withTestHTMLConfig(request.Context(), pwconfig.DefaultHTMLConfig())
	if subject != "" {
		ctx = pwruntime.WithAuthentication(ctx, pwruntime.Authentication{
			Authenticated: true, Subject: subject, Method: "test",
		})
	}
	recorder := httptest.NewRecorder()
	WriteHTMLChain(recorder, request.WithContext(ctx), nil, plan.Bind(greetingParams{Body: body}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d for subject %q", recorder.Code, subject)
	}
	return recorder.Body.String()
}

// TestTheHTMLErrorPageCarriesTheChainsPolicy covers the one document response
// that does not reach WriteHTMLChain. It renders through the failed page's own
// shell, so it carries whatever that shell carries, and a 500 showing a
// signed-in reader's name is the ordinary case rather than an unusual one.
func TestTheHTMLErrorPageCarriesTheChainsPolicy(t *testing.T) {
	previous := registeredHTMLErrorPage()
	t.Cleanup(func() { RegisterHTMLErrorPage(previous) })
	RegisterHTMLErrorPage(func(p Problem) HTMLFragment {
		return staticFragment(`<main>sorry</main>`)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/account", nil)
	request.Header.Set("Accept", "text/html")
	writeHTMLProblem(recorder, request, nil, InternalServerError("boom"))

	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("content type = %q, want the HTML error page", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != privateCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, privateCacheControl)
	}
}
