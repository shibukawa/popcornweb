package pw

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shibukawa/tinybind-go/htmlbind"
)

// varyingFragment is the shape generation emits for a component that reaches a
// builtin element whose registration declared what its provider reads. The
// declared axis is the only thing separating it from staticFragment.
func varyingFragment(markup string, axes ...string) HTMLFragment {
	builder := htmlbind.Builder[struct{}]{}
	return (&htmlbind.Plan[struct{}]{
		Vary: axes,
		Ops:  []htmlbind.Op[struct{}]{builder.Static(markup)},
	}).Bind(struct{}{})
}

// varyHeader is every axis of a response as one string, because Vary is a list
// header and a caller may have added it either way.
func varyHeader(recorder *httptest.ResponseRecorder) string {
	return strings.Join(recorder.Header().Values("Vary"), ", ")
}

// TestDocumentCarriesTheAxesItsComponentsDeclared is the reason the declaration
// exists. A component reading a cookie through a registered element says so in
// the only place that knows — its registration — and the template says nothing a
// caller could read. Without this the response is stored under a key that
// ignores what produced it, and the second visitor is served the first one's
// page.
func TestDocumentCarriesTheAxesItsComponentsDeclared(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLChain(recorder, httptest.NewRequest(http.MethodGet, "/", nil),
		nil, varyingFragment(`<main>hello</main>`, "Cookie"))

	if got := varyHeader(recorder); !strings.Contains(got, "Cookie") {
		t.Errorf("Vary = %q, want it to name the axis the component declared", got)
	}
}

// TestDocumentMergesTheAxesOfEveryChainMember covers the fold. A layout and its
// page each reach their own elements, and a chain that reported only the leaf's
// axes would drop exactly the dependency a shared shell introduces.
func TestDocumentMergesTheAxesOfEveryChainMember(t *testing.T) {
	type layoutParams struct {
		Children htmlbind.Fragment
	}
	builder := htmlbind.Builder[layoutParams]{}
	plan := &htmlbind.Plan[layoutParams]{
		Vary: []string{"Accept-Language"},
		Ops: []htmlbind.Op[layoutParams]{
			builder.Static("<body>"),
			builder.Slot(func(params layoutParams) htmlbind.Fragment { return params.Children }, nil),
			builder.Static("</body>"),
		},
	}
	layout := plan.BindWrapper(layoutParams{},
		func(params *layoutParams, children htmlbind.Fragment) { params.Children = children })

	recorder := httptest.NewRecorder()
	WriteHTMLChain(recorder, httptest.NewRequest(http.MethodGet, "/", nil),
		[]HTMLWrapper{layout}, varyingFragment(`<main>hello</main>`, "Cookie"))

	vary := varyHeader(recorder)
	for _, axis := range []string{"Cookie", "Accept-Language"} {
		if !strings.Contains(vary, axis) {
			t.Errorf("Vary = %q, want it to name %s", vary, axis)
		}
	}
}

// TestDocumentDeclaringNoAxisVariesOnNothing is the other half of the rule. An
// axis costs a shared cache an entry per value, so a page whose components read
// nothing about the request must keep the single entry it deserves.
func TestDocumentDeclaringNoAxisVariesOnNothing(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLChain(recorder, httptest.NewRequest(http.MethodGet, "/", nil),
		nil, staticFragment(`<main>hello</main>`))

	if got := varyHeader(recorder); got != "" {
		t.Errorf("Vary = %q, want a page that renders the same for everyone to vary on nothing", got)
	}
}

// TestFragmentCarriesTheAxesItsComponentDeclared covers the same hole one layer
// down. A fragment response classifies no client and so adds no axis of its own,
// which is why the declared one is the only thing that can reach the header
// here.
func TestFragmentCarriesTheAxesItsComponentDeclared(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLFragment(recorder, httptest.NewRequest(http.MethodGet, "/rows/7", nil),
		varyingFragment(`<tr><td>7</td></tr>`, "Cookie"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := varyHeader(recorder); !strings.Contains(got, "Cookie") {
		t.Errorf("Vary = %q, want it to name the axis the component declared", got)
	}
}

// TestSequenceDoesNotCarryTheChainsAxes fixes the ordering inside
// WriteHTMLChain. A sequence is answered from the page's URL but renders
// nothing: it is the static half of a fragment, derived from the template rather
// than from this request, and it reaches no builtin element at all.
//
// Naming the chain's axes on it would be worse than untidy. A sequence is
// public, immutable, and a year long, so an axis added here fragments a cache
// entry that is meant to be held across a deploy — and it would claim a
// dependency the response provably does not have.
func TestSequenceDoesNotCarryTheChainsAxes(t *testing.T) {
	address := aRenderedSequenceAddress(t)

	request := measureRequest("/orders", false)
	request.Header.Set("Pw-Render", "sequence")
	request.Header.Set("Pw-Sequence-Address", address)
	recorder := httptest.NewRecorder()
	WriteHTMLChain(recorder, request, nil, varyingFragment(`<main>orders</main>`, "Cookie"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("a known sequence address was answered %d", recorder.Code)
	}
	if got := recorder.Header().Get("Pw-Render"); got != updateSequenceMode {
		t.Fatalf("the sequence entry did not answer: Pw-Render = %q", got)
	}
	if got := varyHeader(recorder); strings.Contains(got, "Cookie") {
		t.Errorf("Vary = %q, but a sequence renders no component and reads no cookie", got)
	}
}
