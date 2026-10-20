---
title: Build Tool Configuration
description: Every key of popcornweb.toml — what pw generates, what pw dev runs beside your application, and where the migrations and stylesheet live.
sidebar:
  order: 2
---

`popcornweb.toml` sits at the project root and belongs to the `pw` command. It
describes the *project*: which directories generation reads, which compiler the
sources were written for, what runs beside the application during development.

It holds no runtime setting at all. Ports, pools, cookies, and log levels live
in `config.{APP_ENV}.toml` and are listed in
[Application Configuration Keys](/reference/configuration/). The split is enforced
rather than conventional — a `server` or `session` table here is an error, and
so is a database connection string. The two files are read by two different
programs at two different times.

[`pw init`](/pw/project/init/) writes this file, and
[`pw add`](/pw/project/add/) edits it when you install a capability. Editing it
by hand is expected; the rules below are what the loader checks.

## `[project]`

| Key | Default | Meaning |
| --- | --- | --- |
| `name` | *(required)* | the project name, also the `OTEL_SERVICE_NAME` `pw dev` injects |
| `kind` | `"application"` | `application` builds a binary; `package` is published as a Go module |
| `main` | *(required for an application)* | the package [`pw build`](/pw/project/build/) compiles, e.g. `"./cmd/myapp"` |
| `toolchain` | `"tinygo"` | the compiler the sources were scaffolded for: `tinygo` or `go` |
| `database` | `"sqlite"` | the SQL dialect `.pw.sql` sources generate for: `sqlite`, `postgres`, or `mysql` |

`toolchain`, `database`, and `kind` reject any other value, and each default is
history rather than preference: a project written before the key existed could
only have been TinyGo, could only have been SQLite, and could only have been an
application.

`kind` decides which of the two sections below is legal. A package carries no
`main` — the application that imports it owns the entry point — and an
application carrying a `[package]` section is an error rather than an ignored
block. See [Component Packages](/guides/deployment/package/).

`database` is a *generation* input. It decides the dialect the generated Go
reads your SQL as; the engine the application actually connects to still comes
from the scheme of the `[[middleware.rdb.connections]]` DSN. Keeping the two in
agreement is on you
— nothing here can check a DSN it is forbidden to hold.

## `[generate]`

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

Even a project with no queries must declare `queries = []`. Omitting `generate.queries` is different from an explicit empty scope.

Each purpose lists the directories [`pw generate`](/pw/project/generate/) may
read *for that purpose*, and nothing else. A `.pw.sql` file in a directory that
`queries` does not list is invisible to generation — which is why generation
warns about a `.pw.html` or `.pw.sql` sitting outside the purpose that owns it,
rather than silently picking it up.

| Key | Reads | Required |
| --- | --- | --- |
| `generate.handlers` | handler sources, for route and binding analysis | yes |
| `generate.templates` | `.pw.html` templates, including the document shell | yes |
| `generate.queries` | `.pw.sql` sources | yes |
| `generate.config` | configuration registrations | yes |
| `generate.pages` | [page tree](/guides/cross-layer/discovered-routing/) roots | no |
| `generate.dynamo` | `dynamo`-tagged Go types and `.pw.dynamo` declarations | no |
| `generate.firestore` | `firestore`-tagged Go types and `.pw.firestore` declarations | no |

Every key but `pages`, `dynamo`, and `firestore` is required, and `[]` is how a project says a
purpose generates nothing. A missing key cannot say that, which is why the empty
list is not the same as leaving it out. The two optional keys are the ones a
project can predate: every project scaffolded before page trees existed has
neither the key nor a tree. Store-specific keys appear when `pw add dynamo` or
`pw add firestore` installs that store.

`dynamo` reads Go type declarations rather than a template language, which is
why it is a purpose of its own instead of part of `queries`. See
[DynamoDB Query Format](/reference/dynamo-templates/).

`firestore` follows the same separation and is independent of `dynamo`; a
project may use either or both. See
[Firestore Query Format](/reference/firestore-templates/).

The rules on the entries themselves:

- relative to the project, naming a directory that exists
- no duplicates, and no entry nested inside another entry of the same purpose —
  the inner one's sources would be planned twice, and the second plan would
  delete what the first wrote
- exactly one `generate.templates` entry holds the
  [document shell](/guides/frontend/templates/); a second is an error
- a `generate.pages` entry is a whole tree, so it is neither listed under
  `templates` or `handlers` nor nested with one of their entries

Directory names are defaults, not identity. Every consumer reads the purpose
list rather than the name, so renaming `handlers/` to `web/` is moving the
directory and editing one line. The generated package name follows the
directory, so the sources still compile.

## `[dev.watch]`

```toml
[dev.watch]
includes = []
excludes = []
```

[`pw dev`](/pw/project/dev/) already walks the module for rebuild inputs, so
unlike generation this has a working default and both keys are optional.
`includes` adds relative files or glob patterns the walk misses. `excludes`
skips a directory subtree, which is worth doing for a large tree that only makes
the walk slower.

## `[dev.idp]`

```toml
[dev.idp]
enabled = false
config = "devidp.toml"
port = 0
```

The [development identity provider](/productivity/dev-identity-provider/) `pw
dev` runs beside the application. `enabled = true` requires the roster file to
exist. `port = 0` reserves an available loopback port, which is the useful
default because `pw dev` injects the resolved issuer into the application — a
fixed number matters only to a client registered somewhere outside this project.

## `[dev.otel]`

```toml
[dev.otel]
enabled = true
port = 0
max = 0
```

The [telemetry viewer](/productivity/dev-telemetry-viewer/), and the one block
here that is on by default. `port = 0` reserves a loopback port and `pw dev`
injects the resolved endpoint, exactly as for `dev.idp`. `max` bounds the
records retained per signal; `0` keeps the viewer's own default.

Both `dev.idp` and `dev.otel` affect `pw dev` and nothing else.

## `[migration]`

| Key | Default | Meaning |
| --- | --- | --- |
| `dir` | `"migrations"` | where [migration files](/productivity/migrations/) live, relative to the project |
| `auto` | `true` | apply pending migrations at the start of `pw dev` |

`auto` enables that one step in the developer loop. It never causes an
application to migrate its own database at startup — that stays an explicit call
in your code, because the process that serves requests is rarely the one that
should be changing the schema.

`dir` is a tooling path. It tells `pw` where the files are and has no runtime
meaning.

## `[assets]`

```toml
[assets]
public = "cmd/app/public"
derived = "cmd/app/dist/public"
embed = "cmd/app/public.go"
```

| Key | Default | Purpose |
| --- | --- | --- |
| `assets.public` | `"public"` | Authored input tree |
| `assets.derived` | `"dist/public"` | Built tree served and embedded by the application |
| `assets.embed` | `"public.go"` | Authored Go embed file checked before building assets |

All paths are relative to `popcornweb.toml`. Escaping paths, symlinks in any
component, and overlapping input/output trees are rejected. The derived tree
must be below the directory containing the embed file, as required by `go:embed`.
The example uses `//go:embed all:dist/public` in `cmd/app/public.go` and
`fs.Sub(embeddedPublic, "dist/public")`.

`public_manifest_pw_gen.go` is written beside the embed file, in its package.
`manifest.json`, `routes.json`, the `derived/` staging tree, and the `cache/`
conversion cache go beside the configured derived tree; here that is
`cmd/app/dist/`. Extracted component files go under the authored tree's
`generated/` directory. Tailwind input/output paths remain project-relative.

`pw dev` passes `assets.derived` to the application as
`SERVER_PUBLIC_LOCAL_ROOT`. Direct `go run -tags=pwdev` invocations can set
`server.public.local_root` themselves; its default is `dist/public`.
The project root, module import paths, config files, and dotenv discovery are unchanged.

## `[assets.tailwind]`

```toml
[assets.tailwind]
enabled = true
input = "assets/app.css"
output = "public/generated/app.css"
minify = true
```

Present when the project was scaffolded with [Tailwind](/guides/frontend/styling/),
absent otherwise. `input` and `output` must be different files, both relative to
the project.

`minify` is the odd one: [`pw build`](/pw/project/build/) minifies whatever the
key says, and `pw dev` never does. What the value actually feeds is
[`pw doctor`](/pw/project/doctor/), which reports an unminified stylesheet as a
readiness finding for a deployed environment. Leave it `true`.

Tailwind plugins are not configured here. They are `@plugin` declarations in the
CSS entry, resolved by the Tailwind CLI — Popcorn Web passes the entry through
unchanged and holds no plugin registry.

## `[assets.images]`, `[assets.css]`, `[assets.scripts]`

```toml
[assets.images]
enabled = true
quality = 75
avif = false

[assets.css]
minify = true

[assets.scripts]
enabled = true
```

These switch the [asset conversions](/guides/frontend/static-assets/) on. Every
one of them defaults to off, so a project that declares none embeds a copy of
its authored tree and serves exactly what it served before any of this existed.

| Key | Default | Meaning |
| --- | --- | --- |
| `assets.images.enabled` | `false` | convert a `.png` or `.jpg` named by an `img src` to WebP |
| `assets.images.quality` | `75` | the lossy setting a JPEG source is re-encoded at; a PNG stays lossless and ignores it |
| `assets.images.avif` | `false` | add an AVIF representation of every served image, chosen from `Accept` |
| `assets.css.minify` | `false` | minify stylesheets in place |
| `assets.scripts.enabled` | `false` | build a `.ts` or `.tsx` entry point, and minify an authored `.js` |

`assets.images` needs encoders on the host, which is why
[`pw add images`](/pw/project/add/) writes the key and the Devbox packages
together. Turning it on without them is not an error: the conversion declines,
the authored image ships as written, and `pw doctor` reports it — an unconverted
image is a larger page rather than a broken one.

## `[assets.verify]`

```toml
[assets.verify]
enabled = true
svg_scan = true
allow = ["vendor/**"]
```

Both checks read bytes the asset walk already holds to digest them, so unlike
every conversion above these default to **on**.

| Key | Default | Meaning |
| --- | --- | --- |
| `assets.verify.enabled` | `true` | refuse an authored public file whose bytes contradict its extension |
| `assets.verify.svg_scan` | `true` | refuse an authored `.svg` carrying `<script`, an `on…=` handler, or `javascript:` |
| `assets.verify.allow` | `[]` | paths exempt from both checks, relative to `public/`; a trailing `/**` exempts a subtree |

A refusal fails `pw build` and names the file, what its extension claimed, and
what the bytes carry. An exempted path is printed by the build instead, so a
list added for one bad file and never removed does not go quiet.

[`pw doctor`](/pw/project/doctor/) reports the same two conditions without
running a build, as
[PW0130](/appendix/diagnostics/#pw0130-a-public-files-content-does-not-match-its-extension)
and
[PW0131](/appendix/diagnostics/#pw0131-a-public-svg-carries-executable-content).
[Static assets](/guides/frontend/static-assets/) explains what each check can
decide and why the SVG one is deliberately incomplete.

## `[[packages]]` — in an application

```toml
[[packages]]
module = "example.com/widget"
```

One entry per [component package](/guides/deployment/package/) the application
uses. `module` is the only key, and it must also be in `go.mod`.

The entry is what *links* the package: [`pw generate`](/pw/project/generate/)
emits one blank import per declaration into the bootstrap file it maintains. A
module in `go.mod` and not in this list is an ordinary Go dependency, and a
declaration naming a module with no `[package]` section is an error — it claims
a capability the module does not publish.

## `[package]` — in a package

```toml
[package]
module = "example.com/widget"
summary = "A note widget with its own storage"
assets.declared = true
routes.register = "Register"

[package.requires]
capabilities = ["database"]
engines = ["sqlite"]

[package.generated_with]
pw = "v0.4.0"
tinybind = "v0.3.5"

[package.migrations]
dir = "migrations"
stem = "widget"
engines = ["sqlite"]
```

| Key | Meaning |
| --- | --- |
| `module` | *(required)* the Go module path, which must match `go.mod` |
| `summary` | one line, shown when `pw add` reports the package |
| `import` | the package path an application links, when it differs from the module root; omitting it where the root holds no Go is `PW0144` |
| `requires.capabilities` | project capabilities the package needs, such as `database` |
| `requires.engines` | the SQL engines the package supports; empty means it touches no SQL |
| `generated_with.pw`, `generated_with.tinybind` | the versions that produced the committed artifacts |
| `config.section` | the runtime configuration section the package registers |
| `migrations.dir` | the migration stream, relative to the module root |
| `migrations.stem` | *(required beside `dir`)* names the stream's version table and the package's tables |
| `migrations.engines` | the engines the stream is written for |
| `routes.register` | the exported symbol the application calls to mount the package |
| `assets.declared` | whether the package registers embedded browser files |
| `components.exported` | reserved; writing it today is a load error |

`generated_with` constrains nothing on its own — `go.mod` performs the
resolution. It is the evidence [`pw doctor`](/pw/project/doctor/) compares when
it reports a package generated by a newer framework than this project builds
with.

`migrations.engines` must cover everything `requires.engines` declares.
Otherwise the package claims support for an engine it never wrote schema for,
and the failure lands at the first migration rather than at the declaration.

`generate.queries` must be empty in a package. A generated query carries one
engine's placeholder syntax and a package cannot know its consumer's.

## Rules that apply to the whole file

- **Unknown keys are errors.** A typo does not silently do nothing.
- **Relative paths resolve from the file's directory**, and an absolute path is
  rejected. The project moves as a unit.
- **Command flags override the file.** `pw migrate --dir=other` reads that
  directory for one run without editing anything.
- **This file locates the project.** Every command that acts on one — all of
  them except [`pw init`](/pw/project/init/) and `pw version` — finds the root
  by walking up from the working directory until it sees this file, so `pw`
  works from any subdirectory.
- **Runtime values are forbidden.** `server`, `session`, `security`,
  `middleware`, and `observability` tables belong to the other file, and a
  database connection string belongs there too. `project.database` names an
  engine, never a DSN and never a credential.
