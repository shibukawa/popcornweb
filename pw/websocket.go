package pw

import (
	"net/http"

	"github.com/shibukawa/popcornweb/pwruntime"
	tinybind "github.com/shibukawa/tinybind-go/httpbind"
)

// Socket is an open typed WebSocket connection. It is the module's own type
// rather than a wrapper around it, for the reason Stream is: the value a
// callback receives here is the value it receives on the other transport
// runtime, so a handler body is the same text on both.
//
// Read decodes one message into In and Write encodes one from Out, each through
// a generated codec. Read must be called from one goroutine; Write may be called
// from any, which is what makes a broadcast goroutine safe.
type Socket[In, Out any] = tinybind.Socket[In, Out]

// SocketOptions configures one socket: the read limit, the idle and write
// deadlines, the ping cadence, the offered subprotocols, and the origin check.
// A zero field takes the process default, and a process default left unset takes
// a concrete value — nothing reaches the connection as zero, because an unset
// read deadline is a read nothing can interrupt.
type SocketOptions = tinybind.SocketOptions

// SetSocketDefaults installs the process-wide socket options. It is shared with
// the other transport runtime, so installing them once covers both.
//
// An origin check installed here replaces this framework's own resolution, on
// both runtimes and for every socket. Leave it unset to keep the resolution
// described on WebSocket.
func SetSocketDefaults(opts SocketOptions) {
	pwruntime.SetSocketOriginPolicy(opts.CheckOrigin)
	tinybind.SetSocketDefaults(opts)
}

// SocketDefaults returns the effective process defaults, with every unset field
// resolved to the value it will actually be served with.
func SocketDefaults() SocketOptions { return tinybind.SocketDefaults() }

// WebSocket upgrades the request to a WebSocket, runs fn against a typed
// socket, and closes the socket when fn returns.
//
// The two structs are the protocol: In is what the client sends and Out is what
// this handler answers with, and generation emits a decoder for the first and an
// encoder for the second from the types named in the callback. Neither has to be
// spelled at the call.
//
//	_ = pw.WebSocket(w, r, func(s *pw.Socket[ClientMsg, ServerMsg]) error {
//		for {
//			in, err := s.Read()
//			if err != nil {
//				return nil
//			}
//			if err := s.Write(ServerMsg{Type: "echo", Text: in.Text}); err != nil {
//				return err
//			}
//		}
//	})
//
// The return value is the handshake error and nothing else. A non-nil value
// means the refusal response has already been written as a problem document, so
// the handler logs or counts it rather than answering. fn's own error is raised
// after the connection has been accepted, where no status is left to carry it,
// and reaches the handler installed with SetStreamErrorHandler instead — the
// same sink a stream failure takes, since the two have the same character.
//
// The callback must not read w or r. It runs before this returns here and after
// the handler has returned on the other transport runtime, where the request
// value belongs to whichever request occupies that slot next. Capture what it
// needs — the identity, the peer — before calling.
//
// A handshake from another origin is refused. The comparison is this
// deployment's own, reading the declared proxies and the declared trusted
// origins, so a socket behind a proxy is judged on the origin the browser
// actually used rather than on whatever the proxy put in Host. A request
// carrying no Origin at all is admitted: only a browser is required to send one,
// and refusing its absence would refuse every service and command-line client.
func WebSocket[In, Out any](w http.ResponseWriter, r *http.Request, fn func(*Socket[In, Out]) error) error {
	return WebSocketWith[In, Out](w, r, SocketOptions{}, fn)
}

// WebSocketWith is WebSocket with per-call options, for the endpoint whose
// limits, cadence or origin policy differ from the process defaults.
func WebSocketWith[In, Out any](w http.ResponseWriter, r *http.Request, opts SocketOptions, fn func(*Socket[In, Out]) error) error {
	if opts.CheckOrigin == nil && r != nil {
		// Resolved here rather than inside the callback the upgrader calls,
		// because that callback is handed two strings and the answer needs the
		// peer and the forwarding header as well.
		opts.CheckOrigin = pwruntime.SocketOriginCheck(pwruntime.SocketHandshake{
			TLS:            r.TLS != nil,
			Host:           r.Host,
			RemoteAddress:  r.RemoteAddr,
			ForwardedProto: r.Header.Get("X-Forwarded-Proto"),
		}, Development())
	}
	return tinybind.WebSocketWith[In, Out](w, r, opts, fn)
}
