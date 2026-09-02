# Change request: cache key generation, and method conversions now and later

**From:** Popcorn Web (`github.com/shibukawa/popcornweb`)
**Against:** `github.com/shibukawa/tinybind-go` v0.5.8
**Date:** 2026-08-13
**Status:** answered in v0.5.9 — Ask 1 implemented as `cachekeybind` with two changes, Ask 2 implemented, Ask 3 filed unacted as requested. Integrated downstream 2026-08-13. Ask 3 then landed in v0.5.28 on 2026-09-02, once Go 1.27 and TinyGo 0.42 shipped, with the function forms removed rather than deprecated; integrated downstream the same day.

## Division of responsibility

Every part of this cache is Popcorn Web's except one. The table is here first so that the size of the ask is not in doubt.

| Part of the mechanism | Owner | Status |
| --- | --- | --- |
| Call surface — `MemoStore`, `Memo`, `MemoHas`, `MemoSet`, invalidation | Popcorn Web | shipped |
| `CacheKey` and `CacheTagger` interfaces | Popcorn Web | shipped |
| Store definitions and their configuration section (`[[cache.stores]]`) | Popcorn Web | shipped |
| Store lifetime, name resolution, configuration validation | Popcorn Web | shipped |
| Entry storage, fresh and stale deadlines, entry cap, eviction | Popcorn Web | shipped |
| Miss coalescing and the detached fetch | Popcorn Web | shipped |
| Scope value, the private default, anonymous refusal | Popcorn Web | shipped |
| Stored key assembly — scope, build identity, key method output | Popcorn Web | shipped |
| Invalidation by key, by scope prefix, by tag | Popcorn Web | shipped |
| Value codec | Popcorn Web | shipped |
| Counters | Popcorn Web | shipped |
| Key framing helpers — `KeyString`, `KeyInt`, … | tinybind-go | **exists; reused unchanged, no change requested** |
| Configuration binding generation (`configbind`) | tinybind-go | **exists; used unchanged, no change requested** |
| **`CacheKey() string` generated from struct tags** | **tinybind-go** | **the one thing this document asked for — shipped in v0.5.9 as `cachekeybind`** |

**Asks 2 and 3 below are not part of this cache.** They are method-shape tidying in `htmlbind`, `firestorebind`, `dynamobind`, `jsonbind`, and `sqlbind`, unrelated to anything above, and separable from Ask 1 in every way — treat them as a second document if that is easier to schedule.

## Summary

Popcorn Web has shipped a **data result cache**: what a handler fetched, reused for equal typed keys. It is the other half of your component output cache — that one stores rendered bytes and never touches the query that produced the parameters, and this one stores what the query returned. It reuses your key framing unchanged and diverges everywhere the two caches' misses cost different things.

Building it left exactly one hole we cannot fill downstream, plus a small tidying that needs no language change, plus a list that needs one.

1. **`cachekeybind` — a generator emitting a cache key method from struct tags.** Blocking. Every key in a Popcorn Web application is hand-written today, and the thing being hand-written is a correctness surface.
2. **Three `htmlbind` entries that can become methods in today's Go.** No language change, no behaviour change, and it removes an inconsistency a generated plan currently reads two ways.
3. **The generic-method conversions, deferred.** Filed for planning only. **Please do not act on these yet** — see the trigger in Ask 3.

There is also a section at the end that is *not* an ask: what we learned building a stale window and tag invalidation, offered against the open questions in your `requirement:component-output-cache`.

## What we built, so the asks have context

```go
store, err := pw.MemoStore(ctx, "upstream")

summary, err := pw.Memo(ctx, store, UserSummaryKey{UserID: id, Page: n},
    func(ctx context.Context) (Summary, error) { return fetchUpstream(ctx, id, n) })
```

Stores are named in configuration the way database pools are, carrying TTL, stale window, scope, entry cap, backend, and a fetch timeout. The call site holds a handle and supplies only the key and the fetch.

Four things we took from you unchanged: the `Key*` framing helpers, the scope-prefix position, the private-by-default posture, and the rule that a cache write failure never fails a response that already succeeded.

Four things we deliberately did differently, each because a duplicate fetch costs an upstream call where a duplicate render costs local CPU:

| | Component cache | Data cache |
| --- | --- | --- |
| Concurrent misses | each renders, by design | coalesced onto one fetch |
| Entry | bytes + one deadline | bytes + fresh and stale deadlines + tags |
| Invalidation | TTL only | TTL, by key, by scope prefix, by tag |
| Store interface | `Get`/`Set` | a superset, because a reverse index does not fit `Get`/`Set` |

**Your refusal to coalesce is right for rendering, and we did not reverse it lightly.** Your stated ground is that coalescing inside the runtime would tie one request's cancellation to another request's work. We answered the objection rather than overruling it: the shared fetch runs on a context with cancellation removed and values kept, so a waiter that goes away stops waiting and stops nothing else. That is why the store also carries a fetch timeout — detaching removes the only thing that would otherwise have bounded it.

---

## Ask 1 — a cache key generator (`cachekeybind`)

### Why we cannot build it here

A key type implements one method:

```go
type CacheKey interface{ CacheKey() string }
```

The method returns the entry's identity followed by the framed encoding of every field the result depends on. Hand-written, that is:

```go
func (k UserSummaryKey) CacheKey() string {
    return pw.KeyString("app.UserSummaryKey/v1") + pw.KeyString(k.UserID) + pw.KeyInt(k.Page)
}
```

Three problems with leaving it hand-written, in increasing order of seriousness:

1. It is noise proportional to the field count.
2. The identity prefix is load-bearing and easy to omit. Without it, two key types holding equal field values reach one entry — a wrong answer, not a stale one.
3. **A field added to the struct and forgotten in the method is a silently wrong cache.** The key must contain the whole dependency set; a compiler that cannot see the requirement cannot enforce it.

Only generation closes the third one, and generation over application Go types is yours.

### The shape we are asking for

The same shape your `dynamo` tag already has: a tagged type yields methods, with a compile-time assertion against the runtime interface, and emission is usage-directed.

```go
type UserSummaryKey struct {
    UserID string
    Page   int
    trace  string `cache:"-"`
}
```

emitting

```go
func (k UserSummaryKey) CacheKey() string {
    return cachekeybind.KeyString("example.com/app.UserSummaryKey/v1") +
        cachekeybind.KeyString(k.UserID) +
        cachekeybind.KeyInt(k.Page)
}

var _ cachekeybind.CacheKey = UserSummaryKey{}
```

### Default-include, with `cache:"-"` to exclude

We would like every exported field in the key by default, rather than opt-in per field. The reason is the failure asymmetry, the same one that put your `scope` default on `private`:

- A field that should have been in the key and was not → **one caller's result served to another**.
- A field that need not have been in the key and is → **a cache miss**.

Under opt-in, forgetting a tag is the first failure. Under default-include, forgetting one is the second. The undeclared state should sit where forgetting is slow rather than wrong.

Field types we need covered are exactly the ones your existing helpers frame: `~string`, `bool`, integers, floats, `[]byte`, `time.Time`, pointers, and slices of those. A field whose type has no framing is a **generation error**, not a skipped field — a skipped field is the first failure above, arriving quietly.

### The one sub-question we would like you to decide

The identity has two parts. The type's package path and name we would like **derived**, since it is unique by construction and nothing is gained by making an author restate it.

The **version** is the open one. It has to be raised by hand when the meaning of a result changes while its key does not — a reshaped payload, a corrected computation, a migrated upstream schema — and Go gives a struct no declaration site for it. We do not have a preference strong enough to state as a request:

- a marker field, `_ struct{} `cache:"version=2"``, which is ugly but local;
- a companion const the generator reads by name;
- an optional `CacheVersion() int` method the author writes when a bump is needed, absent meaning 1.

The third is the least intrusive and the easiest to forget. Your call.

### Where the framing helpers should live (low priority)

They are in `htmlbind` today. An application caching an upstream JSON call has no template in it, and importing an HTML render runtime to frame an integer reads oddly. We re-export them from `pwruntime`, so no Popcorn Web application sees this — it is a tidiness note, not a problem we have.

If `cachekeybind` lands as its own package, the helpers moving there with `htmlbind` aliasing them would be the natural home. If that costs anything at all, leave them.

---

## Ask 2 — three entries that can be methods in today's Go

`Builder[P]` reads as methods (`Static`, `Text`, `Raw`, `Attr`, `URLAttr`, …) except where an entry carries an extra type parameter. Three entries are in the function form **without** carrying one, so today's Go already allows them as methods:

```go
// ops.go:390 — introduces no type parameter beyond the receiver's own P
func Require[P any](check func(P) error) Op[P]
// could be:
func (Builder[P]) Require(check func(P) error) Op[P]

// plan.go:268 — P is the plan's own
func Bind[P any](plan *Plan[P], params P) Fragment
// could be, beside the existing Exec and Sequence:
func (p *Plan[P]) Bind(params P) Fragment

// render.go:43
func BindWrapper[P any](plan *Plan[P], params P, setChildren func(*P, Fragment)) Wrapper
// could be:
func (p *Plan[P]) BindWrapper(params P, setChildren func(*P, Fragment)) Wrapper
```

`*Plan[P]` already carries `Exec` and `Sequence`, so this adds no new receiver.

We are asking because these three sit in the same list as the genuinely blocked ones, and bundling them there would hide that they are available now. Keeping the existing functions as deprecated wrappers costs nothing and lets a generated plan move when it is convenient.

---

## Ask 3 — the generic-method conversions (**deferred: do not act yet**)

**Trigger:** a Go release where a method may declare its own type parameters, **and** a TinyGo release carrying it. Both. Popcorn Web targets TinyGo and WebAssembly, so a conversion available only on upstream Go would split our build rather than tidy it. Our current expectation is Go 1.27 with TinyGo 0.42, and we will re-file against the releases that actually ship it.

Listing them now only so the shape is on record when the trigger arrives. Priority order:

**1. `firestorebind.Tx` — the only one whose value is more than tidiness.**

Writes are already methods; typed reads are not, so one transaction is written two ways in adjacent lines:

```go
tx.Store(user)
user, err := firestorebind.LoadTx[User](ctx, tx, key)   // today
user, err := tx.Load[User](ctx, key)                    // after
```

Covering `LoadTx`, `LoadAllTx`, `QueryPageTx`. The transaction boundary stops being an argument and becomes the receiver.

Note that `LoadTx`'s own comment gives **two** reasons for the function form, and only one of them is the language. The other — that a context-carried handle would make one call site mean two things depending on which context reached it — survives the change untouched, so the operation should still be reached through the transaction value rather than through a context.

**2. The `*On` entries on `Handle`.** `Handle` is a concrete type, so the explicit form becomes a method while the context-resolving form stays exactly as it is:

```go
dynamobind.LoadOn[Reading](ctx, h, "readings", key)   // today
h.Load[Reading](ctx, "readings", key)                 // after
h.Store(ctx, "readings", reading)                     // type inferred from the value
```

- DynamoDB: `LoadOn`, `LoadAllOn`, `StoreOn`, `StoreAllOn`, `StoreReturningOn`, `RemoveOn`, `RemoveReturningOn`, `UpdateOn`, `QueryPageOn`, `QueryOn`, `ScanPageOn`, `ScanOn`
- Firestore: `LoadOn`, `LoadAllOn`, `StoreOn`, `StoreAllOn`, `InsertOn`, `InsertAllOn`, `UpdateOn`, `RemoveOn`, `RemoveAllOn`, `QueryPageOn`, `QueryOn`

This matters to us more than it looks: Popcorn Web wraps none of these, so they are what an application author actually writes.

**3. `htmlbind.Builder[P]` — `For`, `ForCtx`, `Await`, `Live`, `Provide`.** Generated plans stop mixing two spellings. Least visible of the four, since no application reads the output.

**4. `jsonbind.Parser` — `ParseSlice`, `ParseMap`.** The same shape as the firestore transaction: a struct with a dozen methods, and the two operations parameterized on the decoded element standing outside as functions. The caller is your generated action decoders.

**5. `sqlbind.AppendValues`.** One function beside `Builder`'s `Arg` and `Statement`. Last because it is one function.

**Explicitly not on this list:** `sqlbind.ScanRows`. `Rows` is an interface, so no language change lets the package give it a method. It stays a function permanently, and we would rather that be recorded than rediscovered.

Migration shape for all of them: the method becomes the body, the existing function stays as a deprecated wrapper. Nothing stored, generated, or on the wire changes, so no caller is forced to move.

---

## Not an ask — input on your cache's open questions

`requirement:component-output-cache` leaves stale-while-revalidate and explicit invalidation open. We built both at the data layer. Offering what that turned up, in case it is useful when you take them; none of it is a request to change the component cache, whose constraints are genuinely different.

**A stale window changes the entry, not the policy.** One TTL cannot express it: the entry needs a fresh deadline and a last-answerable deadline, and a read between them returns the held value *and* starts one revalidation. Retrofitting that onto a `[]byte` plus one duration means changing the store interface, which is why we did not reuse `CacheStore` and built a superset instead.

**Tag invalidation needs a reverse index.** Prefix deletion covers the scope axis for free — your decision to *prepend* the scope is what makes that a range rather than a scan, and it paid off exactly as the comment predicted. It cannot cover the other axis: with the scope first, "every entry of one key type across all readers" is not a prefix. A tag index is what answers it, and it does not fit `Get`/`Set`.

**Coalescing.** Covered above. The short version: your objection is real, and detaching the fetch from every waiter is what removes it. If you ever revisit this for rendering, the mechanism transfers; the cost-benefit does not, and we think your default is still right for a render.

**The plan fingerprint has no equivalent for hand-written code.** Your ID digests the emitted instruction list, so regenerated markup cannot read old entries. A Go function compiles to nothing we can digest, so we use the build identity plus an author-declared version — which is exactly why Ask 1 matters. It is the weakest part of our design and the part generation would strengthen most.

## Outcome (added 2026-08-13, after v0.5.9)

Ask 1 shipped as `cachekeybind`, with two decisions that went against this document and one that answered a question it delegated:

- **Marking is opt-in (`cache:"key"`), not default-include.** The failure-asymmetry argument here assumed a purpose-built key struct where every field is a plausible dependency. It does not survive the input shape the owner wants to support: a storage entity passed to the cache as-is, whose fields are mostly the *result*. Over that input, default-include would build the key out of the value the lookup exists to avoid fetching — unusable rather than merely slow. The argument in this document was wrong about its own premise.
- **No version at all.** All three spellings offered here share the defect the third was rejected for: a number someone has to remember to raise. The module also states that its cache runtime never invalidates, so a version is a deployment lever declared in a library. The build identity already covers the common case.
- **A precedent cited here was half-checked.** `dynamo` is default-include, but `firestore` — the sibling not named — is opt-in. The module already carried both polarities, so the precedent decided nothing, and the half named lost anyway.

Also shipped: Ask 2, with the old functions kept as deprecated wrappers. Ask 3 was filed and untouched, as requested, and shipped in v0.5.28 on 2026-09-02 with the function forms removed rather than deprecated.

One safeguard added beyond the ask: a struct reached as a key with no marked field is a generation error, since an identity-only key would give every instance of one type a single shared entry.
