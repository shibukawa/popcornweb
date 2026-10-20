package pwlsp

// pw/routes: the concept:page-tree routes a project serves.
//
// requirement:editor-route-explorer exists because decision:dual-router-coexistence
// spreads the URL space across two representations: a discovered route is a
// directory name and a registered one is a call in Go. This answers the first
// half from the project model, which needs no Go analysis and no generated
// output; the second half needs the resolved import graph api:cli-doctor loads
// and is not answered here.

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shibukawa/popcornweb/internal/pwgen"
	"github.com/shibukawa/popcornweb/internal/pwroutes"
)

// Route is one discovered route.
type Route struct {
	// Path is the URL the directory serves, with a dynamic segment written the
	// way concept:page-tree spells it in the tree.
	Path string `json:"path"`
	// Page is the page template, relative to the project root.
	Page    string `json:"page"`
	PageURI string `json:"pageUri"`
	// Layouts are the layout templates that wrap it, outermost first.
	Layouts []string `json:"layouts,omitempty"`
	// Handler is the optional page.go beside the template.
	Handler string `json:"handler,omitempty"`
	// Root is the generate.pages entry this route was found under, so a
	// project with two trees can say which is which.
	Root string `json:"root"`
}

// RouteReport is the reply, with what it does not cover stated rather than
// left to be inferred from an absence.
type RouteReport struct {
	Routes []Route `json:"routes"`
	// NotCovered names the routes this answer does not include. It is empty
	// once data:route-table supplies the registered half.
	NotCovered string `json:"notCovered,omitempty"`
}

// routesOf lists what the project serves: the page trees it declares, walked
// here, and the registrations api:cli-generate wrote into data:route-table.
//
// The registered half is read rather than analyzed. Finding it needs the
// resolved import graph, and running that analysis here would be the second
// implementation decision:shared-check-catalog refuses; the table is what
// api:cli-doctor reads for the same reason.
func routesOf(project *Project) RouteReport {
	report := RouteReport{Routes: []Route{}}
	if project == nil {
		report.NotCovered = "there is no project, so nothing names the routes"
		return report
	}
	report.Routes = append(report.Routes, registeredRoutes(project, &report)...)
	for _, source := range project.Sources {
		if source.Purpose != "generate.pages" {
			continue
		}
		root := filepath.ToSlash(relativeTo(project.Root, source.Dir))
		_ = filepath.WalkDir(source.Dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				if skipDirectory(entry.Name()) {
					return fs.SkipDir
				}
				return nil
			}
			if entry.Name() != pwgen.PageFile {
				return nil
			}
			report.Routes = append(report.Routes, routeAt(project, source.Dir, root, filepath.Dir(path)))
			return nil
		})
	}
	sort.Slice(report.Routes, func(i, j int) bool { return report.Routes[i].Path < report.Routes[j].Path })
	return report
}

// routeAt describes one route directory.
func routeAt(project *Project, treeRoot, rootName, directory string) Route {
	page := filepath.Join(directory, pwgen.PageFile)
	route := Route{
		Path:    urlOf(treeRoot, directory),
		Page:    filepath.ToSlash(relativeTo(project.Root, page)),
		PageURI: uriOf(page),
		Root:    rootName,
	}
	if exists(filepath.Join(directory, "page.go")) {
		route.Handler = filepath.ToSlash(relativeTo(project.Root, filepath.Join(directory, "page.go")))
	}
	// The layout chain is every layout.pw.html from the tree root down to this
	// directory, which is the order they wrap in.
	for current := treeRoot; ; {
		if layout := filepath.Join(current, pwgen.LayoutFile); exists(layout) {
			route.Layouts = append(route.Layouts, filepath.ToSlash(relativeTo(project.Root, layout)))
		}
		if current == directory {
			break
		}
		relative, err := filepath.Rel(current, directory)
		if err != nil || relative == "." {
			break
		}
		segment := strings.SplitN(filepath.ToSlash(relative), "/", 2)[0]
		current = filepath.Join(current, segment)
	}
	return route
}

// urlOf turns a directory below a tree root into the URL it serves.
func urlOf(treeRoot, directory string) string {
	relative, err := filepath.Rel(treeRoot, directory)
	if err != nil || relative == "." {
		return "/"
	}
	return "/" + filepath.ToSlash(relative)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// StoryTarget is the reply to pw/storyFor: where the requirement:template-storybook
// pane renders the declaration under the caret.
//
// The URL is built rather than discovered, from the port data:project-config
// declares for requirement:dev-console. Whether anything is listening there is
// the client's question: this server never starts api:cli-dev and never
// contacts it, per policy:editor-tool-execution.
type StoryTarget struct {
	Status string `json:"status"`
	// Declaration is what the caret named.
	Declaration string `json:"declaration"`
	URL         string `json:"url,omitempty"`
	Message     string `json:"message,omitempty"`
}

func storyFor(project *Project, symbol Symbol, resolved bool) StoryTarget {
	target := StoryTarget{Status: "unavailable", Declaration: symbol.Name}
	switch {
	case project == nil:
		target.Message = "there is no project, so there is no storybook to render in"
	case !resolved:
		target.Message = "there is no declaration here to preview"
	case symbol.Kind != kindComponent:
		// A statement returns rows and a record is a shape. Only a component
		// renders, so only a component has a story.
		target.Message = symbol.Name + " is a " + string(symbol.Kind) + ", and the storybook renders components"
	case !project.StorybookEnabled:
		target.Message = "the storybook pane is off for this project; enable dev.console.storybook.enabled"
	default:
		target.Status = "ok"
		target.URL = "http://localhost:" + itoaPort(project.ConsolePort) + "/storybook/story/" +
			symbol.Package + "/" + symbol.Name
	}
	return target
}

func itoaPort(port int) string {
	if port <= 0 {
		return "18081"
	}
	digits := ""
	for port > 0 {
		digits = string(rune('0'+port%10)) + digits
		port /= 10
	}
	return digits
}

// ProjectInfo is the reply to pw/project: what a client needs to build a URL
// or name the project, without loading data:project-config a second time.
type ProjectInfo struct {
	Loaded bool   `json:"loaded"`
	Root   string `json:"root,omitempty"`
	Name   string `json:"name,omitempty"`
	// ConsoleURL is where requirement:dev-console listens when api:cli-dev is
	// running. It is where the console would be, not a claim that anything is
	// there: this server never contacts it.
	ConsoleURL       string `json:"consoleUrl,omitempty"`
	StorybookEnabled bool   `json:"storybookEnabled"`
}

func projectInfo(project *Project) ProjectInfo {
	if project == nil {
		return ProjectInfo{}
	}
	return ProjectInfo{
		Loaded:           true,
		Root:             project.Root,
		Name:             project.Name,
		ConsoleURL:       "http://localhost:" + itoaPort(project.ConsolePort),
		StorybookEnabled: project.StorybookEnabled,
	}
}

// registeredRoutes reads the Go-registered half out of data:route-table.
//
// A project that has not generated has no table, and that is said rather than
// shown as an empty list: an editor view that silently covers half the URL
// space reads as if it covered all of it.
func registeredRoutes(project *Project, report *RouteReport) []Route {
	table, err := pwroutes.Load(project.Root, project.RouteTable)
	if err != nil {
		report.NotCovered = "routes registered in Go; run pw generate to write the route table"
		return nil
	}

	routes := make([]Route, 0, len(table.Entries))
	for _, entry := range table.Entries {
		// The page half is walked here from the source tree, which is current
		// where the table is only as fresh as the last generation.
		if entry.Origin != pwroutes.OriginApplication {
			continue
		}
		route := Route{Path: entry.Pattern, Root: "registered"}
		if entry.Site != nil {
			route.Page = entry.Site.File
			route.PageURI = uriOf(filepath.Join(project.Root, filepath.FromSlash(entry.Site.File)))
		}
		if entry.Handler != "" {
			route.Handler = entry.Handler
		}
		routes = append(routes, route)
	}
	if len(table.Unresolved) > 0 {
		report.NotCovered = countOf(len(table.Unresolved), "registration") +
			" whose pattern the analysis could not read, such as one built at run time"
	}
	return routes
}

// countOf words a number of things, because "1 registrations" reads as a defect
// in the view rather than in the project.
func countOf(count int, noun string) string {
	if count == 1 {
		return "one " + noun
	}
	return strconv.Itoa(count) + " " + noun + "s"
}
