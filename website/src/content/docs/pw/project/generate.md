---
title: pw generate
description: Write every build input — generated Go, the stylesheet, and the asset tree — and stop before the compiler.
sidebar:
  order: 4
---

```sh
pw generate [--code-only] [--debug] [--backend nethttp|fasthttp]
```

`pw generate` is [`pw build`](/pw/project/build/) without its final step. It
produces no binary — it leaves a tree that a compiler can read, and stops.

## What it does

1. compiles templates, SQL, page trees, catalogs, and binding call sites into
   `_pw_gen.go` files **beside their sources**;
2. builds the Tailwind stylesheet **minified**, if Tailwind is enabled;
3. builds the [asset tree](/guides/frontend/static-assets/) into `dist/public`,
   with its compressed sidecars and its manifest;
4. rejects the run if `project.main` depends on a development-only package.

That is the same list `pw build` performs before it links, in the same order,
because `pw build` is defined as this command plus the compiler. The two cannot
drift apart.

Step 4 belongs here rather than beside the compiler for a reason worth saying
out loud: this command hands the tree to a compiler it does not run, so the
check that keeps `contrib/devidp` — an identity provider that signs users in
without checking a password — out of a deployable binary has to happen before
the handoff, not after it.

## Options

| Option | Effect |
| --- | --- |
| `--code-only` | stop after step 1; write no stylesheet and no asset tree |
| `--debug` | keep the source maps in the built tree |
| `--backend` | select the build tags the dependency check lists the graph under |

`--target` selects deployment packaging and belongs to
[`pw build`](/pw/project/build/); it is not accepted here.

`--debug` keeps the source maps exactly as
[`pw build --debug`](/pw/project/build/#debug-artifacts) does. The other half of
that flag is not this command's to give: `-ldflags` belongs to the compiler line
you write, so a debug artifact from here is `pw generate --debug` followed by a
`go build` you did not ask to strip.

### `--code-only`

`--code-only` writes the `_pw_gen.go` files and nothing else. It is for an inner
loop and for an editor task — the generated Go is what a diagnostic points into,
and waiting for a minified stylesheet to see a template error is time nobody
wants to spend.

Do not hand its output to a compiler. `public.go` names `dist/public` in a
`go:embed` directive, so a tree generated with this flag fails to compile on a
directory that was never built, and a project with Tailwind is also missing its
stylesheet — which fails later and more quietly: the pages render unstyled.

Step 4 still runs. The steps the flag skips are the ones that write files, and a
flag is not a way past a check that keeps an identity provider out of a binary.
`--debug` is refused alongside it, because source maps live in a tree this flag
does not build.

## When something else owns the compile step

Reach for `pw build` unless something else owns the compile step. Three cases do.

**A TinyGo build.** `pw build` always links with host `go`, so a TinyGo project
generates and then invokes its own compiler:

```sh
pw generate
tinygo build -scheduler=threads -o myapp ./cmd/myapp
```

**A `go build` you want to control.** Cross-compiling to an unusual target,
passing `-ldflags`, or building several binaries out of one tree are all reasons
to write the compiler line yourself:

```sh
pw generate
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o dist/myapp ./cmd/myapp
```

**An image builder that owns `go build`.** ko and Cloud Native Buildpacks both
compile the project themselves and will not run generation for you. Run this
first, in the working tree, and invoke the builder second.

[Container Images](/guides/deployment/container-images/) uses this command in
`Dockerfile.tinygo`, and explains why a Popcorn Web build has a host phase at
all.

## What it reads

Generation is scoped **per purpose**. Each purpose lists the directories it may
read, and reads nothing else:

```toml
[generate]
handlers = ["handlers"]
templates = ["handlers", "templates"]
queries = ["queries"]
config = ["cmd/myapp"]
pages = ["pages"]
dynamo = []
firestore = ["entities"]
```

| Purpose | Reads | Generates |
| --- | --- | --- |
| `handlers` | route registrations, `pw.Parse` and response calls in Go | request binding, JSON codecs, the OpenAPI fragment |
| `templates` | `.pw.html` | typed renderers; also where the document shell and error pages are found |
| `queries` | `.pw.sql` | context-based query functions |
| `config` | `pw.RegisterConfig` and `pw.RegisterSubCommand` calls in Go | configuration and subcommand binding |
| `pages` | page-tree roots | route registration and page parameters for discovered routing |
| `dynamo` | `dynamo`-tagged Go types and `.pw.dynamo` | record codecs, keys, and typed DynamoDB queries |
| `firestore` | `firestore`-tagged Go types and `.pw.firestore` | entity codecs, keys, and typed Datastore-mode queries |

A directory may appear under several purposes — `handlers` usually appears under
both `handlers` and `templates`, because a page template lives beside the
handler that renders it. Each listed directory is walked recursively, so nested
packages need no entry of their own.

The original four keys—`handlers`, `templates`, `queries`, and `config`—are
required and have no default. `pages`, `dynamo`, and `firestore` are optional
for compatibility with older projects. An empty list is still the clearest way
to state that a project deliberately generates nothing for a purpose:

```toml
queries = []   # this project has no .pw.sql
```

That distinction matters: a forgotten key is an error, while `[]` is a decision
the next reader can see.

A source outside the purpose that owns it is reported and skipped rather than
failing the build, so deliberate samples and fixtures can live beside your code:

```
pw: samples/home.pw.html is outside generate.templates and is not generated from; list its directory to include it
```

Go sources are never reported — ordinary Go code lives throughout a project, and
a call site outside its purpose simply gets no generated binding. A `_pw_gen.go`
left outside every purpose by an earlier layout **is** reported, since nothing
regenerates or removes it any more.

Neither report looks inside another checkout of the repository. A worktree made
by `git worktree add` below the project root — the kind coding agents keep under
`.claude/worktrees` — holds a full copy of the project, and its sources and
generated files answer to its own `popcornweb.toml`, not to this one. Judged
from here, every file in it would look like one nobody listed. A submodule is
part of the project and is reported like any other directory.

Besides declaration files, generation reads Go source for call sites:

| Call | Generates |
| --- | --- |
| `pw.Parse[T]` | request binding for `T` |
| `pw.WriteAPI[T]` | JSON encoding for `T` |
| `pw.WriteStream[T]` | stream encoding for `T` |
| `pw.RegisterConfig[T]` | configuration binding for `T` |
| `pw.RegisterSubCommand[T]` | subcommand parsing for `T` |
| `pw.BadRequest` and the other error constructors | the documented error responses |

The same evidence feeds one OpenAPI 3.1 fragment per package. Those fragments
are merged deterministically at build time, so the API description follows the
code rather than a separate annotation set.

## What it writes

Generated Go is build output, not source:

- filenames are `{source-base}_pw_gen.go`, always beside the source;
- they are excluded from version control by the scaffolded `.gitignore`;
- `.vscode/settings.json` hides them from the editor;
- they are recreated on every application build.

They can always be reproduced. Do not edit or commit them.

### The route table

`pw generate` also writes `dist/routes.json`: every pattern your application
serves, in one file. It holds the routes registered in Go — with the file and
line each was registered at — and the routes your page trees produce, which is
both halves of the URL space that
[the page tree](/tutorial/page-tree/) and handler registration keep in two
different places.

Nothing reads it at runtime. It exists so tooling does not have to run the
analysis again: [`pw doctor`](/pw/project/doctor/) checks it for patterns that
collide, and the editor extension lists it. Like `dist/manifest.json` it is a
build product, ignored by the scaffolded `.gitignore`, and regenerated on every
run — a project scaffolded before this existed needs the ignore line added.

A registration whose pattern is not a compile-time constant cannot be read, so
it is recorded as unresolved rather than dropped, and the tools that read the
table say so instead of reporting a clean route section they cannot back up.

### Pointing errors back at the template

Because the generated file is output nobody wrote, an error inside it names a
file you cannot bookmark and did not open. `generate.line_directives` in
`popcornweb.toml` fixes that: the generator writes Go `//line` directives, and
every tool that reads Go positions follows at once — the compiler, `go vet`,
the debugger, gopls, and your editor.

```toml
[generate]
line_directives = true
```

A type error in a template expression then reads:

```
./queries/users.pw.sql:8: invalid operation: mismatched types untyped int and untyped string
```

instead of naming `users_pw_gen.go`. A panic inside a generated `.pw.sql`
function names the `.pw.sql` in its stack frame too.

It is off by default, and there are two reasons to leave it off.

**It costs `go test -cover`.** With directives on, the coverage profile keeps
the generated file's path and writes the mapped line numbers, so it reports
lines that do not exist in the file it names — and `go tool cover -html` paints
the wrong lines and exits zero rather than complaining. Take template positions
or take coverage; a project cannot have both.

**`.pw.html` gets only half of it.** Compile-time errors map for every dialect.
Runtime stack frames map only for `.pw.sql`, because a `.pw.html` compiles to a
render plan that the shared runtime walks: the failing frame is inside that
runtime, not inside generated code, and no directive on a generated file can
move it.

The setting is per project rather than a flag, because generated output must
not depend on who ran it — [`pw check`](/pw/project/check/) compares the tree
against a fresh generation, and a flag one machine passed and another did not
would report drift on every one of them.

`cmd/<name>/popcornweb_bootstrap_pw_gen.go` is the exception in kind rather
than in rule: it is a generated file of blank imports that links the document
shell and the embedded public assets into the binary, so no handler has to
reference them. It is removed automatically when neither exists.

`dist/public` is created even when a project has no public asset at all, because
`public.go` names the directory in a `go:embed` directive and the compiler reads
the directive rather than the tree.

### CBOR API bodies

`generate.api.cbor` makes every generated binder and writer negotiate
`application/cbor` request and response bodies beside JSON:

```toml
[generate.api.cbor]
enabled = true
```

It is a generation-time, project-wide setting for the same reason
`line_directives` is: which media types a service accepts is a property of the
service, and output must not depend on who ran the command. The two profile
keys beside it, `reject_floats` and `sorted_keys`, are part of the generation
fingerprint, so changing either regenerates the codecs and shows up in
[`pw check`](/pw/project/check/) until committed. What the switch changes on
the wire, and when to leave it off, is the [CBOR guide](/guides/backend/cbor/)'s
subject.

## The single-document rule

A project has exactly one `document.pw.html`. If generation finds more anywhere
in the tree it fails with:

```
pw: multiple default documents: templates/document.pw.html, admin/document.pw.html
```

Alternative shells are ordinary exported components with an unnamed slot,
selected per handler with `pw.WriteHTMLChain`. See
[Templates](/guides/frontend/templates/).

## In a package project

A [component package](/guides/deployment/package/) has no entry
point, no `public.go`, and no document shell, so steps 2 through 4 have nothing
to act on and the command stops after the generated Go. That is the same result
`--code-only` gives, chosen by `project.kind` rather than by a flag the author
has to remember.

This is the one command in the build group a package project accepts.
[`pw build`](/pw/project/build/) and [`pw dev`](/pw/project/dev/) both refuse it,
because there is nothing to run.

## In CI

Verify that generated code is current, then generate and compile:

```sh
pw check
pw generate
go build ./cmd/myapp
```

[`pw check`](/pw/project/check/) writes nothing and fails on stale output.
Because Git ignores generated Go, a repository diff cannot reveal it.

Both [`pw dev`](/pw/project/dev/) and [`pw build`](/pw/project/build/) generate
first, so a direct `pw generate` is for a compile you drive yourself.
