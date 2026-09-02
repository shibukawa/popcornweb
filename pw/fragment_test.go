package pw

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	kzstd "github.com/klauspost/compress/zstd"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// staticFragment is the shape generation emits for a component with neither an
// await block nor a head element.
func staticFragment(markup string) HTMLFragment {
	builder := htmlbind.Builder[struct{}]{}
	return (&htmlbind.Plan[struct{}]{
		Ops: []htmlbind.Op[struct{}]{builder.Static(markup)},
	}).Bind(struct{}{})
}

// styledFragment carries the contributions a scoped style block folds into the
// calling plan, which is what a fragment response has nowhere to put.
func styledFragment() HTMLFragment {
	builder := htmlbind.Builder[struct{}]{}
	return (&htmlbind.Plan[struct{}]{
		Head: []string{`<style>.box_dwu687{color:red}</style>`},
		Ops:  []htmlbind.Op[struct{}]{builder.Static(`<div class="box_dwu687">card</div>`)},
	}).Bind(struct{}{})
}

func TestWriteHTMLFragmentWritesTheTemplateAlone(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLFragment(recorder, httptest.NewRequest(http.MethodGet, "/rows/7", nil),
		staticFragment(`<tr><td>7</td></tr>`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	// Byte equality is the requirement itself: no shell, no merged head, no
	// wrapper, and nothing framed around the markup.
	if body := recorder.Body.String(); body != `<tr><td>7</td></tr>` {
		t.Fatalf("body = %q", body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("content type = %q", got)
	}
	if recorder.Header().Get("Content-Length") == "" {
		t.Error("a buffered fragment should declare a Content-Length")
	}
	// One branch means one representation, so nothing classified this client.
	if got := recorder.Header().Values("Vary"); len(got) != 0 {
		t.Errorf("Vary = %v, want a response that varies on nothing", got)
	}
}

// TestWriteHTMLFragmentRejectsHeadContributions covers the failure that would
// otherwise be silent: the style block is scoped into the document head, so a
// dropped contribution swaps in an unstyled region with nothing in any log.
func TestWriteHTMLFragmentRejectsHeadContributions(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLFragment(recorder, httptest.NewRequest(http.MethodGet, "/card", nil), styledFragment())

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want the rejection", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("content type = %q", got)
	}
	if strings.Contains(recorder.Body.String(), "box_dwu687") {
		t.Fatal("the fragment was rendered anyway")
	}
}

// TestWriteHTMLFragmentSettlesBoundariesInPlace is what makes this path safe
// without a client runtime: the blocking render writes the settled subtree and
// emits no placeholder, so nothing carries a boundary id into a document that
// may already hold one.
func TestWriteHTMLFragmentSettlesBoundariesInPlace(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLFragment(recorder, browserRequest("/panel"), asyncPage(asyncPageParams{Body: Resolved("ready")}))

	body := recorder.Body.String()
	if body != "<main><p>ready</p></main>" {
		t.Fatalf("body = %q", body)
	}
	if strings.Contains(body, "tb-boundary") || strings.Contains(body, "tb-apply") {
		t.Fatalf("a fragment must carry no placeholder or framing: %q", body)
	}
	if recorder.Header().Get("Content-Length") == "" {
		t.Error("an await-capable fragment is still buffered, so it declares a length")
	}
	if got := recorder.Header().Values("Vary"); len(got) != 0 {
		t.Errorf("Vary = %v, want no classification on this path", got)
	}
}

// TestWriteHTMLFragmentAnswersUnrecoveredBoundaryWithAProblem keeps a partial
// failure out of the swap target. Nothing is committed, so the status can still
// be the truth, and a swap library reads that status instead of inserting a body.
func TestWriteHTMLFragmentAnswersUnrecoveredBoundaryWithAProblem(t *testing.T) {
	previous := registeredHTMLErrorPage()
	t.Cleanup(func() { RegisterHTMLErrorPage(previous) })
	builder := htmlbind.Builder[Problem]{}
	RegisterHTMLErrorPage(func(p Problem) HTMLFragment {
		return (&htmlbind.Plan[Problem]{Ops: []htmlbind.Op[Problem]{
			builder.Static("<main data-error-page>failed</main>"),
		}}).Bind(p)
	})

	recorder := httptest.NewRecorder()
	WriteHTMLFragment(recorder, httptest.NewRequest(http.MethodGet, "/panel", nil),
		noRecoverPage(asyncPageParams{Body: Failed[string](errors.New("upstream is down"))}))

	body := recorder.Body.String()
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want a real error status", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("content type = %q", got)
	}
	// An error document swapped into a region would replace that region with a
	// whole page, so this path never reaches for the error page.
	if strings.Contains(body, "data-error-page") {
		t.Error("the HTML error page reached a fragment response")
	}
	if strings.Contains(body, "loading") {
		t.Error("a failed fragment must not ship its fallback")
	}
	if strings.Contains(body, "upstream is down") {
		t.Fatal("the raw Go error reached the client")
	}
}

// TestWriteHTMLFragmentBoundsAwaitWork covers the bound reaching this path,
// which needs the context deadline rather than the option: the option is read by
// the async coordinator, and a fragment render never builds one.
func TestWriteHTMLFragmentBoundsAwaitWork(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panel", nil)
	request = request.WithContext(withTestHTMLConfig(request.Context(),
		HTMLConfig{Streaming: true, AsyncTimeout: 20 * time.Millisecond}))

	slow := Go(request.Context(), func(ctx context.Context) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
			return "late", nil
		}
	})
	WriteHTMLFragment(recorder, request, asyncPage(asyncPageParams{Body: slow}))

	if body := recorder.Body.String(); !strings.Contains(body, `<p class=failed>timeout</p>`) {
		t.Fatalf("the render was not bounded: %q", body)
	}
}

// TestWriteHTMLFragmentRejectsAnAbsentTemplate covers the zero value a handler
// can reach by returning a fragment it never built.
func TestWriteHTMLFragmentRejectsAnAbsentTemplate(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTMLFragment(recorder, httptest.NewRequest(http.MethodGet, "/panel", nil), HTMLFragment{})

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("content type = %q", got)
	}
}

// TestWriteHTMLFragmentCompressesLikeTheBufferedBranch keeps one behaviour for
// both entry points, since a fragment is buffered exactly like a page that
// cannot stream.
func TestWriteHTMLFragmentCompressesLikeTheBufferedBranch(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/rows/7", nil)
	request.Header.Set("Accept-Encoding", "zstd")
	request = request.WithContext(pwruntime.WithResources(request.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{
			reflect.TypeFor[MiddlewareConfig](): MiddlewareConfig{Compression: true},
			reflect.TypeFor[HTMLConfig]():       HTMLConfig{Streaming: true},
		},
	}))
	recorder := httptest.NewRecorder()

	WriteHTMLFragment(recorder, request, staticFragment(`<tr><td>7</td></tr>`))

	if got := recorder.Header().Get("Content-Encoding"); got != "zstd" {
		t.Fatalf("Content-Encoding = %q", got)
	}
	if recorder.Header().Get("Content-Length") != "" {
		t.Error("an encoded body has no length to declare before it closes")
	}
	decoder, err := kzstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	decoded, err := decoder.DecodeAll(recorder.Body.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != `<tr><td>7</td></tr>` {
		t.Fatalf("decoded body = %q", decoded)
	}
}
