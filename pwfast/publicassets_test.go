package pwfast

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// skipWhenTheTreeIsNotRead leaves out the tests that hand the middleware an
// embedded filesystem and expect it back.
//
// The development mode reads dist/public from disk and never consults the
// embedded tree, so those tests describe the built binary rather than the loop.
// It is the skip the shared package already uses for the same tests; this half
// simply never had it, so a pwdev run failed here and nowhere else.
func skipWhenTheTreeIsNotRead(t *testing.T) {
	t.Helper()
	if middlewares.PublicDevelopment() {
		t.Skip("development mode intentionally ignores embedded assets")
	}
}

func assetHandler(t *testing.T, tree fstest.MapFS) fasthttp.RequestHandler {
	t.Helper()
	assets, err := PublicAssets(PublicAssetConfig{Enabled: true, Mount: "/static/"}, tree)
	if err != nil {
		t.Fatal(err)
	}
	return Compose(func(r *fasthttp.RequestCtx) { _, _ = r.WriteString("application") },
		Frame{Slot: SlotPublicAssets, Middleware: assets})
}

func TestAnEmbeddedAssetIsServedWithItsValidator(t *testing.T) {
	skipWhenTheTreeIsNotRead(t)
	handler := assetHandler(t, fstest.MapFS{"site.css": {Data: []byte("body{}")}})

	status, header, body := serve(t, handler, "/static/site.css")
	if status != fasthttp.StatusOK || body != "body{}" {
		t.Fatalf("answered %d %q", status, body)
	}
	// Read case-insensitively: this transport canonicalises the name as Etag
	// and the other as ETag, and a client does not care which.
	etag := ""
	for _, line := range strings.Split(header, "\r\n") {
		if name, value, found := strings.Cut(line, ": "); found && strings.EqualFold(name, "etag") {
			etag = value
		}
	}
	if etag == "" {
		t.Fatalf("no validator was sent:\n%s", header)
	}
	// The validator has to be worth sending, which means a conditional request
	// carrying it gets 304 rather than the body again.
	if status, _, body := serveRaw(t, handler, "/static/site.css", "If-None-Match: "+etag+"\r\n"); status != fasthttp.StatusNotModified || body != "" {
		t.Errorf("a conditional request answered %d %q", status, body)
	}
}

// The path check is the shared one, and this is what it exists for.
func TestAnAssetPathThatCouldEscapeTheTreeIsRefused(t *testing.T) {
	handler := assetHandler(t, fstest.MapFS{
		"site.css":        {Data: []byte("body{}")},
		"secret/keys.txt": {Data: []byte("do not serve")},
	})
	// A dot-segment path never reaches the check on this transport: fasthttp
	// normalises the request URI before a handler sees it, so "/static/../x"
	// arrives as "/x" and misses the mount entirely. The outcome is the same
	// either way — the secret is not served — but the response differs, and
	// that difference belongs in a test rather than in a surprise.
	if _, _, body := serve(t, handler, "/static/../secret/keys.txt"); body == "do not serve" {
		t.Error("a traversal reached the asset tree")
	}
	// What the check does refuse is what survives normalisation: a dotfile, and
	// a request naming a precompressed sidecar directly.
	for _, target := range []string{"/static/.hidden", "/static/site.css.br"} {
		if status, _, body := serve(t, handler, target); status == fasthttp.StatusOK {
			t.Errorf("%s was served: %d %q", target, status, body)
		}
	}
}

func TestAPathOutsideTheMountReachesTheApplication(t *testing.T) {
	handler := assetHandler(t, fstest.MapFS{"site.css": {Data: []byte("body{}")}})
	if _, _, body := serve(t, handler, "/orders"); body != "application" {
		t.Errorf("an application path was taken by the asset frame: %q", body)
	}
}

func TestTheMountItselfRedirectsToItsDirectoryForm(t *testing.T) {
	handler := assetHandler(t, fstest.MapFS{"site.css": {Data: []byte("body{}")}})
	status, header, _ := serve(t, handler, "/static")
	if status != fasthttp.StatusPermanentRedirect {
		t.Errorf("status = %d, want 308", status)
	}
	if !strings.Contains(header, "Location: /static/") {
		t.Errorf("no redirect to the directory form:\n%s", header)
	}
}

// An asset endpoint that accepts any method is one an arbitrary caller can POST
// to, and the answer says nothing.
func TestAnAssetRefusesAMethodItDoesNotAnswer(t *testing.T) {
	handler := assetHandler(t, fstest.MapFS{"site.css": {Data: []byte("body{}")}})
	status, header, _ := serveForm(t, handler, "/static/site.css", "x=1")
	if status != fasthttp.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", status)
	}
	if !strings.Contains(header, "Allow:") {
		t.Errorf("405 carried no Allow header:\n%s", header)
	}
}

func TestAHeadRequestSendsTheHeadersWithoutTheBody(t *testing.T) {
	skipWhenTheTreeIsNotRead(t)
	handler := assetHandler(t, fstest.MapFS{"site.css": {Data: []byte("body{}")}})
	status, header, body := serveRequest(t, handler, "HEAD", "/static/site.css", "", "")
	if status != fasthttp.StatusOK {
		t.Errorf("status = %d", status)
	}
	if body != "" {
		t.Errorf("a HEAD carried a body: %q", body)
	}
	if !strings.Contains(header, "Content-Length: 6") {
		t.Errorf("a HEAD did not report the length:\n%s", header)
	}
}

// The tree an application embeds reaches this runtime the way it reaches the
// other one: through the registration its generated public.go makes at init.
//
// It used not to. The frame was installed only for a tree handed over as a
// runtime option, which neither a scaffolded entry point nor an example passes,
// so this build of an application answered 404 for every stylesheet and every
// script it had just told the browser to load.
func TestTheRegisteredPublicTreeIsServedWithoutBeingHandedOver(t *testing.T) {
	skipWhenTheTreeIsNotRead(t)
	previous := middlewares.SwapPublicFS(fstest.MapFS{"site.css": {Data: []byte("body{}")}})
	t.Cleanup(func() { middlewares.SwapPublicFS(previous) })
	publishChainSettings(t, pwruntime.ChainSettings{
		Public: pwruntime.PublicAssetSettings{Enabled: true, Mount: "/public/"},
	})
	handler, err := Middlewares(func(r *fasthttp.RequestCtx) {
		r.SetStatusCode(fasthttp.StatusNotFound)
	}, RuntimeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, body := serve(t, handler, "/public/site.css"); status != fasthttp.StatusOK || body != "body{}" {
		t.Fatalf("the registered tree answered %d %q", status, body)
	}

	// A tree handed over still wins, which is what the option is for.
	handed, err := Middlewares(func(r *fasthttp.RequestCtx) {
		r.SetStatusCode(fasthttp.StatusNotFound)
	}, RuntimeOptions{PublicFS: fstest.MapFS{"site.css": {Data: []byte("p{}")}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, body := serve(t, handed, "/public/site.css"); body != "p{}" {
		t.Errorf("a tree handed over was not the one served: %q", body)
	}
}
