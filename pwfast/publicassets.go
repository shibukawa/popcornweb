package pwfast

import (
	"io"
	"io/fs"
	"strconv"
	"strings"
	"sync"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// PublicAssetConfig is the shared configuration, so one setting serves both
// builds from one tree.
type PublicAssetConfig = middlewares.PublicAssetConfig

// PublicAssets answers static files from the embedded tree, above everything
// that authenticates.
//
// Every decision is the shared one: which names may be served, which
// representation an Accept-Encoding value selects, what a manifest entry says.
// Only reading the headers and writing the bytes is here, which is what a
// second copy of this may safely be — the path check in particular is not, and
// is not copied: two implementations of which names may be served are two
// chances to serve one that must not be.
func PublicAssets(config PublicAssetConfig, embedded fs.FS) (Middleware, error) {
	config, embedded, err := middlewares.PreparePublicAssets(config, embedded)
	if err != nil {
		return nil, err
	}
	mount, err := middlewares.NormalizePublicMount(config.Mount)
	if err != nil {
		return nil, err
	}
	// The embedded tree is immutable, so a name resolves to the same bytes,
	// media type and validator on every request. Only names that resolve are
	// cached, which bounds the cache by the tree rather than by what clients
	// ask for.
	memoize := !middlewares.PublicDevelopment() && !config.ReadLocal
	var resolved sync.Map

	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(r *fasthttp.RequestCtx) {
			path := string(r.Path())
			if path == strings.TrimSuffix(mount, "/") {
				location := mount
				if query := string(r.URI().QueryString()); query != "" {
					location += "?" + query
				}
				Redirect(r, location, fasthttp.StatusPermanentRedirect)
				return
			}
			if !strings.HasPrefix(path, mount) {
				next(r)
				return
			}
			method := string(r.Method())
			if method != fasthttp.MethodGet && method != fasthttp.MethodHead {
				// Not r.Error: it resets the response, and the Allow header is
				// the only useful thing a 405 carries.
				plainStatus(r, fasthttp.StatusMethodNotAllowed)
				r.Response.Header.Set("Allow", "GET, HEAD")
				return
			}
			name, ok := middlewares.PublicAssetName(strings.TrimPrefix(path, mount))
			if !ok {
				notFound(r)
				return
			}
			// A build that produced a manifest already knows every URL, every
			// representation and every validator, so the request path reads
			// bytes and nothing else.
			if middlewares.PublicManifestRegistered() {
				serveManifestAsset(r, name, embedded)
				return
			}
			serveResolvedAsset(r, name, config, embedded, memoize, &resolved)
		}
	}, nil
}

// serveManifestAsset answers from what the build computed.
func serveManifestAsset(r *fasthttp.RequestCtx, name string, embedded fs.FS) {
	entry, cacheControl, found := middlewares.PublicManifestAnswer(name)
	if !found {
		notFound(r)
		return
	}
	for _, axis := range strings.Split(middlewares.PublicVary(entry), ",") {
		if axis = strings.TrimSpace(axis); axis != "" {
			addVaryHeader(r, axis)
		}
	}
	representation, acceptable := middlewares.PublicManifestRepresentation(entry,
		headerValues(r, "Accept"), headerValues(r, "Accept-Encoding"))
	if !acceptable {
		plainStatus(r, fasthttp.StatusNotAcceptable)
		return
	}
	r.Response.Header.SetContentType(representation.MediaType)
	r.Response.Header.Set("Cache-Control", cacheControl)
	r.Response.Header.Set("ETag", representation.ETag)
	if representation.ContentEncoding != "" {
		r.Response.Header.Set("Content-Encoding", representation.ContentEncoding)
	}
	if string(r.Request.Header.Peek("If-None-Match")) == representation.ETag {
		r.SetStatusCode(fasthttp.StatusNotModified)
		return
	}
	body, err := embedded.Open(representation.Path)
	if err != nil {
		// The manifest and the tree ship together, so a missing file means the
		// two came from different builds and no response would be honest.
		plainStatus(r, fasthttp.StatusInternalServerError)
		return
	}
	defer func() { _ = body.Close() }()
	r.Response.Header.SetContentLength(representation.Length)
	r.SetStatusCode(fasthttp.StatusOK)
	if string(r.Method()) == fasthttp.MethodGet {
		_, _ = io.Copy(r, body)
	}
}

// serveResolvedAsset answers a build with no manifest, resolving the asset and
// keeping it where the tree cannot change under it.
func serveResolvedAsset(r *fasthttp.RequestCtx, name string, config PublicAssetConfig,
	embedded fs.FS, memoize bool, cache *sync.Map) {
	var asset *middlewares.ResolvedPublicAsset
	if memoize {
		if cached, ok := cache.Load(name); ok {
			asset = cached.(*middlewares.ResolvedPublicAsset)
		}
	}
	if asset == nil {
		found, ok := middlewares.ResolvePublicAsset(name, config, embedded)
		if !ok {
			notFound(r)
			return
		}
		asset = middlewares.FinishPublicAsset(found)
		if memoize {
			cache.Store(name, asset)
		}
	}
	// A file whose bytes contradict its extension is refused, the same guard the
	// net/http path applies: the build already declined it, and serving it under
	// a mislabelled type is a content-type-confusion the two transports must not
	// disagree about.
	if mismatch := asset.Mismatch(); mismatch != "" {
		middlewares.ReportPublicMismatch(r, name, mismatch)
		plainStatus(r, fasthttp.StatusInternalServerError)
		return
	}
	if !middlewares.PublicDevelopment() {
		addVaryHeader(r, "Accept-Encoding")
	}
	representation, rank, acceptable := middlewares.PublicRepresentation(
		headerValues(r, "Accept-Encoding"), asset.Asset())
	if !acceptable {
		plainStatus(r, fasthttp.StatusNotAcceptable)
		return
	}
	r.Response.Header.SetContentType(asset.ContentType())
	r.Response.Header.Set("Content-Length", strconv.Itoa(len(representation)))
	etag := asset.ETag(rank)
	if rank >= 0 {
		r.Response.Header.Set("Content-Encoding", middlewares.PublicCodingToken(rank))
	}
	r.Response.Header.Set("ETag", etag)
	if string(r.Request.Header.Peek("If-None-Match")) == etag {
		r.SetStatusCode(fasthttp.StatusNotModified)
		return
	}
	r.SetStatusCode(fasthttp.StatusOK)
	if string(r.Method()) == fasthttp.MethodGet {
		_, _ = r.Write(representation)
	}
}

// headerValues collects every value of a repeating request header, because
// Accept and Accept-Encoding both legitimately arrive more than once and
// reading only the first would negotiate against half of what the client said.
//
// PeekAll rather than VisitAll: the walk allocated a string per header on the
// request just to compare names, twice per asset request, and static assets
// are the highest-volume route most deployments have.
func headerValues(r *fasthttp.RequestCtx, name string) []string {
	matched := r.Request.Header.PeekAll(name)
	if len(matched) == 0 {
		return nil
	}
	values := make([]string, len(matched))
	for index, value := range matched {
		values[index] = string(value)
	}
	return values
}

func notFound(r *fasthttp.RequestCtx) { plainStatus(r, fasthttp.StatusNotFound) }

// plainStatus answers with a status and its message, leaving whatever headers
// the caller already set in place.
//
// fasthttp's own Error resets the response first, which is right for a handler
// abandoning its work and wrong here: a 405 whose Allow header was reset says
// the method is not allowed without saying which are.
func plainStatus(r *fasthttp.RequestCtx, status int) {
	r.SetStatusCode(status)
	r.Response.Header.SetContentType("text/plain; charset=utf-8")
	r.SetBodyString(fasthttp.StatusMessage(status) + "\n")
}
