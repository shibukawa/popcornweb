package pwcli

import (
	"fmt"
	"path"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// assetsConfig is the asset half of the project configuration: what the build
// converts, beside the Tailwind settings that were the only asset keys before.
type assetsConfig struct {
	Public  string
	Derived string
	Embed   string
	// CSSMinify rewrites stylesheets in place. The URL does not change, so no
	// template is touched and no reference has to follow it.
	CSSMinify bool
	// Images converts a referenced png or jpeg. Only an img src is matched;
	// nothing else naming an image is rewritten.
	Images bool
	// ImageQuality is the lossy setting a jpeg source is re-encoded at. A png
	// stays on the lossless axis and ignores it.
	ImageQuality int
	// AVIF adds an avif representation of every converted image, served from
	// the same URL and chosen from Accept. It is separate from Images because
	// it costs a second encode of every image and wins less often than the webp
	// conversion does.
	AVIF bool
	// Scripts builds a referenced TypeScript entry point, and minifies an
	// authored javascript file in place. The second one moves no URL, so it
	// belongs to the tree walk exactly as the stylesheet minify does.
	Scripts bool
	// SourceMaps keeps the map the script build emits, and the comment naming
	// it, in the served tree. It is a property of the build invocation rather
	// than of the project: pw dev sets it, and pw build sets it only when asked
	// for a debug artifact, because a map carries the authored TypeScript and a
	// deployment has no use for shipping its own sources.
	SourceMaps bool
	// Verify refuses an authored public file whose bytes contradict the type
	// its extension declares. It reads bytes the tree walk already holds, so
	// it is on unless a project turns it off.
	Verify bool
	// VerifySVG is the best-effort scan for an SVG that carries something
	// executable. It is separate from Verify because the two have different
	// confidence: the first is decidable from bytes, and this one only names
	// the obvious cases behind a response header that does not need it.
	VerifySVG bool
	// VerifyAllow exempts paths from both checks. A file kept on purpose is
	// kept for reasons neither check can tell apart, so one list covers both.
	VerifyAllow []string
}

// transformAuthoredFile is the tree-walk half of the pipeline: work that needs
// no template reference because the URL does not move. The generation hooks
// handle the other half, where a rewritten attribute has to follow the file.
func transformAuthoredFile(relative string, source []byte, assets assetsConfig, rewrites map[string]string) ([]byte, error) {
	switch strings.ToLower(path.Ext(relative)) {
	case ".css":
		// The URL rewrite runs first: minified output is harder to scan and
		// gains nothing from being scanned instead.
		content := rewriteStylesheetURLs(string(source), relative, rewrites)
		if !assets.CSSMinify {
			return []byte(content), nil
		}
		minified, err := minifyStylesheet(content, relative)
		if err != nil {
			return nil, err
		}
		return []byte(minified), nil
	case ".js", ".mjs":
		if !assets.Scripts {
			return source, nil
		}
		minified, err := minifyScript(string(source), relative)
		if err != nil {
			return nil, err
		}
		return []byte(minified), nil
	}
	return source, nil
}

// rewriteStylesheetURLs points every static url() at whatever its target became.
//
// This is the only way a background-image can follow a conversion: the upstream
// reference hook rewrites template attributes and excludes stylesheets by its
// own non-goals, and these bytes are this project's to transform anyway.
func rewriteStylesheetURLs(source, relative string, rewrites map[string]string) string {
	if len(rewrites) == 0 {
		return source
	}
	var out strings.Builder
	directory := path.Dir(relative)
	rest := source
	for {
		start := strings.Index(rest, "url(")
		if start < 0 {
			out.WriteString(rest)
			return out.String()
		}
		end := closingParen(rest[start+4:])
		if end < 0 {
			out.WriteString(rest)
			return out.String()
		}
		end += start + 4
		out.WriteString(rest[:start+4])
		raw := rest[start+4 : end]
		out.WriteString(rewriteStylesheetURL(raw, directory, rewrites))
		out.WriteString(")")
		rest = rest[end+1:]
	}
}

// closingParen finds the parenthesis that ends a url() token, skipping the ones
// inside a quoted value.
//
// Taking the first ")" instead would cut url("a(b).png") in half and emit a
// stylesheet that no longer parses, which is the worst kind of failure here:
// the build succeeds and the page loses its styling.
func closingParen(value string) int {
	quote := byte(0)
	for i := 0; i < len(value); i++ {
		character := value[i]
		switch {
		case quote != 0:
			if character == '\\' {
				i++
				continue
			}
			if character == quote {
				quote = 0
			}
		case character == '"' || character == '\'':
			quote = character
		case character == ')':
			return i
		}
	}
	return -1
}

func rewriteStylesheetURL(raw, directory string, rewrites map[string]string) string {
	trimmed := strings.TrimSpace(raw)
	quote := ""
	if len(trimmed) >= 2 && (trimmed[0] == '"' || trimmed[0] == '\'') && trimmed[len(trimmed)-1] == trimmed[0] {
		quote = string(trimmed[0])
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	target, ok := rewriteTarget(trimmed, directory, rewrites)
	if !ok {
		return raw
	}
	return quote + target + quote
}

// rewriteTarget resolves one reference and reports its replacement.
//
// A relative URL resolves against the stylesheet's own directory, never against
// the page that loads it. An absolute one is matched by suffix instead, because
// the mount prefix belongs to the runtime configuration and a build that
// guessed it would rewrite the wrong thing on a project that moved it.
func rewriteTarget(value, directory string, rewrites map[string]string) (string, bool) {
	if value == "" || strings.HasPrefix(value, "data:") || strings.HasPrefix(value, "#") {
		return "", false
	}
	if strings.Contains(value, "://") || strings.HasPrefix(value, "//") {
		return "", false
	}
	reference, suffix := splitURLSuffix(value)
	if strings.HasPrefix(reference, "/") {
		var matched, replacement string
		for source, target := range rewrites {
			if !strings.HasSuffix(reference, "/"+source) {
				continue
			}
			if matched != "" {
				// Two sources matching one absolute reference means the build
				// cannot tell which file it names, and guessing would rewrite
				// the wrong one.
				return "", false
			}
			matched, replacement = source, target
		}
		if matched == "" {
			return "", false
		}
		return strings.TrimSuffix(reference, matched) + replacement + suffix, true
	}
	resolved := path.Clean(path.Join(directory, reference))
	target, ok := rewrites[resolved]
	if !ok {
		return "", false
	}
	relative, err := relativeURL(directory, target)
	if err != nil {
		return "", false
	}
	return relative + suffix, true
}

// splitURLSuffix keeps a query or fragment out of the path comparison and puts
// it back on the replacement.
func splitURLSuffix(value string) (string, string) {
	if index := strings.IndexAny(value, "?#"); index >= 0 {
		return value[:index], value[index:]
	}
	return value, ""
}

// relativeURL expresses a tree path from the directory that will reference it,
// so a stylesheet keeps writing the same shape of URL it was written with.
func relativeURL(directory, target string) (string, error) {
	if directory == "." || directory == "" {
		return target, nil
	}
	prefix := directory + "/"
	if strings.HasPrefix(target, prefix) {
		return strings.TrimPrefix(target, prefix), nil
	}
	up := strings.Count(directory, "/") + 1
	return strings.Repeat("../", up) + target, nil
}

// minifyScript minifies an authored javascript file without bundling it.
//
// Nothing is resolved and nothing is inlined: the file keeps its own imports,
// so a module stays a module and a classic script stays classic. Identifiers
// are renamed only inside function scopes, which is what a transform without a
// bundle can do safely.
func minifyScript(source, name string) (string, error) {
	result := api.Transform(source, api.TransformOptions{
		Loader:            api.LoaderJS,
		Sourcefile:        name,
		MinifyWhitespace:  true,
		MinifySyntax:      true,
		MinifyIdentifiers: true,
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("public assets: minify %s: %s", name, result.Errors[0].Text)
	}
	return string(result.Code), nil
}

// minifyStylesheet runs the transform in isolation: no bundling, no import
// following, and no url() rewriting of its own, because this pass already did
// the only rewriting the project wants.
func minifyStylesheet(source, name string) (string, error) {
	result := api.Transform(source, api.TransformOptions{
		Loader:            api.LoaderCSS,
		Sourcefile:        name,
		MinifyWhitespace:  true,
		MinifySyntax:      true,
		MinifyIdentifiers: false,
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("public assets: minify %s: %s", name, result.Errors[0].Text)
	}
	return string(result.Code), nil
}
