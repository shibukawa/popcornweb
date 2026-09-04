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
	"net/url"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/storage"
	tinys3 "github.com/shibukawa/tinygodriver/storage/s3"
	cfr2 "github.com/syumai/workers/cloudflare/r2"
)

func init() {
	storage.RegisterBackend(pwruntime.StorageBackendR2, func(ctx context.Context, config pwruntime.StorageBucketConfig) (storage.Bucket, error) {
		bucket, err := Open(config.Binding)
		if err != nil {
			return nil, err
		}
		// The binding has no presign of its own; Cloudflare's own guidance
		// is a SigV4 URL against the S3 API with an R2 access key. When the
		// configuration carries one beside the binding, the S3 client's
		// signer is used; presigning makes no request, so the client is
		// never dialed from here.
		if config.AccessKeyID != "" || config.SecretAccessKey != "" {
			name := config.Bucket
			if name == "" {
				name = strings.ToLower(config.Binding)
			}
			options := []tinys3.Option{
				tinys3.WithPathStyle(true),
				tinys3.WithRegion("auto"),
				tinys3.WithCredentials(tinys3.Credentials{AccessKeyID: config.AccessKeyID, SecretAccessKey: config.SecretAccessKey}),
			}
			if config.Endpoint != "" {
				options = append(options, tinys3.WithEndpoint(config.Endpoint))
			}
			if config.Region != "" {
				options = append(options, tinys3.WithRegion(config.Region))
			}
			signer, err := tinys3.New(options...)
			if err != nil {
				return nil, fmt.Errorf("popcornweb/cloudflare/r2: presign signer: %w", err)
			}
			bucket.signer, bucket.signerBucket = signer, name
		}
		return bucket, nil
	})
}

// defaultPageSize bounds a listing page when the caller set no limit.
const defaultPageSize = 1000

// Bucket is one R2 bucket binding.
type Bucket struct {
	binding string
	// signer presigns against the S3 API when the configuration carried an
	// access key; nil otherwise, and then Presign refuses by name.
	signer       *tinys3.Client
	signerBucket string
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

// Presign returns a SigV4 query-signed URL against R2's S3 endpoint, when
// the bucket was configured with an access key beside its binding. Without
// one it wraps storage.ErrPresignUnavailable naming what is missing, since
// the binding itself cannot sign a URL.
func (b *Bucket) Presign(ctx context.Context, key string, options storage.PresignOptions) (*url.URL, error) {
	if b.signer == nil {
		return nil, fmt.Errorf("%w: bucket binding %s has no access_key_id and secret_access_key beside it, and R2 presigns only through its S3 API", storage.ErrPresignUnavailable, b.binding)
	}
	signed, err := b.signer.Presign(ctx, b.signerBucket, key, tinys3.PresignOptions{
		Method: options.Method, Expires: options.Expires, ContentType: options.ContentType, Headers: options.Headers,
	})
	if err != nil {
		return nil, fmt.Errorf("popcornweb/cloudflare/r2: presign: %w", err)
	}
	return signed, nil
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
