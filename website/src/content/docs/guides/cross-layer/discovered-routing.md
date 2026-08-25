---
title: Discovered Routing
description: Serve a website from a directory tree, where a directory holding a page template is a route and the registration is generated.
sidebar:
  order: 3
---

A website is mostly `GET` pages, and the code that serves them is mostly the
same three lines repeated: register a pattern, decode the URL, render a
template. The directory layout already describes the URL space — the router only
restates it in Go, and the two drift apart the first time someone renames one
without the other.

The discovered router removes the restatement. Create a directory with a page
template in it, and that directory is a route.

```
pages/
├── page.pw.html                → GET /
├── layout.pw.html
└── users/id_/page.pw.html      → GET /users/{id}
```

Nothing registers those. `pw generate` walks the tree and writes the
registrations, so the filesystem is the source of truth rather than a copy of it.

## Code generation

That walk is the same generation step the rest of the project uses. It writes
`routes_pw_gen.go` at the tree root, plus the page renderers and parameter
structs beside their templates, and all of it is build output that Git ignores
and regeneration recreates.

Three commands run it. `pw dev` watches the project's sources and regenerates
whenever one changes, then rebuilds and restarts — which is why creating a
directory is enough to make a route appear. `pw build` generates before it
compiles, and [`pw generate`](/pw/project/generate/) is that same work stopping
short of the compiler, for a build that TinyGo or your own `go build` drives — or
for running it once by hand.

`popcornweb.toml` names the tree under the `pages` purpose:

```toml
[generate]
pages = ["pages"]
```

A tree root is listed there and nowhere else. It is one generation run over a
whole directory structure rather than a folder of independent sources, which is
why `pw init` leaves it out of `generate.templates` even though the pages in it
are `.pw.html`. In a project carrying both routers, `generate.templates` names
the handler and template directories and `generate.pages` names the tree.
[`pw generate`](/pw/project/generate/) lists every purpose.

## Two routers, one mux

This does not replace the handler package. The two coexist, and a project can
carry either or both.

| | Registered router | Discovered router |
| --- | --- | --- |
| Where a route comes from | a registration you wrote in Go | a directory holding `page.pw.html` |
| Methods | any | `GET` for pages, `POST` for actions |
| Response | whatever the handler writes | the rendered page, unless you take the handler rung |
| Inputs | path, query, headers, cookies, body, multipart | path and query |
| Generated OpenAPI | yes | no, by design |
| Fails when | a pattern is not a compile-time constant | a directory name is not a legal Go package name |

The difference is reach, not purpose. Returning HTML from a registered route
stays ordinary and supported; the discovered router trades that generality for
one shape, and inside that shape you write no registrations at all.

Knowing where the shape ends matters more than knowing what is in it. A page is
a `GET` that renders a template, and its actions are `POST` endpoints that own
their responses. A file download, a webhook, a `PUT`, an endpoint that has to
appear in your OpenAPI document: none of those are pages, and none of them are
awkward. They are registered routes, which is where they belonged before this
router existed.

So the two share one mux without negotiating:

```go
mux := handlers.Handlers()  // registered: your API
pages.Register(mux)         // discovered: the website
```

Registration order does not matter. A generated `GET /{$}` does not shadow a
hand-registered subtree, an unmatched path still answers 404, and a `POST` to a
page still answers 405. One collision is real: register the same method and path
twice and the standard library panics at startup. That is the loud failure
rather than the silent one, but it does mean adding a page can break a server
that already registers that pattern by hand.

An OpenAPI document describes a published API contract. An HTML page is not one,
and a page's action endpoint is that page's implementation detail, so neither
appears there. That exclusion is maintained deliberately rather than falling out
of the design: the generated registry is full of registrations, and a run that
read it back would document every page as an API route.

## Naming a route directory

A route directory is also a Go package, which decides how a dynamic segment is
spelled.

```
pages/users/id_/page.pw.html      → GET /users/{id}
pages/files/rest__/page.pw.html   → GET /files/{rest...}
```

One trailing underscore is a dynamic segment, two are a catch-all.

If you have used a file-based router before, you expected `users/[id]/`. Taste
is not the reason it is spelled otherwise. The Go toolchain rejects an illegal
import path element while it is still matching package patterns, before it
evaluates any build constraint. So one `pages/users/[id]/page.go` does not break its own
package — it breaks `go build ./...` for the whole module. `{id}`, `$id`, `@id`,
`:id`, `(group)`, and `-id` fail the same way, and discovery rejects them first
with the reason.

Exclusion follows the same authority: a directory starting with `_` or `.`, and
`testdata`, are ignored because the toolchain already ignores them. A private
folder inside the tree costs nothing but a leading underscore.

The root page registers `GET /{$}` rather than `GET /`. In the standard library
a bare `/` is a prefix pattern: it would swallow every unmatched path, and a
site with one page would answer 200 everywhere.

## What a page is

One file is a page. What you put beside it decides how much Go runs between the
request and the render.

| Files | Rung | What you get |
| --- | --- | --- |
| `page.pw.html` | template only | the whole handler is generated; the template's own `external` calls fetch the data |
| `+ page.go` with `func Load(w http.ResponseWriter, r *http.Request)` | handler | only the registration is generated; the response is yours |

Two rungs, and the question is only whether `page.go` exists. A `Load` that is
not the handler signature fails generation naming what it is and what it must
be.

A page that fetches does not need a rung of its own. It declares its loader as
an `external` and binds it with
[`val`](/reference/template-syntax/#val--naming-a-value), so the call sits in
the page's own source:

```html
package id_

external LoadUser(id: string): User

export component Page(id: string): html {
{val user = LoadUser(id)}
<h1>{user.name}</h1>
}
```

There used to be a third rung between these two, where `page.go` declared
`func Load(id string) (User, error)` and the generated handler called it. It is
gone, and losing it is a gain: its parameters were the *result* of the load, and
a page keyed on its result cannot be cached — computing the key would need the
load. Keyed on the `id` above, the page is one
[`@cache`](/guides/frontend/rendering-cache/#caching-a-components-own-load) away
from covering the fetch and the render together.

### Inputs

A page declares its inputs on the component — no struct, no binding tags. The
leading ones are the route's dynamic segments, in route order; the rest are
query parameters keyed by parameter name.

```html
package id_

external LoadUser(id: string): User

export component Page(id: string, page: int?): html {
{val user = LoadUser(id)}
<h1>{user.Name}</h1>
<p>page {page}</p>
}
```

That list is the component's, whether or not `page.go` exists — a page's inputs
are what the URL carries, and nothing else reads them.

A URL carries no objects, so inputs are scalars — with one exception below. That
leaves one thing a plain scalar cannot express: an absent `?page` and an
explicit `?page=0` would arrive as the same zero. A trailing question mark keeps
them apart by binding a pointer, which the loader then reads:

```html
external LoadUser(id: string, page: int?): View

export component Page(id: string, page: int?): html {
{val view = LoadUser(id, page)}
<h1>{view.name}</h1>
<p>page {view.page}</p>
}
```

### A key the URL repeats

An array is the exception, because a repeated key is something a URL does carry
natively. A checkbox group or a multi-select submits one pair per checked
control — `?tag=boots&tag=hats` — so an input declared as an array collects them
all, in the order the URL wrote them:

```html
export component Page(tag: string[]): html {
<ul>{for t in tag}<li>{t}</li>{/for}</ul>
}
```

Declare the array only where a key really may repeat. A page reading `?tag=a&tag=b`
through a plain `tag: string` still gets `a` and no warning, which is the case the
array declaration exists to replace.

Three things follow from the repeated key being the only spelling read:

- An absent key and a key carrying only empty values both arrive as an empty
  array, so a blank filter control contributes nothing. That is why an array
  input takes no `?` — it is already absent when it is empty.
- `?tag[]=boots` binds nothing. Brackets are ordinary key characters in Go and
  in the browser alike, so that URL names a key called `tag[]`, which no
  declaration can spell. The bracket convention belongs to PHP and Rack.
- `?tag=boots,hats` is one element holding a comma. Percent-encoding it as
  `%2C` changes nothing, because the query is decoded before anything could
  split it — which is why the comma-joined spelling is not read at all.

A path segment cannot be an array: a segment carries one value, and a catch-all
binds its whole remainder as one string.

```go
func LoadUser(id string, page *int) (View, error) {
	number := 1
	if page != nil {
		number = *page
	}
	return View{Name: "user " + id, Page: number}, nil
}
```

The default for an absent `?page` lives in the loader, where a reader looking
for it will find it, rather than inside a decoder nobody wrote.

The trailing `error` is what lets that loader decide the response. A binding at
the top of a page's body is evaluated before the first byte, so a failure still
picks the status while the rest of the page streams:

```go
func LoadUser(id string, page *int) (View, error) {
	row, ok := store.User(id)
	if !ok {
		return View{}, pw.NotFound("no user " + id)
	}
	…
}
```

Any [problem constructor](/guides/frontend/responses/#constructors) works
there — `pw.NotFound`, `pw.Forbidden`, `pw.BadRequest` — because the generated
handler passes what the render returned to `pw.WriteProblem`, which reads the
status off the error.

A redirect is returned rather than written, for the same reason:

```go
if _, ok := auth.User(ctx); !ok {
	return View{}, pw.SeeOther("/auth/login")
}
```

These are named for their status and returned the same way `pw.NotFound` is —
both are values a function hands back rather than writes. A redirect has two
axes, so there are four:

| | method may become GET | method preserved |
| --- | --- | --- |
| temporary | `pw.SeeOther` — 303 | `pw.TemporaryRedirect` — 307 |
| permanent | `pw.MovedPermanently` — 301 | `pw.PermanentRedirect` — 308 |

`pw.SeeOther` is the one a page reaches for: the target is fetched with GET
whatever the request was, so a reload repeats nothing.

The method axis rarely decides anything in a loader, because the render
answering it is a GET and 303 and 307 are indistinguishable there. It starts
mattering wherever a POST can reach the same code.

A returned redirect takes the same path as a written one: the target is refused
if a browser could only follow it by running script, and an update request gets
a navigate directive instead of a 303.

## Layouts

An ancestor `layout.pw.html` wraps every page below it, outermost first.

```html
package pages

export component Layout(children: html): html {
<div class="page"><slot required /></div>
}
```

A layout is an ordinary component and can hold anything a component holds. Two
rules bound it.

**It must declare `children: html`.** The template compiler emits the wrapper
binder only for that shape, so without the declaration there is nothing for the
generated chain to call. Discovery reports the missing declaration rather than
leaving it to the Go compiler.

**It may only read dynamic segments at or above its own directory.** A layout in
`pages/users/` cannot read the `id` of `/users/{id}`. A wrapper that depends on a
deeper segment cannot be reused when that segment changes, and being reused
across the segments below it is the entire value of an ancestor layout.

The outermost frame is not the layout's, though. `templates/document.pw.html`
still owns the doctype, `<head>`, and `<body>`, and it wraps the layout chain
from outside, exactly as it wraps a classic handler's page. So a layout holds
site chrome, a page holds page content, and neither repeats the shell. A
`document.pw.html` placed inside the tree is not applied.

## What is generated

```
pages/
├── layout.pw.html
├── layout_pw_gen.go     compiled layout component
├── page.pw.html
├── page_pw_gen.go       compiled page component
├── route_pw_gen.go      the route's parameters and decoder
├── routes_pw_gen.go     Register, Routes, and Actions
└── users/id_/
    ├── page.pw.html
    ├── page.go          optional Load, and server actions
    ├── page_pw_gen.go
    └── route_pw_gen.go
```

The registry lives in the tree root and nowhere else. The natural design puts a
composer beside each page, and it does not work: a leaf imports the root for its
ancestor layouts, so the root importing the leaf is a cycle. Composition
therefore lives in the registry, every generated import points down the tree, and
no upward edge exists.

That constraint reaches the handler rung. A handwritten `Load` cannot call a
composer above itself, so a handler-rung page composes its own chain: the
`BindLayout` generated for each ancestor layout, outermost first, and
`pwpage.Render` around the leaf. It is the same chain the registry assembles for
the rungs below, written out.

```go
func Load(w http.ResponseWriter, r *http.Request) {
	route, err := DecodeRoute(r)
	if err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	_ = route
	wrappers := []pwpage.Wrapper{BindLayout(LayoutParams{})}
	if err := pwpage.Render(w, r, wrappers, Page(PageParams{})); err != nil {
		pw.WriteProblem(w, r, err)
	}
}
```

`BindLayout` is generated into the package that holds the layout, so a page
deeper in the tree names its ancestors' — `pages.BindLayout(...)` for the root's,
then each one below it.

For a rung whose reason to exist is owning the response, that is the right side
of the trade.

Every page renders through the same response path a classic handler uses, so the
document shell, [async rendering](/guides/cross-layer/async-rendering/), crawler handling,
compression, and the project's error pages all apply without a page asking for
them.

## Server actions

Pages are `GET`; websites are not. A form or a button has to reach Go somewhere,
and that somewhere is not written as a URL. The template names an exported Go
handler and generation supplies the address.

```html
<button server-action="Rename" data-target="#name">rename</button>
```

```go
func Rename(w http.ResponseWriter, r *http.Request) { /* owns the whole response */ }
```

The attribute lowers to one carrying the endpoint, and every other attribute is
left alone:

```html
<button data-tb-action="/_action/00369cf962b6/Rename" data-target="#name">rename</button>
```

`data-tb-action` is what the framework runtime reads. Everything beside it is
untouched, so what `data-target` means is still your own code's decision — the
lowering resolves a name to an address and models nothing about what a click
should then do to the page.

What this buys over a handwritten `action="/users/42/rename"` is the compiler. A
URL is a string that is never checked against the handler it names. A name is a
symbol that must resolve: rename the Go function and generation fails at the
template that referenced it.

The handler is an ordinary `http.HandlerFunc`, so it can be tested with
`httptest` and needs no registration to run. It reads a typed request the same
way any handler does:

```go
type renameRequest struct {
	Name string `json:"name" check:"required"`
}

func Rename(w http.ResponseWriter, r *http.Request) {
	request, err := pw.Parse[renameRequest](r)
	if err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	...
}
```

### What is reachable

Every exported handler-shaped function in a route package gets an endpoint,
whether or not a template mentions it. That sounds broad until you remember what
a route package is: nothing imports it but the generated registry, so its
exported symbols are that route's surface rather than a general API.

To keep one private, lowercase it. That is the opt-out, and it needs no
declaration, because generated code in another package cannot reach an
unexported symbol. `Load` is excluded — it is the page's own entry point.

### Addresses

`/_action/<hash>/<HandlerName>`. The hash is the leading 12 hex digits of a
digest over the declaring directory and the handler name. There is no build
salt, so regenerating an unchanged project reproduces the same address and a page
left open across a deploy posts somewhere the server still recognizes. The
readable name rides along, so a network trace names the Go function that ran.

The digest covers the declaring directory rather than the serving path, which
matters for layouts: a layout compiles once and renders under every page below
it, so hashing the route path would give one handler a different address per page
and destroy the determinism the hash exists for.

An address hides structure but grants nothing. It is not a capability token, so
each handler still authenticates and authorizes its own caller. The generated
`Actions` table lists every endpoint, which is what makes that surface
inspectable rather than implicit.

## The route table

The registry publishes what the filesystem knows, and deliberately nothing else.

```go
var Routes = []RouteInfo{
	{Pattern: "GET /{$}", Path: "/", Dir: "", Params: nil},
	{Pattern: "GET /users/{id}", Path: "/users/{id}", Dir: "users/id_", Params: []string{"id"}},
}

var Actions = []ActionInfo{
	{Pattern: "POST /_action/00369cf962b6/Rename", Path: "/_action/00369cf962b6/Rename",
		Dir: "users/id_", Handler: "Rename", Hash: "00369cf962b6"},
}
```

A sitemap or a route inspector is built from `Routes`. The pattern, the method,
and which segments are dynamic all come from the tree. Which values a dynamic
segment actually takes does not: that is application data, and the framework has
no way to know it, so a sitemap over `/users/{id}` is yours to expand.

## Commands

Which routers a project starts with is a bootstrap answer, not a permanent one:

```bash
pw init mysite --router=discovered
```

`registered` writes the handler package, `discovered` writes the page tree, and
`both` writes both onto one mux. Choosing wrong costs a command rather than a
rewrite, because `pw add discovered` and `pw add registered` install the other
one into an existing project.

Adding a route to a tree you already have is `pw new page`. It asks for the URL,
converts it to directories (`/users/{id}` → `users/id_/`), validates every
segment before writing anything, and asks which rung you want. Then it stops.
There is nothing to wire up, because nothing registers a page.

One name in all of this is arbitrary. The tree root is `pages` because that is
what `pw init` writes; generation reads the purpose list instead:

```toml
[generate]
handlers = ["handlers"]
templates = ["templates"]
pages = ["pages"]
```

Rename the directory, edit the entry, and everything follows — generation, the
generated package names, `pw new`, and the `pw dev` reload. A tree root is never
also listed under `templates`: the tree run already compiles its page and layout
templates, and the flat run would claim the same output with different content.

`pw dev` picks up a new route with no extra configuration. Its walk compares
files rather than subscribing to events, and a route always arrives with the
template that makes it one.

## Two conventions this router does not follow

- **Route groups without a URL segment.** The bracket spelling other frameworks
  use is not a legal import path element.
- **Richer catch-all typing.** A catch-all binds as a string.

These are the same collision twice: where Go's rules and routing convention
disagree, Go's rules win.

Actions are not on that list. A `<form server-action>` posts to the page's own
path and answers `303` with no JavaScript at all; with the runtime loaded the
submit is intercepted and the response applied in place; and the token is in the
form and on every request the runtime issues, so nothing is left to wire by
hand. See [Server actions](/guides/interactivity/server-actions/).

What that leaves is not a gap so much as a rule: a page renders and a link
navigates with no JavaScript, and a form action keeps that true. A bare
`server-action` on a button does not, because nothing in HTML invokes a button
outside a form — which is a choice the page makes rather than a limit it meets.
