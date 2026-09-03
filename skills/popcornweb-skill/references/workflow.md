# Development workflow and productivity tooling

How to work on a Popcorn Web project day to day: the `pw dev` loop and what it watches, the browser dev console, unit tests with `testutil`, seed data and fixtures, Playwright e2e tests against the dev server, viewing generated API docs, the startup summary, and `pw doctor` diagnostics.

## The pw dev loop

`pw dev` is the everyday command. It takes no arguments; `popcornweb.toml` defines the loop. On startup it:

1. starts the Devbox services declared in `devbox.json` (Valkey by default);
2. runs `pw generate` (recreates every `_pw_gen.go` build output);
3. applies pending migrations, unless `migration.auto = false`;
4. builds the Tailwind stylesheet and starts its watcher, if Tailwind is enabled (always unminified in dev);
5. starts the development identity provider, if `dev.idp.enabled = true`;
6. starts the telemetry viewer, unless `dev.otel.enabled = false`;
7. builds and runs `project.main`.

It then polls watched files twice a second; a change repeats only the affected steps. Watched: the project's Go, `.pw.html`, and `.pw.sql` sources; the migration directory; the Tailwind input; anything in `dev.watch.includes`. `.git`, `vendor`, `node_modules`, `.devbox`, and `public` are always skipped.

```toml
[dev.watch]
includes = ["config.dev.toml", "assets/**/*.svg"]
excludes = ["web/node_modules"]
```

`Ctrl-C` stops everything together. An application that exits on its own (compile error, panic) is reported as `application exited: …` and the loop keeps watching — the next save rebuilds and restarts.

**The port shifts in development.** If `server.port` is taken, a dev run moves to the next free port (up to ten along) and warns with both numbers; the `listening on` line is where your browser goes. Only `APP_ENV` resolving to development shifts — every other environment binds what it was configured with or fails.

**Services** run under the Devbox process manager with its terminal UI disabled; their logs join the same stream, one prefixed line per event (`[valkey ] ... Ready to accept connections`). A project that needs no service drops the package from `devbox.json` — `pw dev` starts whatever Devbox declares and nothing else.

**Migrations** are applied before the application starts and again when a file in the migration directory changes. Turn that off to control it yourself:

```toml
[migration]
auto = false
```

**Telemetry viewer.** `pw dev` also runs a loopback OpenTelemetry receiver with a browser UI (`pw dev: telemetry viewer http://127.0.0.1:54321`) and points the application at it through the standard OTLP environment variables, so traces and correlated logs are readable without a collector. It starts nothing when `OTEL_EXPORTER_OTLP_ENDPOINT` is already set.

**Local log capture.** The same application records are also written as newline-delimited JSON under `.log/` (`[dev.logs]`), one file per `pw dev` invocation. That is the file to query when a user asks what happened during a run — see references/telemetry.md.

## Development console

`pw dev` serves a browser console on a fixed loopback port, printed at startup:

```
pw dev: console http://127.0.0.1:18081
```

Panes:

- **Overview** — loop phase, last change, failure diagnostic, a link to the app, and a **reseed** button that applies the project's seed datasets (clear-insert).
- **Data** — a table browser on the connection the application itself opened (which is what makes SQLite files and `:memory:` reachable). Rows editable in place; nothing hits the database until save. Framework-owned tables are marked.
- **Statements** — run one SQL statement on the same connection; reads capped, writes not; `explain` reads the plan.
- **Declared queries** — every `.pw.sql` statement with typed parameters, runnable through the generated builder itself; unexported statements included.
- **Templates (storybook)** — every template rendered alone with synthesized, editable parameters, inside or outside the document shell; project stylesheet linked.
- **Doctor** — `pw doctor` in the browser.
- **Telemetry** — the dev telemetry viewer, traces named by method and path.
- **Static assets** — what is served, from where, as what.

Two things sit on the application's own pages rather than on the console. An **overlay** puts loop failures (generation, migration, build) over them and reloads a page whose application was replaced. A **launcher** — a small floating button, `bottom-left` by default — opens the console index in a tab of its own and reuses that tab afterwards; it carries a ring while the loop is between two working applications, which is the only thing on a stale page that says it is stale. Hovering it reveals a control that hides it until the tab closes.

The console is compiled under the `pwdev` build tag: `pw build` does not link any of it, and there is no production switch. Configuration keys: `dev.console.enabled`, `dev.console.port` (default 18081), `dev.console.assets.enabled`, `dev.console.data.enabled`, `dev.console.storybook.enabled`, `dev.console.overlay.enabled`, `dev.console.overlay.reload`, `dev.console.launcher.enabled`, `dev.console.launcher.corner`, `dev.otel.enabled`. Turning the overlay **and** the launcher off is what makes a development page byte-identical to a production one. There is no schema editing — the schema moves only by migration.

## Unit testing with testutil

`github.com/shibukawa/popcornweb/testutil` starts the real application — actual routes, middleware stack, and configuration binding — against an isolated copy of every registered setting, reached over HTTP.

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
	// assert on response
}
```

`TestRun` copies configuration, applies your customiser, reserves a loopback port, initialises, starts, and registers cleanup with `t`. The customiser sees port `-1`; use `server.URL` / `server.Port` after startup. The test binary must link the packages whose `init` registers the document shell and config:

```go
import (
	_ "myapp"            // public.go
	_ "myapp/templates"  // the document shell
)
```

Configuration customisers are methods on the config: `config.Get[T]()`, `config.Set(value)`, `config.Update(fn)` — typed, and they reach framework and application config the same way.

Options:

- `testutil.WithMigrations("../migrations")` / `WithMigrationsFS(fsys)` — apply the schema before start. SQLite replays a cached snapshot (this is what makes `sqlite://:memory:` work); PostgreSQL/MySQL apply directly to the configured database, so point the DSN at a suite-dedicated database.
- `testutil.WithSeed("initial")` / `WithSeedDir(dir)` — load datasets (default directory `testdata/seed`, `.yaml` optional) after the schema, before start.
- `testutil.WithTransaction(true)` — every request runs inside one transaction rolled back at test end; tests sharing a database stay independent and parallelizable. Application transactions nest as savepoints.
- `testutil.WithIdentityProvider(...)` — starts the same dev identity provider `pw dev` uses:

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

`WithLoginUser` pre-selects the subject so one `GET` to the login path completes the whole OIDC flow without a browser. Roster comes from exactly one of `WithIdPConfig`, `WithIdPRoster`, or `WithIdPUsers`. `server.LoginAs(t, "guest")` switches user mid-test.

### Fixtures: seed one file, assert another

A dataset becomes a fixture when a test treats it as known state — loaded before the request, compared after with `server.AssertDB`:

```go
server := testutil.TestRun(t, Handlers(), nil,
	testutil.WithMigrations("../migrations"),
	testutil.WithSeed("initial"),
)
// ... perform the request ...
server.AssertDB(t, "after_archive")
```

`AssertDB` compares whole tables and reports every drifted table via `Errorf` (test continues). Rows not in the file fail by default; columns the file omits are ignored (keeps serial ids and timestamps out). Per-table match strategy and matcher values:

```yaml
_match:
  member: exact          # the default
  access_log_2026_*: sub # extra rows tolerated; listed ones must exist

audit_log:
- { id: 1, created_at: [currentdate, 2m], message: [regexp, "^User .+ logged in$"] }
```

Matchers: `[null]`, `[notnull]`, `[any]`, `[currentdate, 2m]`, `[regexp, pattern]`.

`server.Seed(t, "initial")` reseeds mid-test (default clear-insert: named tables truncated and refilled; unnamed tables untouched). Per-table `_operation`: `clear-insert` (default), `insert`, `upsert`, `truncate`, `delete`. `upsert`/`delete` need a primary-key lookup on a second connection — use them only under `WithTransaction` or with a pool that can open one (not `sqlite://:memory:` with one conn). Keys are singular: `_operation`, `_match`, `_tag` (row tags are parsed but never filter — split files instead).

Under `WithTransaction`, `Seed` and `AssertDB` operate inside the test transaction; without it they see committed state only. For a narrow check (one counter, a computed value), query directly: `server.Context()` carries the same resources requests get, including the test transaction, so generated queries work — `queries.CurrentAccess(server.Context())`. `server.DB` exposes the pool for raw SQL.

## Seed data

Datasets live in `testdata/seed/*.yaml`, one file mapping table names to rows, inserted in written order:

```yaml
member:
- { id: 1, name: Frank }
- { id: 2, name: Grace }
```

`pw seed` applies every dataset in the directory (not subdirectories); `pw seed users orders` applies only those, in order; `.yaml` may be omitted. The same files serve the CLI, `testutil.WithSeed`/`AssertDB`, the console's reseed button, and the e2e endpoints — one format, no drift. Seeds follow migration routing (`middleware.rdb.write_group` / `migration_group`) and obey `APP_ENV`, so check the environment before seeding. YAML only — DBUnit XML/CSV/Excel are not read.

## E2E testing with Playwright

A browser test earns its cost only where the browser contributes behaviour a Go client cannot observe (dialogs, fragment swaps, visible login flow). Most coverage belongs in `testutil`. Configuration drives `pw dev` itself:

```ts
// playwright.config.ts
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
	testDir: './e2e',
	workers: 1,
	use: { baseURL: 'http://127.0.0.1:8080' },
	projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
	webServer: {
		command: 'pw dev',
		url: 'http://127.0.0.1:8080/',
		reuseExistingServer: !process.env.CI,
		timeout: 120_000,
	},
});
```

`workers: 1` is required arithmetic: one application, one database, committed writes — no `WithTransaction` rolls anything back across processes. Do not set `APP_ENV`: the suite runs in the development environment on purpose, and **it reseeds the tables its datasets name — hand-typed rows do not survive a run**.

In the `pwdev` build the application serves seed/assert endpoints:

```
POST /_pw/test/seed/{dataset}     apply a dataset
GET  /_pw/test/assert/{dataset}   compare the database (204 ok, 409 with per-table diff)
```

Three locks gate them: the `pwdev` build mode, `APP_ENV` resolving to `dev`, and a loopback caller with no forwarding header; otherwise 404, and a release binary carries no endpoint bytes. Wrap them in helpers and reseed in `beforeEach` — reset at the start, never clean up at the end:

```ts
export async function seed(request: APIRequestContext, dataset: string) {
	const response = await request.post(`/_pw/test/seed/${dataset}`);
	expect(response.status(), await response.text()).toBe(204);
}

test.beforeEach(async ({ request }) => seed(request, 'initial'));

test('archiving a member removes the row', async ({ page }) => {
	await page.goto('/members');
	await page.getByRole('row', { name: 'Grace' })
		.getByRole('button', { name: 'Archive' }).click();
	await expect(page.getByRole('row', { name: 'Grace' })).toHaveCount(0);
});
```

Select by role and visible text, not CSS classes. Datasets only the browser suite needs go in a subdirectory (`seed(request, 'e2e/wide_catalog')`) — a bare `pw seed` does not descend into subdirectories. The assert endpoint compares committed state, so assert after the page shows the result. Login flows: the dev identity provider is already part of the loop, locally and in CI — drive the login page like any page; login *logic* belongs in a Go test with `WithIdentityProvider`. A dedicated database means a dedicated environment (`APP_ENV=e2e`, its own `config.e2e.toml`) and seeding via the `pw seed` CLI, since the endpoints are locked to dev — but session-cookie flows keep the suite in dev (`session.cookie.secure = false` is dev-only).

## Development identity provider

`pw dev` runs a local OpenID Provider when `dev.idp.enabled = true` in `popcornweb.toml`, with a roster in `devidp.toml` (users, display names, claims, extra scopes). Sign-in is picking a user from a list — no password — so it never runs outside development, and `pw build` refuses a binary that imports it. It registers an ephemeral client per run and injects `AUTH_OIDC_ISSUER`, `AUTH_OIDC_CLIENT_ID`, `AUTH_OIDC_CLIENT_SECRET` as environment variables, so no provider credential enters a committed file. Editing the roster reloads in place. Pin `dev.idp.port` when account identity matters: the resolver derives accounts from `issuer + subject`, and a moving port changes the issuer every run.

## Viewing API documentation

The OpenAPI 3.1 document is assembled from the code (routes, `pw.Parse` structs, tags, response calls) — nothing to keep in sync. Serving is configured:

```toml
[server]
openapi = "/openapi.json"    # unset serves nothing (no default)
api_doc = "scalar"           # "scalar", "swagger", or empty
api_doc_path = "/docs"
```

`pw init` writes `api_doc = "scalar"` into `config.dev.toml` only, so a fresh project answers on `/openapi.json` and `/docs` in dev. `api_doc` requires `openapi` or startup fails. Both paths sit beneath the auth chain — list them in `auth.protection.include` to protect them. The UI loads from a public CDN; the endpoint substitutes its own CSP on that page only. To improve the document, write handler and field godoc (first sentence = summary) and declare constraints in tags; set `pw.SetOpenAPIInfo` once in `main`.

## Startup summary

Resolved configuration is reported once per start. On a terminal it is a tree ending with `listening on http://localhost:8080`; values that came from other than built-in defaults are marked `← file`, `← env`, or `← flag`. Elsewhere (pipe, container) the same facts become one structured slog record. `observability.boot_log` overrides: `auto` (default), `tree`, `record`, `off`. Secrets are masked; DSNs keep their public location but lose credentials. With `pw.Middlewares` instead of `pw.Run`, the summary is emitted after initialization without the `listening` line. Note the configured `server.port` and the `listening` address can differ in dev (port shift).

## Message catalogs

In a project declaring `[i18n]`, a misspelled message ID, a missing argument, or a language that never supplied a translation already fails `pw generate`. `pw i18n` answers what a build cannot:

```bash
pw i18n check                    # messages nothing references; translations that drifted from their source text
pw i18n extract                  # convert i18n-marked template text into catalog entries and references
pw i18n rename <from> <to>
pw i18n export <locale> [file]   # hand work to a translator
pw i18n import <locale> <file>
```

`extract` prints every ID it chose, and declines a sentence carrying markup — that shape is the rich block form, and a hole name invented by a tool is one no translator can check. See references/i18n.md.

## Doctor diagnostics

`pw doctor` reports one finding per condition with a stable, never-reused identifier (`PW0412`), each with severity, scope, and a fix. Severity depends on the diagnosed environment: `pw doctor --env=prod` judges the same file more strictly than `--env=dev`, and `--env=all` walks every configuration the project holds. `--format=json` is the machine-readable form, `--strict` turns warnings into a failing exit, and `--online` also checks the live database against migration sources. The same checks run in the console's doctor pane.

Categories: PW01xx project/toolchain (stale `_pw_gen.go` files, devbox/go.mod drift, port already bound), PW02xx routes/templates, PW03xx storage/migrations (duplicate versions, database behind sources → `pw migrate up`), PW04xx configuration/secrets/identity (unlinked backends, secrets in files, dev-only relaxations left on), PW05xx production readiness (exposed api_doc, unminified Tailwind, unrevocable session backend). The full catalog is `website/src/content/docs/appendix/diagnostics.md` on the docs site (Appendix → Diagnostics); look findings up by their PW-code.

## Common mistakes

- **Running the app with `go run` instead of `pw dev`.** You lose generation on change, auto-migrations, services, the identity provider, the console, and the telemetry viewer.
- **Editing `_pw_gen.go` to fix a build error.** They are build outputs; fix the `.pw.html`/`.pw.sql`/Go source and let `pw dev` (or `pw generate`) regenerate.
- **Reading the port from the customiser in `TestRun`.** It sees `-1`; use `server.URL` after startup.
- **Forgetting the blank imports in test binaries.** Without `_ "myapp"` and `_ "myapp/templates"`, the document shell never registers and HTML rendering fails at startup.
- **Using `AssertDB` without `WithTransaction` while a request's transaction is open.** It compares committed state only — the write you meant to assert may not be there yet.
- **`upsert`/`delete` seed operations on a one-connection `sqlite://:memory:` pool without `WithTransaction`.** The primary-key lookup needs a second connection and hangs or sees an empty database.
- **Pointing `WithMigrations` at a shared database that carries another project's version numbers.** Applied versions are recorded by number; your migration looks applied and the schema never arrives.
- **Relying on `_tag` filtering in datasets.** Tags are parsed but never filter; split rows across files.
- **Playwright with `workers > 1`, or cleaning up at test end instead of reseeding at test start.** Committed writes interleave; a failed cleanup poisons the next test.
- **Setting `APP_ENV` for the e2e suite while still using the `/_pw/test/*` endpoints.** They exist only in the dev environment; a dedicated environment must seed via the `pw seed` CLI.
- **Expecting `/openapi.json` or the console in production.** `server.openapi` has no default, and the console/test endpoints are `pwdev`-tagged code absent from `pw build` output.
- **Ignoring the port-shift warning.** Your browser and Playwright `baseURL` must follow the `listening` line, not `server.port`; for a fixed port, free it (see `pw doctor` PW0125).
