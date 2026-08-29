---
title: Health and Readiness
description: The liveness, readiness, OpenAPI, and API-catalog endpoints the primary listener can expose, and why none of them has a default path.
sidebar:
  order: 1
---

An orchestrator needs two different questions answered. *Is this process alive,
or should it be killed and replaced?* And *can this process take traffic right
now?* They have different answers during a rolling deploy, which is why they are
two endpoints:

```toml
[server]
health = "/healthz"
readiness = "/readyz"
```

Neither key has a default. An application that answers on `/healthz` should say
so where an operator reading its configuration will see it — a default would
leave endpoints running that no file in the repository mentions. An unset key
registers no route at all.

`pw init` writes both into `config.dev.toml`, so a scaffolded project has them
in development and states them explicitly for every other environment.

## What each one answers

**`health` is liveness.** It checks nothing and depends on nothing. If the
process can accept a connection and run a handler, it answers `200`. That is the
correct definition: a dependency outage is not a reason to kill and restart your
application, and a liveness probe that fails when the database is down turns one
outage into a restart loop.

**`readiness` pings every configured database pool** — each connection in the
group set, or the single pool — with a one-second bound over the whole check.
Any pool that does not answer makes the endpoint `503`, and the orchestrator
stops routing to this instance until it recovers.

Both respond identically otherwise:

| | |
| --- | --- |
| Methods | `GET` and `HEAD`; anything else is `405` with `Allow` |
| Success | `200`, body `ok` |
| Failure | `503`, body `unavailable` |
| Content-Type | `text/plain; charset=utf-8` |
| Cache-Control | `no-store` |

The body is two words on purpose. These endpoints bypass session and
authentication, so they are reachable by anything that can reach the port, and
they reveal only status — never a DSN, a backend name, a stack trace, or a
configuration value. A probe that leaks the shape of your infrastructure to an
unauthenticated caller is a worse problem than the one it was added to detect.

## Probing from a shell-less container

Docker's `HEALTHCHECK` runs a command inside the container, and the command
everyone reaches for is `curl`. A distroless or scratch image — the right way to
ship a Go binary — has no curl and no shell to run it with, which leaves the
instruction nothing to call. The application binary fills that role itself:

```dockerfile
HEALTHCHECK CMD ["/myapp", "healthcheck"]
```

The subcommand reads the same configuration sources as the server — the TOML
file, the environment, `PORT` — so the port and the `health` path are never
repeated in the Dockerfile. It issues one `GET` against the loopback and exits
`0` on a `2xx` answer. Anything else — another status, a refused connection, a
timeout — exits `1`, which Docker counts as unhealthy. Exit code `2` is never
used; Docker reserves it.

Two options adjust the probe. `--ready` targets the `readiness` path instead,
which makes the verdict include the database pools — appropriate where a
dependent service should wait for a database-ready instance, and too strict for
a restart policy, where a database outage would turn into a restart loop.
`--timeout` bounds the whole probe and defaults to `3s`, safely inside Docker's
own 30-second default, so a hung listener is reported as unhealthy rather than
the probe being killed and reported as nothing.

The probe needs `server.health` (or `server.readiness` under `--ready`) set in
the environment's configuration, and a fixed `server.port`; an unset path or
port `0` fails with a message naming the key. The subcommand name is reserved —
[`pw.RegisterSubCommand`](/guides/architecture/custom-commands/) panics at
startup if an application claims `healthcheck` for itself.

### Dockerfile

The probe is the binary the image already ships, so the instruction stays in
exec form and never asks for a shell:

```dockerfile
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
  CMD ["/app/myapp", "healthcheck"]
```

Docker's `--timeout=5s` is its patience with the whole command; the probe's own
default of `3s` finishes inside it, so the verdict is always the probe's exit
code, never a kill. The environment's configuration must set `server.health` —
a missing key fails the probe with a message naming it, so the misconfiguration
surfaces on the first interval instead of never.

`pw init` writes this line into the project's Dockerfile already, along with the
`config.prod.toml` that sets the key.
[Container Images](/guides/deployment/container-images/) walks through the rest
of that file, including why a Popcorn Web image cannot be built with `COPY` and
`go build`.

### Compose

Compose takes the same exec form in `healthcheck.test`, and this is where
`--ready` earns its place: `depends_on` with `service_healthy` holds a
dependent service back, and a gate that means "the databases behind it answer"
is the readiness path, not liveness:

```yaml
services:
  app:
    image: myapp:latest
    environment:
      PORT: "8080"
    healthcheck:
      test: ["CMD", "/myapp", "healthcheck", "--ready"]
      interval: 10s
      timeout: 5s
      retries: 3
      start_period: 10s
  importer:
    image: myapp:latest
    command: ["import", "/data/users.csv"]
    depends_on:
      app:
        condition: service_healthy
```

A `healthcheck` declared here overrides the image's `HEALTHCHECK` line, so the
image can keep the plain liveness probe for `docker run` while Compose asks the
stricter question its dependency graph needs.

### Kubernetes

Kubernetes ignores `HEALTHCHECK` entirely and probes over HTTP from outside
the container, so the subcommand stays out of the manifest — point the probes
at the endpoints themselves:

```yaml
containers:
  - name: app
    image: myapp:latest
    ports:
      - containerPort: 8080
    env:
      - name: APP_ENV
        value: prod
    livenessProbe:
      httpGet:
        path: /healthz
        port: 8080
    readinessProbe:
      httpGet:
        path: /readyz
        port: 8080
```

The split maps one to one: `livenessProbe` on `health` restarts the pod only
when the process itself has stopped answering, and `readinessProbe` on
`readiness` takes the pod out of the Service until its pools answer. An image
that carries a `HEALTHCHECK` line does no harm here — it is simply never run.

## The OpenAPI endpoint

```toml
[server]
openapi = "/openapi.json"
api_doc = "scalar"
api_doc_path = "/docs"
```

The same listener can serve the generated OpenAPI document and a UI over it.
Both follow the same rule as the probes: no default path, and an unset key
serves nothing. `api_doc` additionally requires `openapi` — a UI over a document
nobody serves has nothing to render.

Where they differ is access. The probes are answered above every extension,
which is what makes them unauthenticated by construction. The documentation
endpoints sit *below* the extension chain, so the session and the authentication
guard reach them exactly as they reach an application route:

```toml
[auth.protection]
include = ["/openapi.json", "/docs"]
```

Protection is opt-in, so an unlisted path stays public — but a test that
authenticates can now read the document the same way it reads its own routes,
and a closed deployment can put its API description behind a login without
moving it off the primary listener.

Setting it per environment is still worth doing. `pw init` writes
`api_doc = "scalar"` into `config.dev.toml` only, and the default is empty,
which keeps the reference private until a staging or production config
deliberately opts in. See [API Documentation](/productivity/api-documentation/),
and [`pw doctor`](/pw/project/doctor/), which reports an exposed documentation
endpoint as a readiness finding for a deployed environment.

## Announcing the API catalog

A client that has your origin still has to be told that the document is at
`/openapi.json` and not at `/openapi` or `/v1/openapi.json`. One key ends that
conversation:

```toml
[server]
openapi = "/openapi.json"
api_doc = "scalar"
api_catalog = true
api_catalog_origin = "https://api.example.com"
```

`/.well-known/api-catalog` now answers with an
[RFC 9727](https://www.rfc-editor.org/rfc/rfc9727.html) catalog naming the three
endpoints above: the document, the UI, and `health`. It takes no path of its own
because the standard fixes the location, and it is assembled entirely from keys
you have already set — so an endpoint that moves takes its catalog entry with it
and there is nothing to keep in step.

`api_catalog_origin` is optional. Leave it unset and the links are written
relative — `/openapi.json` rather than `https://api.example.com/openapi.json` —
which resolves against whatever URL the client fetched the catalog from. That is
correct for every client following the catalog, so the endpoint works with no
origin configured at all.

Name it anyway once something stores your catalog rather than just following it.
RFC 9264 asks for references that are not relative so that a link set outliving
the exchange that delivered it still resolves, and an aggregator holding your
catalog in a database has exactly that problem. `pw doctor` reports the unset key
outside development as PW0429, a note.

What the framework will not do is guess. The only origin it could infer is the
request's own `Host`, which the caller chose, and a guessed absolute URL is
durable and may name a host you do not own — so an unnamed origin publishes what
it knows instead.

Enabling the catalog with neither `openapi` nor `api_doc` set is a startup
error rather than a warning. The only links left would point at the probe and at
the catalog itself, which describes no API to anyone.

Leave it off for an application that serves pages. Handlers publish an API as a
side effect of existing, and a catalog announces one you chose to publish. Every
scaffolded `config.dev.toml` carries the key so the decision is visible beside
the endpoints it would link — `pw init` writes it `true` for the `api-server`
preset and `false` everywhere else.

## Collisions fail startup

Every operational path is validated against the application's routes before the
listener opens. A route that collides with an enabled endpoint is a startup
error, not a silent shadowing — you find out at deploy time rather than from a
probe that has been reporting on the wrong handler.

## Shutting down

On `SIGINT` or `SIGTERM` the server stops accepting new connections and lets
in-flight requests finish, bounded by:

```toml
[server]
shutdown_timeout = "10s"
```

Runtime resources — database pools and anything else registered for cleanup —
are closed after that, under the same bound.

Requests still in flight when the timeout expires are cut off. Size the value
against your slowest ordinary request, and remember that a
[stream](/guides/frontend/streams/) is designed to stay open far longer than
that: a long-lived stream will be terminated by shutdown, and the client is
expected to reconnect.

The readiness endpoint does not start failing when shutdown begins. Draining is
the orchestrator's job — it stops sending traffic, then sends the signal — and
by the time the signal arrives, the listener is already closing.
