---
id: requirement:cloudflare-workers-build-target
type: requirement
title: Cloudflare Workers Build Target
---
api:cli-build --target cloudflare-workers emits a complete Wrangler-deployable Worker from an unchanged application: the Wasm module, the framework-owned JavaScript loader, the generated entry point, and the Wrangler configuration, for either compiler data:project-config names.

```yaml
audience: actor:application-developer
motivation:
  - requirement:cloudflare-workers-hosting passed its unblock_probe on both compilers on 2026-09-02, so the target moves from blocked to delivered under decision:serverless-target-scope
  - the probe needed one loader patch and one entry point shape that no application author should rediscover; both are build output here, per decision:owned-wasm-loader
  - the host re-runs the program per request, so a per-process startup summary becomes one log line per request unless the build says otherwise
configuration:
  where: data:project-config, the deploy.cloudflare table
  compiler: tinygo or go; defaults to project.toolchain, so a project that says nothing builds with the compiler it was scaffolded for
  compiler_constraint: tinygo is refused when project.toolchain is go, because that project routes through the standard ServeMux, whose method patterns TinyGo's net/http does not match; the Worker built and answered 404 on every route when this was allowed. go on a tinygo project is fine, since api:serve-mux works under both
  name: the Worker name written into wrangler.jsonc; defaults to project.name
  compatibility_date: the Wrangler compatibility date; defaults to the date pinned by the pw release, so a build is reproducible until the project chooses to move it
  backend: nethttp only; --backend fasthttp is refused because the fasthttp fork is not verified under js/wasm and system:syumai-workers serves an http.Handler
invocation:
  command: pw build --target cloudflare-workers
  toolchain_selection: the deploy.cloudflare.compiler value, not a flag, per decision:explicit-tinygo-compile-step's reasoning that the compiler choice is a project fact; this target is the one exception to api:cli-build never invoking tinygo, because there is no Dockerfile for the operator to own the line in
  tinygo: tinygo build -target wasm -no-debug -tags pwcloudflare, with -no-debug dropped under --debug
  go: GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -tags pwcloudflare, with the linker flags dropped under --debug
output:
  stage: .pw/build/cloudflare-workers/nethttp/, per decision:serverless-target-scope build_axes
  app: the application copied as a nested module, as requirement:serverless-source-entrypoints stages it; a root file that registers system:tinygodriver's host network driver under the tinygo tag has its staged constraint extended with !pwcloudflare, because the driver's socket calls do not compile for wasm, and the build names the file it left out
  entry: the transformed main beside a generated popcornweb_serverless.go, both package main, so the compiler links one program per Wasm module
  build/app.wasm: the compiled module
  build/wasm_exec.js: the framework-owned loader for the selected compiler, per decision:owned-wasm-loader
  build/worker.mjs: the framework-owned fetch entry that instantiates the module per request and hands the request to the Go handler
  wrangler.jsonc: name, main, compatibility_date, and vars derived from config.prod.toml
  deployment.json: target, backend, artifact, entrypoint, and compiler
entry_point:
  transform: pw.Run becomes capture through pw.Middlewares, exactly as requirement:serverless-source-entrypoints does for the other source targets
  serve: main sets APP_ENV to prod when unset, bridges the Worker environment, initializes the application once, and calls workers.Serve on the captured handler
  environment_bridge: every string value of the Worker env object is placed in the configbind environment before api:runtime-configuration loads, so wrangler vars and secrets reach the application by the same names a container environment would use
  body_order: a handler reads its request body before its first write, because the adapter hands the response to the host on the first write and the host then refuses the body; the same rule the probe recorded
runtime_configuration:
  no_filesystem: the Worker cannot read config.prod.toml, so policy:config-file-resolution finds nothing and defaults, environment, and flags apply
  vars: api:cli-build flattens every scalar and primitive-array key of config.prod.toml into wrangler.jsonc vars under the configbind environment names, so the file the project maintains is still what configures the deployment
  table_arrays: middleware.rdb.connections travels as JSON in the one variable pwconfig.ConnectionsEnv names, with ${NAME} references resolved from the Worker env at load, because it is the array the runtime cannot do without on this host; every other array of tables has no environment form and is reported by key rather than dropped silently
  secrets: a ${NAME} reference is written through unchanged and resolved from the Worker env at load, so a wrangler secret is what satisfies it
  external_assets: requirement:external-public-assets cannot be served, because nothing reads a directory in this host; the build reports the tree it did not ship and the embedded tree keeps serving
startup_summary:
  fact: system:syumai-workers instantiates the module and runs main for every fetch, so the framework initializes per request
  handling: the pwcloudflare build tag turns policy:startup-summary's auto selection to off, so an unconfigured project logs nothing at boot; observability.boot_log set explicitly is still honored
  cost: initialization measured at 1 to 9 ms per request on the probe, which is the host's price rather than a framework defect
verification:
  local: wrangler dev on the stage, then the conformance list of requirement:cloudflare-workers-hosting, which is the check the generated artifact has to pass before decision:serverless-target-scope reports the target shipped
  size: the emitted module size is printed raw and gzip, because the plan limit is the operator's to compare against, per requirement:cloudflare-workers-hosting limits
non_goals:
  - Cloudflare bindings; decision:cloudflare-bindings-placement takes them in the order D1, R2, KV, and requirement:cloudflare-process-state-refusal is what this target does about the state they replace until then
  - a fasthttp backend under this target
  - a listener, a scheduler, or a queue handler; the entry exports fetch only
  - wrangler deploy, account login, or any remote action, per decision:serverless-target-scope meaning_of_support
acceptance:
  - an unchanged scaffolded application builds the stage with either compiler and without editing its main
  - the stage runs under wrangler dev and passes the requirement:cloudflare-workers-hosting conformance list
  - a wrangler var and a wrangler secret reach a configbind key by its environment name
  - config.prod.toml scalars appear in wrangler.jsonc vars, and a table array is reported by key
  - a build without observability.boot_log set emits no startup summary per request
  - the loader carries every import the pinned compiler's Wasm module needs, so a compiler bump that adds one fails the build's own loader test rather than a deployed Worker
  - a host-Go project asking for the TinyGo compiler is refused at configuration load, naming the ServeMux reason
verified:
  date: 2026-09-02
  host_go_project: a pw init scaffold without --tinygo, compiled with go, 10 of 10 checks under wrangler dev, 19.0 MB raw and 4.8 MB gzip
  tinygo_project: a pw init --tinygo scaffold compiled with tinygo, 4.0 MB raw and 1.5 MB gzip
  checks: GET / as HTML, X-Request-Id, security headers, /healthz and /readyz reached through wrangler vars, embedded asset with ETag and 304, 404, no startup summary in the log, no development warning
```
