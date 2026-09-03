---
id: requirement:cloudflare-cache-memo-store
type: requirement
title: Cloudflare Cache Memo Store
---
The data cache of api:data-cache keeps its entries in the Workers Cache API on a Cloudflare Worker, so a memo store survives the per-request program without paying KV's write limits, once the store has a backend seam to keep them behind.

```yaml
audience: actor:application-developer
priority: could; ahead of the KV cache half of requirement:cloudflare-kv-backends
status: proposed 2026-09-02 from the analysis below; not started
why_cache_api_over_kv:
  - a memo entry is a hint, and the Cache API's weaknesses are the ones a hint tolerates
  - KV writes cost and are limited to one per second per key, which a store that refreshes hot keys on every request cannot live under; Cache API puts and matches are free and take milliseconds
  - fresh and stale deadlines map onto Cache-Control on the stored Response, so the TTL is the platform's rather than a field of the entry
  - system:syumai-workers cloudflare/cache exposes Put, Match and Delete with a namespace option, so the client exists
what_changes:
  scope: entries live per colocation, unreplicated; a hit rate differs by colo and an invalidation reaches the colo it ran in, the rest waiting out the TTL; this is the same reason sessions never go here
  enumeration: the Cache API lists nothing, so InvalidateScope and InvalidateTag become generation keys: one generation entry per scope and per tag, recorded into each entry at creation and compared on read, at the cost of one match per tag per read
  key: a URL, synthesized from the store name and the key hash under a private host, because the API keys by Request
prerequisite:
  seam: the store is one in-memory structure today; a backend interface behind CacheStore, implemented by memory first, is the refactor this needs and the one the KV half needs too
  refusal: until then requirement:cloudflare-process-state-refusal keeps refusing cache.enabled on the Worker target
to_confirm:
  - whether the Cache API persists on a workers.dev route or only on a custom domain; documentation has said the latter, and the answer decides what the guide promises
  - the cost of the generation reads on a tagged store under wrangler dev
acceptance:
  - a memo store entry survives from one request to the next under wrangler dev and expires at its stale deadline
  - Invalidate, InvalidateScope and InvalidateTag remove or hide the entry within the colo that ran them
  - requirement:cloudflare-process-state-refusal accepts the backend and keeps refusing memory
```
