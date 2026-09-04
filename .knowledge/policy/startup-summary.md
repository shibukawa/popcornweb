---
id: policy:startup-summary
type: policy
title: Startup Summary
---
Resolved configuration is reported once per process, in the shape its reader can use, instead of one record per key.

```yaml
capture:
  when: api:runtime-configuration finishes loading data:loaded-configuration
  content: environment, resolved config path, every reported key with value and winning place, start time
  secrets: policy:log-emission redaction applies before the value is stored, and a DSN takes the rule:dsn-redaction form rather than the whole mask
  already_dropped: a key whose variant was not selected never arrives, per decision:config-verbosity-tag-adoption value_conditions
brevity:
  requirement: requirement:startup-summary-brevity
  rule: this is the short surface, so an entry rated as detail is skipped while nothing but the default layer set it
  where: the capture, so tree and record skip the same entries and cannot disagree
  rating_is_carried_not_applied: the loader marks the entry and this decides; api:cli-doctor decides the other way
  state: implemented in bootEntries, which is the capture
emission:
  once: the first of Run or Middlewares to complete initialization emits it
  run: emitted after the listener binds, so the reported address is the accepted one and not the configured one decision:development-port-shift may have moved off
  middlewares: emitted after initialization without a listening address
  restarted: once per process is once per rebuild under api:cli-dev, so the loop captures the summary and replaces it per requirement:dev-reload-summary; nothing here knows that happened
selection:
  key: observability.boot_log
  auto: tree when stderr is a character device, otherwise record; off under the pwcloudflare build tag, because requirement:cloudflare-workers-build-target's host runs main per request and a summary per request is noise rather than a summary
  function_hosts: once per process is once per warm instance on the targets of decision:serverless-target-scope, which is one summary per cold start, the record an operator wants; nothing is switched off there, per decision:host-state-fit-severity
  tree: banner, configuration grouped as a tree, and the listening URL, written to stderr
  record: one api:logger record named "popcornweb started"
  off: nothing
tree:
  grouping: dotted keys nest by section; values align with their siblings
  provenance: only non-default places are marked (file, env, flag)
  color: ANSI only on a terminal without NO_COLOR and TERM=dumb
record:
  config: nested groups mirroring the key structure
  config_source: flat group naming the place of every non-default key
  listening: present only when the framework owns the listener
rationale:
  - one boot event keeps log collectors and OTLP exporters from ingesting a record per key
  - a tree shows an operator the shape of the configuration and what was overridden
```
