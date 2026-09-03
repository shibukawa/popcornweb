---
id: requirement:cloudflare-r2-storage
type: requirement
title: Cloudflare R2 Storage
---
R2 is the object store a Worker serves its external public assets from and an application on this host stores files in, reached through the bucket binding inside the Worker and through its S3 API from a process host.

```yaml
audience: actor:application-developer
priority: second of decision:cloudflare-bindings-placement
status: the external tree half is implemented 2026-09-02; the application-facing interface is requirement:object-storage, placed here by decision:object-storage-in-framework
motivation:
  - requirement:external-public-assets is the tree too large to embed, and requirement:cloudflare-workers-build-target cannot serve it, because the host has no filesystem; a bucket is where that tree lives on this host
  - an application that stores uploads needs an object store on every host, and R2 is the one this host provides without egress cost
  - system:tinygodriver storage/s3 already speaks the S3 API, which R2 implements, so the process-host half exists
shape:
  binding: cloudflare/r2, compiled under js && wasm over system:syumai-workers cloudflare/r2, with Open, Get, Head, Put, Delete and List on a Bucket; the host file reports ErrOutsideWorker
  assets: api:cli-build --target cloudflare-workers stages the external tree beside r2-upload.sh, which puts every file under its manifest path with its media type through wrangler r2 object put, taking --local or --remote; the generated entry installs pw.WithExternalAssets over the binding, and the middleware's ExternalAssetSource hook answers the mount from the bucket with the object's ETag, 304 on a match and 206 from the whole body
  configuration: deploy.cloudflare.r2.binding names the binding and deploy.cloudflare.r2.bucket_name the bucket, defaulting to the binding in lower case; wrangler.jsonc carries r2_buckets
  whole_body: an object is read whole per request so http.ServeContent can serve Range and conditional requests; streaming a range through the binding is open
  application_api: requirement:object-storage, whose r2 backend is the cloudflare/r2 package this requirement built
  development: api:cli-dev serves the external tree from disk as it does today; the bucket is a deployment fact
open:
  - whether the asset upload is a build step or a documented wrangler command, per decision:serverless-target-scope's rule that remote actions stay with provider tooling
  - range requests and conditional requests against the bucket, which the public mount answers today from the file
non_goals:
  - R2 as a session, cache or auth-state store; those are requirement:cloudflare-d1-engine and requirement:cloudflare-kv-backends
  - presigned URLs from inside a Worker before the binding exposes them
acceptance:
  - the external public tree of a scaffolded project is served from R2 under wrangler dev with ETag and 304 intact
  - a handler storing and reading an object runs on the host against S3 and in the Worker against the binding from one source
  - a missing binding is reported at startup by name
```
