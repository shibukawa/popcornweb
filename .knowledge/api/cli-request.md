---
id: api:cli-request
type: api
title: pw request
---
pw request sends one request to the project's own application with curl-shaped flags, routes named parameters by data:operation-catalog, and prints the response raw or as one JSON object.

```yaml
usage:
  - pw request [flags] [METHOD] PATH
  - pw request [flags] OPERATION_ID
  - pw request --list [--format json]
proposed_by: requirement:cli-request
status: built 2026-09-11 for the HTTP form; --identity parses and is refused as not built, so the flag is reserved rather than silently ignored
target:
  positional: a path such as /api/users/{id}, a method-and-path pair, or an operationId from the catalog; a literal path with a filled segment such as /api/users/7 is matched against the catalog templates
  method: -X or the leading METHOD word; default GET, or POST once -d or -F is given, as curl does
  resolution: decision:request-execution-target, in the order --url, the running api:cli-dev application, the in-process form
  refused: a --url whose host is not loopback, so the command reaches the project's application and nothing else
flags:
  curl_spelling:
    -X, --request: method
    -d, --data: a key=value pair, @file, or a raw body; repeatable
    -F, --form: a multipart field, key=value or key=@file; repeatable
    -H, --header: a header line; repeatable
    -b, --cookie: a cookie string or a cookie-jar file
    -c, --cookie-jar: write received cookies to a file, so a login response feeds the next call
    -u, --user: basic credentials
    -i, --include: print the status line and headers before the body
    -o, --output: write the body to a file
    -f, --fail: exit nonzero on a 4xx or 5xx
    -s, --silent: no progress and no advisory lines
    -L, --location: follow redirects; default off, because a redirect to the login page is the finding
  own:
    --url: the application origin, overriding discovery
    --env: the environment token whose configuration names server.openapi and the port, defaulting as api:cli-doctor does
    --list: print data:operation-catalog and send nothing
    --format: raw or json; raw is the body on stdout as curl prints it, json is one object with status, headers, body, and the routing report
    --no-catalog: skip the catalog and send exactly what the curl flags say
    --strict: a -d pair naming nothing in the matched operation is a usage error instead of a reported body field
    --identity: make the request as data:cli-identity, the cli.identifier table of the project, through the seam of decision:request-execution-target for the form in use
    --json: send -d pairs as a JSON object even when the catalog is absent, the curl 8 spelling
routing: rule:request-parameter-routing
output:
  raw:
    stdout: the body, decoded from any content coding of decision:response-content-codings
    stderr: the routing report, one line naming the operation matched and where each pair went, unless -s
  json:
    shape: "{status, headers, body, operation, routing}"
    body: parsed when the response is JSON, a string otherwise, so an agent reads one shape
    operation: the catalog entry matched, or null with a reason
    routing: each pair and the location it was sent to
  list:
    raw: one line per operation, method, path template, operationId, a protected mark when the catalog carries one, then the parameters with their locations
    json: the data:operation-catalog entries
exit:
  response_received: 0, whatever the status, as curl does
  fail_flag: 22 on a 4xx or 5xx with -f, the curl code, so a script written for curl keeps its branch
  transport: 1 on no application found, a refused target, a connection failure, or a timeout
  usage: 2 on a flag the command does not know, a pair that names no parameter under --strict, or a catalog the command could not read while --list was asked
catalog_source:
  running: GET server.openapi on the resolved origin, or the service-desc link of requirement:api-catalog-well-known when the path is not in the configuration the command read
  absent: the command says the catalog is unavailable, sends the request with curl semantics, and marks operation null in the report; --list exits 2
  in_process: the document the application binary holds through system:tinybind, per decision:request-execution-target
chain_by_form:
  http: the running application's full middleware chain, so a protected route answers 401 without a credential as it would to a browser; with --identity the request carries a real session minted for that identity, and the CSRF token minted with it
  in_process: the bare mux, per decision:request-execution-target; with --identity the identity is installed on the request context before dispatch; without it, an operation the catalog marks protected is refused before dispatch with exit 2, naming the pattern and --identity; the report names the form and the identity
identity:
  source: data:cli-identity, the cli.identifier table in data:project-config
  undeclared: --identity with no table exits 2 naming the key
  explicit_credential_wins: a -H Authorization or a -b cookie beside --identity is a usage error, because two identities on one request is never what was meant
reporting: policy:cli-progress-reporting, though a single request rarely outlasts a second
neighbours:
  requirement:editor-route-explorer: lists routes in the editor and opens files; this sends requests and opens nothing
  requirement:dev-api-reference: the browser UI on the same document, for a reader; this is the same document for a shell and an agent
  requirement:healthcheck-subcommand: a fixed probe inside the binary; this is a chosen request from outside it
as_built:
  package: internal/pwrequest, with the catalog projection, net/http-style template matching, rule:request-parameter-routing, and Send; internal/pwcli/request.go parses the flags, reads the environment through the doctor loader, resolves the target, and prints
  flags_also: -G moves pairs to the query as curl does, --timeout bounds the one request at 30s by default, and a short flag takes an attached value as in -XPOST
  method_fallback: with no -X, a path that matches nothing under the curl-default method but exactly one operation under another method takes that operation, so /api/users/7 -d verbose=true reaches GET /api/users/{id} rather than a POST curl would have sent
  console_query: GET /api/application on the requirement:dev-console listener returns the announced listening URL or an empty string, read only from loopback like the loop-state query
  cookie_jar: the curl Netscape file format, name, value, and path honoured, domain fixed to the application's host
  exit_main: pwcli Main learned a coded error so a command can exit 2 or 22, beside the existing findings error that exits 1
  tests: internal/pwrequest unit tests over a tinybind-shaped fixture document; internal/pwcli end-to-end through Main against an httptest application inside a scaffolded project; devconsole test for the address query
non_goals:
  - a target outside the project's own application
  - persistent sessions beyond a cookie-jar file; a --identity session lives for the one request and is not written to the jar
  - assertions on the response; a test belongs in go test with api:test-run
```
