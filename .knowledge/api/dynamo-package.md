---
id: api:dynamo-package
type: api
title: database/dynamo Package
---
Importing github.com/shibukawa/popcornweb/database/dynamo registers the DynamoDB configuration binding and opens the client into process state; operations are system:tinybind's methods on the handle the package exposes, and generated queries resolve the same handle themselves.

```yaml
import: github.com/shibukawa/popcornweb/database/dynamo
import_style:
  form: a normal import, following decision:import-registered-session-plugins for the registration half
  effect_of_importing: data:dynamodb-runtime-config appears in the configuration schema and the extension registers itself from init
  effect_of_not_importing: no configuration key, no linked driver
  boundary: this package registers nothing into the rule:rdb-dsn-resolution engine registry, because a DynamoDB endpoint is not an rdb DSN
client_supply:
  process_handle: setup builds one dynamobind Handle from the client and the rule:dynamodb-table-naming resolver, held as process state; no per-request middleware exists and no context node is installed, per requirement:context-lookup-performance
  accessor: Handle(ctx) returns it reading no context on the common path; when the process holds no client, a handle installed with dynamobind WithClient or WithHandle is honoured, which is the unit-test seam
  ensure_client: EnsureClient remains for code handing a context to something still calling context-form dynamobind entries
  missing_client: system:tinybind returns a named no-client error rather than panicking, so a call that ran without the extension fails as an ordinary error
surface:
  - Handle(context.Context) (dynamobind.Handle, error)
  - Client(context.Context) (*dynamodb.Client, error), for an operation dynamobind does not wrap
  - Migrate(context.Context, ...MigrateOption) (Result, error)
  - Plan(context.Context, ...MigrateOption) ([]TableChange, error)
  - RegisterTable(declared string, def func(name string) dynamodb.TableDefinition)
  - WithTableResolver(fn) as a startup option, for a deployment rule:dynamodb-table-naming configuration cannot express
deliberately_absent:
  table_accessor: the resolver runs inside the runtime entry via the handle, so no call site resolves a name
  operation_wrappers: none, per decision:dynamodb-no-runtime-abstraction, which is why the handle methods an application calls are covered by requirement:typed-api-method-convergence rather than by anything reshapeable here
usage:
  item: "h, err := dynamo.Handle(ctx); h.Load[Reading](ctx, \"reading\", r.ItemKey())"
  query: "records.ReadingsSince(ctx, sensor, from), whose generated body resolves Handle through the DynamoHandleResolver generation option"
  parity: a declared query takes context and its parameters, exactly as a flow:sql-generation function does
  remaining_argument: an item operation still names a table, because it has no declaration to read one from; a declared query names neither
lifecycle:
  startup: construct the client, verify credentials and region, and fail before serving when either is missing
  readiness: ListTables with a bounded limit answers the readiness probe, since the driver ships no ping
  shutdown: Close the client through api:application-lifecycle, unless decision:dynamodb-observability-seam supplied the HTTP client, which the driver then leaves alone
migration:
  entry: Migrate and Plan implement requirement:dynamodb-migration in process
  names: resolved through the naming the process handle carries, built from configuration rather than from a request, so the CLI and a handler address one table
  parity: the same code path serves api:cli-migrate through decision:dynamodb-table-registry
constraints:
  - no operation wrapper, error type, or option type of its own
  - no transaction surface; the driver has none
  - the client is fixed at startup and is neither replaced nor reopened per request
  - a test installs a handle on its own context, not a second signature
  - credentials and endpoint never reach a log, an error, or policy:startup-summary unredacted
```
