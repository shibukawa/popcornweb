// Package s3 reaches an S3-compatible store through system:tinygodriver's
// client: S3 itself, MinIO, and R2 through its S3 API from a process host.
//
//	import _ "github.com/shibukawa/popcornweb/storage/s3"
//
// The adapter is thin because the client's own Object and ObjectInfo are
// the shape the storage interface adopted, per decision:object-storage-in-framework.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/storage"
	tinys3 "github.com/shibukawa/tinygodriver/storage/s3"
)

func init() {
	storage.RegisterBackend(pwruntime.StorageBackendS3, open)
}

// defaultPageSize bounds a listing page when the caller set no limit.
const defaultPageSize = 1000

func open(ctx context.Context, config pwruntime.StorageBucketConfig) (storage.Bucket, error) {
	if strings.TrimSpace(config.Bucket) == "" {
		return nil, errors.New("s3 backend needs bucket")
	}
	options := []tinys3.Option{tinys3.WithPathStyle(config.PathStyle)}
	if config.Endpoint != "" {
		options = append(options, tinys3.WithEndpoint(config.Endpoint))
	}
	if config.Region != "" {
		options = append(options, tinys3.WithRegion(config.Region))
	}
	if config.AccessKeyID != "" || config.SecretAccessKey != "" {
		options = append(options, tinys3.WithCredentials(tinys3.Credentials{AccessKeyID: config.AccessKeyID, SecretAccessKey: config.SecretAccessKey}))
	}
	client, err := tinys3.New(options...)
	if err != nil {
		return nil, err
	}
	return &Bucket{client: client, bucket: config.Bucket}, nil
}

// Bucket is one S3 bucket at one endpoint.
type Bucket struct {
	client *tinys3.Client
	bucket string
}

// New wraps a client and a bucket name, for a caller building one outside
// the configuration.
func New(client *tinys3.Client, bucket string) *Bucket {
	return &Bucket{client: client, bucket: bucket}
}

func translate(err error) error {
	if errors.Is(err, tinys3.ErrNoSuchKey) {
		return storage.ErrNotFound
	}
	return err
}

func info(from tinys3.ObjectInfo) storage.ObjectInfo {
	return storage.ObjectInfo{Key: from.Key, Size: from.Size, ETag: from.ETag, ContentType: from.ContentType, LastModified: from.LastModified, Metadata: from.Metadata}
}

// Get opens one object; the body streams from the endpoint.
func (b *Bucket) Get(ctx context.Context, key string) (*storage.Object, error) {
	object, err := b.client.Get(ctx, b.bucket, key)
	if err != nil {
		return nil, translate(err)
	}
	return &storage.Object{ObjectInfo: info(object.ObjectInfo), Body: object.Body}, nil
}

// Head reports one object's metadata.
func (b *Bucket) Head(ctx context.Context, key string) (*storage.ObjectInfo, error) {
	from, err := b.client.Head(ctx, b.bucket, key)
	if err != nil {
		return nil, translate(err)
	}
	result := info(*from)
	return &result, nil
}

// Put stores one object.
func (b *Bucket) Put(ctx context.Context, key string, body io.Reader, options storage.PutOptions) error {
	puts := []tinys3.PutOption{tinys3.WithContentType(options.ContentType)}
	if options.ContentLength > 0 {
		puts = append(puts, tinys3.WithContentLength(options.ContentLength))
	}
	if len(options.Metadata) > 0 {
		puts = append(puts, tinys3.WithMetadata(options.Metadata))
	}
	_, err := b.client.Put(ctx, b.bucket, key, body, puts...)
	return translate(err)
}

// Delete removes one object; a missing key is not an error.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	err := b.client.Delete(ctx, b.bucket, key)
	if errors.Is(err, tinys3.ErrNoSuchKey) {
		return nil
	}
	return err
}

// List returns one page of keys under a prefix.
func (b *Bucket) List(ctx context.Context, options storage.ListOptions) (*storage.ListPage, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	lists := []tinys3.ListOption{tinys3.WithPrefix(options.Prefix), tinys3.WithMaxKeys(limit)}
	if options.Cursor != "" {
		lists = append(lists, tinys3.WithContinuationToken(options.Cursor))
	}
	result, err := b.client.List(ctx, b.bucket, lists...)
	if err != nil {
		return nil, fmt.Errorf("storage/s3: list: %w", translate(err))
	}
	page := &storage.ListPage{NextCursor: result.NextToken}
	for _, object := range result.Objects {
		page.Objects = append(page.Objects, info(object))
	}
	return page, nil
}
