// Package storage is the object storage interface an application writes
// against, with the backend selected by configuration, per
// requirement:object-storage.
//
// A handler resolves a configured bucket by the name it is addressed by and
// stores, reads, lists and deletes objects through it. Which store answers
// — a directory under the project, an S3-compatible endpoint, or an R2
// binding inside a Cloudflare Worker — is the bucket's backend, so the
// handler runs on every host from one source:
//
//	bucket, err := storage.Open(ctx, "uploads")
//	err = bucket.Put(ctx, key, body, storage.PutOptions{ContentType: "image/png"})
//
// A backend registers itself from a blank import, the way an engine does:
//
//	import _ "github.com/shibukawa/popcornweb/storage/local"
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shibukawa/popcornweb/pwruntime"
)

// ErrNotFound reports a key the bucket does not hold. Every backend returns
// it for a missing object, so a caller tests one condition with errors.Is.
var ErrNotFound = errors.New("storage: object not found")

// ObjectInfo is what a store keeps beside an object's bytes.
type ObjectInfo struct {
	Key          string
	Size         int64
	ETag         string
	ContentType  string
	LastModified time.Time
	// Metadata is user metadata, without any backend prefix.
	Metadata map[string]string
}

// Object is an object's metadata and its body. The body is a stream the
// caller closes; a caller needing a piece of it asks the backend for a
// range rather than seeking.
type Object struct {
	ObjectInfo
	Body io.ReadCloser
}

// PutOptions describe a Put beyond its bytes.
type PutOptions struct {
	// ContentType is the media type served back with the object. It is
	// required; a store that guesses from a key would guess differently on
	// two backends.
	ContentType string
	// ContentLength is the body's size when known, which lets a backend
	// stream rather than buffer.
	ContentLength int64
	// Metadata is user metadata kept beside the object.
	Metadata map[string]string
}

// ListOptions page a listing by prefix.
type ListOptions struct {
	Prefix string
	// Limit bounds one page; zero takes the backend's default.
	Limit int
	// Cursor continues a listing from a previous page's NextCursor.
	Cursor string
}

// ListPage is one page of a listing.
type ListPage struct {
	Objects []ObjectInfo
	// NextCursor continues the listing when non-empty.
	NextCursor string
}

// Bucket is one configured bucket. Every method takes a context, and every
// backend answers ErrNotFound for a missing key.
type Bucket interface {
	Get(ctx context.Context, key string) (*Object, error)
	Head(ctx context.Context, key string) (*ObjectInfo, error)
	Put(ctx context.Context, key string, body io.Reader, options PutOptions) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, options ListOptions) (*ListPage, error)
}

// Factory opens a bucket from its configuration. It is called once per
// bucket per process and the result kept.
type Factory func(ctx context.Context, config pwruntime.StorageBucketConfig) (Bucket, error)

var backends = struct {
	sync.RWMutex
	factories map[string]Factory
}{}

// RegisterBackend registers factory under name. A backend package calls it
// from init, so a blank import is what puts a backend in a binary. A
// duplicate or empty name panics: two backends answering one configuration
// value is a build mistake.
func RegisterBackend(name string, factory Factory) {
	if name == "" || factory == nil {
		panic("storage: a backend needs a name and a factory")
	}
	backends.Lock()
	defer backends.Unlock()
	if backends.factories == nil {
		backends.factories = make(map[string]Factory)
	}
	if _, taken := backends.factories[name]; taken {
		panic(fmt.Sprintf("storage: backend %q is already registered", name))
	}
	backends.factories[name] = factory
}

// Backends lists the registered backend names in order.
func Backends() []string {
	backends.RLock()
	defer backends.RUnlock()
	names := make([]string, 0, len(backends.factories))
	for name := range backends.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func factoryFor(name string) (Factory, error) {
	if name == "" {
		name = pwruntime.StorageBackendLocal
	}
	backends.RLock()
	factory, ok := backends.factories[name]
	backends.RUnlock()
	if ok {
		return factory, nil
	}
	return nil, fmt.Errorf("storage backend %q needs its package; add to the application: import _ %q",
		name, backendImport(name))
}

// backendImport names the package that registers a backend, for the error a
// missing blank import produces.
func backendImport(name string) string {
	switch name {
	case pwruntime.StorageBackendR2:
		return "github.com/shibukawa/popcornweb/cloudflare/r2"
	default:
		return "github.com/shibukawa/popcornweb/storage/" + name
	}
}

var opened struct {
	sync.Mutex
	buckets map[string]Bucket
}

// Open resolves a configured bucket by name. The first call opens it and
// later calls return the same value; a name the configuration does not
// carry is an error naming the configured set, so a typo fails at the call
// rather than at the store.
func Open(ctx context.Context, name string) (Bucket, error) {
	config, _ := pwruntime.RegisteredConfig[pwruntime.StorageConfig]()
	if !config.Enabled {
		return nil, errors.New("storage: storage.enabled is false")
	}
	opened.Lock()
	defer opened.Unlock()
	if bucket, ok := opened.buckets[name]; ok {
		return bucket, nil
	}
	var names []string
	for _, element := range config.Buckets {
		names = append(names, element.Name)
		if element.Name != name {
			continue
		}
		factory, err := factoryFor(element.Backend)
		if err != nil {
			return nil, err
		}
		bucket, err := factory(ctx, element)
		if err != nil {
			return nil, fmt.Errorf("storage: open bucket %q: %w", name, err)
		}
		if opened.buckets == nil {
			opened.buckets = make(map[string]Bucket)
		}
		opened.buckets[name] = bucket
		return bucket, nil
	}
	return nil, fmt.Errorf("storage: no bucket named %q; configured: %s", name, strings.Join(names, ", "))
}

// Validate is the startup check: every configured bucket names a registered
// backend and passes its own validation, so a missing blank import is a
// startup failure rather than a first-request one.
func Validate(config pwruntime.StorageConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if !config.Enabled {
		return nil
	}
	for _, element := range config.Buckets {
		if _, err := factoryFor(element.Backend); err != nil {
			return fmt.Errorf("storage.buckets (%s): %w", element.Name, err)
		}
	}
	return nil
}

// Reset forgets every opened bucket, for a test that changes the
// configuration between cases.
func Reset() {
	opened.Lock()
	defer opened.Unlock()
	opened.buckets = nil
}

// ReadAll reads a body whole and closes it, for a caller that needs the
// bytes in memory, such as one serving a Range request from them.
func ReadAll(object *Object) ([]byte, error) {
	if object == nil || object.Body == nil {
		return nil, nil
	}
	defer object.Body.Close()
	return io.ReadAll(object.Body)
}
