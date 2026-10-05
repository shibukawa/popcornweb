//go:build pwdev

package middlewares

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestDevelopmentPublicAssetsUseOnlyLocalIdentity(t *testing.T) {
	root := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.MkdirAll(filepath.FromSlash(localPublicRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.FromSlash(localPublicRoot), "app.css"), []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.FromSlash(localPublicRoot), "app.css.zstd"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	middleware, err := PublicAssets(PublicAssetConfig{Mount: "/public"},
		fstest.MapFS{"embedded.txt": {Data: []byte("embedded")}})
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.NotFoundHandler())

	request := httptest.NewRequest(http.MethodGet, "/public/app.css", nil)
	request.Header.Set("Accept-Encoding", "zstd")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "live" ||
		response.Header().Get("Content-Encoding") != "" || response.Header().Get("Vary") != "" {
		t.Fatalf("response = %d %q %#v", response.Code, response.Body.String(), response.Header())
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/public/embedded.txt", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("embedded fallback status = %d", missing.Code)
	}
}

// The URL a document names has to be one this build serves. The generated
// manifest is linked into a development binary too, and the loop answers from
// the working tree without consulting it, so a revisioned URL there is a
// stylesheet link that 404s on every page.
func TestDevelopmentPublicAssetURLIsOneTheLoopServes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.FromSlash(localPublicRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.FromSlash(localPublicRoot), "app.css"), []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { publicManifestState.Store(nil) })
	RegisterPublicManifest([]AssetEntry{{
		URL: "app.css", CacheControl: "public, no-cache", Revision: "0123456789abcdef",
		Representations: []AssetRepresentation{
			{Path: "app.css", MediaType: "text/css; charset=utf-8", Length: 4, ETag: `"css"`},
		},
	}})
	middleware, err := PublicAssets(PublicAssetConfig{Mount: "/public"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	named := PublicAssetURL("app.css")
	if named != "/public/app.css" {
		t.Fatalf("PublicAssetURL = %q, want the plain URL the loop serves", named)
	}
	response := httptest.NewRecorder()
	middleware(http.NotFoundHandler()).ServeHTTP(response, httptest.NewRequest(http.MethodGet, named, nil))
	if response.Code != http.StatusOK || response.Body.String() != "live" {
		t.Fatalf("the named URL answered %d %q", response.Code, response.Body.String())
	}
}
