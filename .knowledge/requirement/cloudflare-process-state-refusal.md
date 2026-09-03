---
id: requirement:cloudflare-process-state-refusal
type: requirement
title: Cloudflare Process State Refusal
---
A configuration that keeps state in the process or opens a socket is refused under the Cloudflare Workers target, at build where the build can see it and at startup in every case, because the host runs main per request and such state silently does nothing.

```yaml
audience: actor:application-developer
motivation:
  - requirement:cloudflare-workers-build-target's host instantiates the module per fetch, so a memo store is empty on every request, a memory session store forgets every session, and a memory rate limiter limits nothing; each works and each is a lie
  - the host owns no socket and the build leaves the netdev driver out, so a sqlite file, a PostgreSQL DSN or a MySQL DSN cannot be reached; failing at the first query is later and less clear than refusing at startup
refused:
  cache.enabled: true, until requirement:cloudflare-kv-backends gives api:data-cache a KV store
  session.backend: dev-volatile, dev-persist, and rdb over a sqlite scheme; cookie is the backend that works today, and requirement:cloudflare-d1-engine the one that will
  ratelimit: an enabled limiter on the memory store; the cloudflarekv backend of requirement:cloudflare-kv-backends is accepted
  authstate: the memory and sqlite backends, until requirement:cloudflare-d1-engine
  middleware.rdb: any connection whose scheme is sqlite, postgres or mysql, until requirement:cloudflare-d1-engine adds the d1 scheme
where:
  build: api:cli-build --target cloudflare-workers already reads config.prod.toml to write wrangler vars, so it refuses the same keys before compiling; this is where a person sees it first
  startup: validateConfiguredRuntime under the pwcloudflare build tag, because wrangler vars can change after the build; under the host this is every request answering 500 with the reason in the log, per the startup race of decision:owned-wasm-loader
message: names the key, the value, and the backend to use instead, so the refusal is an instruction rather than a wall
non_goals:
  - refusing on the host build; the same configuration is correct for a process
  - a warning rather than an error for the cache; a cache that is always cold is worse than none, and the setting is one line to remove
acceptance:
  - a config.prod.toml with cache.enabled = true fails pw build --target cloudflare-workers naming the key
  - a wrangler var setting a refused value fails the Worker at startup with the key in the log, and every request answers 500 rather than hanging
  - the same configuration builds and starts unchanged on the host
```
