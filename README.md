# Popcorn Web

<img src="docs/logo.png" alt="Popcorn Web" width="480">

Popcorn Web is a small, TinyGo-oriented web application framework for Go, built
directly on `net/http`. Templates, SQL, request binding, and OpenAPI compile to
typed Go ahead of time, so a renamed template argument or a `SELECT` that no
longer matches its result type is a build error rather than a production
incident. Nothing is rediscovered by reflection at request time, which is also
what lets the same source compile under **TinyGo**.

Routing is `net/http`'s own `ServeMux`, handlers stay `http.HandlerFunc`, and
queries return through `database/sql` — so the middleware, `httptest` tests, and
`http.Handler`s already in your codebase keep working, and a handler can leave
this framework as easily as it entered.

**Documentation: <https://shibukawa.github.io/popcornweb/>**

## Quick start

```sh
brew install shibukawa/tap/pw
pw init myapp
cd myapp
pw dev
```

`pw init` asks about ten choices and then writes a project that already
compiles: a handler, a typed page, a document shell, error pages, configuration,
and a `devbox.json`. It runs `go mod tidy` and `pw generate` before reporting
success, so there is no separate setup step afterwards. `pw dev` then starts the
declared services, regenerates code, applies pending migrations, builds, runs,
and restarts on every change — `Ctrl-C` stops all of it together.

Generated projects pin their own Go toolchain, so you do not need a matching Go
installed before creating one. Devbox is optional; if Go is on `PATH`, run
`pw dev` directly.

## Installing

### The `pw` command

`pw` handles scaffolding, code generation, formatting, migrations, and the
development server. Install it first.

| Channel | Command | Covers |
| --- | --- | --- |
| Homebrew | `brew install shibukawa/tap/pw` | macOS (Apple Silicon and Intel), Linux |
| Nix | `nix run github:shibukawa/popcornweb#pw -- version` | `x86_64-linux`, `aarch64-linux`, `aarch64-darwin` |
| Release archive | [releases page](https://github.com/shibukawa/popcornweb/releases) | every target, and the only channel covering Windows |
| Go toolchain | `go install github.com/shibukawa/popcornweb/cmd/pw@latest` | anywhere a matching Go toolchain is installed |

Homebrew and the release archives ship a prebuilt binary; the Nix derivation and
`go install` build from source. `go install` is listed last because it needs a Go
toolchain matching the module's requirement, which is exactly the prerequisite
the other three channels remove. The flake also exposes a `devShells.default`
with Go, `gopls`, and TinyGo if you want the host toolchain without Devbox.

Confirm the installation with `pw version`.

### The library

Popcorn Web requires **Go 1.27 or later**. `pw init` writes a `go.mod` that
already requires the framework; an existing module needs one step:

```sh
go get github.com/shibukawa/popcornweb
```

Application code imports `pw`, which is the stable application-facing API:

```go
import "github.com/shibukawa/popcornweb/pw"
```

## Using `pw`

One command covers the project lifecycle. Every command except `pw init` finds
the project by walking upward from the working directory until it reaches
`popcornweb.toml`, so all of them work from any nested directory.

| Command | Purpose |
| --- | --- |
| [`pw init`](https://shibukawa.github.io/popcornweb/pw/project/init/) | create a runnable project in a new directory |
| [`pw add`](https://shibukawa.github.io/popcornweb/pw/project/add/) | enable a capability the project declined at init |
| [`pw new`](https://shibukawa.github.io/popcornweb/pw/project/new/) | scaffold one more handler or page |
| [`pw generate`](https://shibukawa.github.io/popcornweb/pw/project/generate/) | write everything a compiler needs, stopping before the compiler |
| [`pw check`](https://shibukawa.github.io/popcornweb/pw/project/check/) | report generated files that are stale or missing |
| `pw fmt` | format template sources into their canonical form |
| `pw i18n` | reconcile message catalogs against the templates that use them |
| [`pw migrate`](https://shibukawa.github.io/popcornweb/pw/database/migrate/) | inspect, apply, and roll back migrations |
| [`pw seed`](https://shibukawa.github.io/popcornweb/pw/database/seed/) | load seed datasets |
| [`pw build`](https://shibukawa.github.io/popcornweb/pw/project/build/) | generate and compile a release binary |
| [`pw dev`](https://shibukawa.github.io/popcornweb/pw/project/dev/) | watch, regenerate, migrate, rebuild, and restart |
| [`pw doctor`](https://shibukawa.github.io/popcornweb/pw/project/doctor/) | report what a named environment will actually run |
| [`pw lsp`](https://shibukawa.github.io/popcornweb/productivity/editor-support/) | serve editor analysis over the Language Server Protocol |

`pw build --backend` selects the HTTP implementation (`nethttp` or `fasthttp`)
and `--target` selects deployment packaging (`lambda`, `azure-functions`,
`google-cloud-run-functions`, `vercel-go`). Run `pw help` for the full flag list
of every command.

## What the code looks like

A `.pw.html` file is a typed template language that compiles to Go. Its
parameters become a Go struct, so misspelling one is a generation error with a
position rather than a blank region at runtime.

```
package handlers

export component Home(count: int): html {
  <main>
    <h1>Hello, World!</h1>
    <p>Page views: <strong>{count}</strong></p>
  </main>
}
```

A `.pw.sql` file is the same idea for queries. The result contract is declared,
and a projection that stops matching it fails generation.

```
package queries

type AccessCounter { count: int }

export statement IncrementAccess(): sql.one<AccessCounter> {
  INSERT INTO access_counter (id, count)
  VALUES (1, 1)
  ON CONFLICT(id) DO UPDATE SET count = access_counter.count + 1
  RETURNING count
}
```

The handler that joins them is a plain `http.HandlerFunc`:

```go
package handlers

import (
	"net/http"

	"github.com/shibukawa/popcornweb/pw"
	"myapp/queries"
)

func init() { mux.HandleFunc("GET /{$}", home) }

func home(w http.ResponseWriter, r *http.Request) {
	counter, err := queries.IncrementAccess(r.Context())
	if err != nil {
		pw.WriteProblem(w, r, pw.InternalServerError(err))
		return
	}
	pw.WriteHTML(w, r, Home(HomeParams{Count: counter.Count}))
}
```

`pw generate` writes the `_pw_gen.go` files that back `Home`, `HomeParams`, and
`queries.IncrementAccess`. They are build output, excluded by the `.gitignore`
`pw init` wrote, and never edited by hand.

## Features

### Core

| Feature | |
| --- | --- |
| Standard-library routing | `pw.ServeMux` is a type alias for `net/http.ServeMux` — same patterns, wildcards, and precedence |
| [Registered and discovered routing](https://shibukawa.github.io/popcornweb/guides/cross-layer/discovered-routing/) | register routes explicitly, or let a directory tree declare them |
| [Middleware](https://shibukawa.github.io/popcornweb/guides/backend/middlewares/) | ordinary `func(http.Handler) http.Handler`, plus request IDs, recovery, body limits, and request-scoped loggers |
| [Typed configuration](https://shibukawa.github.io/popcornweb/guides/architecture/configuration/) | TOML and environment variables, per-environment, with scaffolds generated from the declarations |
| [Custom commands](https://shibukawa.github.io/popcornweb/guides/architecture/custom-commands/) | typed CLI subcommands on the deployed binary |
| [Operational endpoints](https://shibukawa.github.io/popcornweb/guides/deployment/operational-endpoints/) | health, readiness, OpenAPI, graceful shutdown, reverse-order cleanup |

### Frontend and rendering

| Feature | |
| --- | --- |
| [Typed templates](https://shibukawa.github.io/popcornweb/guides/frontend/templates/) | `.pw.html` components with typed parameters, compiled to Go |
| [Scoped styles and Tailwind](https://shibukawa.github.io/popcornweb/guides/frontend/styling/) | a component's `<style>` block is namespaced at compile time; Tailwind is one `pw add tailwind` away |
| [Responses](https://shibukawa.github.io/popcornweb/guides/frontend/responses/) | HTML, JSON, XML, CSV, redirects, downloads, and RFC 9457 problem documents |
| [Async rendering](https://shibukawa.github.io/popcornweb/guides/cross-layer/async-rendering/) | stream the page around a boundary that is still loading |
| [Live rendering](https://shibukawa.github.io/popcornweb/guides/cross-layer/live-rendering/) | keep updating a region while the reader stays on the page |
| [Partial updates](https://shibukawa.github.io/popcornweb/guides/cross-layer/partial-updates/) | answer a same-origin link with only the regions whose markup differs |
| [Rendering cache](https://shibukawa.github.io/popcornweb/guides/frontend/rendering-cache/) | cache rendered output by scope |
| [Static assets](https://shibukawa.github.io/popcornweb/guides/frontend/static-assets/) | embedded, fingerprinted asset serving |
| [i18n](https://shibukawa.github.io/popcornweb/guides/frontend/i18n/) | message catalogs reconciled against the templates that use them, plus [locale routing](https://shibukawa.github.io/popcornweb/guides/backend/locale-routing/) |

### Interactivity

| Feature | |
| --- | --- |
| [Server actions](https://shibukawa.github.io/popcornweb/guides/interactivity/server-actions/) | name a Go function from a template instead of writing its URL |
| [Forms](https://shibukawa.github.io/popcornweb/guides/interactivity/forms/) | client-side feedback that agrees with the server-side checks, and suggestion lists that need no script |
| [Fragments](https://shibukawa.github.io/popcornweb/guides/interactivity/fragments/) | server-rendered fragments combined with dialogs, popovers, and custom elements |
| [Navigation](https://shibukawa.github.io/popcornweb/guides/interactivity/navigation/) | view transitions and speculation rules, so ordinary page-to-page navigation feels continuous |
| [Signals](https://shibukawa.github.io/popcornweb/guides/cross-layer/signals/) | a named instruction from a live source to code the page registered |
| [Component scripts](https://shibukawa.github.io/popcornweb/guides/interactivity/component-scripts/) and [browser controls](https://shibukawa.github.io/popcornweb/guides/interactivity/browser-controls/) | the small amount of browser code a server-rendered page still needs |
| [htmx](https://shibukawa.github.io/popcornweb/guides/interactivity/htmx/) and [React](https://shibukawa.github.io/popcornweb/guides/interactivity/react/) | supported when you want them, not required |
| [Typed streams](https://shibukawa.github.io/popcornweb/guides/frontend/streams/) | one response as a sequence of typed events — SSE, NDJSON, or a JSON array, chosen by the client |
| [WebSocket](https://shibukawa.github.io/popcornweb/guides/backend/websocket/) | declare an inbound and an outbound struct; generation writes the encoding on both sides |

### Storage

| Feature | |
| --- | --- |
| [Typed SQL](https://shibukawa.github.io/popcornweb/guides/storage/queries/) | `.pw.sql` compiled to typed Go over `database/sql` |
| [Relational databases](https://shibukawa.github.io/popcornweb/guides/storage/rdb/) | SQLite, PostgreSQL, and MySQL/MariaDB, each on TinyGo as well |
| [DynamoDB](https://shibukawa.github.io/popcornweb/guides/storage/dynamodb/) and [Firestore](https://shibukawa.github.io/popcornweb/guides/storage/firestore/) | typed `.pw.dynamo` and Firestore templates |
| [Object storage](https://shibukawa.github.io/popcornweb/guides/storage/object-storage/) | uploads in S3-compatible storage, through a TinyGo-capable S3 client |
| [Batching](https://shibukawa.github.io/popcornweb/guides/storage/batching/) | cutting the cost of many statements — a transaction on SQLite, `Batch` and `COPY` on PostgreSQL |
| [Data cache](https://shibukawa.github.io/popcornweb/guides/backend/data-cache/) | reuse what an upstream call returned for the same typed question, with concurrent misses collapsed onto one call |
| [Migrations](https://shibukawa.github.io/popcornweb/productivity/migrations/) and [seed data](https://shibukawa.github.io/popcornweb/productivity/seed-data/) | applied by `pw migrate` and `pw dev` |

### Authentication and security

| Feature | |
| --- | --- |
| [What is defended by default](https://shibukawa.github.io/popcornweb/guides/architecture/security/) | what the framework handles, what it hands you, and where a request is checked before your handler runs |
| [Authentication](https://shibukawa.github.io/popcornweb/guides/backend/authentication/) | OpenID Connect (Authorization Code with PKCE), WebAuthn passkeys, or both; `jwt_only` verifies a bearer token without mounting a login |
| [Sessions](https://shibukawa.github.io/popcornweb/guides/backend/sessions/) | opaque server-side sessions or sealed cookies, over [several stores](https://shibukawa.github.io/popcornweb/guides/storage/session-storage/) |
| [Security headers](https://shibukawa.github.io/popcornweb/guides/frontend/security-headers/) | validated browser policy by default, plus CSRF |
| [CORS](https://shibukawa.github.io/popcornweb/guides/backend/cors/) and [rate limiting](https://shibukawa.github.io/popcornweb/guides/backend/rate-limiting/) | configuration switches rather than assembled stacks |

### Operations

| Feature | |
| --- | --- |
| [OpenTelemetry](https://shibukawa.github.io/popcornweb/guides/architecture/telemetry/) | structured logs and traces, with framework spans reaching each SQL statement |
| [OpenAPI 3.1](https://shibukawa.github.io/popcornweb/productivity/api-documentation/) | generated from the handlers, bindings, and comments already in the code |
| [Compression](https://shibukawa.github.io/popcornweb/guides/backend/compression/) | negotiated zstd and gzip, off by default because a proxy usually owns it |
| [`pw doctor`](https://shibukawa.github.io/popcornweb/pw/project/doctor/) | resolves a named environment and reports missing, conflicting, or unsafe settings before deployment |
| [Dev console](https://shibukawa.github.io/popcornweb/productivity/dev-console/) | routes, queries, storybook, telemetry, and diagnostics during `pw dev` |
| [Testing](https://shibukawa.github.io/popcornweb/productivity/testing/) | run an application from an isolated copy of every registered configuration, with [E2E support](https://shibukawa.github.io/popcornweb/productivity/e2e-testing/) |

### Build and deployment targets

| Target | |
| --- | --- |
| Go and TinyGo | the same source, with no reflection in the generated path |
| `net/http` and fasthttp | selected by `pw build --backend` |
| [Containers](https://shibukawa.github.io/popcornweb/guides/deployment/container-images/) | image builds from the project |
| [Serverless](https://shibukawa.github.io/popcornweb/guides/deployment/serverless/) | AWS Lambda, Azure Functions, Google Cloud Run functions, Vercel |
| [Reverse proxy](https://shibukawa.github.io/popcornweb/guides/deployment/reverse-proxy/) and [service proxy](https://shibukawa.github.io/popcornweb/guides/backend/service-proxy/) | in front of, or from within, the application |

## Examples

[`examples/`](examples/) holds complete applications, each with its own README.

| Example | |
| --- | --- |
| [`helloworld`](examples/helloworld/) | the `pw init` scaffold, with a SQLite counter |
| [`todo`](examples/todo/) | one todo list written twice against one PostgreSQL table — once with `net/http` and pgx, once with the framework — so binary size and throughput are measured on identical behaviour |
| [`async_render`](examples/async_render/) | a page whose slow sections stream in afterwards |
| [`live_render`](examples/live_render/) | two regions that keep changing on the server's clock |
| [`partial_update`](examples/partial_update/) | two routes under one layout, with a table whose rows update in place |
| [`htmx_fragment`](examples/htmx_fragment/) | a task board where every interaction re-renders one region |
| [`websocket_chat`](examples/websocket_chat/) | a chat room over a typed WebSocket, in both builds |
| [`oidclogin`](examples/oidclogin/) | browser login through OpenID Connect |
| [`passkeylogin`](examples/passkeylogin/) | OpenID Connect creates the account, a passkey handles repeat login |

## Editor support

[`tools/vscode`](tools/vscode/README.md) is a Visual Studio Code extension that
highlights the three source dialects — `.pw.html`, `.pw.sql`, and `.pw.dynamo` —
including the template expressions embedded in their HTML, SQL, and clause
bodies. It is a grammar only: nothing is executed, no binary is needed, and it
works on a file opened with no workspace. Diagnostics and completion are planned
for a later version through a `pw lsp` language server.

## Documentation

- [Why Popcorn Web](https://shibukawa.github.io/popcornweb/start/why-popcorn-web/) — what the project is trying to do, and the measurements behind it
- [Installation](https://shibukawa.github.io/popcornweb/start/installation/)
- [Tutorial](https://shibukawa.github.io/popcornweb/tutorial/getting-started/) — five chapters from an empty directory to a login
- [Guides](https://shibukawa.github.io/popcornweb/guides/architecture/project-structure/) — one page per feature, in English and Japanese
- [Performance](https://shibukawa.github.io/popcornweb/guides/architecture/performance/) — binary size and throughput per build target, and what changes when you switch
- [Reference](https://shibukawa.github.io/popcornweb/reference/runtime/) — the runtime API, the template and query languages, and every configuration key

## License

Apache License 2.0. See [LICENSE](LICENSE).

The MySQL driver carried by
[`tinygodriver`](https://github.com/shibukawa/tinygodriver) includes an MPL-2.0
TinyGo fork; that notice travels with any artifact that links it.
