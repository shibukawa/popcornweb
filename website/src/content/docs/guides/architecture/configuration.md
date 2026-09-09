---
title: Application Configuration
description: How an application's runtime settings resolve from TOML, environment variables, and flags into one typed struct, and how to add your own.
sidebar:
  order: 3
---

Configuration arrives from several places and resolves to one typed view.
Popcorn Web binds TOML files, environment variables, and command-line options
to structs before the first request, so an invalid value stops startup instead
of surfacing midway through runtime.

Nothing here reflects at runtime. `pw generate` reads your registration call and
writes the binding code ahead of time, which is what keeps the whole mechanism
available under TinyGo — and what makes a few of the rules below stricter than
a reflection-based binder would need.

What this covers is the configuration a running application reads.
`popcornweb.toml`, which `pw` itself reads, does not appear here — that is
[Build Tool Configuration](/reference/build-configuration/).

For the framework's own keys and their defaults, see
[Application Configuration Keys](/reference/configuration/). This page is the machinery
underneath them.

## Environments

`APP_ENV` selects the runtime environment. It accepts `dev`, `stg`, `prod`, or
any other token made of lowercase letters, digits, `-`, and `_`. An invalid
token fails `ParseConfig`. When unset or empty it defaults to **`dev`**.

```sh
APP_ENV=prod ./myapp
```

`pw.Env()` returns the resolved token, and `pw.EnvDevelopment`, `pw.EnvStaging`,
and `pw.EnvProduction` name the well-known ones.

## File resolution

Selecting an environment determines the project-local filename. Popcorn Web
searches the working directory first, then its `config/` directory:

1. `./config.{APP_ENV}.toml`
2. `./config/config.{APP_ENV}.toml`

User and system configuration directories use the environment-neutral
`config.toml`. A project tree does not: a bare `config.toml` there is never
read. The asymmetry is deliberate — a file that applies to every environment is
reasonable on one operator's machine and misleading in a repository, where
"which environment is this for?" would have no answer.

`--config-path` replaces the search entirely:

```sh
./myapp --config-path ./deploy/staging.toml
```

## Secrets and dotenv files

The whole path a secret takes, from a laptop to each kind of host, is on
[Handling Secrets](/guides/deployment/secrets/); this section is the workstation
half of it.

A production file names its secrets rather than holding them — the scaffolded
`config.prod.toml` writes `dsn = "${DATABASE_URL}"` and expects the deployment
to supply the variable. On a workstation that variable has to come from
somewhere, and a shell profile is the wrong place because it follows the
developer across projects. So the application reads dotenv files from its
working directory at startup, in this order:

1. `./.env`
2. `./.env.local`
3. `./.env.{APP_ENV}`
4. `./.env.{APP_ENV}.local`
5. every file under `/run/secrets`, one variable per file, where a container
   runtime mounts its secrets
6. the process environment

A later source wins on the same name, which is the order Vite and Next read
the same family in: `.env.stg` overrides `.env.local`, because the
environment's own file is more specific than a machine-wide one, and
`.env.stg.local` overrides everything. A variable exported in the shell or set
by the platform overrides every file. A value from a `.local` file or from the secret mount is masked in the
startup summary whatever its key is called; the shared files are shown. The split is between what is shared and what is this machine's: `.env`
and `.env.{APP_ENV}` are committed and carry values every checkout agrees on,
while the two `.local` files are what `.gitignore` excludes and therefore where
a secret goes. The files are one more way to fill the environment layer, not a
layer of their own: a name written in `.env.dev.local` and the same name
exported in the shell reach the application identically, and the startup
summary tells them apart only by naming the file each value came from.

`pw init` writes `.env.example`, which lists the variables the selected
capabilities read outside development with empty values, and adds `.env.local`
and `.env.*.local` to `.gitignore`. Copy the template to `.env.local` — or to
`.env.dev.local` when the value belongs to one environment — and fill it in.
`pw doctor` reports a value assigned in the template as an error in every
environment, because that file is committed; it reports a secret in `.env` or
`.env.{APP_ENV}` the way it reports one in a tracked TOML file.

`APP_ENV` itself is read from the process first and from `.env` and
`.env.local` second, so a checkout can pin its environment there. It cannot be
set in `.env.{APP_ENV}` or its local file: those were chosen by the token, and a
value there is ignored with a warning.

The read is unconditional in every process that resolves the project's
configuration from its directory, `pw doctor` included, so there is no switch to
forget. Where there is no filesystem — a Cloudflare Workers build hands the
loader its bindings as the environment — no file is looked for. A platform that
injects every variable itself needs no dotenv file, and the files' absence
changes nothing.

## How one value is resolved

Each key is reachable four ways, in increasing precedence:

```
default  <  TOML file  <  environment variable  <  command-line option
```

Dotenv files feed the environment-variable step, below the process's own
environment.

The three names come from one struct field. Field names become snake_case and
nest under the prefix, and the prefix is not derived from anything — it is the
literal the registration passes:

```go
type AppConfig struct {
	Mailer MailerConfig
}

type MailerConfig struct {
	FromAddress string `default:"noreply@example.com"`
}

// "app" is the first segment of every key below. Rename the type and nothing
// moves; change this string and all three names move together.
func RegisterConfig() { pw.RegisterConfig[AppConfig]("app") }
```

Where that call goes and why its position matters is
[Adding your own settings](#adding-your-own-settings) below. With it in place
the key is `app.mailer.from_address`, the TOML is `[app.mailer]` with
`from_address = …`, the option is `--app-mailer-from_address`, and the
environment variable is `APP_MAILER_FROM_ADDRESS`. Dots separating nesting
levels become dashes in the option and underscores in the variable; underscores
inside a single key survive untouched.

Five tags adjust the result:

| Tag | Effect |
| --- | --- |
| `default:"value"` | value when nothing else supplies one |
| `key:"name"` | override the stable TOML/config key |
| `opt:"long"` / `opt:"long,s"` | override the CLI option, optionally with a short form |
| `env:"NAME"` / `env:"-"` | exact environment variable name, or disable environment input |
| `help:"text"` | description shown in usage and scaffolds |

Overriding `opt` also moves the environment name, which derives from the long
option rather than from the key. That is how `server.port` answers to `--port`
and to `PORT`.

Three further tags describe a key rather than name it:

| Tag | Effect |
| --- | --- |
| `secret:"mask"` / `"hide"` / `"show"` | how the value appears in the startup summary |
| `falsy:"value"` | the value that counts as "not set" for anything depending on this key |
| `dependon:".sibling"` | the key this one answers to; a leading dot is relative to the enclosing struct |

`dependon` does not change binding. A dependent key is still read and still
applied — it is only omitted from the startup summary while its parent is empty,
so a disabled subsystem reports one line instead of seven. A key you set and
cannot find in the summary is therefore a question about its parent, not about
your spelling.

:::caution
Supported field types are `string`, `bool`, `int`, `time.Duration`, `[]string`,
nested structs of those, and a slice of a named struct filled from an array of
tables. Floats, maps, pointers, and other slice element types are **not**
bindable — declare them as `string` or `int` and convert after parsing.
:::

The full declaration surface — every tag, the duration syntax, the array-of-tables
rules, and the TOML subset the loader reads — is
[Application Configuration Declaration](/reference/configuration-declaration/).

## Adding your own settings

Application settings follow the same path as framework settings: declare a
struct, register it under a prefix, and read the result from the request
context. `pw generate` turns the registration call into binding code, so adding
a setting does not add a parallel parser.

### 1. Declare the struct

```go
package handlers

import "github.com/shibukawa/popcornweb/pw"

type AppConfig struct {
	EnvLabel      string `default:"local" help:"environment name shown in the page badge"`
	EnvLabelColor string `default:"#64748b" help:"CSS color of the environment badge"`
}
```

### 2. Register it

```go
func RegisterConfig() { pw.RegisterConfig[AppConfig]("app") }
```

```go
func main() {
	handlers.RegisterConfig()
	if err := pw.Run(context.Background(), handlers.Handlers()); err != nil {
		log.Fatal(err)
	}
}
```

Where you place that call matters. Generated definitions register during package
`init`, so the binding must be created **after** every `init` has run but
**before** parsing begins. Registration after `ParseConfig` panics, and the
prefix must be a string literal that the generator can read.

Each area of a larger application can register its own struct — see
[Project structure](/guides/architecture/project-structure/) — but prefixes
share one namespace, so give them distinct names (`app`, `billing`, `search`).

### 3. Read it

```go
app := pw.Config[AppConfig](r)
```

`pw.Config` is available anywhere a request context is, and takes `nil` outside
a request. It returns no error: an unparsed prefix yields its declared defaults
and an unregistered type yields the zero value, because a handler reading
configuration is already on the response path, where a nil check would only
postpone the same missing value to a later line.

### 4. Set it

```toml
[app]
env_label = "development"
env_label_color = "#059669"
```

```sh
APP_ENV_LABEL=development ./myapp
./myapp --app-env_label=development
```

## Generating a scaffold

Every registered prefix — framework and application alike — can print itself,
with `default` values filled in and `help` text as comments:

```sh
./myapp --generate-config toml > config.dev.toml
./myapp --generate-config env > .env.example
```

Because the binary reports registrations from its actual imports, the scaffold
matches the packages linked into that build. Add a struct and rerun the command;
the new keys appear. Either form exits after writing — the server does not
start. See [Custom Commands](/guides/architecture/custom-commands/).

The env form is the complete variable list, which is more than the template `pw
init` writes; the template names only what the selected capabilities read from
the environment. Direct it at `.env.example` rather than `.env`, since `.env` is
read at the next start and every default the scaffold carries would then count
as set.

## Seeing what took effect

Resolved configuration is reported once at startup — as a tree on a terminal, as
one structured record everywhere else — so "did that value actually land?" has
an answer that costs no extra log line. Each entry shows where its value came
from, and a `secret` tag decides whether it is printed, masked, or dropped.
`observability.boot_log` selects the format. See
[Configuration Summary](/productivity/startup-summary/).
