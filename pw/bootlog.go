package pw

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/shibukawa/popcornweb/internal/bootblock"
	"github.com/shibukawa/popcornweb/internal/configview"
	"github.com/shibukawa/popcornweb/internal/pwtree"
	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/tinybind-go/configbind"
)

// Boot log formats. Startup facts are collected during configuration parsing
// and emitted once, when the framework knows whether it owns a listener.
const (
	// BootLogAuto renders the tree on an interactive terminal and the single
	// structured record everywhere else.
	BootLogAuto = "auto"
	// BootLogTree writes the human-readable startup summary to stderr.
	BootLogTree = "tree"
	// BootLogRecord emits one structured log record through the default logger.
	BootLogRecord = "record"
	// BootLogOff suppresses the startup summary.
	BootLogOff = "off"
)

// bootMessage is the stable message of the structured startup record.
const bootMessage = "popcornweb started"

// redactedValue matches the mask configbind applies to the keys it recognizes
// as sensitive, so a summary never shows two different marks for one idea.
const redactedValue = configview.Redacted

type bootEntry struct {
	key    string
	value  string
	source string
}

// bootReport is everything the framework knows about this process at startup.
type bootReport struct {
	startedAt   time.Time
	environment string
	configPath  string
	configFound bool
	// dotenvFiles are the policy:dotenv-resolution files the load layered
	// under the process environment, in order; empty when none was read.
	dotenvFiles []string
	entries     []bootEntry
}

var bootState struct {
	sync.Mutex
	report  bootReport
	emitted bool
}

// captureBootReport records resolved configuration instead of logging one
// record per key. The summary is emitted later, by Run or Middlewares.
func captureBootReport(result *configbind.LoadResult) {
	report := bootReport{startedAt: time.Now(), environment: Env(), dotenvFiles: pwconfig.DotenvFiles()}
	if result != nil {
		report.configPath, report.configFound = result.ConfigPath, result.FoundFile
		report.entries = bootEntries(result)
	}
	bootState.Lock()
	bootState.report, bootState.emitted = report, false
	bootState.Unlock()
}

// bootEntries takes the keys configbind considers worth reporting: already in
// registration then declaration order, already stripped of the settings a
// disabled parent or an unselected variant made irrelevant, and already masked.
// Redaction is a secret tag or a recognized key name, so it is decided where the
// field is declared rather than guessed again here. An array of tables arrives
// expanded, one entry per element key, which is what keeps a connection set from
// showing up as a single empty line.
//
// One filter is left to this side. An entry marked omittable is a key its author
// rated as detail that nothing but the default layer set, and configbind reports
// the rating rather than applying it: whether detail is worth printing depends on
// which surface is being drawn, and the library cannot know that. This is the
// short surface, so it skips them; api:cli-doctor is the complete one and prints
// them from the same call. A key any source set is never marked, so nothing a
// deployment decided is dropped here.
//
// The skip happens during capture rather than in a renderer, which is what keeps
// the tree and the record forms from disagreeing about what the summary contains.
//
// A DSN is the one exception to arriving ready to print. Masked whole it answers
// none of what an operator opens this summary for, so its public half is
// rendered back in.
func bootEntries(result *configbind.LoadResult) []bootEntry {
	if result == nil || result.Overlay == nil {
		return nil
	}
	reported := result.Provenance()
	entries := make([]bootEntry, 0, len(reported))
	for _, key := range reported {
		if key.Omittable {
			continue
		}
		value := key.Value
		if key.Masked && configview.IsDSNKey(key.Key) {
			if raw, ok := configview.Raw(result.Overlay, key); ok {
				value = configview.DSN(raw)
			}
		}
		entries = append(entries, bootEntry{key: key.Key, value: value, source: string(key.Place)})
	}
	return entries
}

// emitBootReport writes the startup summary exactly once. listening is the URL
// of the framework-owned listener, or empty when the application serves itself.
func emitBootReport(listening string) {
	bootState.Lock()
	defer bootState.Unlock()
	if bootState.emitted {
		return
	}
	bootState.emitted = true
	report := bootState.report
	switch resolveBootLogFormat(ConfigContext[ObservabilityConfig](nil).BootLog) {
	case BootLogOff:
	case BootLogTree:
		_, _ = os.Stderr.WriteString(renderBootTree(report, listening, bootStyleFor(os.Stderr)))
	default:
		processLogger().Info(bootMessage, bootRecordAttrs(report, listening)...)
	}
}

// resolveBootLogFormat maps the configured value to a concrete format. The
// terminal check is what separates a developer reading a summary from an
// operator collecting one structured event.
func resolveBootLogFormat(setting string) string {
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case BootLogTree:
		return BootLogTree
	case BootLogRecord:
		return BootLogRecord
	case BootLogOff:
		return BootLogOff
	}
	if bootLogAutoOff {
		return BootLogOff
	}
	if isTerminal(os.Stderr) {
		return BootLogTree
	}
	return BootLogRecord
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// bootTreeEntries hands the reported keys to the shared layout. api:cli-doctor
// renders the same shape from the same package, so the two summaries a reader
// sees cannot drift apart.
func bootTreeEntries(entries []bootEntry) []pwtree.Entry {
	converted := make([]pwtree.Entry, 0, len(entries))
	for _, entry := range entries {
		converted = append(converted, pwtree.Entry{Key: entry.key, Value: entry.value, Source: entry.source})
	}
	return converted
}

// renderBootTree formats the whole startup summary: banner, configuration
// grouped by section, and the address the server accepted.
func renderBootTree(report bootReport, listening string, style bootStyle) string {
	var out strings.Builder
	for _, line := range bootBanner(report, style) {
		out.WriteString(line)
		out.WriteByte('\n')
	}
	lines := pwtree.Lines(bootTreeEntries(report.entries))
	if len(lines) > 0 {
		out.WriteString(style.dim("configuration") + "\n")
	}
	pwtree.Render(&out, lines, bootSourceTag, style.dim)
	if listening != "" {
		out.WriteString("\nlistening on " + style.bold(listening) + "\n")
	}
	return out.String()
}

// bootBannerArt is the framework's popcorn mascot. It lives in the package that
// reads a summary back, because api:cli-dev finds one in a stream by looking for
// its first row, and a mascot the two halves disagreed about would be a summary
// the developer loop silently stopped recognizing.
var bootBannerArt = bootblock.Art

func bootBanner(report bootReport, style bootStyle) []string {
	name := "Popcorn Web"
	if version := frameworkVersion(); version != "" {
		name += " " + version
	}
	captions := []string{
		"",
		style.bold(name),
		"started at " + report.startedAt.Format("2006-01-02 15:04:05 MST"),
		"env " + report.environment + " · " + bootConfigCaption(report),
		"",
	}
	banner := make([]string, 0, len(bootBannerArt)+1)
	for index, art := range bootBannerArt {
		line := style.accent(art)
		if captions[index] != "" {
			line += "   " + captions[index]
		}
		banner = append(banner, line)
	}
	return append(banner, "")
}

// bootDisplayValue keeps an unset value visible instead of leaving the column
// blank, which reads as a rendering bug.
func bootDisplayValue(value string) string {
	if value == "" {
		return `""`
	}
	return value
}

func bootConfigCaption(report bootReport) string {
	caption := "no config file (defaults, env, and flags only)"
	if report.configFound && report.configPath != "" {
		caption = report.configPath
	}
	if len(report.dotenvFiles) > 0 {
		caption += " · " + strings.Join(report.dotenvFiles, ", ")
	}
	return caption
}

// bootSourceTag names the layer that won a key. Defaults stay unmarked so the
// values an operator actually chose are the ones that catch the eye.
func bootSourceTag(source string) string {
	switch configbind.Place(source) {
	case configbind.PlaceDefault, "":
		return ""
	case configbind.PlaceFile:
		return "file"
	case configbind.PlaceCLI:
		return "flag"
	default:
		// A value a dotenv file set is tagged with the file, so the reader
		// knows which of the two files to open.
		if file, ok := configbind.EnvFileOf(configbind.Place(source)); ok {
			return file
		}
		return source
	}
}

// bootRecordAttrs renders the same facts as one structured record.
//
// Nesting is expressed with dotted keys rather than groups, because a record
// attribute is a scalar: the same record has to survive OTLP export, where a
// nested group has no representation, and "config.server.port" reads the same
// in a terminal as a group would.
func bootRecordAttrs(report bootReport, listening string) []Attribute {
	attrs := []Attribute{
		String("environment", report.environment),
		String("started_at", report.startedAt.Format(time.RFC3339Nano)),
	}
	if version := frameworkVersion(); version != "" {
		attrs = append(attrs, String("version", version))
	}
	if report.configFound && report.configPath != "" {
		attrs = append(attrs, String("config_file", report.configPath))
	}
	if len(report.dotenvFiles) > 0 {
		attrs = append(attrs, String("dotenv_files", strings.Join(report.dotenvFiles, ",")))
	}
	if listening != "" {
		attrs = append(attrs, String("listening", listening))
	}
	for _, entry := range report.entries {
		attrs = append(attrs, String("config."+entry.key, entry.value))
	}
	for _, entry := range report.entries {
		if tag := bootSourceTag(entry.source); tag != "" {
			attrs = append(attrs, String("config_source."+entry.key, tag))
		}
	}
	return attrs
}

// bootStyle applies ANSI attributes only when the destination is a terminal
// that has not asked for plain output.
type bootStyle struct{ color bool }

func bootStyleFor(file *os.File) bootStyle {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return bootStyle{}
	}
	return bootStyle{color: isTerminal(file) || forcedColor()}
}

// forcedColor answers the CLICOLOR_FORCE convention, which is the only way a
// process whose output is a pipe can be told that a terminal is still on the
// other end of it. api:cli-dev is that case: it reads the summary back to report
// requirement:dev-reload-summary, and without this the developer would pay for
// the feature in color. NO_COLOR still wins, because that one is the developer
// speaking rather than a caller.
func forcedColor() bool {
	value := os.Getenv("CLICOLOR_FORCE")
	return value != "" && value != "0"
}

func (style bootStyle) wrap(code, text string) string {
	if !style.color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (style bootStyle) bold(text string) string   { return style.wrap("1", text) }
func (style bootStyle) dim(text string) string    { return style.wrap("2", text) }
func (style bootStyle) accent(text string) string { return style.wrap("33", text) }
