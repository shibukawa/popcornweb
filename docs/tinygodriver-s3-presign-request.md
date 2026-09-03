# Change request: presigned URLs for `storage/s3`, and multipart as a later option

**From:** Popcorn Web (`github.com/shibukawa/popcornweb`)
**Against:** `github.com/shibukawa/tinygodriver` v1.2.11
**Date:** 2026-09-03
**Status:** filed; not yet answered.

## Division of responsibility

Popcorn Web now has an object storage interface (`storage.Bucket`: Get, Head, Put, Delete, List) with three backends: a local directory, `storage/s3` from your repository wrapped thinly, and the Cloudflare R2 binding inside a Worker. The interface, its configuration, the backend registry and the local backend are ours. Your S3 client is the process-host backend as it stands, and the adapter is thin because your `Object` and `ObjectInfo` are the shape we adopted.

| Part of the mechanism | Owner | Status |
| --- | --- | --- |
| `storage.Bucket` interface, registry, configuration, `storage.Open` | Popcorn Web | shipped |
| local directory backend | Popcorn Web | shipped |
| R2 binding backend (`syscall/js`, inside a Worker only) | Popcorn Web | shipped |
| S3 backend | Popcorn Web adapter over your `storage/s3` | shipped |
| whole-object operations, SigV4 header signing, redirect re-signing | tinygodriver | **exists; used unchanged** |
| **SigV4 query-string signing — a presigned URL** | **tinygodriver** | **Ask 1** |
| multipart upload | tinygodriver | Ask 2, low priority, not blocking |

## Summary

1. **Presigned URLs.** One function that returns a URL a browser can GET or PUT directly, signed with SigV4 query parameters, for a bounded time. This is the one thing our interface needs from you and cannot build well without you: the signing key derivation, canonical request and credential scope are already in `cloud/aws`, and a second implementation of them here would drift from yours.
2. **Multipart upload.** Filed for planning only. We have no application asking for an object above the single-Put limit, so nothing waits on it.

## Why presigning matters to us, and why it is asked of you

A Popcorn Web handler that accepts an upload today streams the bytes through the application. For a large file that is the wrong path on every host we deploy to, and on Cloudflare Workers it is a hard limit: the Worker holds the request body in memory, and a Worker is a per-request program. A presigned URL lets the browser talk to the bucket directly and the application only issue the permission.

Cloudflare's own guidance for R2 is the same as S3's: a presigned URL is a SigV4 query-signed URL against the S3 API, made with an R2 access key. The R2 binding has no presign call of its own. So the S3 signer is the one signer on every host, which is why the interface can expose one `Presign` and why it has to come from the place that already owns SigV4.

`cloud/aws` exports `CanonicalQuery`, `URIEncode` and `SHA256Hex`, and `Sign` builds the header form from a `SignRequest`. Query-string signing is the same canonical request with the credential scope, date, expiry and signed-header list carried as `X-Amz-*` query parameters, `UNSIGNED-PAYLOAD` as the payload hash, and the signature appended as `X-Amz-Signature`. We could write that against your exported helpers. We would rather not: the signing key and the canonical request are yours, your redirect re-signing already proves you keep them right across regions, and a presign that disagrees with your header signer by one encoding rule is a `SignatureDoesNotMatch` that no test of ours catches.

## Ask 1 — `Presign` on the S3 client

We know your `requirement:s3-client-scope` lists presigning under other_apis as out of scope, beside versioning, ACL, tagging and lifecycle. This document is the case for moving that one item across the line: unlike the others it is not a bucket-management API but a second output of the signer you already have, and it is the one operation the R2 binding cannot do for itself.

### The shape we are asking for

```go
// PresignOptions describe the request the URL will authorize.
type PresignOptions struct {
	// Method is GET, PUT or HEAD. DELETE if you want; we do not need it.
	Method string
	// Expires bounds the URL's life; the signer writes it as X-Amz-Expires.
	// S3 caps it at seven days, and the cap is yours to enforce or document.
	Expires time.Duration
	// ContentType, when set for a PUT, is signed so the uploader must send it.
	ContentType string
	// Headers, when set, are signed the same way; Content-Disposition on a
	// GET is the case we have in mind.
	Headers map[string]string
}

// Presign returns a URL that authorizes one request against key without the
// caller's credentials, using the client's endpoint, region, path style and
// credentials as Get and Put do.
func (c *Client) Presign(ctx context.Context, bucket, key string, opts PresignOptions) (*url.URL, error)
```

What we care about, in order:

- **The URL addresses the object the way the client would.** Path style or virtual host, and the endpoint, follow the client's configuration, so a presigned URL for MinIO or R2 is right without a second set of options.
- **`UNSIGNED-PAYLOAD`.** The browser sends the body; the signer cannot hash it.
- **Signed headers are the minimum.** `host` always; `content-type` and whatever `Headers` names when set; nothing else, because every signed header is one the uploader has to reproduce exactly.
- **Now is injectable the way `sign` already takes it**, so a pinned test can check a known signature. We would happily contribute the pinned vector.
- **No network.** Presigning is a pure function of the client and the arguments; a `context.Context` is in the signature only so the shape matches the rest of the client, and it is fine if it goes unused.

### What we will do with it

`storage.Bucket` gains `Presign(ctx, key, PresignOptions) (*url.URL, error)`. The S3 backend calls yours. The R2 backend calls yours too, against the R2 S3 endpoint, when the bucket's configuration carries an access key beside the binding; without one it refuses by name. The local backend answers with a URL the application itself serves under an HMAC, so a developer loop needs no bucket, and that same route is the fallback for a deployment that does not want to expose a bucket at all.

### Where it should live

On `*Client`, beside `Get` and `Put`. It reads the same endpoint, region and credentials, and an application that has a client has everything a presign needs. A free function taking the pieces apart would make the caller repeat what the client already resolved, including the path-style decision.

## Ask 2 — multipart upload (**low priority; do not schedule on our account**)

`CreateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload` and `AbortMultipartUpload`, as methods on the client, with the part list carried as `[]CompletedPart{PartNumber, ETag}`. It is filed so that the two asks are one document. We have no application above the single-Put limit, our interface does not expose it, and the R2 binding's multipart is not exposed by the Go adapter we use there either, so it would be S3-only for now. If you take it, a `Presign` for a part upload (`UploadId` and `PartNumber` as query parameters) is what makes it browser-driven, and that is the only interaction between the two asks.

## Not an ask — input on the client, from using it as a backend

- **`Object.Body` is a stream, and we kept it one.** Our interface hands the body up unread, which is the right thing for a large object and the reason our adapter is thin. We would treat a change of that contract as a breaking change on our side; it is worth stating in your scope as something you keep.
- **`ErrNoSuchKey` is the one error we translate.** Every other error passes through unchanged. If the sentinel set grows, `ErrNoSuchBucket` is the next one an application would test for.
- **`List` with `MaxKeys` and `NextToken` maps onto our paging one for one.** Nothing to change; recorded so it stays that way.
