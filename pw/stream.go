package pw

import (
	"net/http"
	"strings"

	tinybind "github.com/shibukawa/tinybind-go/httpbind"
)

// Stream is an open typed event stream. It is the module's own type rather
// than a wrapper around it, so the value a callback receives here is the value
// it receives on the other transport runtime, and a handler body is the same
// text on both.
//
// Write sends one value. Close is the runtime's, not the caller's: the entry
// below runs it whichever way the callback ends.
type Stream[T any] = tinybind.Stream[T]

// WriteStream negotiates a typed event stream, runs fn against it, and closes
// it.
//
// The callback is the shape because it is the only one both transports can
// express. A backend that streams by registering a body writer runs that writer
// after the handler has returned, so a stream the handler opened and held has
// nowhere to live there. Moving this surface to the shape that transport
// requires is what lets one handler serve both.
//
// It returns nothing, for the same reason. The callback runs after the handler
// returned on that backend, so an error cannot travel back to handler code;
// offering one here and not there would make the same source behave differently
// per transport, which is what the shared shape exists to prevent.
//
// A failure to negotiate happens before anything is committed and becomes an
// ordinary problem response. Once the stream is open the status has been sent,
// so a failure from the callback reaches SetStreamErrorHandler instead. Close
// runs either way, which is what keeps the trailing bracket of the JSON array
// framing from depending on a caller remembering to write it.
func WriteStream[T any](w http.ResponseWriter, r *http.Request, fn func(*Stream[T]) error) {
	SetRoute(w, r)
	// Checked here rather than left to the module, so an unacceptable Accept
	// reaches this framework's problem path and a browser gets the registered
	// error page instead of a bare problem document.
	if !supportedStreamAccept(r) {
		WriteProblem(w, r, Problem{
			Status:  http.StatusNotAcceptable,
			Title:   "Not Acceptable",
			Code:    "not_acceptable",
			Message: "unsupported streaming representation",
		})
		return
	}
	tinybind.WriteStream(w, r, fn)
}

// SetStreamErrorHandler installs the destination for a stream failure raised
// after the response status has been sent. Passing nil discards them, which is
// the default: a runtime that logged on its own would be writing somewhere the
// application did not choose.
//
// It is shared with the other transport runtime, so installing it once covers
// both.
func SetStreamErrorHandler(fn func(error)) { tinybind.SetStreamErrorHandler(fn) }

// supportedStreamAccept parses the header through the same allocation-free
// iterator negotiateResponseCoding uses, because this runs per streamed
// request and the split-then-lower version allocated three times to say yes.
func supportedStreamAccept(r *http.Request) bool {
	if r == nil {
		return true
	}
	accept := strings.TrimSpace(r.Header.Get("Accept"))
	if accept == "" || accept == "*/*" {
		return true
	}
	for item := range splitSeq(accept, ',') {
		media, _, _ := strings.Cut(item, ";")
		media = strings.TrimSpace(media)
		switch {
		case media == "*/*",
			strings.EqualFold(media, "text/event-stream"),
			strings.EqualFold(media, "application/x-ndjson"),
			strings.EqualFold(media, "application/ndjson"),
			strings.EqualFold(media, "application/json"),
			strings.EqualFold(media, "application/jsonl"):
			return true
		}
	}
	return false
}
