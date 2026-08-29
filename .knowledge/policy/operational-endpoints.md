---
id: policy:operational-endpoints
type: policy
title: Operational Endpoints
---
The primary HTTP listener may expose minimal health, readiness, and generated OpenAPI endpoints configured by data:server-runtime-config.

```yaml
health:
  methods: GET and HEAD
  success: process is serving and not shutting down
  dependencies: none
readiness:
  methods: GET and HEAD
  success: startup completed and every enabled critical middleware resource reports ready
  failure: HTTP 503
  checks:
    - bounded and context-aware
    - include api:rdb-middleware and selected session backend availability
openapi:
  methods: GET and HEAD
  response: generated OpenAPI document
  requirement: build-time artifact enabled in data:project-config
  cross_origin: Access-Control-Allow-Origin star with credentials off, always, whether or not requirement:cors-middleware is enabled; the document publishes a contract, holds nothing per visitor, and is read by tools whose origins a deployment cannot enumerate
  cross_origin_when_protected: the wildcard forbids credentials, so a cross-origin reader of a document behind policy:authenticated-path-protection receives the unauthenticated answer
access:
  - health and readiness bypass session and authentication and reveal only status
  - OpenAPI follows policy:authenticated-path-protection like an application route
rules:
  - paths are unique absolute paths on the primary listener
  - return no DSN, backend name, stack, configuration, or dependency detail
  - disabled endpoints register no route
api_doc:
  status: implemented, superseding the earlier non-goal that named a hosted documentation UI out of scope
  configuration: data:server-runtime-config api_doc selects scalar or swagger, and api_doc_path serves it
  default: api:cli-init scaffolds it into the development configuration only, so staging and production omit the key and register no route
  access: policy:authenticated-path-protection applies, as it does to the document itself
api_catalog:
  status: implemented 2026-08-28, specified by requirement:api-catalog-well-known
  what: /.well-known/api-catalog answering an RFC 9727 Linkset that links the three endpoints above rather than describing anything of its own
  configuration: data:server-runtime-config api_catalog switches it on, and api_catalog_origin fixes the origin its links are built from; unset writes them relative
  methods: GET and HEAD, the HEAD answer carrying a Link header of the api-catalog relation
  access: policy:authenticated-path-protection applies, and the cross_origin wildcard of the OpenAPI document above applies for the same three reasons
  fixed_path: unlike the four keys above it takes no path, because the RFC fixes the location and the operator reading the file learns the address from the key name
non_goals:
  - metrics endpoint in the first release
```
