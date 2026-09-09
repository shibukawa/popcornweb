---
id: requirement:dotenv-files
type: requirement
title: Dotenv Files
---
A project carries its shared environment values in committed .env files and its secrets in the .local files beside them, which api:cli-init scaffolds as a committed template, .gitignore keeps out of version control, and every configuration load reads as an environment-variable source below the process environment.

```yaml
motivation:
  - config.prod.toml names its secrets as environment variables per policy:container-runtime-image, and a developer needs a file to put those values in that is neither a shell profile nor a committed TOML
  - a keyring secret or a development DSN written as a literal into config.dev.toml is one git add away from the repository, which rule:configuration-advisories can only report after the fact
  - the shape is the one every other tool in the developer's day already reads, which is why the format is dotenv rather than a second TOML
scope:
  resolution: policy:dotenv-resolution
  layering: decision:layered-dotenv-files
  precedence: one layer of data:loaded-configuration, between TOML and the process environment
  variable_names: the same names the environment layer already binds, so a key written in .env.dev and the same key exported in the shell are indistinguishable to the application
scaffold:
  template:
    file: .env.example
    committed: yes, as the one dotenv file .gitignore lets through
    contents:
      - APP_ENV as a comment, because data:runtime-environment is read from the process and the base file only
      - every variable the selected capabilities read from the environment outside development, as NAME= with one help line above it: DATABASE_URL for the database, SESSION_KEYRING_SECRET for a browser login, SESSION_REDIS_DSN for the redis backend, and the AUTH_OIDC issuer, client id, and client secret for an OIDC mode
      - no value; a template holding a real credential is the rule:configuration-advisories env-template-holds-secret error
    derivation: curated per wizard answer, the way config.prod.toml and its gaps comment are, because the definitions that map a key to its variable belong to system:tinybind and are not enumerable from here; a template never names a variable the selected build does not bind
    complete_list: the --generate-config env scaffold of requirement:built-in-config-generation, which the template's header names
  ignore:
    gitignore: .env.local and .env.*.local, so the shared .env, .env.{env}, and the template stay committed
    dockerignore: .env.dev, .env.local, and .env.*.local, so no development or machine secret reaches a build context; api:cli-package skips the whole family when it stages a bundle
  not_written: no .env, .env.{env}, or .local file; the scaffolded development values stay in config.dev.toml until the operator moves them, and the next-steps notice names the .local copies
runtime:
  reads: the application at api:runtime-configuration ParseConfig, and every process that resolves the project's configuration from the project directory, so api:cli-doctor and the api:cli-dev DSN resolution see the same values the application does
  skips: a load whose caller supplied its own Environ, which is how a Cloudflare Worker entry and a test harness say that no filesystem stands behind them
  reports: policy:startup-summary names each dotenv file read, and a value that came from one shows the file as its place rather than the bare word environment
  attribution: system:tinybind 0.5.31 reads the files itself through LoadOptions.EnvFiles, each entry carrying its Secret flag so the caller owns the order, and EnvSecretDirs, and reports PlaceEnvFile plus the file, so the place is known rather than inferred; the layer hands it paths and rewrites the place back to the name a summary shows, keeping the secret-by-origin flag
  secret_mount: /run/secrets is read by default as EnvSecretDirs, so a Compose, Swarm, or Kubernetes file secret reaches the load without a shell in the policy:container-runtime-image; a caller that sets LoadOptions.EnvSecretDirs replaces the default
  dev_loop: api:cli-dev sets APP_ENV=dev on the child when the shell did not, which outranks an APP_ENV in .env, since the loop runs development by definition
  development: api:cli-dev restarts on a change to .env or .env.{env}, on the terms decision:developer-loop-watch-scope gives config.{env}.toml
diagnosis:
  file_set: api:cli-doctor adds the dotenv files of the diagnosed token to the files it reads, so secret-file-not-ignored and secret-file-permissions cover them
  template: env-template-holds-secret in rule:configuration-advisories
acceptance:
  - a fresh api:cli-init project carries .env.example, ignores .env.local and every .env.*.local, and git status shows the template and nothing else of that family
  - a value set in .env.dev.local reaches pw.Config[T] with APP_ENV unset, and the startup summary names .env.dev.local as its place
  - the same key exported in the shell wins over .env.dev.local
  - a key in .env.dev.local wins over the same key in .env.dev, which wins over .env.local, which wins over .env
  - APP_ENV=stg reads .env, .env.local, .env.stg, and .env.stg.local, and never a dev file
  - a malformed line fails startup naming the file and line, as system:go-envparse reports it
  - an absent dotenv file changes nothing, and the summary says none was read
  - a Cloudflare Workers build reads no dotenv file and does not attempt to
  - a file DATABASE_URL under /run/secrets expands the ${DATABASE_URL} of config.prod.toml, and the summary shows the DSN masked with its host kept
  - a PORT written in .env.local binds the port and shows as masked, since the file is a secret source
  - pw doctor reports a tracked .env.dev holding a secret as an error outside dev, and a real credential in .env.example as an error in every environment
non_goals:
  - variable expansion or ${NAME} references inside a dotenv value; the ${…} form belongs to the TOML file layer
  - a dotenv file in the config/ directory or any directory other than the working directory
  - loading a dotenv file into the process environment for code that calls os.Getenv, which stays a raw process read
  - a pw flag that names another dotenv file; the environment token is the only selector
migration:
  - an existing project adds the two .gitignore lines and creates .env.example by hand, or through api:cli-add when it learns the capability
  - a project that exported variables through direnv or a shell profile keeps working, because the process environment still wins
```
