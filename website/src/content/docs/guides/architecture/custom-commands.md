---
title: Custom Commands
description: Add batch jobs and maintenance tasks to the application binary, reusing the same queries and application code as the web server.
sidebar:
  order: 3
---

The binary produced by `pw build` can run your own subcommands as well as the
web server. This is useful for imports, backfills, scheduled batch jobs, and
operational maintenance: the command lives in the same application, so it can
reuse the generated queries and domain code called by web handlers instead of
building a second CLI project.

For the development tool itself, see [pw command](/pw/overview/).
Configuration files, environment variables, generated scaffolds, and their
precedence are covered in [Application Configuration](/guides/architecture/configuration/).

## Your own subcommands

A subcommand needs only a struct and one registration call. `pw generate` reads
that call site and writes the parser, leaving no parallel flag wiring to
maintain.

```go
package main

type importCommand struct {
	Path   string   `arg:"required" help:"CSV file to import"`
	Label  string   `arg:"optional"`
	Tags   []string `arg:"*"`
	DryRun bool     `default:"false" help:"parse without writing"`
}
```

| Tag | Meaning |
| --- | --- |
| `arg:"required"` | a positional argument that must be supplied |
| `arg:"optional"` | a positional argument that may be omitted |
| `arg:"*"` | variadic positional arguments, zero or more |
| *(no `arg` tag)* | an option, with the same tags as configuration fields |

Register it before configuration is parsed, then dispatch:

```go
func main() {
	handlers.RegisterConfig()
	pw.RegisterSubCommand[importCommand]("import", "import a CSV file")

	if err := pw.ParseConfig(); err != nil {
		log.Fatal(err)
	}
	if command, ok := pw.Command[importCommand](); ok {
		if err := runImport(context.Background(), command); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := pw.Run(context.Background(), handlers.Handlers()); err != nil {
		log.Fatal(err)
	}
}
```

```sh
./myapp import ./data/users.csv --dry-run
```

`pw.Command[T]` reports `false` unless that subcommand was the one selected, so
the `if` doubles as the dispatch. Only the chosen subcommand receives input.
Calling `pw.ParseConfig` yourself is safe: `pw.Run` calls it too, and it parses
exactly once.

Registration order matters for the same reason it does for configuration —
generated definitions register during package `init`, so `RegisterSubCommand`
must run after every `init` and before `ParseConfig`. Registering after parsing
panics.

One name is taken: `healthcheck` belongs to the framework's
[container health probe](/guides/deployment/operational-endpoints/#probing-from-a-shell-less-container),
and registering it panics at startup. A Dockerfile that already says
`HEALTHCHECK CMD ["/myapp", "healthcheck"]` must keep meaning the probe, so the
collision fails fast instead of shadowing one or the other.

The subcommand shares the server's parsed configuration. `pw.Config[T]`
therefore returns the same values the server would use, including the DSN,
without a second settings path to keep in sync.

The database pool is a separate matter: it is opened by `pw.Run` and
`pw.Middlewares`, not by `ParseConfig`. A subcommand that needs a connection
opens one from the configured DSN itself.
