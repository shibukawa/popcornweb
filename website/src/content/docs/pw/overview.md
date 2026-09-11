---
title: Overview
description: What the pw development tool does, and where each command fits.
---

One command connects the project lifecycle. `pw` scaffolds the first files,
compiles templates and SQL into Go, runs the development loop, manages the
database, and produces release builds.

```sh
pw help
```

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
  request   send one request to the running application, routed by its OpenAPI
  version   print the version, revision, and toolchain
  help      print this message
```

Install it with Homebrew, Nix, a release archive, or the Go toolchain — see
[Installation](/start/installation/).

## The commands

### Project

| Command | Purpose |
| --- | --- |
| [`pw init`](/pw/project/init/) | create a runnable project |
| [`pw add`](/pw/project/add/) | install a capability the project declined at init |
| [`pw new`](/pw/project/new/) | scaffold one more handler, route, and template |
| [`pw generate`](/pw/project/generate/) | write every build input, stopping before the compiler |
| [`pw check`](/pw/project/check/) | report generated Go that is stale or missing |
| `pw fmt` | format template and query sources into their canonical form |
| `pw i18n` | reconcile message catalogs against the templates that use them |
| [`pw dev`](/pw/project/dev/) | watch, regenerate, migrate, and restart |
| [`pw build`](/pw/project/build/) | produce a release binary |
| [`pw doctor`](/pw/project/doctor/) | report what an environment would run, and what is wrong |
| [`pw request`](/pw/project/request/) | send one request to the running application, routed by its OpenAPI document |
| `pw rename` | rename a template declaration and everything that names it |
| `pw lsp` | serve editor analysis over the Language Server Protocol |

`pw fmt`, `pw i18n`, `pw rename`, and `pw lsp` are documented with the work they
belong to rather than on a page of their own. Formatting appears under
[`pw check`](/pw/project/check/#in-ci), which is where a formatting pass has to
run in a build and why it comes before generation; the catalog commands appear in
[Translated Pages](/guides/frontend/i18n/), beside the message syntax whose
catalogs they maintain; and `pw rename` and `pw lsp` appear in
[Editor Support](/productivity/editor-support/), one because an editor is where
you usually reach it and the other because you never run it yourself.

### Database

| Command | Purpose |
| --- | --- |
| [`pw migrate`](/pw/database/migrate/) | inspect, apply, and roll back migrations |
| [`pw seed`](/pw/database/seed/) | load seed datasets |

## Finding the project

Every command except `pw init` needs a project, but it does not need to run at
the project root. `pw` walks upward from the working directory until it finds
`popcornweb.toml`, allowing subcommands to work from any nested directory. If
the search reaches the top without that file, the command fails with
`popcornweb.toml not found`.

## Exit status

`0` on success, `1` on a command failure, `2` when no command was given. Errors
are written to standard error prefixed with `pw:`. [`pw request`](/pw/project/request/)
shares curl's codes instead, so `2` is a usage error there and `22` a failed
status under `-f`.

## Not to be confused with

The deployed binary has a different command line: configuration flags,
configuration scaffold output, and application-defined subcommands. See
[Custom Commands](/guides/architecture/custom-commands/) for that boundary.
