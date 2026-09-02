// Command benchserver answers the same three routes on either transport, so an
// external load generator can measure one against the other.
//
// It exists because the Go benchmarks beside it run the client in the same
// process as the server, on the same cores. That is fine for comparing two
// servers — the client is identical in both rows, so it cancels — but it cannot
// answer "how many requests per second", because the number it produces is the
// client and the server together. Moving the client out of the process is what
// this is for.
//
// The routing is a switch rather than a mux on both sides. A real deployment
// routes, but the two routers are different code with different costs, and
// including them would mean reporting a difference that is partly the router's.
//
//	go run ./internal/transportbench/benchserver -backend=nethttp -addr=127.0.0.1:8081
//	go run ./internal/transportbench/benchserver -backend=fasthttp -addr=127.0.0.1:8082
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/pwfast"
	"github.com/shibukawa/popcornweb/pwruntime"
	httpbind "github.com/shibukawa/tinybind-go"
	"github.com/shibukawa/tinybind-go/fasthttpbind"
	"github.com/shibukawa/tinybind-go/htmlbind"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// payload, its writers and the wait are the ones the Go benchmarks use, so the
// two sets of numbers describe the same work.
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

const databaseWait = time.Millisecond

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

func main() {
	backend := flag.String("backend", "nethttp", "which transport to serve with: nethttp or fasthttp")
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	flag.Parse()

	pwruntime.SwapHTMLDocument([]htmlbind.Wrapper{
		documentWrapper(`<!doctype html><html><head><title>bench</title></head><body>`, `</body></html>`),
	})
	leaf := staticFragment(`<main><h1>Popcorn Web</h1><p>A page with a shell around it.</p></main>`)

	switch *backend {
	case "nethttp":
		log.Printf("net/http on %s", *addr)
		log.Fatal(http.ListenAndServe(*addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api":
				pw.WriteAPI(w, r, sample)
			case "/html":
				pw.WriteHTML(w, r, leaf)
			case "/db":
				time.Sleep(databaseWait)
				pw.WriteAPI(w, r, sample)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})))
	case "fasthttp":
		log.Printf("fasthttp on %s", *addr)
		log.Fatal(fasthttp.ListenAndServe(*addr, func(r *fasthttp.RequestCtx) {
			switch string(r.Path()) {
			case "/api":
				pwfast.WriteAPI(r, sample)
			case "/html":
				pwfast.WriteHTML(r, leaf)
			case "/db":
				time.Sleep(databaseWait)
				pwfast.WriteAPI(r, sample)
			default:
				r.SetStatusCode(http.StatusNotFound)
			}
		}))
	default:
		log.Fatalf("unknown backend %q", *backend)
	}
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
