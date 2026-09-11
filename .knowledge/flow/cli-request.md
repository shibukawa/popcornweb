---
id: flow:cli-request
type: flow
title: CLI Request Flow
---
One api:cli-request invocation resolves a target, loads data:operation-catalog, routes the given pairs, sends one request, and prints one report.

```yaml
flow:
  trigger: actor:application-developer or an agent invokes api:cli-request
  steps:
    - id: locate
      actor: system:pw-cli
      action: resolve the data:project-config project root and fail outside one, so the command never becomes a general client
    - id: resolve-target
      action: pick the origin per decision:request-execution-target, in the order --url, the api:cli-dev announced address queried from the requirement:dev-console listener, the configured port as a guess, the in-process token
      failure: exit 1 naming pw dev as the way to start the application
    - id: load-catalog
      action: fetch the document at server.openapi, or follow the requirement:api-catalog-well-known service-desc link, and project it into data:operation-catalog
      failure: continue with no catalog, unless --list was asked, which exits 2
    - id: list
      action: when --list was given, print the catalog in the selected format and stop
    - id: match
      action: select the operation by operationId, or by method and path against the templates
      failure: ambiguous match exits 2 naming the candidates; no match continues with curl semantics and records the reason
    - id: route
      action: apply rule:request-parameter-routing to every -d and -F pair, filling path segments, query, headers, cookies, and the body
      failure: a missing required path segment, or an unknown pair under --strict, exits 2 before any connection
    - id: send
      action: issue the one request with a bounded timeout, following redirects only under -L, and reading the cookie jar when -b names one
      failure: connection failure or timeout exits 1
    - id: report
      output: the response, raw on stdout or as the JSON object of api:cli-request, with the routing report; -c writes received cookies
    - id: exit
      action: 0 on any response, 22 under -f for a 4xx or 5xx
  failure:
    default: report and exit; no step writes into the project, and only the send step opens a connection
    never: no step builds the application in the first slice, and none reaches a host other than loopback
dfd:
  boundary: system:pw-cli
  actors:
    - actor:application-developer
  stores:
    - data:operation-catalog
  flows:
    - from: actor:application-developer
      to: system:pw-cli
      data: flags and pairs
    - from: system:pw-cli
      to: requirement:dev-console
      data: announced application address query
    - from: system:pw-cli
      to: api:application-lifecycle
      data: one HTTP request and the server.openapi read
```
