package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SignedPathPrefix is where the application serves the URLs the local
// backend presigns: SignedPathPrefix + bucket name + "/" + key, with the
// expiry and the signature in the query. A backend that serves its own
// presigned requests implements SelfServing, and the framework mounts
// SignedHandler ahead of the application when such a bucket is configured.
const SignedPathPrefix = "/_storage/"

// SelfServing is a backend whose presigned URLs point back at the
// application. VerifySignedRequest checks the signature and the expiry of a
// request under SignedPathPrefix and returns the key it authorizes and the
// method it was signed for.
type SelfServing interface {
	VerifySignedRequest(r *http.Request, key string) (method string, err error)
}

// ErrBadSignature reports a self-served request whose signature or expiry
// does not hold.
var ErrBadSignature = errors.New("storage: signed URL is invalid or expired")

// SignedHandler serves requests under SignedPathPrefix for every configured
// bucket whose backend is SelfServing. A GET streams the object with its
// media type and ETag, a PUT stores the body under the signed key, and a
// request whose signature does not hold is 403; a bucket that does not serve
// itself is 404 here, because its URLs never point here.
func SignedHandler() http.Handler { return signedHandler(Open) }

// SignedHandlerFor is SignedHandler over a resolver of the caller's own, for
// a test that builds its buckets outside the configuration.
func SignedHandlerFor(open func(context.Context, string) (Bucket, error)) http.Handler {
	return signedHandler(open)
}

func signedHandler(open func(context.Context, string) (Bucket, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, SignedPathPrefix)
		name, key, found := strings.Cut(rest, "/")
		if !found || name == "" || key == "" {
			http.NotFound(w, r)
			return
		}
		bucket, err := open(r.Context(), name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		server, ok := bucket.(SelfServing)
		if !ok {
			http.NotFound(w, r)
			return
		}
		method, err := server.VerifySignedRequest(r, key)
		if err != nil || method != r.Method {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			serveSignedGet(w, r, bucket, key)
		case http.MethodPut:
			serveSignedPut(w, r, bucket, key)
		case http.MethodDelete:
			if err := bucket.Delete(r.Context(), key); err != nil {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "GET, HEAD, PUT, DELETE")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}
	})
}

func serveSignedGet(w http.ResponseWriter, r *http.Request, bucket Bucket, key string) {
	object, err := bucket.Get(r.Context(), key)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer object.Body.Close()
	if object.ContentType != "" {
		w.Header().Set("Content-Type", object.ContentType)
	}
	if object.ETag != "" {
		w.Header().Set("ETag", object.ETag)
		if r.Header.Get("If-None-Match") == object.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if object.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, object.Body)
	}
}

func serveSignedPut(w http.ResponseWriter, r *http.Request, bucket Bucket, key string) {
	options := PutOptions{ContentType: r.Header.Get("Content-Type"), ContentLength: r.ContentLength}
	if options.ContentType == "" {
		options.ContentType = "application/octet-stream"
	}
	if err := bucket.Put(r.Context(), key, r.Body, options); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SelfServed reports whether the configured set holds a bucket that serves
// its own presigned URLs, which is when the framework mounts SignedHandler.
func SelfServed(ctx context.Context) bool {
	config, _ := registeredStorageConfig()
	if !config.Enabled {
		return false
	}
	for _, element := range config.Buckets {
		if element.Backend == "" || element.Backend == "local" {
			return true
		}
	}
	return false
}

// DefaultPresignExpiry is the life of a presigned URL when the caller set
// none, on the backends that serve their own.
const DefaultPresignExpiry = 15 * time.Minute
