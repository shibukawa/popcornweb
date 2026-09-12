//go:build pwdev

package pw

import (
	"github.com/shibukawa/popcornweb/pwbrowser"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The set is resolved once per process, so a test that needs a console address
// has to be the one that sets it. These run in their own process because the
// build tag selects them.
func TestDevelopmentModuleIsServedBesideTheCore(t *testing.T) {
	if developmentConsoleURL() == "" {
		t.Skip("no console address in the environment; see TestMain")
	}
	scripts := pwbrowser.Scripts()
	if _, ok := scripts[developmentModuleName]; !ok {
		t.Fatalf("scripts = %v, want the development module", scripts)
	}
	core := scripts[pwbrowser.RuntimeName]
	if !strings.Contains(core, `import("./`+developmentModuleName+`")`) {
		t.Error("the core does not import the development module")
	}
	// The import is relative, so it resolves inside the revision directory the
	// core was served from and nothing has to rewrite it.
	if strings.Contains(core, `import("/`) {
		t.Error("the development import is absolute; it must stay relative to the revision directory")
	}
}

func TestDevelopmentModuleIsFetchable(t *testing.T) {
	if developmentConsoleURL() == "" {
		t.Skip("no console address in the environment")
	}
	recorder := httptest.NewRecorder()
	path := pwbrowser.ScriptURL(developmentModuleName)
	if !serveFrameworkScript(recorder, httptest.NewRequest(http.MethodGet, path, nil)) {
		t.Fatalf("%s was not claimed", path)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, developmentConsoleURL()) {
		t.Error("the console address was not baked into the served module")
	}
	if !strings.Contains(body, "/api/loop-state/stream") {
		t.Error("the module does not subscribe to the loop state stream")
	}
}

// The overlay renders the diagnostic as text and the module never builds markup
// out of it, so a diagnostic quoting the developer's own HTML is read rather
// than run.
func TestOverlayWritesTheDiagnosticAsText(t *testing.T) {
	module := testModule("http://127.0.0.1:18081", true)
	if strings.Contains(module, "innerHTML") {
		t.Error("the overlay assigns innerHTML somewhere")
	}
	if !strings.Contains(module, "text.textContent = state.diagnostic") {
		t.Error("the diagnostic is not written as text")
	}
}

func TestReloadSwitchReachesTheModule(t *testing.T) {
	if !strings.Contains(testModule("http://c", true), "reloadOnRecovery = true") {
		t.Error("reload was not enabled in the module")
	}
	if !strings.Contains(testModule("http://c", false), "reloadOnRecovery = false") {
		t.Error("reload was not disabled in the module")
	}
}

// testModule builds a module with both halves on, which is what a project that
// configured nothing gets.
func testModule(console string, reload bool) string {
	return developmentModule(console, reload, true, true, DevLauncherBottomLeft)
}

func TestReloadIsOnUnlessTurnedOff(t *testing.T) {
	t.Setenv(DevConsoleReloadVar, "")
	if !developmentReload() {
		t.Error("an unset variable disabled reload")
	}
	for _, off := range []string{"0", "false"} {
		t.Setenv(DevConsoleReloadVar, off)
		if developmentReload() {
			t.Errorf("%q did not disable reload", off)
		}
	}
}

// The address is quoted as JSON rather than concatenated, so a value carrying a
// quote cannot close the string and continue as code.
func TestConsoleAddressIsQuotedIntoTheModule(t *testing.T) {
	module := testModule(`http://x"+alert(1)+"`, true)
	if strings.Contains(module, `"+alert(1)+"`) {
		t.Errorf("the address was spliced in unquoted:\n%s", module)
	}
	if !strings.Contains(module, `\"`) {
		t.Errorf("the address was not escaped:\n%s", module)
	}
}

// With no console running there is nothing for the module to talk to, so the
// set and the revision are the release ones.
func TestNoConsoleMeansNoDevelopmentModule(t *testing.T) {
	t.Setenv(DevConsoleURLVar, "")
	if developmentImport() != "" {
		t.Error("an import was emitted with no console address")
	}
	if scripts := developmentScripts(); len(scripts) != 0 {
		t.Errorf("scripts = %v, want none", scripts)
	}
}

// The console address is injected whenever the console runs, because the data
// pane announces to it too. The overlay therefore needs a switch of its own, or
// turning it off would stop working the moment another pane needed the address.
func TestBothSwitchesOffMeansNoModuleAtAll(t *testing.T) {
	if developmentConsoleURL() == "" {
		t.Skip("no console address in the environment")
	}
	t.Setenv(DevConsoleOverlayVar, "0")
	t.Setenv(DevConsoleLauncherVar, "0")
	if developmentImport() != "" {
		t.Error("the core still imported the module with both halves turned off")
	}
	if scripts := developmentScripts(); len(scripts) != 0 {
		t.Errorf("scripts = %v, want none with both halves turned off", scripts)
	}
}

// Either half alone is enough to want the module, so one switch never silently
// takes the other's feature with it.
func TestEitherHalfAloneKeepsTheModule(t *testing.T) {
	if developmentConsoleURL() == "" {
		t.Skip("no console address in the environment")
	}
	for _, off := range []string{DevConsoleOverlayVar, DevConsoleLauncherVar} {
		t.Run(off, func(t *testing.T) {
			t.Setenv(off, "0")
			if developmentImport() == "" {
				t.Error("the core dropped the module with only one half turned off")
			}
			if _, ok := developmentScripts()[developmentModuleName]; !ok {
				t.Error("the module was not served with only one half turned off")
			}
		})
	}
}

// The disabled half is absent from the bytes rather than switched off inside
// them, so a project that turned one off is served a module that cannot run it.
func TestADisabledHalfIsOffInTheServedBytes(t *testing.T) {
	if !strings.Contains(developmentModule("http://c", true, false, true, DevLauncherBottomLeft), "overlayEnabled = false") {
		t.Error("the overlay was not disabled in the module")
	}
	if !strings.Contains(developmentModule("http://c", true, true, false, DevLauncherBottomLeft), "launcherEnabled = false") {
		t.Error("the launcher was not disabled in the module")
	}
}

// The mark is served only when something references it, because every name in
// the set feeds the revision digest and an asset nobody loads would still move
// every URL.
func TestTheMarkIsServedOnlyForTheLauncher(t *testing.T) {
	if developmentConsoleURL() == "" {
		t.Skip("no console address in the environment")
	}
	if _, ok := developmentScripts()[developmentMarkName]; !ok {
		t.Error("the mark was not served with the launcher enabled")
	}
	t.Setenv(DevConsoleLauncherVar, "0")
	if _, ok := developmentScripts()[developmentMarkName]; ok {
		t.Error("the mark was served with the launcher turned off")
	}
}

// The mark is fetched from the revision directory the module was served from,
// which it can only learn from its own URL: a path written into the bytes would
// have to hold the digest of the bytes it is written into.
func TestTheMarkIsResolvedAgainstTheModuleURL(t *testing.T) {
	module := testModule("http://c", true)
	if !strings.Contains(module, `new URL("./`+developmentMarkName+`", import.meta.url)`) {
		t.Error("the mark URL is not resolved against import.meta.url")
	}
}

func TestTheMarkIsServedAsAnImage(t *testing.T) {
	if developmentConsoleURL() == "" {
		t.Skip("no console address in the environment")
	}
	recorder := httptest.NewRecorder()
	path := pwbrowser.ScriptURL(developmentMarkName)
	if !serveFrameworkScript(recorder, httptest.NewRequest(http.MethodGet, path, nil)) {
		t.Fatalf("%s was not claimed", path)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/webp" {
		t.Errorf("Content-Type = %q, want image/webp", got)
	}
	// A WebP file is RIFF....WEBP, so this proves the embedded bytes reached
	// the response rather than a string of the file name.
	if body := recorder.Body.String(); !strings.HasPrefix(body, "RIFF") || !strings.Contains(body[:16], "WEBP") {
		t.Error("the served bytes are not a WebP file")
	}
}

// The launcher is a link with a named target. rel=noopener would defeat that —
// a browser ignores the name when it is set — so it is deliberately absent.
func TestTheLauncherIsALinkThatReusesItsTab(t *testing.T) {
	module := testModule("http://127.0.0.1:18081", true)
	if !strings.Contains(module, `link.target = "pw-dev-console"`) {
		t.Error("the launcher does not name the tab it opens")
	}
	// Matched on the assignment rather than the word, which the comment above
	// it in the served bytes explains and would otherwise trip this.
	if strings.Contains(module, "link.rel") {
		t.Error("a rel is set on the link; noopener there makes the browser ignore the tab name")
	}
	if !strings.Contains(module, `link.href = consoleURL`) {
		t.Error("the launcher is not an anchor carrying the console address")
	}
}

func TestEachCornerPlacesTheLauncher(t *testing.T) {
	for corner, want := range map[string][3]string{
		DevLauncherBottomLeft:  {"bottom: 1rem; left: 1rem", "flex-direction: row;", "left: 100%"},
		DevLauncherBottomRight: {"bottom: 1rem; right: 1rem", "flex-direction: row-reverse;", "right: 100%"},
		DevLauncherTopLeft:     {"top: 1rem; left: 1rem", "flex-direction: row;", "left: 100%"},
		DevLauncherTopRight:    {"top: 1rem; right: 1rem", "flex-direction: row-reverse;", "right: 100%"},
	} {
		module := developmentModule("http://c", true, true, true, corner)
		for _, fragment := range want {
			if !strings.Contains(module, fragment) {
				t.Errorf("corner %q: module does not contain %q", corner, fragment)
			}
		}
	}
}

// The fixed box is the button and nothing more. Left in the flex row, the label
// and the dismiss control hold their width while invisible, and the box then
// swallows clicks along a strip of the application the developer cannot see.
func TestTheLauncherCoversOnlyItsButton(t *testing.T) {
	module := testModule("http://c", true)
	if !strings.Contains(module, "width: 44px; height: 44px;") {
		t.Error("the fixed box is not sized to the button")
	}
	if !strings.Contains(module, ".flyout { position: absolute;") {
		t.Error("the label and dismiss control are not taken out of flow")
	}
}

// pw dev rejects an unrecognised corner before injecting it, so what reaches
// here is either one of the four or nothing at all.
func TestTheCornerFallsBackToTheDefault(t *testing.T) {
	for _, value := range []string{"", "middle"} {
		t.Setenv(DevConsoleLauncherCornerVar, value)
		if got := developmentLauncherCorner(); got != DevLauncherBottomLeft {
			t.Errorf("corner %q resolved to %q, want %q", value, got, DevLauncherBottomLeft)
		}
	}
	t.Setenv(DevConsoleLauncherCornerVar, DevLauncherTopRight)
	if got := developmentLauncherCorner(); got != DevLauncherTopRight {
		t.Errorf("corner = %q, want %q", got, DevLauncherTopRight)
	}
}

func TestLauncherIsOnUnlessTurnedOff(t *testing.T) {
	t.Setenv(DevConsoleLauncherVar, "")
	if !developmentLauncher() {
		t.Error("an unset variable disabled the launcher")
	}
	for _, off := range []string{"0", "false"} {
		t.Setenv(DevConsoleLauncherVar, off)
		if developmentLauncher() {
			t.Errorf("%q did not disable the launcher", off)
		}
	}
}
