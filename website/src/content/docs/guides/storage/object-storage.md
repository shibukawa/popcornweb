---
title: Object storage
description: One bucket interface for uploads and other files, backed by a directory in development, S3-compatible storage on a process host, and an R2 binding in a Cloudflare Worker, with presigned URLs on each.
sidebar:
  order: 3
---

Uploads belong neither in the database nor on the container's disk. Almost
every store that should hold them speaks the S3 API — AWS S3, Cloudflare R2,
MinIO, RustFS, Wasabi — and a Cloudflare Worker reaches R2 through a binding
instead. A handler written against any one of those is tied to it. Popcorn Web
gives the handler one interface and lets the configuration say where the bytes
go, so the same source runs on a laptop, in a container and at the edge.

```go
package handlers

import (
	"bytes"
	"crypto/rand"
	"net/http"
	"path"

	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/storage"
	"github.com/shibukawa/tinybind-go"
)

type uploadInput struct {
	Title string        `payload:"title" check:"required,maxlen=80"`
	File  tinybind.File `payload:"file" check:"required"`
}

type uploadResult struct {
	Key string `json:"key"`
}

func init() { mux.HandleFunc("POST /uploads", upload) }

// newObjectID is a random key segment; the client's file name never becomes one.
func newObjectID() string { return rand.Text() }

func upload(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[uploadInput](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	bucket, err := storage.Open(r.Context(), "uploads")
	if err != nil {
		pw.WriteProblem(w, r, pw.InternalServerError(err))
		return
	}
	// The client controls Filename, so it travels as metadata, never as a key.
	key := "u1/" + newObjectID() + path.Ext(input.File.Filename)
	err = bucket.Put(r.Context(), key, bytes.NewReader(input.File.Content), storage.PutOptions{
		ContentType:   input.File.ContentType,
		ContentLength: int64(len(input.File.Content)),
		Metadata:      map[string]string{"title": input.Title, "filename": input.File.Filename},
	})
	if err != nil {
		pw.WriteProblem(w, r, pw.InternalServerError(err))
		return
	}
	pw.WriteAPI(w, r, uploadResult{Key: key})
}
```

`storage.Open` resolves a bucket by the name the configuration gave it. The
value has `Get`, `Head`, `Put`, `Delete`, `List` and `Presign`. A body is a
stream the caller closes, a listing is paged by a cursor, and a missing key is
`storage.ErrNotFound` on every backend, so one `errors.Is` covers them all.

Two limits apply before the bytes ever reach the bucket: `server.max_request_body`
(10 MiB by default) and the multipart body limit. Raise both when the endpoint
accepts real files. And `ContentType` matters at download time rather than
upload time — the store returns what you sent as the object's `Content-Type`
later — so an application that serves objects back to a browser decides the type
itself rather than trusting the part header.

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
links a database engine: `storage/local`, `storage/s3` and `cloudflare/r2`.
Start with `local`. It needs nothing running, keeps each object as a file with a
sidecar for its media type and metadata, and is what `pw dev` and a test suite
should run on. Move to `s3` for any process host; the endpoint is the only
setting that differs between providers, and addressing defaults to
virtual-host style for `amazonaws.com` and path style everywhere else, which is
what S3-compatible servers expect. A Worker build links `cloudflare/r2` itself
and refuses `local` and `s3` at build and at startup, because a directory and a
socket are both out of reach there; the same file with those backends is
correct on a process host.

Credentials are written as `${NAME}` references and never inline. The array
travels to a Worker as JSON in the single `STORAGE_BUCKETS` variable, which
`pw build --target cloudflare-workers` writes from `config.prod.toml`, and
which any host may set by hand.

## Presigned URLs

A large upload should not stream through the application, and a download should
not either. `Presign` returns a URL the browser uses directly, for one method
and a bounded time:

```go
put, err := bucket.Presign(r.Context(), key, storage.PresignOptions{
	Method:      http.MethodPut,
	Expires:     15 * time.Minute,
	ContentType: "image/png", // signed: the browser must send exactly this
})
get, err := bucket.Presign(r.Context(), key, storage.PresignOptions{})
```

On `s3` the URL is SigV4 query-signed against the endpoint, from the client's
own signer. On `r2` the same signer is used against R2's S3 API, because the
binding has no presign of its own; give the bucket `endpoint`,
`access_key_id` and `secret_access_key` beside its `binding`, or `Presign`
reports what it lacks. On `local` the URL is a relative path under `/_storage/`
that the application serves itself with a key generated per process: a PUT
stores the body, a GET streams it back, and the URL dies with the process or
its expiry. That route is mounted only when a local bucket is configured, and
it is also the shape a deployment gets when it does not want to expose a bucket
at all.

## Reaching the client directly

The `s3` backend is [tinygodriver](https://github.com/shibukawa/tinygodriver)'s
`storage/s3`, a client that speaks the S3 REST API itself and signs with SigV4,
and that exists because `aws-sdk-go-v2` and `minio-go` do not compile under
TinyGo. Nothing about it is TinyGo-only. Reach past the interface to it for the
operations the interface does not carry — a byte range with `GetRange`, a
multipart upload, `WithUnsignedPayload` for a stream too large to hash — by
building a client from the same settings:

```go
client, err := s3.New(s3.WithEndpoint(endpoint), s3.WithRegion(region),
	s3.WithCredentials(s3.Credentials{AccessKeyID: id, SecretAccessKey: secret}))
object, err := client.GetRange(ctx, "myapp-uploads", key, 0, 1<<20)
```

One detail of the client is worth knowing even through the interface. SigV4
signs a hash of the payload, so a `Put` reads the body twice: a body that
implements `io.Seeker` — a `*bytes.Reader`, an `*os.File` — is hashed and
rewound, and anything else is buffered in memory first. Hand `Put` something
that seeks when you can.

Errors through the interface are `storage.ErrNotFound` and the backend's own
otherwise; the S3 sentinels (`s3.ErrAccessDenied`, `s3.ErrBadCredentials`,
`s3.ErrNoSuchBucket`) and `*s3.Error` with its request ID are reachable with
`errors.Is` and `errors.As` for logging. `pw.WriteProblem` turns any error it
does not recognise into a 500 that is logged in full and reported as
`internal error`, which is the right answer for every one of them but a missing
key.

## Local development

The `local` backend is the default answer. When a test has to prove the S3 path
itself, any S3-compatible server works and the endpoint setting is the only
difference from production; [RustFS](https://rustfs.com/) starts in one command:

```sh
docker run -d --name rustfs -p 9000:9000 \
  -e RUSTFS_ACCESS_KEY=rustfsadmin -e RUSTFS_SECRET_KEY=rustfsadmin \
  -e RUSTFS_VOLUMES=/data rustfs/rustfs
```

## When not to use it

A file that ships with the application belongs in the public tree, embedded or
[external](/guides/frontend/static-assets/#when-a-file-should-not-be-in-the-binary),
where the build computes its validators and the mount serves it with caching
headers. A value read on every request belongs in the
[data cache](/guides/backend/data-cache/) or the database; a bucket round trip
per request is the slow path this interface does not try to hide. Multipart
upload is not in the interface; an object above the single-`Put` limit reaches
the client directly, as above.
