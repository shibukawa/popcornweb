---
id: policy:config-file-resolution
type: policy
title: Config File Resolution
---
Project-local configuration files are named per data:runtime-environment, while shared user and system configuration files keep one environment-neutral name.

```yaml
naming:
  project_local: config.{env}.toml
  user_and_system: config.toml
search_order:
  - explicit --config-path value
  - ./config.{env}.toml
  - ./config/config.{env}.toml
  - user config directory {vendor}/{tool}/config.toml
  - system config directory {vendor}/{tool}/config.toml
resolution:
  mode: exclusive first readable match
  base: process working directory for project-local candidates
  explicit_path:
    - bypasses environment-specific naming entirely
    - unreadable explicit path fails without fallback
  not_found: TOML input is skipped; defaults, environment, and CLI still apply
rules:
  - project-local candidates are limited to the working directory and its config/ directory
  - project-local lookup never falls back to a local config.toml
  - user and system directories are never environment-specific
  - the resolved path and the active environment are reported by policy:startup-summary
  - a readable project-local file suppresses user and system candidates
rationale:
  - deployment-specific values live beside the project and are selected without editing files
  - operator-owned machine-wide overrides stay environment-neutral and need no per-environment duplication
engine: system:tinybind configpath resolution with environment-derived extra read paths
sibling: policy:dotenv-resolution names the .env files by the same token, and layers them where this policy selects exclusively, per decision:layered-dotenv-files
consumers:
  - api:runtime-configuration
  - data:loaded-configuration
```
