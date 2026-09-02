---
title: Handlers
description: Routing and request binding — struct tags, validation, and JSON, form, and multipart bodies.
sidebar:
  order: 1
---

Handlers stay ordinary `net/http` handlers, but they do not have to parse every
input by hand. The stable application-facing API,
`github.com/shibukawa/popcornweb/pw`, adds routing-compatible generation and
typed request binding without changing the handler signature.

## Code generation

None of the binding below is reflection at request time. `pw generate` reads
this package's Go source, finds the route registrations and the `pw.Parse` and
response calls in it, and writes the binder, the JSON codecs, and the OpenAPI
fragment into a `_pw_gen.go` file beside the source. Those files are build
output: Git ignores them, and regenerating recreates them.

Three commands run it. `pw dev` watches the project's sources and regenerates
whenever one changes, then rebuilds and restarts. `pw build` generates before it
compiles, and [`pw generate`](/pw/project/generate/) is that same work stopping
short of the compiler, for a build that TinyGo or your own `go build` drives — or
for running it once by hand.

The scan is not the whole module. `popcornweb.toml` names directories per
purpose, and handlers are the `handlers` purpose:

```toml
[generate]
handlers = ["handlers"]
```

Each listed directory is walked recursively, so a nested package needs no entry
of its own. A handler in a directory no purpose lists is not reported, because
ordinary Go lives throughout a project and generation cannot tell which files
were meant for it — the package compiles, no binder is written for its input
type, and `pw.Parse` fails at request time saying so. Add the directory here
rather than looking for the bug in the handler.
[`pw generate`](/pw/project/generate/) lists every purpose.

## Routing

```go
package handlers

import "github.com/shibukawa/popcornweb/pw"

var mux = pw.NewServeMux()

func Handlers() *pw.ServeMux { return mux }
```

On ordinary Go builds, `pw.ServeMux` **is** `net/http`'s `ServeMux`: a type
alias, not a wrapper. Its patterns, wildcards, and precedence are therefore the
standard library's. TinyGo does have a `ServeMux`, but as of TinyGo 0.42 it
predates the Go 1.22 pattern syntax — method prefixes and path parameters are
not available — so TinyGo builds receive a separate implementation with the
same semantics.

Each handler file registers itself in `init`, so adding a route means adding a
file rather than editing a central table:

```go
func init() {
	mux.HandleFunc("GET /", home)
	mux.HandleFunc("GET /users/{id}", showUser)
	mux.HandleFunc("POST /users", createUser)
}
```

For splitting routes across packages as an application grows, see
[Project structure](/guides/architecture/project-structure/).

## Binding a request

Routing remains familiar; request parsing is where generation takes over.
`pw.Parse[T]` fills a struct from the request, and the generator reads that call
site to write the binding code for `T` ahead of time. No runtime reflection is
needed.

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

| Tag | Source |
| --- | --- |
| `input:"name"` *(or no tag)* | query string, falling back to the body |
| `query:"name"` | query string only |
| `payload:"name"` | request body only |
| `path:"id"` | path wildcard, from a pattern like `/users/{id}` |
| `header:"Authorization"` | request header |
| `cookie:"session"` | request cookie |
| `method:"method"` | the HTTP method |

Without an explicit wire name, the field name is lowerCamelCased —
`DisplayName` becomes `displayName`.

`input` is deliberately forgiving: a query parameter wins, and the body is read
only when the query does not provide the value. When accepting both sources
would make the endpoint ambiguous, use `query` or `payload` to require exactly
one.

### Request bodies

The same request struct accepts three body formats, leaving the wire format to
the client:

- `application/json`
- `application/x-www-form-urlencoded`
- `multipart/form-data`

An ordinary HTML form post and a JSON API call can therefore share a handler:

```go
type createUserInput struct {
	Name  string `payload:"name" check:"required,maxlen=40"`
	Email string `payload:"email" check:"required,email"`
}
```

### File uploads

Multipart file fields use `httpbind.File`:

```go
import httpbind "github.com/shibukawa/tinybind-go"

type uploadInput struct {
	Title string        `payload:"title" check:"required"`
	Image httpbind.File `payload:"image" check:"required"`
}
```

`File` exposes `Filename`, `ContentType`, `Size`, and `Content`. The multipart
body limit defaults to 1 MiB and is changed with
`httpbind.SetMaxMultipartBodyBytes`. The framework's own
`server.max_request_body` applies first, so a multipart limit raised past its
10 MiB default does nothing until that one moves too — see
[Application Configuration](/guides/architecture/configuration/).

### Undeclared fields

`payload:"*"` collects everything you did not declare:

```go
type eventInput struct {
	Type   string         `payload:"type"`
	Extras map[string]any `payload:"*"`
}
```

Use `map[string]json.RawMessage` to keep the raw JSON instead of decoded values.

## Validation

The `check` tag declares constraints. They are compiled during generation, not
interpreted per request.

| Rule | Applies to | Example |
| --- | --- | --- |
| `required` | any | rejects empty strings, missing values, empty files |
| `min` / `max` | numbers | `check:"min=1,max=100"` |
| `minlen` / `maxlen` / `len` | strings | `check:"maxlen=40"` |
| `pattern=...` | strings | regular expression |
| `email` | strings | RFC format |
| `uuid` | strings | UUID format |
| `date` | strings | `YYYY-MM-DD` |
| `time` | strings | `HH:MM:SS` |
| `datetime` | strings | RFC 3339 |

Separate rules with commas. If a `pattern` contains a comma, put it last.

### Defaults and enumerations

Two constraints are tags of their own rather than `check` rules:

| Tag | Applies to | Example |
| --- | --- | --- |
| `default:"value"` | scalars | applied when the value is absent |
| `enum:"a,b,c"` | scalars | `enum:"asc,desc"` |

`default` is the tag configuration structs already use, so it means the same
thing on both sides of the framework. `enum` needs a tag of its own for a
different reason: inside `check` the comma already separates rules, so it could
not also separate values. Space around each value is trimmed.

```go
type listInput struct {
	Page int    `query:"page" check:"min=1" default:"1"`
	Sort string `query:"sort" enum:"asc,desc" default:"asc"`
}
```

Writing either one inside `check` is an error, and the message names the tag to
use instead:

```
check: enum is not a check rule; use the struct tag enum:"asc,desc" instead
```

The cost of the comma is that an enum value cannot contain one. A set that needs
such a member wants a validating type rather than a tag.

A failed check makes `pw.Parse` return an error carrying the offending field.
Passing it to `pw.WriteProblem` produces a 400 with field-level detail — see
[Responses](/guides/frontend/responses/).

Every tag above has more to it than the common case: which field kinds each rule
accepts, how `input` resolves per kind, what a rest map excludes, and what
reaches OpenAPI. That is
[Request Binding](/reference/request-binding/).

## Request-scoped accessors

| Call | Returns |
| --- | --- |
| `pw.Logger(r)` | request-scoped `*slog.Logger` |
| `pw.Config[T](r)` | a registered configuration struct |
| `pw.DB(r)` | `(*sql.DB, bool)` — `false` on PostgreSQL, which runs on a native pgx pool |
| `pw.Transaction(r, fn)` | runs `fn` inside a transaction |

Each takes the request, because the request is what a handler holds. Below the
handler — a service function, a job, anything callable without a request — the
same accessor takes a `context.Context` under the `Context` suffix
`database/sql` spells `ExecContext` with: `pw.LoggerContext(ctx)`,
`pw.ConfigContext[T](ctx)`, `pw.DBContext(ctx)`. `pw.Context(r)` is how a
handler produces the context those take.

```go
func createUser(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[createUserInput](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	err = pw.Transaction(r, func(ctx context.Context) error {
		if _, err := queries.InsertUser(ctx, input.Name, input.Email); err != nil {
			return err
		}
		return queries.RecordAudit(ctx, "user.created")
	})
	if err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}
```

The callback does not pass a transaction handle to either query. Generated query
functions recover it from the context, as described in
[Queries](/guides/storage/queries/).

## The lifecycle

```go
func main() {
	if err := pw.Run(context.Background(), handlers.Handlers()); err != nil {
		log.Fatal(err)
	}
}
```

The plain handler is only one part of a running service. Before the first
request arrives, `pw.Run` parses configuration, handles application flags such
as `--generate-config`, validates the configured runtime, initialises the
database pool, checks for collisions between your routes and the operational
endpoints, and builds the middleware stack. Then it serves. On `SIGINT` or
`SIGTERM` it shuts down gracefully and closes registered resources in reverse
order.

When you need the wrapped handler without owning the server — behind another
listener, or in a test — `pw.Middlewares(handler, options...)` performs the same
initialisation and returns the same stack as a plain `http.Handler`.

`pw.WithPublicFS(fsys)` supplies the embedded public tree explicitly;
scaffolded projects instead register it from `public.go`.
