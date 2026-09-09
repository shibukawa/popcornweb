---
id: requirement:environment-switching
type: requirement
title: Environment Switching
---
One application build must run against development, staging, and production configuration selected by an environment variable, without rebuild or file editing.

```yaml
motivation:
  - the same artifact is promoted across environments
  - deployment-specific values are committed as separate files instead of edited in place
scope:
  environment_token: data:runtime-environment
  file_selection: policy:config-file-resolution
  exposure: api:runtime-configuration
behavior:
  - resolve the environment token before any configuration source is read
  - select the project-local TOML candidate by that token
  - keep user and system configuration files environment-neutral
  - keep existing precedence of defaults, TOML, environment variables, and CLI arguments
  - expose the resolved token to application code
tooling:
  - api:cli-dev defaults the environment to dev and watches config.*.toml
  - api:cli-doctor names the environment to inspect with an option, which selects files to read and never reaches an application process
  - api:cli-init scaffolds config.dev.toml and config.prod.toml in the project root
  - requirement:dotenv-files gives the secrets the production file names a per-environment home, selected by the same token
scaffolded_production_file:
  why_two_and_not_one: the promoted artifact reads a file named for its environment, so a project with only the development file has nothing for any environment it is deployed to, and the first deployment invents the file under time pressure
  contents: the structure of the development file with the values a deployment differs on, chiefly data:observability-runtime-config stdout_format json against the plaintext development default
  secrets: none; a value that must not be in version control is a named environment variable in a comment, per policy:container-runtime-image
  endpoints: the server.health and server.readiness paths are set, because requirement:healthcheck-subcommand exits 1 on an unset key and a container probes with it
  not_a_promise_of_completeness: a real deployment edits this file; the scaffold makes it exist and be correct about what it does say
  - requirement:built-in-config-generation writes the scaffold for the active environment
acceptance:
  - APP_ENV=stg loads ./config.stg.toml when present
  - APP_ENV=stg loads ./config/config.stg.toml when the working-directory file is absent
  - unset APP_ENV behaves exactly as APP_ENV=dev
  - a present ./config.toml is ignored for every environment
  - --config-path overrides the environment-derived candidates and fails loudly when unreadable
  - an environment token containing a dot or path separator fails at startup
  - no readable candidate leaves defaults, environment variables, and CLI arguments effective
non_goals:
  - merging multiple configuration files into one layered result
  - per-environment code paths or build tags
  - a CLI flag that overrides APP_ENV for the application process
migration:
  - existing projects rename config.toml to config.dev.toml or move it under config/
  - user and system configuration files require no change
```
