package pwcli

import (
	"path/filepath"
	"testing"
)

// A dotenv edit changes what the next process reads, so it restarts the
// application the way a config.{env}.toml edit does; the template is read by
// nothing and is left alone.
func TestSnapshotWatchFilesIncludesDotenvFilesButNotTheTemplate(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".env", ".env.local", ".env.dev", ".env.dev.local", ".env.example"} {
		writeTestFile(t, filepath.Join(root, name), "A=1\n")
	}
	state, err := snapshotWatchFiles(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".env", ".env.local", ".env.dev", ".env.dev.local"} {
		if _, ok := state[filepath.Join(root, name)]; !ok {
			t.Errorf("%s must be watched", name)
		}
	}
	if _, ok := state[filepath.Join(root, ".env.example")]; ok {
		t.Error("the template must not be watched")
	}
}
