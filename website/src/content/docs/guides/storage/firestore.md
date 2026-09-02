---
title: Firestore
description: Use Firestore in Datastore mode for typed entities, sessions, and authentication without adding a relational database.
sidebar:
  order: 3
---

Firestore can hold application entities and the framework's session and
authentication records without a relational database. Popcorn Web uses the
Datastore API, so the database must be created in **Datastore mode**. A
Firestore Native mode database is a different API and is rejected at startup.

Use this store when a Google Cloud deployment already operates Firestore in
Datastore mode, or when its namespace and transaction model fit the data. Do
not choose it merely as a portable replacement for SQL or DynamoDB: `.pw.sql`
queries do not run against it, and existing records are not migrated.

:::note[Before you start]
This page assumes a project created by [`pw init`](/pw/project/init/). Install
the capability with `pw add firestore`; it creates an `entities/` package, a
starter query, and the `generate.firestore` entry.
:::

## Open the client

The client is opt-in through an import:

```go
package main

import (
	"context"
	"log"

	_ "github.com/shibukawa/popcornweb/database/firestore"
	"github.com/shibukawa/popcornweb/pw"
)

func main() {
	if err := pw.Run(context.Background(), pw.NewServeMux()); err != nil {
		log.Fatal(err)
	}
}
```

For local development, start the Datastore emulator yourself and point the
runtime at it:

```sh
gcloud beta emulators datastore start --host-port=127.0.0.1:8081
```

```toml
[middleware.firestore]
enabled = true
project_id = "demo-popcornweb"
endpoint = "127.0.0.1:8081"
```

`pw dev` does not start this emulator. It belongs to the Google Cloud SDK and
is not a standalone package the project can add to Devbox.

## Declare an entity and its query

The Go type declares both its properties and its key. Datastore keeps the key
beside the properties, so the `name` field below is not stored twice.

```go
// entities/note.go
package entities

import "time"

type Note struct {
	ID        string    `firestore:"-,name"`
	Author    string    `firestore:"author"`
	Body      string    `firestore:"body,noindex"`
	CreatedAt time.Time `firestore:"created_at"`
	ExpiresAt time.Time `firestore:"expires_at,ttl"`
}
```

```text
// entities/notes.pw.firestore
export statement NotesByAuthor(author: string): firestore.many<Note> {
  where author == {author}
}
```

`pw generate` checks every property in the declaration against the struct tags
and writes the codec, key builder, and `NotesByAuthor` function beside the
sources. Renaming `author` in only one file is therefore a generation error,
not a query that quietly returns no rows.

The generated query resolves the process client itself, so its call sites
stay context-only:

```go
for note, err := range entities.NotesByAuthor(r.Context(), accountID) {
	if err != nil {
		return err
	}
	use(note)
}
```

For direct entity operations, take the process handle once with
`firestore.Handle(ctx)` and call `Store`, `Insert`, `Load`, `Update`, and
`Remove` on it. The client is held as process state rather than a
request context value, so nothing pays a context lookup per call. The
[Firestore query format](/reference/firestore-templates/) lists the
tags, query shapes, and generated signatures.

## Deploy the policies the service owns

There is no migration or schema-application step. A kind appears on its first
write, and startup cannot prove that a composite index or TTL policy exists.
Those remain deployment resources.

The `ttl` tag identifies the property but does not enable expiry. Apply the
policy with Google Cloud tooling:

```sh
gcloud firestore fields ttls update expires_at \
  --collection-group=Note --enable-ttl
```

A query that needs a composite index compiles but fails at runtime with
`FAILED_PRECONDITION` until the index is deployed. Declare the index beside the
query so generation can publish the same definition your deployment applies.

## Choose production credentials

Cloud Run, GKE, and GCE should normally use the metadata server:

```toml
[middleware.firestore]
enabled = true
project_id = "my-project"
credentials = "metadata"
timeout = "10s"
max_idle_conns = 16
```

`service_account` is the default and reads `credentials_file` or
`GOOGLE_APPLICATION_CREDENTIALS`. `oauth2` exchanges that key for an access
token. `static` is for a token source installed in Go. The database can be the
default database or a named Datastore-mode database, and `namespace` isolates
every key the process reads and writes.

## Put framework state in Firestore

Sessions and authentication choose their backends independently of application
entities:

```go
import (
	_ "github.com/shibukawa/popcornweb/authstate/firestore"
	_ "github.com/shibukawa/popcornweb/authstore/firestore"
	_ "github.com/shibukawa/popcornweb/database/firestore"
	_ "github.com/shibukawa/popcornweb/sessionstore/firestore"
)
```

```toml
[session]
enabled = true
backend = "firestore"

[auth]
enabled = true
backend = "firestore"
mode = "oidc_only"
```

Each kind is created by its first write. Expired session and ceremony records
are treated as invalid immediately, but removing their bytes still requires a
TTL policy on their `expires_at` property. See [Session storage](/guides/storage/session-storage/)
for the cost and revocation trade-offs.
