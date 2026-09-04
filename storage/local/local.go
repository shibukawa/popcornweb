// Package local keeps objects in a directory under the project, which is the
// backend api:cli-dev and a test run on: a developer loop that needs an S3
// endpoint before a file can be stored is one nobody starts with.
//
//	import _ "github.com/shibukawa/popcornweb/storage/local"
//
// Each object is one file under the directory, with a sidecar carrying the
// media type and user metadata a file system does not keep.
package local

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/storage"
)

func init() {
	storage.RegisterBackend(pwruntime.StorageBackendLocal, open)
}

// sidecarSuffix names the metadata file beside an object.
const sidecarSuffix = ".pwmeta"

// defaultPageSize bounds a listing page when the caller set no limit.
const defaultPageSize = 1000

func open(ctx context.Context, config pwruntime.StorageBucketConfig) (storage.Bucket, error) {
	directory := strings.TrimSpace(config.Directory)
	if directory == "" {
		return nil, errors.New("local backend needs directory")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return &Bucket{root: root, name: config.Name, secret: secret}, nil
}

// Bucket is one directory of objects.
type Bucket struct {
	root string
	// name is what the configuration calls the bucket, which a signed URL
	// carries so the handler can find its way back here.
	name string
	// secret signs the URLs Presign issues; it is generated per process, so
	// a URL outlives neither the process nor its expiry.
	secret []byte
}

// New opens a directory as a bucket named name, for a caller building one
// outside the configuration, such as a test.
func New(name, directory string) (*Bucket, error) {
	bucket, err := open(context.Background(), pwruntime.StorageBucketConfig{Name: name, Directory: directory})
	if err != nil {
		return nil, err
	}
	return bucket.(*Bucket), nil
}

type sidecar struct {
	ContentType string            `json:"content_type"`
	ETag        string            `json:"etag"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// resolve maps a key to a file under the root, refusing a key that would
// escape it. Keys are slash-separated like object keys everywhere.
func (b *Bucket) resolve(key string) (string, error) {
	cleaned := path.Clean("/" + key)
	if cleaned == "/" || strings.HasSuffix(key, "/") || strings.HasSuffix(key, sidecarSuffix) {
		return "", fmt.Errorf("storage/local: invalid key %q", key)
	}
	return filepath.Join(b.root, filepath.FromSlash(cleaned)), nil
}

func (b *Bucket) info(key, file string) (*storage.ObjectInfo, error) {
	stat, err := os.Stat(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, storage.ErrNotFound
	}
	info := &storage.ObjectInfo{Key: key, Size: stat.Size(), LastModified: stat.ModTime()}
	if raw, err := os.ReadFile(file + sidecarSuffix); err == nil {
		var meta sidecar
		if err := json.Unmarshal(raw, &meta); err == nil {
			info.ContentType, info.ETag, info.Metadata = meta.ContentType, meta.ETag, meta.Metadata
		}
	}
	return info, nil
}

// Get opens one object.
func (b *Bucket) Get(ctx context.Context, key string) (*storage.Object, error) {
	file, err := b.resolve(key)
	if err != nil {
		return nil, err
	}
	info, err := b.info(key, file)
	if err != nil {
		return nil, err
	}
	body, err := os.Open(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &storage.Object{ObjectInfo: *info, Body: body}, nil
}

// Head reports one object's metadata.
func (b *Bucket) Head(ctx context.Context, key string) (*storage.ObjectInfo, error) {
	file, err := b.resolve(key)
	if err != nil {
		return nil, err
	}
	return b.info(key, file)
}

// Put writes one object and its sidecar. The bytes land under a temporary
// name and are renamed into place, so a reader never sees a half-written
// object.
func (b *Bucket) Put(ctx context.Context, key string, body io.Reader, options storage.PutOptions) error {
	file, err := b.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(file), ".put-*")
	if err != nil {
		return err
	}
	digest := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(temporary, digest), body)
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(temporary.Name())
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	meta, err := json.Marshal(sidecar{ContentType: options.ContentType, ETag: `"` + hex.EncodeToString(digest.Sum(nil)[:16]) + `"`, Metadata: options.Metadata})
	if err != nil {
		_ = os.Remove(temporary.Name())
		return err
	}
	if err := os.WriteFile(file+sidecarSuffix, meta, 0o644); err != nil {
		_ = os.Remove(temporary.Name())
		return err
	}
	return os.Rename(temporary.Name(), file)
}

// Delete removes one object; a missing key is not an error.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	file, err := b.resolve(key)
	if err != nil {
		return err
	}
	_ = os.Remove(file + sidecarSuffix)
	if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// List walks the directory in key order. The cursor is the last key of the
// previous page, so a page after a deletion still continues.
func (b *Bucket) List(ctx context.Context, options storage.ListOptions) (*storage.ListPage, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	var keys []string
	err := filepath.WalkDir(b.root, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.HasSuffix(entry.Name(), sidecarSuffix) || strings.HasPrefix(entry.Name(), ".put-") {
			return nil
		}
		relative, err := filepath.Rel(b.root, file)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		if strings.HasPrefix(key, options.Prefix) && key > options.Cursor {
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	page := &storage.ListPage{}
	for _, key := range keys {
		if len(page.Objects) == limit {
			page.NextCursor = page.Objects[len(page.Objects)-1].Key
			break
		}
		file, err := b.resolve(key)
		if err != nil {
			continue
		}
		info, err := b.info(key, file)
		if err != nil {
			continue
		}
		page.Objects = append(page.Objects, *info)
	}
	return page, nil
}

// Presign returns a path under storage.SignedPathPrefix that the application
// serves itself, signed with a key this process generated when the bucket
// opened. The URL is relative: the page that hands it to a browser is on the
// same origin, and the backend does not know the host it is served on. It
// lives for options.Expires, or storage.DefaultPresignExpiry, and dies with
// the process, which is what a development loop wants.
func (b *Bucket) Presign(ctx context.Context, key string, options storage.PresignOptions) (*url.URL, error) {
	if _, err := b.resolve(key); err != nil {
		return nil, err
	}
	method := options.Method
	if method == "" {
		method = http.MethodGet
	}
	expires := options.Expires
	if expires <= 0 {
		expires = storage.DefaultPresignExpiry
	}
	deadline := time.Now().Add(expires).Unix()
	query := url.Values{}
	query.Set("exp", strconv.FormatInt(deadline, 10))
	query.Set("sig", b.signature(method, key, deadline))
	return &url.URL{Path: storage.SignedPathPrefix + b.name + "/" + key, RawQuery: query.Encode()}, nil
}

// VerifySignedRequest is the storage.SelfServing half of Presign: the
// signature covers the method, the key and the expiry, so a URL signed for
// a GET does not authorize a PUT and one past its expiry authorizes nothing.
func (b *Bucket) VerifySignedRequest(r *http.Request, key string) (string, error) {
	expiry, err := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	if err != nil || time.Now().Unix() > expiry {
		return "", storage.ErrBadSignature
	}
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	expected := b.signature(method, key, expiry)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(r.URL.Query().Get("sig"))) != 1 {
		return "", storage.ErrBadSignature
	}
	return method, nil
}

func (b *Bucket) signature(method, key string, expiry int64) string {
	mac := hmac.New(sha256.New, b.secret)
	mac.Write([]byte(method + "\n" + b.name + "\n" + key + "\n" + strconv.FormatInt(expiry, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}
