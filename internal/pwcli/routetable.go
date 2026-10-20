package pwcli

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/shibukawa/popcornweb/internal/pwgen"
	"github.com/shibukawa/popcornweb/internal/pwroutes"
	"github.com/shibukawa/tinybind-go/parser"
	"github.com/shibukawa/tinybind-go/routetree"
)

// routeCollector gathers data:route-table across the directories one run
// walks.
//
// It is filled from the analysis each directory's generation already performed,
// so the table costs the run nothing beyond the merge. A nil collector is a run
// that is not producing a table, which is every call that is not a whole-project
// generation.
type routeCollector struct {
	// mu admits the concurrent adds a parallel stage produces. The order of
	// arrival does not reach the output: table() sorts.
	mu      sync.Mutex
	root    string
	entries []pwroutes.Entry
	// unresolved holds the registration calls the analysis could not read a
	// literal pattern out of. data:route-table keeps them so a consumer states
	// a limit rather than reporting a clean table it cannot back up.
	unresolved []pwroutes.Unresolved
	seen       map[string]bool
}

func newRouteCollector(root string) *routeCollector {
	return &routeCollector{root: root, seen: map[string]bool{}}
}

// add records one package's analysis.
func (c *routeCollector) add(directory string, result *parser.Result) {
	if c == nil || result == nil {
		return
	}
	_ = directory
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, route := range result.Routes {
		site := c.siteOf(route.Site.File, route.Site.Line, route.Site.Column)
		// A package reached through more than one purpose is analyzed more
		// than once, and the same registration must not be reported as two.
		key := route.Method + " " + route.Path + "\x00" + siteKey(site)
		if c.seen[key] {
			continue
		}
		c.seen[key] = true
		c.entries = append(c.entries, pwroutes.Entry{
			Pattern: strings.TrimSpace(route.Method + " " + route.Path),
			Origin:  pwroutes.OriginApplication,
			Site:    site,
			Handler: route.Handler.Name,
		})
	}
	for _, diagnostic := range result.Diagnostics {
		site := c.siteOf(diagnostic.File, diagnostic.Line, diagnostic.Column)
		if site == nil {
			continue
		}
		key := "\x01" + siteKey(site)
		if c.seen[key] {
			continue
		}
		c.seen[key] = true
		c.unresolved = append(c.unresolved, pwroutes.Unresolved{
			Site: *site, Reason: diagnostic.Reason, Message: diagnostic.Message,
		})
	}
}

// addPage records a concept:page-tree route, whose material is the tree walk
// rather than a registration site.
func (c *routeCollector) addPage(pattern, page string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, pwroutes.Entry{
		Pattern: pattern,
		Origin:  pwroutes.OriginPage,
		Page:    filepath.ToSlash(relativeToRoot(c.root, page)),
	})
}

// addMount records a path the framework serves, with the configuration key
// that enables it: a mount that is off collides with nothing, so a consumer
// needs the key to say which side the reader may move.
func (c *routeCollector) addMount(pattern, enabledBy string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, pwroutes.Entry{
		Pattern: pattern, Origin: pwroutes.OriginFramework, EnabledBy: enabledBy,
	})
}

// table is what was collected, ready to write.
func (c *routeCollector) table() *pwroutes.Table {
	if c == nil {
		return &pwroutes.Table{}
	}
	built := &pwroutes.Table{Entries: c.entries, Unresolved: c.unresolved}
	built.Sort()
	return built
}

// siteOf makes a position relative to the project root, so the table reads the
// same on every machine and carries none of this one's directories.
func (c *routeCollector) siteOf(file string, line, column int) *pwroutes.Site {
	if file == "" || line <= 0 {
		return nil
	}
	return &pwroutes.Site{
		File:   filepath.ToSlash(relativeToRoot(c.root, file)),
		Line:   line,
		Column: column,
	}
}

func siteKey(site *pwroutes.Site) string {
	if site == nil {
		return ""
	}
	return site.File + ":" + itoa(site.Line) + ":" + itoa(site.Column)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

// writeRouteTable completes the collected table and writes it.
//
// The registrations come from the analysis and the page routes from the tree
// discovery, which together are the whole URL space an application writes.
//
// The framework mounts are not here, although data:route-table lists them:
// their paths come from data:server-runtime-config rather than from
// popcornweb.toml, so they differ by environment and a generation that is
// environment-agnostic cannot know them. The consumer that knows which
// environment it is talking about adds them, which is api:cli-doctor.
func writeRouteTable(root string, config projectConfig, collected *routeCollector) error {
	if collected == nil {
		return nil
	}
	if err := collectPageRoutes(root, config, collected); err != nil {
		return err
	}
	return pwroutes.Write(root, collected.table(), config.Assets.locations().routes)
}

// collectPageRoutes reads each declared page tree through the same discovery
// the generated registry is built from.
//
// Walking the directories here would be a second implementation of
// concept:page-tree, and it would get the pattern wrong: a tree root registers
// as /{$} rather than /, because a bare / is a prefix pattern in the standard
// library and would answer every unmatched path.
func collectPageRoutes(root string, config projectConfig, collected *routeCollector) error {
	for _, declared := range config.Generate.Pages {
		treeRoot := filepath.Join(root, filepath.FromSlash(declared))
		tree, err := routetree.Discover(pwgen.PageConfig(treeRoot, ""))
		if err != nil {
			// A declared tree that will not discover is a configuration
			// finding of its own. The table says nothing about it rather than
			// failing the generation that produced everything else.
			continue
		}
		for _, route := range tree.Routes {
			collected.addPage(route.Pattern(), filepath.Join(treeRoot, filepath.FromSlash(route.RelDir), pwgen.PageFile))
		}
	}
	return nil
}
