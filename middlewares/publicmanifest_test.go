//go:build !pwdev

package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// manifestFixture registers one URL carrying an avif variant, a webp fallback,
// and a compressed sibling of the fallback, which is every axis at once.
func manifestFixture(t *testing.T) fstest.MapFS {
	t.Helper()
	t.Cleanup(func() {
		publicManifestState.Store(nil)
	})
	RegisterPublicManifest([]AssetEntry{
		{URL: "img/logo.webp", Representations: []AssetRepresentation{
			{Path: "img/logo.webp.avif", MediaType: "image/avif", Length: 4, ETag: `"avif"`, Preference: 0},
			{Path: "img/logo.webp", MediaType: "image/webp", Length: 4, ETag: `"webp"`, Preference: 1},
		}},
		{URL: "app.css", CacheControl: "public, no-cache", Representations: []AssetRepresentation{
			{Path: "app.css", MediaType: "text/css; charset=utf-8", Length: 4, ETag: `"css"`},
			{Path: "app.css.br", MediaType: "text/css; charset=utf-8", ContentEncoding: "br", Length: 2, ETag: `"cssb"`},
			{Path: "app.css.zstd", MediaType: "text/css; charset=utf-8", ContentEncoding: "zstd", Length: 3, ETag: `"cssz"`},
			{Path: "app.css.gz", MediaType: "text/css; charset=utf-8", ContentEncoding: "gzip", Length: 3, ETag: `"cssg"`},
		}},
	})
	return fstest.MapFS{
		"img/logo.webp.avif": {Data: []byte("avif")},
		"img/logo.webp":      {Data: []byte("webp")},
		"app.css":            {Data: []byte("body")},
		"app.css.br":         {Data: []byte("br")},
		"app.css.zstd":       {Data: []byte("bdy")},
		"app.css.gz":         {Data: []byte("gzp")},
		"unlisted.txt":       {Data: []byte("hidden")},
	}
}

// TestManifestNegotiatesEveryStoredCoding covers the manifest half of the same
// rule the handler path has: the build's order decides, and each representation
// answers with the validator of its own bytes so a cache holding one cannot
// answer a request for another.
func TestManifestNegotiatesEveryStoredCoding(t *testing.T) {
	for _, testCase := range []struct {
		name, acceptEncoding, body, encoding, etag string
	}{
		{name: "brotli leads", acceptEncoding: "gzip, zstd, br", body: "br", encoding: "br", etag: `"cssb"`},
		{name: "header order does not decide", acceptEncoding: "gzip;q=0.9, br;q=0.1", body: "br", encoding: "br", etag: `"cssb"`},
		{name: "zstd when brotli is refused", acceptEncoding: "br;q=0, gzip, zstd", body: "bdy", encoding: "zstd", etag: `"cssz"`},
		{name: "gzip only", acceptEncoding: "gzip, deflate", body: "gzp", encoding: "gzip", etag: `"cssg"`},
		{name: "identity when nothing is taken", acceptEncoding: "deflate", body: "body", encoding: "", etag: `"css"`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tree := manifestFixture(t)
			response := manifestRequest(t, tree, "/public/app.css", map[string]string{
				"Accept-Encoding": testCase.acceptEncoding,
			})
			if response.Body.String() != testCase.body {
				t.Errorf("body = %q, want %q", response.Body.String(), testCase.body)
			}
			if got := response.Header().Get("Content-Encoding"); got != testCase.encoding {
				t.Errorf("Content-Encoding = %q, want %q", got, testCase.encoding)
			}
			if got := response.Header().Get("ETag"); got != testCase.etag {
				t.Errorf("ETag = %q, want %q", got, testCase.etag)
			}
		})
	}
}

func manifestRequest(t *testing.T, tree fstest.MapFS, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	middleware, err := PublicAssets(PublicAssetConfig{Enabled: true, Mount: "/public"}, tree)
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("request fell through: %s", r.URL.Path)
	}))
	request := httptest.NewRequest(http.MethodGet, target, nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestManifestServesTheNegotiatedMediaType(t *testing.T) {
	tree := manifestFixture(t)
	response := manifestRequest(t, tree, "/public/img/logo.webp", map[string]string{
		"Accept": "image/avif,image/webp,*/*;q=0.8",
	})
	if response.Body.String() != "avif" {
		t.Errorf("body = %q", response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "image/avif" {
		t.Errorf("content type = %q", got)
	}
	if got := response.Header().Get("ETag"); got != `"avif"` {
		t.Errorf("etag = %q", got)
	}
	// A cache holding the avif must not hand it to a client that cannot read it.
	if got := response.Header().Get("Vary"); got != "Accept, Accept-Encoding" {
		t.Errorf("vary = %q", got)
	}
}

// TestManifestFallsBackToTheDefaultRepresentation covers the client that never
// heard of the preferred format: it gets the fallback the build guarantees.
func TestManifestFallsBackToTheDefaultRepresentation(t *testing.T) {
	tree := manifestFixture(t)
	response := manifestRequest(t, tree, "/public/img/logo.webp", map[string]string{
		"Accept": "image/webp,image/png",
	})
	if response.Body.String() != "webp" || response.Header().Get("Content-Type") != "image/webp" {
		t.Errorf("body = %q, type = %q", response.Body.String(), response.Header().Get("Content-Type"))
	}
	if response.Header().Get("ETag") != `"webp"` {
		t.Errorf("two representations shared one validator")
	}
}

// TestManifestDoesNotVaryOnAcceptForOneRepresentation keeps the variant space
// the manifest's rather than the header's: an asset with one media type stores
// one variant in any cache.
func TestManifestDoesNotVaryOnAcceptForOneRepresentation(t *testing.T) {
	tree := manifestFixture(t)
	response := manifestRequest(t, tree, "/public/app.css", nil)
	if got := response.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("vary = %q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "public, no-cache" {
		t.Errorf("cache-control = %q", got)
	}
}

func TestManifestAnswersNotModified(t *testing.T) {
	tree := manifestFixture(t)
	response := manifestRequest(t, tree, "/public/app.css", map[string]string{"If-None-Match": `"css"`})
	if response.Code != http.StatusNotModified || response.Body.Len() != 0 {
		t.Errorf("code = %d, body = %q", response.Code, response.Body.String())
	}
}

// TestManifestRefusesAnUndeclaredURL is the rule that serving is manifest-driven
// rather than filesystem-driven: bytes the build did not declare are not
// reachable, whatever the embedded tree happens to hold.
func TestManifestRefusesAnUndeclaredURL(t *testing.T) {
	tree := manifestFixture(t)
	if response := manifestRequest(t, tree, "/public/unlisted.txt", nil); response.Code != http.StatusNotFound {
		t.Errorf("code = %d", response.Code)
	}
}

// TestManifestPrefersTheFallbackOverRefusing covers a header that mentions
// neither format. Not being listed is not a refusal, and an image a page needs
// is worth more than a 406 nobody asked for.
func TestManifestPrefersTheFallbackOverRefusing(t *testing.T) {
	tree := manifestFixture(t)
	response := manifestRequest(t, tree, "/public/img/logo.webp", map[string]string{"Accept": "image/png"})
	if response.Code != http.StatusOK || response.Body.String() != "webp" {
		t.Errorf("code = %d, body = %q", response.Code, response.Body.String())
	}
}

// TestManifestRefusesEveryRepresentation is the other half: a client that said
// q=0 meant it, so there is nothing left to send.
func TestManifestRefusesEveryRepresentation(t *testing.T) {
	tree := manifestFixture(t)
	response := manifestRequest(t, tree, "/public/img/logo.webp", map[string]string{"Accept": "*/*;q=0"})
	if response.Code != http.StatusNotAcceptable {
		t.Errorf("code = %d", response.Code)
	}
}
