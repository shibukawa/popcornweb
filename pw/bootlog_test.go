package pw

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/internal/bootblock"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/cliparser"
	"github.com/shibukawa/tinybind-go/configbind"
)

func sampleBootReport() bootReport {
	return bootReport{
		startedAt:   time.Date(2026, 7, 27, 23, 31, 4, 0, time.UTC),
		environment: "dev",
		configPath:  "config.dev.toml",
		configFound: true,
		entries: []bootEntry{
			{key: "html.streaming", value: "true", source: "file_toml"},
			{key: "middleware.rdb.connections[0].dsn", value: redactedValue, source: "file_toml"},
			{key: "middleware.rdb.enabled", value: "false", source: "default"},
			{key: "server.port", value: "8080", source: "cli"},
			{key: "session.cookie.name", value: "pw_session", source: "default"},
		},
	}
}

func TestRenderBootTreeGroupsKeysBySection(t *testing.T) {
	rendered := renderBootTree(sampleBootReport(), "http://localhost:8080", bootStyle{})
	for _, want := range []string{
		"Popcorn Web",
		"started at 2026-07-27 23:31:04 UTC",
		"env dev · config.dev.toml",
		"├─ middleware",
		"│  └─ rdb",
		"│     ├─ connections[0]",
		"└─ session",
		"listening on http://localhost:8080",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered summary missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "middleware.rdb.enabled") {
		t.Fatalf("dotted key was not grouped:\n%s", rendered)
	}
	if count := strings.Count(rendered, "server.port"); count != 0 {
		t.Fatalf("server.port rendered flat %d times:\n%s", count, rendered)
	}
}

func TestRenderBootTreeMarksNonDefaultSourcesOnly(t *testing.T) {
	rendered := renderBootTree(sampleBootReport(), "", bootStyle{})
	for _, line := range strings.Split(rendered, "\n") {
		switch {
		case strings.Contains(line, "streaming"):
			if !strings.HasSuffix(line, "true  ← file") {
				t.Fatalf("file source line = %q", line)
			}
		case strings.Contains(line, "port"):
			if !strings.HasSuffix(line, "8080  ← flag") {
				t.Fatalf("flag source line = %q", line)
			}
		case strings.Contains(line, "enabled"):
			if strings.Contains(line, "←") {
				t.Fatalf("default source was marked: %q", line)
			}
		}
	}
	if strings.Contains(rendered, "listening on") {
		t.Fatal("summary announced a listener the framework does not own")
	}
}

func TestRenderBootTreeRedactsSecrets(t *testing.T) {
	rendered := renderBootTree(sampleBootReport(), "", bootStyle{})
	if !strings.Contains(rendered, redactedValue) {
		t.Fatalf("secret value not redacted:\n%s", rendered)
	}
}

func TestRenderBootTreeReportsMissingConfigFile(t *testing.T) {
	report := sampleBootReport()
	report.configFound, report.configPath = false, ""
	if !strings.Contains(renderBootTree(report, "", bootStyle{}), "no config file") {
		t.Fatal("missing config file was not reported")
	}
}

func TestBootRecordIsOneStructuredEvent(t *testing.T) {
	var buffer bytes.Buffer
	backend := pwruntime.NewLogBackend(LevelInfo, pwruntime.NewSlogSink(slog.NewJSONHandler(&buffer, nil)))
	pwruntime.NewLogger(context.Background(), backend).Info(bootMessage, bootRecordAttrs(sampleBootReport(), "http://localhost:8080")...)

	if lines := strings.Count(strings.TrimSpace(buffer.String()), "\n"); lines != 0 {
		t.Fatalf("startup emitted %d records, want 1:\n%s", lines+1, buffer.String())
	}
	// Nesting is dotted keys rather than JSON objects, because the same record
	// has to survive OTLP export, where a record attribute is a scalar.
	var record map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatalf("record is not valid JSON: %v\n%s", err, buffer.String())
	}
	for key, want := range map[string]string{
		"msg":                bootMessage,
		"environment":        "dev",
		"listening":          "http://localhost:8080",
		"config.server.port": "8080",
		"config.middleware.rdb.connections[0].dsn": redactedValue,
		"config_source.server.port":                "flag",
		"config_source.html.streaming":             "file",
	} {
		if got, _ := record[key].(string); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if _, marked := record["config_source.middleware.rdb.enabled"]; marked {
		t.Fatalf("default source was reported as an override: %s", buffer.String())
	}
}
func TestResolveBootLogFormat(t *testing.T) {
	tests := map[string]string{
		BootLogTree:   BootLogTree,
		"  TREE  ":    BootLogTree,
		BootLogRecord: BootLogRecord,
		BootLogOff:    BootLogOff,
		// Tests never run against a terminal, so auto resolves to the record.
		BootLogAuto: BootLogRecord,
		"":          BootLogRecord,
	}
	for setting, want := range tests {
		if got := resolveBootLogFormat(setting); got != want {
			t.Fatalf("resolveBootLogFormat(%q) = %q, want %q", setting, got, want)
		}
	}
}

func TestListenURLPrefersLocalhost(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if got, want := listenURL(listener), "http://localhost:"+strconv.Itoa(port); got != want {
		t.Fatalf("listenURL = %q, want %q", got, want)
	}
}

// TestBootTreeReadsBackAsItsEntries locks the two halves of a reload together.
// api:cli-dev reports requirement:dev-reload-summary by reading this summary
// back, so a layout change that the reader cannot follow has to fail here rather
// than in a developer's terminal, where it would look like a loop that quietly
// stopped reporting reloads.
func TestBootTreeReadsBackAsItsEntries(t *testing.T) {
	report := sampleBootReport()
	rendered := renderBootTree(report, "http://localhost:8080", bootStyle{color: true})

	scanner, complete := bootblock.Scanner{}, false
	for _, line := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n") {
		taken, done := scanner.Feed(line)
		if !taken {
			t.Fatalf("scanner refused a line of the summary: %q\n%s", line, rendered)
		}
		complete = done
	}
	if !complete {
		t.Fatalf("scanner never completed the summary:\n%s", rendered)
	}
	read, ok := bootblock.Parse(scanner.Take())
	if !ok {
		t.Fatalf("summary did not parse:\n%s", rendered)
	}
	if read.Environment != report.environment || read.ConfigFile != report.configPath {
		t.Fatalf("read env %q config %q, want %q and %q", read.Environment, read.ConfigFile, report.environment, report.configPath)
	}
	if read.Listening != "http://localhost:8080" {
		t.Fatalf("read listening %q", read.Listening)
	}
	if len(read.Entries) != len(report.entries) {
		t.Fatalf("read %d entries, want %d:\n%v", len(read.Entries), len(report.entries), read.Entries)
	}
	for index, want := range report.entries {
		got := read.Entries[index]
		if got.Key != want.key || got.Value != bootDisplayValue(want.value) || got.Source != bootSourceTag(want.source) {
			t.Fatalf("entry %d read as %+v, want key %q value %q source %q",
				index, got, want.key, bootDisplayValue(want.value), bootSourceTag(want.source))
		}
	}
}

// The summary is the short surface of requirement:startup-summary-brevity, so a
// key its author rated as detail leaves it while nothing but the default layer
// set it. The rating is reported rather than applied, which is why the entry is
// still in the provenance api:cli-doctor renders from.
func TestBootEntriesSkipRatedDefaults(t *testing.T) {
	const rated = "observability.query.level"
	const toml = "[session]\nenabled = true\nbackend = \"redis\"\n"
	if _, ok := bootKeys(t, toml)[rated]; ok {
		t.Fatalf("%s reached the summary at its default", rated)
	}
	reported := false
	for _, entry := range loadResult(t, toml).Provenance() {
		if entry.Key != rated {
			continue
		}
		reported = true
		if !entry.Omittable {
			t.Fatalf("%s is not marked omittable, so nothing rated it", rated)
		}
	}
	if !reported {
		t.Fatalf("%s left provenance entirely; the summary must skip it, not the library", rated)
	}
	// An unrated key at its default still prints, which is the opt-in polarity:
	// a field nobody rated stays visible.
	if _, ok := bootKeys(t, toml)["session.cookie.same_site"]; !ok {
		t.Fatal("an unrated default left the summary")
	}
}

// The value condition is the other lever, and it removes rather than marks: a
// store the selected backend did not select is inert, which is true on every
// surface.
func TestBootEntriesDropUnselectedBackends(t *testing.T) {
	printed := bootKeys(t, "[session]\nenabled = true\nbackend = \"redis\"\n[session.redis]\ndsn = \"redis://cache.internal:6379\"\n")
	for _, key := range []string{"session.rdb.table", "session.dynamo.table", "session.firestore.kind", "session.cookie_store.name"} {
		if _, ok := printed[key]; ok {
			t.Fatalf("%s printed under backend=redis", key)
		}
	}
	// The token cookie travels under every backend, and the DSN a deployment
	// wrote is what it opened the summary to check.
	for _, key := range []string{"session.cookie.name", "session.redis.dsn"} {
		if _, ok := printed[key]; !ok {
			t.Fatalf("%s left the summary under backend=redis", key)
		}
	}
}

// Neither lever may remove a value a source set. The rating says how interesting
// the key is in general; the winning place says whether this deployment had
// anything to say about it.
func TestBootEntriesKeepRatedKeyASourceSet(t *testing.T) {
	printed := bootKeys(t, "[observability.query]\nlevel = \"debug\"\n")
	if got, ok := printed["observability.query.level"]; !ok || got != "debug" {
		t.Fatalf("a rated key set in the file printed as %q (present %v), want debug", got, ok)
	}
	// The rest of the rated subtree is still absent, so one set leaf brings back
	// itself rather than its siblings.
	if _, ok := printed["observability.query.max_sql_length"]; ok {
		t.Fatal("a rated sibling came back with the leaf that was set")
	}
}

// The developer loop puts the application's stderr behind a pipe, which costs
// the boot log both halves of its terminal check. It pins the format back
// through the generated environment binding, so the name it derives has to be
// the name this binding answers to.
func TestBootLogFormatIsReachableThroughItsEnvironmentBinding(t *testing.T) {
	name := configbind.EnvName(cliparser.DefaultLongName("observability", "boot_log"))
	if got := loadObservability(t, "", name+"="+BootLogTree).BootLog; got != BootLogTree {
		t.Fatalf("%s=tree loaded as %q", name, got)
	}
	if got := resolveBootLogFormat(BootLogTree); got != BootLogTree {
		t.Fatalf("tree resolved to %q", got)
	}
}

// The other half: a process whose stderr is a pipe would render the summary
// plain, and the developer loop is a terminal reading it.
func TestBootStyleTakesColorFromCLICOLORForce(t *testing.T) {
	piped, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer piped.Close()
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm")

	t.Setenv("CLICOLOR_FORCE", "")
	if bootStyleFor(piped).color {
		t.Fatal("a pipe was styled without being asked")
	}
	t.Setenv("CLICOLOR_FORCE", "1")
	if !bootStyleFor(piped).color {
		t.Fatal("CLICOLOR_FORCE did not reach a pipe")
	}
	// NO_COLOR is the developer speaking, and outranks the caller.
	t.Setenv("NO_COLOR", "1")
	if bootStyleFor(piped).color {
		t.Fatal("NO_COLOR was overridden by CLICOLOR_FORCE")
	}
}
