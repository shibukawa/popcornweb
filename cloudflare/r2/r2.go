//go:build js && wasm

// Package r2 reaches a Cloudflare R2 bucket through the binding a Worker's
// environment carries, as the r2 backend of requirement:object-storage and as
// the store the external public tree is served from, per
// requirement:cloudflare-r2-storage.
//
// Inside a Worker the package opens the binding by name. Outside a Worker the
// host build of this package reports that the binding does not exist.
package r2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/storage"
	cfr2 "github.com/syumai/workers/cloudflare/r2"
)

func init() {
	storage.RegisterBackend(pwruntime.StorageBackendR2, func(ctx context.Context, config pwruntime.StorageBucketConfig) (storage.Bucket, error) {
		return Open(config.Binding)
	})
}

// defaultPageSize bounds a listing page when the caller set no limit.
const defaultPageSize = 1000

// Bucket is one R2 bucket binding.
type Bucket struct {
	binding string
}

// Open names a bucket by its binding. The binding is looked up per call
// rather than here, because the Worker env is per request.
func Open(binding string) (*Bucket, error) {
	if strings.TrimSpace(binding) == "" {
		return nil, errors.New("popcornweb/cloudflare/r2: empty binding name")
	}
	return &Bucket{binding: binding}, nil
}

func (b *Bucket) bucket() (*cfr2.Bucket, error) {
	bucket, err := cfr2.NewBucket(b.binding)
	if err != nil {
		return nil, fmt.Errorf("popcornweb/cloudflare/r2: binding %s: %w", b.binding, err)
	}
	return bucket, nil
}

func info(key string, object *cfr2.Object) storage.ObjectInfo {
	return storage.ObjectInfo{
		Key:          key,
		Size:         int64(object.Size),
		ETag:         object.HTTPETag,
		ContentType:  object.HTTPMetadata.ContentType,
		LastModified: object.Uploaded,
		Metadata:     object.CustomMetadata,
	}
}

// Get opens one object; the body streams from the binding.
func (b *Bucket) Get(ctx context.Context, key string) (*storage.Object, error) {
	bucket, err := b.bucket()
	if err != nil {
		return nil, err
	}
	object, err := bucket.Get(key)
	if err != nil {
		return nil, err
	}
	if object == nil {
		return nil, storage.ErrNotFound
	}
	body := object.Body
	if body == nil {
		body = strings.NewReader("")
	}
	return &storage.Object{ObjectInfo: info(key, object), Body: io.NopCloser(body)}, nil
}

// Head reports an object's metadata without its body.
func (b *Bucket) Head(ctx context.Context, key string) (*storage.ObjectInfo, error) {
	bucket, err := b.bucket()
	if err != nil {
		return nil, err
	}
	object, err := bucket.Head(key)
	if err != nil {
		return nil, err
	}
	if object == nil {
		return nil, storage.ErrNotFound
	}
	result := info(key, object)
	return &result, nil
}

// Put stores one object under key with the media type given.
func (b *Bucket) Put(ctx context.Context, key string, body io.Reader, options storage.PutOptions) error {
	bucket, err := b.bucket()
	if err != nil {
		return err
	}
	_, err = bucket.Put(key, io.NopCloser(body), &cfr2.PutOptions{HTTPMetadata: cfr2.HTTPMetadata{ContentType: options.ContentType}, CustomMetadata: options.Metadata})
	return err
}

// Delete removes one object; a missing key is not an error.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	bucket, err := b.bucket()
	if err != nil {
		return err
	}
	return bucket.Delete(key)
}

// List returns one page of keys under a prefix. The binding lists the whole
// bucket, so the page is cut here and the cursor is the last key returned.
func (b *Bucket) List(ctx context.Context, options storage.ListOptions) (*storage.ListPage, error) {
	bucket, err := b.bucket()
	if err != nil {
		return nil, err
	}
	objects, err := bucket.List()
	if err != nil {
		return nil, err
	}
	limit := options.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	page := &storage.ListPage{}
	for _, object := range objects.Objects {
		if !strings.HasPrefix(object.Key, options.Prefix) || object.Key <= options.Cursor {
			continue
		}
		if len(page.Objects) == limit {
			page.NextCursor = page.Objects[len(page.Objects)-1].Key
			break
		}
		page.Objects = append(page.Objects, info(object.Key, object))
	}
	return page, nil
}

// ExternalAssets serves the external public tree from the bucket: the build
// uploads each file under its manifest path, and the middleware asks for
// that path.
func ExternalAssets(binding string) middlewares.ExternalAssetSource {
	return externalAssets{binding: binding}
}

type externalAssets struct{ binding string }

func (source externalAssets) OpenExternalAsset(ctx context.Context, name string) (middlewares.ExternalAsset, bool, error) {
	bucket, err := Open(source.binding)
	if err != nil {
		return middlewares.ExternalAsset{}, false, err
	}
	object, err := bucket.Get(ctx, name)
	if errors.Is(err, storage.ErrNotFound) {
		return middlewares.ExternalAsset{}, false, nil
	}
	if err != nil {
		return middlewares.ExternalAsset{}, false, err
	}
	body, err := storage.ReadAll(object)
	if err != nil {
		return middlewares.ExternalAsset{}, false, err
	}
	modTime := object.LastModified
	if modTime.IsZero() {
		modTime = time.Time{}
	}
	return middlewares.ExternalAsset{Body: body, ETag: object.ETag, MediaType: object.ContentType, ModTime: modTime}, true, nil
}
