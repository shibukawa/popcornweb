package pwcli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/shibukawa/tinybind-go/templates/htmlbind"
)

// encoderVersion names what a conversion's bytes depend on beyond its source.
//
// It joins every cache key. An encoder upgrade that no key mentions serves
// stale bytes from a cache that believes it is current, and this is the only
// place that can say so.
const scriptEncoderVersion = "esbuild/0.28.2"

// assetReferenceHooks are the generation-time conversions this project
// registers. Each one matches exactly one element and attribute pair: an image
// conversion reaches an img src and nothing else naming an image, because
// rewriting a meta tag or a link icon would move a URL whose element the build
// has no reason to touch.
func assetReferenceHooks(root string, assets assetsConfig) []htmlbind.ReferenceHook {
	var hooks []htmlbind.ReferenceHook
	if assets.Images {
		hooks = append(hooks, imageReferenceHook(root, assets))
	}
	if assets.Scripts {
		hooks = append(hooks, scriptReferenceHook(root, assets))
	}
	return hooks
}

// conversionWorkers converts ahead of the compile when there is more than one
// core to do it on. It changes wall clock and nothing else, which is why it is
// derived from the machine rather than from configuration: a build that hashed
// it would stamp the runner into the output.
func conversionWorkers() int {
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	return workers
}

// imageReferenceHook converts a referenced png or jpeg to webp and points the
// attribute at the result.
//
// The axis follows the source: a png is already exact, so leaving the lossless
// axis would be a judgment about its content that no build rule can make, while
// a photograph has nothing to gain from lossless and a great deal to lose.
func imageReferenceHook(root string, assets assetsConfig) htmlbind.ReferenceHook {
	return htmlbind.ReferenceHook{
		Name:      "image-webp",
		Element:   "img",
		Attribute: "src",
		Match:     func(value string) bool { return localReference(value) && convertibleImage(value) },
		CacheKey: func(request htmlbind.ReferenceRequest) (htmlbind.ConversionInputs, error) {
			source, err := authoredAssetPath(root, request.Value, assets)
			if err != nil {
				return htmlbind.ConversionInputs{}, err
			}
			return htmlbind.ConversionInputs{
				Sources: []string{source},
				Params: fmt.Sprintf("webp q=%d %s", assets.ImageQuality,
					imageEncoderParams("webp", losslessSource(source))),
			}, nil
		},
		Transform: func(request htmlbind.ReferenceRequest) (htmlbind.ReferenceResult, error) {
			return convertImageReference(root, request.Value, assets, encodeWebP)
		},
	}
}

// imageEncoder is the seam the size comparison is written against: what the
// bytes are worth is decided here, and which encoder produced them is not this
// function's business.
type imageEncoder func(source string, lossless bool, quality int) ([]byte, error)

func convertImageReference(root, reference string, assets assetsConfig, encode imageEncoder) (htmlbind.ReferenceResult, error) {
	source, err := authoredAssetPath(root, reference, assets)
	if err != nil {
		return htmlbind.ReferenceResult{}, err
	}
	original, err := os.ReadFile(source)
	if err != nil {
		return htmlbind.ReferenceResult{}, fmt.Errorf("image %s: %w", reference, err)
	}
	encoded, err := encode(source, losslessSource(source), assets.ImageQuality)
	if errors.Is(err, errNoEncoder) {
		// An unconverted image is a larger image, not a broken page, so a
		// machine with no encoder declines rather than failing the build. The
		// missing tool is a pw doctor finding, where it can be acted on.
		return htmlbind.ReferenceResult{
			Skip:   true,
			Reason: "no webp encoder is installed",
		}, nil
	}
	if err != nil {
		return htmlbind.ReferenceResult{}, fmt.Errorf("image %s: %w", reference, err)
	}
	// A derived file larger than its source is worth declining, and only the
	// converted bytes can say so. The outcome is cached, so a losing encode
	// runs once and never again.
	if len(encoded) >= len(original) {
		return htmlbind.ReferenceResult{
			Skip:   true,
			Reason: "webp was larger than the source",
		}, nil
	}
	name := hashedName(replaceExtension(assetTreePath(reference), ".webp"), encoded)
	return htmlbind.ReferenceResult{
		Value: referenceSibling(reference, path.Base(name)),
		Files: []htmlbind.ProducedFile{{
			Name:      name,
			MediaType: "image/webp",
			Content:   encoded,
		}},
	}, nil
}

// contentHashLength is how much of the digest a name carries. Twelve hex
// characters is 48 bits: enough that a collision is not a thing that happens to
// a project, short enough that the name still reads as the file it came from.
const contentHashLength = 12

// hashedName puts the digest of the emitted bytes into the file name, which is
// what makes the URL immutable rather than merely long-lived.
//
// Only a URL the build invented is named this way. A URL the author wrote —
// a stylesheet link, an authored script — is left exactly as written, because
// nothing rewrites those references and a renamed file would simply be gone.
func hashedName(name string, content []byte) string {
	digest := sha256.Sum256(content)
	extension := path.Ext(name)
	return strings.TrimSuffix(name, extension) + "." +
		hex.EncodeToString(digest[:])[:contentHashLength] + extension
}

// losslessSource reports whether the authored bytes are exact, which is what
// decides the axis: a png stays lossless and a jpeg has already lost what a
// lossless pass would preserve.
func losslessSource(source string) bool {
	return strings.EqualFold(path.Ext(source), ".png")
}

// scriptReferenceHook builds a referenced TypeScript entry point.
//
// It is the case that needs everything the image case did not: the output name
// replaces rather than appends, the read set is wider than the reference
// because an entry imports other files, the outputs outnumber the rewrite
// because a source map is written that no attribute names, and a stylesheet a
// module imported has to be loaded by a tag the author never wrote.
func scriptReferenceHook(root string, configs ...assetsConfig) htmlbind.ReferenceHook {
	var assets assetsConfig
	if len(configs) > 0 {
		assets = configs[0]
	}
	return htmlbind.ReferenceHook{
		Name:      "script-build",
		Element:   "script",
		Attribute: "src",
		Match:     func(value string) bool { return localReference(value) && buildableEntry(value) },
		CacheKey: func(request htmlbind.ReferenceRequest) (htmlbind.ConversionInputs, error) {
			source, err := authoredAssetPath(root, request.Value, assets)
			if err != nil {
				return htmlbind.ConversionInputs{}, err
			}
			return htmlbind.ConversionInputs{
				Sources: []string{source},
				Params:  scriptEncoderVersion,
			}, nil
		},
		Transform: func(request htmlbind.ReferenceRequest) (htmlbind.ReferenceResult, error) {
			return buildScriptEntry(root, request.Value, assets)
		},
	}
}

func buildScriptEntry(root, reference string, configs ...assetsConfig) (htmlbind.ReferenceResult, error) {
	var assets assetsConfig
	if len(configs) > 0 {
		assets = configs[0]
	}
	source, err := authoredAssetPath(root, reference, assets)
	if err != nil {
		return htmlbind.ReferenceResult{}, err
	}
	treePath := assetTreePath(reference)
	outputBase := replaceExtension(treePath, ".js")
	result := api.Build(api.BuildOptions{
		EntryPoints: []string{source},
		// A linked map, so a stack trace in production names the authored
		// TypeScript rather than one line of minified output. The map is a
		// declared artifact like any other file the build writes, and it is
		// served from the same immutable URL space as the bundle.
		Sourcemap:         api.SourceMapLinked,
		Bundle:            true,
		Write:             false,
		MinifyWhitespace:  true,
		MinifySyntax:      true,
		MinifyIdentifiers: true,
		// A module, because that is what a browser entry point is today: it is
		// the idiom the authored source is written in, it gives top-level
		// await, and it needs no wrapper.
		//
		// It requires type=module on the tag, which this transform cannot see
		// and must not read: the seam converts one distinct value once and
		// replays it for every occurrence, so an output decided from a template
		// position would serve one page's answer to another. The tag is checked
		// by verifyScriptModuleTags instead, which reads every template at once
		// and is not memoized against anything.
		Format:        api.FormatESModule,
		Target:        api.ES2020,
		Outfile:       outputBase,
		AbsWorkingDir: mustAbs(root),
		Metafile:      true,
	})
	if len(result.Errors) > 0 {
		return htmlbind.ReferenceResult{}, fmt.Errorf("script %s: %s", reference, result.Errors[0].Text)
	}
	produced, head, err := scriptOutputs(result, treePath, reference)
	if err != nil {
		return htmlbind.ReferenceResult{}, err
	}
	return htmlbind.ReferenceResult{
		Value: referenceSibling(reference, path.Base(produced[0].Name)),
		Files: produced,
		Head:  head,
		Read:  scriptReadSet(result, root),
	}, nil
}

// scriptOutputs sorts what the build emitted into the file that replaces the
// reference, the files nothing names, and the head entries that load them.
//
// A stylesheet is the reason head entries exist at all: a CSS module imported
// by an entry point becomes a companion file, and no rewritten src can
// introduce its link.
func scriptOutputs(result api.BuildResult, treePath, reference string) ([]htmlbind.ProducedFile, []htmlbind.HeadEntry, error) {
	directory := path.Dir(treePath)
	// The bundle names its map in a trailing comment, so the two names are one
	// decision: the digest is taken over the bundle without that comment, and
	// the comment is written back naming the map the digest produced. Hashing
	// the commented bytes would need the name the comment carries, which is
	// what the digest is being taken to decide.
	var bundle, stylesheet, sourcemap *api.OutputFile
	for index := range result.OutputFiles {
		file := &result.OutputFiles[index]
		switch path.Ext(filepath.ToSlash(file.Path)) {
		case ".js":
			bundle = file
		case ".css":
			stylesheet = file
		case ".map":
			sourcemap = file
		default:
			return nil, nil, fmt.Errorf("script %s: unexpected output %s", reference, file.Path)
		}
	}
	if bundle == nil {
		return nil, nil, fmt.Errorf("script %s: the build produced no bundle", reference)
	}
	body, comment := splitSourceMapComment(string(bundle.Contents))
	base := hashedName(path.Join(directory, path.Base(replaceExtension(treePath, ".js"))), []byte(body))
	produced := []htmlbind.ProducedFile{}
	if sourcemap != nil && comment != "" {
		produced = append(produced, htmlbind.ProducedFile{
			Name:      base + ".map",
			MediaType: "application/json",
			Content:   sourcemap.Contents,
		})
		body += "//# sourceMappingURL=" + path.Base(base) + ".map\n"
	}
	produced = append([]htmlbind.ProducedFile{{
		Name:      base,
		MediaType: "text/javascript; charset=utf-8",
		Content:   []byte(body),
	}}, produced...)

	var head []htmlbind.HeadEntry
	if stylesheet != nil {
		// A css module imported by the entry becomes a file no attribute names,
		// so the conversion declares the link that loads it.
		companion := hashedName(path.Join(directory, path.Base(replaceExtension(treePath, ".css"))), stylesheet.Contents)
		produced = append(produced, htmlbind.ProducedFile{
			Name:      companion,
			MediaType: "text/css; charset=utf-8",
			Content:   stylesheet.Contents,
		})
		head = append(head, htmlbind.HeadEntry{
			Element: "link",
			Attributes: map[string]string{
				"rel":  "stylesheet",
				"href": referenceSibling(reference, path.Base(companion)),
			},
		})
	}
	return produced, head, nil
}

// splitSourceMapComment separates a bundle from the trailing comment naming its
// map, so the digest covers what the code is and not what it is called.
func splitSourceMapComment(bundle string) (string, string) {
	marker := "//# sourceMappingURL="
	index := strings.LastIndex(bundle, marker)
	if index < 0 {
		return bundle, ""
	}
	return bundle[:index], bundle[index:]
}

// scriptReadSet reports every file the build opened, so editing an imported
// module regenerates even though no template names it.
//
// Only the inputs are taken. The metafile also lists the outputs, and an
// earlier line-scanning version swept those in: a recorded dependency that is
// not a file on disk is unverifiable, and an unverifiable record regenerates,
// so the incremental skip this cache exists for never held.
func scriptReadSet(result api.BuildResult, root string) []string {
	if result.Metafile == "" {
		return nil
	}
	var metafile struct {
		Inputs map[string]struct {
			Bytes int `json:"bytes"`
		} `json:"inputs"`
	}
	if err := json.Unmarshal([]byte(result.Metafile), &metafile); err != nil {
		return nil
	}
	read := make([]string, 0, len(metafile.Inputs))
	for name := range metafile.Inputs {
		read = append(read, filepath.Join(root, filepath.FromSlash(name)))
	}
	// The map iterates in no order and the read set joins a cache key, so it is
	// sorted rather than left to vary between runs.
	sort.Strings(read)
	return read
}

// authoredAssetPath resolves a reference URL to the file it names inside the
// authored tree.
//
// The mount prefix belongs to the runtime configuration, so it is not assumed:
// the trailing path is looked up under public, and a reference that resolves
// nowhere is a generation error rather than a silent skip, because a hook that
// cannot read cannot rewrite honestly.
func authoredAssetPath(root, reference string, configs ...assetsConfig) (string, error) {
	var assets assetsConfig
	if len(configs) > 0 {
		assets = configs[0]
	}
	public := filepath.Join(root, filepath.FromSlash(assets.locations().public))
	tree := assetTreePath(reference)
	if tree == "" {
		return "", fmt.Errorf("reference %q names no file", reference)
	}
	candidate := filepath.Join(public, filepath.FromSlash(tree))
	if !strings.HasPrefix(candidate, public+string(filepath.Separator)) {
		return "", fmt.Errorf("reference %q leaves the public tree", reference)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", err
	}
	if err := validateAssetPath(root, filepath.ToSlash(relative)); err != nil {
		return "", err
	}
	info, err := os.Lstat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("reference %q resolves to no file under public", reference)
	}
	return candidate, nil
}

// assetTreePath strips the mount prefix from a reference by taking everything
// after the first path segment, which is what every reference to a public asset
// looks like whatever the mount is named.
func assetTreePath(reference string) string {
	value := reference
	if index := strings.IndexAny(value, "?#"); index >= 0 {
		value = value[:index]
	}
	if !strings.HasPrefix(value, "/") {
		return path.Clean(value)
	}
	trimmed := strings.TrimPrefix(value, "/")
	_, rest, found := strings.Cut(trimmed, "/")
	if !found {
		return ""
	}
	return path.Clean(rest)
}

// referenceSibling names a file beside a reference, in the same shape the
// reference was written in, so a contributed link works wherever the mount is.
func referenceSibling(reference, name string) string {
	base := reference
	if index := strings.IndexAny(base, "?#"); index >= 0 {
		base = base[:index]
	}
	return path.Join(path.Dir(base), name)
}

// localReference reports whether a value names a file in this project's tree.
//
// A reference to another origin is somebody else's file: the hook must not
// claim it, because claiming it means resolving it under public and failing
// generation over a URL that was always correct. Upstream states the same rule
// for its own asset extraction, where an external URL passes through unchanged.
func localReference(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "data:") {
		return false
	}
	// A scheme is what makes it another origin. The check is on the part before
	// any slash, so a path containing a colon is not mistaken for one.
	if head, _, found := strings.Cut(trimmed, "/"); found && strings.Contains(head, ":") {
		return false
	}
	return !strings.Contains(trimmed, "://")
}

func convertibleImage(value string) bool {
	switch strings.ToLower(path.Ext(assetTreePath(value))) {
	case ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}

func replaceExtension(value, extension string) string {
	base := value
	suffix := ""
	if index := strings.IndexAny(base, "?#"); index >= 0 {
		base, suffix = base[:index], base[index:]
	}
	return strings.TrimSuffix(base, path.Ext(base)) + extension + suffix
}

func mustAbs(root string) string {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return root
	}
	return absolute
}
