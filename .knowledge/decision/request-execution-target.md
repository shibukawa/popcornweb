---
id: decision:request-execution-target
type: decision
title: Request Execution Target
---
api:cli-request reaches the application over HTTP on loopback first, and gains a framework-owned request token inside the application binary as a second slice, because a Go application cannot be loaded into the CLI the way hono request loads a TypeScript module.

```yaml
status: accepted 2026-09-11; the http form and the console address query are built, the token form is not
proposed_by: requirement:cli-request
problem:
  hono_precedent: hono request imports the application module into the CLI process and calls app.request(), so no listener exists and no build step runs
  here: system:pw-cli is one binary and the application is another, compiled from the project by host Go or TinyGo per decision:host-tools-target-runtime; no in-process call crosses that boundary
  candidates:
    http: send to a running application; api:cli-dev already builds, runs, and learns the bound address per decision:dev-application-attachment
    token: a framework-owned leading argument in the application binary, like the healthcheck token of requirement:healthcheck-subcommand, that dispatches one request through the handler without binding a port
    host_analysis: evaluate the handler on the host from the generated binder; rejected outright, because a handler body reads a database and a session and nothing on the host stands in for them
decision:
  first_slice: http
  second_slice: token, spawned by api:cli-request when no application is running
resolution_order:
  - --url, when given, refused unless its host is loopback
  - the running api:cli-dev application, whose bound address the loop learned through decision:dev-application-attachment
  - the in-process token, once it exists; until then, an error naming pw dev as the way to start the application
finding_the_running_application:
  the_loop_knows: the announced address is the one that survives decision:development-port-shift, and data:dev-loop-state says it lives in memory in the pw process and is never written into the project
  so: api:cli-request asks the requirement:dev-console listener on dev.console.port, a fixed default per decision:dev-console-consolidation, for the announced application URL; the console gains one loopback query and nothing else
  fallback: the best-effort read of the development configuration's server.port, the same guess the console index shows before the application has announced, marked as a guess in the report
  refused: writing a state file into the project for this, which data:dev-loop-state rules out and which would be one more file the ignore rule has to name
why_http_first:
  - it works on the first day with no change to the application binary, and pw dev is already running in every workflow that would use this
  - the request crosses the real middleware chain, so a session, a CSRF check, or a rate limit answers as it would in a browser, which is the honest test
  - the catalog is served by the same application on server.openapi, so the contract and the handler are the same build by construction
why_token_second:
  - hono's no-server invocation is the property the precedent is valued for, and an agent that has broken the build wants to run one handler without keeping a loop alive
  - the token form has no port to bind and no console to find, so it is the one that works in CI and inside a container image
  - the application binary already holds the assembled document through system:tinybind OpenAPIDocument, so --list needs no server in that form either
token_shape:
  spelling: request, recognized only as the leading argument, like healthcheck; reserved in api:subcommands the same way
  behaviour: parse configuration through api:runtime-configuration, initialize the database and services the handler reads, dispatch one synthesized request through the bare mux with an in-memory response writer, print the report, and exit without binding a listener
  chain:
    is: the bare mux, with no session, CSRF, rate-limit, or authentication middleware; settled 2026-09-11
    why: a passkey or OIDC session cannot be created from a terminal, so a chain in front of a protected route can only refuse, and a CSRF check on a request nobody forged tests nothing; the handler is what the caller wants to exercise
    protected_routes:
      anonymous: an operation data:operation-catalog marks protected is refused before dispatch when --identity was not given, naming the pattern that protects it and --identity as the way to call it; settled 2026-09-11
      why_knowable: policy:authenticated-path-protection is include and exclude patterns in data:authentication-runtime-config, so the token evaluates the same matcher over the same configuration and the refusal is the guard's own answer, computed before the handler
      with_identity: --identity installs data:cli-identity as an authenticated data:request-authentication before dispatch, the handler rung of decision:test-authentication-seams, so the guard would admit and the handler runs as that user
      not_a_bypass: the bare mux reaches no route the chain would have refused, because an anonymous call is refused by the same matcher and an identified call carries what the guard checks for
      limit: a guard inside the handler, api:assurance-guard or an application check, reads the installed value as it would a real one; with no --identity it refuses on the missing session, and the report shows that refusal
identity_seam:
  settled: 2026-09-11, on the user's proposal to define the session for a CLI run up front
  identity: data:cli-identity, one per project under cli.identifier, applied by api:cli-request --identity
  explicit: the flag is required even for a route the catalog marks protected, because an anonymous 401 is a finding a caller may be after, and an identity applied silently would hide it
  ladder: decision:test-authentication-seams already names the two seams, one per form, and neither is new
  in_process: the handler rung; the token installs the identity through the same mechanism api:testutil-auth uses, pwruntime.WithSession and WithAuthentication, before dispatching through the bare mux
  http:
    rung: the server rung; a real api:session-manager session is minted through auth.EstablishSession and the request carries its cookie across the running application's real chain
    who_mints: the application, because the session manager and the account resolver live in its process; the pw process holds neither
    where: one request on the loopback dev listener of decision:dev-application-attachment, which exists only under the pwdev tag and answers only with the per-run attachment token
    how_the_cli_reaches_it: through the requirement:dev-console listener, which already proxies that dev listener and holds the token; the console gains a second loopback query beside the address query, taking no argument beyond the attachment token and returning, for the configured identity, the session cookies and the requirement:csrf-token-lifecycle token minted with them
    csrf: api:cli-request sends the returned token on the header requirement:module-native-csrf reads, so a state-changing call passes the check the browser would pass
    bound: the same bound decision:dev-application-attachment states for the data pane, which already accepts arbitrary SQL over this listener; a session for a named account is not a wider reach than that
  ceremony_rung: driving requirement:contrib-devidp through its flow:devidp-user-selection page with the cookie jar would be the faithful form in an OIDC mode; not built, because the server rung reads identically in every auth.mode and the ceremony is what framework tests prove
  consistency_with_rejections:
    test_seams: decision:test-authentication-seams rejects a header production middleware trusts and a configuration flag that installs a fixed identity; cli.identifier installs nothing and no header is read, since the in-process value is placed on a context only this process owns and the HTTP session is a real one the application recorded
    passkey: decision:passkey-test-authenticator rejects a flag that makes a deployment accept an unverified response; nothing here changes what a deployment accepts, since both seams exist only under pwdev
    build_tag: the token is built only under the pwdev tag, like --pw-print-dsn in pwconfig ParseFrameworkAction, because it installs an identity nobody authenticated and skips CSRF and rate limiting; a release build refuses the word by name rather than passing it through to the application's arguments
    two_answers: the HTTP form crosses the running application's real chain and the in-process form does not, and the report names which form answered, so a 401 from one and a 200 from the other is legible
  platform: one code path on both toolchains; the response writer is a small in-memory implementation rather than net/http/httptest, whose TinyGo standing is unverified, on the both_toolchains rule requirement:healthcheck-subcommand set
  refusal: api:application-lifecycle Middlewares refuses the pending token as it refuses healthcheck, because an application that owns its server cannot answer it
  build: api:cli-request spawns go run with the pwdev tag and the development environment, as decision:dev-harness-process already does for migration, so a database the token needs is the one pw dev would open
costs:
  http:
    - unavailable while the application is down, which is the same cost decision:dev-application-attachment accepted for the data pane
    - the console query is one more loopback surface, bounded to returning an address the console already shows
  token:
    - a full initialization per request, including the database, so it is slower than a call against a running process
    - a second entry into the binary, kept out of api:cli-build artifacts by the pwdev tag; the healthcheck precedent ships in every build and this one does not
non_goals:
  - a remote target; both forms reach the project's own application on this machine
  - hosting the handler inside the pw process
```
