---
id: requirement:object-storage
type: requirement
title: Object Storage
---
An application stores and reads files through one object storage interface whose backend is selected by configuration: S3-compatible storage on a process host, the R2 binding in a Cloudflare Worker, and a local directory in development.

```yaml
audience: actor:application-developer
priority: should; the second half of requirement:cloudflare-r2-storage, placed here by decision:object-storage-in-framework
status: implemented 2026-09-03 as api:storage-package; the external tree still reads through its own source hook, and presigned URLs and multipart stay out
motivation:
  - an application that accepts uploads needs an object store on every host, and today it reaches one only by calling a client directly, which ties a handler to the host it was written on
  - requirement:cloudflare-r2-storage serves the external public tree from a bucket through a source hook of its own; the application-facing half has no home yet
  - system:tinygodriver storage/s3 already speaks the API R2, S3 and every S3-compatible store implement, so the process-host backend is an adapter rather than a client
interface:
  package: storage
  bucket: Get, Head, Put, Delete and List on a Bucket value, each taking a context, with Get returning the body as a stream the caller closes and Head returning metadata alone
  object: key, size, ETag, media type, and last modified, in the shape system:tinygodriver storage/s3 already reports; user metadata carried as a string map
  put: media type required, content length optional, metadata optional
  list: by prefix, paged by a continuation token, so a large bucket is never read whole
  errors: a missing key is a sentinel the caller tests with errors.Is, never a nil object; every other failure carries the backend's error
  no_seek: a body is a stream, not a file; a caller needing Range asks the backend for a range rather than seeking
backends:
  s3:
    package: storage/s3, wrapping system:tinygodriver storage/s3
    configuration: endpoint, region, bucket, and credentials by reference to environment names, never inline, per the secret rule of data:server-runtime-config
    hosts: every process host, and R2 through its S3 API from outside a Worker
  r2:
    package: cloudflare/r2, the binding client of requirement:cloudflare-r2-storage, implementing the same interface; compiled under js && wasm, with the host file refusing to open
    configuration: the binding name; wrangler.jsonc carries r2_buckets for it, per requirement:cloudflare-workers-build-target
  local:
    package: storage/local, a directory under the project, for api:cli-dev and tests
    configuration: the directory, relative to the project
    why: a developer loop that needs an S3 endpoint before a file can be stored is one nobody starts with
selection:
  configuration: a [[storage.buckets]] array, each element naming a bucket by the name handlers address it by, its backend, and the backend's settings; the shape data:project-config's cache.stores and middleware.rdb.connections already use
  environment_form: the array travels as JSON in one variable, the way pwconfig.ConnectionsEnv carries connections, so a Worker receives it through wrangler vars
  handle: storage.Bucket(ctx, name) resolves a configured bucket from process state, the way api:dynamo-package resolves its Handle; no context value stands between a handler and the store
  refusal: a backend the host cannot reach is refused at startup naming the bucket, per requirement:cloudflare-process-state-refusal's shape; on a Worker only r2 is reachable
external_public_tree:
  fact: requirement:cloudflare-r2-storage's ExternalAssetSource is a read of one bucket by manifest path
  convergence: the r2 source reads through the same cloudflare/r2 Bucket the r2 backend is, so one client serves the tree and the application's own objects; folding the source into a configured bucket is open
development:
  api:cli-dev: the local backend by default, so a scaffolded upload handler works before any bucket exists
  parity: the same handler runs against s3 and r2 without a change, which is what the interface is for
presigned_urls:
  status: designed, waiting on system:tinygodriver; the request is docs/tinygodriver-s3-presign-request.md
  interface: Presign(ctx, key, PresignOptions) on Bucket, returning a URL a browser may GET or PUT for a bounded time
  s3: the client's SigV4 query-string signer, once it exists there, so one signer serves every host and the redirect re-signing it already has keeps it right
  r2: the same signer against the R2 S3 endpoint, when the bucket's configuration carries an access key beside the binding; without one, refused by name, because the binding has no presign of its own
  local: a URL the application serves itself under an HMAC, which is also the fallback for a deployment that exposes no bucket
  why_not_built_here: cloud/aws exports the canonical helpers, but the signing key and canonical request are that library's, and a presign that disagreed with its header signer by one encoding rule would be a SignatureDoesNotMatch no test here catches
non_goals:
  - a second SigV4 implementation here
  - multipart upload, until an application needs an object the single Put limit refuses
  - a typed object binding in system:tinybind; the interface carries bytes and metadata
  - S3 through system:tinygodriver from inside a Worker; the binding is the Worker's client
verified:
  date: 2026-09-03
  host: a handler doing put, head, get, list, delete and the not-found check against the local backend under the host process
  worker: the same handler against the r2 backend under wrangler dev, from one source, with the binding declared by the build
acceptance:
  - a handler stores and reads back an object against the local backend under api:cli-dev
  - the same handler runs against storage/s3 on a process host and against cloudflare/r2 under wrangler dev from one source
  - a missing key is reported by the sentinel on every backend
  - a Worker configured with an s3 bucket is refused at startup naming the bucket
  - the external public tree is served through the interface with the ETag and Range behaviour requirement:cloudflare-r2-storage verified
```
