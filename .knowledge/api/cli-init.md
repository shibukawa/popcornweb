---
id: api:cli-init
type: api
title: pw init
---
pw init creates a runnable Popcorn Web project with a shared document shell, representative handler, typed page template, SQL query, error pages, Devbox environment, and generated-artifact conventions.

```yaml
usage: pw init [myapp] [--preset=<name>] [--yes] [--router=registered|discovered|both] [--tailwind|--no-tailwind] [--tinygo|--no-tinygo] [--devbox|--no-devbox] [--database|--no-database] [--db=sqlite|postgres|mysql] [--dynamo|--no-dynamo] [--redis|--no-redis] [--auth=none|oidc|oidc-passkey|passkey] [--session=rdb|cookie|redis|dynamo] [--devidp|--no-devidp] [--skills=claude|agents|none]
mode: decision:interactive-project-bootstrap, opening on the preset step of decision:preset-first-bootstrap
catalog: the capability questions are the requirement:incremental-project-capabilities catalog api:cli-add installs into an existing project
inputs:
  directory: project directory; it seeds the project name step rather than skipping the wizard
  flags: shortcut answers that also seed the wizard
  preset: requirement:init-presets answers, refused together with any capability flag it would answer
  yes: takes the flags and the defaults without asking, for a scripted run inside a terminal
questions:
  preset:
    first: the screen every terminal run opens on
    catalog: requirement:init-presets
    effect: a named preset answers every question below except the project name, and the review screen shows what it answered
    manual: opens decision:navigable-answer-hub on the defaults instead
    kind: the package preset reaches the api:cli-package scaffold, per requirement:package-project-scaffold, and removes the questions that describe an application rather than answering them
    api_server: the api-server preset writes requirement:jwt-only-api-authentication, per requirement:api-server-scaffold, which the authentication question below still refuses to offer
    shortcut: --preset, beside the --kind spelling api:cli-package already carries
  project_name: directory and Go module name
  tinygo_support:
    default: yes
    yes: api:serve-mux routing and the TinyGo toolchain in Devbox
    no: net/http.ServeMux routing and the host Go toolchain only
    rationale: TinyGo produces much smaller binaries and has the more complete wasm target
  router:
    default: registered
    owner: decision:page-router-scaffold-choice
    registered: the handlers tree, its route example, and OpenAPI, which is the shape every existing project has
    discovered: a concept:page-tree only, for a project whose whole job is an HTML website
    both: both trees on one mux, per decision:dual-router-coexistence
    asked_after: the toolchain question, because it decides which source trees the later answers write their examples into
    shortcut: --router
    directories: the answer scaffolds handlers and pages, which are defaults the purpose lists can move afterwards
  tailwind: optional_css below
  devbox:
    default: yes
    asked_last: it decides how this machine gets its tools rather than what the project contains
    yes: devbox.json and devbox.lock pinning the toolchain and the services
    no: the operator keeps their own setup, such as mise, Docker Compose, Nix, Homebrew, or Scoop; the Valkey question is skipped with it
    consequence: without it nothing pins the decision:tailwind-host-toolchain version, so api:cli-init and api:cli-build name the requirement, the standalone CLI at version 4 or later, rather than the Devbox package identifier that only nixpkgs understands
  agent_skill:
    asked_last: it selects a discovery adapter for the machines editing the project rather than an application capability
    default: claude
    choices:
      claude: .claude/skills/popcornweb/
      agents: .agents/skills/popcornweb/
      none: no generated copy
    source: decision:canonical-popcornweb-skill-source
    preset: never answered by a project preset
    shortcut: --skills
  database:
    default: yes
    yes: data:middleware-runtime-config rdb section, the migrations directory, and the .pw.sql and migration examples
    no: no rdb section and no SQL example, leaving a project that renders and serves only
    rationale: the SQL example, the initial migration, and rule:framework-owned-tables migrations all need a database, so declining it removes them together
  database_engine:
    asked_when: the database answer is yes
    default: sqlite
    choices: sqlite, postgres, and mysql per requirement:database-engine-selection
    writes: the rdb DSN, the dialect of the scaffolded migration and .pw.sql example, and the development server package
    shortcut: --db, which conflicts with --no-database
  dynamodb:
    default: no
    asked_when: always, per requirement:dynamodb-store; it combines with any relational answer including none
    yes: data:dynamodb-runtime-config section, the starter dynamo-tagged type and .pw.dynamo declaration, the generate.dynamo purpose, and the amazon/dynamodb-local package in Devbox
    no: no dynamo section and no generate.dynamo key; pw add dynamo enables it later
    shortcut: --dynamo
    writes_no_migration: the schema is the generated table set, per decision:dynamodb-desired-state-migration
    starter_uses_its_own_type: the scaffolded record carries a store and a load call, because requirement:dynamodb-generation emits a codec only for the directions something uses; a tagged type nobody calls generates nothing at all
    not_an_engine: it never becomes a project.database value, and selecting it alone leaves a project with no SQL dialect and no migrations directory
  redis_valkey:
    default: yes
    requires: the Devbox environment, which is the only thing this answer writes to
    yes: Valkey in the Devbox environment for requirement:contrib-redis-valkey consumers
    no: no Valkey package, keeping the development environment minimal
  authentication:
    default: none
    requires: database, because the login session store is the rdb backend
    none: no data:authentication-runtime-config section is written
    oidc: auth.mode oidc
    oidc-passkey: auth.mode oidc_passkey per decision:authentication-bootstrap-strategy, with recovery.policy oidc
    passkey: auth.mode passkey_only, with registration.policy and recovery.policy both administrator and the bootstrap bounds set
    oauth: auth.mode oauth_only per requirement:init-oauth-login-choice, with auth.oauth.provider x and the client values empty; the emulator question is skipped, because the mode has no issuer to emulate
    not_offered: jwt_only is absent from this question and from --auth, per decision:jwt-only-mode-not-scaffolded; the enum is the four values above and an unrecognized --auth is rejected rather than passed through
    passkey_scaffold:
      when: the selected mode mounts api:passkey-endpoints
      config: passkey.rp_id localhost, passkey.origins the development origin, user_verification required, discoverable preferred
      origin: an OIDC redirect_url in a passkey mode uses localhost rather than 127.0.0.1, because an origin has to sit inside the RP ID and an address can never be one
      accounts: SetAccountLookup for every passkey mode, plus SetAccountActivator and an IssueBootstrapCredential wrapper for passkey_only
      browser: public/passkey.js, dependency free, because the framework serves the endpoints but cannot call navigator.credentials for the page
      page: controls bound by element id, so the template carries no inline script
      emulator: refused outside an OIDC mode, so passkey_only never scaffolds an identity provider roster
  session_storage:
    asked_when: always; session storage is not a login, so a project declaring only a language preference still gets the middleware
    default_without_a_login: cookie, which needs no table, no migration, and no import to hold state that fits in a sealed cookie
    default_with_a_login: rdb, because a login writes a record on every sign-in and normally must end one on demand
    default: rdb
    choices: requirement:state-storage-tiers opaque backends
    rdb: one row per session through the sessionstore/sqlite blank import, with its rule:framework-owned-tables migration
    cookie: sealed into a second cookie with no storage and no import, and cookie_store.secret read from the environment
    redis: server-side TTL through the sessionstore/redis blank import, taking the Valkey development server with it
    dynamo: one item per session through the sessionstore/dynamo blank import, expired by table TTL, per requirement:dynamodb-session-store; it is what the website-aws preset of requirement:init-presets takes
    writes: session.backend, the keys of the selected backend only, and the api:session-backend-plugin import in main
    rationale: the choice is a deployment decision, because every backend reads the same in a handler
  oidc_provider:
    asked_when: the selected mode uses OIDC
    local_emulator: requirement:contrib-devidp enabled through data:project-config dev.idp, with a data:devidp-config starter roster
    external: empty issuer, client id, and client secret that the operator or the environment must supply
    rationale: a skipped question never applies its answer, so a provider choice cannot leak into a project without OIDC
outputs:
  - data:project-config
  - concept:project-layout
  - config.dev.toml for requirement:environment-switching
  - Go module and cmd/myapp/main.go
  - project.toolchain in data:project-config recording the selected compiler
  - the decision:explicit-generation-sources purpose lists in data:project-config, each naming the directories this scaffold actually created, with generate.pages named only for a router answer that creates a tree
  - flow:handler-registration mux for the selected toolchain, only for a router answer that includes the handlers tree
  - handler registration and pw.Parse example, only for a router answer that includes the handlers tree
  - pages/page.pw.html, pages/layout.pw.html, and a pages/users/id_ dynamic route example, only for a router answer that includes the page tree
  - api:page-registry Register wiring in concept:application-entry-point for a page tree, over the pw.NewServeMux mux when the handlers tree was declined and over the handler package mux when it was not
  - templates/document.pw.html shared document shell
  - ui:starter-landing-page as the scaffolded page of every router answer, rather than a greeting heading
  - .pw.html page and 400, 401, 403, 404, 409, 413, and 500 templates, each taking the api:error-renderer model as parameters rather than a fixed heading
  - config.dev.toml observability.stdout_format plaintext, the data:observability-runtime-config development default written down where the operator can see it
  - data:project-config dev.console.port and dev.console.launcher.corner, the two development console values a project has an opinion about; both work absent, and both are written because a developer meets their defaults by finding a bookmarked address moved or a requirement:dev-console-launcher button over their own layout
  - data:dynamodb-runtime-config section, the starter dynamo-tagged type, its .pw.dynamo declaration, and the generate.dynamo purpose, only when DynamoDB is selected
  - .pw.sql query example, only when the database is selected
  - data:project-config project.database naming the selected engine, which api:cli-generate reads as its SQL dialect
  - migrations/00001_init.sql as migration version 1, in the dialect of the selected engine, with the data:migration-source scaffolded_version_1 comment-only body
  - the selected rule:rdb-dsn-resolution engine blank import in main whenever the database capability is selected
  - public directory with non-served .keep sentinel and stable public.go embedding scaffold
  - tinygohelper.go netdev registration for rule:tinygo-runtime-compatibility, only when TinyGo is selected
  - .gitignore excluding **/*_pw_gen.go generated application build inputs, the requirement:local-jsonl-log-capture .log/ directory, and the requirement:dotenv-files .env.local and .env.*.local
  - .env.example, the requirement:dotenv-files template naming every secret-classified variable of the selected capabilities with no value
  - data:project-config dev.logs enabled at .log
  - config.prod.toml beside config.dev.toml, per requirement:environment-switching, carrying the same endpoint paths, data:observability-runtime-config stdout_format json, and no secret
  - the requirement:container-image-scaffold files, which every answer set receives: Dockerfile and .dockerignore, plus Dockerfile.tinygo when TinyGo is selected
  - .vscode/settings.json hiding **/*_pw_gen.go
  - one generated copy of decision:canonical-popcornweb-skill-source at the selected agent discovery path, including requirement:agent-log-analysis-skill, unless --skills=none
  - editor_configuration below
  - Devbox configuration with Valkey when selected, TinyGo when selected, and the selected requirement:database-engine-selection server package, only when the Devbox environment is selected
  - data:authentication-runtime-config section for the selected authentication mode
  - data:devidp-config roster and data:project-config dev.idp when the local emulator is selected, with dev.idp.port pinned rather than reserved, because api:auth-credential-store derives the scaffolded account ID from the issuer and a moving port issues a new account on every run
  - api:authentication-endpoints blank import in main and a sign-in and sign-out control on the starter page
  - api:session-backend-plugin blank import in main for a selected backend other than cookie
  - a data:session-runtime-config keyring.secret generated from crypto/rand, written as a literal into config.dev.toml only, per its development_generation; it is per project rather than a template constant, and rule:configuration-advisories reports it as an error if the same file is ever diagnosed as another token
  - rule:framework-owned-tables migrations from the packages that own those tables, at the versions after the application schema, when the mode serves a login
  - the session table migration only for the rdb backend; another backend leaves that version to the auth migration
  - data:middleware-runtime-config rdb settings carrying the requirement:database-engine-selection DSN for the chosen engine, because the scaffolded migrations and queries need a database, only when the database is selected
editor_configuration:
  editorconfig:
    file: .editorconfig, root true
    defaults: utf-8, lf, a final newline, and trimmed trailing whitespace
    go: tabs, restating what gofmt already does so an editor with no Go support does not fight it
    two_space: .pw.html, .pw.sql, TOML, JSON, CSS, and JavaScript, which is the width rule:template-source-layout indents a block by
    reason: the scaffold writes sources in five languages, and the editor that opens them first decides what the next contributor sees
  extensions:
    file: .vscode/extensions.json recommendations
    always: the Go extension and the EditorConfig extension, which are what the scaffolded sources need to be edited the way they were written
    tailwind: the Tailwind CSS extension, only for a project that selected Tailwind, since a declined capability is never advertised as present
    form: recommendations only; nothing is marked unwanted and nothing is required
    scope: VS Code alone, because it is the only editor with a project-local recommendation file the scaffold can write; .editorconfig is the part every other editor reads
optional_css:
  tailwind:
    - configure requirement:tailwind-css-integration in data:project-config
    - add pinned decision:tailwind-host-toolchain package to Devbox
    - create assets/app.css and application-owned CSS output wiring
behavior:
  - start the wizard on the preset step for every terminal run, seeding the project name step from the directory argument when one was given
  - ask nothing but the project name for a named preset, and show the review it answered
  - refuse --preset together with any flag that answers a question the preset answers, before anything is written
  - skip the wizard only for --yes, or for a session with no terminal that was given a name
  - accept --interactive as a no-op, since the wizard it used to request is now the default
  - refuse and print usage when the session has no terminal and no name
  - validate the project name and destination, in the wizard before any file is written
  - refuse to overwrite nonempty destinations by default
  - create files atomically
  - run api:cli-generate
  - scaffold classic rendering according to requirement:nested-html-templates, writing every template under rule:template-source-layout
  - scaffold every tree the router answer selects, and write the document shell and error pages for all three answers because both routers render through them
  - scaffold runtime database configuration for decision:config-driven-database when the database example is enabled
  - refuse an authentication mode without the database, because its login ceremony and allowlist tables need one whatever backend stores the sessions
  - refuse --db together with --no-database, before anything is written
  - accept DynamoDB beside any relational answer, and accept it as the only store, per requirement:dynamodb-store
  - accept an authentication mode backed only by DynamoDB, writing auth.backend = "dynamo", both storage imports, and no auth migration file, per requirement:dynamodb-auth-backend
  - refuse a login with neither store, naming both ways out rather than only the database
  - follow the store the project has when nothing selected a session backend, so a DynamoDB-only project does not default to a relational pool it never opens
  - write the starter migration and .pw.sql example in the dialect of the selected engine, since decision:server-sql-support-tier does not translate between them
  - take the Valkey development server with a Redis-backed session, because the configured session needs a server to reach
  - print the command that generates cookie_store.secret when the cookie backend is selected
  - leave every declined capability to api:cli-add, which reaches the same file state later
reporting:
  policy: policy:cli-progress-reporting
  during: a progress region over the scaffold write, the module resolution, and the api:cli-generate run
  after: the handwritten sources it created, grouped by concept:project-layout directory
  generated: a count and the api:cli-generate command, never the policy:generated-artifacts path list, which is what the same scaffold puts in .gitignore
next_steps:
  - cd myapp
  - devbox shell, only for a project with the Devbox environment
  - pw dev
  - a line naming .env.example and that a copy named .env.local or .env.dev.local is read and ignored by git, per requirement:dotenv-files
  - a notice naming every declined capability, because a scripted run never sees the wizard say it
  - for a server engine, the server to start and the role and database to create
exit:
  success: 0
  wizard_canceled: 0 with a canceled notice and no files written
  invalid_input_or_collision: nonzero with actionable path
```
