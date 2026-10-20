package pwcli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/shibukawa/popcornweb/middlewares"
)

// assetManifestFile is the generated registration the application links. It is
// Go rather than a data file the runtime parses: the table is known at build
// time, and a startup parse would cost every process what one build already
// knows, including a TinyGo target that has no reason to carry a decoder.
const assetManifestFile = "public_manifest_pw_gen.go"

// assetManifestJSON is the same data for tooling. Nothing reads it at runtime;
// it exists so pw doctor and a developer can see what the build decided.
const assetManifestJSON = "dist/manifest.json"

type manifestRepresentationJSON struct {
	Path            string `json:"path"`
	MediaType       string `json:"media_type"`
	ContentEncoding string `json:"content_encoding,omitempty"`
	Length          int    `json:"length"`
	ETag            string `json:"etag"`
	Preference      int    `json:"preference,omitempty"`
	// External names bytes read from the second authored tree at request time.
	// Length and ETag stay zero for one of these, because that tree is deployed
	// as its own artifact and a validator taken here could outlive its bytes.
	External bool `json:"external,omitempty"`
}

type manifestEntryJSON struct {
	URL          string `json:"url"`
	CacheControl string `json:"cache_control"`
	// Revision is the segment that makes this URL immutable, empty for a URL
	// that needs none or may not have one. It is written out for the reason the
	// rest of this file is: pw doctor and a developer can see which assets a
	// deployment can actually cache, without reading the generated Go.
	Revision        string                       `json:"revision,omitempty"`
	Representations []manifestRepresentationJSON `json:"representations"`
}

// writeAssetManifest emits both forms from one grouping, so the table the
// application links and the file a person reads can never disagree.
func writeAssetManifest(root string, assets []derivedAsset, config assetsConfig) error {
	l := config.locations()
	grouped := groupByURL(assets)
	packageName, err := publicPackageName(root, config)
	if err != nil {
		return err
	}
	source, err := renderAssetManifest(packageName, grouped)
	if err != nil {
		return err
	}
	if err := writeScaffoldFile(filepath.Join(root, filepath.FromSlash(l.manifestGo)), source); err != nil {
		return fmt.Errorf("public assets: write %s: %w", assetManifestFile, err)
	}
	encoded, err := json.MarshalIndent(grouped, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(root, filepath.FromSlash(l.manifest))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return writeScaffoldFile(target, append(encoded, '\n'))
}

// groupByURL folds every representation of one URL into one entry. The order is
// the walk's, which is sorted, so two builds of the same tree emit the same
// bytes.
func groupByURL(assets []derivedAsset) []manifestEntryJSON {
	var entries []manifestEntryJSON
	index := map[string]int{}
	for _, asset := range assets {
		position, found := index[asset.url]
		if !found {
			entries = append(entries, manifestEntryJSON{URL: asset.url, CacheControl: cacheControlFor(asset)})
			position = len(entries) - 1
			index[asset.url] = position
		}
		entries[position].Representations = append(entries[position].Representations, manifestRepresentationJSON{
			Path:            asset.path,
			MediaType:       asset.mediaType,
			ContentEncoding: asset.encoding,
			Length:          asset.length,
			ETag:            asset.etag,
			Preference:      asset.preference,
			External:        asset.external,
		})
	}
	// The revision is decided after grouping because it covers the URL rather
	// than any one of its forms: a page served the webp and a page served the
	// avif are holding one URL, and a change to either has to move it.
	for index := range entries {
		entries[index].Revision = revisionFor(entries[index])
	}
	return entries
}

// revisionFor digests every representation of one URL, so a change to any of
// them changes the URL and nothing can hold the old bytes under the new name.
//
// Three kinds of URL get none, each for its own reason. A name the build
// invented already carries its digest, so a segment would be a second copy of
// the same statement, and a longer path saying it. An external file ships as
// its own artifact, and the build never read its bytes to have an opinion about
// them. And an entry with no representation has nothing to digest.
func revisionFor(entry manifestEntryJSON) string {
	if entry.CacheControl == immutableCacheControl || len(entry.Representations) == 0 {
		return ""
	}
	digest := sha256.New()
	for _, representation := range entry.Representations {
		if representation.External {
			return ""
		}
		// The separators keep the digest a function of the set rather than of
		// the concatenation, so no rename can collide with a content change.
		digest.Write([]byte(representation.Path))
		digest.Write([]byte{0})
		digest.Write([]byte(representation.ETag))
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))[:middlewares.RevisionLength]
}

// The two cache policies, one per kind of name.
const (
	// derivedCacheControl revalidates. A file that kept the name its author
	// wrote can serve different bytes after a rebuild, so the strong validator
	// does the work and an unchanged asset costs a 304 with no body.
	derivedCacheControl = "public, no-cache"
	// immutableCacheControl is only honest for a name carrying the digest of
	// its own bytes: different bytes are a different URL, so the response can
	// promise never to change and be believed.
	immutableCacheControl = "public, max-age=31536000, immutable"
)

func cacheControlFor(asset derivedAsset) string {
	if asset.immutable {
		return immutableCacheControl
	}
	return derivedCacheControl
}

func renderAssetManifest(packageName string, entries []manifestEntryJSON) ([]byte, error) {
	var out bytes.Buffer
	fmt.Fprintf(&out, "// Code generated by pw build. DO NOT EDIT.\n\npackage %s\n\n", packageName)
	out.WriteString("import \"github.com/shibukawa/popcornweb/middlewares\"\n\n")
	out.WriteString("// The build knows every URL, every representation, and every validator, so\n")
	out.WriteString("// the request path reads bytes and computes nothing.\n")
	out.WriteString("func init() {\n\tmiddlewares.RegisterPublicManifest([]middlewares.AssetEntry{\n")
	for _, entry := range entries {
		fmt.Fprintf(&out, "\t\t{URL: %s, CacheControl: %s, Revision: %s, Representations: []middlewares.AssetRepresentation{\n",
			strconv.Quote(entry.URL), strconv.Quote(entry.CacheControl), strconv.Quote(entry.Revision))
		for _, representation := range entry.Representations {
			fmt.Fprintf(&out, "\t\t\t{Path: %s, MediaType: %s, ContentEncoding: %s, Length: %d, ETag: %s, Preference: %d, External: %t},\n",
				strconv.Quote(representation.Path), strconv.Quote(representation.MediaType),
				strconv.Quote(representation.ContentEncoding), representation.Length,
				strconv.Quote(representation.ETag), representation.Preference, representation.External)
		}
		out.WriteString("\t\t}},\n")
	}
	out.WriteString("\t})\n}\n")
	return out.Bytes(), nil
}

// publicPackageName reads the package the scaffolded embed lives in, so the
// generated registration compiles beside it whatever the project named it.
func publicPackageName(root string, config assetsConfig) (string, error) {
	name := filepath.Join(root, filepath.FromSlash(config.locations().embed))
	file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.PackageClauseOnly)
	if err != nil {
		return "", fmt.Errorf("public assets: read package clause of %s: %w", config.locations().embed, err)
	}
	return file.Name.Name, nil
}

func detectContentType(content []byte) string {
	return http.DetectContentType(content)
}
