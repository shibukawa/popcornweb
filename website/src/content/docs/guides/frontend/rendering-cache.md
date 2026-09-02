---
title: Rendering Cache
description: Storing a component's rendered bytes with @cache, and the scope declaration that decides both its cache key and the response's cache policy.
sidebar:
  order: 6
---

A handler that already holds its data still pays to turn that data into HTML.
The component walks the rows, escapes every field, and writes the same bytes it
wrote for the visitor before. On one catalogue page that work is small. Under
load, repeated per request, it stops being small.

`@cache` stores those bytes and replays them:

```html
@cache(ttl: "5m", scope: "public")
component ProductGrid(rows: Product[]): html { … }
```

Know what that does not buy before you reach for it. **A hit skips exactly what
the component does, and nothing above it.** The handler computed `rows` before
it called `ProductGrid`, so the query still runs on every request and only the
markup is replayed. Written this way the annotation is worth having when the
markup is the expense — a long table, a rendered article, a tree walked into
nested lists — and worth nothing when the database call is. A component that
renders four fields into a heading gains a key computation and a buffer in
exchange for work it never did.

That boundary is a property of where the work sits, not of the annotation. Move
the load inside the component and the same hit skips the load too, which is
[Caching a component's own load](#caching-a-components-own-load) below.

The second half of the annotation is the half that decides how safe any of this
is, and it is on by default whether you write it or not.

## A page that caches its expensive half

`handlers/catalog.pw.html`:

```html
package handlers

type Product {
  name: string
  price: string
}

@cache(ttl: "5m", scope: "public")
component ProductGrid(rows: Product[]): html {
<ul>{for row in rows}<li>{row.name} — {row.price}</li>{/for}</ul>
}

export component Catalog(rows: Product[]): html {
<main><h1>Catalog</h1><ProductGrid rows={rows} /></main>
}
```

`handlers/catalog.go`:

```go
package handlers

import (
	"net/http"

	"github.com/shibukawa/popcornweb/pw"
)

func ShowCatalog(w http.ResponseWriter, r *http.Request) {
	rows, err := store.Products(r.Context())
	if err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	pw.WriteHTML(w, r, Catalog(CatalogParams{Rows: rows}))
}
```

Nothing in the handler mentions the cache. The annotation compiles into the
generated plan, and the store behind it is on by default, so the second request
inside five minutes replays `ProductGrid` instead of running it. `Catalog` keeps
rendering normally: an annotation covers the component that carries it, not the
page it happens to sit on.

The key is the component's identity, a fingerprint of its generated plan, and
every declared parameter. Editing the template changes that fingerprint, so
markup you have changed cannot be answered from entries the previous build
wrote. A parameter you change is a different key rather than a stale hit.

What the page tells caches in front of it has not moved, though. `scope:
"public"` here says the *entry* is shared, and the response still reports
`private` because nothing declared otherwise — the two travel together only when
the declaration sits on the document shell, which the next section explains.

## Caching a component's own load

Give the component the identifier instead of the rows, and let it load them:

```html
package handlers

external LoadProduct(id: string): Product

@cache(ttl: "5m", scope: "public")
component ProductCard(id: string): html {
{val product = LoadProduct(id)}
<article>
  <h2>{product.name}</h2>
  <p>{product.description}</p>
  <p>{product.price}</p>
</article>
}
```

`{val …}` names the result once; without it each of the three fields would be
its own call. [`val`](/reference/template-syntax/#val--naming-a-value) covers
the binding itself.

The key is computed from `id`, and the stored bytes are the whole rendered
subtree. **A hit therefore skips `LoadProduct` as well as the markup** — not
because the annotation learned to cache data, but because the load is now part
of what the component does, and a replayed component does nothing. Nothing else
is configured; there is no second cache here.

This is the shape worth reaching for. Caching markup alone saves an escape and
a buffer, which is real but small; caching a subtree that loads saves the round
trip that dominated the request. The same annotation, moved one layer, is worth
an order of magnitude more.

Two conditions decide whether a component can be written this way.

**The loader must be a synchronous `external`, and a synchronous external has
no error result.** It returns a value or it returns a zero one; it cannot tell
the page that the lookup failed. That suits a read with a sensible empty answer
and does not suit one whose failure the reader must see. Making it `external
async` does not rescue it: an async call needs an `await` boundary, and a
storing `@cache` is refused on any component that reaches one.

**The load blocks the render.** An `await` boundary would have streamed a
fallback while the work ran, and this does not — the component renders when the
data arrives. On a miss you have traded first-paint latency for the hits that
follow, which is the right trade for a card on a listing page and the wrong one
for the primary content of a page nobody revisits.

When either condition fails, leave the fetch in the handler and cache it there
with [the data cache](/guides/backend/data-cache/). That store also has what this one
does not: a stale window that keeps answering through an upstream outage, and
explicit invalidation for a write that you know made an entry wrong. `@cache`
has a TTL and nothing else, so an entry is wrong until it expires.

## Who the output belongs to

`scope` takes `"private"` or `"public"`, and omitting it means `"private"`.

The default is a security boundary. A recurring cache incident starts with a
page such as `/account`, whose URL is the same for every signed-in reader while
its HTML contains a name, orders, permissions, or another reader-specific
value. If that output is declared public, the component cache can replay one
reader's markup to another. When the document response is public, a CDN or
reverse proxy may also store that page by URL and expose the first cached
reader's screen to everyone who follows. Authentication still ran for the first
request; the shared cache is precisely what lets later requests bypass the
distinction.

For that reason Popcorn Web makes the forgotten declaration land on the safe
side. Treat a component as private until you can show that its output, for the
same declared parameters, is safe for **any** reader to receive. Promote only
those components to `public`: a common catalogue, public article, shared icon,
or other markup that contains no account, tenant, authorization result, or
hidden request-context value. A public component accidentally left private
costs cache hits and memory. A private component accidentally made public can
disclose a person's screen. Those failures are not comparable.

A private component's key is prefixed with the identity of the reader it
rendered for, so two readers never reach one entry. That value is
`pw.RequestAuthentication(r).Subject`, the local account identifier a session
login, a passkey assertion, and a bearer token all resolve to before any handler
runs. One person's entries therefore stay one person's however they signed in,
and adding a second login method partitions nothing that already existed. It is
deliberately not the session token, which rotates.

An anonymous request has no such identifier, and a storing private component
rendered without one stores nothing at all. That is a fallback rather than a
design — an entry written under a blank identity would be a shared entry wearing
a private label, and a miss is the better of the two.

The same declaration decides what the response tells caches in front of you,
and there it is read from the chain rather than from the render. It has to be:
`Cache-Control` is on the wire before the first body byte, while a per-reader
component four levels down renders long after that.
[Responses](/guides/frontend/responses/#cache-policy) covers the header itself.

## Which form to write

Three shapes come up, and the parameter list usually picks for you.

**A component that is identical for everybody** takes both arguments:
`@cache(ttl: "5m", scope: "public")`. Write `public` only when the output is a
function of the declared parameters and nothing else. If the component calls a
Go function that reads the reader out of the context, its parameters do not
describe its output, and generation cannot see that from the call graph.

**A component that differs per reader** takes the `ttl` alone and inherits
`private`. A dashboard summary keyed by account is the case that pays: the
render is expensive, the reader returns within the window, and their entry is
theirs. Weigh it against what it costs the store before adopting it widely —
private keys multiply entries by the number of active readers, and the entry cap
is one number for the whole process.

**A component that stores nothing but still has a scope** takes `scope` alone.
Without a `ttl` the annotation stores nothing and computes no key, which lets it
sit where storage cannot: a layout, the document shell, a page that awaits.

That third form does the work the other two cannot. On a document shell,
`@cache(scope: "public")` is how a marketing site tells shared caches its pages
are shared — one annotation covering everything below it:

```html
@cache(scope: "public")
export component Document(children: html?): html { … }
```

Pointed the other way, it states what static analysis will never find. A
component whose Go function reads request identity from `ctx` looks shared to
every check either side of the toolchain can write, and a bare
`@cache(scope: "private")` on it turns the author's knowledge into a fact the
call graph carries — one that vetoes any `public` claimed above it.

## Where this trips people

**A public claim only counts from the outside in.** A wrapper contains
everything below it, so a `public` declaration decides the response only on the
outermost member of the chain. A page asserting `public` under an undeclared
layout stays private, because the layout's own markup is in the response too and
nothing ever declared it. Put the annotation on the shell.

**Private always wins.** `@cache(scope: "public")` on a component whose call
graph reaches a declared private one fails generation, at the annotation, naming
the component that declared it. A chain assembled at run time through
`pw.WriteHTMLChain` never appeared in a call graph, so generation cannot refuse
it; the response comes out private anyway and the framework logs which component
made it so:

```
WARN chain declaring public rendered private declared_by=pages/account.pw.html:PlanSummary
```

**A cache nobody hits still costs.** A component whose parameters differ on
every call computes a key and renders into a buffer to store an entry no one
reads. Nothing about the response looks wrong, which is why the render span
carries both halves — `pw.render.cache_hits` and `pw.render.cache_misses`, in
[Tracing](/guides/architecture/telemetry/#reading-a-request-trace). A private component on a page most
visitors see signed out reports the same shape, for the different reason that
anonymous renders store nothing.

**Some components cannot store at all.** A storing `@cache` is refused on
anything whose bytes could not stand in for a fresh render: an `html` or `async`
parameter, an `await` boundary anywhere beneath it, the document `head`, an
unsafe `<form>`, or a provider-backed builtin element. Each is a generation
error rather than a run-time surprise, so `pw generate` reports it with the
declaration's position, naming the component that made the component ineligible
when that is not the one you annotated.
[`@cache`](/reference/template-syntax/#cache) lists them in full.

## Turning it off, and sizing it

```toml
[html]
[html.cache]
enabled = true
max_entries = 1024
```

The store is in-process and on by default, because the annotation is the opt-in:
a project that writes none never reaches it. `enabled = false` is the switch for
an operator who suspects a stale region and wants the question answered without
a rebuild, not the switch that makes the annotation mean something.

`max_entries` is worth revisiting the moment anything is scoped. The default was
chosen for keys that hold one entry per parameter set; private keys hold one per
parameter set **per reader**, and eviction is approximate insertion order, so a
cap sized for the shared case thrashes once a per-reader component joins it.

A redraw renders through the page's own options and reaches the same store, so a
component cached on the page stays cached in the response that replaces it.

[`@cache`](/reference/template-syntax/#cache) is the complete annotation
reference, and [Application Configuration Keys](/reference/configuration/#html) lists the keys
above with their defaults.
