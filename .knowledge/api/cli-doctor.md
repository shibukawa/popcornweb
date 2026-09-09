---
id: api:cli-doctor
type: api
title: pw doctor
---
pw doctor reports what a named environment of the project would run, and what is missing or inadvisable there, by reading the project rather than running it.

```yaml
usage: "pw doctor [--env=token|all]... [--config-path=path] [--format=text|json] [--strict] [--online]"
requirement: requirement:project-diagnostics
mechanism: decision:host-side-diagnostic-analysis
inputs:
  env:
    value: the data:runtime-environment token to diagnose
    default: the APP_ENV of the pw process, then dev
    repeatable: naming the option more than once diagnoses each token in one report
    all: --env all diagnoses every token discovered from the project-local config.{env}.toml names
    reason: diagnosing production from a development host is the point of the command, so the token is an argument rather than an ambient variable
    boundary: the option selects what to inspect and never reaches an application process, so requirement:environment-switching keeps its non-goal
  config_path: diagnoses one explicit file, bypassing environment-derived naming as policy:config-file-resolution does
  format: text tree for a terminal, json for CI; text is the default
  strict: promotes every warning to a failing exit, for a release gate
  online:
    effect: enables the rule:storage-checks online set and the provider discovery fetch
    default: off, so the command contacts nothing and a production configuration can be diagnosed from anywhere
    exception: rule:project-integrity-checks may test the configured port on loopback for the dev token, which contacts no remote address
checks:
  catalog: decision:shared-check-catalog
  groups:
    - rule:project-integrity-checks
    - rule:route-and-template-checks
    - rule:storage-checks
    - rule:configuration-advisories
    - rule:production-readiness-checks
sections:
  summary: the project, its capabilities, the diagnosed tokens, and the host mode, per data:diagnostic-report
  features: one line per framework feature, with the implementation the import graph found behind it
  middleware: the effective chain in order, per data:middleware-runtime-config
  database: the data:database-connection-set connections, their drivers, and the pointers that select them
  registrations: the plugins and database/sql drivers the import graph links
  configuration:
    content: the merged view of defaults, this host's environment, the policy:dotenv-resolution files of the diagnosed token, and the selected TOML file
    completeness: >
      this is the complete surface of requirement:startup-summary-brevity, so an
      entry rated as detail is rendered rather than skipped; policy:startup-summary
      is the short one and skips exactly those
    still_absent: a key whose variant was not selected, which no surface shows, per decision:config-verbosity-tag-adoption
    why_not_shorter: >
      a diagnosis is read to find the value a reader could not otherwise see, which
      is the opposite of what a boot summary is read for
  findings: most severe first, each carrying its data:diagnostic-check identifier and documentation link
  limits: every value, registration, and check this run could not determine or run
  pending:
    routes: waits on data:route-table
    capabilities_and_generated: the requirement:incremental-project-capabilities probes and the generated-artifact state drive checks today and are not yet rendered as sections of their own
degradation:
  unparsable_go: analyze the packages that do parse, and report the failure as one error finding naming the file
  stale_generated_metadata: report the drift finding first and mark the configuration, feature, and route sections as approximate
  missing_config_file: not a failure; report defaults with no TOML layer, as api:runtime-configuration would
  missing_route_table: skip rule:route-and-template-checks as limits rather than reporting collisions it cannot back up
rules:
  - require a data:project-config project root, like api:cli-add and api:cli-new
  - read only; write no file, start no process, and open no connection without the online option
  - offer no --fix, because a diagnosis that edits stops being one a reader trusts; remedies stay with api:cli-add, api:cli-generate, and api:cli-migrate
  - name the remedy command for every finding instead, so the read-only boundary costs the reader nothing
  - never treat an undeterminable value as its default, per decision:host-side-diagnostic-analysis
  - state which host mode the run had, so a clean report from a workstation is not read as a clean deployment
exit:
  clean: 0
  error_finding: nonzero
  warning_finding: 0, or nonzero under strict
  no_project_root: nonzero with usage
relations:
  report: data:diagnostic-report
  check: data:diagnostic-check
  flow: flow:project-diagnosis
  sibling: api:cli-check answers content drift of generated files, and doctor answers everything around them
```
