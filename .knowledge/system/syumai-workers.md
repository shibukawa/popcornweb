---
id: system:syumai-workers
type: system
title: syumai/workers
---
github.com/syumai/workers is the fetch-to-net/http adapter that lets a Go Wasm module serve HTTP inside a Cloudflare Worker; requirement:cloudflare-workers-build-target links it into the generated entry point.

```yaml
module: github.com/syumai/workers
pinned: v0.33.0, whose library is byte-identical to main as of 2026-09-01; the only later change is a template file
license: MIT
what_it_provides:
  serve: workers.Serve(http.Handler) registers the handler and signals the loader that the module is ready
  request_bridge: the JS Request becomes an http.Request, the handler's writes stream through an io.Pipe into the JS Response
  env: cloudflare.Getenv and GetBinding read the Worker env through a global the loader supplies
  sockets: cloudflare:sockets connect is exposed to Go for outbound TCP, unused by the framework so far
  bindings: cloudflare/d1 is a database/sql driver registered as d1, cloudflare/r2 wraps a bucket binding with get, head, put, delete and list, cloudflare/kv wraps a namespace with get, put, delete and list; decision:cloudflare-bindings-placement builds the framework backends on these
host_facts:
  per_request_main: the loader instantiates the module and runs main for every fetch, so process state does not survive a request
  body_after_write: a request body read after the first response write fails in the host, upstream issue 176
  loader: the module needs a wasm_exec.js that provides the context global; decision:owned-wasm-loader supplies the framework's own
input_offered:
  - the runtime.getRandomData import missing from the bundled TinyGo wasm_exec.js at TinyGo 0.42, found by requirement:cloudflare-workers-hosting's probe
  - the template fix for reading a body after a write, merged as upstream pull request 204
```
