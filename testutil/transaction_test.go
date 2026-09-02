package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/pwruntime"
)

// notesHandler inserts one row through pw.Transaction and absorbs the failure
// of a nested transaction, so a request exercises savepoint nesting.
func notesHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		err := pw.Transaction(r, func(ctx context.Context) error {
			if err := insertNote(ctx, name); err != nil {
				return err
			}
			nested := pw.TransactionContext(ctx, func(ctx context.Context) error {
				if err := insertNote(ctx, name+"-rolled-back"); err != nil {
					return err
				}
				return errRollback
			})
			if nested != errRollback {
				t.Errorf("nested transaction error = %v, want %v", nested, errRollback)
			}
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

var errRollback = &rollbackError{}

type rollbackError struct{}

func (*rollbackError) Error() string { return "rolled back on purpose" }

func insertNote(ctx context.Context, name string) error {
	executor, err := pwruntime.SQLExecutor(ctx)
	if err != nil {
		return err
	}
	_, err = executor.ExecContext(ctx, "INSERT INTO notes (name) VALUES (?)", name)
	return err
}

func countNotes(t *testing.T, ctx context.Context) int {
	t.Helper()
	executor, err := pwruntime.SQLExecutor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := executor.QueryContext(ctx, "SELECT COUNT(*) FROM notes")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	if rows.Next() {
		if err := rows.Scan(&count); err != nil {
			t.Fatal(err)
		}
	}
	return count
}

func sharedMigrationDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	migration := "-- +goose Up\nCREATE TABLE notes (name TEXT NOT NULL);\n\n-- +goose Down\nDROP TABLE notes;\n"
	if err := os.WriteFile(filepath.Join(directory, "00001_notes.sql"), []byte(migration), 0o644); err != nil {
		t.Fatal(err)
	}
	return directory
}

func sharedDatabase(t *testing.T) (dsn string, committed func() int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shared.db")
	return "sqlite://" + path, func() int {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		count := 0
		if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM notes").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
}

func sharedDatabaseConfig(dsn string) func(*Config) {
	return func(config *Config) {
		config.Update(func(value *pw.ServerConfig) {
			value.Public.Enabled = false
		})
		config.Update(func(value *pw.MiddlewareConfig) {
			value.RDB = pw.RDBConfig{
				Enabled: true,
				Connections: []pw.RDBConnectionConfig{{
					DSN:            dsn,
					ConnectTimeout: time.Second,
					MaxOpenConns:   2,
					MaxIdleConns:   2,
				}},
			}
		})
	}
}

func post(t *testing.T, server *Server, name string) {
	t.Helper()
	response, err := server.Client().Get(server.URL + "/?name=" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
}

// Two tests share one database file. Each runs inside its own transaction, so
// neither observes the other's rows and nothing is committed.
func TestRunTransactionIsolatesTests(t *testing.T) {
	dsn, committed := sharedDatabase(t)
	migrationDir := sharedMigrationDir(t)

	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			server := TestRun(t, notesHandler(t), sharedDatabaseConfig(dsn),
				WithMigrations(migrationDir), WithTransaction(true))
			if count := countNotes(t, server.Context()); count != 0 {
				t.Fatalf("rows before request = %d, want 0", count)
			}
			post(t, server, name)
			// The request row is visible inside the test transaction, and the
			// nested transaction that failed left nothing behind.
			if count := countNotes(t, server.Context()); count != 1 {
				t.Fatalf("rows after request = %d, want 1", count)
			}
			if count := committed(); count != 0 {
				t.Fatalf("committed rows during test = %d, want 0", count)
			}
		})
	}
	if count := committed(); count != 0 {
		t.Fatalf("committed rows after tests = %d, want 0", count)
	}
}

// Without the option the application keeps committing to the shared database.
func TestRunWithoutTransactionCommits(t *testing.T) {
	dsn, committed := sharedDatabase(t)
	migrationDir := sharedMigrationDir(t)

	func() {
		server := TestRun(t, notesHandler(t), sharedDatabaseConfig(dsn), WithMigrations(migrationDir))
		post(t, server, "kept")
		server.Close()
	}()
	if count := committed(); count != 1 {
		t.Fatalf("committed rows = %d, want 1", count)
	}
}

// A DSN naming no linked engine is rejected before the pool is opened, and the
// error names the engines this binary can serve.
//
// The savepoint guard behind this check is no longer reachable from a DSN:
// every engine the framework registers supports savepoints, so a driver that
// does not can only arrive through a caller-supplied pool. The guard itself is
// covered by pwruntime.TestSupportsSavepoint.
func TestRunTransactionRejectsUnknownEngine(t *testing.T) {
	stub := &recordingT{TestingT: t}
	TestRun(stub, notesHandler(t), func(config *Config) {
		config.Update(func(value *pw.ServerConfig) {
			value.Public.Enabled = false
		})
		config.Update(func(value *pw.MiddlewareConfig) {
			value.RDB = pw.RDBConfig{
				Enabled: true,
				Connections: []pw.RDBConnectionConfig{{
					DSN:            "unknown-driver://ignored",
					ConnectTimeout: time.Second,
				}},
			}
		})
	}, WithTransaction(true))
	if !strings.Contains(stub.failure, "unknown-driver") {
		t.Fatalf("TestRun failure = %q, want the unserved scheme named", stub.failure)
	}
	if !strings.Contains(stub.failure, "sqlite") {
		t.Fatalf("TestRun failure = %q, want the linked engines named", stub.failure)
	}
}

// recordingT captures the first Fatalf and every Errorf instead of failing the
// running test.
type recordingT struct {
	TestingT
	failure string
	errors  []string
}

func (r *recordingT) Fatalf(format string, args ...any) {
	if r.failure == "" {
		r.failure = fmt.Sprintf(format, args...)
	}
}

func (r *recordingT) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}
