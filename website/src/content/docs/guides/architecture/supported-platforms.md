---
title: Supported Platforms
description: Every place a Popcorn Web application runs today, with the build option that produces each artifact and what each host takes away.
sidebar:
  order: 1
---

One application, written once against `net/http`, ships as a native binary, a
container image, a function bundle for four providers, or a Wasm module for a
Cloudflare Worker. The source does not change between them. What changes is one
`pw build` flag or one table in `popcornweb.toml`, and, less visibly, what the
host lets the application keep: a file, a socket, a process that outlives the
request. This page lays the set side by side so the choice is made on those
constraints rather than on the deployment command, which is the easy part.

## The set

| Platform | Produce it with | Compiler | Notes |
| --- | --- | --- | --- |
| Native binary, any OS Go targets | `pw build` | host Go | binary in the project root; `GOOS`/`GOARCH` pass through to `go build` |
| Native binary, small | `pw generate` then `tinygo build -scheduler=threads ./cmd/myapp` | TinyGo 0.42 | `project.toolchain = "tinygo"`; less than half the stripped host size |
| Container: Cloud Run services, App Runner, Container Apps, Kubernetes | `docker build` with the scaffolded `Dockerfile` or `Dockerfile.tinygo` | either | the host sets `PORT`; distroless runtime |
| AWS Lambda | `pw build --target lambda` | host Go | Linux binary plus a Dockerfile pinned to the Lambda Web Adapter |
| Azure Functions | `pw build --target azure-functions` | host Go | Linux binary plus `host.json`, a custom handler that forwards HTTP |
| Google Cloud Run functions | `pw build --target google-cloud-run-functions` | host Go, remotely | staged, vendored source with a Functions Framework registration |
| Vercel Go | `pw build --target vercel-go` | host Go, remotely | staged, vendored source with one `api/` handler |
| Cloudflare Workers | `pw build --target cloudflare-workers` | TinyGo or host Go, per `[deploy.cloudflare]` | Wasm module, framework-owned loader, `wrangler.jsonc` |

Two axes cut across the rows. `--backend fasthttp` compiles the same source
against fasthttp for every row but the last, and needs `project.fasthttp = true`
so that generation writes the second half; the Worker target builds `nethttp`
only. The `--target` outputs land under `.pw/build/<target>/<backend>/` beside a
`deployment.json` naming what was built, and the process and source targets
require `config.prod.toml` to exist. `pw dev` is unaffected by every option
here: it runs the host process on the loopback whatever the project will ship
as.

## Which one to take

The default is the first row in a container, and it is right far more often
than the table suggests. A host Go binary behind an assigned `PORT` keeps every
feature of the framework — file-backed SQLite, an in-process memo store, graceful
shutdown, migrations in process — and the scaffolded `Dockerfile` already builds
it. Nothing below is an upgrade on that; each is a way to run where a process
behind a port is not on offer, and each pays for it.

Take TinyGo when the artifact's size is the constraint: an image measured in
megabytes, an edge runtime with an upload limit, a device. The
[performance guide](/guides/architecture/performance/) has the numbers. The
price is two behaviours that no application code can fix — `SIGTERM` is never
delivered, so a stop is a kill after the grace period, and migrations shell out
to `pw` rather than running in process — and a build that compiles for the
machine it runs on. Under TinyGo, an engine that speaks a network protocol needs
`-scheduler=threads`, and forgetting it is a compile error rather than a silent
one.

Take a function host when the organisation already runs one and the application
is a web service that fits its invocation model. Lambda and Azure Functions
start the unchanged binary and forward HTTP to it; Cloud Run functions and
Vercel build the staged source themselves. The
[serverless guide](/guides/deployment/serverless/) walks each contract. Expect
a cold start, sometimes a buffered response, and an instance that is frozen
between requests rather than stopped.

Take Cloudflare Workers when the application should run at the edge and can live
with a program that starts per request. TinyGo keeps the module inside the free
plan's upload limit; host Go builds in seconds but needs a paid plan. The
constraints are the sharpest of the set and are described next.

## What each host takes away

The framework's stores each need something from the host, and the table below
is what decides whether a configuration is correct where it is deployed. A row
that reads "refused" is checked at startup, and on the Worker target also at
build, so a mismatch is an error with the alternative named rather than a store
that silently does nothing.

| Capability | Process and container | Function hosts | Cloudflare Workers |
| --- | --- | --- | --- |
| SQL database | sqlite file, PostgreSQL, MySQL | PostgreSQL, MySQL; a sqlite file lives only as long as the instance's disk | D1 through `d1://BINDING`, SQLite dialect; others refused |
| Session backend | rdb, cookie, redis, dynamo, firestore | same | cookie, or rdb over D1; development backends refused |
| Rate limiter | memory on one replica, redis shared | redis | `cloudflarekv`; memory refused |
| Memo store | yes | yes, per instance | refused until a Cache API backend exists |
| Object storage | local directory, s3 | s3 | r2 binding; local and s3 refused |
| External public tree | directory beside the binary | copied into the stage | an R2 bucket named in `[deploy.cloudflare.r2]` |
| Graceful shutdown | yes; not under TinyGo | provider-owned | none; the program ends with the request |
| Migrations | in process, or `pw migrate` | `pw migrate` against the database | `wrangler d1 migrations apply` on the staged files |

The Worker column follows from one fact: the host instantiates the module and
runs `main` for every request, so nothing kept in the process survives, and a
Worker owns neither a filesystem nor a socket. That is why the database is a
binding, the files are a bucket, the counter is a namespace, and the startup
summary is off there by default. It is also why a handler must finish reading
its request body before its first write.

## What is not a platform here

A Wasm module for a WASI HTTP host — Fastly Compute, or a component-model
runtime — builds with `tinygo build -target=wasip1`, and the application runs
from it, but no HTTP adapter for that ABI ships yet. Provider event functions,
where the trigger is not HTTP, are outside the set; DigitalOcean Functions and
the non-HTTP triggers of every provider above are in that group. So is any
platform reached by turning `main` into a provider-specific handler: every row
above keeps the application's `main` or transforms it mechanically, and a target
that would need the author to adopt a provider type is not added.

The [`pw build`](/pw/project/build/) reference has every flag, the
[build tags](/reference/build-tags/) page the tags each toolchain sets, and the
[container guide](/guides/deployment/container-images/) the two Dockerfiles in
full.
