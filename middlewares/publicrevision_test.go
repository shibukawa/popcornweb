//go:build !pwdev

package middlewares

import (
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/shibukawa/popcornweb/pwruntime"
)

// revisionFixture registers the two kinds of URL the segment distinguishes: one
// that kept the name its author wrote and carries a revision, and one the build
// invented whose name already carries its digest and carries none.
func revisionFixture(t *testing.T) fstest.MapFS {
	t.Helper()
	t.Cleanup(func() {
		publicManifestState.Store(nil)
	})
	RegisterPublicManifest([]AssetEntry{
		{URL: "app.css", CacheControl: "public, no-cache", Revision: "0123456789abcdef",
			Representations: []AssetRepresentation{
				{Path: "app.css", MediaType: "text/css; charset=utf-8", Length: 4, ETag: `"css"`},
			}},
		{URL: "generated/app.css", CacheControl: "public, no-cache", Revision: "fedcba9876543210",
			Representations: []AssetRepresentation{
				{Path: "generated/app.css", MediaType: "text/css; charset=utf-8", Length: 4, ETag: `"gen"`},
			}},
		{URL: "page.7c62e0b1d938.js", CacheControl: RevisionedCacheControl,
			Representations: []AssetRepresentation{
				{Path: "page.7c62e0b1d938.js", MediaType: "text/javascript; charset=utf-8", Length: 4, ETag: `"js"`},
			}},
	})
	return fstest.MapFS{
		"app.css":              {Data: []byte("body")},
		"generated/app.css":    {Data: []byte("gene")},
		"page.7c62e0b1d938.js": {Data: []byte("code")},
	}
}

// TestRevisionedURLIsImmutableAndPlainURLRevalidates is the whole point of the
// segment: one entry, two URLs, two promises. Serving the plain URL immutably
// would let a browser hold a stylesheet forever that the next build replaces,
// and serving the revisioned one with no-cache would spend a round trip on a
// URL that cannot change.
func TestRevisionedURLIsImmutableAndPlainURLRevalidates(t *testing.T) {
	for _, testCase := range []struct{ name, target, cacheControl string }{
		{"plain", "/public/app.css", "public, no-cache"},
		{"revisioned", "/public/0123456789abcdef/app.css", RevisionedCacheControl},
		{"nested plain", "/public/generated/app.css", "public, no-cache"},
		{"nested revisioned", "/public/fedcba9876543210/generated/app.css", RevisionedCacheControl},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tree := revisionFixture(t)
			response := manifestRequest(t, tree, testCase.target, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			if got := response.Header().Get("Cache-Control"); got != testCase.cacheControl {
				t.Errorf("Cache-Control = %q, want %q", got, testCase.cacheControl)
			}
			if got := response.Header().Get("ETag"); got == "" {
				t.Error("a revisioned answer still carries its validator")
			}
		})
	}
}

// TestStaleRevisionIsNotFound is what makes the immutable promise sound. A
// browser holding a URL forever has to be holding one that either serves those
// exact bytes or serves nothing; answering it from the current tree would hand
// back content under a name that promised different content.
func TestStaleRevisionIsNotFound(t *testing.T) {
	for _, target := range []string{
		"/public/ffffffffffffffff/app.css",
		"/public/fedcba9876543210/app.css", // another entry's revision
		"/public/0123456789abcdef/missing.css",
		"/public/fffffffffffffff/app.css",
		"/public/fffffffffffffffff/app.css",
		"/public/0123456789ABCDEF/app.css",
		"/public/0123456789abcdeg/app.css",
	} {
		tree := revisionFixture(t)
		if response := manifestRequest(t, tree, target, nil); response.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", target, response.Code)
		}
	}
}

// TestPublicAssetURLNamesWhatTheBuildDeclared covers the render half. A name the
// build gave a revision is returned under it; everything else is returned plain,
// which resolves and revalidates rather than promising anything.
func TestPublicAssetURLNamesWhatTheBuildDeclared(t *testing.T) {
	revisionFixture(t)
	for _, testCase := range []struct{ name, argument, want string }{
		{"tree path", "app.css", "/public/0123456789abcdef/app.css"},
		{"nested tree path", "generated/app.css", "/public/fedcba9876543210/generated/app.css"},
		// The literal a migration has in hand. Refusing it would turn a
		// mechanical edit into a 404 nobody sees until a browser loads the page.
		{"whole URL", "/public/app.css", "/public/0123456789abcdef/app.css"},
		{"leading slash", "/app.css", "/public/0123456789abcdef/app.css"},
		// Already carries its own digest, so a segment would say it twice.
		{"invented name", "page.7c62e0b1d938.js", "/public/page.7c62e0b1d938.js"},
		// Nothing declared it. The plain URL 404s where a reader can see it.
		{"unknown", "missing.css", "/public/missing.css"},
		{"traversal", "../secret", "/public/../secret"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := PublicAssetURL(testCase.argument); got != testCase.want {
				t.Errorf("PublicAssetURL(%q) = %q, want %q", testCase.argument, got, testCase.want)
			}
		})
	}
}

// TestPublicAssetURLFollowsTheConfiguredMount keeps the URL a document names and
// the path the middleware matches one decision. A mount read from configuration
// in one place and assumed in the other is a page whose stylesheet 404s only in
// the deployment that moved it.
func TestPublicAssetURLFollowsTheConfiguredMount(t *testing.T) {
	revisionFixture(t)
	t.Cleanup(func() { pwruntime.PublishChainSettings(pwruntime.ChainSettings{}) })
	pwruntime.PublishChainSettings(pwruntime.ChainSettings{
		Public: pwruntime.PublicAssetSettings{Enabled: true, Mount: "/static"},
	})
	if got := PublicAssetURL("app.css"); got != "/static/0123456789abcdef/app.css" {
		t.Errorf("PublicAssetURL = %q", got)
	}
	// The whole-URL form is read against the mount that is configured now, so a
	// template carrying yesterday's literal is not silently doubled up.
	if got := PublicAssetURL("/static/app.css"); got != "/static/0123456789abcdef/app.css" {
		t.Errorf("PublicAssetURL = %q", got)
	}
}
