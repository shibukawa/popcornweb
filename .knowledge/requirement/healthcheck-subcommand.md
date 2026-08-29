---
id: requirement:healthcheck-subcommand
type: requirement
title: Health Check Subcommand
---
The application binary itself acts as the container HEALTHCHECK probe, because shell-less images cannot run curl, wget, or a shell, and the binary already resolves port and endpoint paths through api:runtime-configuration.

```yaml
owner: api:application-lifecycle
activation: framework-owned healthcheck subcommand token, handled before application command dispatch like requirement:built-in-config-generation
usage: HEALTHCHECK CMD ["/app", "healthcheck"] in exec form
motivation:
  - distroless and scratch images ship no shell or probe tool
  - the same configuration sources give the probe the effective port and path without duplicated settings
behavior:
  - parse configuration through api:runtime-configuration without database, middleware, or listener startup
  - resolve target as http on loopback, port from data:server-runtime-config, path from the selected endpoint
  - default target is health; --ready selects readiness
  - issue one GET with a bounded timeout and no redirect following
  - 2xx exits 0; any other status, connection failure, or timeout exits 1
options:
  --ready: probe server.readiness instead of server.health
  --timeout: probe deadline as a duration, default 3s
exit_codes:
  healthy: 0
  unhealthy_or_probe_error: 1, never 2 because Docker reserves it
output:
  - one short status line, kept within the 4096-byte inspect capture Docker retains
  - follow policy:operational-endpoints rules: no DSN, backend, stack, or configuration detail
errors:
  - configuration parse failure exits 1 with a message
  - unset server.health, or unset server.readiness under --ready, exits 1 naming the key
  - non-positive server.port exits 1 because the probe cannot locate a random port
rules:
  - the healthcheck token is recognized only as the leading argument, so an application flag value may still be the word healthcheck
  - the token is reserved; api:subcommands RegisterSubCommand panics on it at registration
  - api:application-lifecycle Middlewares refuses any pending framework action by name, so a probe against a self-owned server errors instead of starting a competing listener on every HEALTHCHECK interval
  - probe scheme is plain http on loopback because decision:local-tls-proxy-boundary terminates TLS outside the process
  - the probe targets the local process only and never accepts a remote URL
platform:
  both_toolchains: one code path dials TCP, writes the request, and reads via http.ReadResponse, the same primitives the system:tinygodriver transport uses under TinyGo
non_goals:
  - remote or cross-container probing
  - dependency-level diagnostics beyond the endpoint status code
```
