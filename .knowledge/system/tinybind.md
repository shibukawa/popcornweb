---
id: system:tinybind
type: system
title: TinyBind
---
TinyBind is the generated binding, configuration, response, validation, streaming, and OpenAPI engine wrapped by the Popcorn Web public APIs.

```yaml
module: github.com/shibukawa/tinybind-go
pin: v0.5.13, moved from v0.5.12 by html_i18n_baseline, the message surface decision:upstream-message-surface asked for; v0.5.12 came from v0.5.10 by value_binding_error_and_hoisting below, skipping v0.5.11 which carried the same work with the boundary defect recorded there; v0.5.10 came from v0.5.9 by value_binding below; v0.5.9 came from v0.5.8 by cachekeybind below; v0.5.8 came from v0.5.7 by json_tag_options, which is the same release that carried configbind_verbosity_baseline; v0.5.7 came from v0.5.1 with the live source signal and the component cleanup hook, v0.5.1 from v0.5.0 where the update surface got its second half, v0.5.0 from v0.4.9 where both runtimes took one problem value and one document registry, and v0.4.9 from v0.4.3 where the update response became a value
pin_before_v0_4_9: v0.4.3 moved from v0.4.2 by delta_package_break; v0.4.2 came from v0.4.1 by requirement:pgx-native-execution for the sqlbind Rows cursor, v0.4.1 from v0.4.0 by requirement:context-lookup-performance for the handle resolvers and On entries, and v0.2.10 was left behind by decision:tinybind-v03-adoption
pin_staleness_correction:
  what: this file recorded v0.4.3 as both the pin and the current release until 2026-08-12, five pin moves after it stopped being either
  found_by: reading go.mod against this line while adopting configbind_verbosity_baseline
  reconstructed_from: the commits that changed the go.mod line, which is why the moves above name behavior rather than release notes
  not_reconstructed: what v0.4.4 through v0.4.8 and v0.5.2 through v0.5.6 contained; no pin ever rested on them here
html_template_baseline: v0.1.15
html_async_baseline: v0.1.20
html_live_baseline: v0.2.8, required by requirement:live-html-rendering; v0.2.7 introduced live boundaries and v0.2.8 answered the first of the integration requests raised against them
html_i18n_baseline: v0.5.13, required by requirement:application-i18n; a template carrying a messages declaration or a message reference fails to parse on any earlier release, and the implicit bindings of data:locale-bindings arrived in the same release
html_update_baseline: v0.3.3; v0.3.0 added the htmlupdate package, v0.3.1 handed the asset and every name to the caller per requirement:tinybind-runtime-ownership, v0.3.2 carried head on the action response, and v0.3.3 closed every remaining seam of requirement:tinybind-update-composition-seams and made CSRF module native; adopted by decision:update-runtime-convergence
route_tree_baseline: v0.2.6
current: v0.5.13, which added the message surface decision:upstream-message-surface asked for: a messages declaration, a {t} reference, embedder-declared implicit bindings with a path-segment kind, symbol resolution, and a parse surface for the tooling that rewrites templates; v0.5.12 fixed the boundary-root defect v0.5.11 introduced; v0.5.11 gave a failing external an error result, hoisted a binding to before the first byte, removed the typed page rung, and made a settled async boundary cacheable; v0.5.10 added the val binding both formats needed and typed server actions this framework has not adopted; v0.5.9 added cachekeybind and moved three htmlbind entries onto methods; v0.5.8 acted on json tag options for the first time and gave configbind the two levers requirement:startup-summary-brevity needed
was_current_at_v0_4_3: a performance release across the module that paid for it with delta_package_break; v0.4.2 added the sqlbind Rows cursor, v0.4.1 added the NoSQL handle supply modes, and v0.4.0 implemented the URL half of policy:template-escaping and rewrote the JSON decoder the generator emits
configbind_verbosity_baseline:
  shipped: v0.5.8, all three together; read against the module cache tree on 2026-08-12
  adopted_by: decision:config-verbosity-tag-adoption, for requirement:startup-summary-brevity
  why_it_matters_here: >
    before it, an effective-config output could hide a subtree only by a parent's
    emptiness, so a mode key that holds a value in every mode kept every mode's
    subtree visible, and a key sitting at its default could not be rated at all
  value_conditions:
    spelling: 'dependon:"auth.mode=oidc_only,oidc_passkey" and dependon:".backend!=cookie"'
    what: a dependon tag may test the parent's value rather than only its emptiness, so a variant-selecting key hides every branch it did not select
    comma: alternative values of one parent, never a second parent, and only after an operator
    absent_parent: compares as the empty string, so "=" hides and "!=" shows
    enabled_gate_free: a condition on a parent that itself carries dependon inherits that gate transitively
    upstream_design: its dependon-value-condition decision and dependent-key-visibility rule, not restated here
  summary_rating:
    spelling: 'summary:"omit"'
    what: rates a key as detail; ProvenanceEntry.Omittable reports it, and only while the winning place is the default layer
    reported_not_applied: >
      the library never drops for this reason, because a dependon condition states
      a fact about the configuration while a rating is a judgment about one
      surface, and only the caller knows which surface it is drawing
    upstream_design: its summary-tag-form decision and summary-key-omission rule, not restated here
  enum_became_load_bearing:
    what: generation now reads the enum tag to reject a value condition naming a value the parent does not declare
    why: a mistyped value hides a whole subtree silently and forever, which is the one failure of this feature a reader cannot diagnose from the output
    still_not: enforcement at load; this reads the tag to check a sibling tag, and a source-supplied value outside the enum is still accepted
    upstream_design: the enum_check section of its dependon-value-condition decision
  wire_form:
    broke: 'Definition.DependsOn went from map[string][]string to map[string][]Dependency, where Dependency carries Key, Op, and Values'
    added: 'Definition.Summary map[string]string, keyed and inherited exactly like Secrets'
    surface: api:runtime-configuration reads both through the generated definitions; no application names either
  this_framework: >
    the DependsOn change is why f193f96 regenerated pwconfig and plugin/auth
    alongside the unrelated JSON work, months before either tag was written
json_tag_options:
  shipped: v0.5.8; confirmed against a generated codec rather than release notes
  was: the generated codec read only the name portion of a json tag, so omitempty was inert and json:"-" excluded nothing
  now: the options are acted on, and read the encoding/json/v2 way rather than the encoding/json way
  omitempty: drops a member that would encode as "", [] or {}, and therefore leaves 0 and false on the wire
  omitzero: the one that reaches 0 and false
  unknown_option: fails generation, which is what stops a misspelled omitempy from quietly writing a field somebody meant to drop
  nil_composites: a nil slice encodes as [] and a nil map as {}, because Go draws no line between a nil collection and an empty one
  decode_asymmetry:
    what: the JSON decoder skips a dashed member, but the HTTP request binder still fills the field by its wire name
    consequence: 'a field tagged json:"-" in a pw.Parse struct is still set by a query parameter of that name; nothing in a Parse struct is unbindable'
    recorded_in: the request-binding reference and the skill pitfall list, which both stated the opposite before this release
url_scheme_baseline: v0.4.0, which is where policy:template-escaping's "validate scheme" rule stopped being a statement and started being code
  was: Escape handled &<>"' and nothing else, so javascript: — which contains none of them — reached the attribute unchanged and ran; isURLAttribute named five attributes, so xlink:href, data, srcset, ping, and cite took plain strings and were never scheme-checked at all
  now: URLAttr and URLListAttr apply a scheme allowlist before escaping, over every attribute a browser resolves; DefaultURLSchemes is http, https, mailto, and tel, relative forms always pass, and a refusal renders BlockedURL rather than dropping the attribute so a URL rejected in error leaves a trace
  data_urls: DefaultDataURLMediaTypes admits inline raster images by exact media type and excludes image/svg+xml, which is a script sink wearing an image's media type
  configurable: htmlbind.WithURLSchemes and htmlbind.WithDataURLMediaTypes, reachable through pw.HTMLOption; each replaces its list rather than extending it
  verified: rendered through the real compiler and runtime on 2026-08-06 — javascript:, JaVaScRiPt:, vbscript:, and data URLs of text/html and image/svg+xml all render BlockedURL, while http, https, mailto, relative, and a data URL of image/png render unchanged
json_decoder_break: v0.4.0 replaced jsonbind.RawJSONMap and httpbind.ReadJSONMap with a streaming jsonbind.Parser, so every committed *_pw_gen.go action decoder had to be regenerated
delta_package_break:
  what: v0.4.3 moved the update half of htmlbind into htmlbind/delta — Manifest, Instance, Delta, DeltaRecord, Operation, OpReplace, RenderDelta, RenderDeltaStream, DeltaStreamHead, and every Canon encoder
  why: an application that only renders documents now links none of the hashing and encoding that authenticating a validator needs, which is what the split buys
  htmlbind_keeps: a Collector interface as the observation seam, plus ChainHead and CollectChainAsync; htmlbind.CollectChain still exists but takes a Collector and returns []string, so a pre-v0.4.3 call compiles to a type error rather than an undefined symbol
  htmlupdate: EncodeManifest and DecodeManifest now speak delta.Manifest
  this_framework: no hand-written call site touched any moved symbol, so the whole cost was regeneration; committed *_pw_gen.go boundary encoders now import htmlbind/delta and call delta.CanonJoin
  emitter_changes_riding_along: an escape-exempt value — bool, int, float, datetime, date, time — is emitted as Ops.Raw rather than Ops.Text, which renders byte-identically without the escaping scan; strings and decimals stay on Text, so policy:template-escaping is unmoved. Generated binders hoist httpbind.Queries once per request and read fields through httpbind.QueryLookup, and one httpbind.ReadBody replaces the IsJSONRequest, IsFormRequest, and IsMultipartRequest ladder
  verified: regenerated on 2026-08-08 by go test ./internal/pwcli -run PagesFixture -update, with go build, go vet, and go test green, and examples/live_render compiled through TinyGo
public_wrappers:
  - api:request-binding
  - api:html-response
  - api:api-response
  - api:typed-stream
  - api:problem-response
  - api:runtime-configuration
defects:
  unguarded_position_lookup:
    status: fixed in v0.3.2, which is why this module first moved off v0.2.10
    was: three call sites dereferenced Fset.File(f.Pos()) after guarding f, pkg, and Fset for nil, and that call is the one that returns nil
    sites: generator/plan.go, generator/configbind.go, and generator/dynamobind.go
    fix: each now takes the handle and checks it, which is what generator/configbind_doc.go already did three files away
    symptom_it_removed: a nil pointer dereference in go/token.(*File).Name, taking the calling process down
    trigger: a Go file in a generated directory that does not parse, most often a zero-byte one an editor has created and not yet written into
    mechanism: packages.Load returns a syntax entry for a file it could not parse, that entry reports token.NoPos, and a FileSet lookup of NoPos is nil; measured against golang.org/x/tools with Popcorn Web out of the picture on 2026-08-02
    downstream_containment_kept: api:cli-generate unparsable_source and its recover stay, because the pre-check names the file and the line where the generator would only name the directory, and the recover bounds every generation panic rather than this one
asset_transform_seam:
  shipped: v0.3.1, in one commit that carried the hooks, the cache, the produced files, and the recorded dependency file together; read against the upstream tree on 2026-08-04
  correction: this said v0.3.3 until 2026-08-04, which was the version pinned here rather than the version that shipped it; nothing depends on the difference, since the pin is later than the seam
  design_lives_upstream: its build-time-asset-transforms concept, its element-reference-hook and derived-asset-generation requirements, and its transform-seam-ownership decision, none of which are restated here
  surface: GenerateOptions.ReferenceHooks and StrictReferenceHooks, ConversionCacheDir, DerivedAssetDir, ArtifactDerivedAsset, and GenerateResult Produced, Rewrites, and ReadSet
  results: value and skip; the markup-replacing element result is designed and not built
  head_contribution: v0.3.5 added ReferenceResult.Head as link, script, and style entries, deduplicated per component, cached with the conversion, and restricted so a hook cannot rewrite the document
  concurrency: v0.3.5 added GenerateOptions.ConversionWorkers, excluded from the hashed options because it changes wall clock and never bytes
  bookkeeping: produced files are declared artifacts, and the read set is recorded per run so an edited import regenerates
  division: the module matches, rewrites, and records; the caller owns every codec, format, name, and switch, per requirement:derived-asset-pipeline
  module_non_goals: bundling, minification, a format table, and any runtime negotiation
generator:
  extensible_analysis: requirement:httpbinder-extensible-route-analysis
  openapi:
    - package fragments register during import
    - AssembleOpenAPI returns one deterministic merged JSON document; YAML output was dropped
  configuration:
    - generated definitions register during import
    - ScaffoldTOML and ScaffoldEnv merge every package definition
  html:
    - .pw.html components generate immutable htmlbind.Fragment values
    - components with an unnamed slot also generate htmlbind.Wrapper binders
    - api:render-html-chain composes wrappers around a leaf
    - "`external async` declarations bind Go functions returning a value and an error"
    - "`async T` parameters and record fields become htmlbind.Pending[T], surfaced by api:async-html-value"
    - await, fallback, and recover clauses compile to boundaries described by api:html-boundary-protocol
    - the generated plan carries a constant HasAwaitBlock flag used by decision:automatic-async-render-selection
    - the async render path emits placeholders and bare fragments; completion framing and the client runtime belong to the framework
    - "`external live` declarations bind Go functions of the shape func(ctx, args...) iter.Seq2[T, error], with the context mandatory rather than optional"
    - a live binding sits in an ordinary await clause, so there is no second clause keyword and one clause may mix a live binding with a settle-once one
    - "RenderChain renders a live boundary from its first delivery; RenderChainAsync commits the first delivery and unsubscribes; RenderChainLive keeps delivering and does not end"
    - the entries that must answer bound how long a boundary may show nothing, and running out leaves the fallback rather than rendering recover
    - HasLiveBlock reports whether a chain keeps changing after the document ends, a subset of HasAwaitBlock
    - Content.AppendJSON writes one delivery as a JSON record, escaped for a script context as well as a JSON one
    - boundary ids became positional, so a nested boundary is tb-1-1 and the same chain executed again produces the same ids; api:html-boundary-protocol carries what that changes here
    - "the live entry does not enforce that the body writer is discarded, so passing a real writer produces an endless document response"
    - a live failure reaches the error reporter after the delivery lock is released, from v0.2.8; before that a blocking reporter held the clause's goroutines
    - nothing states which boundary is live, so requirement:live-boundary-liveness-signal is still answered by the framework's own bookkeeping
    - a live render executes the whole composed chain, so requirement:live-mode-plan-slice is still paid per reconnect
  html_update:
    - the htmlupdate package holds every net/http concern of partial updates, so htmlbind stays free of it and generated plans keep working on TinyGo and WebAssembly targets
    - every layout and page of a rendered chain is an update boundary automatically; an ordinary component call is not, and the document shell never is
    - a boundary must render exactly one root element, and a component that cannot is simply not a boundary rather than a generation error
    - two keyed digests per boundary, the frame validator over its own bytes excluding nested boundaries and the input validator over its declared parameters; the frame one is the authority for omitting a boundary
    - a delta skips transmission and never execution, so only a component opting into output caching skips its own render
    - Options carries the validator key, the header prefix, the path prefix, the build identity, and the manifest size cap, and pw wraps it as api:html-update-options
    - Negotiate resolves anything unrecognized to a complete document, which is what lets a live token share the header per decision:update-runtime-convergence
    - Mount installs the runtime asset and the redraw endpoint under one path prefix; pw serves its own merged asset and takes only the redraw route
    - a reloadable modifier on a component declaration generates a typed query decoder and a registration value, consumed by requirement:reloadable-component-endpoint
    - Registry.Register panics on a repeated kind, because the kind covers name, parameters, and markup but not the package
    - WantsUpdate, WriteUpdate, WriteUpdateStatus, and WriteNavigate are the action-response surface requirement:action-response-update branches on
    - the generator gained a data attribute prefix option naming the boundary attributes, which pw sets to its own brand
    - from v0.3.1 a render option names the async placeholder element and the boundary identifiers from that same prefix, so one document no longer holds two spellings
    - from v0.3.1 the browser runtime source, its asset form, and its naming configuration are exported, and serving it is switchable, so a framework composes it into its own asset instead of copying it
    - the runtime is a factory reading its attribute prefix, header namespace, endpoint prefix, and installed name from that configuration; only the protocol version stays compiled in, and an empty installed name installs no global
    - the author-written preserve and ignore markers follow the configured prefix, so no application template carries the module's name
    - Mount takes a one-method router interface satisfied by api:serve-mux, registration returns an error beside a must-variant, and an options validator reports every unusable option at once
    - a failure callback receives every refused redraw with a kind, status, message, cause, and the component and instance it named, so a refusal reaches api:error-renderer and requirement:modern-observability
    - the redraw response carries a keyed ETag with a private, no-cache policy, so an unchanged region answers 304; the policy, the query bound, and the stream media type are all options
    - builtin element registration is unimplemented at v0.3.0 and lands in v0.3.3, so a framework-supplied element had no registration seam until then
    - a synchronous external declaring a leading context.Context receives the render context, and one returning html lowers to a slot, which was the interim shape planned for a framework CSRF element
    - style and script blocks extract to content-hashed files under a configured public directory, unused until requirement:component-asset-extraction sets the options
  route_tree:
    - the routetree package discovers a directory tree and writes the registrations, which is the opposite direction from the registered-router analysis above
    - one run covers one tree; requirement:discovered-page-routing wraps it and flow:page-route-generation drives it
    - reserved file names, generated file names, called symbols, five named blocks, and three whole-file templates are all configurable, which is the seam decision:page-render-binding uses
    - the render block carries every in-scope identifier by name, including a chain that is nil for a page with no ancestor layout, so a framework reshapes the call instead of renaming it
    - the router type and its constructor are their own symbols, separate from the package supplying Request, and an empty constructor name omits the constructor
    - the composer entry takes a configurable writer type and an optional request parameter, with imports derived from the signature
    - a query input declared with a trailing question mark binds a pointer, so an absent value is distinguishable from a zero one
    - the discovered tree reports its package list, which is what lets a framework run request binding over route packages
    - discovery skips files carrying tinybind's own generated header, plus any header prefix the run registers, and deliberately does not skip another tool's generated code
    - the emitted header is settable and pairs with an accessor returning the prefix to register, so a branded output cannot drift from what discovery recognizes
    - the failure entry a generated handler writes through is a symbol, so a framework naming it something else needs no template override
    - a package with no bindable model reports a wrapped sentinel error, so a loop over many packages can skip it with errors.Is
    - an action resolver answers a server-action name the current tree does not hold, which is what would let a flat template use one
    - htmlbind.Signatures and htmlbind.ActionRefs expose component parameter types and template action references for a framework generating around a template
  sql:
    - decision:tinybind-sql-runtime owns statement plans and shared execution
    - declared sql.exec, sql.one, sql.optional, or sql.many selects Exec or Query
    - incompatible SQL result contracts fail generation
    - a SQL dialect option selects the target engine, required from v0.2.2 with no default
    - the dialect carries the placeholder style, dollar for postgresql and question for mysql and sqlite
    - postgresql, mysql, and sqlite from v0.2.3, which covers every decision:server-sql-support-tier first-class engine
  dynamo:
    - the dynamobind runtime package and a generator mode, from v0.2.8, consumed by requirement:dynamodb-generation
    - a dynamo struct tag names the attribute and its options, and an unknown option is a generation error rather than a silently ignored string
    - each tagged type yields EncodeItem, DecodeItem, ItemKey, and a table definition constructor, with compile-time assertions against the runtime interfaces
    - codec emission is usage-directed, so a type gets only the halves a discovered call needs; the key builder and the table constructor are emitted whenever a partitionkey tag exists
    - the table constructor carries the name, the partition key, and the optional sort key; billing mode, capacity, and secondary indexes are left zero
    - dispatch is static, so no registry or init entry is emitted and an unused type links nothing
    - the operation helpers take a driver client argument and pass driver errors, retries, and page boundaries through untouched, which is why decision:dynamodb-no-runtime-abstraction wraps none of it
    - from v0.2.9 a query declaration file generates one named function per access pattern, consumed by requirement:dynamodb-typed-queries
    - from v0.2.10 the client is carried in the context and set with a client setter; from v0.4.1 a parameter form ("On"-suffixed entries taking a Handle) and a DynamoHandleResolver generation option exist beside it, and Popcorn Web uses those two per requirement:context-lookup-performance
    - the same setter takes an optional table resolver function, run inside every runtime entry, which is the seam rule:dynamodb-table-naming installs
    - a declaration carries a required table clause, so a generated query names neither a client nor a table
    - a missing client is a named error rather than a panic, so an entry reached without the middleware fails as an ordinary error
    - the declaration suffix is configurable through DynamoTemplatePattern and the output file name through DynamoQueryName, so Popcorn Web brands both without renaming anything after generation
    - a declared query's attribute names are checked against the tags, and every attribute is aliased unconditionally so no reserved word reaches an expression literally
    - the string key-condition form remains as an unchecked escape hatch
    - table definition emission is suppressible as the named feature item-table, and the whole mode as item-codec
    - single-table design is a stated non-goal, so one struct owns one table
    - a version tag for optimistic locking and a ttl tag are proposed, the latter blocked on the driver
    - no update or condition expression is generated, and secondary index tags are deferred
    - from v0.4.1 DynamoHandleResolver and FirestoreHandleResolver select a framework function answering the Handle, reopening the earlier no-resolver reading; parameter mode covers the library entries a generation option cannot touch
  formatter:
    - the templates/templatefmt package canonicalizes a template source, from v0.3.1, consumed by requirement:template-formatting
    - "Source and SourceAs are pure functions over a byte slice, so an embedder needs no filesystem, no process, and no project"
    - SourceAs names the format explicitly, so the .pw suffixes need no pattern configuration on the editor path; Dir and Identify take the HTMLPattern, SQLPattern, and DynamoPattern options instead
    - a parse failure is carried on the result with the formatted output left nil, so a broken source is never partially rewritten
    - the fmt subcommand is a thin wrapper over the library, with a stdin filter mode selected by -as and a -l listing mode for CI
    - the printer is delegated the same way the parser is: the shared package prints the header and the expressions, each format package prints its own body
    - two spaces per level, a declaration body opening exactly one level, and a soft 100-column width
    - it will not sort, deduplicate, or rewrite one construct into another; SQL keyword case and HTML self-closing syntax are left as authored
  formatter_defects_reported_and_fixed:
    found_in: v0.3.1, formatting every .pw source in this repository, 2026-08-02
    fixed_in: v0.3.2, verified the same way the same day
    non_idempotent_raw_text_escape:
      was: a literal brace run in a script or style body gained one brace pair per formatting pass and never converged
      now: a raw text brace is written back as it stands, because the parser already keeps it as text; only a brace the insertion gate would read as syntax keeps its escape
      note: upstream also drew the pre, textarea, and preserve-whitespace boundary, which are whitespace-preserving but still template text and so still escape
    sql_upsert_split:
      was: "ON CONFLICT(id) DO UPDATE SET was broken across three lines"
      now: the clause absorbs its action keywords and stays on one line
    both: reported upstream rather than worked around here, because a local workaround would have been a second layout implementation
  formatter_idempotence_guard:
    from: v0.3.2
    what: Source and SourceAs format twice and return an error rather than a result that differs between the passes
    where_it_belongs: upstream, which has the AST; it replaces the equivalent check requirement:editor-formatting carried in the extension
    version_floor: an embedder relying on it rather than repeating it must pin v0.3.2 or later
cachekeybind:
  shipped: v0.5.9, answering the blocking ask of requirement:data-result-cache in one round
  package: stdlib-only, holding the CacheKey interface and its own framing helpers, so an application caching a JSON call links no render runtime
  tag: `cache:"key"` marks a field and is the only value the tag takes
  emitted: one CacheKey method per type plus a per-type interface assertion, with the identity derived as package path and type name
  two_changes_against_the_ask:
    opt_in_rather_than_default_include: refused with a reason this framework had not carried — the owner passes a storage entity as-is, whose fields are mostly the result, so default-include would build the key from the value the lookup exists to avoid fetching
    no_version_at_all: refused because a version is a number an author must remember to raise, and because the module states its cache runtime never invalidates, so a version is a deployment lever declared in a library
  correction_taken: the ask cited the dynamo tag as precedent for default-include; the module carries both polarities and firestorebind is opt-in, so the precedent decided nothing and the half named lost
  discovery: usage-directed, so a key type is emitted for the types a registered call actually receives; this framework registers Get, Has, Set and Invalidate on pwruntime.CacheStore as method patterns with the key as argument index 1, since 2026-09-02, and registered the four Memo functions with the key at index 2 before that
  helper_home_resolved: cachekeybind frames its own rather than forwarding htmlbind's, which would have added a dependency to a shipped render runtime; the split also made its helper set wider on integer and float widths
generic_methods_available_today:
  shipped: v0.5.9, the second ask of the same round
  what: Require on Builder, Bind and BindWrapper on Plan, each introducing no type parameter beyond its receiver's own
  old_forms: kept as deprecated wrappers, so nothing here had to move; this framework's remaining call sites are in tests
generic_methods_migrated:
  shipped: 2026-09-02 in the upstream working tree, against the deferred list this framework filed in requirement:typed-api-method-convergence, once Go 1.27 and TinyGo 0.42 were the baseline on both sides
  what: Load, LoadAll, QueryPage, QueryKeysPage and Count on Tx; every On entry as a method on Handle in dynamobind and firestorebind, keyless ones included; For, ForCtx, Await, Live and Provide on Builder; ParseSlice, ParseMap and ParseArray on Parser; AppendValues on Builder
  old_forms: removed, at the owner's instruction, because deprecated wrappers doubled the godoc; Require, Bind and BindWrapper went with them, and so did the Val family that had the same shape
  discovery: a Method pattern beside each Function pattern, so the handle spelling generates what the On spelling did, and tx.Load[T] what LoadTx[T] did
  generated_output: the emitters write the method spelling, so the generated files here regenerate on the go.mod bump
  reaches_here: on the next release and go.mod bump; the storage guides here still show the On spelling until then
value_binding:
  shipped: v0.5.10, answering the change request this framework filed 2026-08-14
  what: '{val name = expr}, a closerless directive scoped to the end of its enclosing block, in both .pw.html and .pw.sql'
  keyword: val rather than the let or var the request spelled, because the binding is immutable and let is JavaScript's reassignable half
  shape_chosen_against_the_request: the statement over the block, on the owner's ground that a bound subtree would indent for a reason the markup does not have; the desugaring that gives analysis its subtree is a normalization pass rather than a parser change
  bindings_are_independent: the comma form binds several names that cannot read each other, matching await; a dependency is two directives
  unread_is_an_error: in both formats, because the value is computed before anything reads it and an external may only answer a query
  widened_by_the_owner: SQL, which the request did not ask for; a normalization named in a WHERE and again in an ORDER BY ran twice there for the same reason
  what_it_changes_here: requirement:component-output-cache stops being a markup-only cache — a component may take an identifier, load inside itself, and cache the load and the render as one unit
  what_it_does_not_change: a synchronous external still has no error result, which is what bounds the shape above; upstream tracks that as an open question
  multi_value_withdrawn: the request's own `a, b = f()` was dropped before implementation, so nothing here destructures
value_binding_error_and_hoisting:
  shipped: v0.5.11, adopted at v0.5.12
  failing_external: a synchronous external whose Go implementation returns a trailing error may be called only as the whole value of a val binding; the template declaration is unchanged and the error is read from the Go source, exactly as a leading context is
  hoisting: a binding is evaluated at the top of its block, and a chain leaf's top-level bindings run during assembly, so a failing loader still chooses the status while the rest of the page streams
  error_reaches_us_unwrapped: nothing in the render path wraps it, so an error carrying HTTP intent is recognised by this framework's existing problem path with no change
  not_for_a_cached_leaf: a plan carrying a storing cache policy is not hoisted, since the prologue runs during assembly and the store is consulted during the render; a cached page gives up the status choice on a miss and nothing on a hit
  typed_page_rung_removed: v0.5.11 refuses the loader signature the discovered router used to call, which requirement:explicit-page-loading adopts here
  settled_boundary_cache: a storing annotation now accepts a component reaching an await boundary and refuses only a live one, which is the line this framework asked for
boundary_root_defect:
  in: v0.5.11 only, fixed in v0.5.12
  what: hoisting nested a component's root element inside the binding node, and the single-root walk refused an unrecognised node, so a component with a script block failed generation and a page or layout silently stopped being an update boundary
  why_it_mattered: the silent half landed on the shape v0.5.11 made mandatory, since every loading page now uses a binding
  found_by: migrating the page-tree fixture here and reading the generated output rather than the release notes
  reported: docs/tinybind-go-v0.5.11-boundary-root-defect.md, with the cause located and a fix suggested; the fix shipped in that shape
  adoption_held: this framework stayed on v0.5.10 until v0.5.12 rather than take either a broken build or a silent degradation
typed_server_actions:
  shipped: v0.5.10, unadopted
  effect_here: the generated page tree route entry gained Published and Typed fields, so internal/pagesfixture regenerated with Typed false throughout; no behavior moved
blocked_on_generic_methods:
  what: five surfaces are package functions only because a Go method cannot take type parameters — the firestorebind transactional reads, the dynamobind and firestorebind On entries, the htmlbind builder operations carrying an extra type parameter, the jsonbind parser's ParseSlice and ParseMap, and sqlbind AppendValues
  why_it_is_this_framework_s_concern: none of them is wrapped here, so each is what an application author writes; requirement:typed-api-method-convergence holds the intent, the priority, and the migration shape
  request: filed unacted upstream 2026-08-13 and deliberately not started; the trigger is a Go release carrying methods with type parameters and a TinyGo release carrying that Go, since this framework targets both
  reason_already_in_the_source: the transactional read's comment names the constraint, and separately refuses a context-carried handle because one call site would then mean two things depending on which context reached it
constraints:
  - a route tree directory name must be a legal Go import path element, per rule:page-directory-naming
  - generator executes with host Go
  - generated mapping path avoids runtime field reflection
  - route discovery analyzes same-package registrations recognized by versioned adapters
  - normal handwritten application code does not import TinyBind
compatibility:
  route_tree_v0_2_4: the routetree package and server-action lowering are additive, so the pin moves from v0.2.3 without touching an existing handler, template, or query
  route_tree_v0_2_5:
    additive: every new seam is additive and the default output is byte-identical, so the pin moves again without regenerating differently
    behavior_change: discovery now skips tinybind's own generated files, which removes registrations a run could previously read back out of a generated registry
  route_tree_v0_2_6:
    additive: the header and the failure selector become configurable with defaults matching what v0.2.5 emitted
    resolves_for_pw: api:cli-generate writes its own generated header, which the v0.2.5 filter could not recognize; registering the prefix is now the supported answer, so page tree output keeps the Popcorn Web brand
  sql_v0_2_2: the SQL dialect became a required generation input, so a run that discovers a .pw.sql without one is a configuration error rather than a silent postgresql assumption
  sql_v0_2_3: the sqlite dialect is additive and emits the question placeholders sqlite already generated through mysql, so naming it changes no generated output
  dynamo_v0_2_8:
    additive: dynamobind is a new package and a new generator mode, so nothing an existing project generates changes
    module_graph: the module now requires system:tinygodriver v1.1.3, because a runtime package imports the DynamoDB client rather than only an example doing so
  dynamo_v0_2_9:
    additive: the query declaration is a new source kind and a new output file, so a project generating only codecs regenerates identically
    answers: the downstream Popcorn Web request, whose allocation decision:dynamodb-framework-scope records
    closes: the read-path drift requirement:dynamodb-generation could not close on its own
  dynamo_v0_2_10:
    breaking: every runtime entry lost its client parameter and a declaration gained a required table clause, so v0.2.9 call sites and declarations both need editing
    scope_for_pw: nothing was released against v0.2.9, so the change costs an edit to these concepts rather than to a project
    size: about 37 KB on a TinyGo wasip1 build, from the context value and the assertion reading it back
    answers: the second downstream request, and answers it by removing the seam rather than adding one
  v0_3_1:
    additive_for_generation: templatefmt and the fmt command are new surfaces, so a project that never formats regenerates identically
    cost_of_adopting: every one of the 33 .pw sources in this repository changes, almost all of it the body indent the formatter adds and this repository never wrote
  v0_3_2:
    taken_for: the unguarded position lookup above, which crashed api:cli-generate on a file an editor had created and not yet written into
    arrives_with: the boundary emission requirement:navigation-delta-rendering consumes, whose activation is opt-in per component except for generated route layouts, which take it automatically
    effect_on_pw: a concept:page-tree component now emits a boundary marker attribute and one update-manifest entry; the rendered document gains an attribute and loses nothing
    measured: one page tree fixture regenerated, and the rest of the suite passed unchanged, so no Popcorn Web source needed editing
    superseded_by: v0.3.3 and the adoption decision:update-runtime-convergence records, so the markers are no longer inert; requirement:module-native-csrf is the half taken first
    formatter: the idempotence guard, which requirement:editor-formatting relies on instead of carrying its own, and which requirement:template-formatting needed before a repository-wide run was safe to repeat
  v0_3_5:
    formatter_fixes: the two defects reported against v0.3.1, a raw text escape that never converged and an ON CONFLICT clause split across three lines
    pin: the version this repository runs, reached independently by decision:update-runtime-convergence and by decision:tinybind-v03-adoption
  html_v0_1_15: generated HTML APIs are not source-compatible with earlier direct-writer output
  html_v0_1_19: async parameters and async render entry points are additive, so existing templates and call sites keep compiling after regeneration
  html_v0_1_20: Content.WriteTo narrows to the bare fragment and the module injects no client runtime, so an async caller must supply framing and a runtime it previously inherited
  html_v0_3_0:
    additive_on_the_wire: a project that never sends the render header renders and serves exactly as it did, so the pin moves without regenerating differently
    generated_output: boundary attributes and validators appear on layout and page roots, which changes generated markup for every page even when no update is enabled
    duplicated_here: the module shipped a browser runtime, a header namespace, and an endpoint prefix that overlap what this framework already owns; decision:update-runtime-convergence decides what happens to each
    not_a_drop_in: the shipped runtime was built for the upstream names, so adopting the transport without adopting the names would have cost an adapted copy of its source
    superseded_by: v0.3.1, so this version is never the one to pin
  html_v0_3_1:
    answers: requirement:tinybind-runtime-ownership in full, which is what makes the transport adoptable without a copy
    generated_go: unchanged, so the pin moves without regenerating differently
    breaking_for_a_direct_user:
      - the preserve and ignore attributes default to the module's short prefix rather than its full name
      - the query bound and stream media type constants were renamed as defaults, having become options
      - registration returns an error rather than panicking
    breaking_here: none, because nothing was released against v0.3.0
    upstream_correction: the embedded runtime was an interim shape its own rollout requirement had recorded, whose exit was never scheduled, rather than a reversed boundary; the effect downstream was the same and requirement:tinybind-runtime-ownership carries the correction
    still_interim_upstream: the module serves an asset by default and retires that only when its own runtime bootstrap selects and injects one; this framework declares caller ownership, so the default never applies here
  html_v0_3_2:
    additive: an action response gained a head field and the rest is documentation, so nothing generated or served changes for a project that does not use it
    action_head: each written region's own contributions are collected and deduplicated across the set; the browser already installed a delta's head before applying operations, so only the server was never filling it
    live_transport_confirmed: the module's document render settles a live boundary in place and finishes the response, and a second connection carries deliveries, which is this framework's own shape rather than a divergence
    live_token_still_absent: no live token is parsed on either side, and the shipped upstream runtime sends the navigation token for both the first connection and every reconnect; filed as a must-priority requirement recommending a live token, which is this framework's existing choice
    still_open: the redraw response carries no head, the slot-carried fragment head defect, and what a fragment response owes a caller it cannot deliver to, each filed as its own requirement rather than settled
  html_v0_3_3:
    answers: every remaining item of requirement:tinybind-update-composition-seams, and moves CSRF into the module
    live_mode: a live token of its own with its own negotiated mode, so subscriptions stay open only in that mode; termination reasons name final, live-pending, failed, done, and retry, a retry may carry a server-side delay hint, the head record carries the build, and a cancelled context closes as retry rather than done
    live_handoff: a response header, a delta body field, and a stream terminator each say whether a live connection is expected, and none appears when the page has no live boundary
    adopted_from_here: the done-versus-retry distinction, the build on the opening record, and resetting the attempt count on a healthy close were this framework's shipped behaviour, offered as input and taken
    live_validators: a delivery carries none and the opening delta does, which answers the question that item left open
    live_defect_fixed_upstream: the live entry had set subscriptions unconditionally, so an ordinary navigation delta on a live route never terminated
    redraw_head: the registry reports the head and assets of every published component for the shell to install once, and a redraw that contributes head announces it on a response header; the body stays a bare subtree
    asset_set: an asset value on the plan with fragment, wrapper, and merge accessors, readable before rendering and folded through slots
    slot_head: the plan reaches fragments carried in parameter structs, so head, sources, assets, and capability flags all fold; a project declaring no html parameter regenerates identically
    vary_axes: a composition reports the request properties its output varies on, so a response can set an honest Vary header rather than guessing
    builtin_elements: a framework registers hyphenated elements that lower to plan steps at generation time, with the value never entering template scope
    csrf: consumed by requirement:module-native-csrf
    protocol_version: deliberately left at 1, because nothing has shipped under it and spending a bump would cost the first real deployment one wasted fallback
  breaking_v0_3_3:
    hyphenated_elements: the namespace is closed, so a project writing Web Components must declare them; requirement:custom-element-registration carries what this framework owes projects
    cached_unsafe_form: a component holding an unsafe form can no longer be output-cached, which policy:csrf-protection records
    scaffolds_unaffected: no template this framework scaffolds writes a hyphenated element or caches a form, so a scaffolded project regenerates identically
  not_built_upstream_v0_3_3:
    - the opaque builtin element shape, declined because the trust assertion would move into framework code
    - a builtin element inside a head declaration, so head placement means only that the body position is refused
    - the embedded asset byte table and the caller-supplied URL function, which is what requirement:component-asset-extraction needs for a TinyGo target
    - a server-side lifetime bound calling the retry seam, left to the caller
    - the redraw head bound as an option, since registration cannot reach the options value
  known_flake_upstream: a superseded live delivery can report a stale value into a reused placeholder, reproduced on the baseline and predating v0.3.3, so a cancellation-ordering race rather than a regression
  other_v0_3_1_features:
    template_formatters: formatters for the html, sql, and dynamo template languages, unevaluated here and a candidate for api:cli-generate or a project check
    asset_transform_hooks: build-time rewriting of referenced assets through registered hooks, which is the seam requirement:component-asset-extraction would build on if that work is taken up
```
