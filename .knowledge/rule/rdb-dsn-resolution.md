---
id: rule:rdb-dsn-resolution
type: rule
title: RDB DSN Scheme Resolution
---
The rdb DSN scheme resolves to an opener and a dialect, not to a database/sql driver name, because a first-class engine may register no name and may not accept a URL.

```yaml
source: each data:database-connection-set element dsn, in the form scheme://rest
registry:
  package: github.com/shibukawa/popcornweb/database
  form: an engine package registers its dialect, schemes, opener, and handoff form from init, per decision:import-registered-session-plugins
  native_opener: an engine may additionally register a native opener that bypasses database/sql; requirement:pgx-native-execution defines the capability and its selection
  linking:
    application: every selected engine is linked by an application blank import that api:cli-init scaffolds into the entry point
    effect: a binary with no relational database carries no SQL driver, and one engine does not pull in the other two
    pw_cli: links every engine, because one binary migrates and seeds any project
schemes:
  - api:rdb-middleware startup
  - api:migration-runner and api:cli-migrate dialect selection
  - api:cli-seed and api:test-seed connector selection
  - rule:savepoint-dialect-support and rule:explain-dialect-support
engines:
  sqlite:
    engine: requirement:contrib-sqlite
    opener: sql.Open with the facade driver name
    handoff: the remainder after the scheme, because the driver takes a path or :memory:
    dialect: sqlite3
  postgres:
    engine: requirement:contrib-postgresql
    aliases:
      - postgresql
    opener: the package Open function, because the package registers no driver name
    handoff: the whole configured string, which is already a libpq URL
    dialect: postgres
  d1:
    engine: requirement:cloudflare-d1-engine
    opener: sql.Open with the d1 driver system:syumai-workers registers, inside a Worker only; the host build of database/d1 registers the scheme with an opener that refuses
    handoff: the remainder after the scheme, which is the wrangler binding name
    dialect: sqlite, so every dialect-keyed rule takes the SQLite path and only the opener differs
  mysql:
    engine: requirement:contrib-mysql
    opener: sql.Open with the registered mysql driver name
    handoff: the remainder after the scheme, because a go-sql-driver DSN is not a URL
    normalization: parseTime=true unless the DSN sets it, per requirement:contrib-mysql
    dialect: mysql
rules:
  - resolve a scheme to an opener, since not every first-class engine registers a database/sql driver name
  - derive the dialect from the resolved engine rather than from the scheme text, so an alias cannot produce a second dialect
  - apply the data:middleware-runtime-config pool bounds identically to every opener result
  - reject an unknown scheme at startup, naming the schemes this build registered
  - a scheme whose engine is not linked into the build fails with that fact, not with a driver-not-found error
  - keep the resolution table the single place a new engine is registered
  - normalize an engine DSN default at the engine, so every caller inherits it rather than repeating it
  - redact credentials from every error, log, and configuration view this resolution produces, in the rule:dsn-redaction form
migration_from_current:
  was: one sql.Open call that special-cased sqlite and passed every other configured string through unchanged
  breaks_on: requirement:contrib-postgresql, which has no driver name to open, and requirement:contrib-mysql, whose DSN is rejected with the scheme still attached
  becomes: a scheme table mapping to an opener, a handoff form, and a dialect
verification:
  - each scheme opens, pings, and reports its dialect
  - postgresql resolves to the same engine and dialect as postgres, and sqlite3 as sqlite
  - an unknown scheme names the schemes this binary serves
  - a shipped engine that was not linked names the import to add
  - a DSN carrying a password produces no output containing it
  - the same resolution serves api:rdb-middleware and api:migration-runner, so a DSN that opens also migrates
```
