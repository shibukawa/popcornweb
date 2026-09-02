---
title: Firestore Query Format
description: The complete firestore struct tag and .pw.firestore query surface generated into typed Go operations.
sidebar:
  order: 5
---

Firestore declarations have two sources. A Go struct with `firestore` tags
defines an entity, its key, and its kind; a `.pw.firestore` file defines named
queries over that entity. `pw generate` compiles both into `_pw_gen.go` build
output beside the sources.

The client uses the Firestore **Datastore mode** API. Configuration and
deployment choices are covered in [Firestore](/guides/storage/firestore/).

## Generation scope

Both sources must be in a directory listed by `generate.firestore`:

```toml
[generate]
firestore = ["entities"]
```

A `.pw.firestore` file outside those directories is reported as a stray source.
Each bound type generates its entity codec, key builder, kind, and policy
metadata. Each exported statement generates one exported Go function.

## The `firestore` tag

```text
firestore:"<property>[,<option>...]"
```

An empty property uses the Go field name. `firestore:"-"` skips the field unless
an identity option uses it. Unexported fields are always skipped.

| Option | Meaning |
| --- | --- |
| `name` | this string field is the key's name |
| `id` | this `int64` field is the key's numeric ID |
| `parent` | this `datastore.Key` supplies the ancestor path |
| `version` | this `int64` receives the entity version returned by a read |
| `ttl` | this stored `time.Time` names the property used by a TTL policy |
| `noindex` | store the property without placing it in an index |
| `omitempty` | omit the property when the field has its zero value |

At most one `name` or `id`, one `parent`, one `version`, and one `ttl` field may
appear on a type. A `noindex` property cannot be filtered, ordered, selected, or
used by `distinct`. The `ttl` option publishes the property name; it does not
apply the Google Cloud TTL policy.

`name`, `id`, and `parent` fields are normally absent from the property map.
Give an identity field a real property name only when the duplicate is
intentional:

```go
ID string `firestore:"external_id,name"`
```

## Property types

| Go type | Datastore value |
| --- | --- |
| `string` and named strings | string |
| `int` through `int64`, `uint8`, `uint16`, `uint32` | integer |
| `float32`, `float64` | double |
| `bool` | boolean |
| `[]byte` | blob |
| `time.Time` | timestamp, stored to microsecond precision |
| `datastore.Key` | key |
| `datastore.LatLng` | geographic point |
| `[]T` | array |
| a same-package struct | embedded entity |
| `*T` | the pointee, or null |
| `datastore.Value` | stored as supplied |

`uint`, `uint64`, `uintptr`, maps, functions, and channels are rejected. A field
with a `datastore` tag but no `firestore` tag is also rejected, because two
mappers silently assigning different property names would defeat generation's
type checks.

## Query grammar

```text
[export] statement <Name>(<param>: <GoType>, ...): firestore.<shape><<Entity>> {
  where <condition>
  ancestor {param}
  select <property>, ...
  distinct <property>, ...
  order <property> [asc|desc], ...
  start {param}
  end {param}
  limit <n>|{param}
  offset <n>|{param}
  index <property> [asc|desc], ...
}
```

Every clause is optional and may appear in any order. A semicolon separates
clauses on one line, and `//` starts a comment. `export` must agree with the
Go name's casing. There is no kind clause: the result type supplies the kind.

### Result shapes

| Shape | Generated return | Requests |
| --- | --- | --- |
| `firestore.batch<T>` | `(firestorebind.Page[T], error)` | exactly one |
| `firestore.many<T>` | `iter.Seq2[T, error]` | one per page as iteration advances |
| `firestore.count<T>` | `(int64, error)` | one aggregation query |
| `firestore.keys<T>` | `(firestorebind.KeyPage, error)` | one keys-only query |

Every generated function starts with `context.Context`, followed by the
declared parameters and optional `datastore.ReadOption` values. `batch`,
`count`, and `keys` also generate a `NameTx` form accepting
`*firestorebind.Tx`; `many` does not, because an iterator could hide an
unbounded number of transactional requests.

### Conditions

Conditions use `==`, `!=`, `<`, `<=`, `>`, `>=`, `in`, and `not in`. Combine
them with `and`, `or`, and parentheses. `and` binds more tightly than `or`.
`in` and `not in` require a slice parameter whose element type matches the
stored property type.

```text
where sensor == {sensor} and at >= {from}
where sensor in {sensors}
where (sensor == {sensor} or site == {site}) and at > {from}
```

Every property name and parameter type is checked against the entity tags.
Key-only fields cannot appear in `where`; use `ancestor` for an ancestor path.

### Paging, projections, and indexes

`start` and `end` take `datastore.Cursor` parameters. Prefer a cursor to a large
`offset`, because Datastore still reads and bills the skipped entities.

`select` leaves unselected fields at their zero values. Do not pass a projected
entity to `Store` or `Update`: Datastore has no partial update, so doing so
replaces the omitted properties with zero values. `distinct` properties must
lead the `order` clause.

Single-property indexes are automatic. Use `index` to publish a composite
index definition with the generated query. Generation does not infer whether
an index is required; without a deployed index, the service returns
`FAILED_PRECONDITION` at runtime.

## Direct operations

The generated entity satisfies the interfaces used by `firestorebind`:

```go
h, err := firestore.Handle(ctx)
key, err := h.Store(ctx, value)
key, err = h.Insert(ctx, value)
value, err = h.Load[Entity](ctx, key)
err = h.Update(ctx, value)
err = h.Remove(ctx, value)
```

`Store` is an upsert, `Insert` requires the entity to be absent, and `Update`
requires it to exist. An incomplete numeric key is allowed for `Insert`, which
returns the server-allocated key. `firestore.Handle` is
`database/firestore`'s process handle accessor. Transactions use
the handle's `Run` and the corresponding operations on `*firestorebind.Tx`.

Driver errors remain visible. Use `firestorebind.AsError` for structured
Datastore errors and `errors.Is` for the package's not-found and precondition
errors.
