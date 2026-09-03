---
name: popcornweb
description: >
  Guidelines for working in a Popcorn Web (pw) Go web project. Load this when
  creating or editing .pw.html templates, .pw.sql / .pw.dynamo / .pw.firestore
  queries, handlers, page trees, message catalogs, popcornweb.toml, or
  config.*.toml; when adding routes, server actions, component scripts,
  database access, caching, sessions, auth, WebSockets, or another language;
  when building for fasthttp, TinyGo, or a serverless host; or when running,
  checking, or debugging a project with the pw command (pw dev, pw generate,
  pw build, pw doctor, pw migrate, pw fmt, pw i18n); or when investigating
  structured development logs in .log with DuckDB.
---

# Popcorn Web

Popcorn Web is a Go web framework built around code generation: you author
typed template sources (`.pw.html`), typed query sources (`.pw.sql`,
`.pw.dynamo`, `.pw.firestore`), Go handlers, message catalogs, and TOML
configuration; the `pw` command generates the Go that glues them together.
Routing stays on `pw.ServeMux`, a type alias compatible with
`net/http.ServeMux`, and handlers stay `http.HandlerFunc`.

## Hard rules

1. **Never edit `*_pw_gen.go` files.** They are build outputs. Change the
   source (`.pw.html`, `.pw.sql`, handler signatures, config declarations) and
   run `pw generate` to rebuild them. They are gitignored in applications and
   committed in package projects — either way the generator owns them.
2. **A directory is invisible to generation until it is listed.** The
   `[generate]` table in `popcornweb.toml` lists source directories per
   purpose (`handlers`, `templates`, `queries`, `config`, `pages`, `dynamo`,
   `firestore`). Creating a new source directory without adding it there
   silently generates nothing.
3. **`.pw.html` is a typed template language**, not Go `html/template`.
   `.pw.sql` is a typed SQL template, not a string with placeholders. Do not
   guess syntax — read [references/templates.md](references/templates.md) and
   [references/sql.md](references/sql.md) before writing either.
4. **Prefer `pw new` over hand-scaffolding** a handler or page: it writes the
   route, the template, the loader, and the registration in the shape
   generation expects.
5. **Configuration is per-environment.** `config.dev.toml` is development;
   deployed environments read `config.prod.toml` (selected by `APP_ENV`) and
   pass secrets as `${ENV_VAR}` references, never literals. `popcornweb.toml`
   is build configuration and holds no runtime setting at all.
6. **A cache scope defaults to private, and that default is a security
   boundary.** `@cache` and the memo store both key per reader unless you write
   `scope: "public"`. Promote only output that is a function of its declared
   parameters and nothing else — see [references/caching.md](references/caching.md).

## Check loop — run after every change

Run these from the project root (inside `devbox shell` if the project has
`devbox.json`), in this order — `pw fmt` rewrites sources, so it comes before
`pw generate`, or doctor reports the generated files as stale (PW0111):

```bash
pw fmt             # rewrite template/query sources into canonical form
pw generate        # write every build input: *_pw_gen.go, the stylesheet, dist/public
go build ./...     # the generated code and your Go compile together
go test ./...      # unit tests
pw doctor          # configuration and project health report
```

- `pw fmt --check` and `pw check` verify without writing (CI, and package
  projects whose artifacts are committed). `pw check` compares generated Go
  only: the asset tree and the stylesheet are gitignored, so there is nothing
  committed to compare them against.
- `pw generate --code-only` stops after the generated Go. Use it in an inner
  loop; do not hand its output to a compiler, since `public.go` embeds a
  `dist/public` it did not build.
- `pw i18n check` additionally reports messages nothing references and
  translations that drifted from the source text they were translated from.
  Run it in a project that declares `[i18n]`; a missing or misspelled message
  already fails `pw generate`.
- `pw build` is the full pipeline: generate, build assets, compile the binary.
  Use it as the final gate for asset-touching changes, and for anything that
  must also compile on the fasthttp half (`pw build --backend fasthttp`).
- `pw doctor --env=prod` (or any config token, or `--env=all`) reports what that
  environment will actually run — use it after editing `config.<env>.toml`.
  `--online` additionally contacts the database and reads migration state;
  `--format=json` is the machine-readable form.
- Template/query type errors surface in `pw generate`; route conflicts and
  config mistakes surface in `pw doctor` and at startup.

Gotcha: a project scaffolded by an older `pw` release can fail `pw fmt --check`
before anyone edited anything — early scaffolds were not written in canonical
form. Run `pw fmt` once and commit; current releases scaffold sources that
already pass.

## Running the app

`pw dev` watches sources, regenerates, rebuilds, restarts, and starts the
development services the project declares (database server, Valkey,
dynamodb-local, dev identity provider, telemetry viewer). The startup summary
prints the port and every mounted route, and a floating launcher on every served
page opens the dev console. Development conveniences (dev console, relaxed auth,
seeded logins) exist only under `pw dev` — never replicate them in handlers.

For database schema changes: write a new `migrations/NNNNN_name.sql` (never
edit an applied one), then `pw migrate up` — or let `pw dev` apply it in
development. `pw migrate status` shows where you are.

## Where to read next

| Task | Reference |
| --- | --- |
| Project layout, generation model, routers, request path, build pipeline | [references/architecture.md](references/architecture.md) |
| Writing `.pw.html` templates (syntax, `val`, `check`, components, slots, types) | [references/templates.md](references/templates.md) |
| Page trees (discovered routing), async/partial/live rendering, forms | [references/rendering.md](references/rendering.md) |
| Server actions, component scripts, `on-<event>` handlers, signals | [references/interactivity.md](references/interactivity.md) |
| Handlers, request binding, responses, streams, WebSockets, middleware, sessions, auth | [references/handlers.md](references/handlers.md) |
| `@cache` on components, the memo store (`store.Get`) on fetched data, cache scope | [references/caching.md](references/caching.md) |
| Message catalogs, `{t}`, locale routing, the language switcher | [references/i18n.md](references/i18n.md) |
| `.pw.sql` queries, migrations, seed data, batching, the pgx escape hatch | [references/sql.md](references/sql.md) |
| DynamoDB and Firestore stores | [references/dynamo-firestore.md](references/dynamo-firestore.md) |
| `popcornweb.toml`, `config.<env>.toml`, config declarations | [references/config.md](references/config.md) |
| Build targets (net/http, fasthttp, TinyGo, WASI), build tags, serverless hosts | [references/deployment.md](references/deployment.md) |
| Local JSONL logs, trace correlation, and DuckDB analysis | [references/telemetry.md](references/telemetry.md) |
| pw dev, testing, e2e, API docs, diagnostics | [references/workflow.md](references/workflow.md) |

## pw command summary

| Command | Purpose |
| --- | --- |
| `pw init` | create a project in a new directory (wizard, or `--yes` + flags) |
| `pw add <capability>` | enable a capability declined at init (database, auth, tailwind, …) |
| `pw new [handler\|page]` | scaffold a handler or a page beside the ones you have |
| `pw generate [--code-only] [--backend …]` | write everything a compiler needs, stopping before the compiler |
| `pw check` | report generated files that are stale or missing |
| `pw fmt [--check] [--stdin=html\|sql\|dynamo] [<path>…]` | format template sources into canonical form |
| `pw i18n check\|extract\|rename\|export\|import` | reconcile message catalogs against the templates that use them |
| `pw migrate <action>` | inspect and apply database migrations |
| `pw seed [<name>…]` | load seed datasets into the database |
| `pw build [--backend …] [--target …] [--debug]` | run `pw generate` and then compile the project |
| `pw dev` | watch, regenerate, rebuild, restart, and run dev services |
| `pw doctor [--env=…] [--format=json] [--strict] [--online]` | report what an environment will actually run |
| `pw version` | print the version, revision, and toolchain |

Documentation: https://shibukawa.github.io/popcornweb/
