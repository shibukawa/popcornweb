# DynamoDB and Firestore stores

How to declare typed DynamoDB access (`dynamo`-tagged structs + `.pw.dynamo` files) and Firestore Datastore-mode access (`firestore`-tagged structs + `.pw.firestore` files) in a project created by `pw init`. Both are separate stores from the relational path — no connection groups, no `pw.Transaction`, no `.pw.sql` — and both compile to `_pw_gen.go` build outputs via `pw generate`, read only from directories listed under `[generate] dynamo = [...]` / `firestore = [...]` in `popcornweb.toml` (a stray query file outside those directories is reported by path). The client in each case is a process handle, not a request-context value.

## DynamoDB

### Enabling

`pw add dynamo` writes the config section, a starter record, the `generate.dynamo` entry, and the `dynamodb-local` package `pw dev` starts. The store is opt-in through a blank import:

```go
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

Those values are for dynamodb-local (any non-empty credential pair works); keep them in `config.dev.toml`. Other keys: `table_prefix`, `table_names`, `timeout` (`"10s"`), `max_idle_conns` (4), `verify_schema` (default `true` — reads every registered table at startup and refuses to serve on a mismatch), `auto_migrate` (default `false` — create missing tables at startup; development only, a configuration error elsewhere).

### The `dynamo` struct tag — the struct is the schema

There is no separate DDL. `pw generate` turns a tagged struct into an item codec, a key builder (`ItemKey()`), and a registered table definition. The declared table name is the snake_case of the type name.

```go
type Reading struct {
	Sensor  Sensor    `dynamo:"sensor,partitionkey"`
	At      int64     `dynamo:"at,sortkey"`
	Celsius float64   `dynamo:"celsius"`
	Flags   []string  `dynamo:"flags,stringset,omitempty"`
	Taken   time.Time `dynamo:"taken,unixtime"`
	Ignored string    `dynamo:"-"`
}
```

Tag form: `dynamo:"<attribute name>[,<option>...]"`. Empty name uses the Go field name; `dynamo:"-"` skips; unexported fields are always skipped. The tag is `dynamo`, not the AWS SDK's `dynamodbav` — a field with `dynamodbav` and no `dynamo` is a generation error.

| Option | Meaning |
| --- | --- |
| `partitionkey` | table partition key |
| `sortkey` | table sort key |
| `omitempty` | write no attribute when zero value |
| `stringset` / `numberset` / `binaryset` | store a slice as `SS`/`NS`/`BS` instead of `L` |
| `unixtime` | store `time.Time` as `N` epoch seconds |

Unknown options are generation errors. Attribute types: `string`→`S`, ints/uints/floats→`N` (via `strconv`, never through `float64` — DynamoDB numbers carry 38 digits), `bool`→`BOOL`, `[]byte`→`B`, `time.Time`→`S` RFC 3339 nano (or `N` with `unixtime`), `[]T`→`L` or a set, `map[string]T`→`M` (non-string key is an error), same-package nested struct→`M`, `*T`→pointee or `NULL`, `dynamodb.AttributeValue`→stored as-is (the escape hatch). Named types work wherever their underlying type does. Decoding leaves a field alone when the item lacks the attribute, so old items decode after a struct grows.

### `.pw.dynamo` query declarations

```
[export] statement <Name>(<param>: <GoType>, ...): dynamo.<shape><<ItemType>> {
  table <name>
  key <attribute> = {param} [and <attribute> <predicate>]
}
```

```
export statement ReadingsSince(sensor: Sensor, from: int64): dynamo.many<Reading> {
  table reading
  key sensor = {sensor} and at > {from}
}

export statement ReadingsBetween(sensor: Sensor, lo: int64, hi: int64): dynamo.page<Reading> {
  table reading
  key sensor = {sensor} and at between {lo} and {hi}
}

statement readingsForSensor(sensor: Sensor): dynamo.many<Reading> {
  table reading; key sensor = {sensor}
}
```

Parameter types are Go types as your package spells them. Both clauses are required, in either order; `;` separates clauses on one line; `//` comments to end of line; `export` must agree with the name's casing.

Result shapes pick the request shape (a Query always returns many rows):

| Shape | Generated return | Requests |
| --- | --- | --- |
| `dynamo.many<T>` | `iter.Seq2[T, error]` | one per page as the range advances |
| `dynamo.page<T>` | `(dynamobind.Page[T], error)` | exactly one; `Page[T]` carries `Count`, `ScannedCount`, `LastEvaluatedKey` |

Key conditions: the partition key predicate is mandatory, first, and always `=`. At most one sort-key predicate follows: `=`, `<`, `<=`, `>`, `>=`, `between {lo} and {hi}`, or `begins_with(attr, {p})` (string sort key only).

Generated signature — no table parameter, no client parameter:

```go
func ReadingsSince(ctx context.Context,
	sensor Sensor, from int64, opts ...dynamodb.QueryOption) iter.Seq2[Reading, error]
```

Variadic options reach the driver (`dynamodb.WithLimit`, `WithScanForward`, `WithConsistentRead`, `WithIndex`); a caller option cannot replace the declared condition. Every attribute is aliased unconditionally in generated expressions, so DynamoDB's 573 reserved words (`status`, `name`, `size`, …) never cause a `ValidationException`.

### Item operations (dynamobind)

Direct operations are methods on the handle and take a table name because they have no declaration to read one from:

```go
h, err := dynamo.Handle(ctx)

h.Load[T](ctx, table, key, opts...) (T, error)
h.Store(ctx, table, v, opts...) error
h.Remove(ctx, table, v, opts...) error
h.Update(ctx, table, v, expression, opts...) error

h.StoreReturning(ctx, table, v, opts...) (T, bool, error)
h.RemoveReturning(ctx, table, v, opts...) (T, bool, error)

h.QueryPage[T](ctx, table, keyCond, opts...) (Page[T], error)
h.ScanPage[T](ctx, table, opts...) (Page[T], error)
h.Query[T](ctx, table, keyCond, opts...) iter.Seq2[T, error]
h.Scan[T](ctx, table, opts...) iter.Seq2[T, error]

h.StoreAll(ctx, table, vs) (unprocessed []T, err error)
h.LoadAll[T](ctx, table, keys, opts...) (items []T, unprocessed []dynamodb.Key, err error)
```

The documented read pattern for one item is `Load(ctx, table, v.ItemKey())`. `Store` is `PutItem` (replaces the whole item); `Update` takes a DynamoDB update expression verbatim; `StoreReturning`/`RemoveReturning` ask for `ALL_OLD` and their bool is false when nothing was there (not an error). `StoreAll`/`LoadAll` chunk to the service limits (`MaxBatchWrite` 25, `MaxBatchGet` 100, both exported); the retry policy for `unprocessed` is yours. Running with the section disabled yields a named no-client error, not a panic. Miss detection: `errors.Is(err, dynamodb.ErrItemNotFound)`; structured driver errors via `errors.As` with `*dynamodb.Error`; decode failures via `dynamobind.AsError`.

### Declared vs deployed table names

Code declares logical names (`reading`, `note`). Deployment mapping is configuration, run by both the request path and the migrator:

```toml
[middleware.dynamo]
table_prefix = "myapp-"

[[middleware.dynamo.table_names]]
declared = "reading"
deployed = "readings-prod-8f21c"
```

Explicit entry wins; otherwise prefix; otherwise the declared name. `dynamo.WithTableResolver` replaces the composed function outright. A `table_names` entry naming a table no code declares is an error.

### Schema in dev: auto_migrate and verify_schema

No migration files, no version table. Desired state = registered table definitions; observed = `DescribeTable`; applying is the comparison:

```go
plan, err := dynamo.Plan(ctx)     // []TableChange
result, err := dynamo.Migrate(ctx)
```

Missing tables are created with the generated keys and polled until active. Key attribute names are compared positionally; a mismatch is an error naming both shapes. Key changes are reported, never performed; tables absent from source are never deleted; there is no `down`. `auto_migrate = true` applies the plan at startup — development/test only. In production, tables come from your provisioning tooling and `verify_schema` (default on) checks them at startup. TTL, retention, autoscaling, tags, and replication are outside the comparison — they belong to whoever owns the table.

### Not available (driver limits)

Filter/projection/condition/update expressions in declarations (pass them yourself via the unchecked string forms), secondary index tags (declared queries run against the table's own keys; `WithIndex` reaches the driver unchecked), single-table design (one struct owns one table), optimistic locking and a `ttl` tag (designed, not built — enable DynamoDB TTL outside the framework or not at all), transactions, PartiQL, Streams, DAX.

## Firestore (Datastore mode)

### Enabling

The database must be created in **Datastore mode**; Native mode is rejected at startup. `pw add firestore` creates an `entities/` package, a starter query, and the `generate.firestore` entry.

```go
import _ "github.com/shibukawa/popcornweb/database/firestore"
```

```toml
[middleware.firestore]
enabled = true
project_id = "demo-popcornweb"
endpoint = "127.0.0.1:8081"
```

For local development start the Datastore emulator yourself (`gcloud beta emulators datastore start --host-port=127.0.0.1:8081`) — `pw dev` does not start it. Production credentials: `credentials = "metadata"` on Cloud Run/GKE/GCE; `service_account` (default, reads `credentials_file` or `GOOGLE_APPLICATION_CREDENTIALS`); `oauth2`; `static`. `namespace` isolates every key the process touches.

### The `firestore` struct tag

Form: `firestore:"<property>[,<option>...]"`. Empty property uses the Go field name; `firestore:"-"` skips unless an identity option uses the field; unexported fields are always skipped.

```go
type Note struct {
	ID        string    `firestore:"-,name"`
	Author    string    `firestore:"author"`
	Body      string    `firestore:"body,noindex"`
	CreatedAt time.Time `firestore:"created_at"`
	ExpiresAt time.Time `firestore:"expires_at,ttl"`
}
```

| Option | Meaning |
| --- | --- |
| `name` | this string field is the key's name |
| `id` | this `int64` field is the key's numeric ID |
| `parent` | this `datastore.Key` supplies the ancestor path |
| `version` | this `int64` receives the entity version returned by a read |
| `ttl` | this stored `time.Time` names the property a TTL policy uses |
| `noindex` | store without indexing (cannot be filtered, ordered, selected, or `distinct`) |
| `omitempty` | omit when zero value |

At most one `name` or `id`, one `parent`, one `version`, one `ttl` per type. Identity fields are normally absent from the property map (the key is stored beside the properties). Property types: strings, `int`–`int64`, `uint8/16/32`, floats, `bool`, `[]byte`, `time.Time` (microsecond precision), `datastore.Key`, `datastore.LatLng`, `[]T`, same-package structs (embedded entity), `*T`, `datastore.Value`. `uint`, `uint64`, `uintptr`, maps, functions, channels are rejected — as is a field with a `datastore` tag but no `firestore` tag.

### `.pw.firestore` query grammar

There is no kind clause — the result type supplies the kind. Every clause is optional, in any order; `;` separates clauses on one line; `//` comments; `export` must agree with casing.

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

```text
export statement NotesByAuthor(author: string): firestore.many<Note> {
  where author == {author}
}
```

Conditions: `==`, `!=`, `<`, `<=`, `>`, `>=`, `in`, `not in`, combined with `and`, `or`, and parentheses (`and` binds tighter). `in`/`not in` need a slice parameter matching the property's element type. Every property name and parameter type is checked against the entity tags; key-only fields cannot appear in `where` — use `ancestor`.

| Shape | Generated return | Requests |
| --- | --- | --- |
| `firestore.batch<T>` | `(firestorebind.Page[T], error)` | exactly one |
| `firestore.many<T>` | `iter.Seq2[T, error]` | one per page as iteration advances |
| `firestore.count<T>` | `(int64, error)` | one aggregation query |
| `firestore.keys<T>` | `(firestorebind.KeyPage, error)` | one keys-only query |

Generated functions start with `context.Context`, then declared parameters, then optional `datastore.ReadOption` values. `batch`, `count`, and `keys` also generate a `NameTx` form taking `*firestorebind.Tx`; `many` does not.

Paging and projections: `start`/`end` take `datastore.Cursor` parameters — prefer a cursor to a large `offset` (skipped entities are still read and billed). `select` leaves unselected fields at zero values; never pass a projected entity to `Store`/`Update` (no partial update — omitted properties get replaced with zeros). `distinct` properties must lead the `order` clause. Single-property indexes are automatic; `index` publishes a composite index definition, but generation does not infer whether one is required — without a deployed index the service returns `FAILED_PRECONDITION` at runtime.

### Direct operations and transactions

```go
h, err := firestore.Handle(ctx)
key, err := h.Store(ctx, value)   // upsert
key, err = h.Insert(ctx, value)   // must be absent; returns allocated key
value, err = h.Load[Entity](ctx, key)
err = h.Update(ctx, value)        // must exist
err = h.Remove(ctx, value)
```

Transactions use the handle's `Run` and the operations on `*firestorebind.Tx`. Use `firestorebind.AsError` for structured Datastore errors and `errors.Is` for not-found and precondition errors. Declared queries resolve the process client themselves — call sites stay context-only.

### Schema and TTL

There is no migration or schema-application step: a kind appears on its first write. Composite indexes and TTL policies are deployment resources — the `ttl` tag only names the property; enable expiry with Google Cloud tooling:

```sh
gcloud firestore fields ttls update expires_at \
  --collection-group=Note --enable-ttl
```

Sessions and auth can use this store via `sessionstore/firestore`, `authstore/firestore`, `authstate/firestore` imports plus `backend = "firestore"` in `[session]`/`[auth]`; expired records are treated as invalid immediately but need a TTL policy to actually be removed.

## Differences from the SQL path

| SQL path | DynamoDB / Firestore |
| --- | --- |
| Schema from `migrations/` (goose, versioned, `down`) | Dynamo: comparison-based `Plan`/`Migrate`, no versions, no `down`; Firestore: kinds appear on first write |
| Connection groups, `pw.SelectDB`, `pw.Transaction` | Process handle (`dynamo.Handle` / `firestore.Handle`); no Dynamo transactions; Firestore uses the handle's `Run`/`Tx` |
| Result shape declared in `.pw.sql` `type` blocks | Result shape is the tagged Go struct itself |
| Table named inside the SQL text | Dynamo: `table` clause in the declaration; Firestore: the entity type supplies the kind |
| Seed data via `pw seed` | Not part of these stores |
| Query diagnostics under `[observability.query]` | SQL-only |
| Batching via `pgx.Batch` / `CopyFrom` (references/sql.md) | Dynamo: `StoreAll`/`LoadAll` chunked to service limits; Firestore: none |

Both stores are reachable from `pw.Memo` like any other upstream — see references/caching.md. `pw fmt --stdin=dynamo` formats a `.pw.dynamo` declaration read from standard input.

## Common mistakes

- Using `dynamodbav` or `datastore` tags — each store requires its own tag (`dynamo` / `firestore`); the SDK tag alone is a generation error.
- Editing `_pw_gen.go`, or placing `.pw.dynamo`/`.pw.firestore` files outside the directories listed under `generate.dynamo`/`generate.firestore`.
- Non-`=` predicate on a Dynamo partition key, more than one sort-key predicate, or a non-key attribute in the `key` clause — all generation errors.
- Omitting the `table` clause in a `.pw.dynamo` statement (both `table` and `key` are required).
- Expecting declared Dynamo queries to use a GSI — declarations run against the table's own keys; `WithIndex` is unchecked.
- Leaving `auto_migrate = true` outside development — it is a configuration error there; production tables come from provisioning, checked by `verify_schema`.
- Hard-coding deployed table names in code — declare logical names and map with `table_prefix`/`table_names`/`WithTableResolver`.
- Creating the Firestore database in Native mode — Datastore mode is required and Native mode is rejected at startup.
- Passing a `select`-projected Firestore entity to `Store`/`Update` — omitted properties are overwritten with zero values.
- Filtering on a `noindex` property, or shipping a composite-index query without deploying the index (`FAILED_PRECONDITION` at runtime).
- Assuming the `ttl` tag enables expiry — it only names the property; apply the policy with `gcloud`.
- Treating `dynamo.many<T>` like `page<T>` — the iterator reports no `Count`/`ScannedCount`/`LastEvaluatedKey` and cannot be resumed.
