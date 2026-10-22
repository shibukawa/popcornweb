package pwcli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeCheckoutMarker makes directory the top of a checkout by writing the
// .git file git leaves there, naming adminDir as the administrative directory.
func writeCheckoutMarker(t *testing.T, directory, adminDir string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(directory, ".git"), "gitdir: "+filepath.ToSlash(adminDir)+"\n")
}

// writeLinkedWorktree is writeCheckoutMarker for what `git worktree add` makes:
// the administrative directory sits under <repository>/.git/worktrees.
func writeLinkedWorktree(t *testing.T, directory, repository string) {
	t.Helper()
	writeCheckoutMarker(t, directory, filepath.Join(repository, ".git", "worktrees", filepath.Base(directory)))
}

// Only a linked worktree is another working copy of the same project. A
// submodule has a .git file of the same shape, and it is part of the project
// that contains it — listed in generate.*, or imported by the build — so
// leaving it out of a scan would hide real sources.
func TestLinkedWorktreeRecognisesOnlyLinkedWorktrees(t *testing.T) {
	repository := t.TempDir()
	for _, testcase := range []struct {
		name  string
		write func(t *testing.T, directory string)
		want  bool
	}{
		{"linked worktree", func(t *testing.T, directory string) {
			writeLinkedWorktree(t, directory, repository)
		}, true},
		{"worktree named by a relative path", func(t *testing.T, directory string) {
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(directory, ".git"), "gitdir: ../../.git/worktrees/checkout\n")
		}, true},
		{"worktree file written with CRLF", func(t *testing.T, directory string) {
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(directory, ".git"), "gitdir: /repo/.git/worktrees/checkout\r\n")
		}, true},
		{"submodule", func(t *testing.T, directory string) {
			writeCheckoutMarker(t, directory, filepath.Join(repository, ".git", "modules", "checkout"))
		}, false},
		{"clone with a .git directory", func(t *testing.T, directory string) {
			if err := os.MkdirAll(filepath.Join(directory, ".git", "worktrees"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"directory without .git", func(t *testing.T, directory string) {
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
		}, false},
		{".git file that names no git directory", func(t *testing.T, directory string) {
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(directory, ".git"), "not a link to anything\n")
		}, false},
		{"empty .git file", func(t *testing.T, directory string) {
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(directory, ".git"), "")
		}, false},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "checkout")
			testcase.write(t, directory)
			if got := linkedWorktree(directory); got != testcase.want {
				t.Fatalf("linkedWorktree = %v, want %v", got, testcase.want)
			}
		})
	}
}

// pw dev run inside a worktree is working on that checkout. Its root carries
// the same .git file as any other worktree, and a scan that skipped it would
// find nothing at all.
func TestNestedWorktreeIsNeverTheWalkRoot(t *testing.T) {
	repository := t.TempDir()
	root := filepath.Join(repository, ".claude", "worktrees", "current")
	writeLinkedWorktree(t, root, repository)
	nested := filepath.Join(root, ".claude", "worktrees", "other")
	writeLinkedWorktree(t, nested, repository)

	if nestedWorktree(root, root) {
		t.Fatal("the checkout being worked on was treated as another one")
	}
	if !nestedWorktree(root, nested) {
		t.Fatal("a worktree below the root was not recognised")
	}
}

// otherWorktreeProject is a project that keeps a second checkout of itself
// below its root, the way Claude Code does under .claude/worktrees, plus a
// submodule that belongs to the project. Each checkout carries what
// scans look for: generated Go no purpose lists, a template outside
// generate.templates, a classic script tag, and a Go source.
func otherWorktreeProject(t *testing.T) (root, worktree, submodule string) {
	t.Helper()
	root = t.TempDir()
	worktree = filepath.Join(root, ".claude", "worktrees", "feature")
	submodule = filepath.Join(root, "third_party", "ui")
	writeLinkedWorktree(t, worktree, root)
	writeCheckoutMarker(t, submodule, filepath.Join(root, ".git", "modules", "ui"))

	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/fixture\n\ngo 1.26.0\n")
	writeTestFile(t, filepath.Join(root, "popcornweb.toml"),
		"[project]\nname = \"fixture\"\nmain = \"./cmd/fixture\"\n\n[generate]\n"+
			"handlers = [\"handlers\"]\ntemplates = [\"handlers\"]\nqueries = []\nconfig = []\n")
	for _, checkout := range []string{root, worktree, submodule} {
		for _, directory := range []string{"handlers", "samples"} {
			if err := os.MkdirAll(filepath.Join(checkout, directory), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		writeTestFile(t, filepath.Join(checkout, "handlers", "home.go"), "package handlers\n")
		writeTestFile(t, filepath.Join(checkout, "samples", "stale_pw_gen.go"), "package samples\n")
		writeTestFile(t, filepath.Join(checkout, "samples", "home.pw.html"),
			"<script src=\"/public/js/app.ts\"></script>\n")
	}
	return root, worktree, submodule
}

// This is the warning pw dev printed for every generated file and every
// template of every worktree under .claude/worktrees on each regeneration.
func TestReportSourcesOutsideScopeSkipsOtherWorktrees(t *testing.T) {
	root, _, _ := otherWorktreeProject(t)
	config, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}

	var output strings.Builder
	if err := reportSourcesOutsideScope(root, config, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), ".claude/worktrees") {
		t.Fatalf("another checkout was scanned:\n%s", output.String())
	}
	// The same files in the project itself are still found: skipping a checkout
	// must not have turned the report off.
	for _, want := range []string{
		"samples/stale_pw_gen.go was generated outside every generate purpose",
		"samples/home.pw.html is outside generate.templates",
	} {
		if !strings.Contains(output.String(), "pw: "+want) {
			t.Errorf("the project's own stray is no longer reported, want %q:\n%s", want, output.String())
		}
	}
	// A submodule is part of the project, so what it holds is judged like the
	// rest of the tree.
	if !strings.Contains(output.String(), "third_party/ui/samples/stale_pw_gen.go was generated outside") {
		t.Errorf("a submodule was left out of the report:\n%s", output.String())
	}
}

// The watcher counts every Go source as a rebuild input, so a worktree's
// sources would restart a server that does not run them each time another
// session edits one.
func TestSnapshotWatchFilesSkipsOtherWorktrees(t *testing.T) {
	root, worktree, submodule := otherWorktreeProject(t)

	state, err := snapshotWatchFiles(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for path := range state {
		if pathWithin(worktree, path) {
			t.Errorf("a file of another checkout is watched: %s", path)
		}
	}
	for _, kept := range []string{
		filepath.Join(root, "handlers", "home.go"),
		filepath.Join(root, "popcornweb.toml"),
		// A module replaced by a submodule is edited alongside the application.
		filepath.Join(submodule, "handlers", "home.go"),
	} {
		if _, ok := state[kept]; !ok {
			t.Errorf("%s is no longer watched", kept)
		}
	}
}

// A template of another checkout is its author's business: refusing the
// project's build over a script tag the project does not contain is a failure
// nobody can find by reading the project.
func TestScriptModuleTagSkipsOtherWorktrees(t *testing.T) {
	root, _, _ := otherWorktreeProject(t)
	// The project's own templates are fine; only the worktree's tag is a classic
	// one, so the scan passes only if it never read the worktree.
	for _, directory := range []string{"handlers", "samples"} {
		writeTestFile(t, filepath.Join(root, directory, "home.pw.html"),
			"<script type=\"module\" src=\"/public/js/app.ts\"></script>\n")
	}
	writeTestFile(t, filepath.Join(root, "third_party", "ui", "handlers", "home.pw.html"),
		"<script type=\"module\" src=\"/public/js/app.ts\"></script>\n")
	writeTestFile(t, filepath.Join(root, "third_party", "ui", "samples", "home.pw.html"),
		"<script type=\"module\" src=\"/public/js/app.ts\"></script>\n")

	if err := verifyScriptModuleTags(root); err != nil {
		t.Fatalf("a template of another checkout was read: %v", err)
	}
}

// Asset retention is decided by reading every template and source in the tree.
// Reading another checkout's would keep alive an asset the project no longer
// references, for as long as that checkout still does.
func TestRetentionScannerSkipsOtherWorktrees(t *testing.T) {
	root, _, _ := otherWorktreeProject(t)

	scanner, err := newRetentionScanner(root, assetsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, source := range scanner.sources {
		names = append(names, source.name)
		if strings.HasPrefix(source.name, ".claude/") {
			t.Errorf("a source of another checkout was scanned: %s", source.name)
		}
	}
	for _, want := range []string{"samples/home.pw.html", "handlers/home.go"} {
		if !slices.Contains(names, want) {
			t.Errorf("the project's own %s is no longer scanned: %v", want, names)
		}
	}
}

// pw doctor pairs every generated file with the source beside it. A worktree
// holds both, but its generated files are judged against its own sources by
// its own run, not as orphans of this one.
func TestDoctorGeneratedScanSkipsOtherWorktrees(t *testing.T) {
	root, _, _ := otherWorktreeProject(t)
	scan := &projectScan{root: root, gitTracked: map[string]bool{}, configFileModes: map[string]os.FileMode{}, devboxVersions: map[string]string{}}
	scan.scanGenerated()

	var paths []string
	for _, generated := range scan.generated {
		paths = append(paths, generated.Path)
		if strings.HasPrefix(generated.Path, ".claude/") {
			t.Errorf("a generated file of another checkout was diagnosed: %s", generated.Path)
		}
	}
	if !slices.Contains(paths, "samples/stale_pw_gen.go") {
		t.Errorf("the project's own generated files are no longer diagnosed: %v", paths)
	}
}
