---
id: requirement:cloudflare-d1-engine
type: requirement
title: Cloudflare D1 Engine
---
D1 is a database engine a project selects like sqlite, postgres or mysql, so a Worker gets its relational store, its session store and its auth state through the code those engines already have, and development keeps running on SQLite with the same dialect.

```yaml
audience: actor:application-developer
priority: first of decision:cloudflare-bindings-placement
motivation:
  - D1 is SQLite, and system:syumai-workers registers it as the database/sql driver named d1, so the SQLite dialect of requirement:database-engine-selection, api:session-store and requirement:contrib-auth-state applies as it is
  - it is the one Cloudflare store with read-your-writes consistency, which sessions and single-use auth state need
  - a Worker without it has cookie sessions and nothing else that persists
shape:
  engine: project.database stays sqlite; D1 is selected by the DSN scheme alone, per rule:rdb-dsn-resolution, because the engine registers under the sqlite dialect and a project's .pw.sql, migrations, session and auth-state code are the sqlite ones already
  scheme: rule:rdb-dsn-resolution gains d1://BINDING, where BINDING is the wrangler binding name; no credential, no host; the registry maps the scheme to the sqlite dialect with its own opener
  package: database/d1, whose js && wasm file opens the binding through system:syumai-workers' d1 driver and whose host file registers the scheme with an opener that reports ErrOutsideWorker, so a d1:// connection validates on the host and opens only in a Worker
  linking: the generated entry of requirement:cloudflare-workers-build-target blank-imports database/d1, so an application names the binding in config.prod.toml and changes no source; its own sqlite import stays for development
  stores: sessionstore and authstate reach D1 through their existing sqlite dialects, so no new store package exists; the rdb session backend and the auth plugin's rdb backend are what a project selects
  development: api:cli-dev runs on the sqlite:// file config.dev.toml names, because the dialect is the same and wrangler dev is not the developer loop; config.prod.toml names the d1:// binding
  wrangler: api:cli-build --target cloudflare-workers writes one d1_databases element per binding the production connections name, taking database_name and database_id from the [[deploy.cloudflare.d1]] table of data:project-config and deriving a name from the binding when the table is silent; a missing id is reported, because wrangler dev runs without one and wrangler deploy does not
  migrations: the build stages the up half of every goose migration under the stage's migrations directory, directives removed, and names it as migrations_dir, so wrangler d1 migrations apply runs the same files goose runs in development; goose itself never runs in the Worker
  transactions: not supported, decided 2026-09-02; the d1 driver implements no BeginTx, so pw.Transaction and the auto_transaction setting fail on a D1 connection, and a handler writes one statement at a time, which is what the framework's own stores already do
  pool: D1 has no connection pool; the pool settings of data:server-runtime-config are accepted and have no effect
open:
  - the per-request main of requirement:cloudflare-workers-build-target means the driver is opened per request; measured cost decides whether that needs a shortcut
  - batch statements, which D1 offers and the driver may not expose
  - seeding on the host reads a DSN through a parser of its own that knows no d1 scheme; development seeds through sqlite, so nothing is lost yet
non_goals:
  - transactions on D1; the driver has none and the framework does not emulate one
  - D1 from a process host through the REST API; that is a different client and not this engine
  - migrating an existing sqlite project's data to D1
acceptance:
  - a sqlite project whose config.prod.toml names a d1:// connection builds for the Worker target without a source change
  - the same project runs under api:cli-dev on SQLite without a change to its sources
  - the staged migrations apply through wrangler d1 migrations apply, and a generated query reads and writes through the binding under wrangler dev
  - requirement:cloudflare-process-state-refusal accepts the d1 scheme and refuses the others
```
