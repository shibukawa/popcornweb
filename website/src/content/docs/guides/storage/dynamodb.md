---
title: DynamoDB
description: A typed DynamoDB store that builds under TinyGo — tagged records, declared queries, and a schema applied by comparison instead of migration files.
sidebar:
  order: 2
---

DynamoDB is not another relational database engine here. It is a separate store,
added beside a relational one or instead of it, and the two share nothing:
`middleware.dynamo` has its own section, its own client, and no connection
group, no transaction runner, and no `.pw.sql`. A project keeping sessions in
SQLite and events in DynamoDB is the expected shape rather than an exception,
and so is a project with no relational database at all.

That it exists at all is the same story as
[object storage](/guides/storage/object-storage/): `aws-sdk-go-v2` does not
build under TinyGo. So the client here is
[`tinygodriver`](https://github.com/shibukawa/tinygodriver)'s, the typed layer
over it is `tinybind`'s `dynamobind`, and both compile on both targets. The
client travels as a process handle rather than a context value — the context
path costs about 37 KB on a `wasip1` build, and that is the cost the handle
avoids.

## Turning it on

Importing the package is what creates the configuration section — a project
that does not use DynamoDB gains no key and links no driver:

```go
// cmd/myapp/main.go
import _ "github.com/shibukawa/popcornweb/database/dynamo"
```

```toml
[middleware.dynamo]
enabled = true
region = "us-east-1"
endpoint = "http://127.0.0.1:8000"
access_key_id = "local"
secret_access_key = "local"
auto_migrate = true
```

`pw add dynamo` writes that section, the starter record, the `generate.dynamo`
entry that generation reads, and the `dynamodb-local` package `pw dev` starts.
The values above are the local server's: it verifies no signature, so any
non-empty credential pair works, and the region is a placeholder it accepts.
Keep them in `config.dev.toml`, never as a deployment default.

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | open the client at startup and hold it as the process handle |
| `region` | *(empty)* | falls back to the environment; resolvable from neither is a startup error |
| `endpoint` | *(empty)* | empty selects the regional host; a value selects a local or compatible server |
| `access_key_id` | *(empty)* | empty selects the driver's environment credentials |
| `secret_access_key` | *(empty)* | redacted everywhere |
| `session_token` | *(empty)* | redacted everywhere |
| `table_prefix` | *(empty)* | prepended to a declared table name |
| `table_names` | `[]` | explicit declared-to-deployed mappings |
| `timeout` | `"10s"` | bounds one request |
| `max_idle_conns` | `4` | size it to the expected concurrency |
| `verify_schema` | `true` | read every registered table once at startup and refuse to serve on a mismatch |
| `auto_migrate` | `false` | create missing tables during startup; development only |

One static credential without the other is rejected, because a half-configured
pair would otherwise fall back to the environment and fail somewhere less
obvious. All three credential keys expand `${NAME}` and are redacted after
expansion, so an expanded secret reaches neither the startup summary nor an
error.

Startup constructs the client and fails before serving when the region or the
credentials are missing; shutdown closes it and releases the pooled
connections.

## Declaring a record

A struct with `dynamo` tags is a table:

```go
package records

type Note struct {
	ID        string    `dynamo:"id,partitionkey"`
	CreatedAt time.Time `dynamo:"created_at,sortkey"`
	Body      string    `dynamo:"body"`
}
```

`pw generate` reads it and emits the item codec, the key builder, and the table
definition, plus the `init` that registers that definition as part of the
desired schema. The declared table name is the snake_case of the type name —
`note` here — and nothing about the deployment appears in the source.

Generation is directed by use: the codec appears for the directions something
actually calls, so deleting a read shrinks the generated code to match. The
directory has to be listed under `generate.dynamo` in `popcornweb.toml`, which
`pw add dynamo` does; a `.pw.dynamo` file outside every listed directory is
reported once, by path, rather than silently skipped.

## Reading and writing one item

The client and the configured table naming travel together as one handle,
held by this package as process state. Nothing is stored in the request
context, so a call site pays no context lookup:

```go
func store(ctx context.Context, note Note) error {
	h, err := dynamo.Handle(ctx)
	if err != nil {
		return err
	}
	return h.Store(ctx, "note", note)
}

func load(ctx context.Context, id string, createdAt time.Time) (Note, error) {
	h, err := dynamo.Handle(ctx)
	if err != nil {
		return Note{}, err
	}
	return h.Load[Note](ctx, "note", Note{ID: id, CreatedAt: createdAt}.ItemKey())
}
```

An item operation names its table because it has no declaration to read one
from. A call that runs with the section disabled gets a named no-client error
rather than a panic. A declared query never takes the handle — its generated
body resolves the same one itself, which is why its call sites stay
context-only.

There are no wrappers around these calls. `database/dynamo` exports the
configuration, the table registry, and the migrator, and nothing else. The
relational side has wrappers because three engines have to look like one; here
there is one client, so a wrapper would be a second name for a thing that
already has one.

## Declared queries

An access pattern goes in a `.pw.dynamo` file beside the package, and generates
one named function:

```
export statement ReadingsSince(sensor: Sensor, from: int64): dynamo.many<Reading> {
  table reading
  key sensor = {sensor} and at > {from}
}
```

```go
for reading, err := range records.ReadingsSince(ctx, sensor, from) {
	// ...
}
```

The call names no attribute, no expression, no table, and no client. A `table`
clause is required in every statement, which is what removes the table
parameter from the signature; the key clause takes the partition key by
equality plus at most one sort-key predicate from `=`, `<`, `<=`, `>`, `>=`,
`between`, and `begins_with`.

| Result type | Returns |
| --- | --- |
| `dynamo.page<T>` | one request — `Page[T]` with `Count`, `ScannedCount`, and `LastEvaluatedKey` |
| `dynamo.many<T>` | an iterator over every page |

How many requests to make stays the author's decision, which is why both exist.

Generation can check things a SQL generator cannot, because here the tags *are*
the schema — there is no separate DDL for the source to disagree with. Every
attribute named must exist on the bound type, so renaming a tag without editing
the declaration fails `pw generate` rather than the first request. A non-key
attribute in a key clause is an error naming the clause it belongs in.
Parameter types are checked against how the tag stores the attribute. And every
attribute is aliased unconditionally, so none of DynamoDB's 573 reserved words
can reach an expression literally.

Filter, projection, condition, and update expressions are not declarable.
Neither are secondary indexes, so a declared query runs against the table's own
keys. The unchecked string key-condition form stays available for what the
declaration cannot express.

Every tag option, attribute type, key predicate, and generation check is in
[DynamoDB Query Format](/reference/dynamo-templates/).

## Declared names and deployed names

Source declares `note`. A deployment may have called it `myapp-note-prod`, and
the gap between the two is one function, installed once, that every runtime
entry runs:

```toml
[middleware.dynamo]
table_prefix = "myapp-"

[[middleware.dynamo.table_names]]
declared = "note"
deployed = "notes-prod-8f21c"
```

An explicit entry wins; otherwise the prefix applies; otherwise the declared
name stands. A prefix alone would not have been enough. Unlike an S3 key
prefix, which listing, IAM, and lifecycle rules all read, a DynamoDB table name
has no structure the service looks at — so a CDK-generated physical name, or an
`orders-prod` that puts the environment last, cannot be produced by prepending
anything. For a deployment neither key expresses,
`dynamo.WithTableResolver` replaces the composed function outright.

One function serves both the request path and the migrator, and that is the
whole reason it is a function. A migration that creates a table the handlers
cannot find would fail nowhere visible. Configured names and resolved names are
both validated at startup against DynamoDB's own rule, and a `table_names`
entry naming a table no code declares is an error rather than a line that
silently does nothing.

## Schema

There are no migration files for this store and no version table. The desired
state is the set of registered table definitions; the observed state is
`DescribeTable`; and applying is the comparison between them:

```go
plan, err := dynamo.Plan(ctx)     // []TableChange
result, err := dynamo.Migrate(ctx)
```

A missing table is created with the generated keys and polled until active. An
existing table has its key attribute names compared positionally, and nothing
else — `DescribeTable` reports no attribute type, so a retyped key reads as
matching while a renamed, missing, or extra key is caught. A mismatch is an
error naming the table, the desired shape, and the observed one, because
knowing only that they differ does not say which one is the surprise.

Two things are never done. A key change is **reported, not performed**: the
driver has no `UpdateTable`, and a partition key is immutable anyway. And a
table present in the account but absent from the source is reported and
**never deleted**.

Nothing is versioned, because nothing needs to be: `DescribeTable` reports the
live shape, so re-applying is a no-op by construction rather than by
bookkeeping, and a hand-made change is visible instead of hidden. There is also
no `down` — reversing a change here would be `DeleteTable`, which destroys data
no SQL rollback destroys.

Creation, though, is a development and test step. `auto_migrate = true` applies
the plan at startup and is a configuration error anywhere else, because a
deployed table comes from whatever provisions the queue and the bucket. What
the framework contributes in production is the other half: `verify_schema`, on
by default. Deployment tooling knows what it created and not what the
application assumes, so the comparison is one only the application can make.
Turning it off is accepted, and warned about.

The same line puts TTL, retention, autoscaling, tags, and replication outside
the comparison entirely. They belong to whoever owns the table, and a mechanism
that reported the difference would fire on every correct deployment — which
trains a reader to stop looking at it.

## What the stack does not have

| Absent | Consequence |
| --- | --- |
| Transactions, PartiQL, Streams, DAX | the driver does not implement them |
| `UpdateTable` | no table is altered in place |
| Secondary index tags | a declared query uses the table's own keys |
| Filter and update expressions | declarations cover key conditions, limit, direction, and consistency |
| Single-table design | one struct owns one table, which `tinybind` declines to work around |

None of these is a Popcorn Web choice. They are the edge of what the layer
below currently does, and they move when it does.

## Sessions on it

`sessionstore/dynamo` puts login sessions in this store, for a deployment with
no relational database at all. It borrows the client this package opens and
registers its own table, so the session table is created and verified with
every other one — and its expired records are removed by TTL you enable, or
not at all. See [Session storage](/guides/storage/session-storage/).
