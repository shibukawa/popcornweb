---
id: data:server-runtime-config
type: data
title: Server Runtime Config
---
The `server` binding controls HTTP listener lifecycle, limits, and proxy trust.

```yaml
fields:
  port: HTTP listen port
  read_header_timeout: duration
  read_timeout: duration
  write_timeout: duration
  idle_timeout: duration
  shutdown_timeout: duration
  max_request_body: bytes
  trusted_proxies: address or network list; the trust gate of decision:forwarded-header-trust, read by every consumer of requirement:proxied-request-identity rather than by one middleware
  health: absolute path of the liveness endpoint; unset serves none
  readiness: absolute path of the readiness endpoint; unset serves none
  openapi: absolute path of the generated OpenAPI document; unset serves none
  api_doc: scalar or swagger, naming the API reference UI of requirement:dev-api-reference; unset serves none
  api_doc_path: absolute path that UI serves at, read only while api_doc names one
  api_catalog: bool, the RFC 9727 well-known endpoint of requirement:api-catalog-well-known; a switch rather than a path because the standard fixes the location
  api_catalog_origin: absolute scheme-and-host origin the catalog's links are built from; unset writes them relative rather than guessing one, per requirement:api-catalog-well-known
  public.enabled: bool
  public.mount: absolute non-root path prefix
  public.read_local: bool
defaults:
  api_doc_path: /docs
  public.enabled: true
  public.mount: /public
  public.read_local: false
rules:
  - expose the port as server.port, --port, and PORT for container-oriented configuration
  - validate the port, positive limits, durations, and proxy networks at startup
  - graceful shutdown uses shutdown_timeout where supported
  - local TLS termination follows decision:local-tls-proxy-boundary
  - policy:operational-endpoints defines endpoint behavior and access
  - health, readiness, openapi, and api_doc carry no default, so that an operator reading a deployment's configuration sees every address it answers on; a default would leave endpoints running that no file mentions
  - api_doc_path is the one endpoint path with a default, and it is read only while api_doc names a UI, so it registers no route the file left unsaid
  - requirement:public-asset-delivery defines the public endpoint
  - local public root is ./public relative to the process working directory
  - public.mount is canonicalized to one leading and trailing slash and rejects root, dot segments, wildcards, queries, and fragments
  - reject duplicate endpoint paths, overlapping mounts, and collisions with application routes
  - OpenAPI documents are assembled from generated package fragments
  - api_catalog is refused with no openapi and no api_doc to link, per requirement:api-catalog-well-known
```
