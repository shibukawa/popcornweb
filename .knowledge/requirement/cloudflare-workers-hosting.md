---
id: requirement:cloudflare-workers-hosting
type: requirement
title: Cloudflare Workers Hosting
---
Popcorn Web targets Cloudflare Workers through a fetch-event adapter to net/http while keeping its application handler unchanged.

```yaml
priority: next candidate after the four targets of decision:serverless-target-scope
host_contract:
  input: Cloudflare fetch Request and bindings
  output: Cloudflare Response
  bridge: convert through an http.Handler produced by pw.Middlewares; no listener and no pw.Run
  packaging: Wasm module plus JavaScript entry loaded by Wrangler
current_candidate:
  package: github.com/syumai/workers, system:syumai-workers, pinned at v0.33.0
  status: unblocked; the probe below passed on both compilers on 2026-09-02 and requirement:cloudflare-workers-build-target delivers the artifact
  important_distinction: this path is Cloudflare JavaScript-hosted Wasm, not the component-model WASI HTTP path deferred by decision:wasi-http-deferred
probe_results:
  date: 2026-09-02, after a first TinyGo-only pass on 2026-08-26 at TinyGo 0.41.1
  application: pw.NewServeMux routes wrapped by pw.Middlewares with an embedded public tree, handed to workers.Serve, no config file
  tinygo: 0.42.0 -target wasm -no-debug, 3.6 MB raw and 1.3 MB gzip, 18 of 18 checks under wrangler 4.128.0
  go: 1.27.0 GOOS=js GOARCH=wasm, 18.3 MB raw and 4.6 MB gzip, 18 of 18 checks; the gzip size exceeds the free plan's 3 MB and fits the paid plan's 10 MB
  checks: GET, POST body in UTF-8, repeated response headers, two Set-Cookie headers and received cookies, 302 with Location, chunked streaming through Flusher, HTML rendered through a generic Builder method, embedded static asset with ETag and 304, 404, 405 from a method pattern, security headers, X-Request-Id
  loader: the upstream TinyGo wasm_exec.js lacked runtime.getRandomData at TinyGo 0.42, and the compiler's own file lacks the context the Go side reads; decision:owned-wasm-loader holds the answer
  per_request_main: the host runs main for every fetch, measured as one startup summary per request; requirement:cloudflare-workers-build-target suppresses it
  body_order: reading the request body after the first response write fails in the host, upstream issue 176; a handler reads first
unblock_probe:
  application: one minimal Popcorn Web handler wrapped by pw.Middlewares and github.com/syumai/workers Serve
  matrix:
    - current project Go with GOOS=js GOARCH=wasm using the upstream worker-go template
    - decision:tinygo-042-baseline or later using the upstream worker-tinygo template
  local_runtime: Wrangler dev receives GET, POST body, duplicate response headers, cookies, redirect, and a streamed response
  limits: emitted upload size is checked against the active Cloudflare plan rather than copied as a permanent framework constant
delivery_after_unblock:
  owner: requirement:cloudflare-workers-build-target
  items:
  - a generated Cloudflare entry point that application code does not edit
  - pinned and diagnosed compiler, adapter, wasm_exec, and Wrangler versions
  - api:cli-generate before the Wasm compile, per rule:container-build-inputs
  - wrangler configuration and JavaScript loader generated as deployment artifacts
  - a local conformance test before any remote deploy action
  - documentation for unsupported filesystem, socket, database, streaming, and instance-lifetime behavior
fallbacks:
  - fix or contribute the smallest incompatibility upstream when github.com/syumai/workers remains the right bridge
  - own a narrow fetch-to-net/http adapter only if the required surface is small, testable, and upstream cannot accept it
  - keep Cloudflare as CDN and proxy to a supported container host; this is operational support, not Workers runtime support
non_goals:
  - silently falling back to a different application runtime
  - implementing Cloudflare bindings before the HTTP conformance probe passes; it has, and decision:cloudflare-bindings-placement orders them D1, R2, then KV
  - claiming generic WASI support from one Cloudflare-specific adapter
acceptance:
  - both compiler paths have recorded pass or precise failure results; both pass as of 2026-09-02
  - at least one path builds, runs under Wrangler, and passes the HTTP conformance probe; both do
  - the generated adapter initializes framework resources once per Wasm instance
  - a failed build names the incompatible dependency and supported version matrix
```
