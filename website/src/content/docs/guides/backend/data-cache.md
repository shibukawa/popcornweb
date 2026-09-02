---
title: Caching Fetched Data
description: Reusing what an upstream call returned for the same typed question, with concurrent misses collapsed onto one call and a private default that keeps one reader's data off another's screen.
sidebar:
  order: 7
---

A handler calls a currency service, an inventory API, a report endpoint. The
answer is the same for a minute at a time, and the page is requested far more
often than that. Every request pays the round trip anyway.

`Get` on a store holds what the call returned and replays it:

```go
quote, err := store.Get(r.Context(), QuoteKey{Pair: pair},
    func(ctx context.Context) (Quote, error) { return fetchQuote(ctx, pair) })
```

The store is what `pw.MemoStore` resolved from the configuration, and every
operation — `Get`, `Has`, `Set`, and the invalidations — is a method on it.

There are two places to put a cache like this, and the other one is
[Rendering Cache](/guides/frontend/rendering-cache/#caching-a-components-own-load).
A component that takes an identifier and loads its own record covers the load
and the markup under one `@cache`, with no store to configure — reach for that
first when it fits, because one annotation replaces everything on this page.

It fits less often than it looks. A component's loader is a synchronous
external, so it cannot report a failure; the component cache has a TTL and no
stale window and no invalidation; and its entries live in the render store,
sized for markup. Use the data cache when the load can fail and the reader must know,
when a write has to drop an entry before it expires, when an upstream outage
should be survived rather than propagated, or when the value is wanted somewhere
no component reaches.

Know what it is not for before reaching for it. **The entry is a round trip
through JSON.** A value your handler computes locally in a microsecond pays a
key, an encode, and a decode in exchange for work it never did. This is worth
having when something leaves the process — a network call, a slow query, a
report — and worth nothing when the expense was a loop over a slice you already
had.

## A handler that calls an upstream once a minute

`pwconfig.toml`:

```toml
[cache]
enabled = true

[[cache.stores]]
name = "rates"
ttl = "1m"
scope = "public"
max_entries = 4096
```

`handlers/quote.go`:

```go
package handlers

import (
	"context"
	"net/http"

	"github.com/shibukawa/popcornweb/pw"
)

// Quote is what the upstream returns and what one entry holds.
type Quote struct {
	Pair  string  `json:"pair"`
	Value float64 `json:"value"`
}

// QuoteKey is the question rather than the answer. A marked field is part of
// the key; an unmarked one is not.
type QuoteKey struct {
	Pair string `cache:"key"`
}

func ShowQuote(w http.ResponseWriter, r *http.Request) {
	store, err := pw.MemoStore(r, "rates")
	if err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	pair := r.PathValue("pair")
	quote, err := store.Get(r.Context(), QuoteKey{Pair: pair},
		func(ctx context.Context) (Quote, error) {
			return fetchQuote(ctx, pair)
		})
	if err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	pw.WriteAPI(w, r, quote)
}

// fetchQuote is your own call to the upstream service.
func fetchQuote(ctx context.Context, pair string) (Quote, error) {
	return Quote{Pair: pair, Value: 1.0}, nil
}
```

`pw generate` writes `QuoteKey`'s key method into `cachekey_pw_gen.go`. What
makes it a key type is the `store.Get` call above: generation follows the call
site to the argument beside the result, so a marked struct nothing passes to the
cache generates nothing.

Marking is opt-in, and that is the opposite of the `json` tags on `Quote`. The
struct you hand the cache is often the entity you already have, where most
fields are the *answer* — building a key out of those would mean assembling it
from the value the lookup exists to avoid fetching. So you mark the query and
leave the rest.

Nothing in `fetchQuote` knows it is cached. `store.Get` wraps the call rather than
replacing it, which is also how caching is removed later: delete the wrapper, or
set `enabled = false` and every call site falls straight through to its own
function without a single edit.

A store is configured rather than constructed because it outlives every request
and carries an operational size — the same reasons a database pool is
configured. `pw.MemoStore` resolves one by name, and a name the configuration
does not hold is an error naming what it does, because a typo that silently
never cached would look exactly like a cache that was working.

## Whose answer is this

`scope` takes `"private"` or `"public"`, and omitting it means `"private"`.

The example above declares `public` deliberately: an exchange rate is the same
for everybody who asks, so one entry serves every reader. Write that only when
the result is a function of the key and nothing else.

Everything else stays private, and the default is a security boundary rather
than a tuning knob. A private entry's key is prefixed with the identity of the
reader it was fetched for — `pw.RequestAuthentication(r).Subject`, the local
account identifier a session login, a passkey assertion, and a bearer token all
resolve to — so two readers never reach one entry. Declare `public` on something
that is actually per-reader and the cache hands one person's data to the next
one who asks.

The two mistakes are not comparable. A shared result left private costs hits and
memory. A per-reader result made public discloses somebody's account, and no
error is raised when it happens, because from the cache's point of view nothing
went wrong.

An anonymous request has no such identifier, and a private store reached without
one stores nothing at all. That is a fallback rather than a design: an entry
written under a blank identity would be a shared entry wearing a private label,
and a miss is the better of the two.

## Which store to configure

Two shapes cover most of it, and the upstream's failure behaviour picks between
them.

**A store with a `ttl` alone** is the one to start with. The entry is fresh for
the duration and a miss calls the upstream. Reach for this whenever the upstream
is reliable and the data has an obvious freshness bound.

**A store that also declares `stale`** keeps answering past the TTL while one
revalidation runs behind the reader's back. Configure it when the upstream is
something you do not control and a page must survive its outage: inside the
stale window a failed revalidation leaves the held value in place until the
window closes, so a five-minute upstream incident degrades the page instead of
breaking it. It costs you data that is knowingly out of date, which is the wrong
trade for a balance and the right one for a catalogue.

```toml
[[cache.stores]]
name = "inventory"
ttl = "30s"
stale = "5m"
```

Configure one store per policy rather than one per call site. Several unrelated
caches sharing `max_entries` evict each other, and the entry cap is per store.

## Concurrent misses call the upstream once

Ten requests arriving on a cold key do not make ten upstream calls. The first
starts the fetch and the rest attach to it, which is the difference between a
cache and a cache that takes the upstream down every time an entry expires.

That has one consequence at the call site, and it is the thing most likely to
catch you:

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

The shared fetch runs on a context deliberately detached from every waiter, so
that a reader who closes their tab stops waiting without cancelling work the
other nine still need. The context handed to your function is that detached one.
Capture the request's context instead and you have pinned the shared call to
whichever reader happened to miss first; when they disappear, everybody's fetch
fails. The detached context keeps values — the reader identity, the trace — and
loses only the deadline, which is why the store carries `fetch_timeout` of its
own.

## Invalidating after a write

A TTL alone leaves open exactly the window the writer already knows is wrong.
When your own handler is what made an entry stale, say so:

```go
store.Invalidate(ctx, QuoteKey{Pair: pair})
```

Two coarser forms exist for the cases one key cannot express.
`store.InvalidateScope(subject)` drops everything one reader holds, which is
what a sign-out or an account change wants. `store.InvalidateTag` drops everything a tag names, for a write that invalidates entries under several
keys at once; a key type declares its tags by implementing `pw.CacheTagger`
beside its `CacheKey` method.

Nothing is invalidated implicitly. The framework does not know which read a
write contradicts, and guessing would be worse than not trying.

## Where this trips people

**A dependency the key does not carry is a wrong answer, not a stale one.**
This is the hazard opt-in marking leaves open, and the one thing generation
cannot check for you: it guarantees the spelling of the key you declared, never
its completeness. A fetch that reads a tenant, a locale, or a feature flag out
of the context while only the ID is marked will serve one tenant's data to
another, indefinitely, and every test that exercises one tenant will pass. When
a fetch gains a dependency, the field beside it gains a `cache:"key"`.

**Two different questions need two key types.** Entries are kept apart by the
key type's own name, which generation writes into the method, so
`QuoteKey{Pair: "x"}` and an `OrderKey` holding the same string do not collide.
One entity you look up two different ways is two structs, not one struct with
two sets of marks.

**Reordering marked fields empties the cache.** The key is built in declaration
order, so moving a marked field past another changes every key the type
produces. The old entries go cold rather than wrong, and they expire on their
own — worth knowing before it is diagnosed as a bug.

**Never cache a write, and never cache a read taken inside a transaction.** The
first is obvious once stated; the second is not. A read inside a transaction is
true only within that transaction, and storing it publishes a value the
surrounding rollback was supposed to erase.

**`store.Has` is racy by construction.** It answers whether an entry is fresh
*now*, and the entry can expire before the next line runs. Use it for a
diagnostic or to decide whether to start expensive work, never as a guard that
assumes the following `store.Get` will hit.

**Entries do not survive a restart, and replicas do not share them.** The store
is in-process. Three instances behind a load balancer hold three independent
caches and will each call the upstream once per key. Size `max_entries` for one
process, not for the fleet.

[Application Configuration Keys](/reference/configuration/#cache) lists every key with its
default, and [Rendering Cache](/guides/frontend/rendering-cache/) covers the
other half of a slow page.
