---
id: requirement:cors-middleware
type: requirement
title: CORS Middleware
---
A deployment names the origins allowed to read it, and one frame answers the preflight and marks every response below it, so a cross-origin client reads the status this framework already computed instead of an opaque network error.

```yaml
status: implemented 2026-08-13, the same day the requirement was recorded
priority: should
source: user request 2026-08-13, the middleware named across the catalog does not exist
was_absent:
  verified: 2026-08-13 by reading the tree, before the implementation; no Go file in middlewares, pwfast, pwruntime or pwconfig contained the string
  described_in_four_places:
    policy:web-middleware: names CORS in the concerns list, so decision:backend-specific-middleware promises to port it per backend
    data:middleware-runtime-config: lists it as a switch and places it in recommended_order beside security.csrf
    policy:security-response-headers: declares CORS a separate middleware concern, which reads as a pointer at one that exists
    website service-proxy guide: says a second hostname costs a CORS preflight, offered as the alternative the guide steers away from
  therefore: the catalog describes a stack member that no configuration can enable, and the one documented alternative to the in-process proxy cannot be built on this framework at all
driving_case:
  primary: requirement:jwt-only-api-authentication and requirement:api-server-scaffold, a resource server whose caller is a browser page served from somewhere else
  why_that_one_is_clean: the token rides an Authorization header, no cookie is involved, policy:csrf-protection does not apply, and credentials mode stays off, so CORS here grants a read and nothing more
  refusals_are_the_point: requirement:typed-http-contract answers every machine client through api:problem-response, and a browser hands script neither the body nor the status of an unmarked cross-origin response; without this frame the typed 401, 403, 429 and 422 all arrive as one indistinguishable network failure
  retry_metadata: requirement:rate-limit-problem-responses returns Retry-After and the X-RateLimit-* trio, none of them CORS-safelisted, so a cross-origin client that cannot be told to back off keeps retrying at the rate that got it limited
  fonts_and_assets: a font served by policy:public-asset-resolution to a page on another origin is fetched in cors mode by every browser, and today it fails there for the same missing header
placement:
  frame: the policy:security-response-headers frame, which does this too rather than gaining a neighbour, decided in decision:cors-above-the-refusals
  slot: SlotSecurityHeaders, moved from 60 to 52, between SlotRecover and SlotRateLimitProcess
  consequence: the frame runs above every frame that can refuse, and below tracing, resources, client address, request ID, access log and recover
  it_also_closes: the configured policy headers now reach the 429 and 503 written at SlotRateLimitProcess, which 60 left uncovered
  preflight_is_not_rate_limited: settled 2026-08-13; the answer is above SlotRateLimitProcess and no bound of its own is added, so a preflight is counted by nothing
preflight:
  recognised: OPTIONS carrying Access-Control-Request-Method
  answered: by this frame, 204, with no further frame reached
  passed_through: an OPTIONS without that header, which is not a preflight and belongs to whatever answers OPTIONS
  nothing_answers_it_today: pwfast servemux sets HandleOPTIONS false with the note that Go does not answer OPTIONS either, so an unanswered preflight reaches the router and becomes 404 or 405
  no_credentials_on_it: the browser sends a preflight without cookies, without Authorization and without the CSRF header, which is why answering it below SlotSession would refuse every one of them
  csrf_already_ignores_it: both CSRF implementations treat OPTIONS as safe, so the preflight was never the frame that would have refused it; session resolution, authentication and the guard were
  response_headers: Access-Control-Allow-Origin, Access-Control-Allow-Methods, Access-Control-Allow-Headers, Access-Control-Max-Age, Access-Control-Allow-Credentials when enabled
  cache_key: Vary Origin, Access-Control-Request-Method and Access-Control-Request-Headers
  browser_caps_max_age: Chrome 7200s, Firefox 86400s, Safari 600s, so a larger configured value is silently reduced rather than honoured
actual_request:
  marks: Access-Control-Allow-Origin, Access-Control-Allow-Credentials when enabled, and Access-Control-Expose-Headers
  when: before calling next, the way policy:security-response-headers already sets its headers, so the marking is on the writer before any frame below writes a refusal
  no_origin: a request carrying no Origin is left unmarked, because it is not a CORS request
  unallowed_origin: no header, and the request continues; the browser is what refuses it, and answering 403 here would deny a same-origin caller that sent an Origin on a POST
declined:
  preflight_from_an_unlisted_origin: 204 with no allow header at all, since a caller that may not send the request needs nothing further to decide
  preflight_for_an_unadmitted_method_or_header: 204 carrying the methods and headers the configuration does admit, so the browser refuses on the specific mismatch and a developer reading the response sees what was allowed rather than an empty answer
  actual_request: unmarked and passed on, per unallowed_origin above
  never_403: a refusal here would deny a same-origin caller that sent an Origin, and would answer a question the browser was going to answer anyway
origin_matching:
  form: exact scheme://host[:port], the shape security.csrf.trusted_origins already takes
  one_implementation: internal/requestorigin, which normalises and compares origins for policy:csrf-protection and the auth endpoints; a second comparison would be a second set of rules about what a port or a trailing slash means
  wildcard: the literal * only, and only with credentials off
  null_origin: never matched, under any configuration, since a sandboxed iframe and a data URL document both present it and neither is an origin a deployment can have meant
  no_pattern_language: no https://*.example.com and no regular expression, for the reason policy:bearer-admission gives about expression languages in framework configuration
  reflection: an allowed origin is echoed from the configured list, never from the request, which is the distinction between an allowlist and an open proxy for the visitor's credentials
vary:
  rule: emit Vary Origin on every in-scope response while an allowlist is configured, including responses this frame left unmarked
  why_including_unmarked: the decision read Origin even when the answer was to emit nothing, and a shared cache keyed on nothing serves the allowed origin's grant to a caller who was refused it
  wildcard_exception: allowed_origins of * with credentials off answers identically for every caller, so it needs no Vary and keeps the response shared-cacheable
  deviation: policy:preference-vary-correctness varies only on a signal an accessor resolved from, and this varies on a signal whose resolution produced no header; the difference is that a preference miss changes nothing about who may read the body and an origin miss is the whole answer
  composes_with: decision:streaming-response-compression and decision:bot-client-classification, which add their own values rather than being replaced
credentials:
  default: off, which is the configuration the driving case wants
  what_on_actually_grants: a cross-origin read of any in-scope path as the logged-in visitor, which is every authenticated page, every requirement:navigation-delta-rendering and requirement:reloadable-component-endpoint GET, and the api:live-delivery-protocol stream
  writes_stay_unavailable:
    mechanism: policy:csrf-protection compares a token whose transport is a cookie only same-origin script can read, so a cross-origin page cannot attach security.csrf.header and every unsafe request is refused 403
    not_a_defence_to_rely_on: it holds for the host-only token cookie this framework writes, and a deployment that broadens that cookie to a shared parent domain has made the two origins same-site and changed the answer
    visible_failure: the 403 reaches the browser as a CORS-shaped error, so the deployment sees a cross-origin problem where the refusal was the origin check
  custom_header_defences_weaken:
    live: policy:csrf-protection rests the live stream on a cross-origin form or link being unable to set a custom header, and a preflight that admits that header is exactly the grant that removes the obstacle
    consequence: a credentialed configuration whose include covers the live path is a subscription grant to the allowed origin, and it should be scoped rather than assumed harmless
  csrf_header_admitted: while credentials are on, the configured security.csrf.header is admitted in preflight without the deployment listing it, because admitting a name grants nothing the token check does not still gate and a header that must be remembered is the one that is forgotten
configuration:
  binding: data:security-runtime-config security.cors, beside csrf and headers, because this is a browser policy resolved and validated at startup like both of them
  not_the_middleware_binding: data:middleware-runtime-config names CORS as a member of the stack and its order, which is what that binding describes; the values live where the other two security policies live
  scope_grammar: security.cors.include and exclude, the segment grammar and exclude precedence shared with policy:authenticated-path-protection and policy:csrf-protection rather than a third one, kept for the reasons path_scope gives
  unmatchable_path: refuse to mark a request whose canonical path cannot be matched unambiguously, for the reason policy:csrf-protection gives
startup_validation:
  - enabled with an empty allowed_origins is an error, since it can only produce a frame that marks nothing
  - allow_credentials true with * is an error, because the specification forbids the pair and a browser drops the response either way
  - allow_credentials true with * in allowed_headers is an error for the same reason
  - allow_credentials true with an include of /** is an error, per the narrowing path_scope forces
  - an origin carrying a path, query, fragment, trailing slash, userinfo or the literal null is an error
  - a control character or an invalid field value anywhere in the binding is an error, as policy:security-response-headers already requires of its own
  - a negative max_age is an error; zero means no preflight caching and is legal
advisories:
  owner: rule:configuration-advisories, because the failure mode of each is a value nobody set
  built: 2026-08-13
  PW0417: allow_credentials true with an origin absent from security.csrf.trusted_origins while the check is on, warning in every environment, because that origin's unsafe requests are refused by the origin comparison and the browser reports the 403 as a cross-origin failure, so the deployment looks in the wrong policy
  PW0418: an http:// origin admitted outside dev, warning
  credentialed-read-over-the-page-tree:
    specified_as: an advisory
    became: a startup error on an include of /** with credentials, which is the form that matters and refuses rather than warns
    not_built_as_an_advisory: a narrower include that still covers a page tree cannot be recognised from the pattern text, and a check that fires on some of them reads as one that covers all
observability:
  problem: a declined origin produces a response indistinguishable from a served one; the status is 200 or 204, the access log records a request that worked, and the only account of the failure is a console message in somebody else's browser
  no_reporting_channel: a CORS failure is not a Reporting API type, so requirement:browser-report-ingest never sees one; unlike a CSP violation there is no browser-side report at all, and the frame that declined is the only thing that knows
  same_argument: browser-report-ingest exists because a page broken by a shipped default said nothing on the server, and this is that failure with the browser half removed
  record: one api:logger record naming the request path, the declined origin, and which of origin, method or header did not match
  never_recorded: the request headers themselves, per policy:log-emission; the origin is the only caller-supplied field this needs
  level:
    is: info, everywhere
    specified_as: debug outside dev and info in the api:cli-dev stream
    why_it_changed: the shipped observability.minimum_level is info in every environment including dev, so the debug half would have been a record nobody sees, which is not an improvement on no record and is the failure this exists to fix
    what_makes_it_affordable: the bound below, which is stricter than the unbounded warn the CSRF refusal already writes
  bound: the drop-and-count shape requirement:browser-report-ingest uses for its own flood, since an origin is caller-controlled and a scan writes one record per request
  dev_console: requirement:dev-telemetry-viewer is where the record earns most, and it needs nothing beyond the record existing
implementation:
  as_built: 2026-08-13
  resolver:
    is: pwruntime ResolveCORS, beside ResolveSecurityHeaders, both called by the one frame at construction
    specified_as: one resolver extended rather than two
    why_it_became_two: the resolved shapes share nothing — one is a list of headers to set on every response, the other is an origin map, a method set, a header set and compiled path patterns — so one entry point would be one struct with two unrelated halves
    what_the_specification_was_actually_after: that a misconfiguration is an error before the port is bound and both transports compute nothing of their own, which two resolvers called from one frame delivers unchanged
  decision_is_transport_free: ResolvedCORS.Decide takes the path, the method and three header values and returns what to write, so the two transports share the deciding and differ only in how they read a header and set one
  frames: the existing security-headers pair, one per backend under decision:backend-specific-middleware, each a thin caller of both resolvers
  cost: no new frame and no new slot; the added work per backend is the preflight short-circuit, since the marking is a header set the frame already knows how to write
  what_the_frame_gains: a branch that can answer and stop, which the header-only version never had, and which is the one real widening of its contract
  headers_off_sends_nothing: the header half now resolves to an empty set when disabled, which mattered to nobody while Enabled was also what installed the frame and matters now that the cross-origin half can install it alone
non_goals:
  - a subdomain or expression origin pattern
  - a per-route CORS declaration in application code, which is the per-route thing that gets forgotten and which the include grammar covers
  - Private Network Access and its Access-Control-Allow-Private-Network header, a Chromium-only negotiation this framework has no case for
  - the WebSocket upgrade, which is not subject to CORS at all; requirement:contrib-websocket rests on CheckOrigin and requirement:proxied-request-identity owns the resolution it needs
  - Timing-Allow-Origin, COOP and COEP, which belong to policy:security-response-headers if anywhere
  - a second listener or a distinct origin for the API, rejected for the reasons decision:report-endpoint-above-the-session already records
acceptance:
  - a preflight for an allowed origin and an allowed method is answered 204 without reaching session resolution, authentication or the guard
  - a preflight for an origin absent from the list is answered without the allow headers, and the browser refuses the request that would have followed
  - an OPTIONS carrying no Access-Control-Request-Method reaches the router unchanged
  - a 401, 403, 429 and a panic-derived 500 each reach a cross-origin caller carrying Access-Control-Allow-Origin, so the status is readable
  - a 429 exposes Retry-After and the X-RateLimit-* trio to a cross-origin client
  - an allowlist configuration emits Vary Origin on an in-scope response that carried no Origin, and a * configuration with credentials off emits none
  - allow_credentials true admits the configured CSRF header in preflight with no deployment listing it
  - a font under the public asset mount is fetched from another origin by a page and loads
  - both transports emit byte-identical headers for one request, and one resolver rejected the same configuration before either bound a port
  - enabling the frame with no allowed origin, or * with credentials, or /** with credentials, each fails startup naming the field
  - the generated OpenAPI document is read by a page on an origin nobody configured, on a deployment whose CORS frame is disabled
  - a project created from the api-server preset carries a commented security.cors block and starts with the frame off
  - a request from an unlisted origin is served normally and leaves exactly one record naming that origin and the reason
  - a preflight for an unadmitted method is answered carrying the methods that are admitted, not an empty allow set
documentation:
  written: 2026-08-13, both locales
  guide: website guides/backend/cors, whose boundary section names the three deployments that should not reach for it and the fourth that looks like this problem and is the service proxy
  reference: a security.cors table in reference/configuration, with the four startup refusals stated
  middlewares_guide: the slot table carries 52 and one paragraph on why a framework frame is off the multiples of ten
  service_proxy: its second-hostname alternative now names the guide that configures it, since it became available for the first time
  standards_index: requirement:web-standards-overview carries the Fetch Standard entry in security_headers, and the wildcard in its OpenAPI section, both added 2026-08-13 after shipping
path_scope:
  decided: 2026-08-13, security.cors.include and exclude are kept
  cheap: the segment grammar is shared with policy:authenticated-path-protection and policy:csrf-protection, so this is a third user of one implementation rather than a third grammar
  credentials_off: whole-app scope discloses nothing a curl could not already fetch, since the browser attaches no cookie and the answer is the one an anonymous caller gets
  the_exception_to_that: a deployment reachable only from inside a network, where the attacker's browser is the thing inside the perimeter and the response was not otherwise fetchable; Private Network Access is the mechanism built for it and is a non-goal here, so scope is what such a deployment has instead
  credentials_on: whole-app scope is a read grant over every authenticated page, every navigation-delta and reloadable GET, and the live stream, which is a different grant from the API the deployment meant to open
  narrowing_is_forced_with_credentials: an include of /** with allow_credentials true is a startup error, so a deployment opening a credentialed origin has to name what it is opening
  one_policy_per_deployment: include and exclude select where the one configured origin set applies, and carry no origin set of their own
considered:
  per_path_origin_rules:
    shape: a list of scoped rules, each with its own include, origins, methods and credentials, first match winning
    why_not: it is the configuration expression language policy:bearer-admission refuses, and the case that motivates it is the OpenAPI document, which openapi_document answers as a property of that endpoint rather than as a rule every deployment writes
    if_it_returns: a deployment genuinely needing two origin sets on two paths is the evidence that would justify it, and nothing in the tree needs one today
openapi_document:
  decided: 2026-08-13, readable from anywhere by default
  what: the generated document at data:server-runtime-config openapi, and the api_doc UI policy:operational-endpoints places beside it
  header: Access-Control-Allow-Origin star with credentials off, always, whatever security.cors says and whether or not the frame is enabled
  why_the_endpoint_and_not_the_configuration: the document describes a contract the deployment already chose to publish, carries nothing per visitor, and is the one path whose plausible reader is a tool nobody can enumerate in advance
  why_it_is_safe_even_when_protected: policy:operational-endpoints puts the document behind policy:authenticated-path-protection like any route, and a wildcard forbids credentials, so a cross-origin page reading a protected document receives the unauthenticated answer and learns nothing
  consequence: a Scalar or Swagger UI on another origin, and any generator pointed at the URL, works with no CORS configuration at all
  not_extended_to: health and readiness, which answer whatever the configured policy says, because a status probe has no reader whose origin nobody can name
scaffolding:
  decided: 2026-08-13
  who: requirement:api-server-scaffold, whose reader is building the machine-facing API this requirement's driving case names
  what: a commented security.cors block naming enabled, allowed_origins and allowed_methods, inert until uncommented because enabled defaults false
  where: the base configuration rather than config.dev.toml, since which origins may call is a deployment fact and not a development relaxation
  silent_about_the_document: the block says nothing about server.openapi, which needs no configuration per openapi_document above
```
