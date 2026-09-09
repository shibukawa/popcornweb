---
id: concept:project-layout
type: concept
title: Generated Project Layout
---
The starter project keeps handlers, templates, SQL, and their generated Go files close together so ownership and code review remain obvious.

```yaml
layout:
  popcornweb.toml: data:project-config
  config.dev.toml: policy:config-file-resolution project-local runtime configuration
  config.prod.toml: requirement:environment-switching production configuration, carrying no secret
  config/: optional policy:config-file-resolution project-local runtime configuration directory
  .env.example: requirement:dotenv-files committed template, read by nothing
  .env and .env.{env}: policy:dotenv-resolution shared values, committed and never scaffolded
  .env.local and .env.{env}.local: policy:dotenv-resolution secrets and machine-local values, ignored by git
  Dockerfile: requirement:container-image-scaffold host Go container recipe
  Dockerfile.tinygo: the same recipe for the TinyGo toolchain, only in a TinyGo project, per decision:separate-tinygo-dockerfile
  .dockerignore: keeps the host copy of generated Go, dist, and development configuration out of the build context
  go.mod: Go module definition
  go.sum: Go dependency checksums
  .gitignore: excludes **/*_pw_gen.go, public/**/*.zstd, .env.local and .env.*.local, and other build-only output
  .editorconfig: the indent, encoding, and line-ending rules of the scaffolded sources, so an editor with no Go or template support still writes them the way rule:template-source-layout and gofmt do
  .vscode/settings.json: hides **/*_pw_gen.go from the editor explorer
  .vscode/extensions.json: the editor extensions this project's sources need, recommended rather than required
  devbox.json: development tools and default Valkey service
  devbox.lock: pinned Devbox dependencies
  assets/app.css: optional Tailwind CSS configuration and plugin declarations
  assets/plugins/*.mjs: optional pinned standalone-compatible Tailwind plugin modules
  public/: requirement:public-asset-delivery source tree
  public/.keep: non-served empty-tree embed sentinel
  public/generated/app.css: optional generated Tailwind output
  public/*.zstd: generated flow:public-asset-build sidecars
  public.go: api:cli-init scaffolded embedded PublicFS accessor
  tinygohelper.go: TinyGo-only netdev registration scaffolded for TinyGo projects
  cmd/myapp/main.go: concept:application-entry-point
  cmd/myapp/popcornweb_bootstrap_pw_gen.go: generated registration-package linker
  handlers/index.go: package mux and Handlers accessor, written by api:cli-init or by api:cli-new for a new handler package
  handlers/home_handler.go: route registration, request types, and net/http handler
  handlers/home.pw.html: typed HTML source
  handlers/home_pw_gen.go: generated HTML and request mapping
  handlers/{name}_handler.go: further routes added by api:cli-new
  pages/: concept:page-tree root, only in a project with the discovered router
  pages/layout.pw.html: ancestor layout wrapping every page below it
  pages/page.pw.html: the root page, served as GET /{$}
  pages/page_pw_gen.go: generated page component
  pages/route_pw_gen.go: generated route parameters and decoder
  pages/routes_pw_gen.go: generated api:page-registry, in the tree root only
  pages/greet/name_/page.pw.html: dynamic route example, serving GET /greet/{name}
  pages/greet/name_/page.go: optional Load and api:page-action-endpoint handlers
  queries/users.pw.sql: named SQL source, only in a project with the database capability
  queries/users_pw_gen.go: generated context-based query functions
  migrations/: data:migration-source handwritten versioned SQL, only in a project with the database capability
  migrations/00001_init.sql: initial application schema as migration version 1
  migrations/{version}_init_popcornweb_{capability}.sql: rule:framework-owned-tables tables, written by api:cli-init or api:cli-add at the next free version
  testdata/seed/: data:seed-dataset files shared by api:cli-seed and api:test-seed
  templates/document.pw.html: requirement:nested-html-templates document shell
  templates/document_pw_gen.go: generated document Fragment and Wrapper
  templates/templates.go: handwritten package marker available before first generation
  templates/400.pw.html: client error page
  templates/404.pw.html: not-found page
  templates/500.pw.html: internal error page
  templates/400_pw_gen.go: generated error page renderer
  templates/404_pw_gen.go: generated error page renderer
  templates/500_pw_gen.go: generated error page renderer
ownership:
  scaffolded_once:
    - public.go
    - tinygohelper.go
  handwritten:
    - popcornweb.toml
    - config.{env}.toml or config/config.{env}.toml
    - .env.example, .env, .env.{env}, and the ignored .local files beside them
    - go.mod
    - .gitignore
    - Dockerfile, optional Dockerfile.tinygo, and .dockerignore
    - devbox.json
    - optional assets/app.css
    - optional assets/plugins/*.mjs
    - public source files excluding *.zstd
    - cmd/myapp/main.go
    - handlers/*_handler.go
    - pages/**/page.go
    - "**/*.pw.html"
    - "**/*.pw.sql"
    - migrations/*.sql
    - testdata/seed/*.yaml
  generated: policy:generated-artifacts
  asset_output: optional public/generated/app.css from flow:tailwind-css-build
rules:
  - a capability declined at api:cli-init leaves its files out and api:cli-add writes the same set later, per requirement:incremental-project-capabilities
  - decision:page-router-scaffold-choice decides whether the handlers tree, the pages tree, or both exist; the document shell and the error pages exist either way
  - a page tree directory is a URL segment and a Go package at once, so rule:page-directory-naming governs its name
  - the generated registry lives in the tree root only, because every generated import points down the tree
  - each decision:explicit-generation-sources purpose lists the directories it may read, so handlers appears under both the handler and template purposes and cmd/myapp appears under the config purpose
  - a source outside the purpose that owns its kind is only warned about
  - generated Go is emitted beside its source
  - generated filenames use {source-base}_pw_gen.go
  - generated filenames never start with an underscore
  - generated Go is excluded from version control and recreated by api:cli-generate during application builds
  - templates/document.pw.html owns doctype, html, head, and body; its body contains an unnamed <slot />
  - classic page templates provide leaf content and do not duplicate the document shell
  - public/.keep preserves an otherwise empty public directory and is never externally reachable
  - generated public/**/*.zstd sidecars are ignored by version control
  - Popcorn Web never rewrites scaffolded public.go after initialization
  - public.go init registers its embedded fs.FS without main.go wiring
  - tinygohelper.go carries the //go:build tinygo constraint so host Go builds skip it
  - tinygohelper.go blank-imports system:tinygodriver netdev; without it TinyGo binaries abort with "Netdev not set"
  - api:public-asset-middleware owns public asset delivery
  - default Tailwind scaffolding creates no package.json or Node package lockfile
  - generated SQL packages contain no package-local shared runtime artifact after decision:tinybind-sql-runtime
```
