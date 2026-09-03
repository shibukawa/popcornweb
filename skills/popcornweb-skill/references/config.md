# Configuration

Popcorn Web has two configuration files read by two different programs at two different times: `popcornweb.toml` (build config, read by the `pw` command) and `config.{APP_ENV}.toml` (runtime config, read by the application binary). The split is enforced — a `[server]` or `[session]` table in `popcornweb.toml` is an error, and so is a DSN there. This file covers both, plus how application code declares its own typed configuration.

## popcornweb.toml (build config)

Sits at the project root, belongs to `pw`, and locates the project (commands walk up until they find it). Unknown keys are **errors**, not warnings. Relative paths resolve from the file's directory; absolute paths are rejected. Command flags override the file for one run.

### `[project]`

| Key | Default | Meaning |
| --- | --- | --- |
| `name` | *(required)* | project name; also the `OTEL_SERVICE_NAME` `pw dev` injects |
| `kind` | `"application"` | `application` builds a binary; `package` is a published Go module |
| `main` | *(required for an application)* | the package `pw build` compiles, e.g. `"./cmd/myapp"` |
| `toolchain` | `"tinygo"` | the compiler the sources were scaffolded for: `tinygo` or `go` |
| `database` | `"sqlite"` | the SQL dialect `.pw.sql` generates for: `sqlite`, `postgres`, or `mysql` |
| `fasthttp` | `false` | also generate and build the fasthttp transport half; `pw build --backend fasthttp` refuses without it |

`database` is a *generation* input: the engine actually connected to comes from the `[[middleware.rdb.connections]]` DSN scheme in the runtime file. Keeping the two in agreement is on you. `kind = "package"` carries no `main` and adds a `[package]` section; an application with a `[package]` section is an error.

### `[generate]`

```toml
[generate]
handlers = ["handlers"]
templates = ["handlers", "templates"]
queries = ["queries"]
config = ["cmd/myapp"]
pages = []
dynamo = []
firestore = []
```

Each purpose lists the directories `pw generate` may read for that purpose and nothing else. `handlers`, `templates`, `queries`, `config` are required with no default; `[]` means "deliberately generates nothing". `pages`, `dynamo`, `firestore` are optional (added by `pw add dynamo` / `pw add firestore` or when using discovered routing). Entries are project-relative existing directories, walked recursively, with no duplicates and no nesting between entries of the same purpose. Exactly one `templates` entry holds the document shell. See references/architecture.md for the full model.

### `[dev.watch]`

```toml
[dev.watch]
includes = []
excludes = []
```

Both optional — `pw dev` already walks the module for rebuild inputs. `includes` adds relative files or glob patterns the walk misses (`["config.dev.toml", "assets/**/*.svg"]`); `excludes` skips a subtree that only slows the walk. Absolute paths are rejected.

### `[dev.idp]`, `[dev.otel]`, `[dev.logs]`, `[dev.console]`

Development-only companions `pw dev` runs beside the application; they affect nothing else.

```toml
[dev.idp]
enabled = false
config = "devidp.toml"   # roster file; must exist when enabled
port = 0                 # 0 reserves a free loopback port; pw dev injects the issuer

[dev.otel]
enabled = true           # the telemetry viewer, on by default
port = 0
max = 0                  # records retained per signal; 0 keeps the viewer default

[dev.logs]
enabled = true           # write application records to .log/*.jsonl as well as the terminal
directory = ".log"

[dev.console]
enabled = true
port = 18081
assets.enabled = true
data.enabled = true
storybook.enabled = true
overlay.enabled = true       # failure overlay on the application's own pages
overlay.reload = true        # reload a page whose application was replaced
launcher.enabled = true      # floating link to the console on those pages
launcher.corner = "bottom-left"   # bottom-left | bottom-right | top-left | top-right
```

Turning the overlay **and** the launcher off is what makes a development page byte-identical to a production one; turning one off leaves the other working, which is why they are two settings.

### `[seed]` and `[assets.verify]`

```toml
[seed]
auto = false             # apply seed datasets in the pw dev loop

[assets.verify]
enabled = true           # refuse an authored public file whose bytes contradict its extension
svg_scan = true          # refuse an authored .svg carrying <script, an on…= handler, or javascript:
allow = ["vendor/**"]    # exempt paths, relative to public/; a trailing /** exempts a subtree
```

Unlike every conversion below, both verification checks default to **on** — they read bytes the asset walk already holds. A refusal fails `pw build` and names the file, what its extension claimed, and what the bytes carry; an exempted path is printed by the build so a stale `allow` entry does not go quiet. `pw doctor` reports the same as PW0130 and PW0131.

### `[i18n]`

Absent in a single-language project, and the catalog is a generation input rather than a runtime setting, which is why it lives here.

```toml
[i18n]
locales = ["ja", "en"]      # empty (or absent) disables everything below
default_locale = "ja"       # must be one of locales
catalog = "messages"        # directory of YAML catalogs, inside the project
missing = "error"           # or "warn"
prefix_default = true       # false drops the prefix from the default language's URLs
path_routes = ["/"]
cookie_routes = ["/admin/"]
header_routes = ["/api/"]

[i18n.label]
ja = "日本語"
en = "English"
```

Any other `i18n.*` key set with an empty `locales` is a load error, as is a `label` naming an undeclared tag. The longest matching route prefix wins. See references/i18n.md.

### `[migration]`

| Key | Default | Meaning |
| --- | --- | --- |
| `dir` | `"migrations"` | where migration files live, relative to the project |
| `auto` | `true` | apply pending migrations at the start of `pw dev` (dev loop only — never at application startup) |

### `[assets.tailwind]`

Present when scaffolded with Tailwind, absent otherwise.

```toml
[assets.tailwind]
enabled = true
input = "assets/app.css"
output = "public/generated/app.css"
minify = true
```

`input` and `output` must differ. `pw build` always minifies and `pw dev` never does; `minify` actually feeds `pw doctor`'s readiness finding. Leave it `true`. Tailwind plugins are `@plugin` declarations in the CSS entry, not keys here.

### `[assets.images]`, `[assets.css]`, `[assets.scripts]`

All default off; a project declaring none serves its authored tree verbatim.

| Key | Default | Meaning |
| --- | --- | --- |
| `assets.images.enabled` | `false` | convert `img src` PNG/JPG to WebP |
| `assets.images.quality` | `75` | lossy setting for JPEG re-encoding; PNG stays lossless |
| `assets.images.avif` | `false` | add an AVIF representation, chosen from `Accept` |
| `assets.css.minify` | `false` | minify stylesheets in place |
| `assets.scripts.enabled` | `false` | build a `.ts` entry point, minify authored `.js` |

### `[[packages]]` / `[package]`

An application lists each component package it links as `[[packages]]` with `module = "example.com/widget"` (must also be in `go.mod`). A `kind = "package"` project describes itself in `[package]` (`module`, `summary`, `requires.capabilities`, `migrations.dir`/`stem`/`engines`, `routes.register`, …). In a package, `generate.queries` must be empty.

## config.{APP_ENV}.toml (runtime config)

### Environment and file resolution

`APP_ENV` selects the environment: `dev`, `stg`, `prod`, or any token of lowercase letters, digits, `-`, `_`. Unset or empty means **`dev`**; an invalid token fails `ParseConfig`. `pw.Env()` returns the resolved token.

The loader reads the **first readable** candidate (files are never merged):

1. `./config.{APP_ENV}.toml`
2. `./config/config.{APP_ENV}.toml`
3. user configuration directory: environment-neutral `config.toml`
4. system configuration directory: likewise

A bare `config.toml` in the project tree is **never read**. `--config-path ./deploy/staging.toml` replaces the search entirely with no fallback. `pw.SetConfigLoadOptions` adjusts the search before `ParseConfig`.

Precedence, fixed: `default < TOML file < environment variable < command-line option`. An absent key in one layer never clears a lower layer's value.

### Deriving the three names of a key

Every struct field yields one stable key and two derived names. Take the TOML key, replace each `.` with `-`, prefix `--`: that is the option (`observability.query.slow_threshold` → `--observability-query-slow_threshold`). Take the option, drop the dashes, upcase: that is the environment variable (`OBSERVABILITY_QUERY_SLOW_THRESHOLD`). Underscores inside a key survive; only nesting dots change.

Deliberate exceptions: `server.port` answers to `PORT` / `--port`; `observability.service_name` to `OTEL_SERVICE_NAME`; `observability.otel.endpoint` / `.headers` to `OTEL_EXPORTER_OTLP_ENDPOINT` / `_HEADERS`; `auth.oidc.*` core keys to `AUTH_OIDC_*`. Three keys have no environment binding at all: `security.headers.content_security_policy`, its `_report_only` twin, and `security.headers.permissions_policy` — set them in TOML. The `[[middleware.rdb.connections]]` array is TOML-only (an array of tables has no flat name).

Durations are Go duration strings (`5s`, `200ms`, `24h`) — a bare number is rejected. Sizes are integers of bytes. Lists are TOML arrays, repeated CLI options, or comma-separated environment values.

### `${NAME}` environment expansion

Inside a TOML **string**, `${NAME}` expands from the process environment at load:

```toml
[[middleware.rdb.connections]]
group = "primary"
dsn = "postgres://app:${PRIMARY_DB_PASSWORD}@db1.internal:5432/app"
```

Rules: only strings expand (not keys, headers, numbers, booleans; array elements and `[[…]]` element fields do). An **undefined name fails the load** — an empty expansion would silently erase a default. A variable set to `""` counts as defined. `$$` yields a literal `$`. Expanded values still belong to the file layer for precedence. `${…}` in an environment or CLI value stays literal. No `${NAME:-default}` fallback form exists. This is the intended way to get credentials into array-of-tables elements, which have no option or variable of their own.

### `[server]` (selected keys)

| Key | Default | Meaning |
| --- | --- | --- |
| `port` | `8080` | HTTP listen port (`PORT`/`--port`) |
| `read_header_timeout` / `read_timeout` / `write_timeout` / `idle_timeout` / `shutdown_timeout` | `5s` / `30s` / `0s` / `2m` / `10s` | server timeouts; zero write timeout permits streams |
| `max_request_body` | `10485760` | bytes |
| `trusted_proxies` | `[]` | trusted proxy IPs/CIDRs |
| `health` / `readiness` / `openapi` | *(empty)* | endpoint paths; **empty serves nothing** — no default paths, deliberately |
| `api_doc` / `api_doc_path` | *(empty)* / `"/docs"` | `scalar`, `swagger`, or empty; requires `openapi` |
| `public.enabled` / `public.mount` / `public.read_local` | `true` / `"/public"` / `false` | embedded static assets |

An application route colliding with an enabled operational endpoint fails startup.

### `[middleware]` and `[[middleware.rdb.connections]]`

| Key | Default | Meaning |
| --- | --- | --- |
| `recovery` / `request_id` / `access_log` | `true` | standard stack |
| `compression` | `false` | encode rendered HTML and JSON for clients that accept it |
| `compression_codings` | `["zstd", "gzip"]` | codings to offer, best first; one left out is not offered at all |
| `request_timeout` | `"0s"` | per-request deadline |
| `rdb.enabled` | `false` | open the framework-owned database pool |
| `rdb.default_group` / `rdb.write_group` / `rdb.migration_group` | *(empty)* | connection group selection |

`compression_codings` is the server's preference order, not the client's `q`-values. An unknown name is a startup error; a known one whose encoder a build tag removed is skipped and named in the startup log. Turn compression off with `compression = false` rather than an empty list; encoder levels are deliberately not configurable. `Vary: Accept-Encoding` is set either way. (`pw_nozstd` and `pw_nogzip` were removed in favour of this key.)

Every database pool is one `[[middleware.rdb.connections]]` table; a reader-writer topology is several. The old `rdb.dsn` key is gone — an enabled database with no connections table fails at startup and names the replacement.

| Key | Default | Meaning |
| --- | --- | --- |
| `group` | *(empty)* | the name this connection is addressed by |
| `dsn` | *(empty)* | data source name; credential masked in reports |
| `readonly` | `false` | read-only transactions; never selected by `pw.SelectWriteDB` |
| `connect_timeout` | `"5s"` | per connection |
| `max_open_conns` / `max_idle_conns` | `0` | pool bounds |
| `conn_max_lifetime` / `conn_max_idle_time` | `"0s"` | |

### `[middleware.dynamo]`

Exists only when `github.com/shibukawa/popcornweb/database/dynamo` is imported. Keys: `enabled` (`false`), `region`, `endpoint` (empty selects the regional host; a value selects a local server), `access_key_id`, `secret_access_key`, `session_token` (redacted), `table_prefix`, `table_names` (`[]`), `timeout` (`"10s"`), `auto_migrate`. Declared-to-deployed table mapping: an explicit `[[middleware.dynamo.table_names]]` entry (`declared = "note"`, `deployed = "notes-prod-8f21c"`) wins; otherwise `table_prefix` applies; otherwise the declared name stands. A `table_names` entry naming a table no code declares is an error.

### `[middleware.firestore]`

Exists only when `database/firestore` is imported; Datastore mode required. Keys: `enabled` (`false`), `project_id` (falls back to `GOOGLE_CLOUD_PROJECT`, then `DATASTORE_PROJECT_ID`), `database`, `namespace`, `endpoint` (falls back to `DATASTORE_EMULATOR_HOST`), `credentials` (`"service_account"` | `metadata` | `oauth2` | `static`), `credentials_file` (falls back to `GOOGLE_APPLICATION_CREDENTIALS`), `timeout` (`"10s"`), `max_idle_conns` (`4`). `metadata`/`static` plus a `credentials_file` is an error.

### `[html]`

| Key | Default | Meaning |
| --- | --- | --- |
| `streaming` | `true` | `false` forces the buffered branch even where a chain could stream |
| `async_timeout` | `"3s"` | bound on one await boundary; zero leaves the request context |
| `async_concurrency` | `0` | simultaneous boundary work per render; zero or less is unbounded |
| `bot_detection` | `true` | render the settled document for crawlers and CLI clients |
| `bot_async_timeout` | `"5s"` | boundary bound on a classified bot request; zero falls back to `async_timeout` |
| `bot_user_agents` | `[]` | extra `User-Agent` substrings, appended to the built-in catalog |
| `scriptless_detection` | `true` | serve the settled document to a scripting-disabled browser, via a noscript redirect |
| `live` | `true` | answer the live connection that keeps a page updating |
| `live_max_duration` / `live_duration_jitter` | `"10m"` / `20` | lifetime of one live connection, spread per connection |
| `live_idle_timeout` | `"5m"` | close a connection nothing has delivered on |
| `live_max_boundaries` / `live_max_responses` | `32` / `4` | boundaries per connection; concurrent connections per client |
| `live_max_signal_bytes` | `262144` | signal payload one live response may write |
| `update.enabled` | `false` | answer navigation deltas, redraws, and action responses |
| `update.validator_key` | — | secret keying the boundary digests; **required** when updates are on |
| `update.max_manifest_bytes` | `8192` | cap on the digest hint a request may carry |
| `cache.enabled` | `true` | reuse the rendered output of `@cache` components |
| `cache.max_entries` | `1024` | entries the in-process render cache holds |

Every `live_` key depends on `streaming`. `cache.enabled` is on where everything else here is off, because the opt-in is the `@cache` annotation — a project writing none never reaches the store. Raise `cache.max_entries` once anything is cached at `scope: "private"`: a private key carries the reader's identity, so entries multiply by the number of active readers. See references/caching.md.

### `[cache]` and `[[cache.stores]]`

The data cache the memo store reads, sized separately from the render cache above because the two fill at different rates from different sources.

```toml
[cache]
enabled = false        # off by design: a memo store has no annotation to opt in with

[[cache.stores]]
name = "rates"         # the name a call site addresses this store by
backend = "memory"     # the only implemented backend
ttl = "1m"
stale = "0s"           # window a stale entry may answer in while one revalidation runs
scope = "private"      # or "public"
max_entries = 1024
fetch_timeout = "30s"  # bound on a fetch running detached from its waiters
```

With `enabled = false` every call site falls straight through to its own fetch, which is how caching is withdrawn from a deployment without editing code. `scope` defaults to `private` for the same reason `@cache`'s does.

### `[ratelimit]`

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | |
| `backend` | `"memory"` | `memory` or `redis` (blank-import `ratelimitstore/redis`) |
| `window` | `"1m"` | period every count is measured over; also what `X-RateLimit-Reset` reports |
| `per_subject` | `600` | requests one authenticated subject may make; `0` disables this bucket |
| `per_address` | `300` | requests one caller with no session may make; **must be positive** |
| `process` | `0` | total unkeyed arrivals per window; `0` leaves only the identity buckets |
| `redis.dsn` / `redis.key_prefix` / `redis.connect_timeout` | — / `"pw:ratelimit:"` / `"5s"` | read only under the redis backend |

There is no per-route form; one budget covers the whole application. A positive `process` must be at least `per_address` and `per_subject`. Redis keys under the `memory` backend are refused rather than ignored, and an unreachable Redis refuses startup — while an unreachable store *at request time* admits the request and logs at error level.

### `[security.cors]`

| Key | Default |
| --- | --- |
| `enabled` | `false` |
| `include` / `exclude` | `["/**"]` / `[]` |
| `allowed_origins` | `[]` — exact `scheme://host[:port]`, or the single `"*"` |
| `allow_credentials` | `false` |
| `allowed_methods` | `["GET", "HEAD", "POST"]` |
| `allowed_headers` | `["Content-Type", "Authorization"]` |
| `exposed_headers` | `X-Request-ID`, `Retry-After`, the three `X-RateLimit-*` |
| `max_age` | `"10m"` — browsers cap it themselves |

Four combinations fail startup: enabled with no origin, `allow_credentials` with `"*"` origins, `allow_credentials` with `"*"` in `allowed_headers`, and `allow_credentials` with `include = ["/**"]`. The generated OpenAPI document is readable cross-origin whether or not this section exists.

### `[observability]`

Top level: `minimum_level` (`"info"`; `trace`…`off`), `stdout_format` (`"json"` or `plaintext`), `service_name`, `resource_attributes`, `boot_log` (`"auto"` | `tree` | `record` | `off`). Subsections:

- `[observability.query]` — statement logging: `enabled = "auto"` (on in dev), `slow_threshold = "200ms"` (zero disables slow detection, `explain`, and `reproduction` together), `bind_values = "auto"`, length bounds.
- `[observability.trace]` — framework spans: `enabled = "auto"` (follows whether traces are exported), `render`, `boundary`, `database`, `statement`. Bind values never reach a span.
- `[observability.otel]` — export: `enabled = false`, `endpoint` (OTLP/HTTP base; `/v1/traces` and `/v1/logs` appended), `headers`, `request_timeout = "10s"`, queue and batch bounds. `OTEL_EXPORTER_OTLP_TIMEOUT` is deliberately not bound to `request_timeout` (units differ).

### `[session]`

`enabled = false`; `backend = "rdb"` (`rdb` | `cookie` | `redis` | `dynamo` | `firestore`); `retention = "720h"`. Cookie keys: `cookie.name = "pw_session"`, `cookie.secure = true` (`false` refuses to start outside dev), `cookie.http_only = true`, `cookie.same_site = "lax"`. Backend-specific keys (`rdb.source`/`group`/`dsn`/`table`, `redis.dsn`/`key_prefix`, `dynamo.table`, `firestore.kind`) are read only for the selected backend, and a non-cookie backend needs its own blank import — the startup error quotes the line to add. `keyring.secret` is the base64 secret signing/sealing browser-carried data; required unless every registered slot is `Shared`/`RequestScope`. `pw init` generates one into `config.dev.toml`; other environments read `SESSION_KEYRING_SECRET`. Session lifetimes (`ttl`, `idle_timeout`, `renewal_interval`) live under `[auth]`, not here.

### `[security]` (brief)

Response headers are on by default: `headers.enabled = true`, `headers.content_type_options = true` (nosniff), `headers.frame_options = "deny"`, `headers.referrer_policy = "strict-origin-when-cross-origin"`. `headers.content_security_policy`, its `_report_only` twin, and `headers.permissions_policy` are empty by default and TOML-only. `headers.hsts.enabled = false`; HSTS is applied only on verified HTTPS requests. CSRF is off until you name the paths it covers (`security.csrf.enabled = false` in the scaffold); its secret is a session slot sealed by `session.keyring.secret`, not a key here.

### `[auth]` (brief)

Exists only when `plugin/auth` is linked (registering an account resolver does that). `enabled = false`; `backend = "rdb"`; `mode = "oidc_only"` (plus `jwt_only` for bearer-token APIs); `login_path`/`callback_path`/`logout_path`; `session.ttl = "24h"`; `protection.include`/`exclude`/`unauthenticated`. `[auth.oidc]`: `issuer`, `client_id`, `client_secret` (all `AUTH_OIDC_*`), `identity_claim = "sub"`, `admission = "authenticated"`. An enabled OIDC mode with empty issuer/client keys fails at startup naming both keys and variables — dev scaffolds carry no provider values because `pw dev` injects the emulator's. `[auth.jwt]` configures `jwt_only`: `issuer`, `audience`, `algorithms`, `admission`, `identity_claim`, `max_token_lifetime`, and `revocation.mode` — which has **no default**, so startup refuses a missing value (`off`, `token`, `subject`, or `both`). Anything but `off` needs `middleware.rdb`, `auth.backend = "rdb"`, and the framework migration. `revocation.on_unavailable` defaults to `refuse` (a `503`, not a `401`); `revocation.max_propagation_delay` permits a per-process cache and its value is the honest answer to how fast a revocation takes effect.

## Configuration declarations (application code)

Declare a struct, register it under a prefix, read it from the request context. `pw generate` reads the registration call (the file must be in a `generate.config` directory) and writes the binding — no runtime reflection, TinyGo-safe.

```go
type AppConfig struct {
	EnvLabel      string `default:"local" help:"environment name shown in the page badge"`
	EnvLabelColor string `default:"#64748b" help:"CSS color of the environment badge"`
}

func RegisterConfig() { pw.RegisterConfig[AppConfig]("app") }
```

Call `RegisterConfig()` from `main` **after** every package `init` has run and **before** parsing begins (registering after `ParseConfig` panics). The prefix must be a string literal; prefixes share one namespace and may contain dots (`"middleware.cache"`). Read anywhere a request context is:

```go
app := pw.Config[AppConfig](r)
```

No error return: an unparsed prefix yields declared defaults, an unregistered type yields the zero value, `nil` is accepted outside a request.

With prefix `app`, field `Mailer.FromAddress` becomes stable key `app.mailer.from_address`, TOML `[app.mailer] from_address = …`, option `--app-mailer-from_address`, variable `APP_MAILER_FROM_ADDRESS`.

**Bindable field types**: `string`, `bool`, `int`, `time.Duration` (duration-string syntax in every source, including `default`), `[]string`, named nested structs of those, and `[]T` where `T` is a named struct in the same package (filled from an array of tables — file-only, no CLI/env per element). Floats, maps, pointers, and other slice element types are **not** bindable; declare `string`/`int` and convert after parsing.

**Tags**: `default:"value"`, `key:"name"`, `opt:"long"` or `opt:"long,s"` (also moves the env name, which derives from the long option), `env:"NAME"` or `env:"-"`, `help:"text"` (falls back to the field's godoc, written back into the tag on first generate). Presentation tags: `secret:"mask"|"hide"|"show"` (auto-masking already triggers on key paths containing `password`, `secret`, `token`, `dsn`, etc.), `dependon:".sibling"` (hides dependents in the startup summary while the parent is off — display only, binding is unaffected), `falsy:"value"` (teaches `dependon` what "off" means for strings, ints, durations; a numeric/duration `dependon` parent without `falsy` fails generation).

**Scaffolds** — the binary is the authoritative key list for its own build:

```sh
./myapp --generate-config toml > config.dev.toml
./myapp --generate-config env > .env
```

Both exit after writing. Parsing reads process environment variables, never a `.env` file — a dotenv loader or the shell must export it.

**Command-line forms** accepted by the binary:

```sh
./myapp --app-mailer-from_address noreply@example.com
./myapp --app-mailer-from_address=noreply@example.com
./myapp --app-tls-enabled              # a bool with no value is true
./myapp --app-tls-enabled=false
./myapp --app-origins a.example --app-origins b.example   # []string accumulates
```

An unknown option, a missing value, and an invalid boolean all fail the load.

**CLI-only subcommands**: `pw.RegisterSubCommand[MigrateOptions]("migrate", "run database migrations")` with `arg:"required"|"optional"|"*"` tags for positionals; read with `options, ok := pw.Command[MigrateOptions]()`. Subcommand structs read no TOML and no environment.

**Startup summary**: resolved configuration is reported once at startup (tree on a terminal, one structured record elsewhere), each entry with the source its value won from. `secret` controls disclosure (`dsn`-suffixed keys keep scheme/host/port and mask only user info), `dependon` filters disabled branches, `observability.boot_log` selects the format.

## Common mistakes

- Putting a runtime table (`server`, `session`, `middleware`, `observability`, a DSN) in `popcornweb.toml`, or a `[generate]`/`[dev.watch]` table in `config.*.toml`. The split is enforced; the loader errors.
- Expecting a bare `config.toml` in the project tree to be read — it never is. The project-local file is always `config.{APP_ENV}.toml`.
- Expecting file merging: only the first readable candidate is read. `config.prod.toml` must be a complete configuration, not a diff over `config.dev.toml`.
- Writing a bare number for a duration (`send_timeout = 5`) — rejected in every source, including `default` tags.
- `${NAME}` naming a variable the process does not have — the load fails (by design; check the deployment environment).
- Registering config after `ParseConfig`, or with a computed (non-literal) prefix, or from a file outside `generate.config` — panics, no binding, or nothing generated respectively.
- Declaring a `float64`, map, or pointer field — generation error; bind a supported type and convert.
- Relying on a TOML typo being caught: an unknown TOML key parses, matches no field, and is silently not applied (unlike a misspelled CLI option, which fails loudly).
- Setting a key and not finding it in the startup summary — check its `dependon` parent, not your spelling; the value was still bound.
- Trying to set a `[[middleware.rdb.connections]]` element via environment variable — array elements are file-only; use `${NAME}` expansion inside the DSN string instead.
