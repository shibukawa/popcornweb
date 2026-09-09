---
id: data:loaded-configuration
type: data
title: Loaded Configuration
---
Each independently registered configbind target is merged and validated at startup, then exposed as an immutable typed snapshot with per-field provenance.

```yaml
registration:
  api: pw.RegisterConfig[T](prefix)
  model: decision:independent-runtime-config-bindings
  identity: binding prefix plus generated Go type
precedence:
  - typed defaults
  - TOML
  - dotenv files, per policy:dotenv-resolution
  - environment variables
  - CLI arguments
toml_selection: policy:config-file-resolution using data:runtime-environment
dotenv_selection: policy:dotenv-resolution using the same token; the files go to system:tinybind as LoadOptions.EnvFiles, which lays them under Environ and labels each value with its file
mapping:
  reflection: forbidden
  mechanism: reuse generated JSON-to-struct mapping
generated_bindings:
  - environment variables
  - CLI arguments
access:
  - pw.Config[T](context.Context) returns T
  - internal provenance remains available for startup logging
registry: data:runtime-config-registry
request_storage: data:request-context-capsule
home_2026_08_11:
  package: github.com/shibukawa/popcornweb/pwconfig
  what_moved: the registry, the load, the framework's own bindings with their generated definitions, the environment the load resolves against, and the connection group resolution that decides which configured database receives a migration
  why: requirement:alternate-http-backend-readiness names configuration binding as the first of four layers that must reach a shared package before a second transport can be built without the first
  why_not_pwruntime: the earlier reading was that moving the registry would drag registration, defaults, the environment overlay, scaffold emission and the boot report along with it, so the read was published instead; what that reading missed is that only two of those are runtime-shaped, and both are hooks now
  hooks: the runtime performing startup supplies an argument filter, which is how framework subcommands come off the command line, and a loaded callback, which is what the startup summary reads
  pw_surface_unchanged: every type is a true alias and RegisterConfig, SetConfigLoadOptions, ParseConfig, Env and Development are thin wrappers, so no application and no document changed
  alias_must_stay_an_alias: the registry is keyed by reflect.Type, so a defined type in pw would be a different one and every lookup would silently miss
  chain_settings_followed: the reduction of ServerConfig, SecurityConfig and MiddlewareConfig into pwruntime.ChainSettings moved with them, so a parse publishes what a chain builder needs and neither transport reduces it twice
  proved_by:
    containment: pwconfig reaches neither transport runtime, asserted through go list -deps rather than through imports, because a dependency arrives by any path
    end_to_end: internal/fastonly parses a file, reads a setting back and serves one fasthttp request through a chain composed from the published settings, in a build whose dependency graph contains no pw
  still_pw: the session manager, the extension registry, the database pools and the startup summary; each is a layer of its own and each has to move before a plugin like plugin/auth can be linked without pw
```
