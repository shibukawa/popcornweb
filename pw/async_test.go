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

// withTestHTMLConfig installs one html binding the way request middleware does,
// so a test can exercise a setting without parsing a configuration file.
func withTestHTMLConfig(ctx context.Context, config HTMLConfig) context.Context {
	return pwruntime.WithResources(ctx, pwruntime.Resources{
		Configs: map[reflect.Type]any{reflect.TypeFor[HTMLConfig](): config},
	})
}

// browserRequest is a request from a client that will run the boundary runtime.
// A streaming assertion needs one, because httptest.NewRequest sends no
// User-Agent and an absent header classifies as a bot, which is exactly the
// buffered branch these tests are not looking at.
func browserRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("User-Agent", chromeUserAgent)
	return request
}

const chromeUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

type asyncPageParams struct {
	Body Pending[string]
}

// asyncPage builds the shape generation emits for a component whose parameter is
// declared `async string` and read inside an await clause.
func asyncPage(params asyncPageParams) htmlbind.Fragment {
	outer := htmlbind.Builder[asyncPageParams]{}
	primary := htmlbind.Builder[string]{}
	recover := htmlbind.Builder[AsyncError]{}
	plan := &htmlbind.Plan[asyncPageParams]{
		HasAwaitBlock: true,
		Check: func(p asyncPageParams) error {
			if !p.Body.IsSet() {
				return htmlbind.ErrUnsetPending("body")
			}
			return nil
		},
		Ops: []htmlbind.Op[asyncPageParams]{
			outer.Static("<main>"),
			outer.Await(
				func(ctx context.Context, p asyncPageParams) (string, error) { return p.Body.Wait(ctx) },
				func(_ asyncPageParams, err AsyncError) AsyncError { return err },
				[]htmlbind.Op[string]{
					primary.Static("<p>"),
					primary.Text(func(value string) string { return value }),
					primary.Static("</p>"),
				},
				[]htmlbind.Op[asyncPageParams]{outer.Static("<p>loading</p>")},
				[]htmlbind.Op[AsyncError]{
					recover.Static("<p class=failed>"),
					recover.Text(func(err AsyncError) string { return err.Code }),
					recover.Static("</p>"),
				},
			),
			outer.Static("</main>"),
		},
	}
	return plan.Bind(params)
}

func TestWriteHTMLStreamsAwaitBoundaries(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, browserRequest("/"), asyncPage(asyncPageParams{Body: Resolved("ready")}))

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Header().Get("Content-Length") != "" {
		t.Error("a streamed response must not declare a Content-Length")
	}
	// The fallback commits first and the completion follows it, so a client that
	// stops reading early still has something to show.
	fallback := strings.Index(body, "<p>loading</p>")
	completion := strings.Index(body, `<template data-tb-boundary="tb-1"><p>ready</p></template><tb-apply for="tb-1"></tb-apply>`)
	if fallback < 0 || completion < 0 || completion < fallback {
		t.Fatalf("fallback then completion not found in order: %q", body)
	}
	// The fallback sits inside a pair of comment markers rather than inside a
	// wrapper element, and that is a correctness property rather than a
	// formatting one: an unknown element in table context is foster-parented out
	// of the tbody, which separates a wrapper from the rows it was wrapping and
	// leaves the fallback in the list forever. Comments are inserted where they
	// were written, in a table and everywhere else.
	if !strings.Contains(body, `<!--tb:tb-1--><p>loading</p><!--/tb:tb-1-->`) {
		t.Errorf("the fallback is not fenced by its markers: %q", body)
	}
	// And nothing writes the element the parser moves. The runtime finds a
	// boundary by walking to its markers, so a document still carrying the old
	// spelling would leave every await boundary showing its fallback forever.
	if strings.Contains(body, "<tb-boundary") {
		t.Errorf("a boundary was written as an element: %q", body)
	}
}

func TestWriteHTMLBuffersWithoutAwaitBlock(t *testing.T) {
	recorder := httptest.NewRecorder()
	builder := htmlbind.Builder[struct{}]{}
	page := (&htmlbind.Plan[struct{}]{Ops: []htmlbind.Op[struct{}]{
		builder.Static("<main>static</main>"),
	}}).Bind(struct{}{})

	WriteHTML(recorder, httptest.NewRequest(http.MethodGet, "/", nil), page)
	if recorder.Body.String() != "<main>static</main>" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
	if recorder.Header().Get("Content-Length") == "" {
		t.Error("a buffered response should still declare a Content-Length")
	}
}

// TestWriteHTMLRecoversFailedBoundary keeps a boundary failure inside the page:
// the status is already committed, so the recover subtree is the only place a
// failure can still be reported.
func TestWriteHTMLRecoversFailedBoundary(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, httptest.NewRequest(http.MethodGet, "/", nil),
		asyncPage(asyncPageParams{Body: Failed[string](errors.New("upstream is down"))}))

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want the already committed 200", recorder.Code)
	}
	if !strings.Contains(body, `<p class=failed>internal</p>`) {
		t.Fatalf("recover subtree missing: %q", body)
	}
	if strings.Contains(body, "upstream is down") {
		t.Fatal("the raw Go error reached the page")
	}
}

// TestWriteHTMLRejectsUnsetPendingBeforeCommit covers the one async failure that
// can still change the status, because it is detected before the initial pass.
func TestWriteHTMLRejectsUnsetPendingBeforeCommit(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, httptest.NewRequest(http.MethodGet, "/", nil), asyncPage(asyncPageParams{}))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content type = %q", got)
	}
}

func TestWriteHTMLBufferedBranchStillResolvesBoundaries(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(withTestHTMLConfig(request.Context(), HTMLConfig{Streaming: false}))

	WriteHTML(recorder, request, asyncPage(asyncPageParams{Body: Resolved("ready")}))
	if body := recorder.Body.String(); body != "<main><p>ready</p></main>" {
		t.Fatalf("body = %q", body)
	}
}

func TestAsyncTimeoutBoundsOneBoundary(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
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
	WriteHTML(recorder, request, asyncPage(asyncPageParams{Body: slow}))

	if !strings.Contains(recorder.Body.String(), `<p class=failed>timeout</p>`) {
		t.Fatalf("timeout was not reported: %q", recorder.Body.String())
	}
}

// TestAsyncTimeoutBoundsTheBufferedBranch covers the half of the bound that
// htmlbind.WithAsyncTimeout does not reach: the option is read by the async
// coordinator, so the blocking path needs the deadline on its context instead.
// Without it, a chain forced onto this branch waits until the request context
// ends, which is the stall the setting exists to prevent.
func TestAsyncTimeoutBoundsTheBufferedBranch(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := browserRequest("/")
	request = request.WithContext(withTestHTMLConfig(request.Context(),
		HTMLConfig{Streaming: false, AsyncTimeout: 20 * time.Millisecond}))

	slow := Go(request.Context(), func(ctx context.Context) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
			return "late", nil
		}
	})
	start := time.Now()
	WriteHTML(recorder, request, asyncPage(asyncPageParams{Body: slow}))

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the render waited %s, so the bound did not apply", elapsed)
	}
	if !strings.Contains(recorder.Body.String(), `<p class=failed>timeout</p>`) {
		t.Fatalf("timeout was not reported: %q", recorder.Body.String())
	}
}

func TestFrameworkScriptIsImmutableAndRevisioned(t *testing.T) {
	recorder := httptest.NewRecorder()
	url := RuntimeScriptURL()
	if !strings.HasPrefix(url, "/_pw/") || !strings.HasSuffix(url, "/popcornweb-runtime.js") {
		t.Fatalf("url = %q", url)
	}
	if !serveFrameworkScript(recorder, httptest.NewRequest(http.MethodGet, url, nil)) {
		t.Fatal("the runtime request was not handled")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("Cache-Control = %q", got)
	}
	if !strings.Contains(recorder.Body.String(), `customElements.define("tb-apply"`) {
		t.Error("the served script does not define the marker element")
	}
	// One asset, both halves: the boundary runtime and the update runtime, both
	// this framework's since the client ownership work. Two would mean two
	// boundary id spaces on one document.
	if !strings.Contains(recorder.Body.String(), "createUpdateRuntime") {
		t.Error("the served script does not carry the update runtime")
	}

	// A stale revision is not this handler's, because the prefix holds more than
	// the asset. It falls through to the one that closes the namespace, which is
	// what keeps it from reaching the application.
	if serveFrameworkScript(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/_pw/deadbeef/boundary.js", nil)) {
		t.Error("the script handler claimed a path that is not its own")
	}
	stale := httptest.NewRecorder()
	if !serveReservedPath(stale, httptest.NewRequest(http.MethodGet, "/_pw/deadbeef/boundary.js", nil)) {
		t.Fatal("a reserved path must not fall through to the application")
	}
	if stale.Code != http.StatusNotFound {
		t.Errorf("stale revision status = %d", stale.Code)
	}
	if serveReservedPath(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/orders", nil)) {
		t.Error("an application path was claimed by the reserved prefix")
	}
}

// TestStreamedResponseCompressesAndFlushes covers the interaction the encoder
// used to make impossible: the streaming branch negotiates zstd like any other
// HTML response, and one flush has to reach the encoder and the response
// writer, because zstd deliberately does not flush its destination.
func TestStreamedResponseCompressesAndFlushes(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Encoding", "zstd")
	request = request.WithContext(pwruntime.WithResources(request.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{
			reflect.TypeFor[MiddlewareConfig](): MiddlewareConfig{Compression: true},
			reflect.TypeFor[HTMLConfig]():       HTMLConfig{Streaming: true},
		},
	}))
	recorder := &flushCountingRecorder{ResponseRecorder: httptest.NewRecorder()}

	WriteHTML(recorder, request, asyncPage(asyncPageParams{Body: Resolved("ready")}))

	if got := recorder.Header().Get("Content-Encoding"); got != "zstd" {
		t.Fatalf("Content-Encoding = %q", got)
	}
	if recorder.Header().Get("Content-Length") != "" {
		t.Error("a streamed response must not declare a Content-Length")
	}
	// The initial pass flushes once and every completion flushes again, so a
	// single-boundary render reaching the client means at least two flushes made
	// it all the way through the encoder.
	if recorder.flushes < 2 {
		t.Errorf("flushes = %d, want the initial pass and the completion", recorder.flushes)
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
	if !strings.Contains(string(decoded), `<tb-apply for="tb-1"></tb-apply>`) {
		t.Fatalf("decoded body lost its framing: %q", decoded)
	}
}

// flushCountingRecorder stands in for the tracked ResponseWriter the middleware
// stack installs, which forwards Flush to the underlying http.Flusher.
type flushCountingRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (r *flushCountingRecorder) Flush() {
	r.flushes++
	r.ResponseRecorder.Flush()
}

// TestCompressedStreamRejectsUnsetPendingCleanly covers the seam between the two
// branches: the streaming branch has to choose an encoding before it knows the
// render succeeds, and a pre-commit failure answers with an unencoded problem
// body that the negotiated header would otherwise misdescribe.
func TestCompressedStreamRejectsUnsetPendingCleanly(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Encoding", "zstd")
	request = request.WithContext(pwruntime.WithResources(request.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{
			reflect.TypeFor[MiddlewareConfig](): MiddlewareConfig{Compression: true},
			reflect.TypeFor[HTMLConfig]():       HTMLConfig{Streaming: true},
		},
	}))
	recorder := httptest.NewRecorder()

	WriteHTML(recorder, request, asyncPage(asyncPageParams{}))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, but the problem body is not encoded", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("content type = %q", got)
	}
	if !strings.HasPrefix(strings.TrimSpace(recorder.Body.String()), "{") {
		t.Errorf("body is not the plain problem document: %q", recorder.Body.String())
	}
}

// noRecoverPage is the same shape as asyncPage with the recover clause omitted,
// which is what an author writes when they say nothing about failure.
func noRecoverPage(params asyncPageParams) htmlbind.Fragment {
	outer := htmlbind.Builder[asyncPageParams]{}
	primary := htmlbind.Builder[string]{}
	plan := &htmlbind.Plan[asyncPageParams]{
		HasAwaitBlock: true,
		Ops: []htmlbind.Op[asyncPageParams]{
			outer.Static("<main>"),
			outer.Await(
				func(ctx context.Context, p asyncPageParams) (string, error) { return p.Body.Wait(ctx) },
				func(_ asyncPageParams, err AsyncError) AsyncError { return err },
				[]htmlbind.Op[string]{primary.Text(func(value string) string { return value })},
				[]htmlbind.Op[asyncPageParams]{outer.Static("<p>loading</p>")},
				nil,
			),
			outer.Static("</main>"),
		},
	}
	return plan.Bind(params)
}

// TestStreamedUnrecoveredBoundaryReplacesDocument is the point of the whole
// escalation: an author who wrote no recover clause never asked for a page that
// claims to be loading forever.
func TestStreamedUnrecoveredBoundaryReplacesDocument(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, browserRequest("/"),
		noRecoverPage(asyncPageParams{Body: Failed[string](errors.New("upstream is down"))}))

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want the already committed 200", recorder.Code)
	}
	if !strings.Contains(body, `<template data-tb-document>`) ||
		!strings.Contains(body, `</template><tb-apply-document></tb-apply-document>`) {
		t.Fatalf("document envelope missing: %q", body)
	}
	if !strings.Contains(body, "500 Internal Server Error") {
		t.Errorf("error page missing: %q", body)
	}
	if strings.Contains(body, "upstream is down") {
		t.Fatal("the raw Go error reached the page")
	}
	// The fallback is still on the wire, because it was committed before the
	// failure was known. The runtime is what removes it.
	if !strings.Contains(body, "<p>loading</p>") {
		t.Error("the committed fallback should still be present in the byte stream")
	}
}

// TestBufferedUnrecoveredBoundaryKeepsItsStatus is the asymmetry worth having:
// nothing is committed on this branch, so the response can still say 500.
func TestBufferedUnrecoveredBoundaryKeepsItsStatus(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(withTestHTMLConfig(request.Context(), HTMLConfig{Streaming: false}))
	recorder := httptest.NewRecorder()

	WriteHTML(recorder, request, noRecoverPage(asyncPageParams{Body: Failed[string](errors.New("upstream is down"))}))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want a real error status", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("content type = %q", got)
	}
	if strings.Contains(recorder.Body.String(), "upstream is down") {
		t.Fatal("the raw Go error reached the client")
	}
	if strings.Contains(recorder.Body.String(), "loading") {
		t.Error("a failed buffered render must not ship its fallback")
	}
}

// TestBufferedUnrecoveredBoundaryRendersTheErrorPage keeps the two branches
// telling one story: this one shows the same page the streaming branch patches
// in, and can still say 500 while doing it.
func TestBufferedUnrecoveredBoundaryRendersTheErrorPage(t *testing.T) {
	builder := htmlbind.Builder[Problem]{}
	RegisterHTMLErrorPage(func(p Problem) HTMLFragment {
		return (&htmlbind.Plan[Problem]{Ops: []htmlbind.Op[Problem]{
			builder.Static("<section id=app-error>"),
			builder.Text(func(p Problem) string { return p.Title }),
			builder.Static("</section>"),
		}}).Bind(p)
	})
	t.Cleanup(func() { RegisterHTMLErrorPage(nil) })

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(withTestHTMLConfig(request.Context(), HTMLConfig{Streaming: false}))
	recorder := httptest.NewRecorder()

	WriteHTML(recorder, request, noRecoverPage(asyncPageParams{Body: Failed[string](errors.New("upstream is down"))}))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want a real error status", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("content type = %q", got)
	}
	if !strings.Contains(recorder.Body.String(), `<section id=app-error>Internal Server Error</section>`) {
		t.Fatalf("error page missing: %q", recorder.Body.String())
	}
}

// A streamed unrecovered boundary must hand the application error page the
// sanitized problem, never the raw internal cause. The buffered and fasthttp
// paths already reduce it; this holds the streaming path to the same rule. The
// default scaffolded page renders Message, so a page that renders Message here
// must not surface the driver or Go error text.
func TestStreamedErrorPageNeverLeaksTheCause(t *testing.T) {
	builder := htmlbind.Builder[Problem]{}
	RegisterHTMLErrorPage(func(p Problem) HTMLFragment {
		return (&htmlbind.Plan[Problem]{Ops: []htmlbind.Op[Problem]{
			builder.Static("<section id=app-error>"),
			builder.Text(func(p Problem) string { return p.Message }),
			builder.Static("</section>"),
		}}).Bind(p)
	})
	t.Cleanup(func() { RegisterHTMLErrorPage(nil) })

	recorder := httptest.NewRecorder()
	WriteHTML(recorder, browserRequest("/"),
		noRecoverPage(asyncPageParams{Body: Failed[string](errors.New("secret-hostname db failure"))}))

	body := recorder.Body.String()
	if !strings.Contains(body, "<section id=app-error>") {
		t.Fatalf("application error page was not used: %q", body)
	}
	if strings.Contains(body, "secret-hostname db failure") {
		t.Fatalf("the raw internal cause reached the streamed error page: %q", body)
	}
}

// TestRegisteredErrorPageReplacesTheBuiltin checks the hook an application uses
// to supply its own generated error template.
func TestRegisteredErrorPageReplacesTheBuiltin(t *testing.T) {
	builder := htmlbind.Builder[Problem]{}
	RegisterHTMLErrorPage(func(p Problem) HTMLFragment {
		return (&htmlbind.Plan[Problem]{Ops: []htmlbind.Op[Problem]{
			builder.Static("<section id=app-error>"),
			builder.Text(func(p Problem) string { return p.Title }),
			builder.Static("</section>"),
		}}).Bind(p)
	})
	t.Cleanup(func() { RegisterHTMLErrorPage(nil) })

	recorder := httptest.NewRecorder()
	WriteHTML(recorder, httptest.NewRequest(http.MethodGet, "/", nil),
		noRecoverPage(asyncPageParams{Body: Failed[string](errors.New("upstream is down"))}))

	if !strings.Contains(recorder.Body.String(), `<section id=app-error>Internal Server Error</section>`) {
		t.Fatalf("registered page not used: %q", recorder.Body.String())
	}
}
