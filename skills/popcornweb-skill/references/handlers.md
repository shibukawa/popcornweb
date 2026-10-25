# Handlers: registered routing, binding, responses, sessions, middleware

How to write HTTP handlers in a Popcorn Web project that uses registered routing: declaring and registering a handler, binding the request into a typed struct, writing HTML/JSON/stream/error responses, reading sessions and the authenticated account, and fitting middleware into the framework stack. All generation described here is done by `pw generate` into `_pw_gen.go` files — build outputs you never edit.

## Declaring and registering a handler

Handlers are ordinary `net/http` handlers. The stable application-facing package is `github.com/shibukawa/popcornweb/pw`. `pw.ServeMux` is a type alias for `net/http.ServeMux` on ordinary Go builds (TinyGo gets a compatible implementation), so patterns, wildcards, and precedence are the standard library's Go 1.22 syntax, and `r.PathValue` works as usual.

The scaffolded `handlers/index.go` holds the mux:

```go
package handlers

import "github.com/shibukawa/popcornweb/pw"

var mux = pw.NewServeMux()

func Handlers() *pw.ServeMux { return mux }
```

Each handler file registers its own routes in `init` — adding a route means adding a file, not editing a central table:

```go
func init() {
	mux.HandleFunc("GET /", home)
	mux.HandleFunc("GET /users/{id}", showUser)
	mux.HandleFunc("POST /users", createUser)
}
```

`main` passes the mux to the lifecycle:

```go
func main() {
	if err := pw.Run(context.Background(), handlers.Handlers()); err != nil {
		log.Fatal(err)
	}
}
```

`pw.Run` parses configuration, validates the runtime, initialises the database pool, checks route collisions with operational endpoints, builds the middleware stack, serves, and shuts down gracefully on SIGINT/SIGTERM. `pw.Middlewares(handler, options...)` performs the same initialisation but returns the wrapped stack as a plain `http.Handler` — for tests, serverless adapters, or an existing `http.Server`.

## Request binding

`pw.Parse[T](r)` fills one struct from one request. The type argument must be a concrete named type written at the call site — `pw generate` reads the call site and writes the binding code ahead of time (no reflection). A `Parse` behind a generic wrapper produces no binding. The call and the type must live in a directory `generate.handlers` lists in `popcornweb.toml`.

```go
type showUserInput struct {
	ID   int    `path:"id"`
	Sort string `query:"sort" default:"name"`
}

func showUser(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[showUserInput](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	// ...
}
```

### Source tags

One tag per field. A field with no tag is an `input` field carrying its own name; without an explicit wire name the field name is lowerCamelCased (`DisplayName` → `displayName`).

| Tag | Source | Notes |
| --- | --- | --- |
| *(no tag)* or `input:"name"` | query, then body | query wins when both carry the name |
| `query:"name"` | query string only | |
| `payload:"name"` | request body only | JSON, form, or multipart |
| `payload:"*"` | every body key no other field consumed | one per struct; must be `map[string]any` or `map[string]json.RawMessage` |
| `path:"id"` | path wildcard from the pattern (`GET /users/{id}`) | |
| `header:"Authorization"` | one request header | |
| `cookie:"session"` | one cookie | |
| `method:"method"` | the HTTP method | receives `GET`, `POST`, ... |

A scalar `input` field reads query then body; a nested struct, slice, or map `input` field always comes from the body. Use explicit `query`/`payload` when accepting either source would be ambiguous. `path`, `header`, `cookie`, and `method` never consume a body key, so a same-named body key still lands in a rest map.

### Field types and bodies

- Scalars: `string`, `int`, `int64`, `bool`, `float64`. Pointer fields are **not** bound.
- Composites: named struct, nested anonymous struct, `[]scalar`, `[]struct`, `map[string]scalar`, `map[string]struct`. Nesting is JSON-first; form/multipart bodies carry flat keys only.
- Three body media types fill the same struct, so an HTML form post and a JSON call share one handler: `application/json`, `application/x-www-form-urlencoded`, `multipart/form-data`.

Multipart files use `httpbind.File` (exposes `Filename`, `ContentType`, `Size`, `Content`):

```go
import httpbind "github.com/shibukawa/tinybind-go/httpbind"

type uploadInput struct {
	Title string        `payload:"title" check:"required"`
	Image httpbind.File `payload:"image" check:"required"`
}
```

Only `required` applies to a file. The multipart body limit defaults to 1 MiB (`httpbind.SetMaxMultipartBodyBytes(8 << 20)` raises it); `server.max_request_body` (10 MiB default) applies first and caps it.

### Validation

`check` holds rules; `enum` and `default` are separate tags — writing either inside `check` is a generation error.

```go
type listInput struct {
	Keyword string `query:"keyword" check:"required,minlen=2,maxlen=64"`
	Page    int    `query:"page" check:"min=1" default:"1"`
	Sort    string `query:"sort" enum:"asc,desc" default:"asc"`
}
```

| Rule | Form | Applies to |
| --- | --- | --- |
| `required` | bare | any field kind |
| `min`, `max` | `min=1` | `int`, `int64`, `float64` (inclusive) |
| `minlen`, `maxlen`, `len` | `minlen=3` | `string` |
| `pattern` | `pattern=^[A-Z]{3}$` | `string`; must be the **last** rule in the tag |
| `email`, `uuid` | bare | `string` |
| `date`, `time`, `datetime` | bare | `string` — `YYYY-MM-DD`, `HH:MM:SS`, RFC 3339 |

Rules to remember:

- Format shortcuts skip empty values unless the field is also `required`.
- Only `required` applies to files, rest maps, structs, slices, and maps.
- `required` on numeric/bool fields accepts the zero value (cannot distinguish omitted from `0`/`false`).
- `default` is scalars-only, parsed at generation time, applied **after** validation and only when the value was absent — never a repair for a rejected value. This ordering enables sentinels: `check:"min=1" default:"-1"` lets a handler distinguish "absent" from a supplied `-1`.
- `enum` values are comma-separated with surrounding space trimmed; the `default` need not appear in the enum list.

### Binding failure

`pw.Parse` returns one error carrying every rejected field, each recording its location (`input`, `query`, `payload`, `path`, `header`, `cookie`). Pass it to `pw.WriteProblem` for an RFC 9457 problem document with field-level detail. Build the same shape by hand for a rule tags cannot express: `pw.Validation(pw.Field(name, location, message))`.

## Responses

### HTML

```go
pw.WriteHTML(w, r, Home(HomeParams{Name: input.Name}))
```

`Home` / `HomeParams` are generated from `handlers/home.pw.html`. The handler passes a leaf fragment; `WriteHTML` renders it inside the registered document shell (`templates/document.pw.html`) — the handler never names or imports the document. The chain is rendered into a buffer and validated before commit, so a template failure becomes a clean 500. `WriteHTML` accepts no status code — it answers 200.

Every HTML response also says whether a shared cache may hold it, and the answer defaults to `private, no-store`. It is read from the template chain before anything renders, not from the request — see [caching.md](caching.md).

- `pw.WriteHTMLChain(w, r, []pw.HTMLWrapper{...}, leaf)` — explicit wrapper chain (e.g. a print shell).
- `pw.WriteHTMLFragment(w, r, Row(RowParams{Item: item}))` — one template as the whole response: no shell, no merged head. For htmx-style swaps. Never streams; a fragment contributing to the document head is rejected with a 500; failures answer `application/problem+json` with their real status (htmx does not swap non-2xx responses).

### JSON

```go
pw.WriteAPI(w, r, user)
```

The call site generates a typed encoder (no reflection). It reads a `json` tag's name and its options the `encoding/json/v2` way: `json:"-"` drops the field entirely, `omitempty` drops a member that would encode as `""` / `[]` / `{}` (so `0` and `false` are still written), and `omitzero` drops the Go zero value (which is what reaches `0` and `false`). Any other option is a generation error, not a silently inert tag. A nil slice encodes as `[]` and a nil map as `{}`, never `null`. Status is 200. `WriteAPI` call sites also feed the OpenAPI document, where `required` still comes from `check:"required"` alone — `omitempty` does not make a field optional there.

`pw.WriteStatus(w, r, http.StatusCreated, value)` is `WriteAPI` with the success status explicit — 201, 202, or 204, which writes no body. Keep the status a literal or named constant: the OpenAPI document lists one response per static status, and a computed status is invisible to the scanner.

### Redirects

`pw.Redirect(w, r, url, status)` and `pw.RedirectSeeOther(w, r, url)` write one.
The framework refuses a target a browser could only follow by running script, and
answers an update request with a navigate directive instead of a 303.

Code that holds no writer — a template loader, a service function — **returns** a
redirect instead: `pw.SeeOther(url)` (303), `pw.TemporaryRedirect` (307),
`pw.MovedPermanently` (301), `pw.PermanentRedirect` (308) are `error` values, and
`pw.WriteProblem` turns them into the redirect response.

### Streams

```go
func events(w http.ResponseWriter, r *http.Request) {
	pw.WriteStream(w, r, func(stream *pw.Stream[ChatEvent]) error {
		for event := range source {
			if err := stream.Write(event); err != nil {
				return err
			}
		}
		return nil
	})
}
```

The client negotiates SSE, NDJSON, or a JSON array via `Accept`; the handler
serves all three unchanged. Closing is the runtime's, not the caller's: it runs
whichever way the callback ends. An error the callback returns reaches the
handler installed with `pw.SetStreamErrorHandler` — there is no status left to
carry it, so install one or a mid-stream failure says nothing in production.

### WebSockets

Reach for a socket only when the client has to keep talking (a chat room, a
collaborative document, a control channel). A one-way stream keeps HTTP framing,
survives proxies that know nothing about upgrades, and answers a failure with an
ordinary problem response — take it first.

```go
func Chat(w http.ResponseWriter, r *http.Request) {
	room, _ := pw.QueryValue(r, "room")   // read the request *here*, before the entry

	if err := pw.WebSocket(w, r, func(s *pw.Socket[ClientMsg, ServerMsg]) error {
		for {
			in, err := s.Read()
			if err != nil {
				return nil // the peer went away, or went quiet past the timeout
			}
			if err := s.Write(ServerMsg{Type: "message", From: room, Text: in.Text}); err != nil {
				return err
			}
		}
	}); err != nil {
		pw.Logger(r).Warn("upgrade refused", pw.Err(err))
	}
}
```

Neither type argument is spelled at the call: generation recovers them from the
closure parameter and writes a decoder and an encoder into `_pw_gen.go`. **If a
socket connects and immediately closes, run `pw generate` before looking anywhere
else** — a socket whose types were never discovered compiles, opens, and fails on
its first message.

- `Read` must be called from one goroutine; `Write` may be called from any (it
  shares the runtime's control-frame lock), which is what makes a broadcast
  goroutine safe.
- **Something must keep reading**, even in a push-only handler: ping and close
  frames are handled inside the read call.
- Returning from the callback is how you close.
- **The callback must not read `w` or `r`.** On the fasthttp build it runs after
  the handler returned. Capture what you need first. `r.Context()` and what it
  carries are fine; `r.RemoteAddr` is not.
- A non-nil return from `pw.WebSocket` means the upgrade was refused and the
  problem response is already written — log it, do not answer.

The framework checks `Origin` before the handshake (an upgrade never reaches CSRF,
which guards unsafe methods), using the same comparison the CSRF frame uses: the
request's origin must be this deployment's own or one in
`security.csrf.trusted_origins`. **Behind a TLS-terminating proxy, declare
`server.trusted_proxies`** or every upgrade is refused on the scheme. A request
with no `Origin` is admitted (only browsers must send one); a literal `null` is
refused. `pw.WebSocketWith(w, r, opts, fn)` takes a per-endpoint policy.

Every connection carries a read limit (1 MiB), an idle deadline (60s), a ping
cadence (54s), and a write deadline (10s), none of which can be turned off.
`pw.SetSocketDefaults` changes them process-wide.

### Errors

```go
pw.WriteProblem(w, r, pw.NotFound("no such user"))
```

Writes RFC 9457 `application/problem+json`. Constructors: `pw.BadRequest`, `Unauthorized`, `Forbidden`, `NotFound`, `Conflict`, `PayloadTooLarge`, `TooManyRequests`, `InternalServerError`, `ServiceUnavailable` — each accepts an `error`, a `string`, another `pw.Problem`, or nothing. `pw.RateLimited(pw.RateLimit{...}, value)` adds `Retry-After` and `X-RateLimit-*` metadata to a 429. For other statuses build `pw.Problem{Status:, Title:, Code:, Message:}` directly.

`WriteProblem` maps any `error`: a `pw.Problem` (even `%w`-wrapped) is used as-is; a binding/validation error keeps its status and field detail; anything else becomes a 500. So a handler can forward a service error directly: `pw.WriteProblem(w, r, err)`.

Two safety behaviours: 5xx details never leak (logged in full, reported as `internal error`); a committed response is never corrupted (the error is logged instead of appended).

### HTML error pages

Scaffolded projects carry status templates including `templates/400.pw.html`, `429.pw.html`, and `500.pw.html`. `pw.RegisterHTMLErrorPage(resolve)` installs the generated `func(Problem) HTMLFragment` resolver; `pw.WriteProblem` uses it when `Accept` prefers HTML and writes problem JSON otherwise. The resolver receives the mapped `Problem`, never the original error, so a template cannot leak a server-side cause.

## Request-scoped accessors

| Call | Returns |
| --- | --- |
| `pw.Logger(r)` | request-scoped logger; never nil. `pw.WithLogAttributes(ctx, …)` adds attributes |
| `pw.Config[T](r)` | a registered configuration struct; never fails (zero value if unregistered) |
| `pw.DB(r)` | `(*sql.DB, bool)` — `false` on PostgreSQL (native pgx pool; use `postgres.WithConn`) |
| `pw.Transaction(r, fn)` | runs `fn` in a transaction; generated queries recover it from the context |
| `pw.RequestAuthentication(r)` | the verified authentication result; `.Subject` is what a private cache key uses |
| `pw.Authenticated(r)` | whether the request carries a verified identity |
| `pw.RequestLocale(r)` | the resolved locale (see [i18n.md](i18n.md)) |
| `pw.MemoStore(r, name)` | a configured data cache (see [caching.md](caching.md)) |
| `pw.TraceID(r)` / `pw.SpanID(r)` | correlation ids, empty outside a trace |

Each takes the request. Below the handler — a service function, a job, a child span — the same accessor takes a `context.Context` under a `Context` suffix: `pw.LoggerContext(ctx)`, `pw.ConfigContext[T](ctx)`, `pw.DBContext(ctx)`, `pw.TransactionContext(ctx, fn)`. `pw.Context(r)` is how a handler produces the context those take, and the one to prefer over `r.Context()`.
| `pw.IsBot(r)` | User-Agent heuristic; use only for render-branch choice, never access decisions |

Reading one value off the request goes through the framework rather than the
request object, because those reads are the ones a second transport cannot
follow: `pw.PathValue(r, "id")`, `pw.QueryValue(r, "sort") (string, bool)`,
`pw.Queries(r)`, `pw.FormValue(r, "body")`. Prefer them to `r.PathValue` and
`r.URL.Query()` — a project that ever builds for fasthttp needs them, and they
cost nothing where it does not.

Three questions about what the caller wants, for a handler that answers more than
one kind of client: `pw.WantsValue(r)` (a script called this by name and holds
the answer), `pw.WantsUpdate(r)` (the runtime intercepted a gesture and expects
regions), `pw.WantsLive(r)` (a live subscription).

```go
err = pw.Transaction(r, func(ctx context.Context) error {
	if _, err := queries.InsertUser(ctx, input.Name, input.Email); err != nil {
		return err
	}
	return queries.RecordAudit(ctx, "user.created")
})
```

A nested `pw.Transaction` opens a savepoint, not a second transaction.

## Sessions

Session slots are declared by Go type in `main`, after every package `init` has run:

```go
pw.RegisterSessionStore[Density]("density", session.Shared)      // client reads and writes; plain cookie
pw.RegisterSessionStore[Locale]("locale", session.ReadOnly)      // client reads only; signed cookie
pw.RegisterSessionStore[Cart]("cart", session.Private)           // sealed cookie while anonymous, backend after login
pw.RegisterSessionStore[Grants]("grants", session.ServerOnly)    // backend always
pw.RegisterSessionStore[Scopes]("scopes", session.RequestScope)  // process memory, one request
```

Lifetime options on the same line: `session.ExpiresAfter(d)` (ends a value early), `session.OutlivesSession(d)` (survives logout; cookie-placed tiers only; `session.BrowserMax` = as long as HTTP allows). `RequestScope` refuses all lifetime options.

Handlers read and write by type:

```go
cart, ok := session.Load[Cart](r.Context())   // bare read; issues no cookie

handle, ok := session.Value[Cart](r.Context())
if ok {
	err := handle.Set(cart)                    // first Set issues the session token
}
```

An anonymous `Private` value is bounded by the cookie budget (~3.8 KB); an oversized write fails with `session.ErrCookieTooLarge` rather than spilling to the server. Logout destroys every slot except `OutlivesSession` ones. Per-account (cross-browser) preferences belong in your own database, not a session. Cookies belonging to no session use `session.NewJar[T]` directly (see the cookies guide).

## Authenticated account

With `plugin/auth` enabled, the framework serves `/auth/login`, `/auth/callback`, and `/auth/logout` (POST only — render sign-out as a form, a GET gets 405) and resolves the session before handlers run. `auth.protection.include` in configuration lists the paths requiring a session; everything else stays public.

```go
import "github.com/shibukawa/popcornweb/plugin/auth"

func home(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.User(r.Context())
	if !ok {
		pw.WriteProblem(w, r, pw.Unauthorized())
		return
	}
	// user.AccountID, user.DisplayName, user.Email, user.Issuer, user.Key
}
```

Write the `!ok` branch even on guarded routes — a later edit to `protection.include` must not silently turn `user.AccountID` into `""`. `auth.User` answers *who*, never *what they may do*; authorization stays in application code. Under `auth.mode = "jwt_only"` (API servers), read the bearer principal with `auth.Bearer(r.Context())` instead. Authorization decisions must consume `pw.RequestAuthentication` / `auth.User`, never the presence of a cookie.

## Middlewares

The framework stack runs outside in on a numbered ladder — the gaps are yours. Each number has a constant (`pw.SlotRequestID`, `pw.SlotAccessLog`, ...):

| Slot | Frame | Switch |
| --- | --- | --- |
| 10 | OpenTelemetry root span | only when tracing exports |
| 20 | resource injection (logger, DB, config) | always on |
| 25 | client address resolution | `server.trusted_proxies` |
| 30 | request ID | `middleware.request_id` |
| 40 | access log | `middleware.access_log` |
| 50 | recover (panic → error response) | `middleware.recovery` |
| 52 | security headers, and CORS | `security.headers.enabled`, `security.cors.enabled` |
| 55 | process rate-limit ceiling (unkeyed) | `ratelimit.process` |
| 70 | request timeout | `middleware.request_timeout` |
| 80 | body limit | `server.max_request_body` |
| 90 | public assets | `server.public.enabled` |
| 100 | health/readiness probes | `server.health`, `server.readiness` |
| 110–150 | extensions: storage (110), session (120), authentication (130), rate limit (135), CSRF (140), guard (150) | per extension |
| 160 | OpenAPI document and UI | `server.openapi`, `server.api_doc` |

The keyed rate limiter sits at 135, after authentication, because a per-subject
budget needs a resolved identity; the unkeyed process ceiling sits at 55, above
almost everything, because a distributed flood is exactly what no keyed bucket
sees. Framework operational endpoints and the public asset mount are exempt from
counting, and the carve-out is not configurable.

Add your own with `pw.RegisterMiddleware(slot, name, func(http.Handler) http.Handler)`, called from `main` before the chain is built:

```go
pw.RegisterSessionStore[RequestTime]("request_time", session.RequestScope)
pw.RegisterMiddleware(pw.SlotSession+5, "request_time", withRequestTime)
```

Pick the number by what the middleware must observe: below 20 no resources are in context; below 50 a panic is answered by recover; below 120 the session is not yet resolved; after 150 only guard-admitted requests arrive. Slots 100 and 160 refuse registration (they are handlers). Reusable capabilities register with `pw.RegisterExtension(pw.Extension{Name, Slot, Setup, Close})` from a package `init`; `Setup` runs once at startup and returning a nil middleware installs nothing.

## Response policy you configure rather than write

Four frames answer above your handler and need no code, only configuration
(keys and defaults in [config.md](config.md)):

- **Compression** (`middleware.compression`, off by default) encodes rendered
  HTML and JSON for clients that accept it, offering `compression_codings`
  (`["zstd", "gzip"]`) in server preference order and setting `Vary:
  Accept-Encoding` either way. Encoder levels are deliberately not configurable.
  Leave it off where a CDN or reverse proxy already does the work. Precompressed
  static assets are a different thing — the build writes `.br`/`.zstd`/`.gz`
  sidecars regardless.
- **CORS** (`security.cors`, off by default) rides in the security-headers frame,
  so a preflight is answered above every refusal your application could produce.
  Four combinations fail startup rather than serving: enabled with no origin,
  `allow_credentials` with `"*"` origins, `allow_credentials` with `"*"` in
  `allowed_headers`, and `allow_credentials` with `include = ["/**"]`.
- **Rate limiting** (`ratelimit`, off by default) gives each caller one budget
  across the whole application: `per_subject` for a verified identity,
  `per_address` otherwise, plus the unkeyed `process` ceiling. There are no
  per-route rules — that vocabulary belongs to the gateway in front. Behind a
  proxy, set `server.trusted_proxies` or every anonymous caller shares one
  bucket. The `memory` backend counts inside one process, so N replicas enforce
  N times the limit; more than one replica wants `backend = "redis"` and a blank
  import of `ratelimitstore/redis`. An unreachable store **admits** the request
  and logs at error level — watch for that line, since a limiter silently not
  limiting is the worst state.
- **Token revocation** (`auth.jwt.revocation.mode`, no default — startup refuses
  a missing value) is the `jwt_only` counterpart to logout. `token` revokes one
  credential by `jti`; `subject` refuses every token an identity was issued
  before now, by comparing a stored stamp against `iat`. There is no HTTP
  endpoint: `auth.RevokeToken` / `auth.RevokeSubject` (and
  `auth.ReinstateToken` / `auth.ReinstateSubject`,
  `auth.TokenRevoked` / `auth.SubjectRevoked`) are Go calls your application
  authorizes. An unavailable store answers `503`, not `401`.

Handlers still write a 429 themselves where the quota is the application's own:
`pw.WriteProblem(w, r, pw.RateLimited(pw.RateLimit{Limit:, Remaining:, Reset:, RetryAfter:}, "quota exceeded"))`.

## OpenAPI from registered handlers

`pw generate` reads route registrations, `pw.Parse[T]` call sites, `check`/`enum`/`default` tags, `pw.WriteAPI` calls, `pw.NewStream[T]`, and the problem constructors, and emits one OpenAPI 3.1 fragment per package; the framework merges them at startup. Nothing is annotated separately.

- Handler doc comments: first sentence → `summary`, rest → `description`. Write `// List the catalogue.` not `// listItems lists...` — the text is carried verbatim. A `Deprecated:` paragraph sets `deprecated: true`.
- Struct and field doc comments become schema/property/parameter descriptions, on request and response types alike.
- Every `check` rule maps to a JSON Schema keyword (`min`→`minimum`, `minlen`→`minLength`, `pattern`, formats, `enum`, `default`).
- Set the document identity once in `main`: `pw.SetOpenAPIInfo(pw.OpenAPIInfo{Title: "Catalogue API", Version: "1.4.0"})` — both fields required; conflicting second calls error.
- Only statically visible routes appear; HTML endpoints are not described.

Serving is configured with `server.openapi` (path, no default) and `server.api_doc` (`"scalar"` or `"swagger"`); protect both via `auth.protection.include`. Programmatic access: `pw.AssembleOpenAPI()`, `pw.OpenAPIJSON`, `pw.ScalarUI(specURL)`, `pw.SwaggerUI(specURL)`.

## Common mistakes

- **Editing `_pw_gen.go` files.** They are build outputs. Edit the `.pw.html` / `.pw.sql` / handler source and run `pw generate` (or let `pw dev` do it).
- **`pw.Parse` through a generic wrapper or type-parameter argument.** The generator needs a concrete named type at the call site; otherwise no binding is generated.
- **`enum=` or `default=` inside `check`.** They are separate struct tags. Also: `pattern=` must be the last rule in a `check` tag.
- **Pointer fields in a request struct.** Not bound. Use value fields; use the `default` sentinel ordering when you must distinguish absent from zero.
- **Expecting `required` to catch an omitted `0` or `false`.** On numeric/bool fields the zero value passes `required`.
- **Expecting `pw.WriteHTML` to set an error status.** It always answers 200; `pw.WriteProblem` always answers problem JSON. Rendering `404.pw.html` under a 404 status is a path you build.
- **Reaching for `omitempty` to drop a `0` or a `false`.** It only drops what encodes as an empty JSON value (`""`, `[]`, `{}`); `omitzero` is the one that drops a zero number or a false flag.
- **Expecting `json:"-"` to keep a field out of a `pw.Parse` struct.** It only governs the JSON codec. The request binder still fills that field by its wire name, so `?hidden=x` sets a `Hidden` field tagged `json:"-"`. A value the caller must not control does not belong in a `Parse` struct at all.
- **Authorizing on cookie presence instead of `auth.User` / `pw.RequestAuthentication`.** Also, omitting the `!ok` branch on guarded routes.
- **Registering middleware or session stores after the chain is built, or from the wrong place.** Call `pw.RegisterMiddleware` / `pw.RegisterSessionStore` in `main`, before `pw.Run`.
- **Logout as a link.** `/auth/logout` is POST only; use a form.
- **Handler mounted through a variable the generator cannot follow.** It works at runtime but disappears from OpenAPI; register with a literal pattern string.
- **Reading `w` or `r` inside a WebSocket or stream callback.** On the fasthttp build the callback outlives the handler. Capture what you need before the entry.
- **Shipping a stream or socket with no `pw.SetStreamErrorHandler`.** A failure after the status is committed has nowhere else to go.
- **Reaching for `r.PathValue` / `r.URL.Query()` in a project that also builds for fasthttp.** Use `pw.PathValue` / `pw.QueryValue`.
- **Enabling rate limiting behind a proxy without `server.trusted_proxies`.** Every anonymous caller resolves to the proxy and shares one bucket, which turns the limit into an outage.
