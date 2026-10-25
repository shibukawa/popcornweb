package pwcli

import (
	"path/filepath"
	"testing"
)

// renderCompareConfig is the project shape of internal/rendercompare: one
// directory of component templates and nothing else. Like the other fixtures it
// has no popcornweb.toml of its own, because a second project root inside this
// module would be found by any command run from a subdirectory.
func renderCompareConfig(t *testing.T) (string, projectConfig) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "rendercompare"))
	if err != nil {
		t.Fatal(err)
	}
	return root, projectConfig{Generate: generationScope{Templates: []string{"."}}}
}

// The generated component is committed, so a change to the emitter shows up as a
// diff a reviewer reads. For this fixture the diff matters twice: the page that
// quotes the render comparison is measuring exactly this file.
//
//	go test ./internal/pwcli -run RenderCompare -update
func TestRenderCompareGeneratedFileIsCurrent(t *testing.T) {
	root, config := renderCompareConfig(t)
	changes, err := planFixture(t, root, config)
	if err != nil {
		t.Fatal(err)
	}
	if *updateFixture {
		if err := applyFileChanges(changes); err != nil {
			t.Fatal(err)
		}
		for _, path := range changePaths(root, changes) {
			t.Logf("wrote %s", path)
		}
		return
	}
	if len(changes) > 0 {
		t.Errorf("the render comparison fixture is stale; run go test ./internal/pwcli -run RenderCompare -update\n  %v",
			changePaths(root, changes))
	}
}
