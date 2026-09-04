package storage_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/storage"
	"github.com/shibukawa/popcornweb/storage/local"
)

// A URL the local backend presigns is served by the handler: a PUT stores
// the body under the signed key, a GET streams it back with its media type,
// and a signature for another method, another key or a past expiry is 403.
func TestLocalPresignedURLsRoundTripThroughTheSignedHandler(t *testing.T) {
	bucket, err := local.New("uploads", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := local.New("uploads", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := storage.SignedHandlerFor(func(_ context.Context, name string) (storage.Bucket, error) {
		if name == "uploads" {
			return bucket, nil
		}
		return nil, errors.New("no bucket")
	})
	ctx := context.Background()
	put, err := bucket.Presign(ctx, "u1/photo.png", storage.PresignOptions{Method: http.MethodPut, Expires: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(put.Path, storage.SignedPathPrefix+"uploads/u1/photo.png") || put.Query().Get("sig") == "" {
		t.Fatalf("presigned URL %s", put)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, put.String(), strings.NewReader("png bytes"))
	request.Header.Set("Content-Type", "image/png")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("PUT answered %d %s", recorder.Code, recorder.Body.String())
	}
	get, err := bucket.Presign(ctx, "u1/photo.png", storage.PresignOptions{})
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, get.String(), nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "png bytes" || recorder.Header().Get("Content-Type") != "image/png" || recorder.Header().Get("ETag") == "" {
		t.Errorf("GET answered %d %q %v", recorder.Code, recorder.Body.String(), recorder.Header())
	}
	// The GET signature does not authorize a PUT.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, get.String(), strings.NewReader("x")))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("a GET signature authorized a PUT: %d", recorder.Code)
	}
	// Another process's key does not verify.
	foreign, _ := other.Presign(ctx, "u1/photo.png", storage.PresignOptions{})
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, foreign.String(), nil))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("a foreign signature was accepted: %d", recorder.Code)
	}
	// An expired URL does not verify.
	expired := *get
	query := expired.Query()
	query.Set("exp", "1")
	expired.RawQuery = query.Encode()
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, expired.String(), nil))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("an expired URL was accepted: %d", recorder.Code)
	}
	// A bucket the opener does not know is 404.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, storage.SignedPathPrefix+"nope/a?exp=1&sig=x", nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("an unknown bucket answered %d", recorder.Code)
	}
}
