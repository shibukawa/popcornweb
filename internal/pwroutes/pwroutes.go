// Package pwroutes holds data:route-table: every pattern an application
// serves, in one record tooling can read.
//
// It exists as a package of its own because three readers need it and none of
// them can import the others: api:cli-generate produces it, api:cli-doctor runs
// rule:route-and-template-checks over it, and api:cli-lsp answers
// requirement:editor-route-explorer from it.
//
// The table holds what was analyzed rather than what will run. A pattern the
// analysis could not resolve is Unresolved rather than absent, so a consumer
// states a limit instead of reporting a clean table it cannot back up.
package pwroutes

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// File is where api:cli-generate writes the table, beside the asset manifest
// and on the same terms: a build product nothing reads at runtime, which is
// what keeps it out of the binary and out of version control.
const File = "dist/routes.json"

// Origin says what produced an entry, which is what
// decision:dual-router-coexistence makes worth distinguishing: the two halves
// of the URL space are written in completely different places.
type Origin string

const (
	// OriginApplication is a literal registration in handwritten Go.
	OriginApplication Origin = "application"
	// OriginPage is a concept:page-tree route, whose material is the tree walk
	// rather than a registration site.
	OriginPage Origin = "page"
	// OriginFramework is a path the framework mounts, per
	// policy:operational-endpoints and requirement:public-asset-delivery.
	OriginFramework Origin = "framework"
)

// Site is where something was written, relative to the project root.
type Site struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column,omitempty"`
}

// Entry is one pattern the application serves.
type Entry struct {
	// Pattern is recorded verbatim, so a consumer compares what net/http will
	// compare rather than a normalization of it.
	Pattern string `json:"pattern"`
	Origin  Origin `json:"origin"`
	// Site is the registration site. It is absent for a generated page and for
	// a framework mount, neither of which is written anywhere a reader could
	// open.
	Site *Site `json:"site,omitempty"`
	// Handler is the handler identity where the analysis resolved one.
	Handler string `json:"handler,omitempty"`
	// Page is the .pw.html behind a page entry, relative to the project root.
	Page string `json:"page,omitempty"`
	// EnabledBy is the configuration key that turns a framework mount on. A
	// mount that is off collides with nothing, so a consumer needs the key to
	// say which side the reader may move.
	EnabledBy string `json:"enabledBy,omitempty"`
}

// Unresolved is a registration call the analysis could not read a literal
// pattern out of.
type Unresolved struct {
	Site Site `json:"site"`
	// Reason is the analysis's own code, such as dynamic_pattern.
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// Table is the whole record.
type Table struct {
	Entries    []Entry      `json:"entries"`
	Unresolved []Unresolved `json:"unresolved,omitempty"`
}

// ErrAbsent reports that no table has been written. It is not a failure: a
// project that has not generated has none, and a consumer says the routes were
// not examined rather than that they are clean.
var ErrAbsent = errors.New("no route table has been generated")

// Sort orders the table so identical inputs produce identical bytes, which is
// what lets api:cli-check compare it like any other generated artifact.
func (t *Table) Sort() {
	sort.SliceStable(t.Entries, func(i, j int) bool {
		left, right := t.Entries[i], t.Entries[j]
		if left.Pattern != right.Pattern {
			return left.Pattern < right.Pattern
		}
		if left.Origin != right.Origin {
			return left.Origin < right.Origin
		}
		return siteKey(left.Site) < siteKey(right.Site)
	})
	sort.SliceStable(t.Unresolved, func(i, j int) bool {
		return siteKey(&t.Unresolved[i].Site) < siteKey(&t.Unresolved[j].Site)
	})
}

func siteKey(site *Site) string {
	if site == nil {
		return ""
	}
	return fmt.Sprintf("%s:%09d:%09d", site.File, site.Line, site.Column)
}

// Write writes the table under root, creating the directory it lives in.
func Write(root string, table *Table, files ...string) error {
	file := File
	if len(files) > 0 && files[0] != "" {
		file = files[0]
	}
	table.Sort()
	encoded, err := json.MarshalIndent(table, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

// Load reads the table under root, or reports ErrAbsent.
func Load(root string, files ...string) (*Table, error) {
	file := File
	if len(files) > 0 && files[0] != "" {
		file = files[0]
	}
	source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrAbsent
		}
		return nil, err
	}
	table := &Table{}
	if err := json.Unmarshal(source, table); err != nil {
		// A table that does not parse is a table that says nothing, and
		// pretending otherwise would report a clean route section.
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	return table, nil
}

// Duplicates groups the entries that register one pattern more than once.
//
// api:serve-mux panics at registration on a duplicate, so this is a startup
// crash found without starting. Only the application half can collide with
// itself this way; a page route and a mount are generated exactly once.
func (t *Table) Duplicates() map[string][]Entry {
	byPattern := map[string][]Entry{}
	for _, entry := range t.Entries {
		if entry.Origin == OriginFramework {
			continue
		}
		byPattern[entry.Pattern] = append(byPattern[entry.Pattern], entry)
	}
	for pattern, entries := range byPattern {
		if len(entries) < 2 {
			delete(byPattern, pattern)
		}
	}
	return byPattern
}

// MountClashes pairs each application entry with the enabled framework mount it
// collides with.
func (t *Table) MountClashes() [][2]Entry {
	var mounts []Entry
	for _, entry := range t.Entries {
		if entry.Origin == OriginFramework {
			mounts = append(mounts, entry)
		}
	}
	var clashes [][2]Entry
	for _, entry := range t.Entries {
		if entry.Origin == OriginFramework {
			continue
		}
		for _, mount := range mounts {
			if entry.Pattern == mount.Pattern {
				clashes = append(clashes, [2]Entry{entry, mount})
			}
		}
	}
	return clashes
}

// Mount is a path the framework serves in one environment, and the
// configuration key that turns it on.
type Mount struct {
	Pattern   string
	EnabledBy string
}

// WithMounts returns the table with one environment's framework mounts added.
//
// They are not in the written table because their paths are runtime
// configuration and differ by environment, while a generation is
// environment-agnostic. A consumer diagnosing a named environment has the
// merged configuration in hand and adds them here.
func (t *Table) WithMounts(mounts []Mount) *Table {
	merged := &Table{
		Entries:    append([]Entry{}, t.Entries...),
		Unresolved: t.Unresolved,
	}
	for _, mount := range mounts {
		if mount.Pattern == "" {
			// An unset path serves nothing, so it is not a mount and collides
			// with nothing.
			continue
		}
		merged.Entries = append(merged.Entries, Entry{
			Pattern:   mount.Pattern,
			Origin:    OriginFramework,
			EnabledBy: mount.EnabledBy,
		})
	}
	merged.Sort()
	return merged
}
