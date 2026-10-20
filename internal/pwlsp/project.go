package pwlsp

// The project model requirement:pw-language-server resolves against.
//
// The model is loaded rather than derived: decision:explicit-generation-sources
// makes a directory invisible to a purpose until popcornweb.toml lists it, so
// walking the tree would answer about files api:cli-generate never reads. What
// this file holds is the shape of the answer and the index built from it; the
// loading itself is injected, because the data:project-config reader lives with
// the CLI and a second reader would be a second answer to the same question.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shibukawa/popcornweb/internal/pwgen"
)

// ErrNoProject reports that no popcornweb.toml stands above the path. It is not
// a failure: it selects the parse-only mode, where syntax diagnostics still
// work and every resolved answer reports unavailable rather than guessing.
var ErrNoProject = errors.New("no popcornweb.toml above this path")

// Loader resolves the project owning a path, or reports ErrNoProject.
type Loader func(start string) (*Project, error)

// SourceDir is one directory a generate purpose lists, and the dialects that
// purpose reads there. A directory covers its whole subtree, matching what
// api:cli-generate walks.
type SourceDir struct {
	// Purpose is the popcornweb.toml key, kept for the message a finding
	// against this directory would carry.
	Purpose string
	Dir     string
	Kinds   []Dialect
}

// Project is the loaded model.
type Project struct {
	RouteTable string
	Root       string
	ConfigPath string
	Name       string
	Sources    []SourceDir
	// ConsolePort is where requirement:dev-console listens when api:cli-dev is
	// running. It is carried so a client can build the URL of a pane without
	// asking the developer to configure one; whether anything is listening
	// there is the client's question, not this server's.
	ConsolePort int
	// StorybookEnabled reports whether requirement:template-storybook's pane
	// is switched on for this project, so a client offers the preview only
	// where it exists.
	StorybookEnabled bool
}

// Declaration is one indexed declaration: enough to answer a symbol query and
// to open the file at it, and nothing that would need a resolved type.
type Declaration struct {
	Name string
	// Container is the path the client shows beside the name, relative to the
	// project root and slash-separated, so it reads the same on every platform.
	Container string
	URI       string
	Kind      int
	Range     Range
}

// index is every declaration of every source the purposes list.
//
// It is rebuilt whole rather than patched, because a purpose change can add or
// remove a directory and reconciling that against a patched index costs more
// than the walk it would save. The walk is a parse of the .pw.* sources only,
// which is a small fraction of a project.
type index struct {
	declarations []Declaration
	// graph is the resolved name layer built from the same parse. It is part
	// of the index rather than beside it because the two are one walk and one
	// lifetime: a reload replaces both or neither.
	graph *TypeGraph
	// files records what was indexed, so a reload can report how much of the
	// project the answers cover.
	files int
	// skipped counts sources the walk could not read. They are not an error:
	// a file removed between the walk and the read is ordinary.
	skipped int
}

// projectState is the server's whole resolved half. Every field is replaced
// together on a reload, so a request never sees a project from before a
// popcornweb.toml edit beside an index from after it.
type projectState struct {
	mutex   sync.RWMutex
	project *Project
	index   index
	// loadError is what the last load failed with, kept so it can be reported
	// as a diagnostic on the configuration file rather than only logged.
	loadError error
	// reported marks that the absent project was announced, because saying it
	// on every open would be noise in a checkout that has no project.
	reported bool
}

func (s *projectState) snapshot() (*Project, index, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.project, s.index, s.loadError
}

func (s *projectState) replace(project *Project, built index, err error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.project, s.index, s.loadError = project, built, err
}

// DialectsFor maps a generate purpose to the dialects it reads there.
//
// A purpose reading only Go contributes no dialect and is still listed, so a
// directory serving two purposes is one entry per purpose and a message can
// name the one that matters. It is exported because the caller that reads
// popcornweb.toml is the one that builds the source list.
func DialectsFor(purpose string) []Dialect {
	switch purpose {
	case "generate.templates", "generate.pages":
		return []Dialect{dialectHTML}
	case "generate.queries":
		return []Dialect{dialectSQL}
	case "generate.dynamo":
		return []Dialect{dialectDynamo}
	default:
		return nil
	}
}

// owns reports whether the project reads this file for some purpose, and the
// purpose key that does.
//
// A page tree is a purpose like any other here. The stricter rule a tree
// applies to the names it does not reserve belongs to stray below, because a
// layout.pw.html and a card.pw.html are both inside the purpose and only one
// of them is compiled.
func (p *Project) owns(path string, kind Dialect) (string, bool) {
	for _, source := range p.Sources {
		if !within(source.Dir, path) {
			continue
		}
		for _, candidate := range source.Kinds {
			if candidate == kind {
				return source.Purpose, true
			}
		}
	}
	return "", false
}

// purposesOf is the set of template purposes the directory holding path serves.
func (p *Project) purposesOf(path string) pwgen.SourcePurposes {
	directory := filepath.Dir(path)
	purposes := pwgen.SourcePurposes{}
	for _, source := range p.Sources {
		if !within(source.Dir, directory) {
			continue
		}
		switch source.Purpose {
		case "generate.templates":
			purposes.Templates = true
		case "generate.queries":
			purposes.Queries = true
		case "generate.dynamo":
			purposes.Dynamo = true
		case "generate.firestore":
			purposes.Firestore = true
		case "generate.pages":
			purposes.Pages = true
		}
	}
	return purposes
}

// strayMessage reports why nothing compiles this source, in the words
// api:cli-generate uses for the same condition.
//
// The wording is not restated here: pwgen owns it, and
// decision:shared-check-catalog makes two wordings of one finding a defect. A
// source outside the project entirely is not stray — it is not this project's
// file at all — which is why the caller checks for a project first.
func (p *Project) strayMessage(path string) (string, bool) {
	if !within(p.Root, path) {
		return "", false
	}
	relative := filepath.ToSlash(relativeTo(p.Root, path))
	return pwgen.StrayTemplateMessage(relative, filepath.Base(path), p.purposesOf(path))
}

// within reports whether path is inside directory or is the directory itself.
func within(directory, path string) bool {
	relative, err := filepath.Rel(directory, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// buildIndex parses every .pw.* source the purposes list.
//
// The parse is the same one an open document gets, so an indexed declaration
// and an outlined one cannot disagree. A source that does not parse contributes
// nothing and is not reported: it is reported when the developer opens it, and
// a project mid-edit would otherwise fill the client with findings about files
// nobody has looked at.
func buildIndex(project *Project) index {
	if project == nil {
		return index{graph: newTypeGraph()}
	}
	built := index{graph: newTypeGraph()}
	seen := map[string]bool{}
	for _, source := range project.Sources {
		if len(source.Kinds) == 0 {
			continue
		}
		_ = filepath.WalkDir(source.Dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				// An unreadable directory is skipped rather than failing the
				// whole index: the rest of the project is still answerable.
				if entry != nil && entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				if skipDirectory(entry.Name()) {
					return fs.SkipDir
				}
				return nil
			}
			kind := dialectOf(path)
			if kind == dialectNone || !contains(source.Kinds, kind) || seen[path] {
				return nil
			}
			seen[path] = true
			source, err := os.ReadFile(path)
			if err != nil {
				built.skipped++
				return nil
			}
			built.files++
			declarations, file := readSource(project, path, kind, string(source))
			built.declarations = append(built.declarations, declarations...)
			built.graph.add(uriOf(path), file)
			return nil
		})
	}
	sort.Slice(built.declarations, func(i, j int) bool {
		if built.declarations[i].Name != built.declarations[j].Name {
			return built.declarations[i].Name < built.declarations[j].Name
		}
		return built.declarations[i].Container < built.declarations[j].Container
	})
	return built
}

// readSource parses one file into both halves of the index: the flat entries a
// symbol search answers from, and the resolved declarations the graph holds.
//
// Only the roots become index entries: a workspace symbol search is for a
// declaration, and a record field named id in forty types would bury every one
// of them. The graph keeps the fields, because that is what a hover reads.
func readSource(project *Project, path string, kind Dialect, text string) ([]Declaration, fileSymbols) {
	starts := newLineStarts(text)
	found := analyze(filepath.Base(path), kind, text, starts)
	container := filepath.ToSlash(relativeTo(project.Root, path))
	uri := uriOf(path)

	symbols := documentSymbols(found, text, starts)
	declarations := make([]Declaration, 0, len(symbols))
	for _, symbol := range symbols {
		declarations = append(declarations, Declaration{
			Name:      symbol.Name,
			Container: container,
			URI:       uri,
			Kind:      symbol.Kind,
			Range:     symbol.Range,
		})
	}
	return declarations, symbolsOf(project, path, uri, kind, text, found, starts)
}

// skipDirectory names the directories an index never descends into. They hold
// no generation source, and .git in particular is large enough that walking it
// would dominate a reload.
func skipDirectory(name string) bool {
	switch name {
	case ".git", ".devbox", "node_modules", "public", "dist", "result":
		return true
	default:
		return strings.HasPrefix(name, ".") && name != "."
	}
}

func contains(kinds []Dialect, kind Dialect) bool {
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

func relativeTo(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return relative
}
