# Caching: rendered output and fetched data

Two stores, both in-process, both keyed with a **private default that is a
security boundary**. `@cache` on a component stores rendered bytes; `store.Get`
stores what an upstream call returned. The same annotation also decides what the
response tells caches in front of you.

## `@cache` — a component's rendered bytes

```html
@cache(ttl: "5m", scope: "public")
component ProductGrid(rows: Product[]): html { … }
```

The annotation takes `ttl` and `scope`, and which of them you write decides what
it does. Writing neither is a generation error. Nothing in the handler mentions
the cache — the annotation compiles into the generated plan and the store is on
by default.

**A hit skips exactly what the component does, and nothing above it.** Written as
above, the handler computed `rows` before it called `ProductGrid`, so the query
still runs on every request and only the markup is replayed. That is worth
having when the markup is the expense (a long table, a rendered article, a tree
walked into nested lists) and worth nothing when the database call is.

The key is the component's identity, a fingerprint of its generated plan, and
every declared parameter. Editing the template changes the fingerprint, so markup
you changed cannot be answered from the previous build's entries.

### Caching the load with the render

Give the component the identifier instead of the rows and let it load them:

```html
external LoadProduct(id: string): Product

@cache(ttl: "5m", scope: "public")
component ProductCard(id: string): html {
{val product = LoadProduct(id)}
<article>
  <h2>{product.name}</h2>
  <p>{product.price}</p>
</article>
}
```

The key is computed from `id`, and the stored bytes are the whole subtree — so a
hit skips `LoadProduct` as well as the markup, not because the annotation learned
to cache data but because a replayed component does nothing. There is no second
cache here. This is the shape worth reaching for: the same annotation, moved one
layer, is worth an order of magnitude more.

Two conditions decide whether a component can be written this way.

- **The loader must be a synchronous `external`, which has no error result.** It
  returns a value or a zero one and cannot tell the page the lookup failed. That
  suits a read with a sensible empty answer. Making it `external async` does not
  rescue it: an async call needs an `await` boundary, and a storing `@cache` is
  refused on any component that reaches one.
- **The load blocks the render.** No fallback streams while it runs. On a miss
  you traded first-paint latency for the hits that follow — right for a card on a
  listing page, wrong for the primary content of a page nobody revisits.

When either fails, leave the fetch in the handler and cache it with the memo store,
which has the stale window and the explicit invalidation `@cache` does not.

### `scope` — who the output belongs to

`scope` takes `"private"` or `"public"` and defaults to `"private"`.

A private component's key is prefixed with `pw.RequestAuthentication(r).Subject`
— the local account identifier a session login, a passkey assertion, and a bearer
token all resolve to before any handler runs. It is deliberately not the session
token, which rotates. An anonymous request has no such identifier, and a storing
private component rendered without one **stores nothing at all**.

Write `public` only when the output is a function of the declared parameters and
nothing else. If the component calls a Go function that reads the reader out of
the context, its parameters do not describe its output, and generation cannot see
that from the call graph. A public component left private costs cache hits; a
private component made public discloses a person's screen. Those are not
comparable.

| Declaration | Cache key | What the response reports |
| --- | --- | --- |
| none | parameters | private |
| `scope: "private"` | reader identity + parameters | private, and refuses a `public` declared around it |
| `scope: "public"` | parameters | shared, unless something else in the chain declares private |

### The three forms

- **`@cache(ttl: …, scope: "public")`** — identical for everybody, and expensive.
- **`@cache(ttl: …)`** — differs per reader, inherits `private`. A dashboard
  summary keyed by account is the case that pays. Private keys multiply entries
  by the number of active readers, and the entry cap is one number for the whole
  process.
- **`@cache(scope: …)` with no `ttl`** — stores nothing, computes no key, and may
  therefore sit where storage cannot: a layout, the document shell, a page that
  awaits. `@cache(scope: "public")` on the document shell is how a marketing site
  tells shared caches its pages are shared. Pointed the other way,
  `@cache(scope: "private")` states what static analysis will never find, and
  vetoes any `public` claimed above it.

A `ttl` on a layout or a document shell is a generation error: the duration would
describe an expiry that cannot happen.

### Where this trips people

- **A public claim only counts from the outside in.** A page asserting `public`
  under an undeclared layout stays private, because the layout's markup is in the
  response too. Put the annotation on the shell.
- **Private always wins.** `scope: "public"` on a component whose call graph
  reaches a declared private one fails generation. A chain assembled at run time
  through `pw.WriteHTMLChain` never appeared in a call graph, so the response
  comes out private anyway and the framework logs which component made it so.
- **A cache nobody hits still costs.** Parameters that differ on every call
  compute a key and render into a buffer to store an entry no one reads. The
  render span carries `pw.render.cache_hits` and `pw.render.cache_misses`.
- **A `check` inside a cached subtree is skipped on a hit.** A component whose
  guard reads anything the key does not carry must not be given a storing
  `@cache`.
- **Some components cannot store at all.** A storing `@cache` is refused on an
  `html` or `async` parameter (or a record reaching one), an `await` boundary
  anywhere beneath, the document `head`, an unsafe `<form>`, or a
  provider-backed builtin element. Each is a generation error naming the
  component that made it ineligible when that is not the one you annotated.

```toml
[html.cache]
enabled = true       # on by default: the annotation is the opt-in
max_entries = 1024   # revisit once anything is scoped private
```

`enabled = false` is the switch for an operator diagnosing a stale region, not
the switch that makes the annotation mean something. A redraw renders through the
page's own options and reaches the same store.

## Response cache policy

Every HTML response says whether a shared cache may hold it, and the default is
no:

```
Cache-Control: private, no-store
```

The answer comes from the **chain**, asked before anything renders, because
`Cache-Control` is on the wire before the first body byte while a per-reader
component four levels down renders long after that. A chain where nothing
declared a scope reports private — the answer a login-gated application gets for
free.

A shared page (a shell declaring `@cache(scope: "public")`) receives no
`Cache-Control` from the framework at all: freshness is a deployment's decision,
so the framework stops asserting rather than asserting something weaker. Set the
lifetime at your CDN or in a middleware of your own.

`no-store` rather than `no-cache` on the private side: a document carries no
entity tag, so there is no conditional request to protect. Other response shapes
keep what they need — a navigation delta and a live delivery are `no-store`, a
redraw is `private, no-cache` (it has an entity tag), a fragment sequence is
`public, max-age=31536000, immutable`. A 429 is always `no-store`.

Before putting a CDN in front of a public site: nothing is shared until a shell
declares it, so a marketing page passes straight through the edge until you write
the annotation.

## The memo store — what a fetch returned

```toml
[cache]
enabled = true

[[cache.stores]]
name = "rates"
ttl = "1m"
scope = "public"
max_entries = 4096
```

```go
// Quote is what the upstream returns and what one entry holds.
type Quote struct {
	Pair  string  `json:"pair"`
	Value float64 `json:"value"`
}

// QuoteKey is the question, not the answer. A marked field is part of the key.
type QuoteKey struct {
	Pair string `cache:"key"`
}

func ShowQuote(w http.ResponseWriter, r *http.Request) {
	store, err := pw.MemoStore(r, "rates")
	if err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	pair := pw.PathValue(r, "pair")
	quote, err := store.Get(r.Context(), QuoteKey{Pair: pair},
		func(ctx context.Context) (Quote, error) { return fetchQuote(ctx, pair) })
	if err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	pw.WriteAPI(w, r, quote)
}
```

`pw generate` writes `QuoteKey`'s key method into `cachekey_pw_gen.go`. What makes
it a key type is the `store.Get` call site — a marked struct nothing passes to the
cache generates nothing. Marking is **opt-in**, the opposite of the `json` tags
beside it: the struct you hand the cache is often the entity you already have,
where most fields are the answer.

A store is configured rather than constructed because it outlives every request
and carries an operational size. `pw.MemoStore` resolves one by name, and a name
the configuration does not hold is an error — a typo that silently never cached
would look exactly like a cache that was working.

`enabled = false` makes every call site fall straight through to its own
function, which is also how caching is withdrawn from a deployment without
editing code.

| Call | What it does |
| --- | --- |
| `store.Get(ctx, key, fetch)` | return the entry, or run `fetch` and store it |
| `store.Set(ctx, key, value)` | store a value you already have |
| `store.Has(ctx, key)` | whether an entry is fresh **now** — racy by construction |
| `store.Invalidate(ctx, key)` | drop one entry |
| `store.InvalidateScope(subject)` | drop everything one reader holds |
| `store.InvalidateTag(tag)` | drop everything a tag names (`pw.CacheTagger`) |

Nothing is invalidated implicitly: the framework does not know which read a write
contradicts.

### Which store to configure

**A `ttl` alone** is the one to start with. **A store that also declares `stale`**
keeps answering past the TTL while one revalidation runs behind the reader's
back, so a five-minute upstream incident degrades the page instead of breaking
it — the right trade for a catalogue, the wrong one for a balance.

```toml
[[cache.stores]]
name = "inventory"
ttl = "30s"
stale = "5m"
```

Configure one store per policy, not one per call site: unrelated caches sharing
`max_entries` evict each other.

### Concurrent misses call the upstream once

Ten requests on a cold key make one upstream call; the rest attach to it. That
has one consequence at the call site:

```go
// Right — the fetch uses the context it is handed.
store.Get(r.Context(), key, func(ctx context.Context) (Quote, error) {
	return fetchQuote(ctx, pair)
})

// Wrong — the fetch captures the request's context instead.
store.Get(r.Context(), key, func(context.Context) (Quote, error) {
	return fetchQuote(r.Context(), pair)
})
```

The shared fetch runs on a context deliberately detached from every waiter, so a
reader closing their tab does not cancel work the other nine need. It keeps
values (reader identity, trace) and loses only the deadline, which is why the
store carries `fetch_timeout`.

### Where this trips people

- **A dependency the key does not carry is a wrong answer, not a stale one.**
  Generation guarantees the spelling of the key you declared, never its
  completeness. A fetch reading a tenant, a locale, or a feature flag from the
  context while only the ID is marked serves one tenant's data to another
  indefinitely. When a fetch gains a dependency, the field beside it gains a
  `cache:"key"`.
- **Two different questions need two key types.** Entries are kept apart by the
  key type's own name, so one entity looked up two ways is two structs.
- **Reordering marked fields empties the cache.** The key is built in declaration
  order; old entries go cold rather than wrong.
- **Never cache a write, and never cache a read taken inside a transaction.** A
  read inside a transaction is true only within it, and storing it publishes what
  the rollback was supposed to erase.
- **Entries do not survive a restart, and replicas do not share them.** Size
  `max_entries` for one process, not the fleet.

## Choosing between the two

Reach for `@cache` on a component that takes an identifier and loads its own
record: one annotation covers the load and the markup with no store to configure.
Reach for the memo store when the load can fail and the reader must know, when a write
has to drop an entry before it expires, when an upstream outage should be
survived rather than propagated, or when the value is wanted somewhere no
component reaches.

**When not to use either:** a value computed locally in a microsecond pays a key,
an encode, and a decode in exchange for work it never did. A component rendering
four fields into a heading gains a key computation and a buffer for the same
reason. Cache what leaves the process, or what is genuinely expensive to render.
