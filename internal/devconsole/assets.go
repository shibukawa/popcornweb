package devconsole

import (
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AssetSource is what the asset pane needs to know about the project. The
// caller resolves it, because locating a project root and reading its
// configuration is pw's job and not this package's.
type AssetSource struct {
	// Root is the project root; public/ and the Tailwind paths resolve from it.
	Root   string
	Public string
	// Mount is the configured public mount, or empty when pw could not
	// determine it. An undetermined mount is reported as undetermined rather
	// than assumed, because a wrong URL here is worse than no URL.
	Mount string
	// TailwindEnabled, TailwindInput, and TailwindOutput mirror the project
	// configuration. Input and Output are project-relative slash paths.
	TailwindEnabled bool
	TailwindInput   string
	TailwindOutput  string
	// Compressible decides precompression eligibility. It is injected rather
	// than reimplemented so that the pane and the build agree by construction:
	// a pane that guessed differently from api:cli-build would be worse than
	// no pane.
	Compressible func(path string) bool
}

// assetEntry is one file under public/, described as the developer loop serves
// it and as a release build would.
type assetEntry struct {
	Path string
	URL  string
	Size int64
	// Eligible reports whether a release build would write sidecars.
	Eligible bool
	// Sidecar state. Present is whether any exists now; Stale is whether one is
	// older than its source. The developer loop never writes them, so an absent
	// sidecar is the normal state and not a finding.
	//
	// SidecarSize is the smallest coding found, because that is what a current
	// browser receives. SidecarCodings names what was found, in preference
	// order, so a partial set is visible rather than averaged away.
	SidecarPresent bool
	SidecarSize    int64
	SidecarStale   bool
	SidecarCodings []string
	// Orphan marks a sidecar whose source is gone or is no longer eligible.
	// A release build would delete it; nothing in the loop will.
	Orphan bool
}

type assetReport struct {
	Entries []assetEntry
	// Limits are the things this run could not determine. They are listed
	// rather than filled in with defaults, the way pw doctor lists what it
	// could not read.
	Limits []string
	// Missing is true when the project has no public directory at all, which
	// is an ordinary shape rather than an error.
	Missing bool
	Mount   string

	TailwindEnabled bool
	TailwindOutput  string
	TailwindPresent bool
	TailwindStale   bool
	TailwindInput   string

	TotalSize      int64
	EligibleSize   int64
	CompressedSize int64
}

// AssetPane builds the static asset pane over source.
func AssetPane(source AssetSource) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		renderPage(w, navFrom(r), "assets", "assets", buildHTML(assetBody, scanAssets(source)))
	})
}

// navKey carries the console nav into a pane handler. A pane rendered with the
// console layout needs the same nav as every other page, and threading it
// through the request keeps AssetPane from taking a Console it would otherwise
// only read one field from.
type navKey struct{}

func scanAssets(source AssetSource) assetReport {
	report := assetReport{
		Mount:           source.Mount,
		TailwindEnabled: source.TailwindEnabled,
		TailwindInput:   source.TailwindInput,
		TailwindOutput:  source.TailwindOutput,
	}
	if source.Mount == "" {
		report.Limits = append(report.Limits,
			"the public mount is undetermined, so the URL column is left blank; server.public.mount lives in the runtime configuration this pane does not read")
	}
	public := source.Public
	if public == "" {
		public = "public"
	}
	publicRoot := filepath.Join(source.Root, filepath.FromSlash(public))
	info, err := os.Stat(publicRoot)
	switch {
	case os.IsNotExist(err):
		report.Missing = true
	case err != nil:
		report.Limits = append(report.Limits, "public directory unreadable: "+err.Error())
		report.Missing = true
	case !info.IsDir():
		report.Limits = append(report.Limits, "public exists but is not a directory")
		report.Missing = true
	default:
		report.Entries, report.Limits = walkPublic(publicRoot, source, report.Limits)
	}
	for _, entry := range report.Entries {
		report.TotalSize += entry.Size
		if entry.Eligible {
			report.EligibleSize += entry.Size
			report.CompressedSize += entry.SidecarSize
		}
	}
	report.scanTailwind(source)
	return report
}

// sidecarSuffixes are the precompressed forms a release build writes, in the
// order a response prefers them. The pane recognizes all of them so a file with
// three siblings reads as one asset rather than as three orphans.
var sidecarSuffixes = [...]struct{ suffix, token string }{
	{suffix: ".br", token: "br"},
	{suffix: ".zstd", token: "zstd"},
	{suffix: ".gz", token: "gzip"},
}

func sidecarSuffixFor(name string) (suffix string, token string, ok bool) {
	for _, entry := range sidecarSuffixes {
		if strings.HasSuffix(name, entry.suffix) {
			return entry.suffix, entry.token, true
		}
	}
	return "", "", false
}

// observedSidecar is what the pane found beside one source for one coding.
type observedSidecar struct {
	token string
	info  os.FileInfo
}

func walkPublic(publicRoot string, source AssetSource, limits []string) ([]assetEntry, []string) {
	sources := map[string]assetEntry{}
	sidecars := map[string][]observedSidecar{}
	err := filepath.WalkDir(publicRoot, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			// A symlink or a device node is refused by the build rather than
			// served, so the pane reports it as a limit instead of a file.
			limits = append(limits, "not a regular file, and a release build would refuse it: "+relative(publicRoot, name))
			return nil
		}
		relative := relative(publicRoot, name)
		if suffix, token, isSidecar := sidecarSuffixFor(name); isSidecar {
			owner := strings.TrimSuffix(relative, suffix)
			sidecars[owner] = append(sidecars[owner], observedSidecar{token: token, info: info})
			return nil
		}
		// The empty-tree sentinel is embedded and never served, so it is not
		// an asset and does not belong in a list of what the project serves.
		if entry.Name() == ".keep" {
			return nil
		}
		sources[relative] = assetEntry{
			Path:     relative,
			Size:     info.Size(),
			Eligible: source.Compressible != nil && source.Compressible(name),
		}
		return nil
	})
	if err != nil {
		limits = append(limits, "public walk stopped: "+err.Error())
	}
	result := make([]assetEntry, 0, len(sources)+len(sidecars))
	for path, entry := range sources {
		entry.URL = assetURL(source.Mount, path)
		if found, ok := sidecars[path]; ok {
			entry.SidecarPresent = true
			sourceInfo, sourceErr := os.Stat(filepath.Join(publicRoot, filepath.FromSlash(path)))
			// The reported size is the smallest coding, because that is what a
			// current browser receives; the codings list says what else exists.
			// A single stale sibling makes the set stale: the build writes them
			// together, so one older than the source means none of them match.
			for _, sidecar := range found {
				entry.SidecarCodings = append(entry.SidecarCodings, sidecar.token)
				if entry.SidecarSize == 0 || sidecar.info.Size() < entry.SidecarSize {
					entry.SidecarSize = sidecar.info.Size()
				}
				if sourceErr == nil && sidecar.info.ModTime().Before(sourceInfo.ModTime()) {
					entry.SidecarStale = true
				}
			}
			if !entry.Eligible {
				// A sidecar beside an ineligible source is one the build would
				// remove, so it is reported where the developer can see it
				// rather than silently ignored.
				entry.Orphan = true
			}
			delete(sidecars, path)
		}
		result = append(result, entry)
	}
	for path, found := range sidecars {
		for _, sidecar := range found {
			suffix := sidecar.token
			for _, known := range sidecarSuffixes {
				if known.token == sidecar.token {
					suffix = known.suffix
					break
				}
			}
			result = append(result, assetEntry{Path: path + suffix, Orphan: true, SidecarPresent: true, SidecarCodings: []string{sidecar.token}})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, limits
}

func (r *assetReport) scanTailwind(source AssetSource) {
	if !source.TailwindEnabled || source.TailwindOutput == "" {
		return
	}
	output := filepath.Join(source.Root, filepath.FromSlash(source.TailwindOutput))
	outputInfo, err := os.Stat(output)
	if err != nil {
		return
	}
	r.TailwindPresent = true
	if source.TailwindInput == "" {
		return
	}
	inputInfo, err := os.Stat(filepath.Join(source.Root, filepath.FromSlash(source.TailwindInput)))
	if err != nil {
		return
	}
	r.TailwindStale = outputInfo.ModTime().Before(inputInfo.ModTime())
}

func relative(root, name string) string {
	path, err := filepath.Rel(root, name)
	if err != nil {
		return filepath.ToSlash(name)
	}
	return filepath.ToSlash(path)
}

func assetURL(mount, path string) string {
	if mount == "" {
		return ""
	}
	return strings.TrimSuffix(mount, "/") + "/" + path
}

// ratio renders a compression result the way a reader compares it, which is a
// percentage of the original rather than a byte count they have to divide.
func (e assetEntry) Ratio() string {
	if !e.SidecarPresent || e.Size == 0 || e.SidecarSize == 0 {
		return ""
	}
	return fmt.Sprintf("%.0f%%", 100*float64(e.SidecarSize)/float64(e.Size))
}

func (e assetEntry) Bytes() string { return humanBytes(e.Size) }

func (e assetEntry) SidecarBytes() string {
	if !e.SidecarPresent {
		return ""
	}
	return humanBytes(e.SidecarSize)
}

// Codings names the sidecars found, so a file carrying two of three reads as
// incomplete rather than as compressed.
func (e assetEntry) Codings() string {
	if len(e.SidecarCodings) == 0 {
		return ""
	}
	return strings.Join(e.SidecarCodings, " ")
}

func (r assetReport) TotalBytes() string { return humanBytes(r.TotalSize) }

func humanBytes(size int64) string {
	switch {
	case size < 1024:
		return fmt.Sprintf("%d B", size)
	case size < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	}
}

var assetBody = template.Must(template.New("assets").Parse(`
<h1>Static assets</h1>
<p class="sub">what the developer loop serves, and what a release build would.</p>

<div class="card">
<div><strong>now</strong> · files are read from the project directly, identity encoding only, and no sidecar is written or served</div>
<div><strong>release</strong> · <code>pw build</code> writes <code>.br</code>, <code>.zstd</code> and <code>.gz</code> beside every eligible file and picks one by negotiation</div>
</div>

{{if .Missing}}
<p class="muted">This project has no <code>public</code> directory.</p>
{{else}}
<h2>public/ <span class="muted">· {{len .Entries}} files · {{.TotalBytes}}</span></h2>
<table>
<tr><th>file</th><th>URL</th><th class="num">size</th><th class="num">compressed</th><th>state</th></tr>
{{range .Entries}}<tr>
<td><code>{{.Path}}</code></td>
<td>{{if .URL}}<code>{{.URL}}</code>{{else}}<span class="undetermined">—</span>{{end}}</td>
<td class="num">{{.Bytes}}</td>
<td class="num">{{if .SidecarPresent}}{{.SidecarBytes}} <span class="muted">{{.Ratio}}</span> <span class="muted">{{.Codings}}</span>{{else if .Eligible}}<span class="muted">on build</span>{{else}}<span class="muted">—</span>{{end}}</td>
<td>{{if .Orphan}}<span class="state-failed">stale sidecar, a build would remove it</span>
{{else if .SidecarStale}}<span class="state-failed">sidecar older than source</span>
{{else if .Eligible}}<span class="muted">compressible</span>
{{else}}<span class="muted">not compressible</span>{{end}}</td>
</tr>{{end}}
</table>
{{end}}

{{if .TailwindEnabled}}
<h2>Generated CSS</h2>
<table>
<tr><th>output</th><th>state</th></tr>
<tr><td><code>{{.TailwindOutput}}</code></td>
<td>{{if not .TailwindPresent}}<span class="state-failed">missing</span>
{{else if .TailwindStale}}<span class="state-failed">older than <code>{{.TailwindInput}}</code></span>
{{else}}<span class="state-healthy">current</span>{{end}}</td></tr>
</table>
{{end}}

{{if .Limits}}
<h2>Not determined</h2>
<ul>{{range .Limits}}<li class="muted">{{.}}</li>{{end}}</ul>
{{end}}
`))
