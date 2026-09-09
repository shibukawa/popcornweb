package pw

import (
	"strings"
	"testing"
)

func TestBootSummaryNamesTheDotenvFilesAndTheirValues(t *testing.T) {
	report := sampleBootReport()
	report.dotenvFiles = []string{".env", ".env.dev.local"}
	report.entries = append(report.entries, bootEntry{key: "server.port", value: "9002", source: "file_env:.env.dev.local"})
	rendered := renderBootTree(report, "", bootStyle{})
	if !strings.Contains(rendered, "config.dev.toml · .env, .env.dev.local") {
		t.Fatalf("the banner does not name the dotenv files:\n%s", rendered)
	}
	if got := bootSourceTag("file_env:.env.dev.local"); got != ".env.dev.local" {
		t.Fatalf("source tag = %q", got)
	}
	attrs := bootRecordAttrs(report, "")
	var found bool
	for _, attribute := range attrs {
		if attribute.Key == "dotenv_files" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the record carries no dotenv_files attribute: %v", attrs)
	}
}
