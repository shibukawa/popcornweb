---
id: decision:firestore-conditional-writes
type: decision
title: Every Conditional Write Is A Verb, A Version, Or A Transaction
---
The Firestore backend has no condition expression to write, so each conditional write of the five framework stores is placed on one of three levels, and the compensating sequence DynamoDB needed disappears where a transaction covers it.

```yaml
status: accepted
decided: 2026-08-05
constraint: system:tinygodriver-firestore, where the wire offers verb preconditions, a version precondition, and a transaction, and no predicate over a property value
three_levels:
  verb:
    insert: fails ErrAlreadyExists if the key is taken, which is put-if-absent
    update: fails ErrNoSuchEntity if the key is absent, which is put-if-present
    cost: none; it is the same single commit an upsert is
  version:
    what: WithBaseVersion or WithUpdateTime, from the Version and UpdateTime a read already returned
    is: optimistic concurrency, and the honest name for it
    catches: a concurrent write the caller never read, which a condition expression over one property does not
    cost: a read the caller usually wanted anyway, then one commit
    reached_through_the_binding: a record type carrying a version field satisfies firestorebind Versioner, and Store appends the precondition on its own; a store neither builds nor forgets the option
    a_zero_version_writes_unconditionally: which is what a first Put wants, so the same code serves both without a branch
    a_caller_option_loses_to_it: firestorebind overrides a caller's own WithBaseVersion with the field's, since the field is the one the decoder filled
  transaction:
    what: read inside, decide in Go, commit
    needed_for: any predicate over a stored value, which is every remaining case below
    cost: one read plus one commit is two round trips from tinygodriver v1.1.8, which starts the transaction inside the first call that needs one; every conditional write below is that shape
    closure_re_runs: ABORTED restarts it, so nothing with an outside effect goes in one
placement_per_store:
  session_put:
    dynamodb: PutItem, unconditioned
    firestore: Put, unconditioned; the record replaces one entity atomically
  session_touch:
    dynamodb: one UpdateItem conditioned on the item existing and being alive
    firestore: read, check aliveness in Go, then Store, whose precondition comes from the version the read filled
    why_not_a_transaction: the predicate is over the record the caller just read and the write is to that same key, so the version precondition already refuses a renewal that raced a rotation or a delete; a transaction would add a round trip and change nothing
    what_is_lost: the single-request renewal, and the whole entity is rewritten rather than two timestamps patched, per requirement:firestore-session-store
    what_is_kept: Touch never revives an expired or missing record, which is the contract rule of api:session-store
    two_things_named_version: the Datastore entity version and the data:session-record version field are unrelated, one being the service's write counter and the other a schema-invalidation stamp; the record type keeps both, and the store never uses one where the other belongs
  authstate_put:
    dynamodb: PutItem conditioned on the item being absent or its deadline having passed
    firestore: Insert, and on ErrAlreadyExists a transaction that reads the colliding record and replaces it only if its stored deadline has passed
    why_the_two_steps: the uncontended case is the common one and costs one commit; the transaction is paid only by an actual key collision, which is rare and is the case that needs the predicate
  authstate_take:
    dynamodb: one DeleteItem returning the item it removed
    firestore: Run with Tx.Load then Tx.Remove, because no commit returns a prior entity
    single_use_guarantee: preserved; two concurrent takes cannot both commit, and the loser re-runs, finds nothing, and reports the contract miss
    alternative_rejected: read outside a transaction and delete with WithBaseVersion, which is two round trips instead of three and returns the same answer; declined because a delete whose precondition fails is indistinguishable from a delete of an already-absent key, so the loser of the race cannot tell that it lost
  credential_sign_count:
    dynamodb: one UpdateItem conditioned on the stored sign count being lower than the accepted one
    firestore: a transaction, since the predicate compares against a stored value
    unchanged: a counter that moves backward is refused by the store rather than by the caller, and a failure fails the ceremony closed
  bootstrap_attempt_spend:
    dynamodb: one UpdateItem adding minus one, conditioned on attempts remaining and the record unconsumed
    firestore: a transaction, because property transformations are excluded by the driver and there is no arithmetic on this wire
    contract_still_met: N parallel guesses against a budget of one leave exactly one caller with an attempt, because the losers abort and re-run against a spent record
first_enrollment_becomes_one_commit:
  dynamodb: decision:dynamodb-auth-compensating-registration fixes an order across three writes and enumerates the partial states, because there is no TransactWriteItems
  firestore: the bootstrap spend and the credential insert are two mutations in one transaction, so the pair is atomic
  what_still_cannot_join_it: the activation callback, which is application code with effects outside the transaction and would run again on every ABORTED re-run
  reachable_states:
    after_the_commit: bootstrap spent and credential stored, account still provisional
    after_the_callback: complete
    never: a credential stored without its bootstrap credential spent, and never a bootstrap spent without its credential
  effect: two reachable states instead of three, and the one that remains is the same one the relational path has when its post-commit step fails
  seam: auth.FirstEnrollmentStore, unchanged; the backend that implements it commits the two writes together rather than ordering them
  not_a_supersession: decision:dynamodb-auth-compensating-registration stands for DynamoDB, and its revisit_if condition is about the DynamoDB driver, not this one
retry_interaction:
  no_second_loop: the driver retries requests and re-runs aborted closures; nothing in these stores adds a loop, which is the passthrough rule system:tinybind states for firestorebind
  idempotence: insert, update and delete are replayable by construction, so the attempts x 2 delivery bound is harmless; there is no ADD-shaped update to double
  what_this_removes: the whole hazard class decision:dynamodb-auth-compensating-registration works around on the retry side
related:
  - requirement:firestore-session-store
  - requirement:contrib-auth-state-firestore
  - requirement:firestore-auth-stores
  - decision:dynamodb-auth-compensating-registration
  - system:tinygodriver-firestore
```
