---
id: decision:layered-dotenv-files
type: decision
title: Layered Dotenv Files
---
The shared .env and .env.{env} and the .local override of each are all read and layered, unlike the project-local TOML that policy:config-file-resolution selects exclusively, because a dotenv file holds the values that differ per environment or per machine rather than a whole configuration.

```yaml
context:
  toml_rule: policy:config-file-resolution reads one file and never falls back to config.toml, so a config.stg.toml is a complete configuration rather than a diff
  dotenv_shape: a dotenv file is a handful of secrets and machine-local values, and its readers elsewhere layer .env under .env.{mode}
  user_request: the template is created, the .local files are ignored, and the runtime reads the whole family
  local_pair: the split copied from the Vite and Next conventions, where .env and .env.{mode} are committed and every .local file is ignored; a project then has a committed place for a shared non-secret and an ignored place for a secret, without a rule about which names are which
options:
  exclusive_env_file:
    reads: only .env.{env}
    for: mirrors the TOML rule exactly, and one file per environment is easy to reason about
    against: a plain .env, which is what every tool and every developer writes first, would be silently ignored
  base_only:
    reads: only .env
    for: the smallest possible rule
    against: a developer who runs APP_ENV=stg locally has no place for staging credentials that does not overwrite the development ones
  layered:
    reads: .env then .env.{env}, then the process environment
    for: the shape the ecosystem already teaches, with a per-environment file that changes only what differs
    against: a second source of layering beside the TOML rule, which requirement:environment-switching lists as a non-goal for configuration files
choice: layered
why:
  - a dotenv file is an environment-variable source, not a configuration file; the requirement:environment-switching non-goal is about merging TOML files, and the environment layer was always a merge of whatever was exported
  - the process environment winning over both files is what keeps a container, a CI job, and a direnv user in control without editing anything
  - the same key in the same environment stays deterministic: later file wins, then process, which is one sentence to remember
template_name:
  choice: .env.example
  rejected: .env.template and .env.dist, which are read by nothing and recognized by fewer tools
  ignore_shape: .env.local and .env.*.local, two lines and no negation, since the template is not matched by either
token_in_files:
  choice: APP_ENV from .env or .env.local is honored when the process did not set it, and APP_ENV in .env.{env} or its .local is ignored with a warning
  why: the base pair is read before the token is needed, and the per-environment pair is selected by the token, so honoring it there would make the selection circular
implements: requirement:dotenv-files
```
