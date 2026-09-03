---
id: decision:object-storage-in-framework
type: decision
title: Object Storage Abstraction In The Framework
---
The object storage interface an application writes against lives in this repository, with system:tinygodriver storage/s3 and the cloudflare/r2 binding as two backends behind it, rather than in system:tinygodriver.

```yaml
status: accepted 2026-09-02; requirement:object-storage holds the work
why_here:
  - the interface exists so a backend is selected by configuration, and configuration-selected backends are this framework's pattern already: api:session-store, the rate limit counter of requirement:cloudflare-kv-backends, and requirement:contrib-auth-state each keep a registry here and a client elsewhere
  - the R2 binding implementation depends on syscall/js and system:syumai-workers; an interface in system:tinygodriver would pull a Cloudflare runtime into a networking driver library, or leave the binding outside the interface it was made for
  - the handle-and-config half is what api:dynamo-package and api:firestore-package do for system:tinygodriver's NoSQL clients, so an object store follows a layering the catalog already has
why_not_tinygodriver:
  - storage/s3 there is a concrete client with Get, Head, Put, Delete and List and its own ObjectInfo and Object types; that is the right thing for it to stay, and the adapter here is thin because of it
  - a second implementation in that repository would be the binding, which is not a network protocol
what_is_asked_of_tinygodriver:
  - nothing new; storage/s3's Object body contract and ObjectInfo are treated as the shape the interface adopts, so a change there is a change here
shape:
  package: storage, holding the interface, the backend registry, the handle accessor and the configuration section
  backends: storage/s3 over system:tinygodriver, cloudflare/r2 over the Worker binding, and a local directory for api:cli-dev
consequences:
  - an application that stores files imports storage and one backend, and its handlers run on a process host and in a Worker from one source
  - the external public tree of requirement:external-public-assets reads through the same backend when it is served from a bucket
```
