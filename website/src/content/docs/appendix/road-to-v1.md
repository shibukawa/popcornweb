---
title: Road to 1.0
description: The API changes that waited on a Go language feature, landed with Go 1.27, with each call site shown before and after.
sidebar:
  order: 4
---

A version number below 1.0 means what it usually means: the surface can still
move, and a release is allowed to take a name back rather than carry it
forever. One batch of changes was designed early and waited on the language
rather than on this project. It has now landed. This page keeps the record —
each call site as it read before, and as it reads now — so that a project
written against the earlier spelling knows exactly what moved and what did not.

## What was waiting, and on what

Before Go 1.27 a method could not declare its own type parameters. Wherever the
framework needed one — a typed read, a typed write, a typed configuration
accessor — the operation had to be a package function, whatever the design
would prefer:

```go
// The store was the receiver in everything except the syntax.
quote, err := pw.Memo(ctx, store, QuoteKey{Pair: pair}, fetchQuote)
```

Go 1.27 lets a method take a type parameter, and TinyGo 0.42 builds it, which
was the second half of the trigger: a conversion available only on host Go
would have split the build rather than tidied it. Popcorn Web requires both
since the module moved to `go 1.27.0`, and each of these operations is now the
method it was always describing.

**What moved is a call shape and nothing else.** Nothing stored, generated, or
on the wire changed. For the entries this framework owns, the old package
functions are gone: each was a one-line stand-in for its method, with the
handle already in place, so the edit at a call site is mechanical and is shown
below. For the entries that belong to tinybind, the old function stays as a
deprecated wrapper, and a project moves call site by call site or not at all.

## The data cache

[`pw.MemoStore`](/guides/backend/data-cache/) resolved a store to a handle
ahead of the language on purpose. With the store held in a value, the
operations moved onto it without touching the line that acquired it.

```go
// Before
store, err := pw.MemoStore(r, "rates")

quote, err := pw.Memo(ctx, store, QuoteKey{Pair: pair}, fetchQuote)
if pw.MemoHas(ctx, store, key) { /* … */ }
pw.MemoSet(ctx, store, key, quote)
pw.MemoInvalidate(ctx, store, key)
pw.MemoInvalidateScope(store, subject)
pw.MemoInvalidateTag(store, "user:u1")
```

```go
// Now — the first line is unchanged, which was the point
store, err := pw.MemoStore(r, "rates")

quote, err := store.Get(ctx, QuoteKey{Pair: pair}, fetchQuote)
if store.Has(ctx, key) { /* … */ }
store.Set(ctx, key, quote)
store.Invalidate(ctx, key)
store.InvalidateScope(subject)
store.InvalidateTag("user:u1")
```

The `pw.Memo` family no longer exists, and the same names are gone from
`pwfast`. `InvalidateScope` and `InvalidateTag` took no type parameter and
could have been methods all along; they moved with the rest so that the store
reads one way rather than two. Generation follows the method calls the way it
followed the functions, so a key type is still discovered from the argument
beside the result.

## A Firestore transaction

This is the one whose value is more than tidiness. Writes were already methods
on the transaction while typed reads were not, so a single transaction was
written two ways in adjacent lines:

```go
// Before
tx.Store(user)
user, err := firestorebind.LoadTx[User](ctx, tx, key)
```

```go
// Now
tx.Store(user)
user, err := tx.Load[User](ctx, key)
```

`Load`, `LoadAll` and `QueryPage` are the three typed reads, and `QueryKeysPage`
and `Count` moved with them. What changed is more than the spelling: the
transaction boundary stopped being an argument and became the receiver, so the
call states what it is inside. `LoadTx` and its siblings remain as deprecated
wrappers.

Reaching a transactional read through the transaction value rather than through
a context was a separate decision, and it survived the change untouched — a
context-carried handle would make one call site mean two different things
depending on which context reached it.

## The DynamoDB and Firestore handles

Popcorn Web wraps neither store, so the `On` entries were what an application
author literally wrote, and the handle was a concrete type waiting to be a
receiver.

```go
// Before
h, err := dynamo.Handle(ctx)
note, err := dynamobind.LoadOn[Note](ctx, h, "note", key)
err = dynamobind.StoreOn(ctx, h, "note", note)
```

```go
// Now
h, err := dynamo.Handle(ctx)
note, err := h.Load[Note](ctx, "note", key)
err = h.Store(ctx, "note", note)
```

Read the last line twice. An operation whose type is inferable from its argument
loses its type argument entirely, so storing, storing many, and removing are
plain calls.

Every `On` entry became a method of the same name without the suffix: `Load`,
`LoadAll`, `Store`, `StoreAll`, `StoreReturning`, `Remove`, `RemoveReturning`,
`Update`, `QueryPage`, `Query`, `ScanPage` and `Scan` on the
[DynamoDB](/guides/storage/dynamodb/) handle, and `Load`, `LoadAll`, `Store`,
`StoreAll`, `Insert`, `InsertAll`, `Update`, `Remove`, `RemoveAll`, `QueryPage`
and `Query` on the [Firestore](/guides/storage/firestore/) handle, along with
its keyless entries. The `On` functions remain, deprecated. The
context-resolving forms beside them — `dynamobind.Load[Note](ctx, …)` — are
exactly as they were, having no receiver by design, and generation reads all
three spellings.

## An isolated test configuration

```go
// Before
testutil.Update[pw.MiddlewareConfig](config, func(middleware *pw.MiddlewareConfig) {
	middleware.CSRF.Enabled = false
})
app := testutil.Get[AppConfig](config)
testutil.Set(config, app)
```

```go
// Now
config.Update(func(middleware *pw.MiddlewareConfig) {
	middleware.CSRF.Enabled = false
})
app := config.Get[AppConfig]()
config.Set(app)
```

Two of the three infer their type from an argument, and `Get` is the one that
still names it. All three of [`testutil`](/productivity/testing/)'s
configuration functions are gone, replaced by the methods.

## The session registry

`session.Register` took the registry as its first argument only because it
carried a typed codec. It is a method now, and the function is gone; a project
declaring its state through `pw.RegisterStore` never called it and has nothing
to change.

```go
// Before
err := session.Register[Cart](registry, "cart", session.Private, nil)
```

```go
// Now
err := registry.Register[Cart]("cart", session.Private, nil)
```

## What did not move

The context-resolving accessors stay functions permanently, because they have no
receiver by design — that is the half of each pair that reads a value out of a
`context.Context` rather than out of a handle. Constructors stay functions for
the same reason.

One entry is blocked by more than the language: `sqlbind.ScanRows` takes a row
cursor, which is an interface, and no package may give a method to a type it
does not define. It stays a function however Go changes, and that is worth
recording rather than rediscovering.

The generated layers' entries moved upstream too — the HTML builder's loop,
await, live and provider entries, the JSON parser's `ParseSlice` and `ParseMap`,
and `sqlbind.AppendValues` — each keeping its function as a deprecated wrapper.
Generated output still spells the function forms, so nothing an application
holds regenerated on their account.
