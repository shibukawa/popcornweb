---
id: api:cli-dev
type: api
title: pw dev
---
pw dev runs the local service set and continuously regenerates, rebuilds, and restarts the configured application.

```yaml
usage: pw dev
steps:
  - start configured Devbox services
  - run api:cli-generate
  - run api:cli-migrate up when data:project-config migration.auto is enabled
  - start requirement:contrib-devidp when data:project-config dev.idp.enabled is true
  - start requirement:dev-console unless data:project-config dev.console disables it, which mounts requirement:dev-telemetry-viewer and every other enabled pane
  - build and start the decision:dev-harness-process binary when a pane needing it is enabled, and rebuild it with the project so a template edit reaches the storybook the way it reaches the application
  - start flow:tailwind-css-build watch mode when enabled
  - enable decision:development-public-assets
  - build and run data:project-config project.main, which binds its port under decision:development-port-shift
  - default data:runtime-environment to dev when APP_ENV is unset
  - watch every Go source for rebuild, plus .pw.html, .pw.sql, popcornweb.toml, config.*.toml, config/config.*.toml, .env, and .env.*, per decision:developer-loop-watch-scope
  - regenerate only from the data:project-config generate purposes, because api:cli-generate reads nothing else
  - regenerate when a concept:page-tree route appears or disappears, which the file walk already reports because a route always carries a page template
  - watch the data:devidp-config file when the development identity provider is enabled
  - watch the data:migration-source directory
  - add data:project-config dev.watch.includes paths and globs, and skip every dev.watch.excludes subtree
  - exclude public/** and public/**/*.zstd from Go rebuild inputs
  - regenerate when generated inputs change
  - reapply pending migrations before restart when migration sources changed
  - rebuild and restart after successful changes
child_process_lifetime:
  rule: no process api:cli-dev started outlives it, by any exit path
  problem:
    indirection: the application is started as go run, which compiles and then runs the binary as a grandchild, so the process pw holds is not the process listening on the port
    escalation: the stop path signals go run and then kills it, and a kill cannot be forwarded, so the binary underneath is orphaned
    unconditional_kill: the wait between the two has no branch for the process having already exited, so the kill is not an escalation after a failed interrupt but the normal ending of every stop
    no_wait_for_exit: the stop path returns without waiting for the process to be gone or the port to be released, so the replacement is started against a socket the previous one may still hold
    rebuild_is_the_common_case: every watched change stops and restarts, so this is met during ordinary editing rather than only at shutdown; the symptom is a restart that cannot bind
    cancellation: the context kills go run the same way, so a canceled run has the same orphan
    abrupt_exit: a pw process that dies without unwinding leaves every child it started, since nothing but its own deferred stops reaps them
    symptom: the next pw dev fails on an address already in use, held by a process the developer did not start and cannot name
    services: the same shape as the known devbox case, where interrupting devbox leaves the process manager it spawned running
  requirement:
    - the application is stopped through something that reaches the process actually serving, not only its launcher
    - a kill applies to the whole group pw created, so an unforwardable signal is not the end of the chain
    - the stop path is the same whether the loop ended by interrupt, by error, or by context cancellation
    - the stop path waits for the process to be gone before the replacement starts, and reports the wait rather than racing it
    - a process that exits on the interrupt is not killed afterwards
    - a leftover process from an earlier run is reported by name and port rather than surfacing as a bind failure
reporting:
  policy: policy:cli-progress-reporting
  startup: a bounded progress region naming the phase in progress, because these steps together take long enough that silence reads as a hang
  phases: services, generation, migration, identity provider, console, CSS build, and the Go build
  browser: a failing phase also reaches an open page through requirement:dev-error-overlay, so the terminal is no longer the only place a diagnostic lands
  collapse: the region gives way to policy:startup-summary and then the application and service log stream
  rebuild: a watch-triggered regenerate, migrate, and rebuild reuses the same region rather than reprinting the startup sequence, and the restarted process reports per requirement:dev-reload-summary rather than reprinting policy:startup-summary
  logs: the application stream stays plaintext under policy:log-emission, since the developer loop is read in a terminal
  diagnostics: never enter the region, so a generation or migration failure keeps its place in the scrollback
services:
  absent_environment: a project without devbox.json declares no service here, so the step is skipped in silence
  default: Valkey
  rule: default services may be disabled or changed in Devbox configuration
  none_declared:
    behavior: skip startup silently when devbox reports that the project defines no service
    rationale: a project with no database and no cache is an ordinary shape, and starting anyway prints an error that reads like a misconfiguration
    probe: devbox exits zero either way, so the wording of its listing is the only signal; an unreadable answer falls through to starting services rather than skipping them
  output: service logs join the developer loop stream, because the process manager terminal UI would paint over generation, migration, and application output
  lifetime: services stop with the developer loop, because interrupting devbox leaves the process manager it spawned running
identity_provider:
  package: requirement:contrib-devidp
  lifetime: starts before the application process and stops with the developer loop
  issuer:
    port: an available loopback port reserved per run, because the injected issuer makes a fixed port unnecessary
    pin: data:project-config dev.idp.port, which api:cli-init scaffolds with a value rather than leaving reserved; a reserved port moves the issuer every run, and the scaffolded resolver builds its account ID out of the issuer, so the same person is handed a new account each time
  client:
    ownership: pw dev registers one ephemeral client per run and the developer declares none
    credentials: client id and secret generated from crypto/rand at startup and discarded at shutdown
    redirect: loopback redirect URIs accepted per policy:devidp-safety, so the application callback path stays application-owned
  injection:
    mechanism: environment variables on the application process, which outrank TOML in data:loaded-configuration precedence
    variables: data:authentication-runtime-config oidc issuer, client id, and client secret environment names
    rule: injected only for the process api:cli-dev starts, never exported to the developer shell
    override: an explicit environment value already set by the developer is preserved
  reporting: log the issuer, the login URL, the roster subjects, and the client id, with the secret masked
  reload: roster changes reload in place without restarting the application
  guardrails: policy:devidp-safety
  default: disabled
console:
  requirement: requirement:dev-console
  boundary: policy:dev-console-boundary
  lifetime: starts before the application process, survives every rebuild and restart, and stops with the developer loop
  listener: one loopback port for the index and every pane, per decision:dev-console-consolidation
  injection: the resolved console URL, on the application process only, for requirement:dev-error-overlay
  state: every phase transition publishes data:dev-loop-state, which reaches an open page through flow:dev-overlay-delivery
  build_mode: the application is built with the pwdev tag, which selects decision:development-public-assets, the overlay module of decision:dev-browser-runtime-scope, and decision:dev-application-attachment
  attachment: an attachment token is generated per run and injected beside the console URL, the way requirement:contrib-devidp injects its client secret
  application_url: announced by the application over that attachment rather than read out of the project, because the loop reads the development configuration best effort and decision:development-port-shift outranks it
  default: enabled
telemetry_viewer:
  requirement: requirement:dev-telemetry-viewer
  flow: flow:dev-telemetry-capture
  mount: a requirement:dev-console pane; the OTLP receiver keeps its own port for exporters that must find it
  injection: data:observability-runtime-config otel enabled and endpoint, on the application process only
  output: records reach both the viewer and the developer loop stream, because a viewer must not empty the terminal
  default: enabled
migration:
  default: enabled, and the one place policy:migration-safety permits an automatic rollback
  ordering: migrations complete before the application process starts
  edited_file:
    problem: a forward-only loop does nothing useful when the file that changed is one already applied, because its version is recorded; the schema the developer is editing is the one schema the loop cannot reach
    behavior: roll back to the version before the changed file, then apply forward to the latest
    scope: the api:cli-dev loop only; api:cli-migrate, startup apply, and api:test-run keep the policy:migration-safety rules unchanged
    trigger: a watched change to an already-applied migration, or a new file inserted below the highest applied version
    data: rows below that version are lost, which is accepted here because requirement:test-data-seeding is where a development database gets its rows back
    reseed:
      default: on, through data:project-config seed.auto
      action: apply the api:cli-seed datasets after the schema is back at the latest version, so the cycle that emptied the database is the cycle that refills it
      bounded_to: the migration cycles that reset the schema, and the first apply that created it; an ordinary rebuild reseeds nothing
      why_bounded: seeding is clear-insert and truncates its tables, so reseeding on every restart would delete what the developer typed into the running application
      off: seed.auto false leaves the database empty after a rollback, for a developer whose rows are worth more than the datasets
    down_required: a changed migration with no usable Down stops before any statement runs, per policy:migration-safety, and the loop reports it and leaves the schema alone rather than half-reversing it
    announced: the loop names the version it is rolling back to and the files it will reverse, because this is the one automatic action in the loop that destroys something
    detection:
      within_a_session: the watcher already knows which file changed, which is enough to pick the target version
      across_sessions: an edit made while the loop was down is invisible, since nothing records the content of an applied file; that case still surfaces as the existing integrity error
  forward_only_elsewhere: policy:migration-safety, unchanged for every other caller
failure:
  generation_css_or_build: keep the developer loop alive and report diagnostics
  application_process:
    rule: report the exit and keep watching, whatever the status was
    reason: the loop exists to survive a half-finished edit, and a project is unbuildable for most of the time between two working states
    instance: a compile error reaching the loop as "application exited: exit status 1", which ended pw dev over a field the developer was in the middle of renaming
    clean_exit: also non-terminal; an application that returned zero on its own is waiting for the next change like any other
    recovery: the next watched change rebuilds and restarts, with no second command to type
    ends_the_loop: only the interrupt, which is the one exit the developer asked for
    precedent: the CSS toolchain exit in the same select already reports and keeps going
  migration: report diagnostics, skip the restart, and keep the developer loop alive
  identity_provider: report diagnostics and stop the loop, because the application cannot log in without its configured issuer
  console: report diagnostics and keep the loop alive, because an unobservable run is still a working one; this covers the telemetry viewer and every other pane
  dev_harness: report diagnostics, disable the panes that need it, and keep the loop alive, because the harness of decision:dev-harness-process is a reader of the project and not part of it
```
