---
title: Storing Files
description: One bucket interface for uploads and other files, with a directory in development, an S3-compatible store on a process host, and an R2 binding in a Cloudflare Worker.
sidebar:
  order: 8
---

A handler that accepts an upload, renders a report to a file, or serves a
document someone attached needs somewhere to put bytes that is not the
database and not the binary. Popcorn Web gives it one interface and lets the
configuration say where the bytes go.

```go
import "github.com/shibukawa/popcornweb/storage"

func upload(w http.ResponseWriter, r *http.Request) {
	bucket, err := storage.Open(r.Context(), "uploads")
	if err != nil {
		pw.WriteProblem(w, r, pw.InternalServerError(err))
		return
	}
	file, header, err := r.FormFile("file")
	// ...
	err = bucket.Put(r.Context(), "u1/"+header.Filename, file, storage.PutOptions{
		ContentType:   header.Header.Get("Content-Type"),
		ContentLength: header.Size,
	})
}
```

`Open` resolves a bucket by the name the configuration gave it. The value has
`Get`, `Head`, `Put`, `Delete` and `List`; a body is a stream the caller
closes, a listing is paged by a cursor, and a missing key is
`storage.ErrNotFound` on every backend, so one `errors.Is` covers them all.

## Three backends, one configuration shape

```toml
[storage]
enabled = true

# config.dev.toml — a directory under the project, nothing to run
[[storage.buckets]]
name = "uploads"
backend = "local"
directory = "uploads"
```

```toml
# config.prod.toml on a process host — S3, MinIO, or R2 through its S3 API
[[storage.buckets]]
name = "uploads"
backend = "s3"
endpoint = "https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com"
region = "auto"
bucket = "myapp-uploads"
access_key_id = "${R2_ACCESS_KEY_ID}"
secret_access_key = "${R2_SECRET_ACCESS_KEY}"
```

```toml
# config.prod.toml in a Cloudflare Worker — the bucket binding
[[storage.buckets]]
name = "uploads"
backend = "r2"
binding = "UPLOADS"
```

The application links the backends it uses with a blank import, the way it
links a database engine: `storage/local`, `storage/s3`, and `cloudflare/r2`.
A Worker build links `cloudflare/r2` itself and refuses any other backend at
build and at startup, because a directory and a socket are both out of reach
there; the same file with `local` or `s3` is correct on a process host.
Credentials are written as `${NAME}` references and never inline.

The array travels to a Worker as JSON in the single `STORAGE_BUCKETS`
variable, which `pw build --target cloudflare-workers` writes from
`config.prod.toml`, and which any host may set by hand.

## When not to use it

A file that ships with the application belongs in the public tree, embedded
or [external](/guides/frontend/public-assets/), where the build computes its
validators and the mount serves it with caching headers. A value read on every
request belongs in the [data cache](/guides/backend/data-cache/) or the
database; a bucket round trip per request is the slow path this interface
does not try to hide. There is no presigned URL and no multipart upload yet;
a handler streams the bytes itself.
