package pwcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Asset locations are project-relative. Auxiliary outputs follow the derived
// tree, while generated Go must share the authored embed file's package.
type assetLocations struct {
	public, derived, embed                                string
	stage, cache, manifest, manifestGo, extracted, routes string
}

func (a assetsConfig) locations() assetLocations {
	public, derived, embed := a.Public, a.Derived, a.Embed
	if public == "" {
		public = "public"
	}
	if derived == "" {
		derived = derivedPublicDir
	}
	if embed == "" {
		embed = "public.go"
	}
	dist := filepath.Dir(filepath.FromSlash(derived))
	return assetLocations{
		public: public, derived: derived, embed: embed,
		stage:      filepath.ToSlash(filepath.Join(dist, "derived")),
		cache:      filepath.ToSlash(filepath.Join(dist, "cache")),
		manifest:   filepath.ToSlash(filepath.Join(dist, "manifest.json")),
		routes:     filepath.ToSlash(filepath.Join(dist, "routes.json")),
		manifestGo: filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(embed)), assetManifestFile)),
		extracted:  filepath.ToSlash(filepath.Join(filepath.FromSlash(public), "generated")),
	}
}

// Check every existing path component, even when the final output does not
// exist yet. In particular, RemoveAll must never traverse a symlinked parent.
func validateAssetPath(root, name string) error {
	if !filepath.IsLocal(name) || name == "." || filepath.ToSlash(filepath.Clean(name)) != name || strings.Contains(name, "\\") {
		return fmt.Errorf("%q must be a canonical project-relative path", name)
	}
	current := root
	for _, segment := range strings.Split(name, "/") {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed: %s", name)
		}
	}
	return nil
}

func validateAssetLocations(root string, a assetsConfig) error {
	l := a.locations()
	for _, entry := range []struct{ key, name string }{
		{"assets.public", l.public}, {"assets.derived", l.derived}, {"assets.embed", l.embed},
		{"asset staging", l.stage}, {"asset cache", l.cache},
		{"asset manifest", l.manifest}, {"route manifest", l.routes}, {"asset Go manifest", l.manifestGo}, {"extracted assets", l.extracted},
	} {
		if err := validateAssetPath(root, entry.name); err != nil {
			return fmt.Errorf("%s: %w", entry.key, err)
		}
	}
	if filepath.Ext(l.embed) != ".go" {
		return fmt.Errorf("assets.embed must name a Go source file")
	}
	// These trees are cleared or contain build-only bytes. They cannot overlap
	// an input, one another, or the files describing/embedding the result.
	outputs := []string{l.derived, l.stage, l.cache}
	for i, output := range outputs {
		if pathWithin(output, l.public) || pathWithin(l.public, output) ||
			pathWithin(output, l.embed) || pathWithin(output, l.manifestGo) || pathWithin(output, l.manifest) {
			return fmt.Errorf("asset output %s overlaps an authored input or manifest", output)
		}
		for _, other := range outputs[i+1:] {
			if pathWithin(output, other) || pathWithin(other, output) {
				return fmt.Errorf("asset outputs %s and %s overlap", output, other)
			}
		}
	}
	if pathWithin(l.public, l.embed) || pathWithin(l.public, l.manifestGo) || pathWithin(l.public, l.manifest) {
		return fmt.Errorf("asset embed and manifests must be outside assets.public")
	}
	relative, err := filepath.Rel(filepath.Dir(filepath.FromSlash(l.embed)), filepath.FromSlash(l.derived))
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return fmt.Errorf("assets.derived must be below the directory containing assets.embed so go:embed can include it")
	}
	return nil
}
