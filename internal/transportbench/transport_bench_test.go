// Package transportbench measures what a reader actually gains by serving the
// same response on the other transport.
//
// It exists because the question it answers is one where an argued number is
// worth nothing. "fasthttp is faster" is true and useless; what a deployment
// needs to know is whether the difference survives contact with the work a real
// handler does, and that is a measurement rather than an opinion.
//
// # What is held constant
//
// One client drives both servers. It is fasthttp's, chosen because it is the
// cheaper of the two and so contributes less noise, but the reason for using a
// single one is that in production the client is a browser and does not change
// when the backend does. Holding it constant makes the difference between two
// rows the server's difference and nothing else.
//
// The in-memory rows go over fasthttputil's pipe, which is a net.Listener and
// therefore serves net/http unmodified. No sockets, no kernel: those rows are
// the HTTP stack alone, which is the largest the gap can possibly look. The TCP
// rows add loopback, which is the same cost on both sides and so shrinks the
// ratio without changing the absolute difference. Both are reported because the
// pair is the answer, and either one alone is misleading.
//
// # What to read
//
// ns/op on the in-memory rows is per-request latency of the framework and the
// HTTP stack. allocs/op is the number worth trusting most: it is deterministic
// and does not move when the machine is busy, which matters here because these
// numbers were taken on a shared machine.
package transportbench

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/pwfast"
	"github.com/shibukawa/popcornweb/pwruntime"
	httpbind "github.com/shibukawa/tinybind-go"
	"github.com/shibukawa/tinybind-go/fasthttpbind"
	"github.com/shibukawa/tinybind-go/htmlbind"
	"github.com/shibukawa/tinygodriver/fasthttp"
	"github.com/shibukawa/tinygodriver/fasthttp/fasthttputil"
)

// payload is the body both halves write. The two registered writers produce the
// same bytes through the same encoder, so what separates the rows is the
// transport rather than the serialization.
type payload struct {
	ID    int      `json:"id"`
	Name  string   `json:"name"`
	Email string   `json:"email"`
	Tags  []string `json:"tags"`
}

var sample = payload{
	ID:    4711,
	Name:  "Popcorn Web",
	Email: "hello@example.test",
	Tags:  []string{"framework", "go", "html"},
}

func init() {
	httpbind.RegisterWrite(func(w http.ResponseWriter, r *http.Request, value payload) error {
		body, err := json.Marshal(value)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write(body)
		return err
	})
	fasthttpbind.RegisterWrite(func(r *fasthttp.RequestCtx, value payload) error {
		body, err := json.Marshal(value)
		if err != nil {
			return err
		}
		r.Response.Header.SetContentType("application/json")
		_, err = r.Write(body)
		return err
	})
}

// databaseWait stands in for one query against a warm local database. It is a
// sleep rather than work because the part that dominates a query is waiting for
// an answer, and a sleep is the honest model of waiting.
const databaseWait = time.Millisecond

// BenchmarkAPI is the case with nothing else in it: bind nothing, write one
// small JSON document. Whatever separates the transports shows up here at its
// largest, because there is no application work to dilute it.
func BenchmarkAPI(b *testing.B) {
	run(b, memory,
		func(w http.ResponseWriter, r *http.Request) { pw.WriteAPI(w, r, sample) },
		func(r *fasthttp.RequestCtx) { pwfast.WriteAPI(r, sample) })
}

// BenchmarkTransportOnly writes the same document without going through the
// framework entry, and exists to keep the other rows honest.
//
// pw.WriteAPI negotiates a content encoding and wraps the body writer; pwfast
// does not, because that surface is not built there yet. So part of what
// BenchmarkAPI reports is a feature one side has and the other lacks rather
// than a cost of the transport. These rows call the module's writer directly on
// both sides, leaving only the server, and the difference between this pair and
// that one is the price of the missing feature rather than of net/http.
func BenchmarkTransportOnly(b *testing.B) {
	run(b, memory,
		func(w http.ResponseWriter, r *http.Request) { _ = httpbind.Write(w, r, sample) },
		func(r *fasthttp.RequestCtx) { _ = fasthttpbind.Write(r, sample) })
}

// BenchmarkAPIOverTCP is the same handler across a loopback socket, which is
// the floor every real deployment pays on both transports.
func BenchmarkAPIOverTCP(b *testing.B) {
	run(b, tcp,
		func(w http.ResponseWriter, r *http.Request) { pw.WriteAPI(w, r, sample) },
		func(r *fasthttp.RequestCtx) { pwfast.WriteAPI(r, sample) })
}

// BenchmarkAPIWithDatabaseWait is the case a deployment actually runs: one
// query's worth of waiting in front of the same response.
func BenchmarkAPIWithDatabaseWait(b *testing.B) {
	run(b, memory,
		func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(databaseWait)
			pw.WriteAPI(w, r, sample)
		},
		func(r *fasthttp.RequestCtx) {
			time.Sleep(databaseWait)
			pwfast.WriteAPI(r, sample)
		})
}

// BenchmarkHTMLPage renders a document shell around a leaf, which is the
// framework's own heaviest ordinary path and the one most Popcorn Web routes
// take.
func BenchmarkHTMLPage(b *testing.B) {
	previous := pwruntime.SwapHTMLDocument([]htmlbind.Wrapper{
		documentWrapper(`<!doctype html><html><head><title>bench</title></head><body>`, `</body></html>`),
	})
	b.Cleanup(func() { pwruntime.SwapHTMLDocument(previous) })

	leaf := staticFragment(`<main><h1>Popcorn Web</h1><p>A page with a shell around it.</p></main>`)
	run(b, memory,
		func(w http.ResponseWriter, r *http.Request) { pw.WriteHTML(w, r, leaf) },
		func(r *fasthttp.RequestCtx) { pwfast.WriteHTML(r, leaf) })
}

// run measures one workload on both transports, sequentially and then in
// parallel.
//
// Sequential ns/op is latency and parallel ns/op is throughput's reciprocal;
// they answer different questions and a backend choice usually turns on the
// second, so both are reported rather than one standing in for the other.
func run(b *testing.B, where network, netHTTP http.HandlerFunc, fastHTTP fasthttp.RequestHandler) {
	b.Run("nethttp", func(b *testing.B) {
		h := startNetHTTP(b, netHTTP, where)
		b.Run("sequential", h.sequential)
		b.Run("parallel", h.parallel)
	})
	b.Run("fasthttp", func(b *testing.B) {
		h := startFastHTTP(b, fastHTTP, where)
		b.Run("sequential", h.sequential)
		b.Run("parallel", h.parallel)
	})
}

// harness is a running server and the client that reaches it.
type harness struct {
	client *fasthttp.Client
	url    string
}

// sequential measures one request at a time, which is per-request latency.
func (h *harness) sequential(b *testing.B) {
	h.verify(b)
	request, response := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(request)
	defer fasthttp.ReleaseResponse(response)
	request.SetRequestURI(h.url)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := h.client.Do(request, response); err != nil {
			b.Fatal(err)
		}
	}
}

// parallel measures the server saturated, which is the number a capacity
// decision is made from.
func (h *harness) parallel(b *testing.B) {
	h.verify(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		request, response := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
		defer fasthttp.ReleaseRequest(request)
		defer fasthttp.ReleaseResponse(response)
		request.SetRequestURI(h.url)
		for pb.Next() {
			if err := h.client.Do(request, response); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

// verify sends one request and refuses to measure anything unless it succeeded.
//
// A handler that fails still answers, and a benchmark over the problem path
// would report a number for work nobody asked about. This is what stops a
// misconfigured row from looking like a fast one.
func (h *harness) verify(b *testing.B) {
	b.Helper()
	request, response := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(request)
	defer fasthttp.ReleaseResponse(response)
	request.SetRequestURI(h.url)
	if err := h.client.Do(request, response); err != nil {
		b.Fatal(err)
	}
	if status := response.StatusCode(); status != http.StatusOK {
		b.Fatalf("the handler answered %d, so this would measure the failure path: %s", status, response.Body())
	}
	if len(response.Body()) == 0 {
		b.Fatal("the handler answered an empty body")
	}
}

// network selects what the two ends talk over.
type network int

const (
	// memory is fasthttputil's pipe: the HTTP stacks with nothing under them.
	memory network = iota
	// tcp is a loopback socket, the cost both transports pay in production.
	tcp
)

func startNetHTTP(b *testing.B, handler http.Handler, where network) *harness {
	listener, url := listen(b, where)
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	b.Cleanup(func() { _ = server.Close() })
	return &harness{client: dialer(listener), url: url}
}

func startFastHTTP(b *testing.B, handler fasthttp.RequestHandler, where network) *harness {
	listener, url := listen(b, where)
	server := &fasthttp.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	b.Cleanup(func() { _ = server.Shutdown() })
	return &harness{client: dialer(listener), url: url}
}

func listen(b *testing.B, where network) (net.Listener, string) {
	b.Helper()
	if where == tcp {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			b.Fatal(err)
		}
		return listener, "http://" + listener.Addr().String() + "/"
	}
	return fasthttputil.NewInmemoryListener(), "http://memory.test/"
}

// dialer builds the one client both servers are driven by. Over the pipe it has
// to be told how to reach it; over TCP the address in the URL is enough.
func dialer(listener net.Listener) *fasthttp.Client {
	if pipe, ok := listener.(*fasthttputil.InmemoryListener); ok {
		return &fasthttp.Client{Dial: func(string) (net.Conn, error) { return pipe.Dial() }}
	}
	return &fasthttp.Client{}
}

func staticFragment(markup string) htmlbind.Fragment {
	builder := htmlbind.Builder[struct{}]{}
	return (&htmlbind.Plan[struct{}]{
		Ops: []htmlbind.Op[struct{}]{builder.Static(markup)},
	}).Bind(struct{}{})
}

func documentWrapper(open, close string) htmlbind.Wrapper {
	type params struct{ Children htmlbind.Fragment }
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
