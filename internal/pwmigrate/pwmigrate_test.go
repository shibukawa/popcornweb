package pwmigrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMigrations(t *testing.T, files map[string]string) string {
	t.Helper()
	directory := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return directory
}

const initialMigration = `-- +goose Up
CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, avatar BLOB, score REAL);
INSERT INTO users (name, avatar, score) VALUES ('it''s me', x'00ff41', 0.1);

-- +goose Down
DROP TABLE users;
`

const secondMigration = `-- +goose Up
CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id), body TEXT);
CREATE INDEX idx_posts_user ON posts(user_id);
CREATE VIEW recent AS SELECT id FROM posts;
-- +goose StatementBegin
CREATE TRIGGER bump AFTER INSERT ON posts BEGIN UPDATE users SET score = score + 1 WHERE id = NEW.user_id; END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER bump;
DROP VIEW recent;
DROP TABLE posts;
`

func fullSources(t *testing.T) string {
	return writeMigrations(t, map[string]string{
		"00001_init.sql":  initialMigration,
		"00002_posts.sql": secondMigration,
	})
}

func openTarget(t *testing.T, dsn string) *Target {
	t.Helper()
	target, err := Open(dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	t.Cleanup(func() { _ = target.Close() })
	return target
}

func TestApplyUpAndDown(t *testing.T) {
	directory := fullSources(t)
	sources, err := Sources(directory)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	target := openTarget(t, "sqlite://"+filepath.Join(t.TempDir(), "app.db"))

	result, err := Apply(context.Background(), target, sources, ActionUp, 0)
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if result.Previous != 0 || result.Current != 2 {
		t.Fatalf("up versions = %d -> %d, want 0 -> 2", result.Previous, result.Current)
	}
	if len(result.Applied) != 2 {
		t.Fatalf("applied = %d, want 2", len(result.Applied))
	}

	again, err := Apply(context.Background(), target, sources, ActionUp, 0)
	if err != nil {
		t.Fatalf("second up: %v", err)
	}
	if len(again.Applied) != 0 || again.Current != 2 {
		t.Fatalf("second up applied %d at version %d, want 0 at 2", len(again.Applied), again.Current)
	}

	down, err := Apply(context.Background(), target, sources, ActionDown, 0)
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if down.Previous != 2 || down.Current != 1 {
		t.Fatalf("down versions = %d -> %d, want 2 -> 1", down.Previous, down.Current)
	}

	zero, err := Apply(context.Background(), target, sources, ActionDownTo, 0)
	if err != nil {
		t.Fatalf("down-to 0: %v", err)
	}
	if zero.Current != 0 {
		t.Fatalf("down-to 0 left version %d", zero.Current)
	}
}

func TestStatusesAndPending(t *testing.T) {
	directory := fullSources(t)
	sources, err := Sources(directory)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	target := openTarget(t, "sqlite://"+filepath.Join(t.TempDir(), "app.db"))

	pending, err := Pending(context.Background(), target, sources)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending = %d, want 2", len(pending))
	}
	if _, err := Apply(context.Background(), target, sources, ActionUpByOne, 0); err != nil {
		t.Fatalf("up-by-one: %v", err)
	}
	statuses, err := Statuses(context.Background(), target, sources)
	if err != nil {
		t.Fatalf("statuses: %v", err)
	}
	if len(statuses) != 2 || !statuses[0].Applied || statuses[1].Applied {
		t.Fatalf("statuses = %+v, want only the first applied", statuses)
	}
}

func TestValidateRejectsBrokenSource(t *testing.T) {
	directory := writeMigrations(t, map[string]string{
		"00001_init.sql": initialMigration,
	})
	sources, err := Sources(directory)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	listed, err := Validate(sources)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(listed) != 1 || listed[0].Version != 1 {
		t.Fatalf("validate = %+v, want version 1", listed)
	}

	duplicate := writeMigrations(t, map[string]string{
		"00001_init.sql":  initialMigration,
		"00001_other.sql": initialMigration,
	})
	duplicateSources, err := Sources(duplicate)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	if _, err := Validate(duplicateSources); err == nil {
		t.Fatal("validate accepted duplicate versions")
	}
}

func TestCreateNumbersSequentially(t *testing.T) {
	directory := writeMigrations(t, map[string]string{"00001_init.sql": initialMigration})
	path, err := Create(directory, "add_posts")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if filepath.Base(path) != "00002_add_posts.sql" {
		t.Fatalf("created %s, want 00002_add_posts.sql", filepath.Base(path))
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read created migration: %v", err)
	}
	if !strings.Contains(string(body), "-- +goose Up") || !strings.Contains(string(body), "-- +goose Down") {
		t.Fatalf("created migration lacks goose markers:\n%s", body)
	}
	if _, err := Create(directory, "bad name"); err == nil {
		t.Fatal("create accepted an invalid name")
	}
}

func TestParseDSN(t *testing.T) {
	for _, testCase := range []struct {
		dsn        string
		dialect    string
		dataSource string
		goose      string
	}{
		// SQLite and MySQL lose the scheme, because neither data source is a
		// URL; a libpq URL is already a DSN and keeps it.
		{dsn: "sqlite://app.db", dialect: "sqlite", dataSource: "app.db", goose: "sqlite3"},
		{dsn: "sqlite3://app.db", dialect: "sqlite", dataSource: "app.db", goose: "sqlite3"},
		{
			dsn:        "postgres://user:pass@127.0.0.1:5432/app?sslmode=disable",
			dialect:    "postgres",
			dataSource: "postgres://user:pass@127.0.0.1:5432/app?sslmode=disable",
			goose:      "postgres",
		},
		{
			dsn:        "postgresql://user:pass@127.0.0.1:5432/app",
			dialect:    "postgres",
			dataSource: "postgresql://user:pass@127.0.0.1:5432/app",
			goose:      "postgres",
		},
		{
			dsn:        "mysql://user:pass@tcp(127.0.0.1:3306)/app",
			dialect:    "mysql",
			dataSource: "user:pass@tcp(127.0.0.1:3306)/app",
			goose:      "mysql",
		},
	} {
		target, dialect, err := ParseDSN(testCase.dsn)
		if err != nil {
			t.Fatalf("parse %q: %v", testCase.dsn, err)
		}
		if target.Dialect != testCase.dialect || target.DataSource != testCase.dataSource ||
			string(dialect) != testCase.goose {
			t.Fatalf("parse %q = %q %q %q", testCase.dsn, target.Dialect, target.DataSource, dialect)
		}
	}
	if _, _, err := ParseDSN("app.db"); err == nil {
		t.Fatal("parse accepted a DSN without a scheme")
	}
	if _, _, err := ParseDSN("cassandra://host"); err == nil {
		t.Fatal("parse accepted an unsupported driver")
	}
}

// TestSnapshotRoundTrip asserts a replayed snapshot is indistinguishable from a
// directly migrated database, including recorded versions and AUTOINCREMENT
// counters.
func TestSnapshotRoundTrip(t *testing.T) {
	directory := fullSources(t)
	sources, err := Sources(directory)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	script, err := Snapshot(context.Background(), sources)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	migrated := openTarget(t, "sqlite://"+filepath.Join(t.TempDir(), "migrated.db"))
	if _, err := Apply(context.Background(), migrated, sources, ActionUp, 0); err != nil {
		t.Fatalf("apply: %v", err)
	}

	replayed, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open memory database: %v", err)
	}
	defer replayed.Close()
	if err := Replay(context.Background(), replayed, script); err != nil {
		t.Fatalf("replay: %v", err)
	}

	wantDump, err := Dump(context.Background(), migrated.DB)
	if err != nil {
		t.Fatalf("dump migrated: %v", err)
	}
	gotDump, err := Dump(context.Background(), replayed)
	if err != nil {
		t.Fatalf("dump replayed: %v", err)
	}
	if normalizeDump(gotDump) != normalizeDump(wantDump) {
		t.Fatalf("replayed database differs\n--- replayed ---\n%s\n--- migrated ---\n%s", gotDump, wantDump)
	}
	// The AUTOINCREMENT counter must survive so a new row cannot reuse id 1.
	if _, err := replayed.Exec("INSERT INTO users (name) VALUES ('next')"); err != nil {
		t.Fatalf("insert after replay: %v", err)
	}
	var nextID int64
	if err := replayed.QueryRow("SELECT id FROM users WHERE name = 'next'").Scan(&nextID); err != nil {
		t.Fatalf("read new id: %v", err)
	}
	if nextID != 2 {
		t.Fatalf("new id = %d, want 2", nextID)
	}

	// Recorded versions must make the replayed database look fully migrated.
	target := AttachSQLite(replayed)
	version, err := Version(context.Background(), target, sources)
	if err != nil {
		t.Fatalf("version after replay: %v", err)
	}
	if version != 2 {
		t.Fatalf("version after replay = %d, want 2", version)
	}
	pending, err := Pending(context.Background(), target, sources)
	if err != nil {
		t.Fatalf("pending after replay: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after replay = %d, want 0", len(pending))
	}
}

// normalizeDump drops the goose timestamp column, which records wall-clock time.
func normalizeDump(dump string) string {
	lines := strings.Split(dump, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, `INSERT INTO "goose_db_version" VALUES(`) {
			if index := strings.LastIndex(line, ","); index >= 0 {
				line = line[:index] + ");"
			}
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func TestSnapshotIsDeterministic(t *testing.T) {
	directory := fullSources(t)
	sources, err := Sources(directory)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	first, err := Snapshot(context.Background(), sources)
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	second, err := Snapshot(context.Background(), sources)
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if normalizeDump(first) != normalizeDump(second) {
		t.Fatalf("snapshots differ\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}
