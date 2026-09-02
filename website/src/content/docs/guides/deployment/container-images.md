---
title: Container Images
description: Why a Popcorn Web image cannot be built with COPY and go build, and what each line of the scaffolded Dockerfile is for.
sidebar:
  order: 2
---

Here is the Dockerfile almost every Go project starts from:

```dockerfile
FROM golang:1.27 AS build
COPY . .
RUN CGO_ENABLED=0 go build -o /out/myapp ./cmd/myapp
```

On a Popcorn Web project it fails, and the way it fails is unhelpful. The
compiler reports undefined symbols — a renderer your template declares, a query
function your `.pw.sql` names, the registration that wires your page tree —
and every one of them belongs to a file that is not in the repository.

Those files are build outputs. `pw generate` writes them beside their sources as
`_pw_gen.go`, `pw init` puts that pattern in `.gitignore`, and the same is true
of the CSS Tailwind compiles and the asset tree under `dist/` that `public.go`
embeds. A Popcorn Web build has a **host phase** before the compiler, and a
Dockerfile that goes straight to `go build` skips it.

`pw init` writes a Dockerfile that does not. This page explains it, so you can
change it rather than only run it.

## The scaffolded Dockerfile

```dockerfile
FROM golang:1.27-trixie AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

RUN GOBIN=/usr/local/bin go install \
      github.com/shibukawa/popcornweb/cmd/pw@$(go list -m -f '{{.Version}}' github.com/shibukawa/popcornweb)

COPY . .

RUN CGO_ENABLED=0 pw build

FROM gcr.io/distroless/static-debian13:nonroot
WORKDIR /app

COPY --from=build /src/myapp /app/myapp
COPY --from=build /src/config.prod.toml /app/config.prod.toml

ENV APP_ENV=prod
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
  CMD ["/app/myapp", "healthcheck"]

ENTRYPOINT ["/app/myapp"]
```

Build and run it:

```bash
docker build -t myapp .
```

The file the scaffold writes carries a comment on every one of these decisions.
The sections below are the reasoning behind them.

### `pw build` replaces `go build`

[`pw build`](/pw/project/build/) is the host phase and the compiler in one
command: it generates, compiles the stylesheet when Tailwind is enabled, builds
the derived asset tree, refuses the build if the entry point reaches a
development-only package, and then links. Running it inside the builder stage is
what makes the image reproducible from a clean checkout, with no host toolchain
and no generated file carried in.

`CGO_ENABLED=0` still matters, because it is what makes the binary static enough
for the runtime base below.

### pw is pinned to the framework, by the framework

pw generates code that a particular framework version reads, so the two have to
agree. Rather than write a version into the Dockerfile and let it drift, the
scaffold reads it back out of the module graph:

```dockerfile
RUN GOBIN=/usr/local/bin go install \
      github.com/shibukawa/popcornweb/cmd/pw@$(go list -m -f '{{.Version}}' github.com/shibukawa/popcornweb)
```

Bump the framework in `go.mod` and the builder follows. There is no second place
to update, which is the only version pin in this file that maintains itself.

### distroless, and why not scratch

`gcr.io/distroless/static-debian13:nonroot` ships CA certificates, time zone
data, an unprivileged user, and no shell. Scratch is smaller and wrong for most
applications: the first outbound HTTPS request — an OIDC discovery document, a
token exchange, any API call — fails at the TLS handshake for want of a
certificate pool, and the error names the peer rather than the missing file.

The `:nonroot` tag runs as uid 65532, which works because the listener is on
8080 rather than a privileged port. The image needs no writable filesystem
either: assets are served from the embedded tree, not from disk.

Both stages name their Debian release: `golang:1.27-trixie` in the builder,
`static-debian13` in the runtime. A bare `golang:1.27` rebases onto each new
Debian stable the day it releases, which changes your build environment on
Debian's schedule rather than yours — and leaves the two stages on different
releases until the distroless side catches up. When you move to a new Debian,
change both lines together.

### `WORKDIR` and `APP_ENV` are load-bearing

Project-local configuration resolves against the **process working directory**,
so `WORKDIR /app` and the file copied next to the binary are one decision, not
two. Move the binary without moving the file and the server starts on defaults
instead of failing — the quieter of the two mistakes, and the harder one to
notice.

`ENV APP_ENV=prod` is equally load-bearing. An unset `APP_ENV` resolves to
`dev`, so an image without this line looks for `config.dev.toml`, does not find
it, and comes up on defaults. See
[Application Configuration](/guides/architecture/configuration/) for the full resolution
order.

### The binary is its own probe

The image has no curl and no shell to run one with, so `HEALTHCHECK` calls the
application:

```dockerfile
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
  CMD ["/app/myapp", "healthcheck"]
```

The subcommand reads the same configuration the image already carries, which is
why no port and no path is repeated here. It needs `server.health` to be set —
an unset key exits `1` naming it — and `pw init` writes that key into
`config.prod.toml` for exactly this reason. [Health and
Readiness](/guides/deployment/operational-endpoints/) covers the probe itself,
`--ready`, and how Compose and Kubernetes ask the same question.

## What `.dockerignore` is protecting

```
**/*_pw_gen.go
dist/
config.dev.toml
*.db
.devbox/
```

The first two entries are the interesting ones. The image regenerates both, and
a host copy carried into the build context would at best be overwritten and at
worst be linked — a generated file from a source you deleted still compiles, and
its registrations still run.

Excluding `dist/` removes `dist/public/.keep`, the sentinel that lets `go:embed`
succeed on an empty tree. That is safe here because the asset build recreates
the directory before the compiler ever reads the embed directive.

`config.dev.toml` is excluded because it carries a local DSN and a keyring
secret generated for one machine. Neither belongs in a layer that anyone able to
pull the image can read.

## Configuration and secrets

`pw init` writes `config.prod.toml` beside `config.dev.toml`, and the Dockerfile
copies it. It sets the port, the health and readiness paths, and JSON log
output, and it carries no secret at all.

That last part is a constraint rather than a preference. An image layer is
readable by anyone who can pull the image, and deleting a file in a later layer
does not remove it from the earlier one. Values the deployment owns arrive two
ways instead. A `${NAME}` reference in the file is expanded when the file loads,
which is how the database connection is written:

```toml
[[middleware.rdb.connections]]
group = "default"
dsn = "${DATABASE_URL}"
```

An undefined name is a load error rather than an empty DSN, so a missing
variable stops the server instead of producing a pool that connects to nothing.
Other secrets have environment variables of their own —
`SESSION_KEYRING_SECRET`, `SESSION_COOKIE_SECRET`, `SESSION_REDIS_DSN`.

Configuration resolution takes the first readable candidate and stops; it does
not merge files. `config.prod.toml` is therefore a complete configuration, not a
set of overrides, and a scaffold cannot finish it for a project with sessions or
a login — those sections hold development endpoints and generated secrets that
must not be copied across. The scaffolded file names the sections it left out,
and the application prints the complete set for you:

```bash
APP_ENV=prod ./myapp --generate-config=toml
```

## Migrations are a separate step

Nothing in this image applies migrations. Automatic apply at startup is
disabled outside development on purpose: several instances starting at once
would race, and a forward-only apply during a rolling deploy is a schema change
nobody approved. Run [`pw migrate up`](/pw/database/migrate/) as its own step —
a Kubernetes Job, a release phase, a task run before the new revision takes
traffic — from an image or a runner that has pw and the `migrations/`
directory.

## Building with TinyGo

A TinyGo project gets a second file, `Dockerfile.tinygo`, and the two differ
only in the builder stage:

```dockerfile
FROM tinygo/tinygo:0.42.0 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

RUN GOBIN=/usr/local/bin go install \
      github.com/shibukawa/popcornweb/cmd/pw@$(go list -m -f '{{.Version}}' github.com/shibukawa/popcornweb)

COPY . .

RUN pw generate
RUN tinygo build -scheduler=threads -o /out/myapp ./cmd/myapp
```

```bash
docker build -f Dockerfile.tinygo -t myapp .
```

[`pw generate`](/pw/project/generate/) is `pw build` without the compiler: the
same generation, stylesheet, asset, and development-import steps, stopping before
the link. The line after it is the entire difference between the two files, which is
also why the compiler is written out rather than hidden behind a flag — it is
the line you change for an output path, a target, or an optimization level.

`-scheduler=threads` is required for any engine that speaks a network protocol.
Under the cooperative scheduler a blocking socket call holds the whole runtime,
so a driver's cancellation watcher never runs, and a query outlives its context
deadline and returns a `nil` error with nothing logged. It costs nothing for an
engine that opens no socket, so the scaffold passes it unconditionally. Remove
it while using PostgreSQL or MySQL and the build fails with a named identifier
rather than shipping the silent version.

The payoff is a much smaller binary. Two things about the image change with it,
and neither is fixable in application code.

**SIGTERM does nothing.** TinyGo's `os/signal` replaces the default disposition
and then delivers nothing to the channel, so the framework installs no handler
under that toolchain. `docker stop` and a pod deletion both send `SIGTERM`, wait
out the grace period, and reach `SIGKILL` every time. `shutdown_timeout` never
runs, and in-flight requests are cut rather than drained. Stop sending traffic
before the stop signal — a Kubernetes `preStop` sleep, or a load balancer that
drains first — and shorten the grace period, since waiting for an answer that
never comes only delays the kill.

**Migrations cannot run in process.** Under TinyGo the migration runner shells
out to `pw`, because the engine behind it is host-only and cannot be linked. A
distroless image carries no pw, so an in-process apply fails there with a
missing-binary error. In practice this changes nothing, because migrations
already belong in the separate step described above.

TinyGo also compiles for the machine running the build rather than reading
`GOOS` and `GOARCH`, so build on the target architecture or pass
`docker buildx --platform`.

:::note[Images without a Dockerfile]
[ko](https://ko.build/) and [Cloud Native Buildpacks](https://buildpacks.io/)
both build a Go image without a Dockerfile, and both replace the Dockerfile
rather than the host phase — they own the `go build` step and will not generate
for you. Run `pw generate` in the working tree first and then
invoke the builder; it works because both read the working directory rather
than the git index, so the generated files `.gitignore` excludes are present and
are used. A CI job that checks out and calls the builder directly gets the
undefined-symbol failure from the top of this page. Neither supports TinyGo, and
neither produces a Docker `HEALTHCHECK` — a platform probe against the
configured health path replaces it.
:::

## When to leave this file alone

The scaffolded Dockerfile is the recommended path, and for most projects the
only change it needs is the base image your organization standardizes on. Reach
for something else when your platform already builds every other service its own
way: a team running Buildpacks for a dozen services gains more from consistency
than from this file, provided the project is host Go and the host phase runs
first.

What does not vary is the host phase. Whatever builds the image, `pw generate`
or `pw build` runs before the compiler, or the compiler has nothing to read.

For every configuration key mentioned here, see the
[configuration reference](/reference/configuration/).
