# Build targets, build tags, and serverless hosts

One application, several ways to ship it. The default — host Go on `net/http`,
`pw build` with no flags — is the one to take unless something below names the
situation. Two independent axes:

```sh
pw build [--debug] [--backend nethttp|fasthttp]
         [--target lambda|azure-functions|google-cloud-run-functions|vercel-go|cloudflare-workers]
```

`--backend` selects the HTTP implementation (default `nethttp`); `--target`
selects provider packaging. `pw dev` is unaffected by both.

## What `pw build` does

1. runs `pw generate`;
2. builds the Tailwind stylesheet **minified**, overriding
   `assets.tailwind.minify` so a release is never accidentally unminified;
3. builds the asset tree into `dist/public` — conversions, `*.br` / `*.zstd` /
   `*.gz` sidecars, and the manifest that decides every cache header;
4. rejects the build if `project.main` depends on a development-only package
   (`contrib/devidp`, which signs users in without a password);
5. runs `go build` on `project.main`.

The binary lands in the project root. With `--target` the result lands under
`.pw/build/<target>/<backend>/` with a `deployment.json` manifest, and
`config.prod.toml` is required.

`pw generate` is the same pipeline without step 5 — use it before invoking a
different compiler yourself. `--debug` keeps the script source map and the Go
symbol table that `-ldflags=-s -w` otherwise removes; take it for a shared test
deployment, never for staging, which exists to rehearse production.

## The fasthttp build

`pw build --backend fasthttp` compiles the same source against fasthttp. It is a
**second build, not a mode**: you write `net/http` handlers and call `pw`,
generation rewrites those handlers for the other transport and imports `pwfast`
under the name `pw`, build tags select which half compiles, and the binary links
no `pw` at all. It requires `project.fasthttp = true` in `popcornweb.toml` —
`pw init` writes it when the wizard question is taken.

What it buys is the allocation profile: 2 allocations per JSON response against
21, 16 against 39 for an HTML page. What that is worth depends on what else the
request does — a loopback socket takes the ratio from 2× to about 10%, and one
database query takes it to noise. **Do not switch expecting a page that talks to
a database to get faster.**

Three constraints it puts on application layout:

- **A build tag excludes a whole file, never a call.** A file holding a transport
  handler must hold nothing else — no type, const, or var declaration, because
  both builds need those and the tag would take them along. Put handlers in files
  of their own. The rewriter reports every authored file that mixes the two and
  names the declarations that would be lost.
- **An untagged file may name only packages every build links, and `pw` is not
  one of them.** A file with no tag importing `pw` puts the whole `net/http`
  runtime into the fasthttp binary. Reach for `pwruntime`, `pwconfig`,
  `pwsession`, `pwdatabase`, `pwobservability`, `pwextension`, `pwratelimit`, or
  `pwbrowser` instead — each publishes what `pw` re-exports. A live source is the
  common case: call `pwruntime.NewSignal`, not `pw.NewSignal`.
- **`net/http`-shaped third-party middleware does not survive.** Nothing wraps
  it, and one binary serves one transport — there is no incremental migration and
  no mixed process.

The rewriter is conservative: an occurrence it does not recognize is a generation
error naming the occurrence, its chain from the handler, and a remedy, rather
than a silent miscompile. `r.RemoteAddr` is the notable refusal — it has no
spelling the rewrite covers; read the client address through the framework
instead. A WebSocket callback must not touch `w` or `r` at all, because on this
build it runs after the handler has returned.

Both halves share one copy of the document shell, error page, reloadable
components, and resolved update configuration in `pwruntime`, so registration
from `init` reaches whichever runtime is linked.

## TinyGo

The generated path uses no runtime reflection, which is what makes TinyGo a
first-class target — chosen for **size**, not speed. `pw build` always links with
host `go`, so a TinyGo build runs the generation and then the compiler:

```sh
pw generate
tinygo build -scheduler=threads -o myapp ./cmd/myapp
```

Leave `--code-only` off: with it, generation writes the Go but not
`dist/public`, and `go:embed` fails on a tree that was never built.

`-scheduler=threads` is **required for every database engine that speaks a
network protocol**. Under the cooperative scheduler a blocking socket call holds
the whole runtime, so the driver's cancellation watcher never runs and a query
outlives its deadline silently. `database/postgres` and `database/mysql` refuse
to compile without it; the diagnostic is
`undefined: build_this_program_with_tinygo_scheduler_threads`.

TinyGo's `net` package has no networking of its own. TinyGo scaffolds include a
root `tinygohelper.go`:

```go
//go:build tinygo

package publicassets

import _ "github.com/shibukawa/tinygodriver/netdev"
```

Without it a TinyGo binary compiles and then exits at startup with
`Netdev not set`. Projects created `--no-tinygo` lack the file — add it before
switching. On TinyGo, `pw.NewServeMux` compiles in the framework's own ServeMux
with Go 1.22 pattern semantics, since TinyGo's does not support them, and
`pw.Run` serves through a listener that can hand a handler the connection, so a
WebSocket upgrade completes.

### Sizes

`examples/helloworld` on an Apple M3, which embeds SQLite — that is most of what
these numbers are:

| Build | net/http | fasthttp |
| --- | --- | --- |
| `go build` | 15.8 MiB | 15.5 MiB |
| `go build -ldflags="-s -w"` | 9.9 MiB | 9.6 MiB |
| `tinygo build` | 4.2 MiB | 5.6 MiB |
| `tinygo build -target=wasip1` | 7.6 MiB | 13.4 MiB |
| `tinygo build -target=wasip1 -no-debug` | 2.9 MiB | 3.8 MiB |

Read the rows against each other, not across. Under host Go fasthttp is
marginally smaller; under TinyGo it is 1.4 MiB *larger*, because the fasthttp
build is additive — `net/http` is still linked (the fork imports it) and brotli,
zlib, the router, the websocket upgrader and a SOCKS dialer come on top. Host
Go's linker discards most of that and TinyGo's keeps more.

**Always pass `-no-debug` for a WASI artifact.** A wasm module embeds its DWARF
as custom sections, so dropping it takes 62–72% off, and it is worth more than
every other choice combined. `-no-debug` earns nothing on macOS native builds
(the Mach-O carries no DWARF to remove). `-target=wasm`, the browser one, does
not build.

## Build tags

| Tag | What it selects | Who passes it |
| --- | --- | --- |
| `fasthttp` | the second build; `pwfast` replaces `pw` | `pw build --backend fasthttp` |
| `pwdev` | dev console, storybook, dev data, the `/_pw/test/*` endpoints | `pw dev` and `pw migrate` pass it to `go run` |
| `force_tinygo_logic` | the TinyGo code path under host Go, so it is testable | you, in tests |
| `tinybind_no_openapi` | keeps generated OpenAPI fragments out of the build | you |
| `fasthttp_nozstd` | (tinygodriver) drops klauspost zstd from the fork; saves ~40 KB | rarely needed since tinygodriver v1.2.4 |
| `nosqlite` | (tinygodriver) keeps the SQLite amalgamation from linking | you |
| `jwt_no_rsa` | (tinygodriver) drops RSA from the JWT package | you |
| `darwinstarttlswith13` | (tinygodriver) vendored mbedTLS for TLS 1.3 and client certs, at the cost of the OS trust policy | you |

`pw_nozstd` and `pw_nogzip` were **removed** in favour of
`middleware.compression`; passing either selects nothing. Tags the toolchain sets
and you never pass: `tinygo`, `gc` (set by TinyGo too — a dependency guarding its
pure-Go fallback on `!gc` selects assembly under TinyGo and fails to link),
`scheduler.threads`, `wasip2`, `illumos`, `appengine`.

TinyGo and fasthttp together need nothing beyond the target tag as of
tinygodriver v1.2.4:

```sh
tinygo build -tags fasthttp -scheduler=threads -o app ./cmd/app
```

## Serverless hosts

"Serverless" covers several incompatible startup models. The question is whether
the host starts an HTTP process, asks for an exported handler, or delivers a
provider-specific event.

| Host shape | Examples | Status |
| --- | --- | --- |
| HTTP container with an assigned `PORT` | Cloud Run services, App Runner, Container Apps | the scaffolded Dockerfile, no target needed |
| Invocation-to-HTTP adapter | AWS Lambda Web Adapter | `--target=lambda` |
| HTTP-forwarding custom handler | Azure Functions | `--target=azure-functions`, HTTP-only functions |
| Exported Go handler, remotely built | Vercel Go, Cloud Run functions | `--target=vercel-go`, `--target=google-cloud-run-functions` |
| Provider event function | DigitalOcean Functions, non-HTTP triggers | deferred |
| Fetch-event Wasm | Cloudflare Workers | `--target=cloudflare-workers`, `nethttp` only; compiler from `[deploy.cloudflare]`; a `d1://BINDING` connection in `config.prod.toml` uses D1 as the SQLite-dialect database, with `[[deploy.cloudflare.d1]]` naming the database; migrations are staged for `wrangler d1 migrations apply`; process-state settings (memo store, memory rate limit, dev session stores, non-d1 rdb) are refused at build and startup; `[deploy.cloudflare.r2]` serves `public-external` from a bucket (stage has `r2-upload.sh`); `ratelimit.backend = "cloudflarekv"` counts in a KV namespace named by `[[deploy.cloudflare.kv]]` |
| Component-model Wasm | Fastly Compute, WASI HTTP hosts | deferred |

Container services are not a separate runtime: they start the scaffolded image
and set `PORT`, which `pw.Run` already binds — including platforms that scale to
zero.

**AWS Lambda** uses the AWS Lambda Web Adapter rather than turning `main` into an
event handler, so one portable image stays usable outside Lambda. The generated
directory holds a Linux `bootstrap`, `config.prod.toml`, and a Dockerfile pinned
to the adapter version, and is the Docker build context. The adapter forwards to
`AWS_LWA_PORT`, then `PORT`, then `8080`; the framework follows the same order.

**Azure Functions** runs the binary as a custom handler with
`enableProxyingHttpRequest`. The host publishes `FUNCTIONS_CUSTOMHANDLER_PORT`,
which `pw.Run` recognizes. Queue triggers and extra bindings are outside this
path.

**Vercel Go and Cloud Run functions** remote-build source, so a port alias cannot
support them. `pw build` copies the module into an isolated tree and transforms
the selected `main` into an initialization function guarded once per warm
instance: Vercel receives `api/Handler`, Cloud Run functions the `PopcornWeb`
Functions Framework registration. `nethttp` uses `pw.Middlewares`; `fasthttp`
uses `pwfast.Start` and an in-memory HTTP/1 bridge. Deploy the generated
directory, not the application checkout.

**Runtime limits still apply.** Set `html.streaming = false` where the ingress
buffers, bound live responses below the provider's duration cap, and use a shared
session and rate-limit backend whenever requests may land on different instances.

## In CI

```sh
pw fmt --check
pw check
pw build
```

`pw check` writes nothing and exits non-zero listing stale files — necessary
because gitignored generated output cannot show staleness in a diff. Add
`pw build --backend fasthttp` for a project that declares the second build;
nothing else proves the rewritten half still compiles.

## Common mistakes

- Running `pw generate --code-only` and then `tinygo build` — `dist/public` does
  not exist and `go:embed` fails. Drop the flag.
- Dropping `-scheduler=threads` from a TinyGo build that links a network database
  driver.
- Importing `pw` from an untagged file in a project that declares
  `project.fasthttp`, or mixing a transport handler with type/const/var
  declarations in one file.
- Expecting a `pw build` artifact to carry the dev console, the launcher, or the
  identity provider. They are `pwdev`-tagged and absent; `--debug` brings none of
  them back.
- Building for fasthttp without `project.fasthttp = true`, or with `--target`
  and no `config.prod.toml`.
- Passing `pw_nozstd` or `pw_nogzip` and expecting an effect.
