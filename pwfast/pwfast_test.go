package pwfast

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/fasthttpbind"
	"github.com/shibukawa/tinybind-go/htmlbind"
	"github.com/shibukawa/tinygodriver/fasthttp"
	"github.com/shibukawa/tinygodriver/fasthttp/fasthttputil"
)

// serve runs one request through a real fasthttp server over an in-memory
// connection.
//
// It goes through the server rather than calling a handler with a hand-built
// RequestCtx, because the properties worth testing here are the ones the
// transport decides: that the response commits, that the header the entry set
// survives serialization, and that a value written into the pooled context
// reaches the wire. A synthesized context would test the wrapper against this
// test's idea of the transport instead of the transport.
func serve(t *testing.T, handler fasthttp.RequestHandler, target string) (status int, header, body string) {
	t.Helper()
	return serveRaw(t, handler, target, "")
}

// serveRaw is serve with extra request headers, already CRLF-terminated.
func serveRaw(t *testing.T, handler fasthttp.RequestHandler, target, extraHeaders string) (status int, header, body string) {
	t.Helper()
	return serveRequest(t, handler, "GET", target, extraHeaders, "")
}

// serveForm posts one urlencoded body, which is the shape the form readers are
// for.
func serveForm(t *testing.T, handler fasthttp.RequestHandler, target, form string) (status int, header, body string) {
	t.Helper()
	return serveRequest(t, handler, "POST", target,
		"Content-Type: application/x-www-form-urlencoded\r\nContent-Length: "+strconv.Itoa(len(form))+"\r\n", form)
}

// serveRequest runs one request of any shape through a real fasthttp server.
func serveRequest(t *testing.T, handler fasthttp.RequestHandler, method, target, extraHeaders, requestBody string) (status int, header, body string) {
	t.Helper()
	listener := fasthttputil.NewInmemoryListener()
	server := &fasthttp.Server{Handler: handler}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("the fasthttp server did not shut down")
		}
	})

	conn, err := listener.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	request := method + " " + target + " HTTP/1.1\r\nHost: example.test\r\n" + extraHeaders + "Connection: close\r\n\r\n" + requestBody
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(conn)
	if err != nil && !isClosed(err) {
		t.Fatal(err)
	}
	response := fasthttp.Response{}
	// A HEAD response declares a length and carries no body, so the reader has
	// to be told not to wait for one.
	if method == "HEAD" {
		response.SkipBody = true
	}
	if err := response.Read(bufio.NewReader(strings.NewReader(string(raw)))); err != nil {
		t.Fatalf("unreadable response: %v\n%s", err, raw)
	}
	return response.StatusCode(), string(response.Header.Header()), string(response.Body())
}

func isClosed(err error) bool {
	return err == io.EOF || strings.Contains(err.Error(), "closed")
}

func staticFragment(markup string) HTMLFragment {
	builder := htmlbind.Builder[struct{}]{}
	return (&htmlbind.Plan[struct{}]{
		Ops: []htmlbind.Op[struct{}]{builder.Static(markup)},
	}).Bind(struct{}{})
}

func TestWriteHTMLChainRendersThroughTheTransport(t *testing.T) {
	status, header, body := serve(t, func(r *fasthttp.RequestCtx) {
		WriteHTMLChain(r, nil, staticFragment(`<main>hello</main>`))
	}, "/")

	if status != fasthttp.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body != `<main>hello</main>` {
		t.Errorf("body = %q", body)
	}
	// The media type is the one property a client branches on, and it is set on
	// the response rather than inferred, so it is worth asserting on the wire.
	if !strings.Contains(strings.ToLower(header), "content-type: text/html") {
		t.Errorf("header did not carry the HTML content type:\n%s", header)
	}
}

func TestWriteHTMLFragmentRendersWithoutADocument(t *testing.T) {
	_, _, body := serve(t, func(r *fasthttp.RequestCtx) {
		WriteHTMLFragment(r, staticFragment(`<li>row</li>`))
	}, "/")
	if body != `<li>row</li>` {
		t.Errorf("body = %q", body)
	}
}

// A render that fails must answer with a problem rather than a partial page,
// which is the whole reason the chain is buffered before anything commits.
func TestAFailedRenderAnswersAProblemRatherThanPartialMarkup(t *testing.T) {
	status, header, body := serve(t, func(r *fasthttp.RequestCtx) {
		// A chain with no leaf is refused by the renderer, which is the
		// cheapest genuine failure that reaches the error path.
		WriteHTMLChain(r, nil, HTMLFragment{})
	}, "/")

	if status == fasthttp.StatusOK {
		t.Errorf("a failed render answered 200: %s", body)
	}
	if strings.Contains(strings.ToLower(header), "text/html") {
		t.Errorf("a failed render committed the HTML content type:\n%s", header)
	}
	// The problem document is the module's, byte-compatible with the net/http
	// half; what matters here is that it is what arrived.
	if !strings.Contains(body, `"status"`) {
		t.Errorf("body is not a problem document: %q", body)
	}
}

func TestWriteAPIWritesTheTypedValue(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	// Write needs a registered writer for the type, the same as on the other
	// half; registering it here is what generated code does at init.
	fasthttpbind.RegisterWrite(func(r *fasthttp.RequestCtx, value payload) error {
		r.Response.Header.SetContentType("application/json")
		_, err := r.WriteString(`{"name":"` + value.Name + `"}`)
		return err
	})
	_, _, body := serve(t, func(r *fasthttp.RequestCtx) {
		WriteAPI(r, payload{Name: "popcorn"})
	}, "/")
	if body != `{"name":"popcorn"}` {
		t.Errorf("body = %q", body)
	}
}

// The document shell is registered once, and both runtimes must find the same
// registration: generated init calls whichever package it imports, so two
// registries would leave one build rendering pages with nothing around them.
func TestTheDocumentShellRegisteredThroughEitherRuntimeIsOneRegistration(t *testing.T) {
	shell := documentWrapper(`<!doctype html><body>`, `</body>`)
	previous := pwruntime.SwapHTMLDocument([]HTMLWrapper{shell})
	t.Cleanup(func() { pwruntime.SwapHTMLDocument(previous) })

	_, _, body := serve(t, func(r *fasthttp.RequestCtx) {
		WriteHTML(r, staticFragment(`<main>hello</main>`))
	}, "/")
	if body != `<!doctype html><body><main>hello</main></body>` {
		t.Errorf("WriteHTML did not render inside the registered shell: %q", body)
	}
}

// WriteHTMLPage puts the document outermost and the caller's layouts inside it,
// which is what generated page tree code depends on.
func TestWriteHTMLPageKeepsTheDocumentOutermost(t *testing.T) {
	previous := pwruntime.SwapHTMLDocument([]HTMLWrapper{documentWrapper(`<doc>`, `</doc>`)})
	t.Cleanup(func() { pwruntime.SwapHTMLDocument(previous) })

	_, _, body := serve(t, func(r *fasthttp.RequestCtx) {
		WriteHTMLPage(r, []HTMLWrapper{documentWrapper(`<layout>`, `</layout>`)}, staticFragment(`<p>leaf</p>`))
	}, "/")
	if body != `<doc><layout><p>leaf</p></layout></doc>` {
		t.Errorf("wrapper order is wrong: %q", body)
	}
}

// The problem value is one type across the two runtimes. An earlier draft
// aliased the module's two-field body under this name, and a handler building
// one with a status would not have compiled.
func TestTheProblemValueIsTheOneBothRuntimesShare(t *testing.T) {
	var shared pwruntime.Problem = NotFound("missing")
	if shared.Status != 404 || shared.Title != "Not Found" {
		t.Errorf("constructor did not build the application-facing problem: %+v", shared)
	}
	// Assigning in both directions is what proves it is an alias rather than a
	// second struct that happens to have the same fields.
	var back Problem = shared
	if back.Message != "missing" {
		t.Errorf("problem did not round trip: %+v", back)
	}
}

func TestRateLimitedCarriesCompatibilityHeaders(t *testing.T) {
	status, header, body := serve(t, func(r *fasthttp.RequestCtx) {
		WriteProblem(r, RateLimited(RateLimit{
			Limit: 25, Remaining: 0, Reset: time.Unix(1_800_000_000, 0), RetryAfter: 3 * time.Second,
		}, "slow down"))
	}, "/limited")

	if status != fasthttp.StatusTooManyRequests {
		t.Fatalf("status = %d", status)
	}
	for _, fragment := range []string{
		"Cache-Control: no-store", "Retry-After: 3", "X-Ratelimit-Limit: 25",
		"X-Ratelimit-Remaining: 0", "X-Ratelimit-Reset: 1800000000",
	} {
		if !strings.Contains(header, fragment) {
			t.Errorf("header does not contain %q:\n%s", fragment, header)
		}
	}
	if !strings.Contains(body, `"status":429`) {
		t.Fatalf("body = %q", body)
	}
}

// documentWrapper builds a shell that renders its children between two strings.
func documentWrapper(open, close string) HTMLWrapper {
	type params struct{ Children HTMLFragment }
	builder := htmlbind.Builder[params]{}
	plan := &htmlbind.Plan[params]{Ops: []htmlbind.Op[params]{
		builder.Static(open),
		builder.Slot(func(p params) htmlbind.Fragment { return p.Children }, nil),
		builder.Static(close),
	}}
	return plan.BindWrapper(params{}, func(p *params, children htmlbind.Fragment) {
		p.Children = children
	})
}
