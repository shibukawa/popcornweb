---
id: requirement:cli-request
type: requirement
title: CLI Request Invocation
---
A developer or an AI agent invokes one registered route of the project from the terminal with curl-shaped flags, and the command uses the generated OpenAPI contract to name the operations, route each parameter to the place the handler reads it from, and report the response in a form an agent can parse.

```yaml
status: first slice implemented 2026-09-11, the same day it was proposed; second and later slices open
priority: should
source: user request 2026-09-11, citing the Hono CLI article https://zenn.dev/yusukebe/articles/ff69c13ccafb28
audience:
  - actor:application-developer
  - an AI coding agent working the project through the bundled skill of decision:canonical-popcornweb-skill-source
precedent:
  hono_request: "hono request -P /api/users -X POST -d '{\"name\":\"Alice\"}' src/index.ts" runs the app's request() in-process, without a listener, and prints status and body as JSON
  what_transfers: the curl-shaped flags, the no-boilerplate invocation, and the machine-readable report an agent can act on
  what_does_not: loading the application source dynamically; a Go application is a binary, so the request reaches it either over HTTP or through a framework-owned token inside that binary, per decision:request-execution-target
problem:
  - a developer checking one handler today writes a curl line by hand, reading the handler source to learn which field is a path segment, a query key, a header, or a JSON body member
  - an agent doing the same has no command that names the routes the project serves and accepts a value per parameter, so it guesses a request shape and reads a 400 to learn the real one
  - the framework already generates the answer: system:tinybind emits the OpenAPI fragments that say where every input lives, and data:route-table names every registration site
scope:
  routes: the registered router of decision:dual-router-coexistence, which is the half the OpenAPI document describes
  operations: what data:operation-catalog lists, which is every route rule:static-route-discovery resolved and the framework endpoints policy:operational-endpoints mounts
  invocation: api:cli-request, one request per invocation, flags after the curl spelling
  routing: rule:request-parameter-routing decides where a -d pair goes when the operation is known, and falls back to curl semantics when it is not
  discovery: --list prints the catalog, so an agent learns the operations before choosing one
  transport: decision:request-execution-target, HTTP to the running application first, in-process later
slices:
  first_as_built:
    - internal/pwrequest holds the projection, matching, routing, and the one send; internal/pwcli/request.go holds the command; the dev console answers GET /api/application with the announced address
    - documented at website pw/project/request in both locales, listed in the pw overview, and added to the bundled skill's command table and workflow reference
  first:
    - api:cli-request against a running application, resolved from --url or the api:cli-dev loop
    - data:operation-catalog read from the application's server.openapi document, or from the requirement:api-catalog-well-known linkset when the path is not configured
    - --list, operation selection by method-and-path or operationId, rule:request-parameter-routing, curl-style -H -b -d -F, raw and JSON reports
  second:
    - the in-process form of decision:request-execution-target, a framework-owned request token in the application binary beside the healthcheck token of requirement:healthcheck-subcommand
    - api:cli-request spawning that form when no application is running
  later:
    - a host-side data:operation-catalog assembled by api:cli-generate, which needs system:tinybind to assemble fragments on the host
    - a discovered-route form for concept:page-tree pages, which answer HTML and have no OpenAPI shape to route by
non_goals:
  - hono docs and hono search, which ship documentation to an agent; decision:canonical-popcornweb-skill-source already does that through the skill tree, and the website is the documentation
  - hono serve --use, applying middleware from the command line; decision:config-driven-database and requirement:application-middleware-registration compose middleware from configuration and code
  - hono optimize; the registered router is net/http and has no prepared-router form to emit
  - a request-recording or replay pane in requirement:dev-console, which requirement:dev-api-reference declined for the same reason: the application already serves the reference UI
  - login ceremonies; a passkey or OIDC session cannot be created from a terminal, so a guarded route is called with --identity through the seams tests already use, or with a bearer token or cookie the caller obtained elsewhere, as with curl
  - load generation, concurrency, or timing statistics
  - a general HTTP client; a target outside the project's own application is refused, which keeps the command from being a curl with a worse name
acceptance:
  - pw request --list on a generated project names every registered route with its method, path template, and the location of each parameter
  - pw request POST /api/users -d name=Alice against an operation whose body is JSON sends {"name":"Alice"} with a JSON content type
  - pw request GET /api/users/{id} -d id=7 -d verbose=true fills the path segment and puts verbose in the query when the catalog says so
  - pw request /unknown -d a=b, a path the catalog does not know, sends a form body exactly as curl would, and says the route was not in the catalog
  - --format json prints one object carrying status, headers, and body, and an agent parses it without scraping
  - --fail exits nonzero on a 4xx or 5xx, and a transport failure exits nonzero regardless
  - with api:cli-dev running and no --url, the request reaches the address the loop announced, including after decision:development-port-shift moved it
  - a request to a target that is not the project's application is refused before any connection is opened
  - pw request --list marks every operation whose path the configured include and exclude patterns protect, and an anonymous in-process call to one is refused before dispatch
  - pw request --identity GET /account, with cli.identifier declared, answers from the handler as that subject in both forms, and the handler sees the principal the application's account resolver returns for it
  - pw request --identity with no cli.identifier table exits 2 naming the key
settled_2026_09_11:
  list_placement: --list stays on pw request; a pw routes command covering the discovered half is not added, since requirement:editor-route-explorer shows that half in the editor
  in_process_chain: the in-process form dispatches through the bare mux with no session, CSRF, or rate-limit middleware, because no login ceremony is reachable from a terminal, so the chain would only ever refuse; consequences in decision:request-execution-target
  protected_routes: the paths policy:authenticated-path-protection guards are known from configuration before any request, so data:operation-catalog marks them; an anonymous in-process call to one is refused rather than reaching a handler the guard would have kept it from, and an anonymous HTTP call sends and takes the 401 as its answer
  identity: a project declares data:cli-identity once under cli.identifier and calls with --identity, so a guarded route is exercised as that user; the seams are the two lower rungs of decision:test-authentication-seams, one per form, and no login ceremony is performed; one identity per project, since switching users from the shell is not a need
open_questions:
  - whether an agent-facing report should carry the operation the catalog matched and the routing it chose, so a wrong guess is visible in the output rather than only in the server log
```
