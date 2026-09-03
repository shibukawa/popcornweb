package middlewares

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shibukawa/popcornweb/internal/assetverify"
	"github.com/shibukawa/popcornweb/pwruntime"
	"sync"
)

// localPublicRoot is the built tree, relative to the working directory. It is
// what the development loop and an explicit local override both read, because
// it is the only tree whose file names match the URLs the pages carry.
const localPublicRoot = "dist/public"

// PublicAssetConfig is the shared leaf's, aliased so there is one declaration
// and one set of binder tags.
type PublicAssetConfig = pwruntime.PublicAssetSettings

// NormalizePublicMount validates a mount point and returns it with a trailing
// slash. Its errors name the runtime configuration key they come from.
func NormalizePublicMount(value string) (string, error) {
	if value == "" || value[0] != '/' || value == "/" {
		return "", fmt.Errorf("server.public.mount must be an absolute non-root path")
	}
	if strings.ContainsAny(value, `\?#`) || hasControl(value) {
		return "", fmt.Errorf("server.public.mount is invalid")
	}
	trimmed := strings.TrimSuffix(value, "/")
	if path.Clean(trimmed) != trimmed || strings.ContainsAny(trimmed, "{}*") {
		return "", fmt.Errorf("server.public.mount must be canonical and contain no wildcards")
	}
	return trimmed + "/", nil
}

// PublicAssets serves the application's static assets under config.Mount and
// forwards every other request downstream. Assets come from the embedded
// filesystem, optionally overlaid by a local public directory, and are
// negotiated against the precompressed sidecars the build produced.
//
// A nil embedded filesystem falls back to the one a generated public.go
// registered with RegisterPublicFS. Outside the pwdev build mode, serving
// without either is a startup error.
func PublicAssets(config PublicAssetConfig, embedded fs.FS) (Middleware, error) {
	mount, err := NormalizePublicMount(config.Mount)
	if err != nil {
		return nil, err
	}
	if embedded == nil {
		embedded = registeredPublicFS()
	}
	if embedded == nil && !publicDevelopment {
		return nil, fmt.Errorf("popcornweb: server.public.enabled requires a registered public filesystem")
	}
	// The embedded tree is immutable, so a name resolves to the same bytes,
	// media type, and validator on every request. The local tree can change
	// between requests, and ReadLocal consults it before the embedded one, so
	// that mode keeps resolving fresh. Only names that resolve are cached,
	// which bounds the cache by the tree rather than by what clients request.
	memoize := !publicDevelopment && !config.ReadLocal
	var resolvedAssets sync.Map
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == strings.TrimSuffix(mount, "/") {
				location := mount
				if r.URL.RawQuery != "" {
					location += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, location, http.StatusPermanentRedirect)
				return
			}
			if !strings.HasPrefix(r.URL.Path, mount) {
				next.ServeHTTP(w, r)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
				return
			}
			name, ok := publicAssetName(strings.TrimPrefix(r.URL.Path, mount))
			if !ok {
				http.NotFound(w, r)
				return
			}
			// A build that produced a manifest already knows every URL, every
			// representation, and every validator, so the request path reads
			// bytes and nothing else.
			if !publicDevelopment && manifestRegistered() {
				servePublicManifest(w, r, name, embedded, config.SVGSandbox)
				return
			}
			// No manifest to ask, so the trees are consulted directly. The
			// external one goes first because it wins the URL, and it has to
			// win here for the same reason it wins there: an author shadowing
			// a file must not see one answer in the loop and the other after a
			// deploy.
			if source := registeredExternalSource(); source != nil {
				mediaType := mime.TypeByExtension(path.Ext(name))
				if mediaType != "" {
					w.Header().Set("Content-Type", mediaType)
				}
				addSVGSandbox(w.Header(), mediaType, config.SVGSandbox)
				if serveExternalFromSource(w, r, source, name) {
					return
				}
				w.Header().Del("Content-Type")
			} else if external, ok := externalAssetPath(name); ok {
				mediaType := mime.TypeByExtension(path.Ext(name))
				if mediaType != "" {
					w.Header().Set("Content-Type", mediaType)
				}
				addSVGSandbox(w.Header(), mediaType, config.SVGSandbox)
				serveExternalFile(w, r, name, external)
				return
			}
			var resolved *resolvedPublicAsset
			if memoize {
				if cached, ok := resolvedAssets.Load(name); ok {
					resolved = cached.(*resolvedPublicAsset)
				}
			}
			if resolved == nil {
				asset, ok := resolvePublicAsset(name, config, embedded)
				if !ok {
					http.NotFound(w, r)
					return
				}
				resolved = finishPublicAsset(asset)
				if memoize {
					resolvedAssets.Store(name, resolved)
				}
			}
			// This is the one path serving bytes no build declared: the
			// development loop, and the local override of a project whose
			// manifest is absent. A manifest-driven response verifies nothing
			// per request because the build already refused this file.
			if resolved.mismatch != "" {
				reportPublicMismatch(r.Context(), name, resolved.mismatch)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			if !publicDevelopment {
				appendVary(w.Header(), "Accept-Encoding")
			}
			representation, rank, acceptable := selectPublicRepresentation(r, resolved.asset)
			if !acceptable {
				http.Error(w, http.StatusText(http.StatusNotAcceptable), http.StatusNotAcceptable)
				return
			}
			w.Header().Set("Content-Type", resolved.contentType)
			addSVGSandbox(w.Header(), resolved.contentType, config.SVGSandbox)
			w.Header().Set("Content-Length", strconv.Itoa(len(representation)))
			etag := resolved.identityTag
			if rank >= 0 {
				w.Header().Set("Content-Encoding", staticContentCodings[rank].token)
				etag = resolved.encodedTags[rank]
			}
			w.Header().Set("ETag", etag)
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = w.Write(representation)
			}
		})
	}, nil
}

// servePublicManifest answers from data the build computed. Nothing here stats
// a file, digests a body, or infers a media type: a URL the manifest does not
// name is 404 even when bytes for it exist, because serving what a build did
// not declare is how a stale representation reaches a cache.
func servePublicManifest(w http.ResponseWriter, r *http.Request, name string, embedded fs.FS, svgSandbox bool) {
	entry, cacheControl, found := publicManifestAnswer(name)
	if !found {
		http.NotFound(w, r)
		return
	}
	header := w.Header()
	appendVary(header, strings.Split(varyForEntry(entry), ",")...)
	representation, acceptable := selectRepresentation(entry, r.Header.Values("Accept"), r.Header.Values("Accept-Encoding"))
	if !acceptable {
		http.Error(w, http.StatusText(http.StatusNotAcceptable), http.StatusNotAcceptable)
		return
	}
	header.Set("Content-Type", representation.MediaType)
	addSVGSandbox(header, representation.MediaType, svgSandbox)
	header.Set("Cache-Control", cacheControl)
	if representation.External {
		serveExternalRepresentation(w, r, representation)
		return
	}
	header.Set("ETag", representation.ETag)
	if representation.ContentEncoding != "" {
		header.Set("Content-Encoding", representation.ContentEncoding)
	}
	if r.Header.Get("If-None-Match") == representation.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body, err := embedded.Open(representation.Path)
	if err != nil {
		// The manifest and the tree ship together, so a missing file means the
		// two came from different builds and no response would be honest.
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer body.Close()
	header.Set("Content-Length", strconv.Itoa(representation.Length))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, body)
	}
}

// externalPublicRoot is the second authored tree, relative to the working
// directory, in the same way localPublicRoot is. It holds what should not enter
// the binary: the bytes ship as their own files and the container scaffold
// copies the directory as it stands, so nothing here is build output.
const externalPublicRoot = "public-external"

// serveExternalRepresentation answers from a real file.
//
// http.ServeContent is the whole reason the external location is a separate
// path rather than a flag on the one above: given something that can seek, it
// supplies Range, If-Range, Last-Modified, and Accept-Ranges without a line of
// protocol code here. That matters because the kind of asset that belongs in
// this tree is exactly the kind a browser asks for in pieces — a video element
// that cannot seek is one that downloads the file to play from the middle.
//
// Every header the manifest owns is already set by the caller. ServeContent
// adds the ones that come from the file itself and writes the status, so
// nothing may be written before it.
func serveExternalRepresentation(w http.ResponseWriter, r *http.Request, representation AssetRepresentation) {
	if source := registeredExternalSource(); source != nil {
		if serveExternalFromSource(w, r, source, representation.Path) {
			return
		}
		// The manifest named an object the store does not hold: the same
		// disagreement as a missing file, answered the same way.
		reportMissingExternal(r.Context(), representation.Path)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	resolved, _, found, _ := safeLocalPath(filepath.FromSlash(externalPublicRoot), representation.Path)
	if !found {
		// The manifest named a file the deployment did not carry, which means
		// the binary and the tree beside it came from different places. No
		// response would be honest, so this is the same 500 the embedded path
		// gives when its own tree disagrees with its manifest.
		reportMissingExternal(r.Context(), representation.Path)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	serveExternalFile(w, r, representation.Path, resolved)
}

// serveExternalFile streams one file, having left every header the caller owns
// already set. ServeContent writes the status, so nothing may be written after
// this decides to call it.
func serveExternalFile(w http.ResponseWriter, r *http.Request, name, resolved string) {
	file, err := os.Open(resolved)
	if err != nil {
		reportMissingExternal(r.Context(), name)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		reportMissingExternal(r.Context(), name)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	// The name reaches ServeContent only as a media type hint, which it will
	// not use: Content-Type is already set and ServeContent leaves a header
	// that is present alone.
	http.ServeContent(w, r, name, info.ModTime(), file)
}

// externalAssetPath resolves a URL in the second authored tree, for the path
// that has no manifest to ask.
func externalAssetPath(name string) (string, bool) {
	resolved, _, found, _ := safeLocalPath(filepath.FromSlash(externalPublicRoot), name)
	return resolved, found
}

// reportedMissingExternal keeps one absent file to one log line, for the reason
// reportedMismatches does: a crawler asking repeatedly is one problem.
var reportedMissingExternal sync.Map

func reportMissingExternal(ctx context.Context, name string) {
	if _, seen := reportedMissingExternal.LoadOrStore(name, struct{}{}); seen {
		return
	}
	pwruntime.ReadLogger(ctx).Error("external public asset named by the manifest is not readable",
		pwruntime.String("asset", name), pwruntime.String("root", externalPublicRoot))
}

// appendVary folds each token into one Vary header without discarding what an
// outer layer already set. The CORS frame adds Vary: Origin, and a plain Set
// here would clobber it — a shared cache would then key a per-origin response
// without Origin and serve it to another origin. Existing tokens are kept, each
// token appears once, and a Vary of * subsumes everything so it stands alone.
func appendVary(header http.Header, tokens ...string) {
	seen := make(map[string]bool)
	var combined []string
	add := func(token string) bool {
		token = strings.TrimSpace(token)
		if token == "" {
			return false
		}
		if token == "*" {
			header.Set("Vary", "*")
			return true
		}
		if key := strings.ToLower(token); !seen[key] {
			seen[key] = true
			combined = append(combined, token)
		}
		return false
	}
	for _, line := range header.Values("Vary") {
		for _, existing := range strings.Split(line, ",") {
			if add(existing) {
				return
			}
		}
	}
	for _, token := range tokens {
		if add(token) {
			return
		}
	}
	if len(combined) > 0 {
		header.Set("Vary", strings.Join(combined, ", "))
	}
}

// varyForEntry names only the headers that can actually change the answer, so a
// cache stores one variant for an asset that negotiates nothing on Accept.
func varyForEntry(entry AssetEntry) string {
	if entryNegotiatesMedia(entry) {
		return "Accept, Accept-Encoding"
	}
	return "Accept-Encoding"
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func publicAssetName(name string) (string, bool) {
	if name == "" || strings.ContainsAny(name, "\\\x00") || !fs.ValidPath(name) || path.Clean(name) != name {
		return "", false
	}
	// A hand-rolled walk rather than strings.Split, because this runs on every
	// static asset request and the split slice was its only allocation.
	for rest := name; ; {
		segment, tail, found := strings.Cut(rest, "/")
		if segment == "" || strings.HasPrefix(segment, ".") {
			return "", false
		}
		if !found {
			break
		}
		rest = tail
	}
	if hasStaticCodingSuffix(name) {
		return "", false
	}
	return name, true
}

type publicAsset struct {
	name     string
	identity []byte
	// encoded holds one precompressed sidecar per staticContentCodings rank,
	// nil where the build produced none. A missing coding is ordinary: an
	// encode that came out no smaller than its source is skipped.
	encoded [maxStaticCodings][]byte
}

// resolvedPublicAsset carries one manifest-less asset with everything a
// response derives from its bytes already computed, so a cached asset costs
// no digest and no type sniff while a request waits.
type resolvedPublicAsset struct {
	asset       publicAsset
	contentType string
	identityTag string
	// mismatch holds the reason this file is not servable, empty when it is.
	// It is decided once beside the media type rather than per request, so a
	// memoized asset costs no second look at its bytes.
	mismatch string
	// encodedTags parallels publicAsset.encoded. Two representations of one URL
	// never share a validator, because a cache holding one must not answer a
	// request that asked for another.
	encodedTags [maxStaticCodings]string
}

func finishPublicAsset(asset publicAsset) *resolvedPublicAsset {
	contentType := mime.TypeByExtension(path.Ext(asset.name))
	if contentType == "" {
		contentType = http.DetectContentType(asset.identity)
	}
	resolved := &resolvedPublicAsset{asset: asset, contentType: contentType, identityTag: publicAssetETag(asset.identity)}
	// Only the signature half runs here. The active-content scan is a build
	// report to an author, and the sandbox header already covers a serving
	// process, so re-scanning every SVG per resolution would buy nothing.
	if finding, refused := assetverify.File(asset.name, asset.identity, signatureOnly); refused {
		resolved.mismatch = finding.Message()
	}
	for rank, body := range asset.encoded {
		if len(body) > 0 {
			resolved.encodedTags[rank] = publicAssetETag(body)
		}
	}
	return resolved
}

func publicAssetETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// signatureOnly is the request-time half of the verification. A serving process
// has no project configuration to read and no author to advise, so it runs the
// check that is decidable from bytes and leaves the rest to the build.
var signatureOnly = assetverify.Options{Signature: true}

// svgSandboxPolicy is the policy an SVG response carries. The sandbox with no
// tokens is a unique origin and no scripting, so an SVG that does execute
// cannot reach the application origin; script-src none would stop the script
// but leave the origin, and the origin is the part worth taking away.
const svgSandboxPolicy = "sandbox"

// svgMediaType is matched against the media type rather than the extension,
// because that is the value the response actually asserts.
const svgMediaType = "image/svg+xml"

// addSVGSandbox tightens an SVG response.
//
// Add rather than Set: the security headers middleware writes the application's
// own Content-Security-Policy, and Set on this field would drop it from every
// asset response. Two policies are both enforced and the browser applies their
// intersection, so this can only tighten what the application declared.
func addSVGSandbox(header http.Header, contentType string, enabled bool) {
	if !enabled || !isSVGMediaType(contentType) {
		return
	}
	header.Add("Content-Security-Policy", svgSandboxPolicy)
}

// isSVGMediaType ignores any parameters, so image/svg+xml; charset=utf-8 is
// still an SVG.
func isSVGMediaType(contentType string) bool {
	base, _, _ := strings.Cut(contentType, ";")
	return strings.EqualFold(strings.TrimSpace(base), svgMediaType)
}

// reportedMismatches keeps one refused path to one log line. A crawler asking
// for the same broken asset a thousand times is one problem, not a thousand,
// and the set is bounded by the tree because only resolvable names reach here.
var reportedMismatches sync.Map

func reportPublicMismatch(ctx context.Context, name, reason string) {
	if _, seen := reportedMismatches.LoadOrStore(name, struct{}{}); seen {
		return
	}
	pwruntime.ReadLogger(ctx).Error("public asset content does not match its extension",
		pwruntime.String("asset", name), pwruntime.String("reason", reason))
}

func resolvePublicAsset(name string, config PublicAssetConfig, embedded fs.FS) (publicAsset, bool) {
	if publicDevelopment || config.ReadLocal {
		asset, found, rejected := readLocalPublicAsset(name)
		if found {
			return asset, true
		}
		if publicDevelopment || rejected {
			return publicAsset{}, false
		}
	}
	return readEmbeddedPublicAsset(embedded, name)
}

// safeLocalPath walks name under root, refusing a symbolic link at every step,
// and resolves a directory to its index.html.
//
// It reports the file, the URL the answer is actually for, whether one was
// found, and whether the walk hit something worth refusing outright rather than
// merely missing. The last two differ: a missing file may fall through to
// another layer, and a symbolic link may not.
func safeLocalPath(root, name string) (resolved string, answered string, found bool, rejected bool) {
	info, err := os.Lstat(root)
	if err != nil {
		return "", "", false, !os.IsNotExist(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", "", false, true
	}
	current := root
	for _, segment := range strings.Split(name, "/") {
		current = filepath.Join(current, filepath.FromSlash(segment))
		info, err = os.Lstat(current)
		if err != nil {
			return "", "", false, !os.IsNotExist(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", false, true
		}
	}
	if info.IsDir() {
		current = filepath.Join(current, "index.html")
		info, err = os.Lstat(current)
		if err != nil {
			return "", "", false, !os.IsNotExist(err)
		}
		name = path.Join(name, "index.html")
	}
	if !info.Mode().IsRegular() {
		return "", "", false, true
	}
	return current, name, true, false
}

func readLocalPublicAsset(name string) (publicAsset, bool, bool) {
	// The built tree is what is served in every mode. The authored tree is an
	// input: a development loop that read it would answer 404 for every
	// reference a conversion moved, since the page names the derived file.
	current, answered, found, rejected := safeLocalPath(filepath.FromSlash(localPublicRoot), name)
	if !found {
		return publicAsset{}, false, rejected
	}
	identity, err := os.ReadFile(current)
	if err != nil {
		return publicAsset{}, false, true
	}
	asset := publicAsset{name: answered, identity: identity}
	if !publicDevelopment {
		for rank := range staticContentCodings {
			sidecar := current + staticContentCodings[rank].suffix
			sidecarInfo, sidecarErr := os.Lstat(sidecar)
			if sidecarErr != nil || !sidecarInfo.Mode().IsRegular() || sidecarInfo.Mode()&os.ModeSymlink != 0 {
				continue
			}
			asset.encoded[rank], _ = os.ReadFile(sidecar)
		}
	}
	return asset, true, false
}

func readEmbeddedPublicAsset(embedded fs.FS, name string) (publicAsset, bool) {
	if embedded == nil {
		return publicAsset{}, false
	}
	info, err := fs.Stat(embedded, name)
	if err != nil {
		return publicAsset{}, false
	}
	if info.IsDir() {
		name = path.Join(name, "index.html")
		info, err = fs.Stat(embedded, name)
		if err != nil {
			return publicAsset{}, false
		}
	}
	if !info.Mode().IsRegular() {
		return publicAsset{}, false
	}
	identity, err := fs.ReadFile(embedded, name)
	if err != nil {
		return publicAsset{}, false
	}
	asset := publicAsset{name: name, identity: identity}
	for rank := range staticContentCodings {
		if sidecar, sidecarErr := fs.ReadFile(embedded, name+staticContentCodings[rank].suffix); sidecarErr == nil {
			asset.encoded[rank] = sidecar
		}
	}
	return asset, true
}

// selectPublicRepresentation picks the smallest stored form the client will
// take, or the identity bytes. The order is staticContentCodings, not the
// client's q-values, which state what can be read rather than what is worth
// sending.
//
// The returned rank indexes staticContentCodings, or is -1 for identity, so the
// caller reads the matching validator without matching on the token again.
func selectPublicRepresentation(r *http.Request, asset publicAsset) ([]byte, int, bool) {
	return selectRepresentationFor(r.Header["Accept-Encoding"], asset)
}

// selectRepresentationFor is selectPublicRepresentation over the header values
// it reads, which is all it reads.
func selectRepresentationFor(values []string, asset publicAsset) ([]byte, int, bool) {
	if publicDevelopment {
		return asset.identity, -1, true
	}
	if len(values) == 0 {
		return asset.identity, -1, true
	}
	header := strings.Join(values, ",")
	if strings.TrimSpace(header) == "" {
		return asset.identity, -1, true
	}
	quality := scanEncodingQuality(header)
	for rank := range staticContentCodings {
		if len(asset.encoded[rank]) > 0 && quality.acceptsCoding(rank) > 0 {
			return asset.encoded[rank], rank, true
		}
	}
	if quality.acceptsIdentity() > 0 {
		return asset.identity, -1, true
	}
	return nil, -1, false
}

// The exported names below are the transport-free half of static asset
// serving, for the second transport's own middleware.
//
// They are exported rather than moved because everything they touch is already
// pure: the manifest and the coding negotiation name no transport at all, and
// what is left here is which header to read and where to write bytes. Moving
// eight hundred lines of asset handling to share four functions would be a
// large change to security-relevant code for no behavioural gain — but the path
// check in particular has to be shared, because two implementations of "which
// names may be served" are two chances to serve one that must not be.
//
// When the clean split reaches this layer, the pure half moves to a shared leaf
// and these become aliases.

// PublicAsset is one resolved asset: its bytes and every precompressed
// representation the build produced.
type PublicAsset = publicAsset

// ResolvedPublicAsset is a PublicAsset with its media type and validators
// computed.
type ResolvedPublicAsset = resolvedPublicAsset

// PublicAssetName validates a request path as an asset name, refusing anything
// that could escape the tree or name a precompressed sidecar directly.
func PublicAssetName(name string) (string, bool) { return publicAssetName(name) }

// ResolvePublicAsset reads one asset from the local tree or the embedded one.
func ResolvePublicAsset(name string, config PublicAssetConfig, embedded fs.FS) (PublicAsset, bool) {
	return resolvePublicAsset(name, config, embedded)
}

// FinishPublicAsset computes the media type and the validators of an asset.
func FinishPublicAsset(asset PublicAsset) *ResolvedPublicAsset { return finishPublicAsset(asset) }

// PublicRepresentation chooses what to send for one Accept-Encoding value,
// returning the body, the coding rank, and whether anything was acceptable.
func PublicRepresentation(acceptEncoding []string, asset PublicAsset) ([]byte, int, bool) {
	return selectRepresentationFor(acceptEncoding, asset)
}

// PublicCodingToken names the content coding of a rank returned by
// PublicRepresentation.
func PublicCodingToken(rank int) string { return staticContentCodings[rank].token }

// PublicManifestAnswer resolves a request path below the mount to the entry
// that answers it and the Cache-Control that answer carries.
//
// The cache policy comes back beside the entry rather than being read off it,
// because a revisioned URL and a plain one are the same entry answered under
// two promises. A transport that read entry.CacheControl directly would serve
// the revalidating policy for a URL a document promised was immutable, which is
// the whole point of the segment lost silently.
func PublicManifestAnswer(name string) (AssetEntry, string, bool) { return publicManifestAnswer(name) }

// PublicManifestRegistered reports whether a build produced a manifest, which
// is what lets the request path read bytes and nothing else.
func PublicManifestRegistered() bool { return !publicDevelopment && manifestRegistered() }

// PublicManifestRepresentation chooses what to send for a manifest entry.
func PublicManifestRepresentation(entry AssetEntry, accept, acceptEncoding []string) (AssetRepresentation, bool) {
	return selectRepresentation(entry, accept, acceptEncoding)
}

// PublicVary names the headers that can change the answer for an entry.
func PublicVary(entry AssetEntry) string { return varyForEntry(entry) }

// PublicDevelopment reports whether this build serves assets in development
// mode, where nothing is negotiated and nothing is cached.
func PublicDevelopment() bool { return publicDevelopment }

// Asset returns the underlying representations, so a second transport can
// negotiate over them.
func (r *resolvedPublicAsset) Asset() publicAsset { return r.asset }

// ContentType is the media type this asset is sent as.
func (r *resolvedPublicAsset) ContentType() string { return r.contentType }

// Mismatch is the reason this file must not be served — its bytes contradict
// its extension — and empty when it is servable. A second transport reads it to
// apply the same refusal the net/http path does rather than serving a
// mislabelled file.
func (r *resolvedPublicAsset) Mismatch() string { return r.mismatch }

// ReportPublicMismatch logs a content/extension mismatch once per asset, so a
// second transport reports it the same way the net/http path does.
func ReportPublicMismatch(ctx context.Context, name, reason string) {
	reportPublicMismatch(ctx, name, reason)
}

// ETag is the validator for one coding rank, or for the identity
// representation when rank is negative.
func (r *resolvedPublicAsset) ETag(rank int) string {
	if rank >= 0 {
		return r.encodedTags[rank]
	}
	return r.identityTag
}
