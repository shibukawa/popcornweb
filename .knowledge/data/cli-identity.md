---
id: data:cli-identity
type: data
title: CLI Identity
---
The one identity a project declares in data:project-config under cli.identifier, so api:cli-request can call a route guarded by policy:authenticated-path-protection as a stated user without performing a login ceremony.

```yaml
proposed_by: requirement:cli-request
settled: 2026-09-11, user proposal to define the session for a CLI run up front rather than excluding guarded routes; one identity, since switching users from the shell is not a need
location: data:project-config, a cli.identifier table; the cli section is new and holds settings that affect the pw command alone, which is not the api:cli-dev loop and so not dev.*
shape:
  table: "[cli.identifier]"
  fields:
    subject: the account ID, required; it becomes the authentication subject exactly as api:testutil-auth records it
    display_name: optional
    method: session, passkey, oidc, or bearer; default oidc, matching the authtest default
    scope: optional authorization scope claims, tenant, roles, or permissions, as data:request-authentication carries them
    authenticated_at: optional, a duration before now; default now, so a recent-authentication window passes unless the entry says otherwise
  principal: never declared here; the application's registered account resolver turns the subject into its typed principal, which is what a completed login does, so the file states who and never what the application would decide about them
example: |
  [cli.identifier]
  subject = "user-001"
  display_name = "Alice"
  scope = { roles = ["admin"] }
selection: api:cli-request --identity; the flag with no table declared is a usage error naming the key
relation_to_devidp:
  devidp_roster: data:devidp-config declares users for the login screen of requirement:contrib-devidp, with OIDC claims
  why_separate: the devidp roster exists only in an OIDC mode and describes a provider's users, while this describes a session the application would hold after any login, so it reads identically in passkey_only where no provider runs
  convenience: a subject shared with a roster entry names one user, so a developer who logs in as that user in the browser and calls from the shell sees one account
rules:
  - the table installs nothing by itself; it is read only when api:cli-request is invoked with --identity, on a pwdev build, through decision:request-execution-target
  - it can carry no credential, token, or password, because nothing here authenticates; the seam records a session for an account the caller chose, the class auth.EstablishSession is already in
  - the application's account resolver may refuse the subject, and that refusal is the CLI's answer
  - one identity per project; a second user is a change to the file, not a flag, because switching users from the shell was judged not a need
  - api:cli-build reads none of it
```
