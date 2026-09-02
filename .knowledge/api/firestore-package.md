---
id: api:firestore-package
type: api
title: database/firestore Package
---
Importing github.com/shibukawa/popcornweb/database/firestore registers the Firestore configuration binding and opens the client into process state; operations are system:tinybind firestorebind's methods on the handle this package exposes, and generated queries resolve the same handle themselves.

```yaml
import: github.com/shibukawa/popcornweb/database/firestore
import_style:
  form: a normal import, following decision:import-registered-session-plugins for the registration half
  effect_of_importing: data:firestore-runtime-config appears in the configuration schema and the extension registers itself from init
  effect_of_not_importing: no configuration key, no linked driver
  boundary: this package registers nothing into the rule:rdb-dsn-resolution engine registry, because a Datastore endpoint is not an rdb DSN
client_supply:
  process_handle: setup builds one firestorebind Handle from the client, held as process state; no per-request middleware exists and no context node is installed, per requirement:context-lookup-performance
  accessor: Handle(ctx) returns it reading no context on the common path; when the process holds no client, a handle installed with firestorebind WithClient or WithHandle is honoured, which is the unit-test seam
  no_table_resolver: the option dynamobind needs has no counterpart, per decision:firestore-namespace-isolation; the namespace is a client option set once
  missing_client: firestorebind returns ErrNoClient rather than panicking, so a call that ran without the extension fails as an ordinary error
surface:
  - Handle(context.Context) (firestorebind.Handle, error)
  - Client(context.Context) (*datastore.Client, error), for an operation firestorebind does not wrap
  - EnsureClient(context.Context) (context.Context, bool), for code handing a context to something still calling context-form firestorebind entries
  - Kinds() []KindInfo, the linked framework kinds and the timestamp property each expiry policy points at, read from the firestorebind Expirer each store type implements rather than from a list maintained beside them
binding_entries_the_stores_use:
  reads: [Load, LoadAll, QueryPage, QueryKeysPage, Count]
  writes: [Store, Insert, Update, Remove]
  transactions: [Run, Tx.Load, Tx.Store, Tx.Insert, Tx.Remove]
  handle: each store resolves Handle once per operation and calls the method on it, so no context value is read
  what_this_asks_of_a_store_type: EncodeEntity, DecodeEntity and EntityKey on the internal record type, hand-written since these types are not generated; a version field adds EntityVersion and makes a conditional write automatic
  why_not_the_driver_directly: the typed entries stamp the resolved namespace onto every key, and the driver does not
escape_hatch_hazard:
  what: firestorebind ClientFromContext returns the client with no namespace applied
  effect: a store reaching the driver through it writes into the default namespace, which decision:firestore-namespace-isolation depends on not happening
  answered: firestorebind v0.3.6 exports KeyFor and KeysFor, with handle forms (methods on Handle since v0.5.28) which stamp the resolved namespace and leave an explicitly placed key alone
  rule_here: a call site on the driver path passes its keys through KeyFor rather than reimplementing the resolver, and this package writes no helper of its own for it
  where_it_still_bites: nowhere in these five stores, since RemoveKeys removed the one operation that had no typed form
deliberately_absent:
  migrate_and_plan: there is nothing to apply, per decision:firestore-no-schema-application
  register_kind: nothing has to be enumerated for a migrator to find, so decision:dynamodb-table-registry has no counterpart; Kinds reports what is linked for the guide and the CLI to print, and no code reads it back
  operation_wrappers: none, on the reasoning of decision:dynamodb-no-runtime-abstraction, which is why the handle methods and the transactional reads an application calls are covered by requirement:typed-api-method-convergence rather than by anything reshapeable here
  transaction_surface: none of its own; the firestorebind wrapper of system:tinybind binds the driver's
why_ensure_client_is_here_and_was_added_late_for_dynamo:
  fact: requirement:dynamodb-auth-backend found that a store reading its client from the setup context never gets one, because setup carries no request
  effect: this package exposes EnsureClient from the start rather than after the same failure
lifecycle:
  startup:
    - construct the client from data:firestore-runtime-config and mint one token, so a bad credential fails before serving
    - one lookup of a reserved key, which is the mode and reachability probe of decision:firestore-datastore-mode-only
  readiness:
    what: the same reserved-key lookup
    why_not_a_listing: there is no ListTables counterpart and no ping, so a probe has to be an ordinary read
    cost: one small read per probe, which the guide states so a deployment can set the interval knowing that
  shutdown: Close the client through api:application-lifecycle, unless decision:dynamodb-observability-seam supplied the HTTP client, which the driver then leaves alone
diagnostics:
  unauthenticated_at_startup: reported with the clock named as a likely cause, per system:tinygodriver-firestore, because the status alone points at the credential and the credential is usually fine
  failed_precondition_at_startup: reported as a mode error, never as a missing index, per decision:firestore-datastore-mode-only
  permission_denied: reported with the project, the database, and the service account identity, and never with the key
constraints:
  - no operation wrapper, error type, or option type of its own
  - the client is fixed at startup and is neither replaced nor reopened per request
  - a test installs a handle on its own context, not a second signature
  - no request this package or any store over it issues produces a query-diagnostics record, per policy:query-log-safety
  - credentials, the key file contents, and a static token never reach a log, an error, or policy:startup-summary
related:
  - requirement:firestore-store
  - data:firestore-runtime-config
  - api:dynamo-package
```
