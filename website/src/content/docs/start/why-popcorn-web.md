---
title: Why Popcorn Web
description: Two aims — make the naive Go implementation the fast and safe one, and make the web frontend a first-class thing to build in Go.
sidebar:
  order: 0
---

Two things this project is trying to do.

The first is to make **the naive implementation the fast and safe one**. Write
the obvious `net/http` handler, and get performance and security you would
otherwise have to go back and add.

The second is to **raise the standing of frontend work in Go**. Go is currently
where you write the API while the frontend belongs to the JavaScript ecosystem.
That division is not a law of nature, and this framework is an argument against
it.

## The naive implementation, and what it gets for free

An application still looks like Go. `net/http` handlers, `context.Context`,
ordinary middleware, and `database/sql` remain the boundaries a team reads,
tests, and operates — so a handler can be wrapped by standard middleware,
mounted beside another `http.Handler`, or tested with `httptest`. Adopting the
framework does not also mean teaching the codebase a private request model.

### Built on the interfaces Go already has

A handler is an `http.HandlerFunc`, and what wraps it is the
`func(http.Handler) http.Handler` every Go codebase already writes. Queries
return through `database/sql`. Request-scoped values travel on the
`context.Context` Go has carried since 1.7. Routing is the `http.ServeMux` that
Go 1.22 taught to match methods and path parameters, so `GET /users/{id}` is the
standard library's pattern rather than a syntax this framework invented.

That is what keeps the profiling tools, the middleware conventions, and the
libraries already in your `go.mod` working, and it is why a handler can leave
this framework as easily as it entered.

A few things do take a different shape, for one reason: the second transport.
fasthttp has no `http.ResponseWriter` and no routing of its own, so a read or a
control that would go through one — `r.PathValue`, `http.NewResponseController`
— is a `pw` call instead: `pw.PathValue`, `pw.QueryValue`, and the flush
`Stream.Write` performs for you. Routing those through the framework is what
lets one handler source compile for either build.

### Generated code, no reflection, and TinyGo

`pw generate` turns templates, SQL, request binding, response types, and
configuration declarations into typed Go. A changed query result or a missing
template argument fails during generation or compilation rather than in
production, and the runtime does not rediscover those shapes with reflection on
every request.

Rendering is where that shows up most plainly. The same inventory table written
twice — once as an `html/template` parsed at startup, once as a typed component
— rendered to the same bytes. Medians of fifteen runs on Go 1.27.0, Apple M3:

| Rows | `html/template` | Generated |
| --- | --- | --- |
| 1 | 1.70 µs · 760 B · 30 allocs | 289 ns · 168 B · 4 allocs |
| 50 | 77.3 µs · 25.8 KiB · 1264 allocs | 6.05 µs · 168 B · 4 allocs |
| 500 | 785 µs · 259 KiB · 12 888 allocs | 58.4 µs · 168 B · 4 allocs |

The memory column is the one that compounds. A fifty-row page costs 168 bytes
instead of 26 KiB, and a five-hundred-row page the same 168 against 259 KiB. What
is never allocated is never collected later — a cost the collector otherwise
charges to whichever request happens to be running.

Removing reflection has a second consequence, and it is the one the project is
betting on. If WebAssembly becomes an ordinary place to deploy server code,
TinyGo is the compiler that gets you there — and `html/template` does not run
there at all. It compiles, then panics during package initialization on
reflection TinyGo does not implement, before a single byte is rendered. A
generated component has nothing to reflect over and simply runs.

Most of what a Go server reaches for has the same problem: `crypto/tls`, the Go
1.22 `ServeMux`, `aws-sdk-go-v2`, the Google client libraries. Rather than treat
TinyGo as a degraded mode, the dependencies were rebuilt.
[`tinygodriver`](https://github.com/shibukawa/tinygodriver) supplies a host
network driver, HTTPS over the operating system's TLS stack, `database/sql`
drivers for SQLite, PostgreSQL and MySQL, and clients for S3, DynamoDB and
Datastore. That is more infrastructure than one person would sensibly take on,
and it exists because AI assistance made it tractable.

### Standards rather than house conventions

What goes on the wire is what the specifications say, so the people and
machines on the other side already know how to read it. Errors are
[RFC 9457 problem documents](/guides/frontend/responses/#errors); rate limits
carry `Retry-After`; deprecation uses RFC 9745 and RFC 8594; login is OpenID
Connect and WebAuthn; traces are W3C Trace Context; the API describes itself as
OpenAPI 3.1. [Web Standards](/appendix/web-standards/) lists each one — and,
just as usefully, the ones deliberately left out and why.

### A simple implementation should not be a bare one

A short handler is attractive. A service that is short only because
authentication, response policy, compression, observability, and configuration
checks were left for later is not. Those concerns stay on the supported path, so
a small application can gain them without growing a second framework around
itself:

- [Federated login and passkeys](/guides/backend/authentication/) provide both
  external identity and phishing-resistant repeat login.
- [Security response headers](/guides/frontend/security-headers/) establish
  browser policy by default, while one switch enables negotiated
  [response compression](/guides/backend/compression/) when the application
  rather than a proxy owns compression.
- Static analysis turns the handlers, bindings, response calls, and comments
  already in the code into [OpenAPI 3.1 documentation](/productivity/api-documentation/).
- [OpenTelemetry integration](/guides/architecture/telemetry/#reading-a-request-trace) exports structured
  logs and traces, while framework spans make requests, renders, and database
  statements visible without application-specific timers.
- Typed [configuration](/guides/architecture/configuration/) accepts environment
  variables, and [`pw doctor`](/pw/project/doctor/) resolves each named
  environment and reports missing, conflicting, or unsafe settings before
  deployment.

Some of these remain deliberate choices: compression is off when a proxy should
own it, and federated login still needs a provider. What changes is the starting
point. The straightforward implementation becomes naturally more secure, faster
over the wire, observable, and easier to operate.

## Frontend work belongs in Go too

Ask where a Go web application ends today and the answer is usually "at the JSON
boundary". The templates, the styling, the interactivity, and the routing that
describes the site all moved to the JavaScript ecosystem, and Go kept the part
that has no user interface.

That split is worth challenging, because the reasons for it are historical
rather than technical. `html/template` fails at render time instead of build
time, has no component model, and cannot see a typo in a field name. A team that
wants a typed, composable frontend has not had one in Go — so it reached for the
ecosystem that did.

What is here instead:

- **Components with typed parameters.** A [`.pw.html`](/guides/frontend/templates/)
  file compiles to Go. Rename a parameter and the handler stops compiling;
  misspell a field and generation says so, with a position.
- **Styles that belong to a component.** A scoped `<style>` block in a component
  is compiled and namespaced, and [Tailwind CSS](/guides/frontend/styling/) is
  one `pw add tailwind` away — including after the fact.
- **Rendering that does not wait for the slowest query.**
  [Async rendering](/guides/cross-layer/async-rendering/) streams a page around
  the boundary that is still loading, and
  [live rendering](/guides/cross-layer/live-rendering/) keeps updating a region
  while the reader is still on the page.
- **A route is a directory.** [Discovered routing](/guides/cross-layer/discovered-routing/)
  reads the filesystem rather than a registration you keep in sync with it.
- **Navigation that replaces what changed.**
  [Partial updates](/guides/cross-layer/partial-updates/) answer a same-origin
  link with the regions whose markup differs, and the layout chain you already
  wrote is what makes the delta possible.

Several of these are ideas the JavaScript ecosystem worked out first, and the
debt is acknowledged. A few go further than their inspiration: async and live
rendering are the same `await` clause in the template, distinguished by the
declaration rather than by a second syntax, and partial updates need no client
state because the boundaries are the layouts the server already renders.

None of it is a client-side framework. The reader receives server-rendered HTML
from the first byte to the last, and the only browser code involved is one small
module that moves finished markup into place.

Rendering only on the server can look like a step backwards. It is not. Next.js
and React, the projects that led the web frontend ecosystem to where it is, have
spent the last few years moving back toward the server, and server components
rendering server-side are what they now put in front. The code they ship to the
browser is the part they are trying to shrink. Rebuilding that client-component
ecosystem in Go would cost more than it returns. Server components are a
different proposition: assembling HTML on the server and sending it is work Go is
already good at, and there is room to do it with less energy and more speed.

The JavaScript ecosystem has been relentless in its pursuit of productivity; the
Go ecosystem has favored stability. Popcorn Web is not trying to split the
difference. The aim is to keep Go's stable interfaces and predictable operations,
close the gap, and eventually move past the productivity bar the JavaScript
ecosystem has set.

## Where to go next

[Getting started](/tutorial/getting-started/) builds a running project in the
first chapter. [Performance](/guides/architecture/performance/) has the numbers
for each build target, including the `fasthttp` one, and what actually changes
when you switch.
