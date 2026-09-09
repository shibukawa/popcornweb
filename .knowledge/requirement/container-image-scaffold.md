---
id: requirement:container-image-scaffold
type: requirement
title: Container Image Scaffold
---
api:cli-init writes a Dockerfile, a .dockerignore, and — for a TinyGo project — a Dockerfile.tinygo, so a scaffolded project builds a runnable image before anyone has to discover rule:container-build-inputs by failing.

```yaml
audience: actor:application-developer
motivation:
  - the obvious Dockerfile is COPY . . and go build, and rule:container-build-inputs makes it fail on missing symbols in files nobody can see, because policy:generated-artifacts excludes them from version control
  - the recipe depends on the toolchain, the Tailwind, and the engine answers, and no later command knows them as well as the wizard that asked them
  - requirement:healthcheck-subcommand exists so a shell-less image can be probed, and nothing points a project at it until an image references it
  - the pinned decision:tailwind-host-toolchain and TinyGo versions live in devbox.json, which does not reach inside an image, so a hand-written Dockerfile silently drifts from the toolchain the project was tested with
unconditional:
  rule: the files are written without a question, like .gitignore and .editorconfig
  reason: they change nothing about the application, cost one deletion to decline, and requirement:init-presets has no answer that would make an image undesirable
  not_a_capability: it is not a requirement:incremental-project-capabilities member, so api:cli-add offers no docker entry and api:cli-doctor reports no absence; an existing project copies the files from the requirement:container-deployment-docs page instead
files:
  Dockerfile:
    always: yes
    toolchain: host Go, per decision:separate-tinygo-dockerfile
    builder: the golang image matching the go directive of the scaffolded go.mod, with the Debian release pinned in the tag so a new stable does not rebase the builder while policy:container-runtime-image still names the previous release
    builder_runs: api:cli-build, which is the whole host phase and the compiler in one command
    stages: a builder under rule:container-build-inputs, and a runtime stage under policy:container-runtime-image
  Dockerfile.tinygo:
    written_when: the TinyGo answer is yes
    toolchain: TinyGo, under rule:tinygo-container-operations
    builder: the published TinyGo image at decision:tinygo-042-baseline or later
    builder_runs: api:cli-generate and then an explicit tinygo build, per decision:explicit-tinygo-compile-step
    scheduler_flag: -scheduler=threads written into that invocation when the selected engine speaks a network protocol; omitting it later is a compile error, per the scheduler enforcement of rule:tinygo-runtime-compatibility, so the scaffold writes a correct default rather than the only defence
    same_runtime_stage: policy:container-runtime-image, so the two files differ in the builder stage only
  .dockerignore:
    always: yes
    excludes: .git, .devbox, devbox.d, dist, "**/*_pw_gen.go", "*.db", the local binary, config.dev.toml, and the requirement:dotenv-files .env.dev, .env.local, and .env.*.local
    reason: the image rebuilds the generated Go and the asset tree, and a host copy of either would be copied in and then overwritten or, worse, linked
    development_config_excluded: a development DSN, a development keyring secret, and a devidp roster have no business in a production layer
  config.prod.toml:
    written_by: requirement:environment-switching, which owns it as the second environment file rather than as a container support file
    consumed_here: the runtime stage copies it and sets APP_ENV so policy:config-file-resolution finds it
    health_is_required: requirement:healthcheck-subcommand exits 1 on an unset server.health, so the HEALTHCHECK instruction and that file agree or the image reports unhealthy on its first interval
    completeness:
      constraint: policy:config-file-resolution takes the first readable candidate and stops, so this is a whole configuration rather than a set of overrides on the development one
      what_the_scaffold_can_write: server, observability, and the rdb section, whose element already carries a ${DATABASE_URL} reference for a non-development environment
      what_it_cannot: the session, auth, dynamo, and firestore sections, every value of which is a development endpoint, a development credential, or a secret generated for one machine
      handling: the file names the sections it left out rather than omitting them silently, and points at the requirement:built-in-config-generation --generate-config=toml scaffold for the complete set
      why_not_silent: a section missing from a capability the project has produces a server that starts with it switched off, which is worse than one that refuses to start
parameterized_by_the_wizard:
  project_name: the binary name, the copied path, and the ENTRYPOINT
  toolchain: whether Dockerfile.tinygo is written at all
  tailwind: the builder stage installs the pinned decision:tailwind-host-toolchain standalone executable only when Tailwind is enabled
  database_engine: a comment naming api:cli-migrate as the separate step, per policy:migration-safety; nothing is copied into the runtime stage
  pw_version: pinned to the api:cli-version that ran the scaffold, because api:cli-generate output is read by the framework version it was generated against
  port: the data:server-runtime-config port already written into the scaffolded configuration, in EXPOSE and in the probe
acceptance:
  - docker build succeeds on a fresh clone of a scaffolded project with no host Go, no pw, and no Devbox present
  - the resulting container answers the configured health path and reports healthy to docker inspect
  - the image contains no shell, no Go toolchain, no pw, and no secret
  - a TinyGo project builds from both files, and the TinyGo image is measurably smaller
  - a Tailwind project serves the generated stylesheet from the embedded tree, with no Node.js in either stage
  - a build with dist excluded by .dockerignore still compiles, because flow:public-asset-build creates dist/public before the compiler reads the embed directive
  - the pinned pw version and the framework version in go.mod agree in a freshly scaffolded project
non_goals:
  - a Compose file, which requirement:container-deployment-docs shows rather than scaffolds because its services are the operator's choice
  - a Kubernetes manifest, a Helm chart, or any platform-specific deployment descriptor
  - a migration image; api:cli-migrate as a separate step is documented rather than scaffolded
  - publishing a Popcorn Web base image, which requirement:cli-distribution already excludes for pw itself
  - regenerating or upgrading the files in a project that has them, which is the requirement:incremental-project-capabilities non-goal about upgrading an installed capability
```
