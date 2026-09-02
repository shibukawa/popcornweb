---
id: requirement:typed-api-method-convergence
type: requirement
title: Typed API Method Convergence
---
Every typed operation a project writes as a package function only because a Go method cannot take type parameters becomes a method once the language allows, and this is where that intent and its upstream requests are held.

```yaml
status: landed on both sides 2026-09-02; the module moved to go 1.27.0, the three sites built here dropped their build tags and their functions, and system:tinybind carries the requested five as methods with the old functions removed and its emitters moved
constraint: until Go 1.27 a method could not declare its own type parameters, so an operation needing one was a package function whatever the design preferred
expected: was Go 1.27 with TinyGo 0.42, and that is what shipped; both are the baseline since 2026-09-02
why_it_is_held_here:
  the_surface_is_this_framework_s: api:dynamo-package and api:firestore-package wrap no operation, per decision:dynamodb-no-runtime-abstraction, so an upstream package function is literally what an application author writes
  the_flow: this catalog carries the intent, the priority, and the shape; the upstream change is requested from here when the work is taken, the way system:tinybind already records input offered and taken
migration_shape:
  method_becomes_the_body: the existing package function stays as a deprecated wrapper calling it
  additive: a project moves call site by call site and no compiler error forces any of it
  nothing_stored_moves: each of these is a call shape, so no generated artifact, stored entry, or wire format changes
written_ahead_behind_a_build_tag:
  what: the three sites built here carry their methods already, in files tagged go1.27 beside the code they extend, so the release is a flip rather than a design
  where: pwruntime/cache_go127.go, testutil/testutil_go127.go and session/registry_go127.go, each with a test file beside it
  invisible_until_then: an older toolchain does not select the file, so today's build is unchanged; TinyGo reports the release tags of the Go it accepts, so both builds cross at one point
  the_tag_names_a_release_rather_than_the_feature: if 1.27 arrives without methods taking type parameters the tag has to move to the release that does carry them, because a consumer building on 1.27 would otherwise take a file that does not compile
  how_it_was_checked: no toolchain parses the syntax yet, so each file was mechanically lowered — the receiver becomes the first argument — and its tests run in that form; the delegation is verified and the syntax is not
  what_the_release_still_costs: the functions keep the bodies and the documentation, so the flip moves each body onto its method, marks the function deprecated, and updates the public page, which still describes all of this as waiting
  retired: 2026-09-02; the tagged files were folded into the files they extended and the tags removed, and the functions went rather than staying deprecated, see below
landed_2026_09_02:
  module_line: go 1.27.0, which is what let the tagged files stop being tagged; a consumer building on an older Go is refused at the module line rather than at a method declaration
  here:
    memo: Get, Has, Set, Invalidate, InvalidateScope and InvalidateTag on *pwruntime.CacheStore carry the bodies; Memo, MemoHas, MemoSet, MemoInvalidate, MemoInvalidateScope and MemoInvalidateTag are deleted from pwruntime, pw and pwfast
    test_configuration: Get, Set and Update on *testutil.Config carry the bodies and the functions are deleted; Update infers its type from the edit's parameter, Set from its value, and Get still writes it
    session_registry: Register on *session.Registry carries the body and the function is deleted; pw.RegisterStore, which every application goes through, calls the method and changed no signature
    discovery: pwgen registers the four cache methods as Method patterns on pwruntime.CacheStore with the key at argument index 1, replacing the four Function patterns on pw with the key at index 2; the wrappers fixture calls store.Get and still yields the key type
    why_deleted_rather_than_deprecated: each function was a one-line delegation to a method that already existed behind the tag, so nothing could drift, and every call site in the module is this framework's own; the migration_shape above was written for the upstream half, where a caller is another project
  upstream:
    shipped: the transaction reads on Tx, the On entries on Handle, For, ForCtx, Await, Live, Provide and the Val family on Builder, ParseSlice, ParseMap and ParseArray on Parser, and AppendValues on Builder, with the old functions removed at the owner's instruction, per system:tinybind
    extra: the keyless firestorebind twins and the two keyless transaction reads moved with their generic siblings, for the reason InvalidateScope moved here
    generated_output: moved upstream the same day; a generated <Name>Tx twin, plan, decoder or SQL spells the method, so every _pw_gen.go here regenerates on the go.mod bump
    reaches_this_framework: on the next tinybind release and go.mod bump; until then the storage guides here describe the spelling that release will carry
  public_page: website appendix road-to-v1 rewritten from waiting to landed, in both languages, with the data cache, testutil and session sections showing the removed spelling beside the current one
sites:
  firestore_transaction:
    priority: first, and the only one whose value is more than tidiness
    now: writes are methods on the transaction while typed reads are package functions, so one transaction is written two ways in adjacent lines
    becomes: Load, LoadAll, and QueryPage on the transaction value
    why_it_matters: the transaction boundary stops being a parameter and becomes the receiver, so the API states what the call is inside
    already_recorded_upstream: the comment on the transactional read names the language as the reason it is a function
    survives_the_move: the same comment separately refuses a context-carried handle, because one call site would then mean two things depending on which context reached it; that reason is untouched, so the operation is reached through the transaction value either way
    owner: system:tinybind firestorebind
  handle_entries:
    priority: second
    now: a context-resolving form and an explicit-handle form stand side by side, the latter suffixed On, which api:dynamo-package and api:firestore-package both expose as the supported call
    becomes: the On form is a method on the concrete handle; the context form stays as it is
    extra_win: an operation whose type is inferable from its argument loses its type argument entirely — storing, storing many, and removing all read as plain calls
    dynamo: LoadOn, LoadAllOn, StoreOn, StoreAllOn, StoreReturningOn, RemoveOn, RemoveReturningOn, UpdateOn, QueryPageOn, QueryOn, ScanPageOn, ScanOn
    firestore: LoadOn, LoadAllOn, StoreOn, StoreAllOn, InsertOn, InsertAllOn, UpdateOn, RemoveOn, RemoveAllOn, QueryPageOn, QueryOn
    owner: system:tinybind dynamobind and firestorebind
  html_builder:
    priority: third, and the least visible
    partly_done: Require moved to the builder in system:tinybind v0.5.9 with the function kept as a deprecated wrapper, since it carried no extra type parameter; For, ForCtx, Await, Live, and Provide are the ones still waiting
    now: ordinary builder operations are methods while the four carrying an extra type parameter are package functions
    which: the loop and its context form, the await boundary, the live boundary, and the provider entry — ForCtx belongs here with For and was missed the first time this list was written
    becomes: methods on the builder, so generated code stops mixing two spellings in one plan
    smaller_because: this is generated output rather than authored code, so no application reads it
    owner: system:tinybind htmlbind
  test_configuration:
    priority: with the memo work, since both are this framework's own
    now: an isolated test configuration is a struct whose three typed operations are package functions taking it
    which: Get, Set, and Update of testutil
    becomes: methods on the configuration value, so a test reads config.Get rather than naming the package twice per line
    inferable_but_still_blocked: two of the three infer their type from an argument, and a method still may not declare a type parameter even when nothing has to be written, so all three wait together
  session_registry:
    priority: with the memo work
    now: Register takes the registry as its first argument because it carries a typed codec
    becomes: a method on the registry, with the type inferred from the codec
  json_parser:
    priority: with the html builder, since the caller is generated code
    now: the parser is a struct with a dozen methods, and the two operations parameterized on the decoded element are package functions taking it
    which: ParseSlice and ParseMap of jsonbind
    same_shape_as: the firestore transaction, one type written two ways in adjacent lines
    owner: system:tinybind jsonbind
  sql_builder:
    priority: last, being one function
    now: the statement builder has methods, and appending a typed value list is a package function taking it
    which: AppendValues of sqlbind
    owner: system:tinybind sqlbind
  memo:
    priority: fourth by value, first by readiness
    what: the typed data cache operations move onto the store handle, per decision:memo-store-handle
    already_prepared: the handle is introduced before the methods can exist precisely so this move edits no call site
    owner: this framework, which is why it is the one site not requested from anyone
requests_upstream: the firestore transaction, the handle entries, the html builder, the json parser, and the sql builder, each being system:tinybind's code and none of them reachable from here
built_here: the memo store, the test configuration, and the session registry
ruled_out_deliberately:
  an_interface_receiver: sqlbind ScanRows takes the row cursor, which is an interface, so the package cannot give it a method however the language changes; it stays a function
  the_context_forms: every ctx-resolving entry — the dynamo and firestore non-On forms, the configuration accessors, the session slot lookup — has no receiver by design and is the half that stays, per api:dynamo-package
  constructors: a store, jar, or codec constructor has nothing to receive on
  foreign_receivers: the response and websocket writers take net/http or fasthttp types, which no package here may extend
  process_registries: the configuration seed, bound, swap, and value entries address process state rather than a value
possible_without_the_language:
  what: three htmlbind entries introduced no type parameter beyond the one their receiver already carried, so each was a legal method in today's Go
  which: Require on the builder, Bind and BindWrapper on the plan
  shipped: system:tinybind v0.5.9, with each old function kept as a deprecated wrapper
  cost_here: none; the only call sites in this framework are in tests, which still compile
  why_separating_them_paid: bundling them into the blocked list would have hidden that they were available, and they shipped in the same release as the blocking ask rather than waiting on a language change
acceptance:
  - a project that migrates nothing keeps compiling, because every replaced function survives as a wrapper
  - the transaction boundary appears as a receiver rather than as an argument
  - no generated artifact, stored cache entry, or wire format changes in any of the four
```
