---
title: pw init
description: Create a runnable Popcorn Web project.
sidebar:
  order: 1
---

```sh
pw init <project-name> [--preset=<name>] [--yes] [--tailwind] [--tinygo] [--no-devbox] [--no-database] [--db=<engine>] [--dynamo] [--firestore] [--no-redis] [--router=<kind>] [--auth=<mode>] [--session=<backend>] [--devidp] [--skills=<dir>]
```

The command creates a complete, runnable project in a new directory. In a
terminal it opens on a list of presets; `--yes` takes the flags and the defaults
without asking, which is what a script wants.

## Presets

Ten questions, each defensible on its own, add up to a sequence nobody can
answer before they have built anything. A preset is a name for one of the
handful of combinations people actually want, and choosing one is the only
question left except the project name.

| Preset | Project | What it decides |
| --- | --- | --- |
| `website-login` | a website whose pages belong to whoever signed in | discovered routing, OIDC, Redis sessions, SQLite, Tailwind |
| `website-aws` | the same website with no relational database to operate | discovered routing, OIDC, DynamoDB for everything, Tailwind |
| `website-discovered` | a website with no accounts and nothing to store | discovered routing, Tailwind, no login, no database |
| `website-registered` | the same site written as Go registrations | registered routing, otherwise identical |
| `api-server` | a machine-facing API whose callers bring their own token | registered routing, JWT verification, no browser login |
| `package` | a module published for other projects to import | a different project kind — see [Component packages](/guides/deployment/package/) |
| `manual` | anything the six above do not describe | nothing; every answer is yours |

Every preset answers TinyGo and Devbox the same way — no to the first, yes to
the second — because neither changes what the project contains. TinyGo is still
reachable from a preset: open that row on the review screen below.
`--preset=<name>` gives the same answers without the terminal, and it is refused
beside any flag that answers a question it already answered, since neither would
obviously win.

**The review screen is the list of what a preset chose, and every row on it is
editable.** Press enter on a row to reopen that question and land back on the
list. A preset is where you start, not what you are stuck with, and `manual` is
the same screen opened on the defaults instead.

Which one to take: `website-discovered` if you are learning the framework, since
it is the smallest project that still serves pages, and every capability it
declines is one [`pw add`](/pw/project/add/) away. Take `website-login` when you
already know the application has accounts — retrofitting a login means a
database, a session store, and a provider, and the preset wires all three
together correctly.

[Changing what a preset chose](/pw/project/presets/) covers moving off any of
these answers afterwards.

## Options

| Option | Effect |
| --- | --- |
| `--preset=<name>` | answer every question below at once; see [Presets](#presets) |
| `--yes` | take the flags and the defaults without asking |
| `--tailwind` | also scaffold the Tailwind CSS toolchain |
| `--tinygo` | also target TinyGo: `pw.ServeMux` routing, the toolchain in `devbox.json`, and a second Dockerfile |
| `--no-devbox` | no `devbox.json`; keep your own setup — mise, Docker Compose, Nix, Homebrew, Scoop |
| `--no-database` | no rdb configuration, no migrations, and no SQL example |
| `--db=<engine>` | `sqlite` (default), `postgres`, or `mysql` |
| `--dynamo` | add the DynamoDB store: its configuration, a typed record, and the local server |
| `--firestore` | add Firestore in Datastore mode: its configuration, a typed entity, and a query declaration |
| `--no-redis` | leave the Valkey development server out of `devbox.json` |
| `--router=<kind>` | `registered` (default), `discovered`, or `both`; see [Discovered routing](/guides/cross-layer/discovered-routing/#commands) |
| `--auth=<mode>` | `none` (default), `oidc`, `oidc-passkey`, `passkey`, or `oauth` |
| `--session=<backend>` | with a login, where sessions live: `rdb` (default), `cookie`, `redis`, `dynamo`, or `firestore` |
| `--devidp` | with an OIDC mode, wire up the local identity provider |
| `--skills=<dir>` | where the bundled agent skill lands: `claude` (default), `agents`, or `none`; see [The agent skill](#the-agent-skill) |

`--tailwind`, `--no-database`, `--dynamo`, `--firestore`, `--no-redis`, and `--auth` all select
capabilities [`pw add`](/pw/project/add/) can install later, so declining one
costs nothing permanent. A browser login needs one server store for ceremony
and account-side records. With `--no-database`, add either `--dynamo` or
`--firestore`; without one of those, an `--auth` mode is rejected.

`--tinygo` is the answer `pw add` cannot revisit — see
[Changing the toolchain](#changing-the-toolchain). That is why the default is
host Go: it is the direction that assumes less, and the routing is the same
either way.

## Choosing the database

`--db` decides five things at once: the DSN in `config.dev.toml`, the dialect
the starter migration is written in, the development server added to
`devbox.json`, the driver the binary links, and `project.database` in
`popcornweb.toml`. That last key is what `pw generate` reads to know the
placeholder syntax `.pw.sql` sources compile to. SQLite is the default because
it runs with nothing to start beside the application.

| Engine | DSN | Development server |
| --- | --- | --- |
| `sqlite` | `sqlite://<name>.db` | none |
| `postgres` | `postgres://<name>:<name>@127.0.0.1:5432/<name>?sslmode=disable` | `postgresql` in `devbox.json` |
| `mysql` | `mysql://<name>:<name>@tcp(127.0.0.1:3306)/<name>` | `mysql80` in `devbox.json` |

Every engine adds one blank import to `main.go`, which is what registers it:

```go
import _ "github.com/shibukawa/popcornweb/database/postgres"
```

The scaffolded credentials are development values in `config.dev.toml`. Create
the role and database they name once, then `pw migrate up`.

Changing engines afterwards is not something `pw add` will do for you: the DSN,
every migration, and every `.pw.sql` source would have to be rewritten together.
Pick the engine the project is going to deploy against.

### Generated SQL follows the engine

`project.database` is the one place a project states its engine for generation.
There is no implicit default at the generator: a silently assumed dialect emits
placeholders the engine rejects at the first query, so `pw generate` passes what
`popcornweb.toml` says and nothing else.

```toml
[project]
database = "postgres"   # sqlite, postgres, or mysql
```

| Engine | Placeholders |
| --- | --- |
| `postgres` | `$1`, `$2`, … |
| `mysql` | `?` |
| `sqlite` | `?` |

Changing this key changes every generated query, so run `pw generate` after
editing it and commit the result together with the DSN change.

A project written before this key existed has no `[project] database`, and is
read as `sqlite`, which is the only engine that existed then.

## Changing the toolchain

The selected compiler is recorded as `project.toolchain` in `popcornweb.toml`,
and it decides which mux type the handler packages use: a host-only project —
the default — keeps `http.ServeMux`, and a TinyGo project routes through
`pw.ServeMux` so one import works on both toolchains. Generation discovers
either, so the difference is confined to the scaffold.

There is no command for switching afterwards, because the change reaches source
you own. Doing it by hand is four edits:

1. set `project.toolchain` in `popcornweb.toml` to `tinygo` or `go`;
2. swap the mux type and accessor in each handler package's `index.go`;
3. add or remove `tinygo@latest` in `devbox.json`;
4. add or remove `tinygohelper.go`, the TinyGo-only netdev registration —
   without it a TinyGo binary aborts at startup with `Netdev not set`.

Then run [`pw generate`](/pw/project/generate/). A `project.toolchain` value
other than `tinygo` or `go` is rejected when the project loads.

## Authentication

Authentication changes more than one flag, so the selected mode determines the
`[auth]` section written to `config.dev.toml`:

| Answer | `auth.mode` | What it means |
| --- | --- | --- |
| None | — | no `[auth]` section at all |
| OIDC | `oidc` | every login goes through an OpenID Provider |
| OIDC + passkey | `oidc_passkey` | OIDC bootstraps the account, passkeys handle repeat logins |
| Passkey only | `passkey_only` | no external provider; recovery policy is yours |
| OAuth provider | `oauth_only` | a provider that issues no ID Token, such as X, says who its access token belongs to |

Choosing an OIDC mode asks one follow-up: **local emulator** or **external
provider**. The OAuth answer has no follow-up, because there is no emulator for
a plain OAuth provider: the scaffold names the built-in provider definition
(`provider = "x"`), leaves `client_id` and `client_secret` empty for
`AUTH_OAUTH_CLIENT_ID` and `AUTH_OAUTH_CLIENT_SECRET` to fill, and registers the
development callback URL at `http://localhost:8080/auth/callback`, which must
match the one registered with the provider exactly.

The local emulator is the development identity provider that
[`pw dev`](/pw/project/dev/) runs. `pw init` sets `dev.idp.enabled` in
`popcornweb.toml` and writes a `devidp.toml` roster with two starter users, and
`pw dev` injects the issuer and generated client credentials — so nothing about
the provider is written into a committed config file.

For an external provider, `config.dev.toml` contains empty `issuer`,
`client_id`, and `client_secret` values. Those values are placeholders, **not
optional settings**. The application refuses to start until they are supplied
in the file or through `AUTH_OIDC_*` environment variables; the remaining
alternative is to use the emulator.

## Session storage

An `--auth` mode asks one more question: which server backend holds the session
state that goes there. All five answers read the same in a handler —
`session.Load[T]` and the auth helpers do not change — so this is a deployment
decision, not an API one. What *is* server-placed is decided by each
`pw.RegisterSessionStore` line instead.

| Answer | `session.backend` | What the project gets |
| --- | --- | --- |
| Database | `rdb` | one row per session, revocable, swept; carries a migration |
| Cookie | `cookie` | the record sealed into a second cookie; no storage, no revoking |
| Redis or Valkey | `redis` | server-side TTL per record; revocable, nothing to sweep |
| DynamoDB | `dynamo` | one item per session; revocable, removed by table TTL |
| Firestore | `firestore` | one entity per session; revocable, removed by a deployed TTL policy |

**Storage is opt-in by blank import.** A session backend registers itself from
its package `init`, so the one line that imports it is what puts it — and its
client library — in the binary:

```go
// cmd/myapp/main.go, written by pw init
import (
	// The sessions, and the single-use login records the ceremony needs.
	_ "github.com/shibukawa/popcornweb/authstate/sqlite"
	_ "github.com/shibukawa/popcornweb/sessionstore/sqlite"
)
```

A SQL store is one package per engine, because no engine reads another's DDL.
`--db=postgres` therefore writes `sessionstore/postgres` and
`authstate/postgres`, and the migrations in the PostgreSQL dialect. `sqlite`,
`postgres`, and `mysql` all pass the same store contract test.

`pw init` writes the corresponding import for every backend except `cookie`. The cookie backend is built
into `pw` and needs none, which is why a project can start with sessions and no
storage at all. A project on `rdb` never links the Redis client, and the reverse
holds too.

Configure a backend without its import and startup stops with the missing line
quoted, rather than with a login that fails at the first request:

```
session.backend = "redis" needs its plugin; add to the application:
import _ "github.com/shibukawa/popcornweb/sessionstore/redis"
```

The answer also decides what else is scaffolded. `rdb` writes the session table
migration; the other backends do not. `dynamo` and `firestore` use the store
selected for the project, including its auth records. `redis` adds the Valkey development server to
`devbox.json` even if `--no-redis` was passed, because the session it configures
needs a server to reach.

Every answer gets a `session.keyring.secret`, generated for that project and
written into `config.dev.toml`, because a scaffolded project should run without
an authored secret. It belongs to development: any other environment reads
`SESSION_KEYRING_SECRET`, and `pw doctor --env=prod` reports a literal there as
an error.

```sh
export SESSION_KEYRING_SECRET=$(openssl rand -base64 32)
```

[Session storage](/guides/storage/session-storage/) compares the backends in
terms of revocation, size, and who enforces expiry.

## The agent skill

`pw` bundles a skill: the guideline an AI coding agent loads before it edits
`.pw.html` templates, `.pw.sql` queries, or configuration, with references for
the template and query syntax, the project anatomy, and the `pw` commands that
check an edit. `--skills` decides which agent directory a copy lands in:

| Answer | What it writes |
| --- | --- |
| `claude` (default) | `.claude/skills/popcornweb/` — the directory Claude Code discovers |
| `agents` | `.agents/skills/popcornweb/` — the shared layout other coding agents read |
| `none` | nothing |

The copy is Markdown and costs nothing at runtime, which is why placing it is
the default. It answers for the machines the project is edited on rather than
for the project itself, so a preset never decides it, and a project nobody
edits with an agent passes `--skills=none` — or deletes the directory later,
the way it deletes `.vscode/`. The files come from the `pw` binary that created
the project; there is no command that refreshes them afterwards, so after a
framework upgrade copy the current tree from the repository's
`skills/popcornweb-skill/` if you want the newer guideline.

## Validation

The project name accepts letters, digits, `-`, and `_`; `.` and `..` are
rejected. The destination must be empty or absent — an accidental `pw init .`
in a populated tree fails rather than scattering files.

## What it writes

```
myapp/
├── popcornweb.toml           project name, main package, generation sources
├── config.dev.toml            runtime configuration for APP_ENV=dev
├── config.prod.toml           the same structure for APP_ENV=prod, secrets as ${NAME}
├── .env.example               the variables a deployment supplies; copy to .env.local
├── go.mod
├── devbox.json / devbox.lock  Go + Valkey (+ tailwindcss with --tailwind)
├── cmd/myapp/main.go          calls pw.Run
├── handlers/
│   ├── index.go               the package-level mux and Handlers()
│   ├── home_handler.go        route registration and the net/http handler
│   └── home.pw.html           typed page template
├── templates/
│   ├── document.pw.html       shared document shell
│   ├── templates.go           package marker, present before first generation
│   └── 400|401|403|404|409|413|500.pw.html   error pages
├── queries/users.pw.sql       named SQL with a typed result (with the database)
├── migrations/00001_init.sql  initial schema, in goose format (with the database)
│                              plus framework tables with a login: the session
│                              table only for --session=rdb
├── public/.keep               empty-tree sentinel; never served
├── public.go                  embeds public/ and registers it
├── .claude/skills/popcornweb/  the bundled agent skill (--skills moves or drops it)
├── .vscode/settings.json      hides **/*_pw_gen.go
└── .gitignore                 excludes *_pw_gen.go, .env.local and .env.*.local, and other build output
```

`popcornweb.toml` names the directories it just created under each `[generate]`
purpose, because [`pw generate`](/pw/project/generate/) reads those lists and
has no default to fall back on. `handlers` appears under both `handlers` and
`templates`, since the starter page template sits beside its handler, and
`cmd/myapp` appears under `config`, where the application registers its
configuration.

With an OIDC mode plus `--devidp` it also writes `devidp.toml`, the roster of
selectable development users, and adds `[dev.idp]` to `popcornweb.toml`.

With `--tailwind` it also writes `assets/app.css` and
`public/generated/app.css`, adds the `[assets.tailwind]` block to
`popcornweb.toml`, pins `tailwindcss` in `devbox.json`, and links the
stylesheet from the document shell. No `package.json` and no Node lockfile are
created. See [Styling](/guides/frontend/styling/) for enabling this later.

Each file is written to a temporary path and renamed into place. If the command
is interrupted, it cannot leave a half-written source file behind.

## What it runs

Writing files is not enough to prove the scaffold is usable. `pw init` therefore
runs `go mod tidy` and [`pw generate`](/pw/project/generate/) before reporting
success, leaving a project that compiles immediately:

```
Created myapp

Not included: redis-valkey, auth, tailwind
  pw add <capability> enables one later

  cd myapp
  devbox shell
  pw dev
```

The notice lists only what this run declined, so a scripted `pw init` learns the
same thing the wizard says beside each answer.

Declining Devbox drops the `devbox shell` line, since there is no shell to
enter. Tailwind then has no pinned toolchain either, so the scaffold states what
to install:

```
Tailwind CSS needs its own toolchain here:
  install the standalone tailwindcss CLI, version 4 or later
```

It names the requirement rather than the Devbox package, because
`tailwindcss_4@4.1.18` is a nixpkgs identifier that means nothing to mise,
Homebrew, or Scoop. [`pw build`](/pw/project/build/) reports the same when the
binary is missing.

The generated `go.mod` requires the framework at the version of the `pw` binary
that created it. When `pw` was built from a working copy rather than a release,
it writes a `replace` directive pointing at that checkout instead.

## Next steps

- [1. Getting started](/tutorial/getting-started/) — a walkthrough of the output.
- [pw add](/pw/project/add/) — installing a capability you declined here.
- [pw new](/pw/project/new/) — adding the second handler.
- [Project structure](/guides/architecture/project-structure/) — growing past one package.
