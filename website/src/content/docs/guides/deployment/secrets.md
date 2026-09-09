---
title: Handling Secrets
description: Where a database password, a signing key, or a client secret is allowed to live at each stage from a laptop to a deployment, and what checks that it stayed there.
sidebar:
  order: 3
---

A secret has one rule in Popcorn Web: it reaches the process as an environment
variable, and it is never written into a file that is committed or copied into
an image. Everything on this page follows from that rule — the two spellings a
configuration file uses to say "the deployment supplies this", the files a
workstation keeps the value in, the way each host injects it, and the checks
that report when the rule was broken.

The rule has one deliberate exception. `config.dev.toml` carries a development
database DSN and a keyring secret generated for the machine that ran `pw init`.
That file is shared with the team on purpose: the password of a database Devbox
runs beside the application is a fixture, not a credential, and `pw doctor`
stays silent about it under `--env=dev`. The same content in `config.prod.toml`
is an error.

## Two spellings in the configuration file

The scaffolded `config.prod.toml` writes the database connection like this:

```toml
[[middleware.rdb.connections]]
group = "default"
dsn = "${DATABASE_URL}"
```

`${NAME}` is expanded when the file loads, from the environment the process was
started with. An undefined name is a load error, not an empty DSN, so a
deployment that forgot the variable stops at startup rather than serving
requests against a pool that connects to nothing. The reference form works for
any key, and it is the only form for an array of tables, because a repeated
table has no flat environment name of its own.

Every scalar key also has an environment variable, derived from its name or set
by an `env:"NAME"` tag, so the second spelling is to leave the key out of the
file and set the variable:

```sh
SESSION_KEYRING_SECRET=$(openssl rand -base64 32)
```

Both spellings end in the same place. Prefer `${NAME}` when the file should
document that a value exists — a reader of `config.prod.toml` then sees every
input the deployment needs, including the ones it does not carry. Prefer the
bare variable when the key is one nobody would look for in the file, such as an
OTLP header. The [configuration reference](/reference/configuration/) lists the
variable of every key.

Your own settings follow the same pattern. A field tagged `secret:"mask"` is
redacted from the startup summary and from `pw doctor`, and an `env` tag names
the variable a deployment sets:

```go
type MailerConfig struct {
	APIKey string `secret:"mask" env:"MAILER_API_KEY" help:"transactional mail API key"`
}
```

Without the tag, a key whose name contains one of the usual tokens — `secret`,
`password`, `token`, `api_key`, `credential`, `dsn` — is masked anyway. A name
outside that list, such as `smtp_pass` or `signing_seed`, is not, so tag it.

## On a workstation

Four dotenv files are read from the working directory at startup, in this
order, before the process environment: `.env`, `.env.local`, `.env.{APP_ENV}`,
`.env.{APP_ENV}.local`. The split is between the two that are committed and the
two that are not. `.env` and `.env.{APP_ENV}` carry values every checkout
agrees on — a shared staging issuer URL, say — and the `.local` pair is what
`.gitignore` excludes, so that is where a secret goes:

```sh
cp .env.example .env.local
$EDITOR .env.local
```

`.env.example` lists, with empty values, every variable the capabilities you
selected at `pw init` read outside development. Put a value that belongs to one
environment in `.env.stg.local` rather than `.env.local`, so running the same
checkout as staging and as development does not share a credential between them.
The full mechanics — precedence, where `APP_ENV` itself may be set, what a
Cloudflare Workers build reads instead — are in
[Secrets and dotenv files](/guides/architecture/configuration/#secrets-and-dotenv-files).

## In a container

The runtime image holds the binary and `config.prod.toml`, and nothing else: no
shell, no package manager, and no secret. `.dockerignore` keeps `config.dev.toml`
and every `.local` dotenv file out of the build context, so a secret cannot be
copied into a layer by accident, and an image layer is readable by anyone who
can pull the image even after a later layer deletes the file.

The variables come from the platform. Compose's `environment`, an ECS task
definition's `secrets` with `valueFrom`, Cloud Run's `--set-secrets`, a
Kubernetes `Secret` mounted as `env`: each of these sets the variable before
the process starts, which is the only moment the file layer can expand
`${NAME}`.

A secret mounted as a file is read as well. Every regular file under
`/run/secrets` — where Docker puts a Compose or Swarm secret, and where a
Kubernetes `Secret` volume is conventionally mounted — is one variable: the
file name is the variable name, its content is the value. So a Compose file
can hand over the database connection without an environment entry:

```yaml
services:
  app:
    secrets: [DATABASE_URL]
secrets:
  DATABASE_URL:
    file: ./deploy/database_url.txt
```

The directory is read after the dotenv files and before the process
environment, so a variable set by both takes the process's value. A value
read from it is masked in the startup summary and in `pw doctor` whatever its
key is called, because the mount says it is a secret more reliably than a
field name does. The same applies to every value read from a `.local` dotenv
file. The
[container images guide](/guides/deployment/container-images/#configuration-and-secrets)
covers what the scaffolded `Dockerfile` copies and why.

## On Cloudflare Workers

A Worker has no filesystem, so neither the TOML nor a dotenv file is read there.
`pw build --target cloudflare-workers` flattens the scalars of `config.prod.toml`
into `vars` in `wrangler.jsonc` under the same variable names a container uses,
and a `${NAME}` reference is resolved from a Wrangler secret of that name:

```sh
npx wrangler secret put DATABASE_URL
```

The [serverless guide](/guides/deployment/serverless/) covers the rest of the
target.

## What checks that the rule held

`pw doctor --env=prod` reads the project the way the deployment would and
reports, by key and file and never by value:

- a secret set as a literal in the configuration file (PW0412), and the file
  it is in being tracked by git (PW0415) or readable beyond its owner (PW0416);
- a value still at its scaffolded placeholder (PW0413), in every environment;
- one literal shared between two environment files under `--env=all` (PW0414);
- a value assigned in `.env.example`, which is committed (PW0438).

A secret read from `.env.prod.local` is not a literal in the configuration
file, so PW0412 stays quiet for it, but the file itself is still held to
PW0415 and PW0416. The startup summary tells the same story at run time: every
masked key shows `*****` and the file or variable it came from, and a DSN keeps
its host and database so the summary can still say which database this process
is attached to. A value from a `.local` file or from `/run/secrets` is masked on
its origin alone, so a port set in `.env.local` shows as `*****` too — which is
one more reason to keep the shared values in `.env`.

## When a value is not a secret

A port, a feature switch, a timeout, or a log level belongs in the TOML file,
where a reader of the deployment can see it and `pw doctor` can reason about it.
Moving every setting into `.env.local` because it is convenient hides the
configuration from both, and a `--generate-config env` scaffold written to
`.env` rather than `.env.example` does the same in one step: every default it
lists is read at the next start as a value somebody set.
