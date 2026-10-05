---
title: SQL Query Format
description: The complete .pw.sql language — statement kinds, parameter types, conditional SQL, predicates and relations, and the checks that reject a statement.
sidebar:
  order: 3
---

A `.pw.sql` file is a typed query language compiled to Go by `pw generate`. The
SQL inside it stays SQL — nothing is translated, rewritten, or portable — while
its boundary with Go becomes checked: parameter types, result columns, and the
presence of a `WHERE` clause are all decided at build time.

This page is the whole language. For choosing between the generated layer and
raw access, and for how a statement finds its connection, see
[Queries](/guides/storage/queries/) and
[Relational databases](/guides/storage/rdb/).

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

The file opens with the Go package its generated code joins. Each `.pw.sql`
compiles into a `_pw_gen.go` of its own, in the same Go package as every other
file of its directory, and generation reads only the directories
`generate.queries` lists in `popcornweb.toml`. A `.pw.sql` outside every listed directory is
reported rather than silently skipped.

`generate.queries` must be empty in a [component
package](/guides/deployment/package/): a generated query carries one engine's
placeholder syntax, and a package cannot know its consumer's.

| Declaration | What it introduces |
| --- | --- |
| `package name` | the Go package the generated file joins |
| `type Name { field: T … }` | a result shape; becomes a Go struct of the same name |
| `statement name(…): kind { … }` | a package-private statement |
| `export statement Name(…): kind { … }` | the same, published as Go API |

A `type` is visible only to the statements of the file that declares it. Its
name is nevertheless unique across the directory, because every file's struct
lands in one Go package. The two rules meet when two files return the same
shape: naming a type another file declared fails with `unknown type Name`, and
declaring it again under the same name fails with `duplicate generated template
declaration Name`. Keep the statements that share a result type in one file.
Where they have to stay apart, each file declares the shape under a name of its
own — `NewProjectID` in one, `NewTicketID` in the other.

## The dialect

The placeholder token comes from `project.database` in `popcornweb.toml`:
`$1`, `$2`, … for `postgres`, and `?` for `mysql` and `sqlite`. You write
`{name}` either way and the generated signatures are identical, so switching
engines changes the emitted SQL text and nothing you call.

That token is the **only** thing the dialect changes. Everything else reaches
the generated SQL verbatim: `||` is not rewritten into `CONCAT`, `ON CONFLICT`
is not translated into `ON DUPLICATE KEY UPDATE`, and MySQL's missing
`RETURNING` is not worked around. A translation layer of that kind looks correct
and fails subtly — `||` is concatenation in PostgreSQL and SQLite but logical OR
in MySQL, so rewriting it can invert a predicate. Write for the engine you
selected.

One generated package therefore serves one engine, which is worth weighing
before reaching for SQLite in tests against a PostgreSQL deployment. The two
share `RETURNING` and `ON CONFLICT`, so plain CRUD often does port — but nothing
checks that it did, and the package you exercise is not the one you ship.

## Types

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

The table stops at the Go type; your driver has to agree as well. Use an
optional type wherever NULL is possible — a required `string` reading a NULL is
an error rather than an empty string.

Two rows need more than the driver's agreement:

- A `url` column is carried as text in both directions. A `url.URL` parameter
  binds as its string form and a returned column is parsed back, because
  `database/sql` can neither bind nor scan a struct.
- `datetime`, `date`, and `time` require the driver to hand back a `time.Time`.
  With MySQL that means `parseTime=true` in the DSN; with SQLite it depends on
  the driver and the column's declared type, since SQLite stores no date type of
  its own. Either way it is driver configuration, and the dialect selection
  cannot set it for you.

## Statement kinds

| Kind | Contract | Generated result |
| --- | --- | --- |
| `sql.exec` | no row result | `sql.Result` |
| `sql.one<T>` | exactly one row | `T`; zero rows is `sql.ErrNoRows`, several rows is an error |
| `sql.optional<T>` | zero or one row | `*T`; zero rows is `nil, nil`, several rows is an error |
| `sql.many<T>` | zero or more rows | `iter.Seq2[T, error]`, streamed rather than accumulated |
| `sql.predicate` | a reusable condition | none — usable only from another statement |
| `sql.relation<T>` | a typed subquery | none — usable only from another statement |

`sql.many` scans and yields one row at a time; no slice accumulates behind the
iterator. Breaking out of the range closes the underlying `sql.Rows`, and query,
scan, and iteration errors all arrive through the error value:

```go
for user, err := range queries.ListActiveUsers(ctx, true) {
	if err != nil {
		return err
	}
	consume(user)
}
```

## Parameters

Every `{name}` in a body is a prepared-statement placeholder carrying a declared
parameter. Template expressions are never concatenated into SQL text, so value
binding cannot create an injection-prone query.

```sql
export statement RenameUser(id: int, name: string): sql.exec {
UPDATE users
SET name = {name}
WHERE id = {id}
}
```

```go
statement, err := queries.BuildRenameUser(42, "Ada")
// statement.SQL  == "... SET name = $1 WHERE id = $2 ..."
// statement.Args == []any{"Ada", 42}
```

The guarantee is absolute and it costs something. A handwritten `$1` or `?` is a
generation error, and a value parameter can never stand in for a structural
element — a table name, a column name, an operator, a sort direction.

Two parameter names are refused: `ctx` and `db`, which are the context and the
executor in the public signature of every generated function. Everything else is
available, `err` and `result` included, because generated code prefixes the
variables it introduces with an underscore.

### Slice expansion

A slice parameter expands into a value list:

```sql
export statement FindUsers(ids: int[]): sql.many<User> {
SELECT id, name, active
FROM users
WHERE id IN ({ids})
ORDER BY id
}
```

An empty slice has no valid rendering, so the builder returns an error rather
than emitting `IN ()`. Handle the empty case in the caller, or use a condition
to select a different SQL structure.

## Result types and SELECT columns

The order of result fields must match the SELECT or RETURNING column order, and
each column name or alias must correspond to a field name. Generation checks
both, so a SELECT list that drifts away from its result type fails the build
rather than the query:

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

That check only holds if the shape is knowable statically, which is why a
runtime condition may not add or remove a SELECT or RETURNING column. Keep the
result shape identical across every branch.

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

`{else}` is available, and the condition must be `bool`. Only the branches that
survive consume placeholders, so numbering and `Args` stay aligned however the
branches fall.

### Operators and commas between conditions

You do not manage the `AND`, the `OR`, the commas, or the parentheses. Write the
statement you want when every condition holds, and punch the conditions out of it:

```sql
export statement SearchUsers(
  name: string, city: string, minAge: int,
  hasName: bool, hasCity: bool, hasAge: bool, staffOnly: bool
): sql.many<User> {
SELECT id, name, city, age
FROM users
WHERE
  {if hasName}name LIKE {name}{/if}
  AND {if hasCity}city = {city}{/if}
  AND ({if hasAge}age >= {minAge}{/if} OR {if staffOnly}role = 'staff'{/if})
ORDER BY id
}
```

Read that with the `{if}` wrappers deleted and it is the SQL it renders. With only
`hasCity` set it renders `WHERE city = $1` — the operator that would have dangled
is withheld, the empty parenthesised group takes its own parentheses and the `AND`
that attached it, and `city` becomes `$1` rather than `$2`. With nothing set the
`WHERE` itself never appears. An operator that is *not* dangling is written exactly
where you put it, including the newline and indent you wrote, so a predicate that
worked before renders the same bytes.

Put the operator between the two conditions, in the enclosing text. That is where
it sits in the finished statement, which is what lets the source read as the SQL.
An operator inside the branch — `{if hasCity}AND city = {city}{/if}` — works
identically and older templates are written that way, but it reads as part of that
one condition when it really joins two.

This covers `WHERE`, `HAVING`, `QUALIFY`, and a join's `ON`, plus the parenthesised
groups inside them. Commas are managed in `SET`, `VALUES`, an `INSERT` column list,
`ORDER BY`, `GROUP BY`, `FROM`, `WITH`, `WINDOW`, `USING`, and `PARTITION BY`; an
`ORDER BY` or `GROUP BY` whose every item is conditional drops its own keyword the
way `WHERE` does.

```sql
export statement AddUser(id: int, name: string, city: string, withCity: bool): sql.exec {
INSERT INTO users (id, name{if withCity}, city{/if})
VALUES ({id}, {name}{if withCity}, {city}{/if})
}
```

Guard a column and its value with the **same** condition. Generation follows each
branch path and requires the two counts to end equal, so two independent conditions
each guarding a matched pair are fine, and so is one `{if}/{else}` choosing a column
and the same `{if}/{else}` choosing its value. A multi-row `VALUES`, an
`INSERT … SELECT`, an `INSERT` with no column list, and a `sql.predicate` inside a
list are left undecided rather than guessed.

`SELECT` and `RETURNING` keep their commas as written, because a conditional result
column is forbidden outright — that refusal answers the question before a comma is
reached. An `OVER (…)` in the select list is a result context for the same reason,
so a conditional `PARTITION BY` item belongs in a `WINDOW` clause.

### Where it deliberately does not reach

A parenthesis that follows a word is data rather than a group, so an `IN ({ids})`
list and a function argument list keep their parentheses and their commas in every
branch — eliding an argument would change the call's arity. `USING (…)` keeps its
own for the same reason, since that parenthesis carries a derived table in
`DELETE FROM t USING (SELECT …) s`.

The `AND` that closes a `BETWEEN` belongs to that form rather than to the clause, so
splitting one across a condition is a generation error. Put the whole `BETWEEN`
inside the condition:

```sql
-- rejected
WHERE n BETWEEN {lo} {if hasHi}AND {hi}{/if}
```

A `CASE` arm is neither a clause nor a list, so there is no keyword to withhold and
no separator to drop — an empty fragment would leave `CASE WHEN THEN`. A fragment
inside `CASE` that can emit nothing is therefore a generation error:

```sql
-- rejected
WHERE CASE WHEN {if flagA}a{/if} THEN 1 ELSE 0 END = 1
```

Giving the condition an `{else}` that also emits makes it legal, because it can no
longer be empty. Branches that leave different parenthesis nesting are an error too,
rather than a paired guess.

None of this loosens the mutation rule below. An `UPDATE` or `DELETE` still needs a
`WHERE` that is provably non-empty on every branch, and an `UPDATE` whose `SET` items
are all conditional is still refused, because a withheld comma fills nothing.

## Predicates and relations

A private `sql.predicate` is a reusable condition:

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

A private `sql.relation<T>` is a typed subquery usable in `FROM subquery` or
`JOIN subquery`. Its name starts with an uppercase letter: that is the only
spelling `FROM subquery` reads as a relation, so one declared as `activeUsers`
fails where it is used, with `subquery relation name must be PascalCase`.

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

Composition does not fragment the parameter list: subquery and outer arguments
share one placeholder sequence, ordered as they appear in the final SQL. The
alias is explicit and lower snake case. Recursive relations are rejected.

Neither kind can be exported, and neither generates a function of its own.

## Two safety rules

### UPDATE and DELETE require a WHERE clause

Whether a clause can come out empty is a property of the template rather than of
runtime data, so the whole proof runs at generation time and no guard is emitted
into generated code.

```sql
-- rejected: one call path deletes every row
export statement UnsafeDelete(id: int, enabled: bool): sql.exec {
DELETE FROM users
{if enabled}WHERE id = {id}{/if}
}

-- accepted: no path leaves the clause empty
export statement SafeDelete(id: int, name: string, byID: bool): sql.exec {
DELETE FROM users WHERE {if byID}id = {id}{else}name = {name}{/if}
}
```

The keyword has to belong to the statement itself. A `WHERE` inside a subquery,
a CTE body, a string literal, or a comment does not satisfy the requirement. The
same proof covers a dynamic `SET` list — an UPDATE whose assignments are all
conditional is an error — and it applies to every cardinality, so a
`DELETE … RETURNING` declared `sql.one<T>` is proven the same way. A
`sql.predicate` satisfies the requirement only when it is itself non-empty on
every path.

There is no opt-in for a deliberate full-table UPDATE or DELETE. Write that as a
[migration](/productivity/migrations/).

### SELECT columns must match the result type

Covered above, and it is the other half of the same idea: combined with the rule
against conditional result columns, it keeps the generated struct an accurate
description of every row the statement can return.

## `export` and name casing

`export` decides whether a statement joins the package's public Go API. The
generated function is named exactly as the statement is declared, so the name's
own case is what Go reads, and it has to agree with `export`:

| Declaration | Generated | |
| --- | --- | --- |
| `export statement FindUser(…)` | `func FindUser(…)` | public API |
| `statement findUser(…)` | `func findUser(…)` | package-private, callable anywhere in the package |
| `export statement findUser(…)` | — | error: `export` cannot publish an unexported name |
| `statement FindUser(…)` | — | error: the name would be public without `export` |

`sql.predicate` and `sql.relation` are the exception. They are embedded into
another statement's builder rather than executed, so they generate no function
of their own name and `export` has nothing to agree with. A predicate takes
either case. A relation is written in PascalCase without `export`, because
[`FROM subquery`](#predicates-and-relations) accepts no other spelling.

## Generated signatures

Popcorn Web generates the **context-resolved** form under the declared names.
No exported function takes a `*sql.DB` or a `*sql.Tx`; the executor comes from
the context, which is why the same function works inside a transaction and
outside one.

```go
func Name(ctx context.Context, p ...P) (sql.Result, error)   // sql.exec
func Name(ctx context.Context, p ...P) (T, error)            // sql.one<T>
func Name(ctx context.Context, p ...P) (*T, error)           // sql.optional<T>
func Name(ctx context.Context, p ...P) iter.Seq2[T, error]   // sql.many<T>

func BuildName(p ...P) (sqlbind.Statement, error)            // every exported statement
```

`p ...P` stands for the mapped template parameters. A private statement receives
the same pair under `name` and `buildName`.

`Statement` is declared once in `github.com/shibukawa/tinybind-go/sqlbind`
rather than per generated package, so its value crosses package boundaries
unchanged:

```go
type Statement struct {
	SQL  string
	Args []any
}
```

`BuildName` is what a SQL test, a log line, or a custom database abstraction
uses:

```go
statement, err := queries.BuildGetUser(42)
log.Printf("sql=%s args=%v", statement.SQL, statement.Args)
```

## Where a statement runs

Nothing in a `.pw.sql` names a database. The context carries the pool of the
effective connection group in an ordinary request and the active transaction
inside `pw.Transaction`:

```go
err := pw.Transaction(r, func(ctx context.Context) error {
	if _, err := queries.InsertUser(ctx, name); err != nil {
		return err
	}
	return queries.RecordAudit(ctx, "user.created")
})
```

A statement that says nothing about where it runs goes to the default group;
`pw.SelectDB` and `pw.SelectWriteDB` pin one, for a single statement and for a
whole `pw.Transaction` alike. A generated function never learns the topology,
which is why one development SQLite file can answer every group name. See
[Relational databases](/guides/storage/rdb/) and
[Runtime API](/reference/runtime/#database).

## Grouping JOIN rows

A JOIN returns the parent row again for every child, and no cardinality
declaration can undo that flattening. `sqlbind.ScanRows[T]` rebuilds the tree
afterwards, on any query — SQL templates are not involved.

```go
type Organization struct {
	ID    int    `db:"organization_id" groupkey:""`
	Name  string `db:"organization_name"`
	Users []User
}

type User struct {
	ID   int    `db:"user_id" groupkey:""`
	Name string `db:"user_name"`
}
```

```go
rows, err := db.QueryContext(ctx, `
SELECT o.id AS organization_id, o.name AS organization_name,
       u.id AS user_id,         u.name AS user_name
FROM organizations o
LEFT JOIN users u ON u.organization_id = o.id
ORDER BY o.id, u.id`)
if err != nil {
	return nil, err
}
defer rows.Close()
return sqlbind.ScanRows[Organization](rows)
```

| Rule | Detail |
| --- | --- |
| `groupkey:""` | exactly one scalar field per grouped struct level |
| `db:"alias"` | the column alias a scalar field reads; without the tag, the snake-case form of the Go field name |
| Same root key | rows merge into one root object |
| Same child key | rows merge into one child object |
| NULL child key | that child is absent, which is what an outer join means |
| NULL root key | an error |

Two constraints decide when to reach for it. `ScanRows` targets host Go with
`database/sql` and is **excluded from TinyGo builds**, and it consumes every
result row to construct the tree, so a very large result is held in memory.
Reach for `sql.one`, `sql.optional`, or `sql.many` for ordinary queries, where
rows stream past one at a time, and for `ScanRows` when a JOIN keeps repeating
the same parent and the parent has to come back whole.

## Common errors

Generation:

- a handwritten `$1` or `?` placeholder
- a SELECT column count or name that disagrees with the result type
- a SELECT or RETURNING column added or removed by a condition
- an UPDATE or DELETE with no proven `WHERE`, or an UPDATE whose `SET` items are all conditional
- a non-`bool` condition in `{if …}`
- an INSERT whose column count and value count can disagree on some branch
- a `BETWEEN` whose closing `AND` is split across a condition
- a conditional fragment inside `CASE` that can emit nothing
- branches that leave different parenthesis nesting
- a parameter named `ctx` or `db`, which are the context and executor of every generated function
- a recursive `sql.relation`
- a `sql.relation` whose name starts with a lowercase letter
- a `type` used from a file other than the one that declares it, or declared in two files of one directory
- an `export` that disagrees with the statement name's casing
- a `.pw.sql` in a component package

Run time:

- an empty slice passed to an expanded value list
- zero or several rows for `sql.one`, several rows for `sql.optional`
- a query error ignored while ranging over `sql.many`

Every generated statement is logged with its duration in `dev`, and anything
slower than the threshold brings a query plan and a paste-able rerun snippet
with it. See [Query Diagnostics](/productivity/query-diagnostics/).
