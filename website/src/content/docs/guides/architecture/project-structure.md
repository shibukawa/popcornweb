---
title: Project structure and principles
description: The project at three scales — the pw development cradle, feature-oriented packages, and a net/http handler with generated data boundaries.
sidebar:
  order: 2
---

A Popcorn Web project has more than one structure. The directory tree is the
most visible, but it sits between two others: the tool environment that builds
and runs the application, and the request path inside one handler.

The same design choice appears at all three scales. Keep familiar Go and Web
interfaces visible. Package the repetitive work around them so a project does
not have to invent it again.

## The development cradle

During development, the application binary does not run alone. `pw dev` watches
its sources, generates Go, applies migrations, builds assets, starts the binary,
and replaces it after a change. Around that process it provides the local
identity provider, structured log capture, telemetry receiver, database tools,
template storybook, and diagnostics.

<figure>
<svg viewBox="0 0 700 410" role="img" aria-label="The pw project tool surrounds a development cradle. Lifecycle commands consume template, SQL, and Go sources and produce the application binary. Inside pw dev, watch, generation, migration, build, and restart operate the binary while a development identity provider, telemetry, logs, database console, storybook, and doctor support it. popcornweb.toml configures pw, while runtime configuration, environment variables, and flags are read by the application binary.">
  <defs>
    <marker id="pw-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" opacity="0.65"/>
    </marker>
  </defs>
  <rect x="155" y="16" width="525" height="324" rx="10" fill="currentColor" fill-opacity="0.035" stroke="currentColor" stroke-width="1.5" opacity="0.7"/>
  <text x="175" y="42" fill="currentColor" font-family="inherit" font-size="15" font-weight="600">pw — project tool</text>
  <g fill="currentColor" fill-opacity="0.07" stroke="currentColor" stroke-width="1" opacity="0.9">
    <rect x="180" y="58" width="142" height="44" rx="5"/>
    <rect x="338" y="58" width="142" height="44" rx="5"/>
    <rect x="496" y="58" width="158" height="44" rx="5"/>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="11" text-anchor="middle">
    <text x="251" y="77">init · add · new</text>
    <text x="251" y="92" opacity="0.6">scaffold the project</text>
    <text x="409" y="77">generate · prepare · build</text>
    <text x="409" y="92" opacity="0.6">compile and package</text>
    <text x="575" y="77">migrate · seed · doctor</text>
    <text x="575" y="92" opacity="0.6">operate and inspect</text>
  </g>
  <rect x="180" y="120" width="474" height="198" rx="8" fill="currentColor" fill-opacity="0.035" stroke="currentColor" stroke-width="1.5" opacity="0.65"/>
  <text x="198" y="145" fill="currentColor" font-family="inherit" font-size="13" font-weight="600">pw dev — development cradle</text>
  <rect x="326" y="174" width="176" height="70" rx="6" fill="currentColor" fill-opacity="0.13" stroke="currentColor" stroke-width="1.5"/>
  <text x="414" y="204" fill="currentColor" font-family="inherit" font-size="13" text-anchor="middle" font-weight="600">application binary</text>
  <text x="414" y="222" fill="currentColor" font-family="inherit" font-size="10" text-anchor="middle" opacity="0.65">the same program you deploy</text>
  <g fill="currentColor" fill-opacity="0.055" stroke="currentColor" stroke-width="1" opacity="0.85">
    <rect x="198" y="166" width="108" height="92" rx="5"/>
    <rect x="522" y="166" width="112" height="92" rx="5"/>
    <rect x="239" y="270" width="350" height="32" rx="5"/>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="10" text-anchor="middle">
    <text x="252" y="187">watch</text>
    <text x="252" y="204">generate</text>
    <text x="252" y="221">migrate · assets</text>
    <text x="252" y="238">build · restart</text>
    <text x="578" y="187">development IdP</text>
    <text x="578" y="204">telemetry · logs</text>
    <text x="578" y="221">data · queries</text>
    <text x="578" y="238">storybook · doctor</text>
    <text x="414" y="291">development console and build-failure overlay</text>
  </g>
  <g stroke="currentColor" stroke-width="1.2" fill="none" marker-end="url(#pw-arrow)" opacity="0.55">
    <path d="M306 211 L326 211"/>
    <path d="M522 211 L503 211"/>
    <path d="M414 270 L414 245"/>
    <path d="M132 76 L179 76"/>
    <path d="M132 201 L197 201"/>
    <path d="M132 319 L154 319"/>
    <path d="M414 374 L414 245"/>
  </g>
  <g fill="currentColor" fill-opacity="0.04" stroke="currentColor" stroke-width="1" opacity="0.8">
    <rect x="10" y="43" width="122" height="66" rx="5"/>
    <rect x="10" y="168" width="122" height="66" rx="5"/>
    <rect x="10" y="286" width="122" height="66" rx="5"/>
    <rect x="277" y="374" width="274" height="30" rx="5"/>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="10" text-anchor="middle">
    <text x="71" y="65">.pw.html · .pw.sql</text>
    <text x="71" y="82">Go · migrations</text>
    <text x="71" y="99">assets · testdata</text>
    <text x="71" y="194">database and</text>
    <text x="71" y="211">local services</text>
    <text x="71" y="312">popcornweb.toml</text>
    <text x="71" y="329">project/tool settings</text>
    <text x="414" y="394">config.{env}.toml · environment · application flags</text>
  </g>
</svg>
</figure>

The outer box is a tool boundary, not a runtime dependency. `pw build` produces
the application binary; production runs that binary without `pw`, the console,
the development identity provider, or the storybook. Development is richer
because `pw dev` is allowed to coordinate the tools around the binary, not
because those tools have been hidden inside the release.

### Principle: one command owns the environment

A new project should not begin with a list of unrelated installations and
connection strings. `pw init` creates a runnable environment, and `pw dev`
brings it back with one command. Tools that nearly every application needs —
generation, migration, seed data, local identity, logs, traces, template
inspection, and database inspection — arrive connected to the project already.

This borrows the strongest habit of modern frontend tooling: a framework can
package an existing compiler, watcher, stylesheet tool, or protocol more usefully
than a README that asks every team to wire the same pieces together. The pieces
remain recognizable. Tailwind is still Tailwind, OpenTelemetry is still OTLP,
OIDC is still OIDC, and the running service is still a Go binary.

### Two configurations, two readers

The cradle also explains why the project has two kinds of configuration. They
look similar because both use TOML. They answer to different programs.

| Input | Read by | Decides |
| --- | --- | --- |
| `popcornweb.toml` | `pw` | project root, main package, generation scopes, migrations, assets, and development tools |
| `config.{APP_ENV}.toml` | application binary | server, database, authentication, sessions, observability, and application settings |
| environment variables and application flags | application binary | deployment-time overrides of runtime settings |

`dev.logs` belongs in `popcornweb.toml` because it controls the process running
beside the application. `server.port` belongs in `config.dev.toml` because the
application binds it. The same distinction holds in production: the release
binary needs runtime configuration, but it has no reason to read the project
layout that built it.

## Folders follow features

Inside the cradle sits an ordinary Go module. `pw init` starts it shallow: one
handler package and one query package. When separate areas acquire separate
owners, the tree grows by feature rather than by technical layer.

```text
myapp/
├── popcornweb.toml
├── config.dev.toml
├── cmd/myapp/main.go
├── templates/
│   ├── document.pw.html          the one document shell
│   └── 400|404|500.pw.html
├── migrations/
├── webroot/
│   ├── index.go                  root mux; mounts the feature areas
│   ├── home_handler.go
│   ├── home.pw.html
│   ├── admin/
│   │   ├── index.go              admin mux
│   │   ├── dashboard_handler.go
│   │   ├── dashboard.pw.html
│   │   └── queries/reports.pw.sql
│   └── accounts/
│       ├── index.go              accounts mux
│       ├── signup_handler.go
│       ├── signup.pw.html
│       └── queries/accounts.pw.sql
└── queries/
    └── users.pw.sql              shared by more than one feature
```

A handler and the template it renders stay together. A feature owns the queries
only it uses; a query moves upward after more than one feature actually shares
it. The path therefore says who owns a change before any architecture document
has to.

### Each feature owns a mux

A feature package has the same shape as the small scaffold:

```go
// webroot/admin/index.go
package admin

import "github.com/shibukawayoshiki/popcornweb/pw"

var mux = pw.NewServeMux()

func Handlers() *pw.ServeMux { return mux }
```

```go
// webroot/admin/dashboard_handler.go
package admin

func init() { mux.HandleFunc("GET /dashboard", dashboard) }
```

The root imports and mounts its children:

```go
// webroot/index.go
package webroot

func init() {
	mux.Handle("/admin/", http.StripPrefix("/admin", admin.Handlers()))
	mux.Handle("/", accounts.Handlers())
}
```

Paths inside `admin` are relative to its mount. The parent imports its children,
so their `init` functions register routes before the parent mounts them; children
never import the parent, so no package cycle appears. Subtree patterns and
`http.StripPrefix` are standard `net/http` composition.

:::caution
Avoid mounting a feature at `/public/`. Embedded static assets use
`server.public.mount`, which defaults to `/public`. Move either the feature or
the asset mount; startup reports a collision rather than choosing one. See
[Static Assets](/guides/frontend/static-assets/).
:::

### What generation reads

Generation scope is explicit and purpose-specific:

```toml
[generate]
handlers = ["webroot"]
templates = ["webroot", "templates"]
queries = ["webroot", "queries"]
config = ["cmd/myapp"]
```

`webroot/admin/queries` is already covered by the `webroot` entry. Only a new
top-level source directory requires an edit. No purpose has an implicit default:
a missing key is an error, while `[]` states that the project intentionally
generates nothing for that purpose. See [`pw generate`](/pw/project/generate/)
for the generated outputs and out-of-scope diagnostics.

Three things remain global however many feature packages exist:

- one `document.pw.html` shell;
- one ordered migration set under `migration.dir`;
- one namespace shared by registered configuration prefixes.

### Principle: layer by feature

A top level of `controllers`, `services`, `repositories`, and `models` scatters
one feature across generic packages. It often adds request types, persistence
types, domain types, and mappers whose only new information is how to copy one
shape into another. That costs review time, binary weight, human attention, and
AI context without necessarily creating a meaningful boundary.

Popcorn Web starts with the opposite default: keep a feature internally
shallow, compose features with Go packages and muxes, and extract a shared
package only after shared ownership exists. A layer must earn its place by
holding different knowledge or reversing a real dependency.

That does not exile domain knowledge from SQL. Schema constraints, query shape,
indexes, and transaction boundaries determine what the application permits and
how it fails. Generated queries keep that behavior visible; a generic CRUD
repository is not inserted merely to make the database appear farther away.

## One handler stays `net/http`

The smallest scale is one request. Here too, the framework surrounds a familiar
center instead of replacing it.

| Concern | How it works |
| --- | --- |
| Unit of work | one `http.Handler` |
| Links | full document requests by default |
| Forms | ordinary submissions and redirects |
| Mutation | handler or application service |
| Browser default after a mutation | Post/Redirect/Get |
| Transaction boundary | explicit, via `pw.Transaction` |
| Client-side enhancement | optional |

<figure>
<svg viewBox="0 0 700 210" role="img" aria-label="A request enters a generated typed binder and then a standard net/http handler. The handler calls generated query functions toward the database and generated template functions toward HTML output. A Popcorn Web response helper writes the final HTTP response. The handler retains the request, response writer, context, redirects, and status decisions.">
  <defs>
    <marker id="handler-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" opacity="0.65"/>
    </marker>
  </defs>
  <g fill="currentColor" fill-opacity="0.045" stroke="currentColor" stroke-width="1.2">
    <rect x="12" y="78" width="92" height="48" rx="5"/>
    <rect x="132" y="65" width="130" height="74" rx="5"/>
    <rect x="292" y="54" width="142" height="96" rx="7" fill-opacity="0.12" stroke-width="1.7"/>
    <rect x="466" y="18" width="132" height="55" rx="5"/>
    <rect x="466" y="132" width="132" height="55" rx="5"/>
    <rect x="622" y="18" width="66" height="55" rx="5"/>
    <rect x="622" y="132" width="66" height="55" rx="5"/>
  </g>
  <g fill="currentColor" font-family="inherit" text-anchor="middle">
    <text x="58" y="99" font-size="12">HTTP</text>
    <text x="58" y="115" font-size="12">request</text>
    <text x="197" y="89" font-size="11">pw.Parse[T]</text>
    <text x="197" y="106" font-size="10" opacity="0.65">generated binding</text>
    <text x="197" y="122" font-size="10" opacity="0.65">and validation</text>
    <text x="363" y="84" font-size="13" font-weight="600">net/http handler</text>
    <text x="363" y="105" font-size="10" opacity="0.65">w · r · context</text>
    <text x="363" y="122" font-size="10" opacity="0.65">status · redirect · policy</text>
    <text x="532" y="41" font-size="11">generated queries</text>
    <text x="532" y="58" font-size="10" opacity="0.65">typed parameters and rows</text>
    <text x="532" y="155" font-size="11">generated template</text>
    <text x="532" y="172" font-size="10" opacity="0.65">typed HTML parameters</text>
    <text x="655" y="50" font-size="11">database</text>
    <text x="655" y="155" font-size="11">HTTP</text>
    <text x="655" y="171" font-size="11">response</text>
  </g>
  <g stroke="currentColor" stroke-width="1.3" fill="none" marker-end="url(#handler-arrow)" opacity="0.65">
    <path d="M104 102 L131 102"/>
    <path d="M262 102 L291 102"/>
    <path d="M434 81 L465 54"/>
    <path d="M598 46 L621 46"/>
    <path d="M434 123 L465 151"/>
    <path d="M598 159 L621 159"/>
  </g>
  <text x="350" y="202" fill="currentColor" font-family="inherit" font-size="10" text-anchor="middle" opacity="0.58">application logic stays in the center; representation movement is generated at the edges</text>
</svg>
</figure>

Zoom out one step and the same handler sits in a conventional server stack.
`http.Server` accepts the connection, framework middleware wraps the mux, and
`http.ServeMux` selects application code. The colours in the following figure
distinguish standard library code, framework runtime, application code, and code
generated from application sources.

![A request rising through http.Server, the framework's middlewares, and http.ServeMux into a handler that calls a generated binder, query function, and component function before the runtime writes the response](../../../../assets/diagrams/request-parts.svg)

The code has the same shape Go developers already know:

```go
type createMemoInput struct {
	Body string `form:"body" check:"required,maxlen=1000"`
}

func createMemo(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[createMemoInput](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	if _, err := queries.CreateMemo(r.Context(), input.Body); err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	http.Redirect(w, r, "/memos", http.StatusSeeOther)
}
```

The handler still receives `http.ResponseWriter` and `*http.Request`. It owns
control flow, the status or redirect, calls to external systems, and transaction
boundaries. `r.Context()` remains the carrier understood by Go libraries.

What disappears is handwritten representation plumbing. `pw.Parse` uses a
generated binder to move path, query, header, form, or JSON input into a typed
struct and validate it. Generated query functions move typed parameters and
rows across SQL. Generated template functions accept typed parameters and
produce HTML. `pw.WriteProblem` and HTML response helpers write the protocol
shape consistently.

The generation boundary is finite and visible:

| Source you own | Generated Go |
| --- | --- |
| `*.pw.html` | typed component functions and parameter structs |
| `*.pw.sql` | typed, context-taking query functions and row scanning |
| `pw.Parse[T]` call sites | request binding and validation for `T` |
| `pw.WriteAPI[T]` / `pw.WriteStream[T]` call sites | response encoding for `T` |
| `pw.RegisterConfig[T]` call sites | startup configuration binding for `T` |
| all of the above | an OpenAPI 3.1 fragment |

Generated files end in `_pw_gen.go` and live beside their sources. They are
build output: `pw generate` overwrites them and `pw dev` regenerates them after
a relevant edit. Moving this work ahead of the request catches mismatched query
rows, missing template parameters, invalid output contexts, and binding errors
at build time. It also removes request-time reflection, which keeps TinyGo a
practical target.

### Principle: preserve common sense; generate the borders

Replacing `net/http` would discard knowledge already shared by Go developers,
libraries, debuggers, and tests. Popcorn Web keeps its mux patterns, handler
signature, request context, middleware model, redirects, and status codes.

But familiarity is not a reason to hand-copy data between representations.
Request binding, SQL rows, configuration, and template parameters are mechanical
boundaries where generation can add type checks and better errors. The framework
spends its abstraction budget there. The center remains ordinary Go.

The browser runtime follows the same rule. Standard links, forms, complete
responses, typed binding, templates, errors, configuration, and OpenAPI do not
depend on it. Import the server-driven update layer when a screen needs it; a
minimal application does not pay for a component graph, patch protocol, or
hydration dependency.

The three scales now line up. `pw` packages the development environment without
entering the release. Feature packages organize ownership without hiding Go's
composition. Generated borders remove data-moving code without hiding the
`net/http` handler that decides what the request means.
