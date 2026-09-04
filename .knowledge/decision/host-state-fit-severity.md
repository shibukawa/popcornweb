---
id: decision:host-state-fit-severity
type: decision
title: Host State Fit Severity
---
A configuration that keeps state the deployment host cannot hold is refused, and one that keeps state the host holds badly is advised on; the severity follows what the host can do, not the store's name.

```yaml
status: accepted 2026-09-04; the refusing half is implemented by requirement:cloudflare-process-state-refusal, the advising half proposed by requirement:serverless-state-advisories
two_hosts_two_answers:
  refuse:
    where: a host that instantiates the program per request and owns neither a filesystem nor a socket, which is requirement:cloudflare-workers-build-target today
    why: a memo store there is empty on every read, a memory counter counts one, a sqlite file has nowhere to be; the configuration works and lies, and no operator intent makes it right
    when: at build from config.prod.toml, and at startup under the pwcloudflare tag, because a wrangler var can reintroduce the state after the build
  advise:
    where: a host that keeps a process warm across requests but runs many of them, freezes an idle one, and gives it only ephemeral disk: the four function targets of decision:serverless-target-scope, and a container platform scaled past one replica
    why: the same stores are legitimate there for a reason the framework cannot see; a memory rate limiter on one replica is correct, a per-instance memo store is a hint by design, and a sqlite file may be a read-only bundle shipped with the binary
    when: at build for a function target, where the host is known, in the words rule:configuration-advisories already uses; api:cli-doctor keeps reporting the same findings for a container, where the replica count is the operator's fact
startup_summary:
  contract: policy:startup-summary emits once per process, and a function host's process is a warm instance, so one summary per cold start is the record an operator wants
  exception: only the per-request host turns the automatic format off, because there once per process is once per request
  consequence: nothing to add for the function targets; the question is answered by the contract rather than by a switch
what_is_common_to_every_host:
  - a development session backend is refused outside APP_ENV=dev at startup on every host, by pwsession.ValidateDevelopmentMode; the Worker build repeats it earlier, at build, and a function build may do the same
non_goals:
  - refusing a memory store on a container host; the replica count is not in any file the framework reads
  - a per-host configuration profile; one config.prod.toml describes the deployment and the build reads it against the host it targets
```
