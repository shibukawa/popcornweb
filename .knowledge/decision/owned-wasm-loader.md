---
id: decision:owned-wasm-loader
type: decision
title: Owned Wasm Loader
---
The Cloudflare Workers loader JavaScript, wasm_exec.js and worker.mjs, is embedded in pw and written by api:cli-build, rather than taken from system:syumai-workers' asset generator at build time.

```yaml
status: accepted 2026-09-02, implemented by requirement:cloudflare-workers-build-target
context:
  - the upstream generator ships a wasm_exec.js per compiler that the compiler's own release did not review; at TinyGo 0.42 it lacked the runtime.getRandomData import the module requires, and every request failed at instantiation
  - the compiler's own wasm_exec.js cannot be used unchanged either: the upstream Go side reads the Worker context through a global the loader has to provide from the second argument of go.run
  - the two facts together mean the correct loader is neither file, and the merge of the two is what the probe hand-wrote
shape:
  files: one wasm_exec.js per compiler and one worker.mjs, embedded in the CLI beside the code that writes the stage
  derivation: each wasm_exec.js is the pinned compiler's own file with one change, the context proxy the Go side reads; the license headers stay
  worker_mjs: exports fetch only, instantiates the cached module per request, exposes the tryCatch helper and the ready signal the Go side calls, and hands the request to the handler the Go side registered
  pinning: the files are tied to the compiler versions the release supports, decision:tinygo-042-baseline and the go directive, and a compiler bump is where they are re-derived
why_not_the_upstream_generator:
  - a build would depend on go run of an upstream command whose output tracks upstream releases rather than the compiler the project builds with
  - the missing import was found by a failing Worker, not by a build; an owned file can be tested against the module's import list at build time
  - the generator deletes and recreates its output directory, so a patch had to be applied after it and before the compiler, which is a sequence no operator should own
why_not_contribute_only:
  - the import gap is worth an upstream pull request, and one is a candidate, but the loader's correctness for a given pw release cannot wait on it
consequences:
  - a compiler bump adds a step: compare the compiler's wasm_exec.js against the embedded one and carry the diff
  - the framework, not the application, answers for loader breakage in a deployed Worker
```
