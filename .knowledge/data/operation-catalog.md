---
id: data:operation-catalog
type: data
title: Operation Catalog
---
The list of invocable operations api:cli-request routes parameters against, projected from the assembled OpenAPI document of system:tinybind and never authored by hand.

```yaml
proposed_by: requirement:cli-request
status: built 2026-09-11 as internal/pwrequest Catalog, from the running application's document; the host-side and in-process sources are not built
source:
  document: the OpenAPI 3.1 document policy:operational-endpoints serves at server.openapi, assembled from the per-package OpenAPI fragments of system:tinybind
  discovery: requirement:api-catalog-well-known service-desc when the command has no configuration to read the path from
  in_process: system:tinybind OpenAPIDocument inside the application binary, per decision:request-execution-target
  host_side: a later api:cli-generate output beside data:route-table, once system:tinybind can assemble fragments on the host; today assembly is a runtime call
entry:
  operation_id: the OpenAPI operationId, or method plus path template when the fragment carries none
  method: the HTTP method
  path_template: the route pattern as registered, with {name} segments
  parameters:
    - name, location (path, query, header, cookie), schema type, required
  request_body:
    content_type: the first media type the operation declares, which is what rule:request-parameter-routing assembles
    fields: top-level property names and schema types when the body schema is an object
  responses: status codes and media types, for the report and nothing else
  origin: application, or framework for the policy:operational-endpoints paths
  protected: whether the path template matches the include and exclude patterns of policy:authenticated-path-protection in the environment the command read, evaluated by the same matcher the guard uses; settled 2026-09-11
covers:
  - every registered route rule:static-route-discovery resolved into a fragment
  - the framework endpoints policy:operational-endpoints mounts, which the document already describes
does_not_cover:
  page_routes: concept:page-tree pages stay out of the document by decision:dual-router-coexistence, so they are invoked by path with curl semantics and never routed
  unresolved: registrations data:route-table lists as unresolved have no fragment; the command sends to them by path and reports no operation
  page_actions: api:page-action-endpoint handlers, generated rather than discovered, for the same reason as pages
matching:
  by_id: an operationId given as the target selects the entry directly
  by_path: a literal path is matched against path templates the way net/http matches, method first, so /api/users/7 selects GET /api/users/{id} and binds id=7
  ambiguous: two templates matching one path is a state decision:dual-router-coexistence permits, and the command reports both and sends nothing until a method or an operationId disambiguates
rules:
  - the catalog is a projection of the served document, so it is as current as the build that serves it and can disagree with a source edited since
  - a parameter's location comes from the fragment and never from the flag it arrived on
  - schema types drive coercion in rule:request-parameter-routing; a value the schema cannot hold is sent as given and reported
  - protected is a projection of configuration, so a route guarded inside its handler by api:assurance-guard is not marked; that guard answers its own refusal and the report carries it
consumers:
  - api:cli-request
  - a later pw routes listing, if requirement:cli-request settles that open question
```
