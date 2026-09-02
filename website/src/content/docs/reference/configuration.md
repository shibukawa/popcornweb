---
title: Application Configuration Keys
description: Every runtime configuration key of a running application, its default, and the TOML, environment, and command-line names it answers to.
sidebar:
  order: 1
---

These are the keys a *running application* reads: ports, pools, cookies,
severities. The file `pw` itself reads to build that application is a different
file with a different schema — see
[Build Tool Configuration](/reference/build-configuration/).

One struct field becomes three inputs. `ServerConfig.ReadHeaderTimeout` is
`server.read_header_timeout` in TOML, `SERVER_READ_HEADER_TIMEOUT` in the
environment, and `--server-read_header_timeout` on the command line — and the
last two are derived from the first by rule, not by a table someone maintains.
So the listings below name each key once, and you can compute the other two
forms yourself.

Rules have exceptions, and this one has a handful worth knowing before the
tables. For how configuration is *used* — declaring your own settings, reading
them in a handler, generating a scaffold — see
[Application Configuration](/guides/architecture/configuration/).

## Deriving the other two names

Take the TOML key, replace each `.` with `-`, and prefix `--`:
`observability.query.slow_threshold` becomes
`--observability-query-slow_threshold`. Underscores inside a key survive; only
the dots that separate nesting levels change.

Take that option name, drop the dashes, and upcase:
`OBSERVABILITY_QUERY_SLOW_THRESHOLD`.

Seven keys break the rule on purpose, because a conventional name already exists
and an application should not have to translate:

| Key | Environment variable |
| --- | --- |
| `server.port` | `PORT` (option is `--port`) |
| `observability.service_name` | `OTEL_SERVICE_NAME` |
| `observability.otel.endpoint` | `OTEL_EXPORTER_OTLP_ENDPOINT` |
| `observability.otel.headers` | `OTEL_EXPORTER_OTLP_HEADERS` |
| `observability.trace.sampler` | `OTEL_TRACES_SAMPLER` |
| `observability.trace.sampler_arg` | `OTEL_TRACES_SAMPLER_ARG` |
| `auth.oidc.issuer`, `client_id`, `client_secret`, `redirect_url` | `AUTH_OIDC_*`; deployed redirects are fixed, while loopback development may derive one from the request |

Three keys have no environment binding at all:
`security.headers.content_security_policy`, its `_report_only` twin, and
`security.headers.permissions_policy`. Set them in TOML.

The `[[middleware.rdb.connections]]` array is TOML-only as well. An array of
tables has no flat name to bind, which is why a deployment's DSN goes in as a
`${DATABASE_URL}` reference: the file layer expands it, and an undefined name is
a load error rather than an empty DSN.

## Where values come from

`APP_ENV` selects the environment and therefore the project-local filename.
Popcorn Web reads, in order:

1. `./config.{APP_ENV}.toml`
2. `./config/config.{APP_ENV}.toml`
3. the user and system configuration directories, where the file is the
   environment-neutral `config.toml`

A bare `config.toml` in the project tree is never read. Later sources override
earlier ones, and environment variables and flags override every file.

Durations are Go duration strings: `5s`, `200ms`, `2m`, `24h`. Sizes are plain
integers of bytes. A list is a TOML array, or a comma-separated value in an
environment variable.

## `[server]`

| Key | Default | Meaning |
| --- | --- | --- |
| `port` | `8080` | HTTP listen port |
| `read_header_timeout` | `"5s"` | request header read timeout |
| `read_timeout` | `"30s"` | request read timeout |
| `write_timeout` | `"0s"` | response write timeout; zero permits long-lived streams |
| `idle_timeout` | `"2m"` | keep-alive idle timeout |
| `shutdown_timeout` | `"10s"` | graceful shutdown timeout |
| `max_request_body` | `10485760` | maximum request body in bytes |
| `cbor_max_body` | `0` | maximum [CBOR request body](/guides/backend/cbor/) in bytes; `0` keeps the 1 MiB default |
| `trusted_proxies` | `[]` | trusted proxy IP or CIDR |
| `health` | *(empty)* | liveness endpoint path, e.g. `/healthz` |
| `readiness` | *(empty)* | readiness endpoint path, e.g. `/readyz` |
| `openapi` | *(empty)* | OpenAPI document path, e.g. `/openapi.json` |
| `api_doc` | *(empty)* | API documentation UI: `scalar`, `swagger`, or empty |
| `api_doc_path` | `"/docs"` | where that UI is mounted |
| `api_catalog` | `false` | answer `/.well-known/api-catalog` with an [RFC 9727 catalog](/appendix/web-standards/#api-discovery) |
| `api_catalog_origin` | *(empty)* | absolute origin the catalog's links are built from; empty writes them relative |
| `public.enabled` | `true` | serve the embedded static assets |
| `public.mount` | `"/public"` | where they are mounted |
| `public.read_local` | `false` | read from disk instead of the embedded tree |

The three operational endpoints carry no default path, and that is deliberate:
an application answering on `/healthz` should say so where an operator reading
its configuration will see it. A default would leave three endpoints running
that no file mentions.

An application route colliding with an enabled operational endpoint fails
startup, before either can shadow the other. `api_doc` additionally requires
`openapi` — a UI over a document nobody serves has nothing to render. See
[API Documentation](/productivity/api-documentation/).

`api_catalog` is a switch rather than a path because RFC 9727 fixes the
location, and it is the one framework address an operator cannot read off this
file. It requires `openapi` or `api_doc` for the reason `api_doc` requires
`openapi`: a catalog whose only links point at a probe and at itself describes
no API, and startup refuses it rather than serving it.

`api_catalog_origin` is optional. Unset, the catalog's links are relative and
resolve against the URL the client fetched it from, which is correct for anything
following the catalog. Name it once something *stores* your catalog instead —
RFC 9264 asks for references that are not relative so a stored link set still
resolves. The framework never infers the origin from the request's `Host`,
because a guess taken from a header the caller chose is durable and may name a
host you do not own. `pw doctor` reports the unset key outside development as
PW0429, a note.

## `[middleware]`

| Key | Default | Meaning |
| --- | --- | --- |
| `recovery` | `true` | recover a panicking handler into a 500 |
| `request_id` | `true` | assign and propagate a request correlation ID |
| `access_log` | `true` | one record per request |
| `compression` | `false` | encode rendered HTML and JSON for clients that accept it |
| `compression_codings` | `["zstd", "gzip"]` | codings to offer, best first; one left out is not offered at all |
| `request_timeout` | `"0s"` | per-request deadline; zero leaves none |
| `rdb.enabled` | `false` | open the framework-owned database pool |
| `rdb.default_group` | *(empty)* | connection group for statements that pin none |
| `rdb.write_group` | *(empty)* | connection group for framework-owned writes |
| `rdb.migration_group` | *(empty)* | connection group for migrations and seeds |

With `compression` enabled, `Vary: Accept-Encoding` is set either way — a cache
that saw one representation must not serve it to a client that asked for
another.

`compression_codings` is the order the server prefers, not the client's
`q`-values, which only say what can be read. An unknown name is a startup error;
a known one whose encoder a build tag removed is skipped, and named in the
startup log. Turn compression off with `compression = false` rather than an
empty list. The encoder levels are deliberately not configurable — see
[Response Compression](/guides/backend/compression/).

Every database is configured with the connection set below, one table per pool:
a single database is one table, and a reader-writer topology is several. The
section itself carries no DSN, so there is one place to look for one.

Earlier versions also took a `rdb.dsn` key with the pool keys beside it. That
form is gone. Move the DSN and its pool bounds into one
`[[middleware.rdb.connections]]` table; an enabled database with no table fails
at startup and names the replacement.

### `[[middleware.rdb.connections]]`

| Key | Default | Meaning |
| --- | --- | --- |
| `group` | *(empty)* | the name this connection is addressed by |
| `dsn` | *(empty)* | data source name; only its credential is masked where it is reported |
| `readonly` | `false` | open read-only transactions and serve no framework write |
| `connect_timeout` | `"5s"` | as above, per connection |
| `max_open_conns` | `0` | |
| `max_idle_conns` | `0` | |
| `conn_max_lifetime` | `"0s"` | |
| `conn_max_idle_time` | `"0s"` | |

A `readonly` connection can never be selected by `pw.SelectWriteDB`, which is
what lets a caller that must write stay ignorant of the topology. See
[Relational databases](/guides/storage/rdb/).

### `[middleware.firestore]`

These keys exist only when `database/firestore` is imported. The database must
use Datastore mode.

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | open the client and install it into request contexts |
| `project_id` | *(empty)* | Google Cloud project; falls back to `GOOGLE_CLOUD_PROJECT`, then `DATASTORE_PROJECT_ID` |
| `database` | *(empty)* | named database; empty selects the default database |
| `namespace` | *(empty)* | namespace applied to every key the process reads and writes |
| `endpoint` | *(empty)* | service or emulator endpoint; falls back to `DATASTORE_EMULATOR_HOST` |
| `credentials` | `"service_account"` | `service_account`, `metadata`, `oauth2`, or `static` |
| `credentials_file` | *(empty)* | service-account key; falls back to `GOOGLE_APPLICATION_CREDENTIALS` |
| `timeout` | `"10s"` | bound on one request |
| `max_idle_conns` | `4` | idle HTTP connections kept for the client |

`metadata` and `static` do not read `credentials_file`, so configuring both is
an error. A non-positive timeout or negative connection bound also stops
startup. See [Firestore](/guides/storage/firestore/).

## `[html]`

| Key | Default | Meaning |
| --- | --- | --- |
| `streaming` | `true` | `false` forces the buffered branch even when a chain could stream |
| `async_timeout` | `"3s"` | bound on one await boundary; zero leaves the request context as the only deadline |
| `async_concurrency` | `0` | simultaneous boundary work per render; zero or less is unbounded |
| `bot_detection` | `true` | render the settled document for crawlers and CLI clients |
| `bot_async_timeout` | `"5s"` | boundary bound on a classified bot request |
| `bot_user_agents` | `[]` | additional `User-Agent` substrings, matched case-insensitively |
| `scriptless_detection` | `true` | serve the settled document to a browser with scripting disabled, via a noscript redirect |
| `live` | `true` | answer the live connection that keeps a page updating after its document is complete |
| `live_max_duration` | `"10m"` | lifetime of one live connection before it closes and the client reconnects |
| `live_duration_jitter` | `20` | percentage that lifetime is spread by, per connection |
| `live_idle_timeout` | `"5m"` | close a live connection nothing has delivered on |
| `live_max_boundaries` | `32` | boundaries one live connection may serve; zero or less is unbounded |
| `live_max_responses` | `4` | concurrent live connections per client; zero or less is unbounded |
| `update.enabled` | `false` | answer navigation deltas, redraws, and action responses |
| `update.validator_key` | — | secret keying the boundary digests; required when `update.enabled` is true, and refused under 32 bytes of key material |
| `update.max_manifest_bytes` | `8192` | cap on the digest hint a request may carry |
| `cache.enabled` | `true` | reuse the rendered output of `@cache` components |
| `cache.max_entries` | `1024` | entries the in-process render cache holds; zero or less is unbounded |

A template that opens an await boundary renders correctly under either
`streaming` setting. The key decides only whether the fallbacks reach the
browser before the work behind them settles, which makes `streaming = false` the
escape hatch for a proxy that buffers responses anyway.

`bot_async_timeout` sits above `async_timeout` because a bot request waits for
every boundary before any byte leaves, and an indexer waits longer than a
browser. A zero here falls back to `async_timeout` rather than meaning
unbounded: a mistyped key must not hold a crawler's connection open for the
whole request deadline. Entries in `bot_user_agents` are appended to the
built-in catalog and never replace a built-in token.

Every `live_` key depends on `streaming`, because a buffered document settles
its live boundaries in place and writes no placeholder a delivery could replace.
`live = false` is a load dial rather than an outage: documents stay valid and
keep the content their live boundaries committed, and no client is invited to
connect. See [Live Rendering](/guides/cross-layer/live-rendering/) for what each bound
buys.

`update.validator_key` is refused at startup when it is missing and updates are
on, rather than serving unkeyed digests: an unkeyed digest of low-entropy content
lets a guess be confirmed by comparing digests. A key carrying fewer than 32
bytes of material is refused the same way, because a guessable key is an unkeyed
digest with extra steps — a base64 value is measured decoded, anything else as
the bytes it is, the same floor the session keyring enforces. Rotating it is not a break —
comparisons miss and the next response is a complete document. An oversized
manifest is dropped rather than rejected, so a request past
`update.max_manifest_bytes` costs a larger delta instead of an error. See
[Partial Updates](/guides/cross-layer/partial-updates/) for what each path buys.

`cache.enabled` is on where every other capability here is off, because the
opt-in is the [`@cache`](/reference/template-syntax/#cache) annotation rather
than this key: generation refuses one on a component whose stored bytes could
not stand in for a fresh render, so a template carrying it has already been
checked and has already asked. A project writing no annotation never reaches the
store and pays nothing. Turn it off to rule the cache out while diagnosing a
stale region. `cache.max_entries` bounds what one process holds — the key covers
every declared parameter, so a component taking an arbitrary string has as many
entries as it has callers.

Raise that bound once anything is cached at `scope: "private"`. A private key
carries the reader's identity as well as the parameters, so the entry count
multiplies by the number of active readers, and a cap chosen when every key was
shared will evict entries faster than they are reused.

## `[cache]`

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | reuse what a fetch returned for equal keys |

### `[[cache.stores]]`

| Key | Default | Meaning |
| --- | --- | --- |
| `name` | *(empty)* | the name a call site addresses this store by |
| `backend` | `"memory"` | where entries live; memory is the only implemented backend |
| `ttl` | `"1m"` | how long an entry is fresh |
| `stale` | `"0s"` | how long a stale entry may still answer while one revalidation runs; zero disables the window |
| `scope` | `"private"` | `private` keys entries per reader; `public` shares one entry between all of them |
| `max_entries` | `1024` | entries this store holds; zero or less is unbounded |
| `fetch_timeout` | `"30s"` | bound on a fetch running detached from its waiters |

This store holds what a handler *fetched*, which is a different question from
the `html.cache` above: that one holds rendered bytes and this one holds the
data those bytes were rendered from. They are sized separately because they fill
at different rates from different sources, and one cap covering both would let
whichever is busier evict the other.

`enabled` is off where `html.cache.enabled` is on, because the opt-in is not
symmetrical. A component asks to be cached in its own annotation, so the render
store can be on and idle. A data cache has no annotation to read: a store's
`Get` is an ordinary call in a handler, and turning the section on is the only statement
that any of them should store anything. With it off, every call runs its fetch
and returns, which is also how caching is withdrawn from a deployment without
editing code.

`scope` defaults to `private` for the reason the render cache's does. A
per-reader result declared `public` is served to whoever asks next, and nothing
reports it; a shared result left `private` costs hits. See
[Caching Fetched Data](/guides/backend/data-cache/).

`fetch_timeout` exists because concurrent misses are collapsed onto one fetch
that runs detached from every waiter, so no caller's cancellation can end it.
Without a bound of its own, one unresponsive upstream would hold a goroutine per
cold key for as long as it stayed unresponsive.

## `[security]`

| Key | Default | Meaning |
| --- | --- | --- |
| `headers.enabled` | `true` | the switch every key below answers to |
| `headers.content_type_options` | `true` | `X-Content-Type-Options: nosniff` |
| `headers.frame_options` | `"deny"` | `X-Frame-Options` |
| `headers.referrer_policy` | `"strict-origin-when-cross-origin"` | `Referrer-Policy` |
| `headers.content_security_policy` | *(empty)* | `Content-Security-Policy` (no environment binding) |
| `headers.content_security_policy_report_only` | *(empty)* | the report-only variant (no environment binding) |
| `headers.permissions_policy` | *(empty)* | `Permissions-Policy` (no environment binding) |
| `headers.hsts.enabled` | `false` | `Strict-Transport-Security`, on verified HTTPS requests only |
| `headers.hsts.max_age` | `"0s"` | |
| `headers.hsts.include_subdomains` | `false` | |
| `headers.hsts.preload` | `false` | |

HSTS is applied only on a verified HTTPS request. Sending it over plaintext
would ask a browser to remember a policy the connection could not vouch for.

### `[security.cors]`

| Key | Default | Meaning |
| --- | --- | --- |
| `cors.enabled` | `false` | the switch every key below answers to |
| `cors.include` | `["/**"]` | paths the policy covers, in the segment grammar |
| `cors.exclude` | `[]` | paths it does not, taking precedence over `include` |
| `cors.allowed_origins` | `[]` | exact `scheme://host[:port]` values, or the single `"*"` |
| `cors.allow_credentials` | `false` | whether a listed origin may read a response sent with cookies |
| `cors.allowed_methods` | `["GET", "HEAD", "POST"]` | methods a preflight admits |
| `cors.allowed_headers` | `["Content-Type", "Authorization"]` | request headers a preflight admits |
| `cors.exposed_headers` | `["X-Request-ID", "Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"]` | response headers script may read |
| `cors.max_age` | `"10m"` | how long a browser may cache one preflight |

Four combinations fail startup rather than serving: an enabled policy with no
origin, `allow_credentials` with `"*"`, `allow_credentials` with `"*"` in
`allowed_headers`, and `allow_credentials` with an `include` of `"/**"`. The
first three are configurations a browser drops the response for; the last is a
grant wider than any deployment means. See [Cross-Origin
Requests](/guides/backend/cors/).

Browsers cap `max_age` themselves — Safari at ten minutes, Chrome at two hours —
so a larger value is reduced rather than honoured. The generated OpenAPI
document is readable cross-origin whether or not this section exists.

## `[observability]`

| Key | Default | Meaning |
| --- | --- | --- |
| `minimum_level` | `"info"` | severity floor: `trace`, `debug`, `info`, `warn`, `error`, `off` |
| `stdout_format` | `"json"` | terminal record encoding: `json` or `plaintext` |
| `service_name` | *(empty)* | also read from `OTEL_SERVICE_NAME` |
| `resource_attributes` | `[]` | extra `key=value` identifiers reported with the service name |
| `boot_log` | `"auto"` | startup summary: `auto`, `tree`, `record`, `off` |

`auto` renders the tree on an interactive terminal and one structured record
everywhere else. See [Configuration Summary](/productivity/startup-summary/).

### `[observability.query]`

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `"auto"` | log every generated statement: `auto`, `on`, `off` — `auto` is on in `dev` |
| `level` | `"info"` | severity of an ordinary statement record |
| `slow_threshold` | `"200ms"` | duration above which a statement is slow; zero disables slow detection |
| `slow_level` | `"warn"` | severity of a slow statement record |
| `bind_values` | `"auto"` | log argument values: `auto`, `on`, `off` |
| `explain` | `true` | capture a plan-only `EXPLAIN` for a slow statement |
| `reproduction` | `true` | emit a paste-able rerun snippet for a slow statement |
| `max_sql_length` | `4096` | bound on the logged statement text |
| `max_value_length` | `256` | bound on each logged argument value |

`auto` ties the setting to the environment, so a development run is instrumented
without configuration and every other environment stays silent until someone
opts in. `explain` and `reproduction` depend on `slow_threshold`, not on
`enabled`: setting the threshold to zero switches off all three at once. See
[Slow Query Diagnostics](/productivity/query-diagnostics/).

### `[observability.trace]`

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `"auto"` | open framework spans: `auto`, `on`, `off` — `auto` follows whether traces are exported |
| `render` | `true` | a span per HTML response, with the initial build inside it |
| `boundary` | `true` | a span per settled async boundary and per live delivery |
| `database` | `true` | a client span per executed statement |
| `statement` | `true` | the statement text on that span |
| `sampler` | *(by environment)* | which traces are recorded: `always_on`, `always_off`, `traceidratio`, or a `parentbased_` form |
| `sampler_arg` | *(by environment)* | the sampler argument; for either ratio form, the fraction of traces kept |

`auto` reads the export switch rather than the environment, because a span
nothing exports is pure cost. `on` also installs the request root span, so a
project holding its own tracer provider gets a complete tree without configuring
an endpoint here.

`boundary` depends on `render` and `statement` depends on `database`, so
switching a parent off switches its child off with it. `statement` is bounded by
`observability.query.max_sql_length`, which bounds the same text on the query
record.

Bind values never reach a span, whatever `statement` says. They stay on the
query record, which names the statement span rather than the request root. See
[Request Tracing](/guides/architecture/telemetry/#reading-a-request-trace).

`sampler` is the only key here with no fixed default. It resolves to
`parentbased_always_on` when `APP_ENV` is `dev` and to `parentbased_traceidratio`
at `0.1` in every other environment, including one whose name this framework
does not know. Setting the key replaces both, and the startup summary reports
which applied. An argument that cannot be parsed stops the process rather than
falling back to recording everything — see
[Sampling](/guides/architecture/telemetry/#sampling-which-traces-are-kept).

### `[observability.metrics]`

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `"auto"` | record framework metrics: `auto`, `on`, `off` — `auto` follows whether metrics are exported |
| `interval` | `"60s"` | how often a collection is exported |
| `temporality` | `"delta"` | `delta` reports each interval; `cumulative` reports the total since process start |
| `http` | `true` | `http.server` request duration, concurrency, and body sizes |
| `db` | `true` | `db.client` operation duration and connection pool state |
| `runtime` | `true` | the `go.*` memory, goroutine, and GC instruments |
| `render` | `true` | `pw.render` duration and bytes, boundary settle, and live delivery |
| `cache` | `true` | component output and data result cache hits and misses |

`enabled` reads the export switch the way `trace.enabled` does, and it is
independent of it: metrics on with tracing off is the configuration a heavily
sampled deployment ends up in, and a sampling ratio changes no count here.

`interval` is the resolution of every chart of these instruments, and it is a
different number from `otel.flush_interval`, which is how often a batch of spans
leaves. Turn off `runtime` when the platform already collects those values from
its own agent; the rest of the groups have no second source. See
[Metrics](/guides/architecture/telemetry/#metrics).

### `[observability.otel]`

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | export traces and logs |
| `endpoint` | *(empty)* | OTLP/HTTP base URL; `/v1/traces`, `/v1/logs`, and `/v1/metrics` are appended |
| `headers` | *(empty)* | comma-separated `key=value` list; values are never logged |
| `request_timeout` | `"10s"` | bound on one export request |
| `queue_size` | `2048` | records held in memory; a full queue drops rather than blocking the request |
| `max_export_size` | `512` | bound on one exported batch |
| `flush_interval` | `"5s"` | how often a partial batch is sent |

These defaults restate the bounds the exporter applies to a zero value, so a
scaffolded file says what the process will do instead of showing a zero that
means "ask someone else".

`OTEL_EXPORTER_OTLP_TIMEOUT` is deliberately **not** bound to `request_timeout`.
The standard variable counts milliseconds, while every duration here is a Go
duration string, and one key cannot mean both.

## `[session]`

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | |
| `backend` | `"rdb"` | server backend a server-placed slot uses: `rdb`, `cookie`, `redis`, `dynamo`, or `firestore` |
| `retention` | `"720h"` | how long the store may hold one record; the `[auth]` lifetime narrows it |
| `cookie.name` | `"pw_session"` | |
| `cookie.path` | `"/"` | |
| `cookie.domain` | *(empty)* | |
| `cookie.secure` | `true` | `false` only for loopback development; outside `dev` the process refuses to start with it |
| `cookie.http_only` | `true` | |
| `cookie.same_site` | `"lax"` | |
| `rdb.source` | `"middleware"` | `middleware` reuses the `middleware.rdb` pool; `dedicated` opens `session.rdb.dsn` |
| `rdb.group` | *(empty)* | connection group holding the session table; empty resolves to `middleware.rdb.write_group` |
| `rdb.dsn` | *(empty)* | dedicated session database; only its credential is masked where it is reported |
| `rdb.table` | `"popcornweb_session"` | |
| `redis.dsn` | *(empty)* | `redis://` or `rediss://` server; only its credential is masked where it is reported |
| `redis.key_prefix` | `"pw:session:"` | key space the session store owns |
| `redis.connect_timeout` | `"5s"` | startup ping and per-command deadline |
| `cookie_store.name` | `"pw_session_data"` | cookie holding the sealed record |
| `keyring.secret` | *(empty)* | base64 secret signing and sealing anything the browser carries (masked) |
| `keyring.previous_secrets` | `[]` | retired secrets kept readable during a rotation (masked) |
| `dynamo.table` | `"popcornweb_session"` | declared session table, mapped onto the deployed one by `middleware.dynamo` |
| `dynamo.consistent_read` | `false` | read sessions with strong consistency, at twice the read capacity |
| `firestore.kind` | `"popcornweb_session"` | entity kind holding session records |

Only the keys of the selected backend are read, and a backend other than
`cookie` reaches the binary through its own blank import — the startup error
quotes the line to add. [Session storage](/guides/storage/session-storage/) compares the
five and lists what each one requires.

This section declares no duration. An expiry states how long a proof of identity
stays good, so `session.ttl`, `session.idle_timeout`, and
`session.renewal_interval` are declared under `[auth]` instead.

The token in the browser is opaque in every backend, so nothing here signs it.
The CSRF secret is not a key here either: it is a registered session slot, so
one keyring seals it too and `security.csrf` carries no secret of its own.

`keyring.secret` protects what travels beside it: a `session.ReadOnly` slot is
signed and a `session.Private` slot is sealed, both from that one secret. It is
therefore required unless every registered slot is `session.Shared` or
`session.RequestScope`, whatever
`backend` names — a private slot rides a sealed cookie while a visitor is still
anonymous. `pw init` generates one into `config.dev.toml`; every other
environment reads `SESSION_KEYRING_SECRET`.

## `[ratelimit]`

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | |
| `backend` | `"memory"` | counter storage: `memory` or `redis` |
| `window` | `"1m"` | period every count below is measured over; also what `X-RateLimit-Reset` reports |
| `per_subject` | `600` | requests one authenticated subject may make in a window; `0` disables this bucket |
| `per_address` | `300` | requests one caller with no session may make in a window; must be positive |
| `process` | `0` | total arrivals allowed in a window, unkeyed; `0` leaves only the identity buckets |
| `redis.dsn` | *(empty)* | `redis://` or `rediss://` counter server; only its credential is masked where it is reported |
| `redis.key_prefix` | `"pw:ratelimit:"` | key space this limiter owns |
| `redis.connect_timeout` | `"5s"` | startup ping and per-command deadline |

The `redis.*` keys are read only under `backend = "redis"`, which reaches the
binary through a blank import of `ratelimitstore/redis` — the startup error
quotes the line to add. Setting `redis.dsn` under the `memory` backend is
refused rather than ignored, and `backend = "redis"` without a DSN is refused
too. A positive `process` must be at least `per_address` and `per_subject`,
since one caller allowed more than the total describes a limit that can never
bind.

The counts have no per-route form; one budget covers the whole application,
and the framework's operational endpoints and the public asset mount are
exempt without a key to change it. [Rate
Limiting](/guides/backend/rate-limiting/) explains how the three counts
compose and what happens when the store is unreachable.

## `[auth]`

These keys exist only when `plugin/auth` is linked into the binary, which
happens when the application registers an account resolver. An application that
imports nothing authentication-related has no `[auth]` prefix to configure.

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | |
| `backend` | `"rdb"` | storage for ceremony, allowlist, credential, and bootstrap records: `rdb`, `dynamo`, or `firestore` |
| `mode` | `"oidc_only"` | browser modes plus `jwt_only` for a bearer-token API |
| `login_path` | `"/auth/login"` | starts the provider flow |
| `callback_path` | `"/auth/callback"` | verifies the result and starts the session |
| `logout_path` | `"/auth/logout"` | ends the session; `POST` only |
| `post_login_path` | `"/"` | local path a completed login lands on |
| `session.ttl` | `"24h"` | absolute session lifetime |
| `session.idle_timeout` | `"0s"` | inactivity expiry; zero disables it |
| `session.renewal_interval` | `"0s"` | minimum interval between idle expiry renewals |
| `recent_auth_max_age` | `"5m"` | how recently a request must have authenticated to change a login method |
| `protection.include` | `[]` | path patterns that require authentication |
| `protection.exclude` | `[]` | path patterns that stay public |
| `protection.unauthenticated` | `"redirect"` | `redirect` or `unauthorized` |

### `[auth.oidc]`

| Key | Default | Meaning |
| --- | --- | --- |
| `issuer` | *(empty)* | `AUTH_OIDC_ISSUER` |
| `client_id` | *(empty)* | `AUTH_OIDC_CLIENT_ID` |
| `client_secret` | *(empty)* | `AUTH_OIDC_CLIENT_SECRET` (masked in the startup summary) |
| `redirect_url` | *(empty)* | `AUTH_OIDC_REDIRECT_URL`; empty or a rooted path is derived from the request origin only with `allow_loopback_http` and a loopback `Host`; deployments require an absolute URL |
| `scopes` | `[]` | |
| `identity_claim` | `"sub"` | the verified claim that identifies a local account |
| `admission` | `"authenticated"` | `authenticated`, `claim`, `registered`, or `existing` |
| `auto_provision` | `true` | let an unknown verified identity create an account through the resolver |
| `claim.path` | *(empty)* | JSON Pointer into verified claims, for `admission = "claim"` |
| `claim.values` | `[]` | accepted values |
| `claim.match` | `"any"` | `any` or `all` |
| `registered_claims` | `[]` | claims compared against the allowlist; defaults to `identity_claim` |
| `provider_logout` | `true` | also end the provider session on logout |
| `allow_loopback_http` | `false` | permit an `http` loopback issuer and a request-derived loopback redirect during development |

`identity_claim` becomes the account link, so whatever it names must be stable
for the life of the account and unique within the issuer. A reissued or reused
value hands one person another person's account. A deployment that provisions
users in advance usually cannot know a subject yet and points this at its own
directory identifier, such as an employee number.

An enabled OIDC mode with an empty `issuer`, `client_id`, or `client_secret`
fails at startup rather than at the first login, and the error names both the
missing keys and their environment variables. That is why a project scaffolded
for the local emulator carries no provider values at all —
[`pw dev`](/pw/project/dev/) injects them. See
[Authentication](/guides/backend/authentication/).

### `[auth.jwt]`

These keys are read by `auth.mode = "jwt_only"`. The mode verifies a bearer
access token on every request and creates no browser session or authentication
endpoint.

| Key | Default | Meaning |
| --- | --- | --- |
| `issuer` | *(empty)* | **required** exact `iss`; also `AUTH_JWT_ISSUER` |
| `audience` | `[]` | **required** `aud` values naming this API |
| `audience_match` | `"any"` | require `any` or `all` configured audiences |
| `algorithms` | `[]` | **required** RSA verification allowlist, such as `["RS256"]` |
| `required_token_type` | `"at+jwt"` | required `typ`; empty explicitly permits an absent type |
| `required_scopes` | `[]` | scope values every token must carry |
| `discovery` | `"oidc"` | `oidc`, `oauth`, or `manual` key discovery |
| `jwks_uri` | *(empty)* | **required** for `manual`; must share the issuer origin |
| `leeway` | `"30s"` | clock-skew allowance; at most 5 minutes |
| `max_token_lifetime` | *(empty)* | **required** upper bound for `exp - iat`; at most 24 hours |
| `max_token_bytes` | `8192` | compact-token size limit; at most 64 KiB |
| `jwks_refresh_cooldown` | `"1m"` | minimum delay between unknown-`kid` key refreshes |
| `allow_loopback_http` | `false` | permit an HTTP loopback issuer during development |
| `identity_claim` | `"sub"` | verified claim that identifies the local account |
| `admission` | *(empty)* | **required**: `authenticated`, `claim`, `registered`, or `existing` |
| `auto_provision` | `false` | let an unknown verified identity create an account |
| `claim.path` | *(empty)* | JSON Pointer into verified claims for `admission = "claim"` |
| `claim.values` | `[]` | accepted values at that pointer |
| `claim.match` | `"any"` | `any` or `all` |
| `registered_claims` | `[]` | claims compared with the allowlist; defaults to `identity_claim` |
| `revocation.mode` | *(empty)* | **required**: `off`, `token`, `subject`, or `both` |
| `revocation.on_unavailable` | `"refuse"` | `refuse` or `admit` when the revocation store cannot answer |
| `revocation.max_propagation_delay` | `"0s"` | revocation-result cache duration; zero disables the cache |
| `dev.trust_unverified_tokens` | `false` | `pw dev` and loopback only; forbidden in staging and production |

`registered` admission and every revocation mode except `off` require the
relational auth tables and `middleware.rdb`. The other admission rules can stay
stateless. `protection.unauthenticated` must be `unauthorized`, and
`security.csrf.enabled` must be false because this mode has no session secret.
See [JWT-only API servers](/guides/backend/authentication/#jwt-only-api-servers).

## A key you set that the startup summary does not show

Many keys above answer to a parent switch. `server.api_doc_path` depends on
`server.api_doc`; every `[auth.oidc]` key depends on `auth.enabled`;
`observability.otel.endpoint` depends on `otel.enabled`.

When the parent is empty or false, the summary omits the dependents and keeps
the parent — an empty parent is the reason its dependents vanished, and printing
seven keys that decide nothing would bury the one that does. Binding is
unaffected: the value was read, it simply has nothing to act on yet. So a key
you set and cannot find in the summary is a question about its parent, not about
your spelling.

## The list your binary actually has

The tables here cover the framework and the authentication plugin. Your build
also carries whatever your own packages registered, and only the binary knows
the union:

```sh
./myapp --generate-config toml > config.dev.toml
./myapp --generate-config env > .env
```

Because the scaffold is assembled from registrations present in that build, it
is the authoritative list for that binary — including your `[app]` prefix and
excluding any framework capability you never imported. Adding a struct and
rerunning the command is how the new keys appear; nothing here needs editing.
See [Custom Commands](/guides/architecture/custom-commands/).
