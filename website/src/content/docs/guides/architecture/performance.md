---
title: Performance
description: A scale for request costs and the settings to check before a production release.
sidebar:
  order: 6
---

Popcorn Web handles sessions, CSRF, security headers, request IDs, and other
request bookkeeping by default. That work does not make the framework inherently
slower: in the todo benchmark, Popcorn Web completed a request with less CPU time
than the hand-written `net/http` service.

That result can change with the application and the machine. Treat the numbers
below as a scale for deciding where to investigate, not as a performance guarantee.

## Build targets

One application, three ways to ship it. The default is host Go on `net/http`,
and it is the one to take unless something below names your situation.

`pw build --backend fasthttp` compiles the same source against fasthttp instead.
It is a second build rather than a mode: `pw generate` derives the handlers, the
binders and the route registration from the net/http source you wrote, build
tags select which half compiles, and the binary carries no `net/http` runtime at
all. TinyGo is the third, and it is chosen for size rather than speed.

| Build | net/http | fasthttp |
| --- | --- | --- |
| `go build` | 17.7 MiB | 17.1 MiB |
| `go build -ldflags="-s -w"` | 11.0 MiB | 10.6 MiB |
| `tinygo build` | 4.2 MiB | 5.6 MiB |
| `tinygo build -no-debug` | 4.2 MiB | 5.6 MiB |

Those are [`examples/helloworld`](https://github.com/shibukawa/popcornweb/tree/main/examples/helloworld)
on an Apple M3 with Go 1.27, and the subject matters: it embeds SQLite, which is
most of what you see. The transport accounts for about 0.6 MiB of the difference
between the two columns (0.35 MiB once stripped), so a smaller binary is not a
reason to switch. TinyGo is a reason: the WASI table below puts its module at a
third of the stripped host build.

The two TinyGo rows are older than the rest. They were taken with TinyGo 0.41 and
have not been repeated, because TinyGo 0.42 cannot yet link a native binary
against the macOS 27 SDK. The WASI table was repeated, and its `-no-debug` rows,
the comparable ones, have grown by about 30% since, so read the native figures as
a floor.

`-no-debug` earns nothing here, and the reason is macOS rather than TinyGo: the
linked Mach-O carries no DWARF at all — debug information stays in the object
files — so the flag has nothing to remove. Do not read that as a verdict on the
flag. See the WASI table below, where it is the largest lever on the page.

Read the bottom row against the one above it rather than across. Under host Go
fasthttp is marginally the smaller of the two; under TinyGo it is 1.4 MiB
*larger*. The reason is that the fasthttp build is additive rather than a
substitution: `net/http` is still linked, because the fork imports it, and on
top of that come brotli, zlib, the router, the websocket upgrader and a SOCKS
proxy dialer. Host Go's linker discards most of that and TinyGo's keeps more of
it.

So the two reasons to leave the default point in opposite directions on this
table, and taking both at once buys the smallest per-request cost at the largest
TinyGo binary.

TinyGo needs `-scheduler=threads` if the binary links a network database driver,
and until tinygodriver v1.2.4 it also could not link fasthttp at all — a zstd
decoder reached for arm64 assembly its linker does not resolve. Both are
[build tags](/reference/build-tags/) rather than anything this page decides.

### WASI, where `-no-debug` is the whole story

`tinygo build -target=wasip1` produces a module the same application runs from,
and there the flag is not a rounding error:

| `tinygo build -target=wasip1` | net/http | fasthttp |
| --- | --- | --- |
| plain | 10.3 MiB | 13.7 MiB |
| `-no-debug` | 3.8 MiB | 4.9 MiB |

A wasm module embeds its DWARF as custom sections, so dropping it takes 63% off
the net/http build and 65% off the fasthttp one. `wasip2` lands within 0.1 MiB of
each of these. `-target=wasm`, the browser one, does not build: tinygodriver's
`netdev` has no implementation for it, and no server target needs one.

Two things follow. **Always pass `-no-debug` for a WASI artifact** — it saves more
than the choice of backend and the choice of target put together, and at 3.8 MiB
the module is about a third of the stripped host build. And the fasthttp penalty
mostly evaporates: about 1 MiB rather than the 3.4 MiB the plain column suggests,
because most of what the fork adds is debug information about itself.

For where such a module is deployed, see
[Serverless](/guides/deployment/serverless/).

### What it costs at request time

The transports differ most where there is nothing else happening, and that is
also where the number matters least.

| Per request, 8 goroutines | net/http | fasthttp |
| --- | --- | --- |
| JSON response, no socket | 1.5 µs, 22 allocs | 0.7 µs, 3 allocs |
| HTML page, no socket | 2.4 µs, 42 allocs | 1.0 µs, 11 allocs |
| JSON response over loopback | 9.1 µs | 8.0 µs |
| The same, behind one 1 ms query | 150 µs | 150 µs |

Read the allocation counts rather than the times: they are deterministic and do
not move when the machine is busy. Three allocations against twenty-two is the
real difference, and it is what the pooled request value buys.

Read the last two rows for the decision. A loopback socket costs both sides the
same and takes the ratio from 2× to about 13%; one database query takes it to
noise. Serving the whole `helloworld` page — a template render and a SQLite
write — over one connection takes 261 µs on host Go with net/http and 256 µs with
fasthttp, which is one measurement of the same thing twice. TinyGo on net/http was
about 10% slower the last time it was measured, at 369 µs against 335 µs.

So switch for the allocation profile under a load that is genuinely
transport-bound, or for what TinyGo does to the image. Do not switch expecting a
page that talks to a database to get faster, and weigh it against what the second
build costs you: your handlers must sit in files a build tag can exclude, and
`pw generate` has to run for both halves.

## Cost by layer

These results come from
[`examples/todo`](https://github.com/shibukawa/popcornweb/tree/main/examples/todo)
under 20 concurrent clients. They report CPU time attributed to each request on
Go 1.27.0 and an Apple M3, with PostgreSQL 17 in Docker on the same machine.

| Per request | CPU time |
| --- | --- |
| Whole middleware chain | 3.3 µs |
| └ CSRF check | 2.4 µs |
| One `SELECT` returning 50 rows | 37 µs |
| Encode and write a JSON response | 31 µs |
| Render and write an HTML response | 88 µs |
| Whole request, Popcorn Web | 149 µs |
| Whole request, `net/http` comparison | 204 µs |

The JSON and HTML rows describe different responses, so the table is not a sum.
Its useful signal is the order of magnitude. The middleware chain takes a few
microseconds, while a simple query takes tens and HTML rendering takes more. A
database call across a real network may take hundreds of microseconds or several
milliseconds.

The last two rows differ by more than the per-layer rows explain, and the
profile says where the rest goes: system calls. `html/template` writes a page
out as it walks it, one value at a time through the response writer, and the
comparison service spends two thirds of its CPU inside `write`. A generated
component renders into a buffer and hands the finished document over in one
call, and the framework spends half. Take `write` out and the two cost about the
same, around 75 µs a request, so the whole difference sits there. It does not
appear in the table above, because it is not a layer — it is how the layer above
it reaches the socket.

Measure which layer dominates your application first. Removing a few microseconds
of middleware only matters after that layer has proved to be the bottleneck.

## Settings to check before a production release

A development setting that reaches production can cost more than a small code
optimization can recover.

### Environment and logging

`config.dev.toml` enables debug output and SQL logging. Load a production
configuration with `APP_ENV=prod` or the corresponding environment name for load
tests and production, and verify that query logging is off. If you enable detailed
logging during an investigation, turn it off again when the investigation ends.

[Query diagnostics](/productivity/query-diagnostics/) explains how to investigate
slow queries without leaving all production SQL logging enabled.

### Session backend

The `cookie` backend needs no external store, and its cryptographic work costs
about 0.5 µs per request for a typical session — opening the incoming record and
sealing the outgoing one measures 0.44 µs at 256 bytes and 0.72 µs at 1 KB. Its
real scaling constraint is the wire: the response carries the whole session
record, and the browser returns that record on the next request. Larger sessions
therefore mean larger request and response headers.

The `rdb`, `redis`, `dynamo`, and `firestore` backends leave only a small identifier
in the browser. They reduce the bytes on the wire, but add a storage access to each
request.

```toml
[session]
enabled = true
backend = "rdb" # or redis, dynamo, firestore
```

Use `cookie` for a small session when avoiding another dependency matters. Choose
a server-side backend when revocation, persistence, size, or network transfer
belongs on the server. Operational requirements should narrow the choice first;
then measure with the expected session size and storage latency. `dev-volatile`
loses every session on restart and is restricted to development.

See [Sessions](/guides/backend/sessions/) for the persistence and revocation
tradeoffs.

### Database connections

A production configuration can contain more than one connection. A common layout
uses one group for writes and another for reads; multiple connections in the same
reader group are selected round-robin.

```toml
[middleware.rdb]
enabled = true
default_group = "reader"
write_group = "writer"
migration_group = "writer"

[[middleware.rdb.connections]]
group = "writer"
dsn = "postgres://app:${DB_PASSWORD}@writer.example/app"
max_open_conns = 20

[[middleware.rdb.connections]]
group = "reader"
dsn = "postgres://app:${DB_PASSWORD}@reader-1.example/app"
readonly = true
max_open_conns = 20

[[middleware.rdb.connections]]
group = "reader"
dsn = "postgres://app:${DB_PASSWORD}@reader-2.example/app"
readonly = true
max_open_conns = 20
```

With this configuration, an unpinned query uses `reader`. Handlers that only read
can keep using that default, but Popcorn Web does not infer a destination from the
SQL text. Write paths therefore need an explicit code change.

```go
// One write.
user, err := queries.CreateUser(pw.SelectDB(r, "writer"), name)

// A write transaction.
err := pw.TransactionContext(pw.SelectDB(r, "writer"), func(ctx context.Context) error {
	return queries.RecordAudit(ctx, "user.created")
})
```

Reads that must observe a preceding write should also use `writer` rather than wait
for replica convergence. [Relational databases](/guides/storage/rdb/) describes
connection groups and transaction behavior in detail.

Pool limits apply to each connection. Before increasing `max_open_conns`, add the
limits across all configured connections, multiply that sum by the number of
application instances, and verify that the database can accept the resulting total.

One cost this framework does not charge is worth knowing while you size that
pool. No transaction is opened when a request arrives. A handler that reads one
row or writes one row runs one statement, holds a connection for that statement,
and never pays a `BEGIN` and `COMMIT` around it — which is also why a pool sized
for statements rather than for whole requests is enough. The boundary appears
where `pw.Transaction` is written, and nowhere else, so the choices a
transaction carries — the isolation level, whether it is read-only and therefore
servable by a replica, where the commit falls relative to a slow external call —
stay with the code that knows which ones matter. See
[Queries](/guides/storage/queries/#transactions).

### Compression and CSRF

Response compression is off by default. Enable it only when a CDN or reverse proxy
is not already doing that work; see
[Response compression](/guides/backend/compression/).

CSRF is the most visible item in the middleware measurement, but it still costs
only 2.4 µs in this benchmark. Exclude only APIs that use bearer authentication and
cannot be called from a browser with cookies. Removing protection from a wider path
is not a useful performance trade.

```toml
[security.csrf]
enabled = true
include = ["/**"]
exclude = ["/api/**"]
```

## Measure the application

Use production-equivalent settings and compare the same features, response, and
data. Match the instrument to the decision: latency for user wait time, throughput
for capacity, and a CPU profile for work performed in code.

Once a profile identifies a candidate, replace that layer and measure again. If
the HTTP stack itself proves to be the limit, the second transport is a build
flag away — [build targets](#build-targets) above has what it costs and what it
does not buy.
