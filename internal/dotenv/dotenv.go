// Package dotenv reads the .env files a project keeps beside its configuration,
// per policy:dotenv-resolution: .env, .env.local, .env.{env}, .env.{env}.local,
// then the secret directory a container runtime mounts, layered in that order
// under the process environment.
//
// The read itself is configbind's, through LoadOptions.EnvFiles in that order
// with the .local files marked Secret, and EnvSecretDirs for the mount, which
// is what lets the startup summary and pw doctor name the file a value came
// from and mask a value a secret source supplied. What stays here is the
// part configbind is not told: which files a token selects, where the token
// itself comes from, and the warning for an APP_ENV written into a file
// APP_ENV chose.
package dotenv

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/go-envparse"
	"github.com/shibukawa/popcornweb/internal/pwenv"
	"github.com/shibukawa/tinybind-go/configbind"
)

// Entry is one assignment in a dotenv file.
type Entry struct {
	Name  string
	Value string
}

// File is one dotenv file that exists and parsed.
type File struct {
	// Name is the file's name relative to the directory it was read from,
	// which is how a summary and a report refer to it.
	Name string
	// Path is where it was read from, which is what the load is handed.
	Path    string
	Entries []Entry
	// Local marks the .local member of a pair: ignored by git, and read as a
	// secret source, so every value in it is masked wherever it is shown.
	Local bool
}

// Lines renders the entries as KEY=value lines, the shape os.Environ uses.
func (f File) Lines() []string {
	lines := make([]string, 0, len(f.Entries))
	for _, entry := range f.Entries {
		lines = append(lines, entry.Name+"="+entry.Value)
	}
	return lines
}

// Layer is the dotenv files read for one token, in read order, and the secret
// directories the load is asked to read after them.
type Layer struct {
	Files []File
	// SecretDirs are the directories handed to LoadOptions.EnvSecretDirs, only
	// the ones that exist. Their files are read by the load, not here; Environ
	// reads them again for the framework's own environment-carried arrays.
	SecretDirs []string
	// Warnings are what the read noticed and went on from: today, an APP_ENV
	// in the token's own files, which cannot change the token that selected
	// them.
	Warnings []string
}

// Names lists what was read, in order: the files by name, then the secret
// directories with a trailing separator so a summary reads them as such.
func (l Layer) Names() []string {
	names := make([]string, 0, len(l.Files)+len(l.SecretDirs))
	for _, file := range l.Files {
		names = append(names, file.Name)
	}
	for _, dir := range l.SecretDirs {
		names = append(names, dir+string(os.PathSeparator))
	}
	return names
}

// envFiles is the layer as the load takes it: every file in order, the local
// ones marked secret by origin.
func (l Layer) envFiles() []configbind.EnvFile {
	files := make([]configbind.EnvFile, 0, len(l.Files))
	for _, file := range l.Files {
		files = append(files, configbind.EnvFile{Path: file.Path, Secret: file.Local})
	}
	return files
}

// Environ composes the environment the way the load does: the files in
// order, then the secret directories, then the process environment, so a later
// source wins over an earlier one and an exported variable wins over every
// file. The framework's own environment-carried arrays read this, since
// configbind keeps its composed environment to itself.
func (l Layer) Environ(process []string) []string {
	var lines []string
	for _, file := range l.Files {
		lines = append(lines, file.Lines()...)
	}
	for _, dir := range l.SecretDirs {
		lines = append(lines, readSecretDir(dir)...)
	}
	return append(lines, process...)
}

// readSecretDir reads a secret mount the way configbind does: each regular
// file is one variable, dot-prefixed names and directories are skipped,
// symlinks are followed, and trailing line endings are stripped. Errors are
// left to the load, which reports them; this read only feeds Environ.
func readSecretDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var lines []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines = append(lines, name+"="+strings.TrimRight(string(data), "\r\n"))
	}
	slices.Sort(lines)
	return lines
}

// Read parses the dotenv file at path, naming it name in errors and results.
// An absent file is (nil, nil): nothing was read and nothing is wrong. A file
// that exists and cannot be read is an error, because a permission problem on
// a secrets file is never a fallback case.
func Read(path, name string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("popcornweb: read %s: %w", name, err)
	}
	entries, err := Parse(name, data)
	if err != nil {
		return nil, err
	}
	return &File{Name: name, Path: path, Entries: entries}, nil
}

// Parse reads dotenv source through system:go-envparse, the parser configbind
// reads the same files with: NAME=value with an optional export prefix, #
// comments, and unquoted, single-quoted, and double-quoted text, the last with
// JSON escapes. A later assignment of the same name wins, as it does between
// files. There is no ${NAME} expansion; the ${…} form belongs to the TOML
// layer. Entries come back in name order, so a file reads the same whatever
// order it was written in.
func Parse(name string, data []byte) ([]Entry, error) {
	values, err := envparse.Parse(bytes.NewReader(data))
	if err != nil {
		var parseErr *envparse.ParseError
		if errors.As(err, &parseErr) && parseErr.Line > 0 {
			return nil, fmt.Errorf("popcornweb: %s:%d: %w", name, parseErr.Line, parseErr.Err)
		}
		return nil, fmt.Errorf("popcornweb: %s: %w", name, err)
	}
	entries := make([]Entry, 0, len(values))
	for key, value := range values {
		entries = append(entries, Entry{Name: key, Value: value})
	}
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}

// readBase reads the shared file and its local override from dir, which is
// what the token is resolved from.
func readBase(dir string) ([]File, error) {
	var files []File
	for _, name := range []string{pwenv.DotenvBase, pwenv.DotenvBase + pwenv.DotenvLocalSuffix} {
		file, err := Read(filepath.Join(dir, name), name)
		if err != nil {
			return nil, err
		}
		if file != nil {
			file.Local = name != pwenv.DotenvBase
			files = append(files, *file)
		}
	}
	return files, nil
}

// ReadLayer reads the four files of env from dir in the order the load layers
// them, and notes which of dirs exist to be read as secret mounts. The token
// is the caller's: this is what pw doctor uses, where the environment to
// inspect is an option rather than the process's own.
func ReadLayer(dir, env string, secretDirs []string) (Layer, error) {
	var layer Layer
	tokenFile := pwenv.DotenvFileName(env)
	for _, name := range pwenv.DotenvFileNames(env) {
		file, err := Read(filepath.Join(dir, name), name)
		if err != nil {
			return Layer{}, err
		}
		if file == nil {
			continue
		}
		file.Local = strings.HasSuffix(name, pwenv.DotenvLocalSuffix)
		if !strings.HasPrefix(name, tokenFile) {
			layer.Files = append(layer.Files, *file)
			continue
		}
		// The load reads the file itself and binds no APP_ENV, so the drop
		// here only keeps Environ honest; the warning is the point.
		kept := file.Entries[:0:0]
		for _, entry := range file.Entries {
			if entry.Name == pwenv.Var {
				layer.Warnings = append(layer.Warnings, fmt.Sprintf(
					"%s: %s is ignored here, because this file was selected by it", name, pwenv.Var))
				continue
			}
			kept = append(kept, entry)
		}
		file.Entries = kept
		layer.Files = append(layer.Files, *file)
	}
	for _, secretDir := range secretDirs {
		if info, err := os.Stat(secretDir); err == nil && info.IsDir() {
			layer.SecretDirs = append(layer.SecretDirs, secretDir)
		}
	}
	return layer, nil
}

// Resolve reads the layer for the process itself: the token comes from the
// process environment, then from .env and .env.local when the process did not
// set it, and that token selects the second pair. It reports the token and
// whether anything declared it, on the terms of pwenv.ResolveDeclared.
func Resolve(dir string, process []string, secretDirs []string) (Layer, string, bool, error) {
	// An empty APP_ENV in the process is "unset", not an override of the
	// file, which is why the process is asked first and the files only when
	// the process declared nothing.
	env, declared, err := pwenv.ResolveDeclared(process)
	if err != nil {
		return Layer{}, "", false, err
	}
	if !declared {
		base, err := readBase(dir)
		if err != nil {
			return Layer{}, "", false, err
		}
		if env, declared, err = pwenv.ResolveDeclared(Layer{Files: base}.Environ(nil)); err != nil {
			return Layer{}, "", false, err
		}
	}
	layer, err := ReadLayer(dir, env, secretDirs)
	if err != nil {
		return Layer{}, "", false, err
	}
	return layer, env, declared, nil
}

// Load runs the configuration load with the layer as its dotenv input: the
// files in order as EnvFiles, the .local ones marked Secret, and the mounts as
// EnvSecretDirs, so a value from either secret source is masked by origin. The files are handed to configbind by path and read back as places
// by name, so a summary says ".env.stg.local" whatever directory the load ran
// against; the secret flag survives the rename.
func Load(options configbind.LoadOptions, layer Layer, process []string) (*configbind.LoadResult, error) {
	options.EnvFiles = layer.envFiles()
	options.EnvSecretDirs = layer.SecretDirs
	options.Environ = process
	if options.Environ == nil {
		options.Environ = []string{}
	}
	result, err := configbind.Load(options)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(layer.Files))
	for _, file := range layer.Files {
		names[file.Path] = file.Name
	}
	for _, key := range result.Overlay.Keys() {
		entry, ok := result.Overlay.Get(key)
		if !ok {
			continue
		}
		path, ok := configbind.EnvFileOf(entry.Place)
		if !ok {
			continue
		}
		name, known := names[path]
		if !known || name == path {
			continue
		}
		place := configbind.PlaceEnvFile + configbind.Place(name)
		if entry.IsMulti {
			result.Overlay.SetMulti(key, entry.Multi, place)
		} else {
			result.Overlay.Set(key, entry.Raw, place)
		}
		if entry.Secret {
			result.Overlay.MarkSecret(key)
		}
	}
	for index, file := range result.EnvFiles {
		if name, known := names[file.Path]; known {
			result.EnvFiles[index].Path = name
		}
	}
	return result, nil
}
