---
title: Installation
description: Install the pw command through Homebrew, Nix, a release archive, or the Go toolchain, and add Popcorn Web to a Go module.
sidebar:
  order: 1
---

The `pw` command handles scaffolding, code generation, migrations, and the
development server. Install it first. Projects created by `pw init` pin their Go
toolchain, so you do not need to install a separate Go version before creating
the project.

## Installing `pw`

### Homebrew

```sh
brew install shibukawa/tap/pw
```

The formula installs a prebuilt binary from the tagged release, on macOS
(Apple Silicon and Intel) and Linux. Upgrades come with `brew upgrade`.

### Nix

```sh
nix run github:shibukawa/popcornweb#pw -- version
```

That runs `pw` without installing anything. To put it on `PATH`, add the flake's
`packages.<system>.pw` to your profile or environment, or use the exported
`overlays.default` from a flake of your own.

The derivation builds from source with `buildGoModule`, so it covers
`x86_64-linux`, `aarch64-linux`, and `aarch64-darwin`. Intel macOS is served by
the Homebrew formula and the release archives instead — nixpkgs dropped that
platform.

The flake also exposes a `devShells.default` with Go, `gopls`, and TinyGo, if
you want the host toolchain without Devbox.

### Release archive

Every tag publishes one archive per target plus a `checksums.txt`, on the
[releases page](https://github.com/shibukawa/popcornweb/releases). Extraction
yields `pw` directly, with no directory prefix, so verifying the checksum and
moving the binary onto your `PATH` is the whole procedure. Windows is covered
here and by no other channel.

### Go toolchain

```sh
go install github.com/shibukawa/popcornweb/cmd/pw@latest
```

This works and stays supported. It is listed last because it needs a Go
toolchain that matches the module's requirement, which is exactly the
prerequisite the other three channels remove.

### Checking

```sh
pw version
```

```
pw 0.1.0 (abc1234, darwin/arm64, go1.27.0)
```

`pw help` lists every command:

```
Usage: pw <command> [arguments]

Commands:
  init      create a project in a new directory
  add       enable a capability in a project that declined it
  new       scaffold a handler or a page beside the ones you have
  generate  write everything a compiler needs, stopping before the compiler
  check     report generated files that are stale or missing
  fmt       format template sources into their canonical form
  i18n      reconcile message catalogs against the templates that use them
  migrate   inspect and apply database migrations
  seed      load seed datasets into the database
  build     run generate and then compile the project
  dev       watch, regenerate, rebuild, and restart
  doctor    report what a named environment will actually run
  version   print the version, revision, and toolchain
  help      print this message
```

[pw command](/pw/overview/) says what each one is for and where it is documented.

## The library

Popcorn Web requires **Go 1.27 or later**.

For a new project, [`pw init`](/pw/project/init/) writes a `go.mod` that already
requires the framework; no manual `go get` is needed. An existing module needs
one additional step:

```sh
go get github.com/shibukawa/popcornweb
```

Application code imports the [`pw`](/guides/frontend/handlers/) package, which is
the stable application-facing API:

```go
import "github.com/shibukawa/popcornweb/pw"
```

## Devbox (optional)

Generated projects include a `devbox.json` that pins Go and a Valkey service.
When you use `pw init --tailwind`, it pins the standalone Tailwind CSS binary as
well. Devbox keeps those tools reproducible, but it is optional: if Go is
already on `PATH`, skip `devbox shell` and run `pw dev` directly.

Install Devbox from [jetify.com/devbox](https://www.jetify.com/devbox/).

## Next steps

- [1. Getting started](/tutorial/getting-started/) — create and run your first project.
