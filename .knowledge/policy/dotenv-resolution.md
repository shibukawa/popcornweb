---
id: policy:dotenv-resolution
type: policy
title: Dotenv Resolution
---
Dotenv files are named by data:runtime-environment like the project-local TOML, but the shared .env, the token's .env.{env}, and the .local override of each layer rather than exclude each other, and the process environment sits above all four.

```yaml
naming:
  base: .env, committed
  base_local: .env.local, ignored by git
  per_environment: .env.{env}, committed
  per_environment_local: .env.{env}.local, ignored by git
  template: .env.example, never read
  committed_versus_local: the committed pair carries values every checkout agrees on; the .local pair is this machine's, which is where a secret goes
  secret_dir: /run/secrets, where Docker and a Kubernetes Secret volume mount file secrets; each regular file is one variable named by the file, dot-prefixed entries skipped, symlinks followed, trailing line endings stripped; skipped when absent
search:
  base_directory: process working directory, the same base policy:config-file-resolution uses for project-local TOML
  candidates:
    - ./.env
    - ./.env.local
    - ./.env.{env}
    - ./.env.{env}.local
    - /run/secrets/* after the files, or the directories a caller set in LoadOptions.EnvSecretDirs
  not_searched: config/, user and system configuration directories, parent directories
read_order:
  - resolve the token from the process environment, then from ./.env and ./.env.local when the process did not declare it
  - hand the four candidates in that order to system:tinybind as LoadOptions.EnvFiles, the .local ones marked Secret, and the secret directory as EnvSecretDirs, which reads them in that order, later source winning on the same name, under the process environment
  - the order is the one Vite and Next read the family in: the environment's own file outranks a machine-wide local one, and the environment's local file outranks everything
  - keep the composed lines for the environment-carried arrays of data:middleware-runtime-config and api:storage-package, which the framework decodes outside the load
token:
  process_first: APP_ENV in the process environment is the token, per data:runtime-environment; an empty process value is unset rather than an override
  base_files: APP_ENV in ./.env or ./.env.local is honored when the process did not set it, the local file winning, so a checkout can pin its own environment
  env_files: APP_ENV in ./.env.{env} or its .local is ignored with a startup warning, because the file was selected by the token it tries to change
  declared: a token taken from a base file counts as declared for the startup warning of pwconfig.EnvironmentDeclared
grammar:
  parser: system:go-envparse, so the accepted text is that library's rather than a spelling of this project's own
  line: NAME=value, with optional surrounding whitespace and an optional export prefix
  value: unquoted, single-quoted, and double-quoted text, mixed within one value; double quotes take JSON escapes; an unquoted # starts a comment
  empty: NAME= sets the variable to the empty string, which the environment layer then treats as set
  comments_and_blanks: a line starting with # and a blank line are skipped
  duplicate_name: the later assignment wins within one file, as it does between files
  not_supported: ${NAME} expansion, shell quoting beyond the above, and YAML forms
  order: entries are read back in name order, so a file reads the same whatever order it was written in
errors:
  absent_file: skipped without a message beyond the policy:startup-summary line that says which files were read
  unreadable_present_file: startup error, because a permission problem on a secrets file is never a fallback case
  malformed_line: startup error naming the file and the line the parser reports
skip:
  supplied_environ: a load whose Environ the caller set reads no file, which is how a Cloudflare Worker entry and internal tools opt out
  reason: those callers have no filesystem, or have a configuration of their own, and a dotenv read there would be a second source nobody asked for
provenance:
  place: system:tinybind PlaceEnvFile plus the file name, as .env or .env.dev.local, so policy:startup-summary and api:cli-doctor say where a value came from
  masking: a value from a .local file or a secret directory is masked by origin, whatever the key is called and whatever its secret tag says, and so is a TOML string that expanded a ${NAME} such a source set; a value from the committed files follows the key's own classification
consumers:
  - api:runtime-configuration
  - data:loaded-configuration
  - api:cli-doctor
  - api:cli-dev
rationale: decision:layered-dotenv-files
```
