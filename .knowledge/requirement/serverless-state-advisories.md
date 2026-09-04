---
id: requirement:serverless-state-advisories
type: requirement
title: Serverless State Advisories
---
pw build for a function target reports, before compiling, the configuration whose state the host keeps badly: a memory rate limiter, a file-backed SQLite connection, and a development session backend, each as an advisory naming the host's behaviour and the backend to use instead.

```yaml
audience: actor:application-developer
status: proposed 2026-09-04 from the question whether requirement:cloudflare-process-state-refusal applies to the other targets; not implemented
priority: should
motivation:
  - the four function targets of decision:serverless-target-scope keep a process warm across requests but run many instances, freeze an idle one, and give it only ephemeral disk, per requirement:serverless-source-entrypoints resource_lifetime
  - api:cli-doctor already knows rate-limit-store-in-process and memory-database-outside-dev, per rule:configuration-advisories, but reads them against a deployment shape it cannot see; a --target build knows the host and reads config.prod.toml anyway
  - the Worker build refuses the same keys, and a project moving from a Worker to Lambda would otherwise lose every warning at once
severity: warning, per decision:host-state-fit-severity; the build completes, and the stage carries the configuration as written
advised:
  ratelimit.backend memory: every instance enforces the configured limit on its own, so the effective limit is the instance count times what was declared, and a frozen instance forgets its counts; remedy is the redis backend
  middleware.rdb sqlite file: the file lives on the instance's disk and dies with it, so writes are lost on eviction and never shared; remedy is a server engine, unless the file is a read-only bundle, which the advisory names as the case that is fine
  session.backend dev-volatile or dev-persist: refused at startup outside development on every host already; the build says so before the deploy rather than after
  cache.enabled: per-instance by design and legitimate; reported as information rather than warning, so the reader knows the hit rate is per instance
where: buildProcessDeployment and buildSourceDeployment, reading config.prod.toml through the same parser the Cloudflare build uses, before the compiler runs
wording: the reason and remedy of the matching rule:configuration-advisories entry, so doctor and build say the same thing about the same key
not_covered:
  - a container platform scaled past one replica; the replica count is not in any file the build reads, and api:cli-doctor's advisories remain the answer there
  - memory session storage, which has no production backend by that name; sessions are rdb, cookie, redis, dynamo or firestore
acceptance:
  - pw build --target lambda with ratelimit.backend = "memory" completes and prints the rate-limit-store-in-process advisory once
  - the same with a sqlite:// file connection prints the ephemeral-disk advisory, and with sqlite://:memory: the existing memory-database-outside-dev one
  - a development session backend is reported at build with the same words the runtime refuses it with
  - a configuration with none of these prints nothing new
```
