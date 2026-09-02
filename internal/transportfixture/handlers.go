// Package transportfixture is authored handler code written the way an
// application writes it, so the transport analysis has something in this
// repository to run against.
//
// It exists because nothing else here is a valid input. Generated files are
// emitted per backend rather than rewritten, and the examples are each their own
// module whose query and template packages exist only after pw generate. This
// package compiles with the rest of the tree, so a pw entry that loses its call
// pattern is caught by the build and by the analysis together.
//
// Every handler below is deliberately ordinary. The point is not to exercise
// unusual shapes but to hold the calls an application actually makes, so that
// the set of registered patterns is checked against the set that gets used.
package transportfixture

import (
	"net/http"

	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// Greeting is the response type an API handler writes.
type Greeting struct {
	Message string `json:"message"`
}

// APIHandler binds a request and answers with a typed value, which is the
// shortest path through the framework an application takes.
func APIHandler(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[struct{}](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	_ = input
	pw.WriteAPI(w, r, Greeting{Message: "hello"})
}

// PageHandler renders a page inside the registered document shell and reports a
// problem the same way every other handler does.
func PageHandler(w http.ResponseWriter, r *http.Request) {
	if pw.IsBot(r) {
		pw.WriteHTMLFragment(w, r, fragment("<p>crawler</p>"))
		return
	}
	pw.WriteHTML(w, r, fragment("<main>page</main>"))
}

// ActionHandler is the shape requirement:action-response-update describes: one
// branch, the regions on one side and the ordinary response on the other.
func ActionHandler(w http.ResponseWriter, r *http.Request) {
	if pw.WantsUpdate(r) {
		pw.WriteUpdate(w, r, http.StatusOK, pw.Replace("total", fragment(`<b id="total">1</b>`)))
		return
	}
	pw.WriteUpdateNavigate(w, r, "/orders")
}

// StreamHandler writes a typed event stream through the callback entry.
func StreamHandler(w http.ResponseWriter, r *http.Request) {
	pw.WriteStream(w, r, func(stream *pw.Stream[Greeting]) error {
		return stream.Write(Greeting{Message: "tick"})
	})
}

// ClientMsg and ServerMsg are the two halves of a socket protocol: what the
// client sends and what the handler answers with. Each direction carries its
// variants in a discriminator field, which is how the stream above spells them
// too.
type ClientMsg struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ServerMsg struct {
	Type string `json:"type"`
	Room string `json:"room,omitempty"`
	From string `json:"from,omitempty"`
	Text string `json:"text,omitempty"`
}

// SocketHandler upgrades to a WebSocket and runs a protocol loop.
//
// Neither type argument is spelled at the call: discovery recovers both from the
// closure parameter and emits a decoder for the first and an encoder for the
// second.
//
// Everything the callback needs is read before the entry, because on the second
// transport the callback runs after this handler has returned and the request
// value belongs to whichever request occupies that slot next.
func SocketHandler(w http.ResponseWriter, r *http.Request) {
	room, _ := pw.QueryValue(r, "room")
	who := pw.RequestAuthentication(r)

	_ = pw.WebSocket(w, r, func(socket *pw.Socket[ClientMsg, ServerMsg]) error {
		for {
			in, err := socket.Read()
			if err != nil {
				return nil
			}
			if err := socket.Write(ServerMsg{
				Type: "echo",
				Room: room,
				From: who.Subject,
				Text: in.Text,
			}); err != nil {
				return err
			}
		}
	})
}

// SpecHandler serves the generated specification, which is a pw entry an
// application mounts rather than writes.
func SpecHandler(w http.ResponseWriter, r *http.Request) {
	pw.OpenAPIJSON(w, r)
}

// AccessorHandler reads the request-scoped surface a handler reads: the pool
// it queries, the span and the logger it reports through, and who is asking.
//
// Each of those takes the request, per policy:request-scoped-accessor-shape,
// and each is registered so that the argument collapses onto this transport's
// single value. The Context form appears here too, on the line after the span
// is opened: below the handler, and inside it once a child span has produced a
// context, the pair's other half is what reads.
func AccessorHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := pw.DB(r); !ok {
		pw.WriteProblem(w, r, pw.InternalServerError(http.ErrNoCookie))
		return
	}
	ctx, span := pw.StartSpan(r, "accessors")
	defer span.End()
	pw.LoggerContext(ctx).Info("serving", pw.String("trace", pw.TraceID(r)))

	if pw.Authenticated(r) {
		pw.WriteAPI(w, r, Greeting{Message: pw.RequestAuthentication(r).Subject})
		return
	}
	pw.WriteAPI(w, r, Greeting{Message: "anonymous"})
}

// renderError is the shared helper the eligibility rule exists to carry: it
// takes the transport and is called from a handler, so a transform that refused
// it would refuse everything that calls it.
func renderError(w http.ResponseWriter, r *http.Request, err error) {
	pw.WriteProblem(w, r, pw.InternalServerError(err))
}

// HelperCallingHandler is why the admission set closes over the call graph.
func HelperCallingHandler(w http.ResponseWriter, r *http.Request) {
	renderError(w, r, http.ErrNoCookie)
}

func fragment(markup string) pw.HTMLFragment {
	builder := htmlbind.Builder[struct{}]{}
	return (&htmlbind.Plan[struct{}]{
		Ops: []htmlbind.Op[struct{}]{builder.Static(markup)},
	}).Bind(struct{}{})
}
