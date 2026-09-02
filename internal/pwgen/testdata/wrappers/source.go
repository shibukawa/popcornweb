package wrappers

import (
	"context"
	"net/http"

	"github.com/shibukawa/popcornweb/pw"
)

type request struct {
	ID int `path:"id"`
}

type response struct {
	ID int `json:"id"`
}

type AppConfig struct {
	Name string `default:"demo"`
}

type ImportCommand struct {
	Path string `arg:"required" help:"input path"`
}

type createRequest struct {
	Name string `json:"name"`
}

type created struct {
	ID int `json:"id"`
}

// itemSummary is the entity shape the cache is passed as-is: the marked fields
// are the query and the rest is the result, so a payload field added here does
// not change the key.
type itemSummary struct {
	ItemID string `cache:"key"`
	Page   int    `cache:"key"`
	Title  string
	Total  int
}

type inbound struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type outbound struct {
	Text string `json:"text"`
}

var mux = pw.NewServeMux()

func init() {
	pw.RegisterConfig[AppConfig]("app")
	pw.RegisterSubCommand[ImportCommand]("import", "import data")
	mux.HandleFunc("GET /items/{id}", item)
	mux.HandleFunc("POST /items", create)
	mux.HandleFunc("GET /socket", socket)
	mux.HandleFunc("GET /summary", summary)
}

func item(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[request](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	pw.WriteAPI(w, r, response{ID: input.ID})
}

func create(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[createRequest](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	_ = input
	pw.WriteStatus(w, r, http.StatusCreated, created{ID: 1})
}

// summary reaches the cache, which is what makes itemSummary a key type. The
// key is the argument beside the result rather than a type argument, so nothing
// here spells itemSummary in brackets, and the read is a method on the store
// the handler resolved.
func summary(w http.ResponseWriter, r *http.Request) {
	store, err := pw.MemoStore(r, "upstream")
	if err != nil {
		pw.WriteProblem(w, r, pw.InternalServerError(err))
		return
	}
	value, err := store.Get(r.Context(), itemSummary{ItemID: "a", Page: 1},
		func(ctx context.Context) (response, error) { return response{ID: 1}, nil })
	if err != nil {
		pw.WriteProblem(w, r, pw.InternalServerError(err))
		return
	}
	pw.WriteAPI(w, r, value)
}

// socket spells neither type argument, so what the generator emits for it comes
// from the closure parameter alone.
func socket(w http.ResponseWriter, r *http.Request) {
	_ = pw.WebSocket(w, r, func(s *pw.Socket[inbound, outbound]) error {
		in, err := s.Read()
		if err != nil {
			return err
		}
		return s.Write(outbound{Text: in.Text})
	})
}
