package pwfast

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
	"github.com/shibukawa/tinybind-go/htmlbind/delta"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// The cache policy of an update response is this framework's to write. The
// module computes the axes, the mode echo, and the entity tag and deliberately
// writes no policy, so a response leaving here without one is a per-reader body
// a shared cache may hold under the page's own URL — and a page, its deltas and
// its redraws share that URL.
//
// These are the assertions pw/htmlupdate_test.go makes about the other
// transport. They live here too because the two halves answering one request
// under two policies is the drift the shared decisions exist to prevent, and a
// test on one side alone is what let this half ship with no policy at all.

// cardParams is what generation would declare for a reloadable component: the
// instance id is a parameter, because that is where the boundary reads it from.
type cardParams struct {
	ID   string
	Page string
}

var cardOps = htmlbind.Builder[cardParams]{}

var cardPlan = &htmlbind.Plan[cardParams]{
	Boundary: &htmlbind.Boundary[cardParams]{
		ComponentID: "pw.test.Card@v1",
		Attr:        "data-" + pwruntime.UpdateAttributePrefix + "-id",
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

func cardComponent(kind string) pwruntime.UpdateReloadable {
	return pwruntime.UpdateReloadable{
		KindID: kind,
		Render: func(_ context.Context, instanceID string, values url.Values) (htmlbind.Fragment, error) {
			return cardPlan.Bind(cardParams{ID: instanceID, Page: values.Get("page")}), nil
		},
	}
}

// redrawHeaders are what a redraw client sends.
func redrawHeaders(kind, instance string) map[string]string {
	return map[string]string{
		"Pw-Render":   "redraw",
		"Pw-Kind":     kind,
		"Pw-Instance": instance,
		"Pw-Build":    "test-build",
	}
}

// headerValue reads one field out of a serialized response header block.
func headerValue(header, name string) string {
	for _, line := range strings.Split(header, "\r\n") {
		field, value, found := strings.Cut(line, ":")
		if found && strings.EqualFold(strings.TrimSpace(field), name) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// varyAxes collects every axis named across the response's Vary fields, which
// may arrive as several lines or one comma-separated one.
func varyAxes(header string) map[string]bool {
	axes := map[string]bool{}
	for _, line := range strings.Split(header, "\r\n") {
		field, value, found := strings.Cut(line, ":")
		if !found || !strings.EqualFold(strings.TrimSpace(field), "Vary") {
			continue
		}
		for _, axis := range strings.Split(value, ",") {
			if axis = strings.TrimSpace(axis); axis != "" {
				axes[strings.ToLower(axis)] = true
			}
		}
	}
	return axes
}

func assertVary(t *testing.T, mode, header string, want ...string) {
	t.Helper()
	axes := varyAxes(header)
	for _, axis := range want {
		if !axes[strings.ToLower(axis)] {
			t.Errorf("the %s response does not vary on %s:\n%s", mode, axis, header)
		}
	}
}

// An action response restates validators for one document under whatever
// credentials the request carried, so it is never shareable. Without the render
// axis a cache stores it under the page's URL alone and answers every later
// request for the page with a JSON body, which is not a degraded page but no
// page at all until the entry expires.
func TestAnActionResponseIsNeverStored(t *testing.T) {
	withUpdateSettings(t)
	_, header, _ := serveWith(t, func(r *fasthttp.RequestCtx) {
		WriteUpdate(r, fasthttp.StatusOK, Replace("total", staticFragment(`<b id="total">9</b>`)))
	}, map[string]string{"Pw-Render": "action", "Pw-Build": "test-build"})

	if control := headerValue(header, "Cache-Control"); control != updateCacheControl {
		t.Errorf("action Cache-Control = %q, want %q\n%s", control, updateCacheControl, header)
	}
	assertVary(t, "action", header, "Pw-Render", "Pw-Build")
}

// A navigation instruction is an action response by another name and takes the
// same policy.
func TestANavigateResponseIsNeverStored(t *testing.T) {
	withUpdateSettings(t)
	_, header, _ := serveWith(t, func(r *fasthttp.RequestCtx) {
		WriteUpdateNavigate(r, "/orders/9")
	}, map[string]string{"Pw-Render": "action", "Pw-Build": "test-build"})

	if control := headerValue(header, "Cache-Control"); control != updateCacheControl {
		t.Errorf("navigate Cache-Control = %q, want %q\n%s", control, updateCacheControl, header)
	}
	assertVary(t, "navigate", header, "Pw-Render", "Pw-Build")
}

// A redraw renders per-user content, so it stays out of every shared cache —
// and it is no-cache rather than no-store because no-store would forbid the
// conditional request its entity tag exists for.
func TestARedrawIsPrivateAndRevalidates(t *testing.T) {
	withUpdateSettings(t)
	handler := func(r *fasthttp.RequestCtx) {
		if !RedrawComponents(r, cardComponent("fixture.card.Card")) {
			t.Error("a redraw request was not answered")
		}
	}
	status, header, _ := serveRaw(t, handler, "/orders?page=2",
		headerLines(redrawHeaders("fixture.card.Card", "card-1")))
	if status != fasthttp.StatusOK {
		t.Fatalf("status = %d\n%s", status, header)
	}
	if control := headerValue(header, "Cache-Control"); control != redrawCacheControl {
		t.Errorf("redraw Cache-Control = %q, want %q\n%s", control, redrawCacheControl, header)
	}
	// The two shared axes are this framework's; the narrower two come from the
	// module, which is what knows a redraw is addressed by kind and instance.
	assertVary(t, "redraw", header, "Pw-Render", "Pw-Build", "Pw-Kind", "Pw-Instance")
	etag := headerValue(header, "ETag")
	if etag == "" {
		t.Fatalf("a redraw carried no entity tag\n%s", header)
	}

	// And the answer to a client that already holds those bytes is this
	// framework's too, because a 304 is a cache decision and the module stopped
	// making them.
	conditional := redrawHeaders("fixture.card.Card", "card-1")
	conditional["If-None-Match"] = etag
	status, header, body := serveRaw(t, handler, "/orders?page=2", headerLines(conditional))
	if status != fasthttp.StatusNotModified {
		t.Errorf("a redraw whose bytes the client holds = %d, want 304\n%s", status, header)
	}
	if body != "" {
		t.Errorf("a 304 carried a body: %s", body)
	}
}

// The document answers from the page's URL, exactly as its deltas and redraws
// do, so it has to name what told it apart from them. This is the axis whose
// absence was found in a browser on the other transport: a cache that stored
// the page under the URL alone answered every later request with it, which is
// not a degraded page but no page at all until the entry expires.
func TestADocumentNamesTheAxesThatTellItFromADelta(t *testing.T) {
	withUpdateSettings(t)
	_, header, _ := serveRaw(t, func(r *fasthttp.RequestCtx) {
		WriteHTMLChain(r, nil, staticFragment(`<main>orders</main>`))
	}, "/orders", "")

	assertVary(t, "document", header, "Pw-Render", "Pw-Build")
}

// A project that never enabled updates has no delta to be told apart from, and
// an axis naming a header nothing sends would split its cache for nothing.
func TestADocumentNamesNoUpdateAxisWithoutUpdates(t *testing.T) {
	previous, had := pwruntime.ResolvedUpdateSettings()
	pwruntime.PublishUpdateSettings(pwruntime.UpdateSettings{})
	t.Cleanup(func() {
		if had {
			pwruntime.PublishUpdateSettings(previous)
			return
		}
		pwruntime.PublishUpdateSettings(pwruntime.UpdateSettings{})
	})

	_, header, _ := serveRaw(t, func(r *fasthttp.RequestCtx) {
		WriteHTMLChain(r, nil, staticFragment(`<main>orders</main>`))
	}, "/orders", "")

	if axes := varyAxes(header); axes["pw-render"] || axes["pw-build"] {
		t.Errorf("a document of a project without updates names an update axis:\n%s", header)
	}
}

// An axis the module named and one this framework names are one axis. Repeating
// it would not be wrong for a cache, but it says the two writers do not know
// about each other, which is how one of them starts clobbering the other.
func TestTheSharedVaryAxesAreNamedOnce(t *testing.T) {
	withUpdateSettings(t)
	_, header, _ := serveRaw(t, func(r *fasthttp.RequestCtx) {
		if !RedrawComponents(r, cardComponent("fixture.card.Card")) {
			t.Error("a redraw request was not answered")
		}
	}, "/orders?page=2", headerLines(redrawHeaders("fixture.card.Card", "card-1")))

	counted := map[string]int{}
	for _, line := range strings.Split(header, "\r\n") {
		field, value, found := strings.Cut(line, ":")
		if !found || !strings.EqualFold(strings.TrimSpace(field), "Vary") {
			continue
		}
		for _, axis := range strings.Split(value, ",") {
			if axis = strings.TrimSpace(axis); axis != "" {
				counted[strings.ToLower(axis)]++
			}
		}
	}
	for axis, times := range counted {
		if times > 1 {
			t.Errorf("Vary names %s %d times:\n%s", axis, times, header)
		}
	}
}

// headerLines renders a header map in the wire form serveRaw appends.
func headerLines(headers map[string]string) string {
	var out strings.Builder
	for name, value := range headers {
		out.WriteString(name + ": " + value + "\r\n")
	}
	return out.String()
}
