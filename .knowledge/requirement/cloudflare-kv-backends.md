---
id: requirement:cloudflare-kv-backends
type: requirement
title: Cloudflare KV Backends
---
KV backs the stores that tolerate eventual consistency, the rate limiter and the data cache, so a Worker keeps approximate state across requests without pretending KV is a database.

```yaml
audience: actor:application-developer
priority: third of decision:cloudflare-bindings-placement
status: the rate limiter half is implemented 2026-09-02; the data cache half stays open
motivation:
  - KV propagates in tens of seconds and offers no read-your-writes across locations, so it is wrong for a session that rotates or an auth state consumed once, and requirement:cloudflare-d1-engine covers those
  - a rate limit is an estimate and a cache entry is a hint; both are what KV is built for, and both are what requirement:cloudflare-process-state-refusal takes away from a Worker until this exists
shape:
  ratelimit: ratelimitstore/cloudflarekv, registered under ratelimit.backend = cloudflarekv beside memory and redis, counting in the namespace ratelimit.cloudflarekv.binding names; the count is read, incremented and written with the window as expiration, rounded up to KV's one-minute minimum, and the expiration is set on every write, so a window measures from the last arrival; the host build registers the name and refuses to open
  cache: deferred behind requirement:cloudflare-cache-memo-store, which fits a hint cache better than KV's write limits do; a KV store would key as decision:memo-store-handle keys are and invalidate through a prefix list, for a deployment that wants entries shared across colocations at the price of tens of seconds of propagation; both need the backend seam that requirement names first
  package: compiled under js && wasm over system:syumai-workers cloudflare/kv; the host file reports the backend absent
  configuration: the namespace binding name in the store's own section; [[deploy.cloudflare.kv]] of data:project-config names the namespace id behind a binding, and api:cli-build writes kv_namespaces with a placeholder id for a binding the table does not name
  development: api:cli-dev runs the memory backends; KV is a deployment fact
open:
  - the cost of a list per invalidation, and whether tags stay supported on this backend or are refused
  - whether the memo store's per-request emptiness is better served by KV or by the Cache API of the host
non_goals:
  - sessions or auth state on KV, however convenient the API is
  - strong consistency of any kind
acceptance:
  - a rate-limited route under wrangler dev limits across requests with the configured window, within the estimate the backend documents
  - a memo store entry survives from one request to the next under wrangler dev and expires at its stale deadline
  - requirement:cloudflare-process-state-refusal accepts these backends and keeps refusing the memory ones
```
