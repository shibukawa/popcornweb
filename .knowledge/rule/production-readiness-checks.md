---
id: rule:production-readiness-checks
type: rule
title: Production Readiness Checks
---
The PW05xx data:diagnostic-check entries: the pre-launch checklist as something that runs, so the list cannot quietly go stale the way a documentation page does.

```yaml
premise:
  form: the checklist a project would otherwise read once and forget, expressed as checks against the diagnosed token
  scope: dev_only, so the whole group is silent while the token is dev
  boundary: readiness here means what the framework configured; it says nothing about capacity, backups, or infrastructure
exposure:
  openapi-exposed:
    trigger: data:server-runtime-config openapi names a path
    severity: warning
    reason: policy:operational-endpoints protects it like an application route, so this asks whether that was intended rather than declaring a hole
  debug-logging:
    trigger: observability minimum_level below info
    severity: warning
    relation: the same condition rule:configuration-advisories reports as verbose-log-level-outside-dev, listed once and cited here
assets:
  tailwind-minify-off:
    trigger: assets.tailwind.enabled true with minify false
    severity: warning
    reference: requirement:tailwind-css-integration
  stylesheet-stale:
    trigger: the generated CSS older than a .pw.html or CSS source it is built from
    severity: error
    reason: flow:tailwind-css-build failing a production build on stale output is the rule; this reports it before the build
  precompression-stale:
    trigger: a public asset newer than its .zstd sidecar, or a sidecar whose source is gone
    severity: warning
    reference: flow:public-asset-build, which api:cli-build runs, so this fires on a tree built by hand
  public-serving-from-disk:
    trigger: server public.read_local true
    severity: warning
    relation: the same condition as local-public-read-outside-dev, cited rather than duplicated
sessions:
  unrevocable-session:
    trigger: auth.enabled with session.backend cookie
    severity: warning
    reason: the record travels with the browser, so a logout expires the client copy while a copy taken beforehand keeps authenticating, per decision:cookie-session-storage and the login_slot_placement of policy:session-security
    silent_in_dev: the dev_only scope of the premise above is doing real work here; the pairing is the correct one in development, where the backend exists so that a login needs no infrastructure
    relation: plugin/auth warns about the same condition at startup outside dev, so an operator who never runs doctor still hears it once per boot
crawlers:
  status: deferred until the framework owns the artifacts
  robots-missing-or-permissive:
    intent: a non-prod deployment serving anything other than a disallowing robots.txt, and a prod one serving none
    blocked_on: no framework-owned robots.txt exists yet, so nothing declares what is expected
  sitemap-and-social-metadata:
    intent: a declared sitemap route and per-page social metadata
    blocked_on: data:route-table gives the page list, and the metadata shape is not designed
rules:
  - a check in this group cites the catalog that owns the condition instead of restating it, so one setting produces one finding
  - the group is a view over checks rather than a second severity system; nothing here is fatal at startup
  - a deferred entry stays listed with what blocks it, because the value of the checklist is that its gaps are visible
```
