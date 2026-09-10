---
id: api:cli-fmt
type: api
title: pw fmt
---
pw fmt rewrites the project's template sources into their canonical form, reports the ones that differ, or filters one source through standard streams.

```yaml
status: implemented at internal/pwcli/fmt.go
usage: "usage: pw fmt [--check] [--stdin=html|sql|dynamo] [--width=n] [<path>...]"
requirement: requirement:template-formatting
preceded_by: decision:tinybind-v03-adoption, adopted first so the pin move was its own diff
implementation:
  library: the system:tinybind templatefmt package, whose SourceAs is pure and whose Dir walks a directory
  wrapper: this command adds source discovery, flag parsing, and exit codes, and no layout of its own
  placement: internal/pwcli, as one file beside generate.go, registered in the two places every other command is
  registration:
    - the dispatch switch in Main
    - the commandSummaries table that printUsage renders, which is what keeps the documented list from going stale
    - a fmtUsage constant, matching the shape every other command's usage constant has
  summary_line: "format template sources into their canonical form"
  order_in_help: after generate, because it is the other thing that reads the same sources
scope:
  default: every directory the data:project-config generate.templates, generate.queries, generate.pages, and generate.dynamo purposes list
  reason: the same purposes api:cli-generate reads, so a source the project does not generate from is not one the project formats either
  recursion: a generate.pages entry is a tree and is walked; the other purposes name one directory each and are not
  paths: naming paths formats those instead, without consulting a purpose, because an explicit argument is already a decision
  suffixes: the .pw pattern set, which internal/pwgen now exports as constants so the generator's discovery globs and the formatter's are one definition
  excluded:
    migrations: data:migration-source .sql files are system:goose sources rather than concept:template-source-dialects, and the purposes do not list them
    generated: a *_pw_gen.go is go/format's, per policy:generated-artifacts
options:
  check: write nothing and exit nonzero when a source differs, for CI and for the requirement:editor-tasks task
  stdin: format one source read from standard input and write it to standard output, naming the dialect because a stream has no suffix to match
  width: the soft column width, defaulting to the templatefmt default rather than to a value restated here
  mutually_exclusive: --stdin takes no path and no --check, because a stream is one source and there is nothing to list
  absent_deliberately:
    preserve_whitespace: the generator's own setting is not readable from here and a second spelling of it would drift; revisit when data:project-config carries one
    diff: a caller wanting a diff runs --check and its own diff tool
exit_codes:
  0: nothing to do, or every source rewritten
  1: a source differs under --check, or a source failed to parse
  2: usage error
behavior:
  - rewrite a file only when its formatted form differs, so an unchanged file keeps its timestamp and no watcher wakes
  - report a parse failure with path, line, and column, leave that file untouched, and continue with the remaining files
  - report every failure rather than the first, because a formatting run is not a build and stopping early hides the rest
  - never invoke generation, so formatting a package that does not currently generate is safe
  - print nothing per unchanged file; a quiet run is a formatted tree
  - under --check, list the differing paths and nothing else, so the output is usable as input
stdin_mode:
  reads: one source, whole
  writes: the formatted source, or nothing at all on failure
  never: touches the filesystem or consults data:project-config, so it works outside a project
  parity: the same contract the decision:formatter-delivery embedded module already implements, which is what makes the delegated editor path a swap rather than a rewrite
guard:
  owner: templatefmt formats twice internally and errors rather than returning an unstable result, from v0.3.2
  here: none of its own, for the reason requirement:editor-formatting gives
  failure_report: a source that does not settle is reported per requirement:formatter-settle-failure-report and fixed upstream
boundaries:
  writes: only under an explicit run, and never from api:cli-generate or api:cli-dev
  network: none
  database: none, so the command needs no configuration beyond data:project-config
acceptance:
  - pw fmt on a formatted tree writes nothing and exits 0
  - pw fmt --check on an unformatted tree lists the paths and exits 1
  - pw fmt --stdin=sql outside any project formats a buffer and exits 0
  - a source that fails to parse is reported with its position, left untouched, and does not stop the run
  - a .pw source outside every generate purpose is not formatted unless named as a path
  - a migrations .sql file is never formatted
  - running twice changes nothing on the second run
```
