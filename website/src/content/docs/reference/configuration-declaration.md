---
title: Application Configuration Declaration
description: How a configuration struct is written — every tag, the field types that bind, the three names one field answers to, and how the sources resolve.
sidebar:
  order: 6
---

A configuration struct is declared once and reached four ways: a `default` tag,
a TOML file, an environment variable, and a command-line option. `pw generate`
reads the registration call and writes the binding code, so nothing reflects at
run time — which is what keeps configuration available under TinyGo, and what
makes several rules below stricter than a reflection-based binder would need.

This page is the declaration surface. The framework's own keys and their
defaults are in
[Application Configuration Keys](/reference/configuration/); the narrative version, with a
worked example, is
[Application Configuration](/guides/architecture/configuration/).

## Registering a struct

```go
type AppConfig struct {
	EnvLabel string `default:"local" help:"environment name shown in the page badge"`
}

func RegisterConfig() { pw.RegisterConfig[AppConfig]("app") }
```

| Rule | Detail |
| --- | --- |
| The prefix is a string literal | generation reads it statically; a computed value produces no binding |
| The call sits in a directory `generate.config` lists | otherwise nothing is generated for it |
| Registration happens after every `init` and before parsing | generated definitions register during `init`; registering after `ParseConfig` panics |
| Prefixes share one namespace | give each area its own — `app`, `billing`, `search` |
| A prefix may contain dots | `pw.RegisterConfig[CacheConfig]("middleware.cache")` |

Reading it back takes no error handling:

```go
app := pw.Config[AppConfig](r)
```

`pw.Config` accepts `nil` outside a request. A registered prefix that was never
parsed yields its declared defaults and an unregistered type yields the zero
value, because a handler reading configuration is already on the response path,
where a nil check would only postpone the same missing value to a later line.

## Field types

| Supported | |
| --- | --- |
| `string` | |
| `bool` | |
| `int` | |
| `time.Duration` | Go duration syntax in every source |
| `[]string` | a TOML array, a repeated CLI option, or a comma-separated environment value |
| a named nested struct of those | becomes a nested TOML table |
| `[]T` where `T` is a named struct in the same package | filled from an array of tables |

Floats, maps, pointers, and other slice element types cannot be bound. Receive
them in a supported representation and convert after parsing — a `float64`
declared directly is a generation error reading `unsupported basic type
float64`.

### Durations

```go
type MailerConfig struct {
	SendTimeout time.Duration `default:"5s" help:"outbound send timeout"`
}
```

```toml
[app.mailer]
send_timeout = "1h30m"
```

A bare number is rejected in every source, because `5` cannot say whether it
means seconds or nanoseconds. That applies to the `default` tag as well, where
an unparsable value fails `pw generate` rather than startup. Scaffolds emit
durations as quoted strings, and a field with no `default` starts at `"0s"`.

Only `time.Duration` itself is treated this way. A named type of your own whose
underlying type is `time.Duration` binds as an integer.

### Repeated settings

```go
type AppConfig struct {
	Routes []RouteConfig `help:"static routes"`
}

type RouteConfig struct {
	Path    string
	Dir     string
	Listing bool `default:"false"`
}
```

```toml
[[app.routes]]
path = "/"
dir = "./public"

[[app.routes]]
path = "/files"
dir = "./files"
listing = true
```

Element count is data, so an element has **no CLI option and no environment
variable**: the file is its only source. `default` still applies, once per
element. The rules:

- The element struct is a named struct in the same package, held by value.
  `[]*RouteConfig` and a struct that reaches itself are both rejected.
- `opt` or `env` on an element field is a generation error rather than a tag
  that quietly does nothing, and so are `falsy`, `dependon`, and `secret` —
  each of those needs a stable config key, and an element's key belongs to one
  element rather than to the configuration.
- A subcommand cannot take a slice of structs at all.
- To get a credential or a machine-specific path into an element, write a
  [`${NAME}` reference](#referencing-the-environment-from-a-file) in its value.
- The scaffold renders one example `[[…]]` block per slice.

## Tags

Five tags name a key:

| Tag | Effect |
| --- | --- |
| `default:"value"` | the value when no source supplies one |
| `key:"name"` | override the stable TOML and config key |
| `opt:"long"` / `opt:"long,s"` | override the CLI long option, optionally with a one-character short form |
| `env:"NAME"` / `env:"-"` | an exact environment variable name, or no environment input at all |
| `help:"text"` | the description shown in usage and scaffolds |

Three describe a key rather than naming it:

| Tag | Effect |
| --- | --- |
| `secret:"mask"` / `"hide"` / `"show"` | how the value appears in the startup summary |
| `dependon:"key"` / `dependon:".sibling"` | the key this one answers to; a leading dot is relative to the enclosing struct |
| `falsy:"value"` | the value that counts as "not set" for anything depending on this key |

`dependon` and `secret` may sit on a nested struct field, where they cover the
whole subtree. `falsy` may not: it names one value, and a struct has none.

### Godoc as the help source

A field with no `help` tag takes its description from its doc comment, and the
generator writes that text back into the struct tag:

```go
type MailerConfig struct {
	// FromAddress is the envelope sender.
	FromAddress string `default:"noreply@example.com"`
}
```

After one run the source reads
`` `default:"noreply@example.com" help:"FromAddress is the envelope sender"` ``.
The tag is the single source of truth from then on: an existing `help` always
wins, and re-running changes nothing. Only the first paragraph is used, `//go:`
and lint directives are dropped, and one trailing period is removed. A trailing
line comment works too. The same text feeds generated CLI usage, and a
subcommand registered with an empty help string falls back to its struct godoc.

## The three names one field answers to

Field names become snake case and nest under the registered prefix. With prefix
`app`:

```go
type AppConfig struct {
	Mailer MailerConfig
}

type MailerConfig struct {
	FromAddress string
}
```

| Surface | Name |
| --- | --- |
| Stable config key | `app.mailer.from_address` |
| TOML | `[app.mailer]` with `from_address = …` |
| CLI | `--app-mailer-from_address` |
| Environment | `APP_MAILER_FROM_ADDRESS` |

Take the stable key, replace each `.` with `-`, and prefix `--`: that is the
option. Take the option, drop the dashes, and upcase: that is the variable.
Underscores inside a single key survive; only the dots that separate nesting
levels change.

`opt` moves both, because the environment name derives from the long option
rather than from the key:

```go
Port int `key:"listen_port" default:"8080" opt:"port,p" help:"HTTP listen port"`
```

| Surface | Name |
| --- | --- |
| Stable key | `app.listen_port` |
| TOML | `[app] listen_port = 8080` |
| CLI | `--port 8080` or `-p 8080` |
| Environment | `PORT=8080` |

With `opt` present the derived `--app-listen_port` is not registered. `env`
moves only the environment name and is used exactly as written; it must begin
with a letter or `_`, and assigning one name to two fields is a generation
error. That is how `observability.service_name` answers to `OTEL_SERVICE_NAME`
while keeping its own TOML key and option.

## Where values come from

```
default  <  TOML file  <  environment variable  <  command-line option
```

Precedence is fixed and not configurable. An absent key in one layer does not
clear the value a lower layer supplied; a present key always overrides.

`APP_ENV` selects the environment and therefore the project-local filename. It
accepts `dev`, `stg`, `prod`, or any other token of lowercase letters, digits,
`-`, and `_`; an invalid token fails `ParseConfig`, and unset or empty means
`dev`. Popcorn Web reads the first readable file of:

1. `./config.{APP_ENV}.toml`
2. `./config/config.{APP_ENV}.toml`
3. the user configuration directory, where the file is the environment-neutral
   `config.toml`
4. the system configuration directory, likewise

A bare `config.toml` in the project tree is never read. The asymmetry is
deliberate — a file that applies to every environment is reasonable on one
operator's machine and misleading in a repository, where "which environment is
this for?" would have no answer.

**Files are never merged.** The first readable candidate is the only one read,
which lets a local file replace a system one rather than combine with it.
`--config-path` replaces the search entirely and does not fall back; a missing,
unreadable, or directory path there fails the load.

```sh
./myapp --config-path ./deploy/staging.toml
```

`pw.SetConfigLoadOptions` adjusts the search before `ParseConfig` for a binary
that needs configuration before it decides to serve — a CLI subcommand, a
migration runner, a one-shot job. Calling it after parsing panics.

### The TOML subset

Configuration reads a restricted subset rather than the full TOML
specification:

| Accepted | Not accepted |
| --- | --- |
| tables, nested tables, bare dotted keys | quoted keys |
| string, bool, integer, and float scalars | inline tables |
| arrays of primitive scalars | nested arrays |
| arrays of tables | |
| comments | |

There are two limits here rather than one — what the parser accepts and what a
field can receive — and the second is narrower. A TOML float parses and still
cannot bind to a field.

Every key after a `[[…]]` header belongs to that element, so the enclosing
table's own keys have to come before the first element.

TOML is the asymmetric case for typos: an unknown key parses, matches no field,
and is silently not applied, while a misspelled CLI option fails loudly.

### Referencing the environment from a file

`${NAME}` inside a TOML **string** expands from the environment at load. The
reference need not span the whole value:

```toml
[[middleware.rdb.connections]]
group = "primary"
dsn = "postgres://app:${PRIMARY_DB_PASSWORD}@db1.internal:5432/app"
```

This exists mainly to get credentials into the elements of an array of tables,
which have no option and no variable of their own.

- Only strings expand. Keys, table headers, numbers, and booleans do not. Array
  elements and the fields of `[[…]]` elements do.
- An undefined name **fails the load**. The file layer outranks defaults, so
  expanding to an empty string would quietly erase a `default`; failing at
  startup is easier to notice. A variable set to the empty string counts as
  defined and expands to `""`.
- `$$` yields one literal `$`. A `$` followed by neither `{` nor `$` stays
  literal.
- An expanded value still belongs to the file layer, so environment and CLI
  overrides keep their usual precedence.
- A `${…}` written in an environment or CLI value stays literal.
- A reference names a raw environment variable; per-field `env` names and
  `env:"-"` do not affect it.
- There is no `${NAME:-default}` fallback form.

### Command-line forms

```sh
./myapp --app-mailer-from_address noreply@example.com
./myapp --app-mailer-from_address=noreply@example.com
./myapp --app-tls-enabled              # a bool with no value is true
./myapp --app-tls-enabled=false
./myapp --app-origins a.example --app-origins b.example   # []string accumulates
```

An unknown option, a missing value, and an invalid boolean all fail the load.

## Scaffolds

Every registered prefix can print itself, with `default` values filled in and
`help` text as comments:

```sh
./myapp --generate-config toml > config.dev.toml
./myapp --generate-config env > .env.example
```

Because the scaffold is assembled from the registrations present in that build,
it is the authoritative key list for that binary — including your own prefixes
and excluding any framework capability you never imported. Within a `[prefix]`
table the keys follow the struct's declaration order; the tables themselves are
ordered by prefix and type name, so output never depends on package
initialisation order. The `.env` scaffold is sorted by variable name instead,
having no table grouping to hang declaration order on, and it respects `opt`,
`env:"NAME"`, and `env:"-"`.

The struct's godoc becomes the TOML table comment. Either form exits after
writing — the server does not start.

Parsing reads the process environment laid over the working directory's `.env`,
`.env.local`, `.env.{APP_ENV}`, and `.env.{APP_ENV}.local`, so a copy of the
scaffold named `.env.dev.local` is read at the next start. Direct the scaffold
at `.env.example`, the committed template: every default it carries would
otherwise count as an environment value. See
[Secrets and dotenv files](/guides/architecture/configuration/#secrets-and-dotenv-files).

## CLI-only subcommands

```go
type MigrateOptions struct {
	Path   string   `arg:"required" help:"migration directory"`
	Label  string   `arg:"optional" help:"migration label"`
	DryRun bool     `default:"false" help:"print changes without applying"`
	Extra  []string `arg:"*" help:"additional migration inputs"`
}

func init() { pw.RegisterSubCommand[MigrateOptions]("migrate", "run database migrations") }
```

```go
options, ok := pw.Command[MigrateOptions]()
```

A subcommand struct reads **no TOML and no environment value**; its fields come
from the command line alone.

| Tag | Meaning |
| --- | --- |
| *(none)* | an option, named by the same rules as a configuration field |
| `arg:"required"` | a positional argument that must be present |
| `arg:"optional"` | a positional argument that may be omitted |
| `arg:"*"` | the remaining positional arguments |

```sh
./myapp migrate ./migrations
./myapp migrate ./migrations --dry_run release extra-a extra-b
```

Options may appear before or after positional arguments. Only the selected
subcommand returns a value from `pw.Command`. A missing required argument, an
unknown command or option, and `--help` all fail parsing with generated usage
text. `pw.SubCommand` remains as a deprecated alias of `RegisterSubCommand`.

See [Custom Commands](/guides/architecture/custom-commands/).

## What the startup summary shows

Resolved configuration is reported once at startup, each entry with the source
its value won from. On a terminal this is the configuration tree; in a container
or a pipe it is the same data in a structured log record. `secret` controls
disclosure, `dependon` filters inactive branches, and `falsy` helps that filter
recognize non-boolean forms of “off.”

### `secret`: control what the startup log discloses

Configuration often reaches a log collector, so a credential must not appear
merely because the application printed its effective settings. Use `secret`
for a sensitive field whose name does not trigger the automatic rule, to omit
a field completely, or to undo an automatic false positive:

```go
type DeliveryConfig struct {
	Password        string
	SigningMaterial string `secret:"mask"`
	TokenBucketSize int    `default:"128" secret:"show"`
	InternalNote    string `secret:"hide"`
}
```

Registered under `delivery`, the visible part of the startup tree is:

```text
delivery
├─ password           *****
├─ signing_material   *****
└─ token_bucket_size  128
```

`password` is masked automatically. `signing_material` does not look sensitive
to the automatic rule, so `secret:"mask"` supplies the missing policy.
`token_bucket_size` contains `token` but is not a secret, so `secret:"show"`
undoes that conservative match. `internal_note` is absent because `hide`
removes the whole entry; `mask` would retain the key and source while replacing
only a non-empty value with `*****`.

The exact automatic rule is case-insensitive substring matching against the
full stable key path. A field with no `secret` tag is masked when that path
contains any of:

```text
password  secret  apikey  api_key  credential  access_key  accesskey  token  dsn  private_key
```

A stable key ending in `.dsn` is the one display exception. It still counts as
sensitive, but the startup summary and `pw doctor` retain the scheme, host,
port, and database path while replacing user information with `*****` and
dropping the query string. If the DSN cannot be parsed safely, the whole value
is masked.

An explicit `secret` tag always wins: `mask` masks, `hide` omits, and `show`
prints the value. Use `show` only to correct a name such as
`token_bucket_size`, never to expose a credential.

### `dependon`: remove disabled branches from the summary

An authentication switch illustrates the noise `dependon` removes. With
authentication disabled, provider paths and credentials may still have
defaults or values, but they do not describe anything the process will use:

```go
type AuthConfig struct {
	Enabled bool       `default:"false"`
	Mode    string     `default:"oidc_only" dependon:".enabled"`
	OIDC    OIDCConfig `dependon:".enabled"`
}

type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string `secret:"mask"`
}
```

When `auth.enabled` is `false`, the startup tree reports the decision and
nothing underneath it:

```text
auth
└─ enabled  false
```

Without `dependon`, the same disabled feature would also print `mode` and every
OIDC setting. A leading dot names a sibling in the enclosing struct, so both
tags above resolve to `auth.enabled`; an absolute key such as
`dependon:"server.tls_enabled"` can cross a struct boundary. Putting the tag on
the `OIDC` struct field applies it to every leaf in that subtree. Dependencies
are transitive: one empty parent hides the dependent and anything that depends
on it.

This is a presentation filter, not a feature switch. Hidden fields are still
bound from TOML, environment variables, and flags; their CLI options, help, and
scaffolds remain. Application behavior must still read `Enabled` or its own
equivalent.

### `falsy`: teach `dependon` what “off” means

`dependon` already treats a missing value, an empty string, and boolean `false`
as off. A string enum often uses a non-empty choice such as `none` or `off`
instead. `falsy` gives that choice the same meaning for display filtering:

```go
type ExportConfig struct {
	Mode     string `default:"none" enum:"none,otlp,stdout" falsy:"none"`
	Endpoint string `dependon:".mode"`
	Headers  string `secret:"mask" dependon:".mode"`
}
```

With `mode = "none"`, the summary shows `export.mode` and hides `endpoint` and
`headers`. Without `falsy:"none"`, `none` is merely a non-empty string, so both
dependents would remain visible.

Numbers and durations need the same explicit decision because zero can be a
real setting. Here zero disables slow-statement detection and therefore hides
the `EXPLAIN` option:

```go
type QueryConfig struct {
	// Zero disables slow-statement detection, and with it EXPLAIN.
	SlowThreshold time.Duration `falsy:"0s" help:"slow statement threshold"`
	Explain       bool          `dependon:".slow_threshold" help:"run EXPLAIN on slow statements"`
}
```

The falsy value also fills the field when nothing else sets it:

- no `default` tag and no source sets the key — the field resolves to the falsy
  value;
- a source sets the key to `""` — it resolves to the falsy value, keeping that
  source as its origin;
- a `default` tag is present — the default wins and `falsy` never substitutes.

The comparison is by value rather than by text, so `0`, `0s`, and `0ms` all read
as off. `falsy` applies only to strings, integers, and durations: booleans
already have `false`, while lists have no single value the generator can safely
declare to be off. Without a `falsy` tag a number or a duration cannot be a
`dependon` parent at all; generation fails rather than guessing that zero means
disabled.

See [Startup Summary](/productivity/startup-summary/).

## Common errors

- a prefix that is not a string literal, or an empty one
- `pw.RegisterConfig` called after `ParseConfig`
- a field of an unsupported type — a float, a map, a pointer, a slice of
  anything but `string` or a named struct
- an unparsable `default`, including a bare number on a `time.Duration`
- `opt`, `env`, `falsy`, `dependon`, or `secret` on an array-of-tables element
  field
- `falsy` on a nested struct field
- a number or duration named by `dependon` with no `falsy` tag of its own
- one environment name assigned to two fields
- `${NAME}` naming a variable the process does not have
