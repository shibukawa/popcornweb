---
title: Testing
description: Run a real application from an isolated configuration copy with testutil.
sidebar:
  order: 1
---

A fast test is useful only if it still exercises the application you deploy.
`github.com/shibukawa/popcornweb/testutil` starts the actual routes,
middleware stack, and configuration binding against an isolated copy of every
registered setting. Tests reach that application over HTTP instead of calling a
hand-assembled approximation.

## A first test

```go
func TestHome(t *testing.T) {
	server := testutil.TestRun(t, Handlers(), func(config *testutil.Config) {
		config.Update(func(middleware *pw.MiddlewareConfig) {
			middleware.RDB = pw.RDBConfig{
				Enabled: true,
				Connections: []pw.RDBConnectionConfig{{
					DSN:            "sqlite://:memory:",
					ConnectTimeout: time.Second,
					MaxOpenConns:   1,
					MaxIdleConns:   1,
				}},
			}
		})
	}, testutil.WithMigrations("../migrations"))

	response, err := server.Client().Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}
```

`TestRun` first copies every registered configuration and sets the copied port
to `-1`. It then applies your customiser, reserves an available loopback port,
initialises the copied runtime resources, and starts the server. Cleanup is
registered with `t`, leaving nothing for the test to defer.

That order creates one important constraint: a customiser that reads the port
sees `-1`, because the real port is chosen later. Use `server.URL` or
`server.Port` after startup instead.

## Registering generated packages

The server is real, which means its generated registrations must be real too.
Document shells and configuration definitions register during package `init`,
so the test binary has to link their packages:

```go
import (
	_ "myapp"            // public.go
	_ "myapp/templates"  // the document shell
)
```

Without these the server starts without a registered document and HTML
rendering fails at startup.

## Customising configuration

| Call | Purpose |
| --- | --- |
| `config.Get[T]()` | read a copied configuration struct |
| `config.Set(value)` | replace one wholesale |
| `config.Update(fn)` | edit one in place |

Each is a method generic over the configuration type, so framework and
application settings are reached the same typed way. `Set` and `Update` infer
the type from their argument; `Get` has nothing to infer it from and is written
with it:

```go
config.Update(func(app *AppConfig) {
	app.EnvLabel = "test"
})
```

## Options

### `WithMigrations` / `WithMigrationsFS`

```go
testutil.WithMigrations("../migrations")
```

Applies the migration set before the server starts. `WithMigrationsFS` takes an
`fs.FS` instead, for embedded migrations.

How the schema arrives depends on the engine in the DSN:

- **SQLite** replays a cached snapshot into the copied database. That is what
  makes `sqlite://:memory:` work — an in-process database is unreachable by
  DSN, so SQL is transferred rather than a connection string.
- **PostgreSQL and MySQL** apply the migrations to the configured database
  directly. A second `TestRun` against the same database applies nothing and
  reuses the schema, which is how a package of tests shares one prepared
  server.

Point a server DSN at a database dedicated to the test suite. Applied versions
are recorded by number, so a database already carrying another project's
version 1 makes your first migration look applied and the schema never arrives.

### `WithSeed` / `WithSeedDir`

```go
testutil.WithSeed("initial")
```

Loads datasets after the schema is installed and before the server starts. Names
are relative to the seed directory — `testdata/seed` by default, overridable
with `WithSeedDir` — and the `.yaml` extension may be omitted. Datasets are
applied in the order given.

A dataset file is a table name mapped to rows:

```yaml
member:
- { id: 1, name: Frank }
- { id: 2, name: Grace }
```

The same files also drive `pw seed`. One fixture format therefore serves both
the CLI and the test suite instead of drifting into two versions. See
[Fixtures](#fixtures) for using that file as the expected state as well, and
[Seed Data](/productivity/seed-data/) for the format itself.

### `WithTransaction`

```go
testutil.WithTransaction(true)
```

Runs every request of the test server inside one transaction that is rolled back
when the test finishes. Tests sharing one database stay independent and can run
in parallel. Transactions the application itself starts nest into it as
savepoints, which requires a driver with savepoint support.

This works on every engine, including PostgreSQL's native pgx path: the test
transaction is opened on whichever kind of pool the connection holds, and
seeding and assertion run inside it either way.

### `WithIdentityProvider`

```go
server := testutil.TestRun(t, handlers.Handlers(), nil, testutil.WithIdentityProvider(
	testutil.WithIdPConfig("../devidp.toml"),
	testutil.WithLoginUser("admin"),
	testutil.WithIdPBinding(func(config *testutil.Config, idp testutil.IdPInfo) {
		config.Update(func(auth *handlers.AuthConfig) {
			auth.Issuer, auth.ClientID, auth.ClientSecret = idp.Issuer, idp.ClientID, idp.ClientSecret
		})
	}),
))
```

Starts the same development identity provider [`pw dev`](/pw/project/dev/) uses,
on its own loopback port, before the application server. `WithLoginUser`
pre-selects the subject, so the authorization endpoint redirects straight back
with a code and the whole login completes without driving a browser:

```go
response, err := server.Client().Get(server.URL + "/login")
```

The roster comes from exactly one of `WithIdPConfig` (a `devidp.toml` file),
`WithIdPRoster` (the same TOML held in the test), or `WithIdPUsers` (Go values).
`WithIdPBinding` writes the issuer and the generated client credentials into the
copied configuration; it runs after `customize`, so it wins over a placeholder.
`server.LoginAs(t, "guest")` switches user mid-test, and `server.IdPInfo()`
returns the same values for a test that wires its client by hand.

## Fixtures

A dataset stops being seed data and becomes a **fixture** the moment a test
treats it as a known state. The same file then works from both ends: loaded
before the request as the starting state, and compared against the database
afterwards as the expected one. `server.AssertDB` is that second end.

```go
func TestArchiveRemovesTheMember(t *testing.T) {
	server := testutil.TestRun(t, Handlers(), nil,
		testutil.WithMigrations("../migrations"),
		testutil.WithSeed("initial"),
	)

	response, err := server.Client().Post(server.URL+"/members/2/archive", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	server.AssertDB(t, "after_archive")
}
```

Writing the expectation as a file rather than as a list of `SELECT` assertions
is what makes the whole table part of the test. A handler that archives the
right member and also clears somebody else's row fails here, because nothing in
`after_archive.yaml` said that row could change — and that is precisely the bug
a hand-written `SELECT` on member 2 would pass straight over.

A mismatch goes through `Errorf` rather than `Fatalf`, so the test continues and
one run reports every table that drifted instead of only the first:

```
Popcorn Web database does not match:
after_archive.yaml:
Table: member
- Expected
+ Actual
id: 1
  name: Frank
id: 2
- name: Grace
```

### Comparing part of a table

By default a table must match the dataset exactly: a row in the database that
the file does not mention is a failure. Columns are not held to that standard —
one the file omits is ignored, which is how a serial `id` or a `created_at`
stays out of the expectation.

When extra *rows* are legitimate, name the strategy per table:

```yaml
_match:
  member: exact          # the default
  access_log_2026_*: sub # extra rows are fine, the listed ones must exist

member:
- { id: 1, name: Frank }
```

Prefer `exact` and reach for `sub` only where rows arrive from something the
test does not control — an audit trail, an append-only log, a table another test
in the same package also writes. `sub` on an ordinary table quietly stops
catching the row you did not expect, which was the reason to compare tables in
the first place.

A column whose value cannot be written down in advance — a generated timestamp,
a message with an identifier embedded in it — is written as a matcher instead:
`[notnull]`, `[currentdate, 2m]`, `[regexp, …]`. The full set is in
[the dataset format](/productivity/seed-data/#values-that-only-make-sense-as-an-expectation).
Without them the assertion for those columns would have to leave the file and
move into Go.

### Reseeding mid-test

`server.Seed` applies a dataset to the running server, which resets state
between the phases of one test:

```go
server.Seed(t, "initial")
```

By default each table named in the file is truncated and re-inserted, so a
dataset returns that table to exactly what it describes regardless of what the
previous phase did to it. Tables the file does not mention are untouched.

Under `WithTransaction` both helpers operate inside the test transaction. That
is what makes them usable together: `AssertDB` sees writes the request has not
committed, and rows `Seed` adds disappear with the rollback. Without it,
`AssertDB` compares committed state only, and a request whose transaction is
still open has not been compared at all.

### Adding to a table instead of replacing it

Truncate-then-insert is the default, not the only option: `_operation` selects
`insert`, `upsert`, `truncate`, or `delete` per table, and
[the dataset format](/productivity/seed-data/#_operation-what-happens-to-the-table-first)
spells all five out.

Two of them carry a constraint a test meets before anything else does.
`upsert` and `delete` need the table's primary keys, and that lookup runs on the
pool rather than on the transaction doing the seeding. A pool capped at one
connection is therefore already empty when the lookup runs, and the seed stops
there and waits. Reach for one of these two operations only under
`WithTransaction`, which puts the lookup on the test transaction, or against a
database whose pool can open a second connection — which rules out
`sqlite://:memory:`, where a second connection would be a second empty database.
`insert`, `truncate`, and the default need no primary keys and are unaffected.

### Row tags are parsed but never filter

A row may carry a `_tag` list, and dbtestify's CLI filters on it. Popcorn Web
exposes neither the include nor the exclude filter, so every row in the file is
applied whatever its tags say. Split the rows across files when a test needs a
subset.

## Asserting against the database

A fixture compares whole tables. When the check is narrower than that — one
counter, one column, a value you want to compute in Go — read it directly
instead. HTTP assertions show what the client observed; database assertions
often need the same runtime state that produced it. `server.Context()` carries the
resources installed on requests, including the `WithTransaction` transaction,
so generated queries can prepare or inspect data within the transaction used by
the handlers:

```go
counter, err := queries.CurrentAccess(server.Context())
```

`server.DB` exposes the pool directly when you need raw SQL.

## When the test needs a browser

Everything on this page observes the application from a Go `http.Client`: it
sees responses, not what a browser does with them. A dialog that submits, a
fragment swapped into the page, a login the user actually clicks through —
those need a real browser, and the same seed datasets serve there too.
[E2E Testing](/productivity/e2e-testing/) covers driving the application with
Playwright and reseeding the database from browser tests.
