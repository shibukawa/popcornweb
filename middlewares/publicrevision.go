package middlewares

// A revision segment is how an asset that kept the name its author wrote gets
// an immutable URL.
//
// The other way is renaming the file, which the build already does for every
// URL it invents. That only works where every reference is rewritten, and a
// stylesheet link is not rewritten by anything: renaming public/app.css would
// simply make it gone. The segment moves the digest out of the file name and
// into the URL, so the file keeps its name and the URL still changes when the
// bytes do — as long as the document names it through PublicAssetURL rather
// than as a literal.
//
// It is the shape pwbrowser already serves the framework's own scripts under,
// for the same reason: an asset reached through a digest of its own bytes can
// promise never to change and be believed.

import (
	"strings"

	"github.com/shibukawa/popcornweb/pwruntime"
)

// RevisionedCacheControl is what an asset reached through its revision segment
// carries. Different bytes are a different segment, so this is genuinely
// immutable rather than merely long-lived.
//
// It is stated here rather than read from pwbrowser because the two are the
// same string for the same reason and not by dependency: that one describes the
// framework's script set, and this one describes an application's tree.
const RevisionedCacheControl = "public, max-age=31536000, immutable"

// RevisionLength is how much of the digest a segment carries. Sixteen hex
// characters is 64 bits, which is what pwbrowser's own revision uses, and the
// build reads this constant rather than repeating the number.
const RevisionLength = 16

// defaultPublicMount answers when nothing published a configuration, which is
// every unit test and no serving process.
const defaultPublicMount = "/public/"

// PublicAssetURL is the URL a document should name for one asset, given its
// path inside the served tree.
//
// A build that declared a revision for the asset gets the revisioned URL, which
// is answered immutably. Everything else gets the plain URL, which revalidates:
// the development loop, where a manifest would only make an edit invisible; a
// project that never ran a build; a name that already carries its own digest,
// which needs no second copy of the same statement; and a file in the external
// tree, whose bytes the build never read.
//
// Both URLs are correct and both are served. Only one of them can be cached
// forever, and which one this is depends on what the build knew — never on what
// the template wrote.
func PublicAssetURL(name string) string {
	mount := resolvedPublicMount()
	target, ok := publicAssetTarget(name, mount)
	if !ok {
		// Not a name this middleware would ever serve. It is returned under the
		// mount unchanged so the 404 lands where a reader can see it, rather
		// than being reshaped into a URL that looks deliberate.
		return mount + strings.TrimPrefix(name, "/")
	}
	// The development loop serves the working tree and never consults the
	// manifest, so it must not be asked for a URL only the manifest can
	// resolve. The generated manifest is linked into a pwdev binary all the
	// same — it is an init in the project's own package — which is why the
	// build mode is asked here rather than whether one was registered: naming
	// the revision there was a stylesheet link that 404ed on every page.
	if publicDevelopment {
		return mount + target
	}
	entry, found := manifestEntry(target)
	if !found || entry.Revision == "" {
		return mount + target
	}
	return mount + entry.Revision + "/" + target
}

// publicAssetTarget reads an asset name either way a caller is likely to have
// one: the path inside the served tree, or the whole URL a template used to
// write as a literal.
//
// The second is what a migration has in hand — the string being replaced is
// exactly "/public/app.css" — and refusing it would turn a mechanical edit into
// a 404 that only appears in a browser. The tree path is the form to write:
// the mount belongs to the runtime configuration, and a template that spells it
// out has a second place to change when it moves.
func publicAssetTarget(name, mount string) (string, bool) {
	trimmed := name
	if rest, cut := strings.CutPrefix(trimmed, mount); cut {
		trimmed = rest
	}
	return publicAssetName(strings.TrimPrefix(trimmed, "/"))
}

// resolvedPublicMount reads the mount a deployment configured, normalized the
// way the middleware normalizes it, so a URL a document names and a path the
// middleware matches cannot disagree.
func resolvedPublicMount() string {
	settings, published := pwruntime.ResolvedChainSettings()
	if !published {
		return defaultPublicMount
	}
	mount, err := NormalizePublicMount(settings.Public.Mount)
	if err != nil {
		// A mount this invalid fails the chain build with the same error, so
		// there is no process where this answer is the one anybody sees.
		return defaultPublicMount
	}
	return mount
}

// publicManifestAnswer resolves a request path below the mount to the entry
// that answers it and the cache policy that answer carries.
//
// The whole path is tried first, so a URL that worked before revisions existed
// answers exactly as it did, and so a real directory can never be mistaken for
// a revision. Only when nothing is stored under the whole path is the first
// segment read as one, and only when it equals the entry's own revision does
// the answer become immutable.
//
// A segment that names a revision this build does not serve is not found rather
// than answered from the current tree. That is what keeps the promise sound: a
// browser holding an old URL forever is holding a URL that 404s, not one that
// quietly returns different bytes.
func publicManifestAnswer(name string) (AssetEntry, string, bool) {
	if entry, found := manifestEntry(name); found {
		return entry, entry.CacheControl, true
	}
	segment, rest, split := strings.Cut(name, "/")
	if !split || !isRevisionSegment(segment) {
		return AssetEntry{}, "", false
	}
	entry, found := manifestEntry(rest)
	if !found || entry.Revision == "" || entry.Revision != segment {
		return AssetEntry{}, "", false
	}
	return entry, RevisionedCacheControl, true
}

// isRevisionSegment is a shape check, not a lookup: it decides whether reading
// the segment as a revision is worth a second pass over the manifest. The
// comparison against the entry's own revision is what actually decides.
func isRevisionSegment(segment string) bool {
	if len(segment) != RevisionLength {
		return false
	}
	for index := 0; index < len(segment); index++ {
		character := segment[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
