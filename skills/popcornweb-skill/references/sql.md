# Typed SQL: .pw.sql templates, migrations, seed data

How to declare typed SQL statements in `.pw.sql` files, call the generated Go from handlers, and manage schema (migrations) and starting rows (seed data) in a project created by `pw init`. `.pw.sql` files compile to `_pw_gen.go` build outputs via `pw generate` — never hand-edit generated files. Queries are only read from directories listed under `[generate] queries = [...]` in `popcornweb.toml`; a `.pw.sql` outside every listed directory is reported as an error, and `generate.queries` must be empty in a component package.

## File layout

```sql
package queries

type User {
  id: int
  name: string
  active: bool
}

export statement GetUser(id: int): sql.one<User> {
SELECT id, name, active
FROM users
WHERE id = {id}
}
```

| Declaration | Introduces |
| --- | --- |
| `package name` | the Go package the generated file joins |
| `type Name { field: T … }` | a result shape; becomes a Go struct of the same name |
| `statement name(…): kind { … }` | a package-private statement |
| `export statement Name(…): kind { … }` | the same, published as Go API |

**A `type` is file-scoped but its name is directory-wide.** A statement can only name a `type` declared in its own `.pw.sql`; using one from another file fails with `unknown type Name`. Every file's struct still lands in one Go package, so declaring the same name in two files fails with `duplicate generated template declaration Name`. Keep statements that share a result type in one file; where they must stay apart, declare the shape per file under different names (`NewProjectID`, `NewTicketID`).

The SQL body stays SQL — nothing is translated between engines. The only dialect-dependent output is the placeholder token, decided by `project.database` in `popcornweb.toml`: `$1, $2, …` for `postgres`, `?` for `mysql` and `sqlite`. You always write `{name}`. Everything else (`||`, `ON CONFLICT`, `RETURNING`) reaches the SQL verbatim; write for the engine you selected.

## Parameter and field types

| Template type | Go type |
| --- | --- |
| `string`, `decimal` | `string` |
| `bool` | `bool` |
| `int` | `int` |
| `float` | `float64` |
| `bytes` | `[]byte` |
| `datetime`, `date`, `time` | `time.Time` |
| `url` | `url.URL` |
| `T[]` | `[]T` |
| `T?` | `*T` |

Use an optional type (`T?`) wherever NULL is possible — a required `string` reading NULL is an error, not an empty string. `datetime` needs the driver to return `time.Time` (MySQL: `parseTime=true` in the DSN; the framework adds it unless the DSN sets it).

## Statement kinds

| Kind | Contract | Generated result |
| --- | --- | --- |
| `sql.exec` | no row result | `sql.Result` |
| `sql.one<T>` | exactly one row | `T`; zero rows → `sql.ErrNoRows`, several → error |
| `sql.optional<T>` | zero or one row | `*T`; zero rows → `nil, nil`, several → error |
| `sql.many<T>` | zero or more rows | `iter.Seq2[T, error]`, streamed |
| `sql.predicate` | reusable condition | none — usable only from another statement |
| `sql.relation<T>` | typed subquery | none — usable only from another statement |

`sql.many` yields one row at a time; breaking out of the range closes the underlying `sql.Rows`. Query, scan, and iteration errors all arrive through the error value:

```go
for user, err := range queries.ListActiveUsers(ctx, true) {
	if err != nil {
		return err
	}
	consume(user)
}
```

## Parameters bind values, never structure

Every `{name}` becomes a prepared-statement placeholder; template expressions are never concatenated into SQL text, so binding cannot create an injection. A handwritten `$1` or `?` is a generation error. A parameter can never stand in for a table name, column name, operator, or sort direction.

```sql
export statement RenameUser(id: int, name: string): sql.exec {
UPDATE users
SET name = {name}
WHERE id = {id}
}
```

A slice parameter expands into a value list:

```sql
export statement FindUsers(ids: int[]): sql.many<User> {
SELECT id, name, active
FROM users
WHERE id IN ({ids})
ORDER BY id
}
```

An empty slice is a builder error (there is no valid `IN ()`); handle the empty case in the caller or restructure with a condition.

## Result types must match SELECT columns

Field order must match SELECT/RETURNING column order, and each column name or alias must correspond to a field name. Checked at generation:

```sql
type UserSummary {
  id: int
  displayName: string
}

export statement ListUsers(): sql.many<UserSummary> {
SELECT id, display_name AS displayName
FROM users
ORDER BY id
}
```

A runtime condition may not add or remove a SELECT or RETURNING column — the result shape must be identical across every branch.

## Conditional SQL

```sql
export statement SearchUsers(name: string, activeOnly: bool): sql.many<User> {
SELECT id, name, active
FROM users
WHERE name = {name}
{if activeOnly}
  AND active = TRUE
{/if}
ORDER BY id
}
```

`{else}` is available; the condition must be `bool`. Only surviving branches consume placeholders, so numbering and `Args` stay aligned.

`{val name = expr}` and `{check Call(…)}` work here as they do in `.pw.html` (see references/templates.md). In a query a binding normalises a value once for several parameter positions and contributes no bytes to the statement itself.

## Predicates and relations

A private `sql.predicate` is a reusable condition; a private `sql.relation<T>` is a typed subquery used via `FROM subquery` / `JOIN subquery`. Neither can be exported and neither generates a function.

```sql
statement minimumID(id: int): sql.predicate {
id >= {id}
}

export statement FindRecentUsers(minimum: int): sql.many<User> {
SELECT id, name, active
FROM users
WHERE {minimumID(minimum)}
ORDER BY id
}
```

```sql
statement ActiveUsers(minimumID: int): sql.relation<ActiveUser> {
SELECT id, name
FROM users
WHERE id >= {minimumID} AND active = TRUE
}

export statement ListActiveUsers(minimumID: int, name: string): sql.many<ActiveUser> {
SELECT active_users.id, active_users.name
FROM subquery ActiveUsers(minimumID) AS active_users
WHERE active_users.name = {name}
ORDER BY active_users.id
}
```

Subquery and outer arguments share one placeholder sequence in final SQL order. The alias is explicit and lower snake_case. Recursive relations are rejected.

**A relation's name must be PascalCase** even though it is not exported: `FROM subquery activeUsers(…)` fails with `subquery relation name must be PascalCase`. A predicate's name takes either case.

## Safety rules

- **UPDATE and DELETE require a WHERE clause**, proven at generation time across every conditional path. A `WHERE` inside a subquery, CTE, string literal, or comment does not count. The same proof covers a dynamic `SET` list (an UPDATE whose assignments are all conditional is an error) and applies to every cardinality. There is no opt-in for a full-table UPDATE/DELETE — write that as a migration.
- **`export` must agree with name casing**: `export statement FindUser` → public `func FindUser`; `statement findUser` → package-private `func findUser`. `export statement findUser` and `statement FindUser` are both errors. `sql.predicate`/`sql.relation` generate no function, so this rule does not apply to them: a predicate takes either case, and a relation is PascalCase **without** `export` (see above).

## Generated signatures and calling from handlers

Generated functions are context-resolved — no exported function takes a `*sql.DB` or `*sql.Tx`. The executor (pool or active transaction) comes from the context:

```go
func Name(ctx context.Context, p ...P) (sql.Result, error)   // sql.exec
func Name(ctx context.Context, p ...P) (T, error)            // sql.one<T>
func Name(ctx context.Context, p ...P) (*T, error)           // sql.optional<T>
func Name(ctx context.Context, p ...P) iter.Seq2[T, error]   // sql.many<T>

func BuildName(p ...P) (sqlbind.Statement, error)            // every exported statement
```

`sqlbind.Statement` (from `github.com/shibukawa/tinybind-go/sqlbind`) is `struct { SQL string; Args []any }` — use `BuildName` for tests, log lines, or custom database layers. In a handler:

```go
counter, err := queries.IncrementAccess(r.Context())
```

## Transactions and connection groups

The transaction boundary is explicit — the framework never wraps a request in one automatically:

```go
err := pw.Transaction(r, func(ctx context.Context) error {
	if _, err := queries.InsertUser(ctx, name); err != nil {
		return err
	}
	return queries.RecordAudit(ctx, "user.created")
})
```

A nested `pw.Transaction` opens a savepoint: its failure rolls back only the inner work. Drivers without savepoint support return `ErrSavepointUnsupported`.

Nothing in `.pw.sql` names a database. Unpinned statements go to the default connection group; `pw.SelectDB(r, "writer")` pins one, for a single statement or a whole transaction:

```go
user, err := queries.CreateUser(pw.SelectDB(r, "writer"), name)

err := pw.TransactionContext(pw.SelectDB(r, "writer"), func(ctx context.Context) error {
	return queries.RecordAudit(ctx, "user.created")
})
```

One transaction never spans two groups (`ErrCrossGroupTransaction`). A single-connection configuration (including every `testutil` run) answers every group name with that one database.

## Escape hatches when a query does not fit

`db, ok := pw.DB(r)` returns the pool — but on **PostgreSQL `ok` is `false`**: requests run on a native pgx pool with no `*sql.DB` behind them. The way to that connection is `postgres.WithConn`:

```go
err := postgres.WithConn(ctx, func(conn *pgx.Conn) error {
	batch := &pgx.Batch{}
	for _, name := range names {
		batch.Queue("INSERT INTO items (name) VALUES ($1)", name)
	}
	results := conn.SendBatch(ctx, batch)
	for range names {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return err
		}
	}
	return results.Close()
})
```

Called inside `pw.Transaction`, the callback receives the connection that transaction is already executing on, so the work joins it and rolls back with it; called outside one, a pooled connection is leased for the call. **Nothing derived from the connection may outlive the callback.** A group that is not PostgreSQL returns an error naming the engine it found. `WithConn` is also how `LISTEN`/`NOTIFY` and `errors.As` against `*pgx.PgError` are reached.

Work done through `WithConn` **does not reach the query log** — it bypasses the executor those diagnostics attach to. Open a span (`pw.StartSpanKind(ctx, "import-items", pw.SpanKindClient)`) and log the shape yourself: how many statements went, and how long the exchange took.

### Batching

Where the cost is round trips rather than query time, the fix depends on the engine.

- **Start with a transaction.** Every statement outside one is its own transaction, which SQLite pays for with an fsync each time — two hundred inserts go from ~50 ms to ~1 ms. **On SQLite that is the whole answer**; there is no network to amortise.
- **PostgreSQL: `pgx.Batch`** through `WithConn`, as above — one round trip, run as one implicit transaction. Keep DDL out of a batch (the server may parse every queued statement before running any), and `VACUUM`, `CREATE DATABASE`, and `CREATE INDEX CONCURRENTLY` are rejected in a batch of more than one statement. Reads inside a batch do not necessarily share a snapshot; queue it inside `pw.Transaction` when you need a stronger isolation level.
- **PostgreSQL: `conn.CopyFrom`** when the rows share one table and column shape — the bulk-ingest path, with no per-row `RETURNING` and no `ON CONFLICT`. For upserts, COPY into a staging table and follow with `INSERT … ON CONFLICT` in the same transaction. Never point `COPY FROM` at a file path: that reads the *database server's* filesystem.
- **MySQL** has no pipelining, so its batch package joins statements into one multi-statement command, which needs `multiStatements=true` and `interpolateParams=true` in the DSN — the first widens what an injection can do on every connection, and the second renders arguments into the SQL instead of binding them. Only writes, one error for the whole command, and no joining an open transaction. Take it only for a write-heavy import whose operator agreed to both settings.

**When not to batch:** inserts into one table are a multi-row `INSERT`, which parses once, works on every engine, and is what `.pw.sql` slice expansion already writes. Reach for a batch when the statements genuinely differ.

For JOINs that repeat the parent row per child, `sqlbind.ScanRows[T]` regroups rows into nested structs using `groupkey:""` and `db:"alias"` tags on plain Go structs (host Go only — excluded from TinyGo builds; holds the whole result in memory). Prefer `sql.one`/`sql.optional`/`sql.many` for ordinary queries.

## Migrations

Migrations live in `migrations/`, in goose format — one `.sql` file with two annotated sections, numbered in application order:

```sql
-- +goose Up
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL
);

-- +goose Down
DROP TABLE users;
```

```sh
pw migrate create add_email   # create a new numbered file
pw migrate status             # what is applied / pending
pw migrate up                 # apply pending
pw migrate down               # roll back
```

Workflow rules:

- `pw dev` applies pending migrations at startup and when a migration file changes — in development you rarely type migrate commands.
- Always write the `Down` section, even if you expect never to run it.
- Applied versions are recorded in the database by number, so `pw migrate up` is safe to run twice. Never renumber or edit an applied migration — correct a mistake with a later version.
- Migrations go to `middleware.rdb.write_group`, or `middleware.rdb.migration_group` when set; a `readonly` connection is never chosen (configuring one there fails at startup). The CLI resolves the DSN from the application's own configuration, so it obeys `APP_ENV` — confirm the environment first.
- In tests: `testutil.WithMigrations("../migrations")` applies the set before the test server starts. SQLite replays a cached snapshot (this is what makes `sqlite://:memory:` work); PostgreSQL/MySQL apply directly and a second `TestRun` against the same database reuses the schema.
- Migrations are for **structure** and run in every environment including production. Development rows belong to seed data, not migrations.

## Seed data

A dataset is one YAML file in `testdata/seed/`: table names mapped to row lists, inserted in written order (order matters for foreign-key references, within and across files):

```yaml
member:
- { id: 1, name: Frank }
- { id: 2, name: Grace }
- { id: 3, name: Heidi }
```

```sh
pw seed                # every dataset in the directory
pw seed users orders   # only these, in this order (.yaml may be omitted)
```

Seeds carry no version and are recorded nowhere — running one twice inserts again. They follow the same routing as migrations (`write_group` / `migration_group`, never `readonly`) and obey `APP_ENV`.

The same files serve tests as fixtures:

```go
server := testutil.TestRun(t, Handlers(), nil,
	testutil.WithMigrations("../migrations"),
	testutil.WithSeed("initial"),
)
```

`WithSeedDir` moves the directory. `server.AssertDB(t, "after_archive")` compares the database against a dataset with a per-table diff. DBUnit-style keys are supported (`_operation` per table, `_match`, matcher values like `[notnull]`), but the format is YAML only — DBUnit XML/CSV/Excel is never parsed.

## Query diagnostics

In `dev`, every generated statement is logged with its SQL, args, and duration (`msg="sql executed"`). Past `slow_threshold` the record moves to `warn` and adds `explain` (the plan-only query plan, captured on the same connection and transaction) and `reproduction` (a paste-able shell snippet that binds arguments rather than inlining them). Configured under `[observability.query]`; `enabled = "auto"` is on only when `APP_ENV` is `dev`, and outside dev both `enabled` and `bind_values` need an explicit `"on"`. Only generated `.pw.sql` calls are instrumented — session, auth, and migration statements produce no records.

## Common mistakes

- Hand-writing `$1` or `?` in a `.pw.sql` body — generation error; always use `{name}`.
- Editing `_pw_gen.go` — it is build output; change the `.pw.sql` and rerun `pw generate` (or let `pw dev` do it).
- Putting a `.pw.sql` in a directory not listed under `generate.queries` in `popcornweb.toml` — reported, not compiled.
- SELECT columns drifting from the declared result type (count, order, or names), or a condition adding/removing a column.
- UPDATE/DELETE whose WHERE can vanish on some conditional path — rejected; a deliberate full-table change belongs in a migration.
- Passing an empty slice to an `IN ({ids})` expansion — runtime builder error.
- Ignoring the per-row error while ranging over `sql.many`.
- `export` and name casing disagreeing (`export statement findUser`, `statement FindUser`).
- A lowercase `sql.relation` name (`statement activeUsers(…): sql.relation<T>`) — `FROM subquery` only accepts PascalCase.
- Naming a `type` declared in another `.pw.sql` (`unknown type`), or declaring one name in two files of a directory (`duplicate generated template declaration`).
- Expecting dialect translation — SQL text is emitted verbatim; a package generated for SQLite is not the one you ship on PostgreSQL.
- Renumbering or editing an applied migration, or seeding/migrating with the wrong `APP_ENV` selected.
- Using a migration for test rows (runs in production) or a seed for schema (never versioned, inserts again on rerun).
- Reaching for `pw.DB` on PostgreSQL and treating `ok == false` as a misconfiguration — it is the native pgx pool; use `postgres.WithConn`.
- Letting rows, a `pgx.Rows`, or a batch result escape a `WithConn` callback — the connection returns to the pool the moment it returns.
- Caching a read taken inside a transaction (see references/caching.md).
