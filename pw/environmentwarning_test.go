package pw

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwobservability"
	"github.com/shibukawa/popcornweb/pwruntime"
)

// captureProcessLog installs a backend that records what the framework says
// about itself at startup, and puts the previous one back afterwards.
func captureProcessLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var recorded bytes.Buffer
	handler := slog.NewTextHandler(&recorded, &slog.HandlerOptions{Level: slog.LevelDebug})
	t.Cleanup(pwobservability.SwapProcessBackend(
		pwruntime.NewLogBackend(pwruntime.LevelDebug, pwruntime.NewSlogSink(handler))))
	return &recorded
}

// Development is not a neutral default — it decides whether query records carry
// bind values and whether a session cookie may travel without Secure — so a
// process that landed there because nobody said otherwise has to say so.
func TestAnUnnamedEnvironmentIsAnnouncedAtStartup(t *testing.T) {
	recorded := captureProcessLog(t)
	swapEnvForTest(t, DefaultEnv, false)

	reportEnvironment()

	output := recorded.String()
	if output == "" {
		t.Fatal("an unnamed environment produced no startup warning")
	}
	if !strings.Contains(output, "level=WARN") {
		t.Errorf("the notice is not a warning: %s", output)
	}
	// It has to name the variable to set and what the silence bought, or it is a
	// line an operator scrolls past.
	for _, fragment := range []string{EnvVar, "development", "bind values", "cookie.secure"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("the warning does not mention %q: %s", fragment, output)
		}
	}
}

// A deployment that named its environment gets nothing: a warning printed on
// every correct startup is one that stops being read.
func TestANamedEnvironmentIsSilent(t *testing.T) {
	const environment = "live"
	recorded := captureProcessLog(t)
	swapEnvForTest(t, environment, true)

	reportEnvironment()

	if output := recorded.String(); output != "" {
		t.Errorf("APP_ENV=%q still warned: %s", environment, output)
	}
}
