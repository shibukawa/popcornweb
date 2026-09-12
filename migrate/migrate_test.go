package migrate_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/migrate"
	_ "github.com/shibukawa/tinygodriver/database/sql/sqlite"
)

const counterMigration = `-- +goose Up
CREATE TABLE counter (id INTEGER PRIMARY KEY AUTOINCREMENT, value INTEGER NOT NULL);
INSERT INTO counter (value) VALUES (7);

-- +goose Down
DROP TABLE counter;
`

// migrationDir writes a migration tree and, on the delegated build, makes the pw
// command the child process needs available on PATH.
func migrationDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "00001_counter.sql"), []byte(counterMigration), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	if migrate.Delegated {
		requirePWOnPath(t)
	}
	return directory
}

func requirePWOnPath(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(binDir, "pw"), "github.com/shibukawa/popcornweb/cmd/pw")
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pw for the delegated path: %v\n%s", err, output)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestSnapshotProducesReplayableSchema(t *testing.T) {
	directory := migrationDir(t)
	for _, source := range []struct {
		name   string
		option migrate.Option
	}{
		{name: "directory", option: migrate.WithDir(directory)},
		{name: "filesystem", option: migrate.WithFS(os.DirFS(directory))},
	} {
		t.Run(source.name, func(t *testing.T) {
			script, err := migrate.Snapshot(context.Background(), source.option)
			if err != nil {
				t.Fatalf("snapshot: %v", err)
			}
			if !strings.Contains(script, "CREATE TABLE counter") {
				t.Fatalf("snapshot lacks the migrated table:\n%s", script)
			}

			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatalf("open memory database: %v", err)
			}
			defer db.Close()
			if err := migrate.Replay(context.Background(), db, script); err != nil {
				t.Fatalf("replay: %v", err)
			}
			var value int
			if err := db.QueryRow("SELECT value FROM counter").Scan(&value); err != nil {
				t.Fatalf("read replayed row: %v", err)
			}
			if value != 7 {
				t.Fatalf("value = %d, want 7", value)
			}
		})
	}
}

func TestUpAndStatus(t *testing.T) {
	directory := migrationDir(t)
	dsn := "sqlite://" + filepath.Join(t.TempDir(), "app.db")

	result, err := migrate.Up(context.Background(), dsn, migrate.WithDir(directory))
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if result.Previous != 0 || result.Current != 1 {
		t.Fatalf("versions = %d -> %d, want 0 -> 1", result.Previous, result.Current)
	}

	states, err := migrate.Status(context.Background(), dsn, migrate.WithDir(directory))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(states) != 1 || !states[0].Applied || states[0].Version != 1 {
		t.Fatalf("states = %+v, want version 1 applied", states)
	}
}

func TestSourceHashChangesWithContent(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "00001_counter.sql"), []byte(counterMigration), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	first, err := migrate.SourceHash(migrate.WithDir(directory))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	repeat, err := migrate.SourceHash(migrate.WithDir(directory))
	if err != nil {
		t.Fatalf("hash again: %v", err)
	}
	if first != repeat {
		t.Fatal("hash is not stable for unchanged sources")
	}
	if err := os.WriteFile(filepath.Join(directory, "00002_more.sql"),
		[]byte("-- +goose Up\nSELECT 1;\n\n-- +goose Down\nSELECT 1;\n"), 0o644); err != nil {
		t.Fatalf("write second migration: %v", err)
	}
	changed, err := migrate.SourceHash(migrate.WithDir(directory))
	if err != nil {
		t.Fatalf("hash after change: %v", err)
	}
	if changed == first {
		t.Fatal("hash did not change after a migration was added")
	}
}

func TestOptionValidation(t *testing.T) {
	if _, err := migrate.Snapshot(context.Background(), migrate.WithDir("  ")); err == nil {
		t.Fatal("empty directory was accepted")
	}
	if _, err := migrate.Snapshot(context.Background(), migrate.WithFS(nil)); err == nil {
		t.Fatal("nil filesystem was accepted")
	}
	if err := migrate.Replay(context.Background(), nil, "SELECT 1;"); err == nil {
		t.Fatal("nil database was accepted")
	}
}
