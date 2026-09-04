---
id: api:storage-package
type: api
title: storage Package
---
The storage package is the bucket interface of requirement:object-storage: a handler resolves a configured bucket by name and stores, reads, lists and deletes objects through it, with the backend a blank import registered.

```yaml
package: github.com/shibukawa/popcornweb/storage
open: storage.Open(ctx, name) resolves the bucket [[storage.buckets]] names, opens it once per process through its backend's factory, and keeps it; an unknown name reports the configured set
bucket: Get, Head, Put, Delete and List, each taking a context
object: ObjectInfo carries key, size, ETag, media type, last modified and user metadata; Object adds the body as a stream the caller closes; ReadAll reads a body whole and closes it
put: PutOptions carries the media type, which is required, the length when known, and metadata
list: ListOptions carries prefix, limit and cursor; ListPage carries the objects and the next cursor
presign: Presign(ctx, key, PresignOptions) returns a URL a client uses for one request without the application's credentials; PresignOptions carries method, expiry, media type and signed headers; a backend that cannot issue one wraps ErrPresignUnavailable
signed_route: SignedPathPrefix, SelfServing, SignedHandler and SelfServed are the self-served half: a backend whose URLs point back at the application implements SelfServing, and the runtime mounts SignedHandler at SlotSignedStorage when one is configured
errors: ErrNotFound for a missing key on every backend; every other failure carries the backend's error
registry: RegisterBackend(name, factory) from a backend package's init; Backends lists the names; Validate is the startup check api:application-lifecycle runs, naming the blank import a configured backend is missing
backends:
  local: storage/local, a directory with a sidecar per object for the media type and metadata; the api:cli-dev default, and the one SelfServing backend
  s3: storage/s3, a thin adapter over system:tinygodriver storage/s3, including R2 through its S3 API from a process host
  r2: cloudflare/r2 over the Worker binding, compiled under js && wasm, with the host build registering a refusing factory
configuration:
  section: storage, with enabled and the buckets array; each element carries name, backend and the backend's own keys, the rest ignored so one file serves two hosts
  environment_form: pwconfig.BucketsEnv, STORAGE_BUCKETS, carrying the array as JSON with ${NAME} references resolved at load
  worker: requirement:cloudflare-process-state-refusal accepts r2 and refuses local and s3 at build and at startup
```
