---
id: requirement:firestore-store
type: requirement
title: Firestore Store
---
Firestore in Datastore mode is a third kind of store a project adds beside its relational database and beside DynamoDB, on the same terms requirement:dynamodb-store set: its own configuration section, no relation to middleware.rdb, and no runtime abstraction over the two key-value stores.

```yaml
motivation:
  - system:tinybind firestorebind and system:tinygodriver-firestore together make a typed Datastore path that builds under TinyGo, which is the same pairing requirement:dynamodb-store rests on
  - a deployment on Google Cloud that already pays for Firestore should not add a relational database for five framework tables, which is the argument requirement:dynamodb-session-store made for AWS
  - decision:tinygo-storage-direction points at deployments where a relational database is the odd dependency, and until now the only non-relational answer was AWS-shaped
placement:
  section: middleware.firestore, its own data:firestore-runtime-config
  combinations: rdb only, dynamo only, firestore only, and any pair or all three are valid
  engine_question: requirement:database-engine-selection keeps naming one SQL engine; this is a separate capability answer, exactly as DynamoDB is
  no_second_key: nothing chooses between DynamoDB and Firestore in general; each capability that can be backed by either names its own backend, per decision:auth-backend-selection and requirement:state-storage-tiers
mode: decision:firestore-datastore-mode-only
surfaces:
  package: api:firestore-package
  configuration: data:firestore-runtime-config
  session_backend: requirement:firestore-session-store
  auth_backend: requirement:firestore-auth-backend
  auth_stores: requirement:firestore-auth-stores
  ceremony_store: requirement:contrib-auth-state-firestore
  generation: requirement:firestore-generation
  tests: requirement:firestore-test-isolation
  isolation: decision:firestore-namespace-isolation
  conditional_writes: decision:firestore-conditional-writes
  expiry: decision:firestore-expiry-policy
  schema: none, per decision:firestore-no-schema-application
  observability: decision:dynamodb-observability-seam, which is an HTTP-client seam and not DynamoDB-specific; this driver exposes the same WithHTTPClient
  runtime_abstraction: none, on the reasoning of decision:dynamodb-no-runtime-abstraction
scope_of_the_first_cut:
  in: the five framework stores of decision:auth-backend-selection, plus the client, the configuration, and the middleware they need
  in_now: the application-facing codec and declared-query path too, per requirement:firestore-generation, built once the framework stores were serving
  order: the framework stores went first because they need only the client, the context seam and the typed entries, so they did not wait on the generator
upstream_dependency:
  binding: system:tinybind firestorebind, which owns the context client, the namespace resolution, the typed entity entries, and the typed transaction wrapper
  state_on_2026_08_05: released
  minimum_versions: tinybind-go v0.3.7 and system:tinygodriver-firestore v1.1.9, which is what v0.3.7 itself requires, so the two move together
  retired: the tinygodriver v1.1.3 requirement this repository carried before this
  what_this_repository_needs_from_it: WithClient, WithNamespace, Load, Store, Insert, Update, Remove, LoadAll, QueryPage, QueryKeysPage, Run, Tx.Load and the Tx write methods; nothing generated, per the scope split above
  no_fallback_needed: the earlier plan to carry our own context key if the package slipped is withdrawn, since the package exists
  asks_answered: every one, per the upstream_requests of system:tinygodriver-firestore, so RemoveKeys, KeyFor and the Expirer contract exist and none of the code they replace had to be written
bounded_by_the_stack:
  - no partial update, so a renewal rewrites a whole entity, per requirement:firestore-session-store
  - no condition expression, so every predicate over a stored value is a transaction, per decision:firestore-conditional-writes
  - no table admin, so nothing creates or verifies a kind, per decision:firestore-no-schema-application
  - no TTL on the wire, so expiry deletion is a policy the deployment applies, per decision:firestore-expiry-policy
  - no response integrity check, unlike the x-amz-crc32 layer of system:tinygodriver-dynamodb
  - a transaction costs one round trip more than the reads it performs, which is the lazy start of system:tinygodriver-firestore rather than a fixed three
  effect: these bound the stores by what the service and the driver offer, not by a Popcorn Web choice
tinygo:
  buildable: the client and firestorebind both build under TinyGo, so no part of this store needs decision:migration-execution-split delegation, and there is no migration to delegate anyway
  credential_cost: the JWT path links RSA signing, which is native on TinyGo builds and 583 KB against 1040 KB for the pure-Go path; a deployment using the metadata server links none of it
  guidance: on Cloud Run or GKE, name the metadata token source, which is both smaller and one fewer secret to carry
acceptance:
  - a project with a firestore section and no rdb section starts and serves
  - a project with all three sections opens all three and reports each in policy:startup-summary
  - an application that never imports api:firestore-package links no Datastore code and gains no configuration key
  - the same application source builds and runs under host Go and under TinyGo
  - a native-mode database is refused at startup with the mode named, per decision:firestore-datastore-mode-only
  - credentials and the credential file path never reach a log, an error, or policy:startup-summary unredacted
implemented:
  built: 2026-08-05
  packages: [database/firestore, sessionstore/firestore, authstate/firestore, authstore/firestore]
  generation: 2026-08-05, per requirement:firestore-generation
  verified: a scaffolded passkey_only project with middleware.rdb absent and middleware.firestore enabled starts, passes the mode probe, and serves
  test_server: internal/firestoretest, an in-process Datastore server over lookup, commit, beginTransaction, rollback and runQuery; the gcloud emulator is a Java process inside the Cloud SDK image and contention cannot be provoked against it, so the unit suite runs against this instead
  found_along_the_way:
    explicit_endpoint_wants_a_credential: the driver treats an endpoint as an emulator only when DATASTORE_EMULATOR_HOST is set and no endpoint is configured, so a dev config naming the emulator address is asked for a credential; api:firestore-package resolves a placeholder token for a plain-http endpoint rather than failing startup on a key nobody needs
non_goals:
  - a portable key-value facade over Firestore, DynamoDB, and any other store
  - Firestore native mode, which is a different API this stack does not speak
  - routing a generated .pw.sql query to Firestore
  - changing the relational default of decision:tinygo-storage-direction
  - one kind shared by several stores, which is the single-table design system:tinybind declines
  - migrating existing relational or DynamoDB records into Firestore
```
