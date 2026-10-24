---
title: pw build
description: Produce a native or provider-targeted release artifact with generated code and built assets.
sidebar:
  order: 6
---

```sh
pw build [--debug] [--backend nethttp|fasthttp]
         [--target lambda|azure-functions|google-cloud-run-functions|vercel-go]
```

`pw build` turns the current project state into a release artifact. With no
target it produces the ordinary binary. `--backend` selects the HTTP
implementation and defaults to `nethttp`; `--target` selects provider
packaging.

## What it does

1. compiles templates, SQL, page trees, catalogs, and binding call sites into
   `_pw_gen.go` files beside their sources;
2. builds the Tailwind stylesheet **minified**, if Tailwind is enabled — this
   overrides `assets.tailwind.minify`, so a release is never accidentally
   unminified;
3. builds the [asset tree](/guides/frontend/static-assets/) into `dist/public`:
   it converts what the project asked to convert, writes the `*.br`, `*.zstd`
   and `*.gz` sidecars, and emits the manifest that decides every cache header;
4. rejects the build if `project.main` depends on a development-only package;
5. runs `go build` on `project.main` from `popcornweb.toml`.

Steps 1 through 4 are exactly [`pw generate`](/pw/project/generate/). This
command is defined as that one plus the compiler, so the two cannot drift apart
in content or in order.

The binary lands in the project root, named after the main package. The
scaffolded `.gitignore` already excludes it, along with everything under
`dist/` — the built tree, the conversion cache, and the manifest are all build
output. [`build.output`](/reference/build-configuration/#build) names another
path, for a project whose root already holds a directory of that name; without
it such a build stops before generating and says so.

With `--target`, the result instead lands under
`.pw/build/<target>/<backend>/`. Lambda and Azure Functions receive a Linux
binary plus provider metadata; Google Cloud Run functions and Vercel Go receive
a locally compiled, vendored source tree. See [Serverless Hosting](/guides/deployment/serverless/)
for each artifact contract.

Today, `contrib/devidp` is the only development-only package. It is the identity
provider used by [`pw dev`](/pw/project/dev/), and it signs users in without
checking a password. Linking that behavior into a deployable binary is a build
defect, not a production setting, so `pw build` stops and names the importing
package.

## Debug artifacts

```sh
pw build --debug
```

`--debug` keeps the debug information a deployable artifact otherwise drops: the
source map the script build emits, and the DWARF and symbol table that
`-ldflags=-s -w` removes. Nothing else about the build changes.

Reach for it when a shared test or CD deployment is being debugged by more than
one person. Do not reach for it for staging, which exists to rehearse
production — an artifact that differs from the production one rehearses nothing.

Without it the map is absent, and the bundle carries no `sourceMappingURL`
comment either, because a bundle naming a map the tree does not hold turns every
devtools open into a request for a file that is not there. The two shapes give
the bundle the same hashed name, so the URL a page loads does not depend on which
one produced it. Panic stacks still carry function names and line numbers in both:
`pw build` retains Go's pclntab either way.

`--debug` brings back nothing from [`pw dev`](/pw/project/dev/). The error
overlay, the launcher, and the development identity provider are absent from a
`pw build` artifact of either shape, and step 4 above still refuses a build that
imports one.

## Running the result

```sh
APP_ENV=prod ./myapp
```

`APP_ENV` selects which project-local configuration file is read; see
[Application Configuration](/guides/architecture/configuration/).

## Cross-compiling and TinyGo

`pw build` shells out to `go build`, so the usual environment variables apply:

```sh
GOOS=linux GOARCH=amd64 pw build
```

The generated path uses no runtime reflection, so the same sources can target
TinyGo. `pw build` always links with host `go`, so a TinyGo build generates and
then invokes that compiler itself:

```sh
pw generate
tinygo build -scheduler=threads -o myapp ./cmd/myapp
```

[`pw generate`](/pw/project/generate/) is this command without its final step —
it leaves the whole tree a compiler needs, including the `dist/public` that
`public.go` names in a `go:embed` directive. Do not narrow it with
`--code-only` here; that flag writes the generated Go alone, and the compiler
then fails on a directory nothing built.

`-scheduler=threads` is required for any engine that speaks a network protocol.
Under the cooperative scheduler a blocking socket call holds the whole runtime,
so a driver's cancellation watcher never runs and a query outlives its context
deadline without reporting one. The `database/postgres` and `database/mysql`
packages refuse to compile without the flag rather than letting that happen at
run time.

[Container Images](/guides/deployment/container-images/) uses both commands in
the two Dockerfiles `pw init` writes.

TinyGo's `net` package has no networking implementation of its own; every socket
passes through a Netdever registered by the program. Projects scaffolded with
TinyGo support include a root `tinygohelper.go` for that registration:

```go
//go:build tinygo

package publicassets

import _ "github.com/shibukawa/tinygodriver/netdev"
```

The `//go:build tinygo` constraint keeps the file out of host Go builds. Without
it a TinyGo binary compiles fine and then exits at startup:

```
2026/01/01 00:00:00 Netdev not set
```

Only a project created with `--tinygo`, or with that wizard answer, gets the
file — it is not the default. Add it by hand before switching a project to
TinyGo.

## In CI

Verify that generated code is current before building:

```sh
pw check
pw build
```
