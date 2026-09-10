---
title: pw add
description: Install a framework capability into an existing project.
sidebar:
  order: 2
---

```sh
pw add [registered|discovered|devbox|database|dynamo|firestore|redis-valkey|auth|tailwind|images]
pw add <module-path>
```

`pw init` asks which capabilities a project starts with, before the project is
understood well enough to answer. `pw add` installs one afterwards, so a
declined answer is not a decision the project is stuck with.

The command runs inside an existing project and asks its questions in a wizard.
There is no flag form: unlike `pw init`, which creates a fresh directory, this
edits configuration, migrations, and sources the project already depends on —
and the review screen is where that edit is approved.

An argument containing a dot in its first element is read as a module path
instead, which installs a
[component package](/guides/deployment/package/). That route has no wizard and
no review screen, because it copies nothing: it writes the `go.mod` requirement
and one `[[packages]]` entry, then prints the remaining commands. See
[The consuming side](/guides/deployment/package/#the-consuming-side).

## The catalog

| Capability | What it installs |
| --- | --- |
| `registered` | the handler tree, its mux, and one route registration written in Go |
| `discovered` | the page tree, its layout, and the `generate.pages` entry that reads it |
| `devbox` | `devbox.json` and `devbox.lock`, carrying the toolchain this project already uses |
| `database` | the `[middleware.rdb]` section, the migration directory, and a typed SQL example |
| `dynamo` | the `[middleware.dynamo]` section, a typed record, and the local DynamoDB server |
| `firestore` | the `[middleware.firestore]` section, a typed entity, and a `.pw.firestore` query |
| `redis-valkey` | the Valkey development server in `devbox.json` |
| `auth` | login sessions, the framework tables, and the account resolver |
| `tailwind` | the pinned Tailwind toolchain, its CSS entry, and the `[assets.tailwind]` block |
| `images` | the pinned image encoders and the `[assets.images]` block that switches conversion on |

The argument preselects the first step; omitting it lists only what this project
does not already carry. Two capabilities depend on another one: `auth` needs
`database`, `dynamo`, or `firestore` for its login records, and `redis-valkey` needs `devbox`, because
the Valkey answer writes nothing but a Devbox package. Choosing one in a project
that lacks its dependency installs the dependency first and says so on the
review screen.

`images` writes the encoders and the switch in one step on purpose. A project
that turned conversion on without them would convert nothing and report that on
every build; a project that installed them without the switch would carry two
packages it never runs. Without Devbox there is nothing to pin them with, so the
capability names the tools in words and leaves the install to you.

## Detection

A capability is detected from the files that carry it, never from a list in
`popcornweb.toml` — a manifest could disagree with a hand-edited project:

| Capability | Evidence |
| --- | --- |
| `devbox` | `devbox.json` |
| `database` | `[middleware.rdb]` in an environment configuration file |
| `dynamo` | `[middleware.dynamo]` in an environment configuration file |
| `firestore` | `[middleware.firestore]` in an environment configuration file |
| `redis-valkey` | the Valkey package in `devbox.json` |
| `auth` | the `[auth]` section, or an `init_popcornweb_auth` migration at any version |
| `tailwind` | `assets.tailwind.enabled` in `popcornweb.toml` |
| `images` | `assets.images.enabled` in `popcornweb.toml` |

Adding a capability the project already has fails and names the file that proves
it:

```
pw: add: this project already has auth, per migrations/00003_init_popcornweb_auth.sql
```

## What it writes

The review screen lists every change before anything is written:

```
  Review
    Capability     auth
    Login          OIDC
    OIDC provider  External provider

    create  handlers/accounts.go
    create  migrations/00002_init_popcornweb_session.sql
    create  migrations/00003_init_popcornweb_auth.sql
    append  config.dev.toml
    edit    cmd/lean/main.go
    then    pw migrate up

  enter add  ·  esc back  ·  ctrl+c cancel
```

`auth` asks which protocol the provider speaks first, OIDC or a plain OAuth
provider such as X, and only the OIDC answer goes on to ask about the local
emulator. A passkey mode stays a `pw init` answer, because its relying-party
registration is bound to the origin the deployment is reached on.

Four rules govern that list:

**Migrations take the next free version.** A project that already applied
`00001` through `00007` gets `00008`; nothing is renumbered, because a migration
the project may have applied can never move.

**Configuration is appended, not rewritten.** Your comments and tuned values
survive. A section of the same name already present stops the command.

`pw add auth` installs the `rdb` session backend, the one that fits a project
that already has a database. Session storage is opt-in by blank import, so the
import line is one of the manual steps —
[`pw init`](/pw/project/init/#session-storage) is where the cookie and Redis
backends are offered instead.

**Application-owned files are never overwritten.** A conflict is reported and
nothing is written. What the framework will not do for you — the call in
`main.go`, the stylesheet link in the document shell — is printed as a manual
step instead.

**Nothing is partial.** Every file is computed first and written together, so a
step that cannot succeed leaves the project as it was.

## Exit status

| Situation | Exit |
| --- | --- |
| capability installed | 0 |
| wizard canceled | 0, nothing written |
| no terminal | non-zero with usage |
| already present, or a conflict | non-zero with the path and the reason |
