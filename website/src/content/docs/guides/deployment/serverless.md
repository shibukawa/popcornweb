---
title: Serverless Hosting
description: Which scale-to-zero and function runtimes a Popcorn Web application can use, and where an HTTP adapter is required.
sidebar:
  order: 3
---

“Serverless” describes several incompatible startup models. The useful question
is whether the host starts an HTTP process, asks for an exported handler, or
delivers a provider-specific event. Popcorn Web supports the first model and
HTTP adapters to it without changing application code.

| Host shape | Examples | Status |
| --- | --- | --- |
| HTTP container with an assigned `PORT` | Cloud Run services, AWS App Runner, Azure Container Apps | supported by the normal Dockerfile |
| Invocation-to-HTTP adapter | AWS Lambda Web Adapter | supported; add the adapter to the deployment |
| HTTP-forwarding custom handler | Azure Functions | supported for HTTP-only functions |
| Exported Go handler, remotely built | Vercel Go, Cloud Run functions | supported by generated source staging |
| Provider event function | DigitalOcean Functions and non-HTTP triggers | not supported |
| Fetch-event Wasm | Cloudflare Workers | supported by a generated Worker module, TinyGo or host Go |
| Component-model Wasm | Fastly Compute and WASI HTTP hosts | not supported |

Container services are not a separate runtime. They start the scaffolded image
and set `PORT`; [`pw.Run`](/reference/runtime/) already binds it. This includes
platforms that scale the container to zero. If a container host is available
to you, it is the one to take: every target below trades something — a cold
start, a buffered response, a per-request lifetime — for running where the
application would not otherwise run, and none of those trades is worth making
for a service that could simply be a process behind a port. The
[supported platforms](/guides/architecture/supported-platforms/) lays the whole set side
by side.

Builds have two independent axes. `--target` selects the deployment host and
`--backend` selects `nethttp` or `fasthttp`; `pw dev` remains unchanged.

```shell
pw build --target=lambda --backend=nethttp
pw build --target=azure-functions --backend=fasthttp
pw build --target=google-cloud-run-functions --backend=nethttp
pw build --target=vercel-go --backend=fasthttp
pw build --target=cloudflare-workers
```

Every result is written under `.pw/build/<target>/<backend>/` with a
`deployment.json` manifest. `config.prod.toml` is required by the process and
source targets. A fasthttp build also requires `project.fasthttp = true`; the
Cloudflare target builds `nethttp` only.

## AWS Lambda

Use the [AWS Lambda Web Adapter](https://github.com/aws/aws-lambda-web-adapter)
instead of changing `main` into a Lambda event handler. For an image deployment,
copy the adapter into the Lambda extensions directory in the runtime stage:

```dockerfile
COPY --from=public.ecr.aws/awsguru/aws-lambda-adapter:1.0.1 \
  /lambda-adapter /opt/extensions/lambda-adapter
```

The application keeps its normal entry point. The adapter forwards requests to
`AWS_LWA_PORT`, then `PORT`, then `8080`; Popcorn Web follows the same order for
its listener. The generated directory contains a Linux `bootstrap`,
`config.prod.toml`, and a Dockerfile pinned to the adapter version. It sets
`APP_ENV=prod` and is the Docker build context.

This intentionally does not embed a Lambda Runtime API client in the framework.
The adapter supports Function URLs, API Gateway, ALB, buffered responses, and
response streaming while leaving one portable image usable outside Lambda.

## Azure Functions

Run the binary as a custom handler and enable HTTP request forwarding. The host
publishes the assigned listener as `FUNCTIONS_CUSTOMHANDLER_PORT`; `pw.Run`
recognizes it automatically.

```json
{
  "version": "2.0",
  "customHandler": {
    "description": { "defaultExecutablePath": "run.sh" },
    "enableProxyingHttpRequest": true
  }
}
```

The generated directory contains the Linux handler, `run.sh`, `host.json`, and
the catch-all `http/function.json`. Upload that directory with Azure Functions
Core Tools or your infrastructure workflow. Queue triggers and
extra input/output bindings use Azure's custom payload, not ordinary HTTP, and
are outside this adapter-free path. Azure also cautions that Functions is not a
general reverse proxy; for a full web application, Container Apps or App Service
usually has fewer routing and cold-start constraints.

## Vercel Go and Cloud Run functions

Vercel's Go runtime requires a `.go` file under `api/` exporting an
`http.HandlerFunc`. Cloud Run functions requires registration with the Go
Functions Framework. Both remote-build source rather than starting the
application's configured `main`, so a port alias cannot support them.

`pw build` copies the application module into an isolated source tree and
transforms the selected `main` into an initialization function. Vercel receives
`api/Handler`; Cloud Run functions receives the `PopcornWeb` Functions
Framework registration. Initialization is guarded once per warm instance.

For `nethttp`, the generated handler uses `pw.Middlewares`. For `fasthttp`, it
uses `pwfast.Start` and the framework's in-memory HTTP/1 bridge so the provider
still receives the required `http.HandlerFunc`. The staged source is formatted,
its module is tidied and vendored, and its provider package is compiled from
that vendor tree before the build is reported ready. Deploy the generated
directory, not the application checkout.

## Cloudflare Workers

A Worker runs a Wasm module behind a fetch event rather than a process behind a
port, so this target compiles the application to Wasm and ships the JavaScript
that loads it. Nothing in the application changes: `pw build` copies the module
into an isolated tree, turns `main` into an initialization function the same way
the source targets do, and adds an entry point that hands the middleware chain
to the [syumai/workers](https://github.com/syumai/workers) adapter.

The compiler is a project setting rather than a flag, because it decides what
the deployed artifact is. It defaults to `project.toolchain`; the other two
keys default to the project name and to a date pinned by the `pw` release. A
project scaffolded for host Go routes through the standard `ServeMux`, whose
method patterns TinyGo does not match, so `compiler = "tinygo"` is refused
there; a TinyGo project may choose either compiler.

```toml
[deploy.cloudflare]
compiler = "tinygo"              # or "go"
name = "myapp"                   # the Worker name in wrangler.jsonc
compatibility_date = "2025-08-01"
```

TinyGo produces a module of a few megabytes that fits the free plan; host Go
produces one several times larger that needs a paid plan, and starts faster to
build. Both pass the same conformance checks. The stage holds `build/app.wasm`,
the loader `build/wasm_exec.js` and `build/worker.mjs` that the framework owns
and pins to the compiler version, and a `wrangler.jsonc` naming them, so the
whole directory is what `wrangler dev` and `wrangler deploy` read:

```shell
pw build --target=cloudflare-workers
cd .pw/build/cloudflare-workers/nethttp
npx wrangler dev
```

Three things follow from the host rather than from the framework. A Worker has
no filesystem, so `config.prod.toml` is not read there; instead the build
flattens every scalar in it into `vars` in `wrangler.jsonc` under the same
environment names a container would use, and a `${NAME}` reference is resolved
from a `wrangler secret` of that name. The `[[middleware.rdb.connections]]`
array travels too, as JSON in the single `MIDDLEWARE_RDB_CONNECTIONS`
variable, which any host may set; every other array of tables has no
environment form and is reported by name rather than carried. The external public directory cannot be served, so
only the embedded tree is; and the host instantiates the module and runs `main`
for every request, which is why the startup summary is off under this target
unless `observability.boot_log` sets a format. Each request pays the
framework's initialization, a few milliseconds, and no process state such as a
memo store survives from one request to the next. A handler must also finish
reading its request body before the first write, because the adapter hands the
response to the host on that write and the host then refuses the body.

Because of that per-request lifetime, a configuration that keeps state in the
process is refused rather than silently emptied on every request: an enabled
memo store, a `dev-volatile` or `dev-persist` session backend, a memory-backed
rate limiter, and any `middleware.rdb` connection other than a `d1://` binding.
The build reports them from `config.prod.toml` before compiling, and the Worker
reports them again at startup if a wrangler var reintroduces one, each with the
backend to use instead.

### D1 as the database

A Worker reaches [D1](https://developers.cloudflare.com/d1/) through a
binding, and D1 is SQLite, so a project that develops on SQLite deploys to D1
with one line: the production connection names the binding instead of a file.

```toml
# config.prod.toml
[[middleware.rdb.connections]]
group = "default"
dsn = "d1://DB"
```

`project.database` stays `sqlite`, the queries, migrations, session store and
auth state are the SQLite ones already, and `pw dev` keeps running on the
local file. The build links the D1 engine in place of the SQLite driver, which
cannot be compiled to Wasm, writes a `d1_databases` entry for every binding the
connections name, and stages the up half of each migration for Wrangler:

```toml
# popcornweb.toml — the database behind the binding; the id is what
# wrangler deploy needs and wrangler dev does not
[[deploy.cloudflare.d1]]
binding = "DB"
database_name = "myapp"
database_id = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
```

```shell
cd .pw/build/cloudflare-workers/nethttp
npx wrangler d1 migrations apply myapp --local   # or --remote
npx wrangler dev
```

The D1 driver has no transactions, so `pw.Transaction` and
`auto_transaction` fail on a D1 connection; write one statement at a time,
which is how the framework's own stores already work. There is no connection
pool either, and the pool settings have no effect.

### R2 for the external public tree

The Worker has no directory beside it, so the
[external public tree](/guides/frontend/static-assets/#when-a-file-should-not-be-in-the-binary) is served from an R2
bucket instead. Name the bucket binding and the build does the rest: the
generated entry reads the tree from the bucket, `wrangler.jsonc` declares the
binding, and the stage holds a copy of the tree with a script that uploads
every file under its URL path with its media type.

```toml
# popcornweb.toml
[deploy.cloudflare.r2]
binding = "ASSETS"
bucket_name = "myapp-assets"
```

```shell
cd .pw/build/cloudflare-workers/nethttp
sh r2-upload.sh --local    # seed wrangler dev's local bucket
sh r2-upload.sh --remote   # the deployed bucket
```

The mount answers with the bucket's ETag, `304` on a match, and `206` for a
Range request. Each object is read whole per request, so the tree is for the
assets that are too large to embed and not so large that a Worker cannot hold
one in memory. An application's own files go through the
[storage interface](/guides/storage/object-storage/) with `backend = "r2"`
and a binding; the build declares every such binding in `wrangler.jsonc` and
refuses the `local` and `s3` backends, which a Worker cannot reach.

### KV for the rate limiter

A memory-backed rate limiter counts nothing on a host that runs the process
per request, so a Worker counts in a
[KV namespace](https://developers.cloudflare.com/kv/) instead. The count is
an estimate: KV has no atomic increment and propagates in tens of seconds,
which a rate limit tolerates and a session does not, so sessions and auth
state stay on D1.

```toml
# config.prod.toml
[ratelimit]
enabled = true
backend = "cloudflarekv"

[ratelimit.cloudflarekv]
binding = "RATELIMIT"
```

```toml
# popcornweb.toml — the namespace behind the binding; wrangler dev runs on a
# placeholder id, wrangler deploy needs the real one
[[deploy.cloudflare.kv]]
binding = "RATELIMIT"
id = "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
```

A window shorter than a minute keeps its count for a minute, which is the
shortest expiration KV accepts. The memo store has no KV backend yet; a
Worker builds with `cache.enabled = false`.

## Runtime limits still apply

Function hosts may buffer responses, cap duration, freeze an idle instance, and
provide only ephemeral local storage. Configure `html.streaming = false` where
the ingress buffers, bound live responses below the provider duration, and use a
shared session or rate-limit backend whenever requests may land on different
instances.
