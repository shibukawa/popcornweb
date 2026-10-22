package pwcli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/shibukawa/popcornweb/internal/assetverify"
)

// The build writes three directories under dist. Only the first is served; the
// other two exist so a rebuild can be honest about what it did.
const (
	// derivedPublicDir is the tree the application embeds and serves. It is
	// rebuilt from scratch every time, which is what makes a removed source
	// remove its outputs without a cleanup rule.
	derivedPublicDir = "dist/public"
	// derivedStageDir receives what the generation-time hooks produced. It is
	// separate from the served tree so that tree can be cleared without
	// deleting files pw generate wrote in an earlier step.
	derivedStageDir = "dist/derived"
	// extractedAssetDir receives the style and script blocks a component
	// declares, which the generator extracts to content-hashed files.
	//
	// It is inside the authored public tree rather than beside the staged
	// conversions, because these are referenced by a URL the generator computed
	// at generation time and are as much a source of the served tree as an
	// authored file is. It is also where flow:tailwind-css-build already writes,
	// so nothing new has to be embedded or served.
	extractedAssetDir = "public/generated"
	// conversionCacheDir keeps conversion outcomes across runs, including the
	// decision to decline one. Deleting it costs time and nothing else.
	conversionCacheDir = "dist/cache"
)

// externalPublicDir is the second authored tree, and the one exception to the
// paragraph above: it is not under dist because it is not build output. Nothing
// transforms what is in it, so a staged copy would be identical to its source,
// and these are the files a project least wants copied. It is committed like
// public/ and the container scaffold ships it as it stands.
const externalPublicDir = "public-external"

// Representation ordering. The build states which media type is worth serving,
// because that is a judgment about the bytes; the request only says what it can
// read. The last one is the fallback every client can take.
const (
	representationPreferenceFirst   = 0
	representationPreferenceDefault = 1
)

// sidecarCoding is one precompressed form the build writes beside a file.
//
// The order is the order a response prefers, and it is pure ratio: every one of
// these is encoded here, so preferring the smallest costs a request nothing.
// That is the whole reason brotli appears at all — at these levels it beats the
// others by a margin no per-request encoder could pay for, and it never has to
// run while a request waits.
type sidecarCoding struct {
	token  string
	suffix string
	// params names the encoder level so a cached sidecar misses when the level
	// changes, the same reason the image cache digests its encoder parameters.
	params string
	encode func([]byte) ([]byte, error)
}

func derivedSidecarCodings() ([]sidecarCoding, error) {
	zstdEncoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedBestCompression),
		zstd.WithEncoderConcurrency(1),
		zstd.WithEncoderCRC(false),
	)
	if err != nil {
		return nil, fmt.Errorf("public assets: create zstd encoder: %w", err)
	}
	return []sidecarCoding{
		{token: "br", suffix: ".br", params: "brotli/1.2.6 " + strconv.Itoa(brotli.BestCompression), encode: encodeBrotli},
		{token: "zstd", suffix: ".zstd", params: "compress/1.20.3 " + zstd.SpeedBestCompression.String(), encode: func(source []byte) ([]byte, error) {
			return zstdEncoder.EncodeAll(source, nil), nil
		}},
		{token: "gzip", suffix: ".gz", params: "compress/1.20.3 " + strconv.Itoa(gzip.BestCompression), encode: encodeGzip},
	}, nil
}

func encodeBrotli(source []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := brotli.NewWriterLevel(&buffer, brotli.BestCompression)
	if _, err := writer.Write(source); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func encodeGzip(source []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(source); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// sidecarTokens maps a produced suffix to its coding token, in the same order.
// The walks that only recognize a produced file read this rather than build an
// encoder to ask.
var sidecarTokens = [...]struct{ suffix, token string }{
	{suffix: ".br", token: "br"},
	{suffix: ".zstd", token: "zstd"},
	{suffix: ".gz", token: "gzip"},
}

// sidecarTokenFor reports the URL a produced sidecar is a representation of and
// the coding it carries, or false for a path that is an asset rather than a
// sidecar. The suffix is not the token: gzip is stored as .gz.
func sidecarTokenFor(name string) (url string, token string, ok bool) {
	for _, entry := range sidecarTokens {
		if strings.HasSuffix(name, entry.suffix) {
			return strings.TrimSuffix(name, entry.suffix), entry.token, true
		}
	}
	return "", "", false
}

// hasSidecarSuffix reports whether a path names a generated sidecar of any
// coding.
func hasSidecarSuffix(name string) bool {
	_, _, found := sidecarTokenFor(name)
	return found
}

// derivedAsset is one file in the built tree, with the metadata the manifest
// needs. Everything here is decided by the build, never at request time.
type derivedAsset struct {
	url       string
	path      string
	mediaType string
	encoding  string
	length    int
	etag      string
	// immutable marks a URL the build invented, whose name carries the digest
	// of its own bytes. Only such a URL can promise never to change: a name the
	// author wrote serves whatever the next build puts behind it.
	immutable  bool
	preference int
	// external marks a file in the second authored tree, which ships beside the
	// binary rather than inside it. Such an asset carries no length and no
	// etag: the build never read it whole and never wants to claim it did.
	external bool
}

// derivedReport is what the build tells the developer. A rewrite is invisible
// in the template, so the build is the only place it can be seen.
type derivedReport struct {
	converted []string
	skipped   []string
	retained  []string
	// unserved names a file the served tree never owed anyone, which is a
	// different statement from a source some conversion replaced.
	unserved []string
	// exempted names a file assets.verify.allow carried past the content
	// checks. It is reported so the list cannot go quiet: an exemption added
	// for one bad file and never removed is how a check stops being one.
	exempted []string
	// shadowed names an external file that took a URL the embedded tree also
	// holds. The external one wins, so this is a warning and not an error, but
	// the embedded file is still there and still looks like the one answering.
	shadowed []string
	written  int
}

// verifyOptions is the project's switches in the form the shared checker takes.
func verifyOptions(assets assetsConfig) assetverify.Options {
	return assetverify.Options{
		Signature: assets.Verify,
		SVGScan:   assets.VerifySVG,
		Allow:     assets.VerifyAllow,
	}
}

// buildDerivedAssets turns the authored public tree and whatever the generation
// hooks produced into the served tree and its manifest.
//
// The order is load-bearing: the tree is cleared before anything is copied into
// it, sources are dropped only after their replacement exists, sidecars are
// written over final bytes, and the manifest is written last because it
// describes all of it.
func buildDerivedAssets(root string, assets assetsConfig) (derivedReport, error) {
	// A variant is converted by the tree walk, so the upstream conversion cache
	// never sees it. Without one of its own, every build re-encoded every image
	// whether or not anything had changed.
	cache := filepath.Join(root, filepath.FromSlash(assets.locations().cache))
	return buildDerivedAssetsWithEncoder(root, assets, cachedImageEncoder(cache, "avif", encodeAVIF))
}

// buildDerivedAssetsWithEncoder is the body, with the variant encoder stated.
// Whether a variant wins is a property of the image; where it lands is a
// property of this code, and only the second is worth a test running an encoder.
func buildDerivedAssetsWithEncoder(root string, assets assetsConfig, encodeVariant imageEncoder) (derivedReport, error) {
	report := derivedReport{}
	if err := validateAssetLocations(root, assets); err != nil {
		return report, err
	}
	authored := filepath.Join(root, filepath.FromSlash(assets.locations().public))
	output := filepath.Join(root, filepath.FromSlash(assets.locations().derived))
	staged := filepath.Join(root, filepath.FromSlash(assets.locations().stage))

	if err := verifyPublicEmbed(root, assets); err != nil {
		return report, err
	}
	rootInfo, err := os.Lstat(authored)
	if err != nil {
		return report, fmt.Errorf("public assets: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return report, fmt.Errorf("public assets: public must be a regular directory")
	}
	if err := os.RemoveAll(output); err != nil {
		return report, fmt.Errorf("public assets: clear %s: %w", derivedPublicDir, err)
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return report, fmt.Errorf("public assets: create %s: %w", derivedPublicDir, err)
	}
	// The sentinel is committed, because go:embed fails on an absent directory
	// and a fresh clone compiles before it ever runs a build. Clearing the tree
	// removes it, so it is written back rather than left as a deletion.
	if err := writeDerivedFile(filepath.Join(output, ".keep"), nil); err != nil {
		return report, err
	}

	produced, err := stagedProducedFiles(staged)
	if err != nil {
		return report, err
	}
	if !assets.SourceMaps {
		report.unserved = append(report.unserved, dropSourceMaps(produced)...)
	}
	// A conversion is recognized by its output existing, so nothing has to be
	// threaded back from the generator to know which sources were replaced.
	converted := map[string]string{}
	for name := range produced {
		if source, ok := convertedSourceFor(name, authored); ok {
			converted[source] = name
		}
	}

	rewrites := publicURLRewrites(converted)
	// Built only when the first retention question arrives: a walk with nothing
	// converted and no script inputs never pays for reading the project.
	var retention *retentionScanner
	retentionScan := func() (*retentionScanner, error) {
		if retention == nil {
			built, err := newRetentionScanner(root, assets)
			if err != nil {
				return nil, err
			}
			retention = built
		}
		return retention, nil
	}
	err = filepath.WalkDir(authored, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("public assets: symbolic links are not allowed: %s", name)
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("public assets: irregular file is not allowed: %s", name)
		}
		relative, err := filepath.Rel(authored, name)
		if err != nil {
			return err
		}
		slashed := filepath.ToSlash(relative)
		if hasSidecarSuffix(slashed) {
			// A sidecar in the authored tree is left over from the build this
			// one replaces; the served tree gets its own.
			return nil
		}
		if replacement, ok := converted[slashed]; ok {
			scan, err := retentionScan()
			if err != nil {
				return err
			}
			retain, reason := scan.mustBeRetained(slashed)
			report.converted = append(report.converted, slashed+" -> "+replacement)
			if !retain {
				return nil
			}
			report.retained = append(report.retained, slashed+" ("+reason+")")
		} else if scriptBuildInput(slashed, assets) {
			scan, err := retentionScan()
			if err != nil {
				return err
			}
			retain, reason := scan.mustBeRetained(slashed)
			if !retain {
				report.unserved = append(report.unserved, slashed+" (TypeScript is a build input)")
				return nil
			}
			// Reported only as retained. A file that ships is not one the build
			// declined to serve, and saying both would describe two artifacts.
			report.retained = append(report.retained, slashed+" ("+reason+")")
		}
		source, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		// Verification is here rather than in a pass of its own because the
		// bytes are already in hand. It runs on the authored file, before any
		// transform: what the build produced is labelled by the build, and
		// re-deriving a type from a name it chose would only test it against
		// itself.
		if finding, refused := assetverify.File(slashed, source, verifyOptions(assets)); refused {
			return fmt.Errorf("public assets: %w", finding)
		}
		if assetverify.Exempt(slashed, assets.VerifyAllow) {
			report.exempted = append(report.exempted, slashed)
		}
		content, err := transformAuthoredFile(slashed, source, assets, rewrites)
		if err != nil {
			return err
		}
		return writeDerivedFile(filepath.Join(output, filepath.FromSlash(slashed)), content)
	})
	if err != nil {
		return report, err
	}

	for name, content := range produced {
		target := filepath.Join(output, filepath.FromSlash(name))
		if _, err := os.Lstat(target); err == nil {
			return report, fmt.Errorf("public assets: %s is produced by a conversion and also authored", name)
		}
		if err := writeDerivedFile(target, content); err != nil {
			return report, err
		}
	}

	if assets.AVIF {
		variants, err := writeAVIFVariants(authored, output, variantTargets(authored, output, converted), assets, encodeVariant)
		if err != nil {
			return report, err
		}
		report.skipped = append(report.skipped, variants...)
	}
	if err := writeDerivedSidecars(output, filepath.Join(root, filepath.FromSlash(assets.locations().cache))); err != nil {
		return report, err
	}
	immutable := map[string]bool{}
	for name := range produced {
		immutable[name] = true
	}
	entries, err := collectDerivedAssets(output, immutable)
	if err != nil {
		return report, err
	}
	entries, err = addExternalAssets(root, entries, assets, &report)
	if err != nil {
		return report, err
	}
	report.written = len(entries)
	if err := writeAssetManifest(root, entries, assets); err != nil {
		return report, err
	}
	return report, nil
}

// verifyPublicEmbed refuses to build a tree the application will not embed. The
// scaffolded file is never rewritten by pw, so an older project is told exactly
// what to change rather than silently serving its authored tree.
func verifyPublicEmbed(root string, assets assetsConfig) error {
	l := assets.locations()
	name := filepath.Join(root, filepath.FromSlash(l.embed))
	relative, err := filepath.Rel(filepath.Dir(filepath.FromSlash(l.embed)), filepath.FromSlash(l.derived))
	if err != nil {
		return err
	}
	pattern := filepath.ToSlash(relative)
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%s is required; run pw init to create the public embed scaffold", l.embed)
	}
	source, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(source), "\n") {
		if directive, ok := strings.CutPrefix(line, "//go:embed "); ok {
			for _, token := range strings.Fields(directive) {
				if token == "all:"+pattern {
					return nil
				}
			}
		}
	}
	return fmt.Errorf(`%s does not embed the configured derived tree.
Change its two references:
  //go:embed all:%s
  fs.Sub(embeddedPublic, "%s")`, l.embed, pattern, pattern)
}

// stagedProducedFiles reads what the generation hooks wrote. An empty or absent
// directory is the ordinary case for a project registering no transform.
func stagedProducedFiles(staged string) (map[string][]byte, error) {
	produced := map[string][]byte{}
	info, err := os.Lstat(staged)
	if err != nil || !info.IsDir() {
		return produced, nil
	}
	err = filepath.WalkDir(staged, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(staged, name)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		produced[filepath.ToSlash(relative)] = content
		return nil
	})
	return produced, err
}

// convertedSourceFor maps a produced file back to the authored file it replaced,
// which is possible because this project owns both naming rules: an image keeps
// its path and changes its extension, and a script entry does the same.
func convertedSourceFor(produced, authored string) (string, bool) {
	extension := path.Ext(produced)
	base := strings.TrimSuffix(produced, extension)
	// A produced name carries the digest of its own bytes, which is what makes
	// its URL immutable. The source it came from is the name without it.
	base = strings.TrimSuffix(base, "."+contentHashOf(base))
	var candidates []string
	switch extension {
	case ".webp":
		candidates = []string{".png", ".jpg", ".jpeg"}
	case ".js":
		candidates = []string{".ts", ".tsx"}
	default:
		return "", false
	}
	for _, candidate := range candidates {
		source := base + candidate
		if info, err := os.Lstat(filepath.Join(authored, filepath.FromSlash(source))); err == nil && info.Mode().IsRegular() {
			return source, true
		}
	}
	return "", false
}

// dropSourceMaps takes the emitted maps out of the produced set and takes the
// comment naming each one out of the bundle it was written into. It reports
// what it dropped, sorted, so the build says so rather than quietly shipping a
// different artifact than the last invocation did.
//
// It runs here rather than in the conversion because the map is decided by how
// the build was invoked, and a conversion is memoized against its inputs and
// replayed. Deciding it here also keeps generation identical between the two
// artifact shapes: the bundle digest is taken over the body without its comment,
// so removing the comment leaves the name, the rewritten reference, and every
// byte of generated Go exactly where they were.
//
// The comment has to go with the file. A bundle still naming a map the tree does
// not hold turns every devtools open into a request for a file that is not
// there, which is a worse artifact than the one this is removing.
func dropSourceMaps(produced map[string][]byte) []string {
	var dropped []string
	for name := range produced {
		if path.Ext(name) != ".map" {
			continue
		}
		dropped = append(dropped, name+" (this artifact carries no source map)")
		delete(produced, name)
	}
	for name, content := range produced {
		if path.Ext(name) != ".js" {
			continue
		}
		if body, comment := splitSourceMapComment(string(content)); comment != "" {
			produced[name] = []byte(body)
		}
	}
	sort.Strings(dropped)
	return dropped
}

// scriptBuildInput reports whether an authored file is one the script build
// consumes rather than one the served tree owes anyone.
//
// No browser runs TypeScript, so with the script build on, a .ts or .tsx under
// public is an input by definition. An entry is already recognized by the bundle
// that replaced it; this is the module that entry imported, which no conversion
// produces a file for and which was therefore copied out beside the bundle it
// had been compiled into. The emitted source map carries its text, so a stack
// trace still names the authored line.
//
// Without the script build nothing consumes it, and a file the build does not
// understand is served as written, as everything else under public is.
func scriptBuildInput(name string, assets assetsConfig) bool {
	if !assets.Scripts {
		return false
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".ts", ".tsx":
		return true
	}
	return false
}

// contentHashOf reports the digest segment a produced name ends with, or the
// empty string when it ends with something else. It is what lets a produced
// file be mapped back to the source it replaced without threading a table out
// of generation.
func contentHashOf(name string) string {
	segment := name[strings.LastIndexByte(name, '.')+1:]
	if len(segment) != contentHashLength {
		return ""
	}
	for index := 0; index < len(segment); index++ {
		character := segment[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return ""
		}
	}
	return segment
}

// publicURLRewrites turns the conversion set into the substitutions a stylesheet
// pass applies, so a background-image follows the file it names.
func publicURLRewrites(converted map[string]string) map[string]string {
	rewrites := make(map[string]string, len(converted))
	for source, target := range converted {
		rewrites[source] = target
	}
	return rewrites
}

// retentionScanner decides whether dropping an authored file would break a
// reference the build cannot rewrite. Keeping a converted source costs bytes;
// dropping one that a script or a meta tag still names costs a broken page, so
// the scan errs toward keeping it and says so.
//
// The sources are read once and every asset is answered from that one pass.
// Retention used to walk and re-read the whole project per asset, which made
// the question O(assets × project files) of disk I/O on every rebuild.
type retentionScanner struct {
	sites   []referenceSite
	sources []scannedSource
}

// scannedSource is one file a reference could hide in, held in memory for the
// duration of the build.
type scannedSource struct {
	name       string
	content    string
	isTemplate bool
}

func newRetentionScanner(root string, assets assetsConfig) (*retentionScanner, error) {
	scanner := &retentionScanner{sites: rewrittenReferenceSites(root, assets)}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			l := assets.locations()
			for _, dir := range []string{l.public, l.derived, l.stage, l.cache} {
				if name == filepath.Join(root, filepath.FromSlash(dir)) {
					return filepath.SkipDir
				}
			}
			switch entry.Name() {
			case ".git", ".pw", "dist", "node_modules", "public", ".devbox":
				return filepath.SkipDir
			}
			if nestedWorktree(root, name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !scannableSource(entry.Name()) {
			return nil
		}
		// This build's own output is not evidence about what a developer wrote.
		// The manifest names every served URL, so a source retained by one build
		// appears in the next build's manifest and would keep itself retained
		// forever; generated Go carries the rewritten reference anyway, and the
		// authored file it came from is scanned on its own.
		if strings.HasSuffix(entry.Name(), "_pw_gen.go") {
			return nil
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return nil
		}
		relativeName, relErr := filepath.Rel(root, name)
		if relErr != nil {
			relativeName = name
		}
		scanner.sources = append(scanner.sources, scannedSource{
			name:       filepath.ToSlash(relativeName),
			content:    string(content),
			isTemplate: strings.HasSuffix(entry.Name(), ".pw.html"),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return scanner, nil
}

func (s *retentionScanner) mustBeRetained(relative string) (bool, string) {
	for _, source := range s.sources {
		if !strings.Contains(source.content, relative) {
			continue
		}
		if source.isTemplate &&
			ownedReferenceCount(source.content, relative, s.sites) >= strings.Count(source.content, relative) {
			continue
		}
		// A rewritten reference no longer names the source, so an occurrence
		// that survived generation is one this build could not reach.
		return true, "still named by " + source.name
	}
	return false, ""
}

// referenceSite is one element and attribute a conversion rewrites.
type referenceSite struct{ element, attribute string }

// rewrittenReferenceSites reads the positions the hooks own off the hooks
// themselves, so the retention scan and the transforms cannot drift apart about
// what a rewritten reference is.
//
// A disabled conversion contributes no site, which is right: with nothing
// converting an image, no img src occurrence is a reference anything reached.
func rewrittenReferenceSites(root string, assets assetsConfig) []referenceSite {
	hooks := assetReferenceHooks(root, assets)
	sites := make([]referenceSite, 0, len(hooks))
	for _, hook := range hooks {
		sites = append(sites, referenceSite{element: hook.Element, attribute: hook.Attribute})
	}
	return sites
}

// ownedReferenceCount counts the occurrences of a source that sit in an
// attribute a conversion rewrites.
//
// The retention scan reads a surviving occurrence as proof the build could not
// reach it, and for a stylesheet that holds: its url() is rewritten in the bytes
// that ship, so what is left is what was missed. A template is not that. The
// authored .pw.html is an input, the rewrite lands in the generated Go beside
// it, and the template goes on naming the file its author wrote. Every asset any
// template referenced was therefore retained, and the build shipped both the png
// and the webp it had just made.
//
// Only the owned positions are discounted. A meta tag, a link href, or a path a
// script builds still counts, which is the case retention exists for, and the
// count errs toward retaining: an occurrence this cannot account for keeps the
// file.
func ownedReferenceCount(source, needle string, sites []referenceSite) int {
	if len(sites) == 0 {
		return 0
	}
	count, rest := 0, source
	for {
		start := strings.IndexByte(rest, '<')
		if start < 0 {
			return count
		}
		end := strings.IndexByte(rest[start:], '>')
		if end < 0 {
			return count
		}
		end += start
		tag := rest[start : end+1]
		for _, site := range sites {
			if tagIsElement(tag, site.element) && assetTreePath(tagAttribute(tag, site.attribute)) == needle {
				count++
			}
		}
		rest = rest[end+1:]
	}
}

// tagIsElement reports whether a tag opens the named element, so that "<script"
// is not read out of "<scripted" and a closing tag names nothing.
func tagIsElement(tag, element string) bool {
	if !strings.HasPrefix(strings.ToLower(tag), "<"+element) {
		return false
	}
	rest := tag[1+len(element):]
	return rest == "" || isTagSpace(rest[0]) || rest[0] == '>' || rest[0] == '/'
}

// scannableSource lists the file kinds a reference can hide in. A binary is
// excluded because a byte sequence matching a path inside one is not a
// reference.
func scannableSource(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".html", ".htm", ".js", ".mjs", ".ts", ".tsx", ".css", ".json", ".md", ".txt", ".xml", ".toml", ".yaml", ".yml":
		return true
	}
	return strings.HasSuffix(name, ".pw.html")
}

func writeDerivedFile(target string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return writeScaffoldFile(target, content)
}

// variantTargets maps every served image URL back to the authored file it came
// from, whether a conversion moved it or not.
//
// An unconverted image is included on purpose: a machine with an avif encoder
// and no webp one — every stock mac, since sips writes avif and cannot write
// webp — still has something to offer, and it offers it under the URL the page
// already names.
func variantTargets(authored, output string, converted map[string]string) map[string]string {
	targets := map[string]string{}
	for source, target := range converted {
		targets[target] = source
	}
	_ = filepath.WalkDir(output, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(output, name)
		if relErr != nil {
			return nil
		}
		slashed := filepath.ToSlash(relative)
		if _, claimed := targets[slashed]; claimed || !convertibleImage(slashed) {
			return nil
		}
		if info, statErr := os.Lstat(filepath.Join(authored, filepath.FromSlash(slashed))); statErr == nil && info.Mode().IsRegular() {
			targets[slashed] = slashed
		}
		return nil
	})
	return targets
}

// writeAVIFVariants adds a second media representation to every served image,
// stored as a sibling of the URL rather than as a URL of its own.
//
// It is a sibling because markup cannot express the choice: the build never
// writes a picture element, and srcset carries density descriptors and no media
// type, so the only place left to choose is the response.
//
// It encodes from the authored source rather than from the webp, because a
// second lossy pass over already-lossy bytes is worse than one pass over the
// original.
func writeAVIFVariants(authored, output string, targets map[string]string, assets assetsConfig, encode imageEncoder) ([]string, error) {
	var declined []string
	served := make([]string, 0, len(targets))
	for target := range targets {
		served = append(served, target)
	}
	sort.Strings(served)
	for _, target := range served {
		source := targets[target]
		primary := filepath.Join(output, filepath.FromSlash(target))
		reference, err := os.Stat(primary)
		if err != nil {
			continue
		}
		lossless := losslessSource(source)
		encoded, err := encode(filepath.Join(authored, filepath.FromSlash(source)),
			lossless, assets.ImageQuality)
		if errors.Is(err, errNoEncoder) {
			// Nothing here can write the format on the axis this source needs,
			// which is a diagnostic rather than a build failure: the primary
			// representation still serves.
			//
			// The axis is named because the two resolve separately: a machine
			// can write a lossy variant of a photograph and be unable to write
			// a lossless one of a png, and only one of the two is a reason to
			// install anything.
			declined = append(declined, target+" (no "+losslessLabel(lossless)+" avif encoder)")
			continue
		}
		if err != nil {
			return nil, err
		}
		// The variant only earns its place by being smaller than the
		// representation it would be chosen over.
		if int64(len(encoded)) >= reference.Size() {
			declined = append(declined, target+" (avif was larger than the webp)")
			continue
		}
		if err := writeDerivedFile(primary+".avif", encoded); err != nil {
			return nil, err
		}
	}
	return declined, nil
}

// losslessLabel names an axis for a report line.
func losslessLabel(lossless bool) string {
	if lossless {
		return "lossless"
	}
	return "lossy"
}

// sidecarCacheDir keeps compression outcomes across runs, beside the image
// variants under the conversion cache. The served tree is rebuilt from nothing
// every time, and brotli at this level runs at a few megabytes a second, so an
// unchanged asset must not pay for it twice.
const sidecarCacheDir = "sidecars"

// writeDerivedSidecars compresses the finished bytes. Compressing before a
// conversion would compress bytes nobody serves, which is why this runs last.
//
// A coding whose result is no smaller than its source is skipped rather than
// written: a sidecar that saves nothing costs a file in the embed and a
// representation in the manifest for no reason, and the negotiation falls
// through to the next coding on its own. The skip is cached too — as an empty
// entry — because declining a file costs the same encode as compressing it.
func writeDerivedSidecars(output, cacheRoot string) error {
	var files []string
	err := filepath.WalkDir(output, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		if hasSidecarSuffix(name) || entry.Name() == ".keep" {
			return nil
		}
		if !publicAssetCompressible(name) {
			return nil
		}
		files = append(files, name)
		return nil
	})
	if err != nil || len(files) == 0 {
		return err
	}

	// The files are independent, so they run on the conversion workers. The
	// channel holds them all before any worker starts: a worker that stops on
	// an error must not leave the send side blocked.
	workers := min(conversionWorkers(), len(files))
	jobs := make(chan string, len(files))
	for _, name := range files {
		jobs <- name
	}
	close(jobs)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			// Each worker holds its own encoders: the zstd encoder hands out a
			// single internal state, so a shared one would serialize the pool.
			codings, err := derivedSidecarCodings()
			if err != nil {
				errs <- err
				return
			}
			for name := range jobs {
				if err := writeSidecarsFor(name, cacheRoot, codings); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	group.Wait()
	close(errs)
	return <-errs
}

// writeSidecarsFor writes every winning coding of one file, answering from the
// sidecar cache when the same bytes were already encoded at the same level.
func writeSidecarsFor(name, cacheRoot string, codings []sidecarCoding) error {
	source, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(source)
	key := hex.EncodeToString(digest[:])[:32]
	for _, coding := range codings {
		cachePath := ""
		if cacheRoot != "" {
			cachePath = filepath.Join(cacheRoot, sidecarCacheDir,
				coding.token+"-"+coding.params, key+coding.suffix)
			if cached, readErr := os.ReadFile(cachePath); readErr == nil {
				// An encoded result is never empty, so an empty entry is a
				// cached decline rather than a miss.
				if len(cached) == 0 {
					continue
				}
				if writeErr := writeScaffoldFile(name+coding.suffix, cached); writeErr != nil {
					return writeErr
				}
				continue
			}
		}
		encoded, encodeErr := coding.encode(source)
		if encodeErr != nil {
			return fmt.Errorf("public assets: encode %s as %s: %w", name, coding.token, encodeErr)
		}
		declined := len(encoded) >= len(source)
		if cachePath != "" {
			if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
				// A cache that cannot be written is a slow build and nothing
				// worse, so the write is best effort and its failure is not
				// the build's.
				payload := encoded
				if declined {
					payload = nil
				}
				_ = writeScaffoldFile(cachePath, payload)
			}
		}
		if declined {
			continue
		}
		if writeErr := writeScaffoldFile(name+coding.suffix, encoded); writeErr != nil {
			return writeErr
		}
	}
	return nil
}

// collectDerivedAssets reads the finished tree once and produces one manifest
// entry per URL, with the compressed sibling folded in as a representation of
// the same URL rather than as a URL of its own.
func collectDerivedAssets(output string, immutable map[string]bool) ([]derivedAsset, error) {
	var assets []derivedAsset
	err := filepath.WalkDir(output, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(output, name)
		if err != nil {
			return err
		}
		slashed := filepath.ToSlash(relative)
		if entry.Name() == ".keep" {
			return nil
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		url, encoding := slashed, ""
		if source, token, isSidecar := sidecarTokenFor(slashed); isSidecar {
			url, encoding = source, token
		}
		// A media sibling is a second representation of one URL, never a URL of
		// its own: nothing may link it, and a cache must not learn about it
		// except through the negotiated response.
		mediaType, preference := derivedMediaType(url, content), representationPreferenceDefault
		if strings.HasSuffix(url, ".avif") {
			url = strings.TrimSuffix(url, ".avif")
			mediaType, preference = "image/avif", representationPreferenceFirst
		}
		sum := sha256.Sum256(content)
		assets = append(assets, derivedAsset{
			url:        url,
			immutable:  immutable[url],
			path:       slashed,
			mediaType:  mediaType,
			encoding:   encoding,
			length:     len(content),
			etag:       fmt.Sprintf("%q", fmt.Sprintf("%x", sum[:16])),
			preference: preference,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(assets, func(i, j int) bool {
		if assets[i].url != assets[j].url {
			return assets[i].url < assets[j].url
		}
		if assets[i].preference != assets[j].preference {
			return assets[i].preference < assets[j].preference
		}
		return assets[i].encoding < assets[j].encoding
	})
	return assets, nil
}

// addExternalAssets folds the second authored tree into the manifest.
//
// It copies nothing, and that is the whole point of the tree existing. A file
// here is a kind policy:asset-transform-matrix already declines to touch, so a
// staging copy under dist would be byte-identical to its source — which for the
// largest files in a project is the most expensive nothing a build can do.
//
// It also computes no digest and records no length. The directory is deployed
// as its own artifact, so a validator taken here could describe bytes the
// deployment later replaced; the file answers for itself at request time.
func addExternalAssets(root string, entries []derivedAsset, assets assetsConfig, report *derivedReport) ([]derivedAsset, error) {
	authored := filepath.Join(root, externalPublicDir)
	info, err := os.Lstat(authored)
	if err != nil || !info.IsDir() {
		// A project without the tree is the ordinary case, not a failure.
		return entries, nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("public assets: %s must be a regular directory", externalPublicDir)
	}
	embedded := map[string]int{}
	for position, entry := range entries {
		if _, exists := embedded[entry.url]; !exists {
			embedded[entry.url] = position
		}
	}
	shadowed := map[string]bool{}
	var external []derivedAsset
	walkErr := filepath.WalkDir(authored, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		fileInfo, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("public assets: symbolic links are not allowed: %s", name)
		}
		if entry.IsDir() {
			return nil
		}
		if !fileInfo.Mode().IsRegular() {
			return fmt.Errorf("public assets: irregular file is not allowed: %s", name)
		}
		relative, err := filepath.Rel(authored, name)
		if err != nil {
			return err
		}
		slashed := filepath.ToSlash(relative)
		if entry.Name() == ".keep" || hasSidecarSuffix(slashed) {
			return nil
		}
		// Only the window, because the whole reason a file is here is that it
		// may be enormous. requirement:asset-content-verification reads no more
		// than that for anything but an SVG.
		prefix, err := leadingBytes(name, assetverify.IsSVG(slashed))
		if err != nil {
			return err
		}
		if finding, refused := assetverify.File(slashed, prefix, verifyOptions(assets)); refused {
			return fmt.Errorf("public assets: %w", finding)
		}
		if assetverify.Exempt(slashed, assets.VerifyAllow) {
			report.exempted = append(report.exempted, externalPublicDir+"/"+slashed)
		}
		if _, collides := embedded[slashed]; collides {
			// The external tree wins, so the collision is defined rather than
			// ambiguous, and a warning is the honest severity. It is worth
			// saying out loud because the embedded file still exists and still
			// looks like the one being served.
			shadowed[slashed] = true
			report.shadowed = append(report.shadowed,
				externalPublicDir+"/"+slashed+" (shadows public/"+slashed+")")
		}
		external = append(external, derivedAsset{
			url:        slashed,
			path:       slashed,
			mediaType:  derivedMediaType(slashed, prefix),
			preference: representationPreferenceDefault,
			external:   true,
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(external) == 0 {
		return entries, nil
	}
	kept := entries[:0:0]
	for _, entry := range entries {
		// Every representation of a shadowed URL goes, sidecars included: half
		// an entry from each tree would let one coding answer for bytes the
		// other holds.
		if !shadowed[entry.url] {
			kept = append(kept, entry)
		}
	}
	kept = append(kept, external...)
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].url != kept[j].url {
			return kept[i].url < kept[j].url
		}
		if kept[i].preference != kept[j].preference {
			return kept[i].preference < kept[j].preference
		}
		return kept[i].encoding < kept[j].encoding
	})
	return kept, nil
}

func derivedMediaType(url string, content []byte) string {
	if mediaType := mime.TypeByExtension(path.Ext(url)); mediaType != "" {
		return mediaType
	}
	return detectContentType(content)
}
