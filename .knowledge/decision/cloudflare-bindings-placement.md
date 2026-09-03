---
id: decision:cloudflare-bindings-placement
type: decision
title: Cloudflare Bindings Placement
---
Cloudflare bindings are integrated in this repository as backend packages beside the existing store and engine packages, not in system:tinygodriver, and in the order D1, then R2, then KV.

```yaml
status: accepted 2026-09-02; requirement:cloudflare-d1-engine, requirement:cloudflare-r2-storage and requirement:cloudflare-kv-backends hold the work
why_not_tinygodriver:
  - system:tinygodriver's remit is speaking a network protocol under TinyGo: netdev, mbedtls, the fasthttp fork, the DynamoDB and Datastore HTTP clients
  - a Worker binding is not a network client; it is a syscall/js call on the env object the loader hands in, exists only inside a Worker, and has nothing to drive
  - the client layer already exists in system:syumai-workers, as cloudflare/d1, cloudflare/r2 and cloudflare/kv; what is missing is the framework glue, which is what sessionstore/dynamo and authstate/firestore are here
why_not_contrib:
  - contrib holds the authentication family; a store or an engine backend lives beside its siblings, so the packages follow that layout rather than a provider directory
order:
  d1_first:
    - D1 speaks the SQLite dialect and system:syumai-workers registers it as a database/sql driver, so the sqlstore and dialect code of api:session-store and requirement:contrib-auth-state, and the .pw.sql generation of requirement:database-engine-selection, apply with the least new code
    - it gives a Worker every store that needs read-your-writes consistency, which KV cannot
    - development keeps running on SQLite with the same dialect, so api:cli-dev is untouched
  r2_second:
    - requirement:external-public-assets cannot be served from a Worker, and R2 is where that tree goes; it is also the object store an application on this host would reach for, through requirement:object-storage per decision:object-storage-in-framework
    - the S3 API of R2 is already reachable through system:tinygodriver storage/s3 from a process host, so the Worker binding is the missing half rather than a new capability
  kv_last:
    - KV is eventually consistent, with propagation measured in tens of seconds, so it is wrong for a session that rotates, an authstate that is consumed once, or anything read back within a request cycle
    - its fit is the approximate stores: rate limiting and the data cache of api:data-cache
shape_of_every_binding:
  build_constraint: the binding package compiles under js && wasm only, with a host-side file that reports the backend absent, so a project builds on the host and under the target from one source
  configuration: the binding name is configuration, in the same section the process backends use, and reaches the runtime through the wrangler binding rather than through a DSN or a credential
  development: api:cli-dev runs on the host process, so each backend names the host substitute it pairs with, and wrangler dev is the only place the binding itself runs
  refusal: requirement:cloudflare-process-state-refusal turns a process-state configuration under the target into an error, which is what makes the missing binding visible before the first request
```
