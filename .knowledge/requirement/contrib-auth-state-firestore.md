---
id: requirement:contrib-auth-state-firestore
type: requirement
title: Firestore Authentication State Store
---
authstate/firestore implements requirement:contrib-auth-state over requirement:firestore-store, so a deployment with no relational database can run the passkey, OAuth, and OIDC ceremonies.

```yaml
package: authstate/firestore, beside the memory, sqlite, postgres, mysql, redis, and dynamo adapters
store: requirement:firestore-store
selected_by: decision:auth-backend-selection, as one of the five stores requirement:firestore-auth-backend moves together
record: data:auth-state-record
public_api:
  - NewRawStore(Options) returns the authstate.RawStore a backend supplies
  - NewStore[T](api:auth-state-codec, Options) wraps it for a caller that owns a codec
  shape: a raw store over encoded payloads, matching requirement:contrib-auth-state-dynamo, because two of the three ceremony record types are unexported
client:
  source: the request context, installed by the api:firestore-package middleware
  no_constructor_argument: firestorebind carries the client in the context, as dynamobind does for requirement:contrib-auth-state-dynamo
  missing: firestorebind's ErrNoClient, surfaced as requirement:contrib-auth-state ErrUnavailable
kind:
  name: popcornweb_authstate, per rule:framework-owned-tables
  key: datastore.NameKey over the namespace and the correlation key joined
  why_joined: the same reason requirement:contrib-auth-state-dynamo gives, reached differently; here the concern is not one hot partition but one entity group, since an ancestor path would put every ceremony of one protocol into a group that serializes its writes
  no_ancestor: every ceremony record is its own entity group, so a login spike contends with nothing
  lost_by_joining: a cheap protocol-scoped listing, which nothing needs once no prune exists
put:
  shape: Insert, and on ErrAlreadyExists a transaction that reads the colliding record and replaces it only when its stored deadline has already passed
  effect: an expired collision is replaced and an unexpired one is not, which is the sqlite adapter's rule and the dynamo adapter's condition reached in two steps instead of one
  cost: the common case is one commit; the transaction is paid only by a real key collision
  rejection: an unexpired collision is ErrAlreadyExists, unchanged from the contract
  bounds: the encoded payload is bounded before the request, and a record over datastore.MaxEntityBytes is refused with the limit named
take:
  shape: Run with Tx.Load then Tx.Remove, which reads the record and queues its delete
  why_a_transaction: no commit returns a prior entity, so there is no counterpart to the DynamoDB delete that hands back what it removed, per decision:firestore-conditional-writes
  single_use: preserved; two concurrent takes cannot both commit, and the loser re-runs, finds nothing, and returns ErrNotFound
  cost: two round trips, since the lazy transaction start of system:tinygodriver-firestore folds the begin into the read
  no_row: ErrNotFound
  after_removal: expiry is validated, then bounds, then decode; a malformed, expired, or undecodable record stays consumed, per data:auth-state-record
  closure_purity: the closure reads and queues a delete and does nothing else, so the ABORTED re-run is safe by construction rather than by care
expiry:
  authority: the stored deadline, checked on Take; a record past it is never returned
  removal: a TTL policy on the deadline property, applied by deployment tooling, per decision:firestore-expiry-policy
  one_property: the deadline is a datastore.Time, so unlike requirement:contrib-auth-state-dynamo there is no second second-precision attribute maintained beside a millisecond one
  no_prune: a keys-only query for expired ceremony records would cost more than the records; the sqlite adapter publishes Prune because a bounded DELETE is cheap there, and this adapter publishes none
security:
  - namespace, key, expiry, and payload never enter a log or a stable error, per data:auth-state-record
  - this kind is framework-owned, so policy:query-log-safety records nothing for it and no payload can reach a diagnostic artifact
  - payload encryption stays an explicit codec or deployment responsibility
acceptance:
  - a passkey, OAuth, and OIDC ceremony completes with this adapter selected and no relational database configured
  - a second Put for an unexpired key returns ErrAlreadyExists without overwriting
  - a Put over an expired key of the same name succeeds
  - two concurrent Takes of one key return the value exactly once
  - a Take after expiry returns the contract error and leaves nothing behind
  - a Put racing another Put on one expired key leaves exactly one record
implemented:
  built: 2026-08-05, in authstate/firestore
  verified: two concurrent takes of one key return the value exactly once, and an uncontended put is one commit with no transaction at all
non_goals:
  - a prune or sweep operation
  - namespace-scoped enumeration
  - sharing one kind with requirement:firestore-session-store
  - an ancestor relation grouping the ceremonies of one protocol
```
