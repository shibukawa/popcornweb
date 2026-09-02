---
id: decision:memo-store-handle
type: decision
title: Resolve The Memo Store To A Handle Before The Methods Exist
---
A caller resolves a named store of data:cache-store-set to a handle and passes that handle to the typed entry points, so the surface becomes methods without editing a call site once the language allows a method to take type parameters.

```yaml
status: accepted and built 2026-08-13; the methods landed 2026-09-02 with the move to go 1.27.0, and the package-level functions were retired the same day
owner: api:data-cache
language_constraint: a Go method may not declare its own type parameters, so the typed operation cannot be a method on the store today whatever the design prefers
same_constraint_elsewhere: requirement:typed-api-method-convergence collects every site this shapes, and this is the only one where the eventual move can be prepared for rather than merely waited on
now:
  acquire: a package-level function taking the context and the store name, returning the handle and an error
  operate: package-level generic functions taking that handle
later:
  acquire: unchanged, which is the whole point
  operate: methods on the handle, and the package-level functions are then retired
  contingent_on: the language gaining methods with type parameters, not on a particular release; the reminder to check is carried outside this catalog
landed_2026_09_02:
  operate: Get, Has, Set, Invalidate, InvalidateScope and InvalidateTag on the handle, per api:data-cache
  acquire: unchanged, as planned; no call site that resolved a store changed its first line
  retired: Memo, MemoHas, MemoSet, MemoInvalidate, MemoInvalidateScope and MemoInvalidateTag, from pwruntime, pw and pwfast, per requirement:typed-api-method-convergence
why_the_handle_now:
  migration_is_additive: with the store named at each call, moving to methods would mean editing every call site to first obtain a handle; with the handle, the acquisition line is already written and the operation moves from a function to a method
  the_global_surface_stops_growing: every operation beside the read — a membership test, an overwrite, an invalidation — would otherwise need its own exported name in pw, each generic, each spelling the store parameter again; as methods they cost nothing at package scope
  startup_resolution_becomes_possible: the store set is immutable after startup, so an application may resolve once during setup and hold the handle, which is what turns an unknown store name back into a startup failure rather than a first-request one
  a_name_at_the_call_site_could_not: it had no earlier moment to fail in
cost:
  one_more_line: a caller writes an acquisition before its first read, where the previous shape read directly
  worth_it_because: that line is the one thing the later surface needs and the one thing a name-per-call shape would have had to add retroactively
per_request_or_once:
  per_request: a map lookup and an error check, which is what a handler doing one cached read will write
  once_at_setup: preferred where the name is static, because it fails at startup and skips the lookup
  both_supported: the handle holds no request state; scope and tracing come from the context passed to each operation, not from the handle
rejected:
  wait_for_the_language: it would ship the name-per-call shape first and then break every call site, which is the cost this decision exists to avoid
  a_handle_that_carries_the_context: it would tie a process-lived store to one request, and the scope value must be read per operation anyway
```
