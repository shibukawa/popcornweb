package middlewares

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"
)

type fakeExternalSource struct {
	objects map[string]ExternalAsset
	fail    bool
}

func (source fakeExternalSource) OpenExternalAsset(_ context.Context, name string) (ExternalAsset, bool, error) {
	if source.fail {
		return ExternalAsset{}, false, errors.New("bucket unavailable")
	}
	asset, ok := source.objects[name]
	return asset, ok, nil
}

// A registered source answers the external tree in place of the directory,
// with the validators it knows and Range support from the whole body; a
// name it lacks falls through to the embedded tree, and its failure is 500.
func TestExternalAssetSourceServesTheExternalTree(t *testing.T) {
	RegisterExternalAssetSource(fakeExternalSource{objects: map[string]ExternalAsset{
		"video/intro.mp4": {Body: []byte("0123456789"), ETag: `"r2-etag"`, MediaType: "video/mp4", ModTime: time.Unix(1700000000, 0)},
	}})
	t.Cleanup(func() { RegisterExternalAssetSource(nil) })
	embedded := fstest.MapFS{"app.css": &fstest.MapFile{Data: []byte("body{}")}}
	middleware, err := PublicAssets(PublicAssetConfig{Enabled: true, Mount: "/public"}, embedded)
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/public/video/intro.mp4", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "0123456789" || recorder.Header().Get("ETag") != `"r2-etag"` || recorder.Header().Get("Content-Type") != "video/mp4" {
		t.Errorf("source not served: %d %q %v", recorder.Code, recorder.Body.String(), recorder.Header())
	}
	ranged := httptest.NewRequest(http.MethodGet, "/public/video/intro.mp4", nil)
	ranged.Header.Set("Range", "bytes=2-4")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, ranged)
	if recorder.Code != http.StatusPartialContent || recorder.Body.String() != "234" {
		t.Errorf("range not served from the source body: %d %q", recorder.Code, recorder.Body.String())
	}
	conditional := httptest.NewRequest(http.MethodGet, "/public/video/intro.mp4", nil)
	conditional.Header.Set("If-None-Match", `"r2-etag"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, conditional)
	if recorder.Code != http.StatusNotModified {
		t.Errorf("conditional request answered %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/public/app.css", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "body{}" {
		t.Errorf("embedded tree lost behind the source: %d %q", recorder.Code, recorder.Body.String())
	}

	RegisterExternalAssetSource(fakeExternalSource{fail: true})
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/public/video/intro.mp4", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("a failing store answered %d", recorder.Code)
	}
}
