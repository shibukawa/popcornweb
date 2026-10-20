package middlewares

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// PreparePublicAssets gives both transports the same filesystem selection,
// configuration validation, and development startup diagnostic.
func PreparePublicAssets(config PublicAssetConfig, embedded fs.FS) (PublicAssetConfig, fs.FS, error) {
	if config.LocalRoot == "" {
		config.LocalRoot = localPublicRoot
	}
	if err := validatePublicLocalRoot(config.LocalRoot, publicDevelopment || config.ReadLocal); err != nil {
		return config, nil, err
	}
	if embedded == nil {
		embedded = registeredPublicFS()
	}
	if embedded == nil && !publicDevelopment {
		return config, nil, fmt.Errorf("popcornweb: server.public.enabled requires a registered public filesystem")
	}
	if publicDevelopment {
		info, err := os.Stat(filepath.FromSlash(config.LocalRoot))
		if err != nil || !info.IsDir() {
			message := "public derived tree is unavailable; using embedded assets, which may be stale"
			if embedded == nil {
				message = "public mount has no usable tree; public asset requests will return 404"
			}
			slog.Warn(message, "local_root", config.LocalRoot, "mount", config.Mount)
		}
	}
	return config, embedded, nil
}

func validatePublicLocalRoot(root string, checkDisk bool) error {
	if !filepath.IsLocal(root) || root == "." || strings.Contains(root, "\\") || filepath.ToSlash(filepath.Clean(root)) != root {
		return fmt.Errorf("server.public.local_root must be a canonical working-directory-relative path")
	}
	if !checkDisk {
		return nil
	}
	current := ""
	for _, part := range strings.Split(root, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("server.public.local_root: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("server.public.local_root: symbolic links are not allowed")
		}
	}
	return nil
}
