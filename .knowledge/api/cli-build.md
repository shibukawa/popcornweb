---
id: api:cli-build
type: api
title: pw build
---
pw build generates current source artifacts and builds the configured application entry point.

```yaml
usage: pw build [--debug] [--backend nethttp|fasthttp] [--target lambda|azure-functions|google-cloud-run-functions|vercel-go|cloudflare-workers]
steps:
  - run api:cli-generate
  - run flow:tailwind-css-build production mode when enabled
  - run flow:public-asset-build
  - resolve project.main and optional build settings from data:project-config
  - reject the build when the dependency graph of project.main contains a development-only package such as requirement:contrib-devidp
  - run go build with the resolved settings
defaults:
  package: data:project-config project.main
deployment:
  targets: decision:serverless-target-scope build_axes; the stage and manifest are per target and backend
  cloudflare_workers: requirement:cloudflare-workers-build-target, which reads deploy.cloudflare from data:project-config and is the one target this command compiles with tinygo when the project says so
tinygo:
  invocation: none for the plain build; the command builds with host go even for a project whose toolchain is TinyGo, and only --target cloudflare-workers invokes tinygo, because that target has no Dockerfile for the operator to own the line in
  consequence: the rule:tinygo-runtime-compatibility scheduler constraint reaches an operator through documentation rather than through a flag this command passes
  resolution: decision:explicit-tinygo-compile-step keeps it that way; a TinyGo build is api:cli-generate plus a tinygo build invocation the caller writes, and -scheduler=threads is enforced by the engine package's compile-time guard rather than passed by this command
split: api:cli-generate is every step above except the compiler, and this command is defined as that sequence plus go build, so the two cannot drift
container: this command is the whole builder stage of rule:container-build-inputs, which is why its steps are ordered before any compiler rather than beside it
failure:
  - preserve previous successful output
  - return compiler diagnostics and nonzero status
  - an unlistable dependency graph skips the development-only check and lets the compiler report the real diagnostic
```
