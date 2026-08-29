---
id: requirement:api-catalog-well-known
type: requirement
title: API Catalog Well-Known URI
---
A deployment that already serves an OpenAPI document answers `/.well-known/api-catalog` with an RFC 9727 Linkset assembled from the endpoints it is configured to serve, so a client that knows only the origin finds the contract, the reference UI, and the status probe without being told any path.

```yaml
status: implemented 2026-08-28, the same day the requirement was recorded
priority: should
source: user request 2026-08-28 naming RFC 9727, published June 2025
standard:
  rfc: RFC 9727, Standards Track, https://www.rfc-editor.org/rfc/rfc9727
  registers:
    well_known_uri: api-catalog, permanent, change controller IETF
    link_relation: api-catalog, "a list of APIs available from the Publisher of the link context"
    profile_uri: https://www.rfc-editor.org/info/rfc9727
  normative:
    - a supporting Publisher SHALL answer GET /.well-known/api-catalog with an API catalog document
    - a supporting Publisher SHALL answer HEAD /.well-known/api-catalog with a response carrying a Link header of the api-catalog relation
    - the catalog MUST be published as application/linkset+json, the RFC 9264 Linkset format
    - the content type SHOULD carry the profile parameter naming the profile URI above
    - the catalog MUST include hyperlinks to API endpoints
    - metadata such as usage policy, version, and the OpenAPI definition is RECOMMENDED in the catalog, or otherwise reachable from the endpoint URIs it lists
    - other formats MAY be offered by content negotiation at the same URI, and never instead of the Linkset
  borrowed_relations:
    service-desc: RFC 8631, machine-facing description of the API
    service-doc: RFC 8631, human-facing documentation
    service-meta: RFC 8631, further machine-facing metadata
    status: RFC 8631, API health indication
    item: RFC 6573, a member of the catalog
why_this_framework_is_the_easy_case:
  premise: RFC 9727 asks a Publisher to collate metadata that its API management framework already holds, and lists that collation as the expensive step of adoption
  here: data:server-runtime-config already holds every target the RFC's own primary example links to, so the catalog is a projection of configuration rather than a document anybody authors
  mapping:
    service-desc: server.openapi, the generated document of policy:operational-endpoints, assembled by system:tinybind
    service-doc: server.api_doc_path, the Scalar or Swagger page of requirement:dev-api-reference
    status: server.health, the liveness endpoint
    service-meta: nothing today, so the relation is omitted rather than pointed at a placeholder
  consequence: adoption adds a serializer and one configuration switch, and adds no metadata source, no authoring step, and no release-lifecycle task
driving_case:
  primary: requirement:api-server-scaffold and requirement:jwt-only-api-authentication, a resource server whose caller was handed an origin and nothing else
  today: the caller has to be told /openapi.json out of band, because nothing on the origin says where the contract is; the path is a framework default the deployment may have moved, and requirement:cors-middleware already grants the cross-origin read of a document nobody can find
  generator_tools: an OpenAPI client generator, an API gateway importer, and a catalogue crawler each start from an origin, and this is the one path they can all be pointed at by construction
document_shape:
  form: RFC 9727 Appendix A.1, the typed-relation shape, plus one Appendix A.2 item link
  why_both: A.1 carries the relations this framework has targets for, and its API endpoint appears only as an anchor; the §4.1 MUST reads on the strictest interpretation as requiring a link whose target is the endpoint, so one item link from the catalog to the API base satisfies that reading at the cost of one link, and neither reading is left to be argued at a client
  contexts:
    catalog:
      anchor: the absolute well-known URI
      item: the API base URI, one entry, because one binary publishes one API
    api:
      anchor: the API base URI
      service-desc: the openapi path, type application/json
      service-doc: the api_doc path, type text/html
      status: the health path, with no type member, because the endpoint answers with a status code and no body and a declared type would be a claim about bytes that are never sent
  omitted_links: a relation whose configuration key is unset emits no member at all, rather than an empty array, per RFC 9264 requiring an array with a distinct object per target
  reference_form: the example below is the configured-origin form; an unset origin writes the same document with every anchor and href reduced to its path, per reference_form
  example: |
    {"linkset":[
      {"anchor":"https://api.example.com/.well-known/api-catalog",
       "item":[{"href":"https://api.example.com/"}]},
      {"anchor":"https://api.example.com/",
       "service-desc":[{"href":"https://api.example.com/openapi.json","type":"application/json"}],
       "service-doc":[{"href":"https://api.example.com/docs","type":"text/html"}],
       "status":[{"href":"https://api.example.com/healthz"}]}
    ]}
rfc_9264_conformance:
  href: MUST be present on every link target object, and SHOULD NOT be a relative reference
  anchor: MUST be a URI reference and SHOULD NOT be a relative reference
  relation_member: MUST be an array of link target objects, one per target
  the_rfc_contradicts_itself_here:
    where: RFC 9727 §5.1 writes "api-catalog" with a bare string value, while Appendix A.4 writes the same relation as an array of link target objects
    which_is_right: A.4, because RFC 9264 §4.2.2 requires the array and RFC 9727 §4.2 requires conformance to RFC 9264
    why_it_is_recorded: §5.1 is the example a reader lands on first, and an implementation copying it emits a document a conforming Linkset parser rejects
reference_form:
  the_recommendation: RFC 9264 says anchor and href SHOULD NOT be relative references, so a link set reused outside the HTTP exchange that delivered it still resolves; it is a SHOULD NOT, and the empty-string href that specification defines for a self-target is itself a relative reference
  the_framework_has_no_origin: no configuration key names the deployment's external scheme and host, and decision:ingress-tls-termination records that TLS terminates upstream on every framework-started deployment, so r.TLS is nil and the listener sees neither the scheme nor necessarily the host the client used
  chosen:
    configured: server.api_catalog_origin, an absolute scheme-and-host origin, writes absolute links and satisfies the recommendation; it is also the RFC §5.1 canonical-instance answer for a deployment answering on several hosts
    unset: absolute-path references -- /openapi.json rather than https://host/openapi.json -- which resolve against the URI the client fetched
  why_not_guess_from_the_host:
    the_first_design_did: an unset origin derived one per request from the Host and the effective scheme, with an advisory outside dev about the Host being caller-controlled
    what_was_wrong_with_it: it made the working configuration the one the diagnostic warns about, so api_catalog = true was not something a deployment could set on its own; the settings file demanded an origin before the endpoint was usable
    and_it_was_the_worse_form_anyway: relative and Host-derived resolve to the same place for the client in front of the process, and they differ only once the document outlives that exchange -- which is the case the recommendation is about; there a guessed absolute URI is durable and may name a host the deployment does not own, while a relative reference can only ever mean wherever the document was read from
    the_house_rule_already_said_so: plugin/auth trustedOrigins declares its origins from auth.passkey.origins and auth.oidc.redirect_url and records that neither is inferred from a header a caller can send; the derived origin was a third answer to a question this module had already answered once
    neither_of_those_can_serve_this: both exist only under their own feature, and the catalog's driving case is a bearer API with no passkey ceremony and no redirect URL, so the origin is declared here or not at all
  consequences_of_dropping_the_derivation:
    always_prebuilt: the document depends on the settings and on nothing about a request, so it is assembled once at startup on both transports and the byte-identical acceptance holds by construction
    no_request_read: the frame reads neither Host nor the forwarded scheme, so requirement:proxied-request-identity keeps the host out of scope and nothing here reopens it
    shared_cacheable: one answer for every caller, with no Vary and no dependency a cache key cannot express
  advisory_changed_with_it: PW0429 became a note naming the missing self-containment, from a warning naming a caller-controlled origin, because the served document is now conformant and works and what it lacks is what one setting would add
transport:
  path: /.well-known/api-catalog, fixed by the RFC and not the deployment's to choose
  methods: GET and HEAD, as every other endpoint of policy:operational-endpoints answers both
  content_type: application/linkset+json; profile="https://www.rfc-editor.org/info/rfc9727"
  head_link_header: Link: </.well-known/api-catalog>; rel="api-catalog"
  head_is_self_referential:
    why: RFC 9727 §3 allows the relation target to be the well-known URI itself, and this framework serves the document there rather than resolving to a separate location, so the header names the resource that answered
    what_it_is_for: a Publisher that hosts the catalog elsewhere uses the header to say where; here it says the location did not move, which is the answer §2 asks for
  https: the RFC §8 SHOULD is the deployment's ingress to satisfy, not the binary's; the framework's contribution is that the derived origin says https because the effective scheme is resolved rather than read from a nil r.TLS
  compression: the existing requirement:response-gzip-encoder covers it, and RFC §5.3's compression guidance needs nothing new for a document this size
  rate_limiting:
    is: the RFC §8 recommendation, met by enabling requirement:rate-limit-problem-responses, which sees this path like any other; no bound of its own is added
    deliberately_not_exempt: the framework exempts health, readiness, the document, the UI and the asset mount, because each is high-frequency traffic the framework itself routes and counting it turns the limit into an outage; a catalog is fetched once by a discovery client and is not that traffic, and the RFC asks for the opposite of an exemption here
access:
  authenticated_path_protection: policy:authenticated-path-protection applies, as it does to the OpenAPI document itself, so a deployment that protects its contract protects the pointer to it by the same rule
  cross_origin: Access-Control-Allow-Origin star with credentials off, always, on exactly the terms requirement:cors-middleware openapi_document already argues for the document
  why_the_same_answer: the argument there is that the document publishes a contract the deployment chose to publish, holds nothing per visitor, and has readers whose origins nobody can enumerate; a discovery endpoint is the strongest case of all three, and a catalog readable only from origins already known defeats what it is for
  private_apis: RFC §5.2 and §8 describe an internal catalog reachable only by authorised roles, which on this framework is the ordinary composition of policy:authenticated-path-protection with a CORS configuration that does not reach the path, and needs no separate mode
configuration:
  key: server.api_catalog, boolean, default false
  deviates_from_its_siblings: server.health, server.readiness and server.openapi are path strings whose unset value disables them, so that an operator reading the file sees every address the deployment answers on; this one is a switch, because the address is fixed by the RFC and stated by the key
  origin_key: server.api_catalog_origin, an absolute origin, optional, per absolute_uris above
  scaffolding:
    who: requirement:api-server-scaffold, whose reader is building the machine-facing API this exists to advertise
    gate: the same servesAPI test corsRuntimeConfig uses, so the two blocks reach one reader rather than two
    what: api_catalog = true beside the endpoints it links, with api_catalog_origin commented, because which origin this deployment answers on is a fact nobody has chosen at scaffold time
    where: config.dev.toml, beside server.openapi and server.api_doc; a base-file switch would be on in every environment while the document it links is named only here, which is the PW0425 refusal
    elsewhere:
      is: api_catalog = false, which api:cli-init writes into every scaffolded configuration rather than omitting, so the endpoint is learned from the file that configures the three endpoints it links
      why_written_at_all: an omitted key is one a reader finds only by reading the reference, and the decision this key carries -- whether this project publishes an API on purpose -- is one they should meet beside the endpoints it would name
      why_false: a project serving pages publishes an API as a side effect of having handlers, which is not the same as deciding to publish one
      package_kind: writes no runtime configuration at all, so nothing about the endpoint reaches it
  no_link_customisation: a deployment cannot add, rename, or retarget a link, because every target is a path the same file already sets and a second spelling of one of them is a value that drifts from the route it describes
  as_built: pwconfig.ServerConfig api_catalog and api_catalog_origin, the latter dependon the former, carried into pwruntime.ChainSettings so the transport that binds no configuration composes from the same reading
diagnostics:
  owner: rule:configuration-advisories
  built: 2026-08-28, both entries
  PW0425:
    trigger: server.api_catalog is on while neither server.openapi nor server.api_doc names a path
    severity: error, every environment
    why: the served document would carry a status link and an item link and no link to any API description, which satisfies the §4.1 MUST on no reading, so the endpoint answers with a document that conforms to nothing
    remedy: set server.openapi, or turn the catalog off
    also_a_startup_refusal: pwruntime.ResolveAPICatalog returns the error, so both transports refuse to build a chain rather than serving the document; the advisory is what finds it by reading the configuration instead of by a failed start
  PW0429:
    trigger: server.api_catalog is on with no server.api_catalog_origin, diagnosed outside dev
    severity: note
    why: the links are then relative, which every client following the catalog resolves correctly and which a reader that stores one cannot, per reference_form
    remedy: name the deployment's canonical origin
    not_a_warning: the served document is conformant and works; what is missing is the self-containment RFC 9264 recommends, which is a note about an improvement rather than a report of a fault
  path_clash: needs no check, because PW0202 already refuses an application route colliding with an enabled framework mount and this is one
  route_table:
    the_path_is_listed: pwcli frameworkMounts names it under server.api_catalog, and pw validation's operationalEndpointPaths carries it, so the fixed path is checked for duplicates and application-route collisions like every configured one
    why_it_has_to_be: it is the one framework address a reader cannot find by reading the settings file, because the standard chose it
  no_adoption_nudge:
    considered: a note on a deployment serving server.openapi with the catalog off
    rejected: the arrangement works as designed and rule:configuration-advisories keeps such findings out, so the note would be the kind that teaches a reader to skim the report
non_goals:
  - nested catalogs, the RFC §4.3 and §5.3 answer to a portfolio too large for one document; one binary publishes one API, and a deployment large enough to need grouping is assembling the catalog above these binaries rather than in one of them
  - the RFC §5.1 canonical redirect across a Publisher's domains, which is ingress configuration and not a route this binary can register
  - the Appendix A.3 alternatives -- APIs.json, HAL, RESTdesc, and the Schema.org WebAPI extension -- offered by content negotiation, since the Linkset is the only mandatory format and none of the four is generated from anything the framework holds
  - a service-meta link, which would need a usage-policy or terms document this framework has no source for; RFC §4.1 permits the metadata to live at the listed endpoint URIs instead, and the OpenAPI document is where it does
  - emitting the api-catalog link relation on ordinary application responses, per RFC §3; the well-known URI is discoverable by construction, and a header on every page is bytes on every response for a lookup a client performs once
  - listing individual routes as separate APIs, which reads "API" as one operation where the RFC means the specification resources of a whole API
acceptance:
  - GET /.well-known/api-catalog returns 200, application/linkset+json with the profile parameter, and a body a conforming RFC 9264 parser accepts
  - HEAD on the same path returns the same headers, a Link header naming the api-catalog relation, and no body
  - every anchor and href in the emitted document is an absolute URI
  - every relation member is an array, including a single-target one, so the §5.1 bare-string form is emitted nowhere
  - a configured api_catalog_origin is used verbatim, and a request carrying a forged Host does not change one URI of the response
  - an unset openapi path removes the service-desc member entirely rather than emitting an empty array
  - the health endpoint's status link carries no type member
  - a cross-origin fetch from an origin nobody configured reads the document on a deployment whose CORS frame is disabled
  - enabling the catalog with no openapi and no api_doc fails api:cli-doctor with PW0425
  - both transports emit byte-identical documents for one request, per decision:backend-specific-middleware
  - a project created from the api-server preset serves the catalog on first run
implementation:
  as_built: 2026-08-28
  serializer:
    is: pwruntime.buildAPICatalog, hand-written rather than marshalled, so the two transports emit one byte sequence and the shared leaf links no reflection a second build has to carry
    resolver: pwruntime.ResolveAPICatalog, beside ResolveCORS and for the same reason -- a misconfiguration is an error before a port is bound, and neither transport validates its own
  frames: the documentation frame of each transport, which already answers the document and the UI this points at, so no frame and no slot were added
  placement_consequence: the catalog sits beneath the guard with the document it describes, so a deployment protecting its contract protects the pointer to it by one rule rather than two
documentation:
  written: 2026-08-28, both locales
  standards_index: requirement:web-standards-overview carries an API discovery section beside the OpenAPI one, because the catalog is projected from the endpoints that section already describes
  guide: the operational-endpoints deployment guide gains the section, since that page is where all four framework endpoints are configured and where a reader deciding to enable one is already looking
  reference: two rows in the [server] table, plus the two paragraphs a table cannot carry -- why the key is a switch, and why an unset origin is a hazard rather than a convenience
  boundary_stated: the guide names the deployment that should not reach for this, which is the one serving pages
```
