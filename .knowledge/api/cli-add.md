---
id: api:cli-add
type: api
title: pw add
---
pw add installs a framework capability into an existing project, so a choice declined at api:cli-init is not a decision the project is stuck with.

```yaml
usage: "pw add [capability]"
requirement: requirement:incremental-project-capabilities
mode: decision:post-init-scaffold-wizard
inputs:
  capability: preselects the first wizard step; omitting it lists the capabilities the project still lacks
  answers: the capability-specific questions, asked in the wizard only
questions:
  capability: single-select over the capabilities the project does not already carry
  database_engine:
    asked_when: the answers reach the database, directly or through a dependency
    choices: sqlite, postgres, and mysql per requirement:database-engine-selection
    default: sqlite
  database_dsn:
    asked_when: an engine has been chosen
    default: the requirement:database-engine-selection DSN for that engine
  oidc_provider:
    asked_when: auth is selected
    choices: requirement:contrib-devidp local emulator, or an external provider left for the operator to fill in
    mode: oidc or oauth, asked as the login protocol first, per requirement:init-oauth-login-choice; the emulator question follows the oidc answer only
  review: lists every file to create, every configuration section to append, and every follow-up command
capabilities:
  devbox:
    writes:
      - devbox.json carrying the toolchain project.toolchain records, plus the Tailwind pin when it is enabled
      - devbox.lock
    consumer: api:cli-dev starts the services it declares, and skips the step entirely for a project without the file
  database:
    writes:
      - data:middleware-runtime-config rdb section in every environment configuration file present
      - the migration directory, holding the same data:migration-source scaffolded_version_1 api:cli-init writes when the project has none, which creates no table
      - the same commented-out starter .pw.sql api:cli-init writes, and the generate.queries entry that opens the purpose for it
      - data:project-config project.database naming the selected engine, which api:cli-generate reads as its SQL dialect
      - the development server package in devbox.json for a server engine, when the project has that environment
    dialect: the starter migration and .pw.sql are written for the selected engine, per requirement:database-engine-selection
    manual: the engine blank import, because the entry point is application-owned
    enables: data:migration-source, api:migration-runner, and .pw.sql generation
    leaves_alone: an existing migration set, which is the application's own schema
  redis-valkey:
    requires: devbox, because the answer writes nothing but a package in that environment
    writes:
      - Valkey package in devbox.json, which api:cli-dev exposes as the development server
      - the endpoint an application passes to requirement:contrib-auth-state-redis and to session.backend redis
  dynamo:
    requires: nothing, per requirement:dynamodb-store; it combines with any database answer including none
    writes:
      - data:dynamodb-runtime-config section in every environment configuration file present
      - the records directory holding one dynamo-tagged starter type and one .pw.dynamo declaration, and the data:project-config generate.dynamo entry that opens the purpose for both
      - the amazon/dynamodb-local package in devbox.json, which api:cli-dev exposes as the development server, following the redis-valkey model
      - the local endpoint and placeholder credentials in config.dev.toml only
    key_may_be_absent: generate.dynamo is optional like generate.pages, so this edit may have to add the key rather than replace it
    manual: the api:dynamo-package import, because concept:application-entry-point is application-owned
    enables: requirement:dynamodb-generation and requirement:dynamodb-migration
    writes_no_migration: the schema is the generated table set, per decision:dynamodb-desired-state-migration, so no migration file and no version range is involved
  auth:
    requires: database, because its login ceremony and allowlist tables live there whichever backend stores the sessions
    session_backend:
      installed: rdb, the backend that fits a project already carrying a database
      alternatives: api:cli-init offers the cookie and redis backends of requirement:state-storage-tiers
    writes:
      - rule:framework-owned-tables migration files for the session store and the authentication tables
      - data:authentication-runtime-config section in every environment configuration file
      - account resolver source that api:authentication-endpoints calls
      - data:devidp-config roster and data:project-config dev.idp when the local emulator is selected
    imports: the application already links plugin/auth through its account resolver, so only the api:session-backend-plugin blank import is printed as a manual step
  discovered:
    writes:
      - the concept:page-tree root with the same starter page, layout, and dynamic route example api:cli-init writes
      - the data:project-config generate.pages entry that opens the purpose for it
    detection: the generate.pages entries, because a tree no purpose lists is a directory nothing generates from
    key_may_be_absent: generate.pages is the one optional purpose, so this is the only capability whose edit may have to add its key rather than replace it
    manual: the api:page-registry Register call, because concept:application-entry-point is application-owned
    requirement: requirement:discovered-page-routing
  registered:
    writes:
      - the handler package, its flow:handler-registration mux and accessor, and one route example
      - the generate.handlers entry, and the same directory added to generate.templates, because a page template sits beside the handler that renders it
    for: a project scaffolded with the discovered-only answer of decision:page-router-scaffold-choice
    manual: the mux wiring in concept:application-entry-point
  tailwind:
    writes:
      - assets.tailwind section in data:project-config for requirement:tailwind-css-integration
      - pinned decision:tailwind-host-toolchain package in devbox.json
      - assets/app.css entry point
    manual: the stylesheet link belongs in the application-owned document shell, so it is printed rather than injected
    without_devbox: the requirement is printed instead of pinned, naming the standalone CLI and its minimum version rather than the Devbox package identifier, because there is no package list to write to
detection: the requirement:incremental-project-capabilities probes; no capability list is recorded anywhere
versioning:
  migration_version: the next free version in the project migration directory
  rationale: an application that already applied 00001 through 00007 must not have those renumbered, so no version range is reserved for the framework
  identity: the name stem published by the owning package, which makes the file recognizable at any version
configuration_edits:
  form: append the missing section to each existing environment configuration file
  reason: operator comments and hand-tuned values must survive the edit
  conflict: an existing section of the same name stops the command
rules:
  - require a data:project-config project root
  - refuse a capability the project already carries, naming the file that proves it
  - detect an installed capability by migration name stem or configuration section rather than by version
  - offer a missing required capability together with the one selected, and refuse the pair when it is declined
  - never rewrite or renumber an existing migration
  - never overwrite an application-owned file; report the conflict and stop
  - write nothing when any step would fail, so a partial capability cannot reach a project
  - run api:cli-generate after a capability that adds generated sources
  - print the commands that finish the installation, starting with api:migration-runner
  - reject a capability whose mode has no implementation, matching api:cli-init
relations:
  init: api:cli-init writes the same files for a new project, from the same capability catalog
  flow: flow:capability-addition
  layout: concept:project-layout
  sibling: api:cli-new adds sources rather than capabilities, and names them after what they are rather than after the router that serves them
exit:
  success: 0
  canceled_wizard: 0 with a canceled notice and no files written
  no_terminal: nonzero with usage
  already_present_or_conflict: nonzero with the path and the reason
```
