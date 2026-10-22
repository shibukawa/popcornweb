---
title: pw dev
description: The development loop — services, generation, migrations, CSS, and restart on change.
sidebar:
  order: 5
---

```sh
pw dev
```

This is the everyday command. It takes no arguments because the project file
defines the loop.

## What it does on startup

1. starts the Devbox services declared in `devbox.json`;
2. runs [`pw generate`](/pw/project/generate/);
3. applies pending migrations, unless `migration.auto` is `false`;
4. builds the Tailwind stylesheet and starts its watcher, if Tailwind is enabled;
5. starts the development identity provider, if `dev.idp.enabled` is `true`;
6. starts the telemetry viewer, unless `dev.otel.enabled` is `false`;
7. prepares local JSONL capture, unless `dev.logs.enabled` is `false`;
8. builds and runs `project.main`.

After startup, it polls watched files twice a second. A change repeats only the
steps affected by that file rather than rebuilding the entire environment. The
restarted application does not reprint its
[configuration summary](/productivity/startup-summary/#restarts-under-pw-dev):
a restart that resolved to the same configuration prints `reloaded`, and one
that did not prints the rows that moved.

## What it watches

- the project's own Go, `.pw.html`, and `.pw.sql` sources;
- the migration directory;
- the Tailwind input, when Tailwind is enabled;
- anything matched by `dev.watch.includes` in `popcornweb.toml`.

The walk covers the module, not the `[generate]` purposes: any Go source is a
rebuild input, including files no purpose generates from. `.git`, `vendor`,
`node_modules`, `.devbox`, and the `public` tree are always skipped, and so is
any worktree made by `git worktree add` below the project root, such as the
checkouts a coding agent keeps under `.claude/worktrees`. Each one holds a
second copy of every source, so counting them would restart the application
whenever someone edits another checkout. A submodule is part of the project and
is walked like any other directory.

`dev.watch.includes` takes relative glob patterns for inputs the walk does not
reach. `dev.watch.excludes` skips a subtree, which is what to reach for when a
large dependency tree makes the walk the slowest step of the loop. Absolute
paths are rejected in both:

```toml
[dev.watch]
includes = ["config.dev.toml", "assets/**/*.svg"]
excludes = ["web/node_modules"]
```

## The port

The application binds `server.port`, and in development it does not insist on
it. A port already taken — a second project open in another terminal, a process
left behind by a loop that did not unwind — moves the run to the next free one
rather than ending it:

```
WARN the configured port could not be bound, so this development run moved to the next free one
     configured_port=8080 port=8081

listening on http://localhost:8081
```

Both numbers are reported, and they mean different things: `server.port` in the
configuration tree is what the file asked for, and the `listening` line at the
end is the address that answers. The second one is where your browser goes. The
console links what the application announced rather than what the project file
says, so its application link follows the shift too.

The search gives up ten ports along, and only a development run makes it.
`APP_ENV=stg`, `APP_ENV=prod`, and every other named environment bind what they
were configured with and fail if they cannot, because a health check, a reverse
proxy, and an operator all go to the port the file names. An unset `APP_ENV`
resolves to development, so a deployment that never set the variable shifts as
well — which is why the warning names the environment, and why setting
`APP_ENV` is what restores the strict bind.

When development needs a fixed port anyway — an external OAuth provider with a
registered callback, say — nothing shifts while the port is free, so the fix is
to stop whatever holds it. [`pw doctor`](/pw/project/doctor/) reports a bound
`server.port` before the loop starts, which is faster than reading it off a
warning after the fact.

## Services

The services declared in `devbox.json` — Valkey by default — run under the
Devbox process manager with its full-screen terminal UI disabled. Their logs
join the same stream as generation, migration, and application output, one
prefixed line per event, instead of painting over it:

```
[valkey	] 1:M 27 Jul 2026 23:02:32.103 * Ready to accept connections tcp
```

A project that needs no service can drop the package from `devbox.json`;
`pw dev` starts whatever Devbox declares and nothing else.

## Tailwind

During development the watcher always produces **unminified** CSS, regardless of
`assets.tailwind.minify`, because minification is the slow part of the loop. A
failed CSS watcher does not take down the server. `pw dev` keeps running and
falls back to watching the input file directly.

`tailwindcss` must be on `PATH`, which is what `devbox shell` is for. See
[Styling](/guides/frontend/styling/).

## Migrations

Pending migrations are applied before the application starts, and again when a
file in the migration directory changes. Turn this off when you would rather
control it yourself:

```toml
[migration]
auto = false
```

## Development identity provider

`pw dev` starts a local OpenID Provider when `dev.idp.enabled` is true, injects
its issuer and credentials into the application process, and stops it with the
loop. Editing the roster reloads it without a restart.

```toml
[dev.idp]
enabled = true
```

See [Development Identity Provider](/productivity/dev-identity-provider/) for the
roster format, the claims, and what the provider does and does not implement.

## Telemetry viewer

`pw dev` also runs a loopback OpenTelemetry receiver and a browser UI, and
points the application at it through the standard OTLP environment variables. It
is on by default, so traces and correlated log records are readable without a
collector:

```
pw dev: telemetry viewer http://127.0.0.1:54321
```

```toml
[dev.otel]
enabled = true
```

It starts nothing when `OTEL_EXPORTER_OTLP_ENDPOINT` is already set, leaving your
own collector in charge. See
[Development Telemetry Viewer](/productivity/dev-telemetry-viewer/).

## Local structured logs

Application logs still appear as readable terminal text. Independently of the
viewer, `pw dev` also captures the structured form in `.log/pw-dev-*.jsonl`.
One invocation owns one file across application rebuilds, and the file appears
only after the first record.

```toml
[dev.logs]
enabled = true
directory = ".log"
```

The directory is relative to the project and is not cleaned automatically.
Disable this switch to retain only the terminal and configured OTLP outputs.
See [Telemetry](/guides/architecture/telemetry/) for the schema and DuckDB
queries.

## The console

`pw dev` serves a browser console beside the application, on a fixed loopback
port, holding the panes the loop needs: project state, static assets, the
database and its declared queries, the template storybook, `pw doctor`, and the
telemetry viewer above.

```
pw dev: console http://127.0.0.1:18081
```

```toml
[dev.console]
enabled = true
port = 18081
```

It is what the rest of the loop is read through, and none of it exists in a
release build. See [Development Console](/productivity/dev-console/).

## Test data endpoints

The application `pw dev` builds carries the `pwdev` build tag, and in the
development environment that binary serves `POST /_pw/test/seed/{dataset}` and
`GET /_pw/test/assert/{dataset}` on its own listener, for loopback callers
only. A browser test suite uses them to reset and verify the database through
the same `testdata/seed` files `pw seed` reads. A release build carries no
endpoint bytes. See [E2E Testing](/productivity/e2e-testing/).

## Stopping

`Ctrl-C` cancels the whole loop, stopping the application, Tailwind watcher, and
Devbox services. That is the only thing that ends it. An application that exits
on its own — a compile error, a panic, a clean return — is reported as
`application exited: …` and the loop keeps watching, because a project spends
most of the time between two working states in a state that does not run. The
next change you save rebuilds and restarts it.
