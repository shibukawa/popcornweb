---
title: pw doctor
description: Report what a named environment would run, what stands behind it, and what is wrong there — without starting the application.
sidebar:
  order: 7
---

```sh
pw doctor
pw doctor --env=prod
```

A Popcorn Web application decides what it will run from two places that no
single file contains. Configuration selects a session backend, a database, an
identity provider. The binary decides whether the code implementing those
choices was ever linked, because a plugin registers itself through a blank
import. Both halves are correct on their own, and a project can be wrong in the
gap between them: `session.backend = "rdb"` in a file whose application never
imports the plugin that registers `rdb`.

Startup catches that. It catches it in production, at the moment the process
refuses to serve, with a message that names the problem and not the import line
that fixes it. `pw doctor` answers the same question earlier, and from a machine
that is not the deployment.

## The environment is an argument, not the shell

Checking that a deployment is configured sensibly is only useful before the
deployment. So the environment to diagnose is an option:

| Command | Diagnoses |
| --- | --- |
| `pw doctor` | the `APP_ENV` of your shell, then `dev` |
| `pw doctor --env=prod` | `config.prod.toml`, with `.env`, `.env.local`, `.env.prod`, and `.env.prod.local` beneath this host's environment |
| `pw doctor --env=stg --env=prod` | both, in one report |
| `pw doctor --env=all` | every `config.*.toml` in the project |

The option changes which files are read. It never reaches an application
process, so it overrides `APP_ENV` for nothing.

That distinction does real work, because severity follows the environment being
diagnosed rather than a fixed opinion about what production ought to look like.
Query logging left on is the arrangement `dev` is supposed to have; anywhere
else it is a warning that names the threshold and whether bind values are
included. A session cookie without `secure` is a note in `dev` and an error in a
deployment. A loopback OIDC issuer is how [`pw dev`](/pw/project/dev/) signs you
in locally, and an outage — or worse — in staging.

One file, judged twice, because the same file means different things in
different places:

```
$ pw doctor --env=dev
0 error, 0 warning, 3 note

$ pw doctor --env=prod
6 error, 4 warning, 2 note
```

## What it reads, and what it refuses to do

`pw doctor` reads the project. It does not build the application, start a
process, or write a file, and it opens no connection unless you ask.

That constraint is not caution for its own sake. The moment you most want a
diagnosis is when the application no longer compiles, and a tool that has to
build it first has nothing to say precisely then. Diagnosing production from
your laptop should also touch nothing in production, and it should not require
production's secrets to be sitting on your laptop.

So the inputs are:

- `popcornweb.toml`, the migration directory, `devbox.json`, `go.mod`, the
  generated artifacts, and what the repository tracks or ignores;
- the configuration file the environment selects, merged over the typed
  defaults, with the layer that won each key;
- your own process environment, marked as such;
- the import graph of `project.main`, resolved with `go list`, which is how a
  missing plugin or driver import becomes visible without a build.

The report opens with the state that produced the findings, because a finding is
only readable next to the value behind it:

```
features
  database             on  sqlite
  session              on  rdb  not linked: github.com/shibukawa/popcornweb/sessionstore/sqlite
  authentication       off
  security headers     on
  query diagnostics    on  auto

middleware, in order
  1. recovery
  2. request id
  3. access log
  4. database pool
  5. session
  6. application handler

database
  default#1  sqlite  default, write
```

## Findings carry an identifier and a fix

```
  error PW0402: connection default#1 uses the mysql scheme and the application links no driver for it
        add: import _ "github.com/shibukawa/tinygodriver/database/sql/mysql"
        fix: add the blank import of the driver package for that scheme
        …/appendix/diagnostics/#pw0402-no-database-sql-driver-answers-the-configured-dsn-scheme
```

Identifiers are stable and never reused, so `PW0402` can be searched, pasted
into an issue, and looked up in the
[diagnostics reference](/appendix/diagnostics/) — a page generated from the same
catalog the command evaluates, so a check cannot exist without its entry.

Secrets are reported by place, never by value. The finding names the key and the
file; the credential itself appears in no section of the report. A DSN keeps the
half that is an operational fact — `postgres://*****@db.internal:5432/app` — so
the report can still tell you which database the environment is attached to. Run
`--env=all` and a literal secret that appears in two environment files is
reported as a match between their keys, with no value on either side. Nothing
else can see that: a running process knows its own environment and not the file
belonging to another.

The classification is by field name, which marks every DSN. A `sqlite://app.db`
path holds no credential, so it produces no disclosure finding — a warning you
learn to scroll past costs you the one that mattered.

Where a secret is kept is a deployment question, so `--env=dev` does not ask it.
The password of a database Devbox runs beside the application belongs in
`config.dev.toml`, which is a file the team shares on purpose. The same content
under `--env=prod` is an error, and so is committing that file. What a secret
*is* is still judged everywhere: a value left at its scaffolded placeholder, or
one shared with a deployment, is a finding in `dev` too.

## What it did not look at

Every report ends with what the run could not determine, and which checks that
suppressed:

```
not examined
  database: --online was not given, so nothing was contacted
    applied migration state and connection reachability were not read
  environment variables: this host does not hold a deployment's environment
    a key whose deployed value arrives from the environment is reported as unknown at this host
```

A clean report from your laptop and a clean report from CI are not the same
claim, and the report says which one you are holding. When authentication is
enabled for `prod` and no provider values are declared anywhere this host can
read, that is either platform injection or a real gap; `pw doctor` cannot tell
which, so it names the variables the deployment must set instead of guessing.

Run the same command in CI, where those variables exist, and the notes become
verdicts:

```sh
pw doctor --env=prod --strict --format=json
```

## Options

| Option | Effect |
| --- | --- |
| `--env=<token>` | environment to diagnose; repeatable, or `all` |
| `--config-path=<path>` | diagnose one explicit file |
| `--format=text\|json` | `json` for CI; its keys are a supported interface |
| `--strict` | make warnings fail too |
| `--online` | permit database connections: reachability and pending migrations |

Without `--online`, the migration section states the pending count as unknown
rather than showing you a database it never contacted. With it, `pw doctor`
connects using the same driver linkage [`pw migrate`](/pw/database/migrate/)
carries, applies nothing, and will not open a SQLite file that does not exist —
opening one would create it, and a diagnosis that writes is not a diagnosis.

## Exit status

`0` when nothing failed, `1` on any error finding, and `1` for a warning under
`--strict`.

## There is no `--fix`

Deliberately. A diagnosis you have to audit before you can trust it is worth
less than one you can read. Every finding instead names the command that
resolves it, and those commands already exist: [`pw add`](/pw/project/add/) for a
capability whose configuration and dependency drifted apart,
[`pw generate`](/pw/project/generate/) for a generated file that outlived its
source, [`pw migrate`](/pw/database/migrate/) for a schema behind its sources.
