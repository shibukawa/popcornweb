//go:build pwdev

package pwfast

import (
	"testing"
	"testing/fstest"

	"github.com/shibukawa/tinygodriver/fasthttp"
)

func TestDevelopmentPublicAssetsFallbackWithoutLocalTree(t *testing.T) {
	t.Chdir(t.TempDir())
	middleware, err := PublicAssets(PublicAssetConfig{Mount: "/public", LocalRoot: "cmd/app/dist/public"}, fstest.MapFS{"app.css": {Data: []byte("body{}")}})
	if err != nil {
		t.Fatal(err)
	}
	status, _, body := serve(t, middleware(func(r *fasthttp.RequestCtx) { r.SetStatusCode(404) }), "/public/app.css")
	if status != 200 || body != "body{}" {
		t.Fatalf("fallback = %d %q", status, body)
	}
}
