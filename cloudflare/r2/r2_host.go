//go:build !(js && wasm)

// Package r2 reaches a Cloudflare R2 bucket through the binding a Worker's
// environment carries. This is the host build: there is no binding, so the
// r2 storage backend registers under its name and refuses to open, Open
// reports the same, and ExternalAssets returns a source that reports it per
// request rather than failing the process it was configured into.
package r2

import (
	"context"
	"errors"
	"net/url"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/storage"
)

// ErrOutsideWorker is returned when a bucket binding is opened on a host that
// is not a Cloudflare Worker.
var ErrOutsideWorker = errors.New("popcornweb/cloudflare/r2: a bucket binding opens only inside a Cloudflare Worker; use the s3 backend against R2's S3 API here")

func init() {
	storage.RegisterBackend(pwruntime.StorageBackendR2, func(context.Context, pwruntime.StorageBucketConfig) (storage.Bucket, error) {
		return nil, ErrOutsideWorker
	})
}

// Bucket is one R2 bucket binding.
type Bucket struct{ binding string }

// Presign is unavailable on this host, like every other operation.
func (b *Bucket) Presign(context.Context, string, storage.PresignOptions) (*url.URL, error) {
	return nil, ErrOutsideWorker
}

// Open names a bucket by its binding; on this host it fails.
func Open(binding string) (*Bucket, error) { return nil, ErrOutsideWorker }

// ExternalAssets returns a source that fails every read with
// ErrOutsideWorker.
func ExternalAssets(binding string) middlewares.ExternalAssetSource {
	return externalAssets{}
}

type externalAssets struct{}

func (externalAssets) OpenExternalAsset(context.Context, string) (middlewares.ExternalAsset, bool, error) {
	return middlewares.ExternalAsset{}, false, ErrOutsideWorker
}
