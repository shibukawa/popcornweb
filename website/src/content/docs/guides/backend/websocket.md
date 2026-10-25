---
title: WebSocket
description: Declaring a two-way protocol as an inbound and an outbound struct, and letting generation write the encoding on both sides of the connection.
sidebar:
  order: 3
---

A [stream](/guides/frontend/streams/) goes one way. The handler sends, the
client reads, and the request that opened it is the only thing the client ever
said. That covers most of what looks like it needs a socket — a progress feed, a
token stream from a model, a tail of a log — and it should be your first choice
every time, because it keeps HTTP framing, survives proxies that know nothing
about upgrades, and answers a failure with an ordinary problem response.

Reach for a WebSocket when the client has to keep talking: a chat room, a
collaborative document, a control channel where the browser subscribes and then
changes its subscription. That is the one thing a stream cannot do.

`pw.WebSocket[In, Out]` upgrades the request and hands the connection to a
callback:

```go
package handlers

import (
	"net/http"

	"github.com/shibukawa/popcornweb/pw"
)

// The protocol, as two structs. Each direction carries its variants in a
// discriminator field, which is how a stream spells them too.
type ClientMsg struct {
	Type string `json:"type"` // "join" | "say"
	Name string `json:"name"`
	Text string `json:"text"`
}

type ServerMsg struct {
	Type string `json:"type"` // "welcome" | "message" | "error"
	From string `json:"from"`
	Text string `json:"text"`
	Code string `json:"code"`
}

func Chat(w http.ResponseWriter, r *http.Request) {
	// Read anything you need from the request here, before the entry.
	room, _ := pw.QueryValue(r, "room")

	if err := pw.WebSocket(w, r, func(s *pw.Socket[ClientMsg, ServerMsg]) error {
		for {
			in, err := s.Read()
			if err != nil {
				return nil // the peer went away, or went quiet past the timeout
			}
			switch in.Type {
			case "say":
				err = s.Write(ServerMsg{Type: "message", From: room, Text: in.Text})
			default:
				err = s.Write(ServerMsg{Type: "error", Code: "unknown_type"})
			}
			if err != nil {
				return err
			}
		}
	}); err != nil {
		// The refusal has already been sent. This is for your log.
		pw.Logger(r).Warn("upgrade refused", pw.Err(err))
	}
}
```

Neither type argument is spelled at the call. Generation recovers `ClientMsg`
and `ServerMsg` from the closure parameter and writes a decoder for the first
and an encoder for the second into `_pw_gen.go`, which is build output nobody
edits. `Read` returns a `ClientMsg`; `Write` takes a `ServerMsg`; no `[]byte`
and no `encoding/json` appear anywhere in your handler.

A message type with no `omitempty` on it writes every field, so a client reads
the discriminator and ignores what that variant does not use. Tag the fields a
variant leaves empty and they stop being sent, which is the same encoder rule
[Responses](/guides/frontend/responses/#json) describes. Leaving the tags off is
the better default for a protocol like this one: a client that can count on the
field always being present needs no absent case, and the bytes a discriminated
union saves by omitting three empty strings are not the ones worth saving.

That generation step is not optional. A socket whose types were never discovered
compiles, opens, accepts the connection, and then fails on its first message —
so if a socket connects and immediately closes, run `pw generate` before looking
anywhere else.

## Who may call what

`Read` must be called from one goroutine. `Write` may be called from any: it
takes a lock the runtime's own control frames share, so a broadcast goroutine
writing to a hundred sockets cannot interleave a frame with a message. That is
the difference between this and holding a raw connection, where concurrent
writers corrupt the wire with no diagnostic on the server side.

Something has to keep reading, even in a handler that only pushes. Ping and
close frames are handled inside the read call, so a callback that writes on a
timer and never reads answers no ping and notices no close — the connection dies
at the first idle timeout with nothing to say why. A push-only handler still
runs a read loop and discards what it gets.

Returning from the callback is how you close. The runtime sends the close frame
and tears down the connection whichever way the callback ends, so a peer is never
left waiting on a close that a forgotten `defer` was supposed to send.

## What the callback may not touch

The callback must not read `w` or `r`. On the fasthttp build it runs *after* the
handler has returned, and the request value there belongs to whichever request
occupies that slot next — so a `r.Header.Get` inside the callback would read
somebody else's headers. Capture what you need first, as the example does with
`pw.QueryValue`.

`r.Context()` and everything reached through it — `pw.RequestAuthentication`,
your own context values — is fine to capture. `r.RemoteAddr` is not: it has no
spelling the fasthttp rewrite covers, so a handler that reads it is a handler the
second build refuses.

## Who is allowed to connect

An upgrade request is a `GET` carrying the session cookie, and it never reaches
the CSRF middleware, which guards unsafe methods. An upgrade accepted from
another site is therefore cross-site request forgery with a connection left open
behind it. So the framework checks the origin before the handshake, using the
same comparison the CSRF frame uses: the request's `Origin` must be this
deployment's own origin, or one named in `security.csrf.trusted_origins`.

Two consequences are worth knowing before you deploy.

**Behind a TLS-terminating proxy, declare it.** The comparison includes the
scheme, and nothing terminates TLS inside the application, so a deployment that
has not named its proxy resolves its own origin as `http://…` while the browser
reports `https://…` — and refuses every upgrade. This is the same declaration
the CSRF check already needs:

```toml
[server]
trusted_proxies = ["10.0.0.0/8"]
```

**A request with no `Origin` header is admitted.** Only a browser is required to
send one, so its absence means a service or a command-line client, and refusing
it would lock out every non-browser caller. What protects those connections is
authentication, which they have to carry anyway. A literal `null` origin — what
a sandboxed frame sends — is a browser, and is refused.

When an endpoint genuinely needs its own policy, `pw.WebSocketWith` takes one
per call, and it wins over the framework's:

```go
opts := pw.SocketOptions{
	CheckOrigin: func(origin, host string) bool { return origin == "https://partner.example" },
}
_ = pw.WebSocketWith(w, r, opts, chatLoop) // the same callback, named
```

## Limits and deadlines

Every connection carries a read limit, an idle deadline, a ping cadence, and a
write deadline, and none of them can be turned off — an unbounded read is a
goroutine and a connection nothing can reclaim. The defaults are chosen to match
what a `gorilla/websocket` application already ran with:

| Option | Default | What it bounds |
| --- | --- | --- |
| `ReadLimit` | 1 MiB | One inbound message, counted after decompression; a larger one closes the connection with 1009 |
| `IdleTimeout` | 60s | The read deadline, refreshed by each pong |
| `PingInterval` | 54s | How often the runtime pings; must be below `IdleTimeout` |
| `WriteTimeout` | 10s | One write, so a stalled peer cannot pin a writer |

Change them for the whole process at startup, or for one endpoint with
`pw.WebSocketWith`:

```go
pw.SetSocketDefaults(pw.SocketOptions{
	IdleTimeout:  90 * time.Second,
	PingInterval: 30 * time.Second,
})
```

A `PingInterval` at or above `IdleTimeout` is refused at the handshake rather
than served, because it would be a timer that only ever fires after the
connection was already declared dead.

## Where failures go

`pw.WebSocket` returns the handshake error and nothing else. A non-nil value
means the upgrade was refused and the [problem
response](/guides/frontend/responses/#errors) has already been written — your
handler logs it or counts it, and must not try to answer.

Everything after that has no status left to carry it. An error the callback
returns reaches the handler installed with `pw.SetStreamErrorHandler`, which is
the same sink a mid-stream failure uses; the name says stream because one
installer covers both. Install one, or a socket failing in production says
nothing at all:

```go
pw.SetStreamErrorHandler(func(err error) {
	slog.Error("socket", "error", err)
})
```

## The same source on both builds

A project with `fasthttp = true` builds this handler twice, and the callback body
is the same text both times — `pw.Socket` and `pwfast.Socket` are one type, and
the rewrite moves the import qualifier and nothing else. The lifetime rule above
is what buys that: it is written for the transport where the callback outlives
the handler, and it costs the net/http build nothing.

Under TinyGo, `pw.Run` serves through a listener that can hand a handler the
connection, because TinyGo's own `net/http` cannot complete an upgrade and would
hang the handshake with no error and no log line. That is the framework's job
rather than yours; there is no line to add.

## Reference

The full surface — `Socket.Subprotocol`, `Socket.Close`, every option field — is
in the [runtime reference](/reference/runtime/#writing-a-response). For
choosing between this and a one-way response, [Streams](/guides/frontend/streams/)
is the other half of that decision.
