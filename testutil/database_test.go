package testutil

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/database"
	"github.com/shibukawa/popcornweb/database/postgres"
	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/pwdatabase"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/database/pgx"
	"github.com/shibukawa/tinygodriver/database/pgx/pgxpool"
)

func TestSuppliedDatabasesStayIsolatedAndOwnedAfterFailure(t *testing.T) {
	if err := pw.ParseConfig(); err != nil {
		t.Fatal(err)
	}
	before := pw.ConfigContext[pw.MiddlewareConfig](nil)
	processSet := pwdatabase.Connections()
	open := func(n int) pwruntime.Connection {
		c, err := pwdatabase.OpenOne(pw.RDBConnectionConfig{DSN: "sqlite://:memory:", ConnectTimeout: time.Second, MaxOpenConns: 1, MaxIdleConns: 1}, "supplied")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if _, err := c.DB.Exec(fmt.Sprintf("CREATE TABLE counter(value INTEGER); INSERT INTO counter VALUES (%d)", n)); err != nil {
			t.Fatal(err)
		}
		return c
	}
	first, second := open(1), open(2)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		db, _ := pw.DB(r)
		var n int
		if err := db.QueryRow("SELECT value FROM counter").Scan(&n); err != nil {
			t.Error(err)
			return
		}
		fmt.Fprint(w, n)
	})
	configure := withUndialableDatabase("sqlite:///no/such/directory/db")
	a := TestRun(t, handler, configure, WithDatabase(first))
	b := TestRun(t, handler, configure, WithDatabase(second))
	if getBody(t, a) != "1" || getBody(t, b) != "2" || getBody(t, a) != "1" {
		t.Fatal("databases crossed between servers")
	}
	a.Close()
	b.Close()
	if !reflect.DeepEqual(before, pw.ConfigContext[pw.MiddlewareConfig](nil)) || pwdatabase.Connections() != processSet {
		t.Fatal("TestRun changed process state")
	}
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "1_broken.sql"), []byte("-- +goose Up\nNOT VALID SQL;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := &recordingT{TestingT: t}
	if s := TestRun(stub, handler, configure, WithDatabase(first), WithMigrations(broken)); s != nil || stub.failure == "" {
		t.Fatal("invalid migration was accepted")
	}
	if err := first.DB.Ping(); err != nil {
		t.Fatalf("setup failure closed caller pool: %v", err)
	}
	// A non-SQLite SQL handle without a DSN must not dial a placeholder or
	// the configured database while preparing migrations.
	noDSN := first
	noDSN.Driver, noDSN.DataSource = "postgres", ""
	stub = &recordingT{TestingT: t}
	if s := TestRun(stub, handler, configure, WithDatabase(noDSN), WithMigrations(broken)); s != nil || !strings.Contains(stub.failure, "needs DataSource for migrations") {
		t.Fatalf("missing migration DSN: %s", stub.failure)
	}
}

type missingToolingNative struct {
	database.NativeDB
	closed int
}

func (c *missingToolingNative) Close() error { c.closed++; return nil }

func TestSuppliedDatabaseValidation(t *testing.T) {
	native := &missingToolingNative{}
	for _, tc := range []struct {
		name       string
		configure  func(*Config)
		connection pwruntime.Connection
		want       string
	}{
		{"nil", nil, pwruntime.Connection{Driver: "postgres"}, "nil pool"},
		{"missing-driver", nil, pwruntime.Connection{Native: native}, "needs a Driver"},
		{"wrong-tooling-driver", nil, pwruntime.Connection{Driver: "postgres", Native: native, DataSource: "sqlite://:memory:"}, "does not match"},
		{"disabled", func(c *Config) { c.Update(func(m *pw.MiddlewareConfig) { m.RDB.Enabled = false }) }, pwruntime.Connection{Driver: "postgres", Native: native}, "middleware.rdb.enabled"},
		{"missing-tooling-dsn", withUndialableDatabase("postgres://unused"), pwruntime.Connection{Driver: "postgres", Native: native}, "needs DataSource"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &recordingT{TestingT: t}
			s := TestRun(stub, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), tc.configure, WithDatabase(tc.connection))
			if s != nil || !strings.Contains(stub.failure, tc.want) {
				t.Fatalf("failure = %q, want %q", stub.failure, tc.want)
			}
		})
	}
	if native.closed != 0 {
		t.Fatal("failed setup closed caller native pool")
	}
}

// counterFixture writes the migration creating a counter table with one row
// of 7, and a dataset that replaces it with 42. The table name and the
// migration version are the caller's, so a run against a shared server can
// keep clear of the other suites' tables and version numbers.
func counterFixture(t *testing.T, table string, version int64) (migrationDir, seedDir string) {
	t.Helper()
	migrationDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(migrationDir, fmt.Sprintf("%d_%s.sql", version, table)), []byte(
		"-- +goose Up\nCREATE TABLE "+table+" (value INTEGER NOT NULL);\nINSERT INTO "+table+" VALUES (7);\n\n-- +goose Down\nDROP TABLE "+table+";\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	seedDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(seedDir, "counter.yaml"), []byte(table+":\n- { value: 42 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return migrationDir, seedDir
}

// withUndialableDatabase enables the database on a DSN nothing can open, so a
// TestRun that dialed the configuration instead of the handed pool would fail.
func withUndialableDatabase(dsn string) func(*Config) {
	return func(config *Config) {
		config.Update(func(value *pw.ServerConfig) {
			value.Public.Enabled = false
		})
		config.Update(func(value *pw.MiddlewareConfig) {
			value.RDB = pw.RDBConfig{
				Enabled:     true,
				Connections: []pw.RDBConnectionConfig{{DSN: dsn, ConnectTimeout: time.Second}},
			}
		})
	}
}

func getBody(t *testing.T, server *Server) string {
	t.Helper()
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", response.StatusCode, body)
	}
	return string(body)
}

func TestRunServesAHandedDatabase(t *testing.T) {
	migrationDir, seedDir := counterFixture(t, "counter", 1)
	connection, err := pwdatabase.OpenOne(pw.RDBConnectionConfig{
		DSN: "sqlite://:memory:", ConnectTimeout: time.Second, MaxOpenConns: 1, MaxIdleConns: 1,
	}, "handed")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	server := TestRun(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		db, ok := pw.DB(r)
		if !ok {
			t.Error("no database on the request")
			return
		}
		var value int
		if err := db.QueryRowContext(r.Context(), "SELECT value FROM counter").Scan(&value); err != nil {
			t.Error(err)
			return
		}
		fmt.Fprint(w, value)
	}), withUndialableDatabase("sqlite:///no/such/popcornweb-dir/app.db"),
		WithDatabase(connection), WithMigrations(migrationDir), WithSeedDir(seedDir))

	// The schema went to the handed database, and so does the fixture.
	if body := getBody(t, server); body != "7" {
		t.Fatalf("migrated counter = %q", body)
	}
	server.Seed(t, "counter")
	if body := getBody(t, server); body != "42" {
		t.Fatalf("seeded counter = %q", body)
	}
	server.AssertDB(t, "counter")
	if server.DB != connection.DB {
		t.Fatal("the tooling handle is not the handed pool")
	}
	// The pool outlives the server, because the test owns it.
	server.Close()
	if err := connection.DB.Ping(); err != nil {
		t.Fatalf("the handed pool was closed with the server: %v", err)
	}
}

// TestRunServesAHandedPostgresPool is the case WithDatabase exists for: a
// PostgreSQL the test started, whose pgx pool it already holds.
//
// Start a server with:
//
//	docker run -d --name pgtest -e POSTGRES_PASSWORD=pw -e POSTGRES_USER=pw \
//	    -e POSTGRES_DB=pw -p 55432:5432 postgres:17
func TestRunServesAHandedPostgresPool(t *testing.T) {
	dsn := os.Getenv("PW_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set PW_POSTGRES_TEST_DSN to run this test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// The server is shared with the other suites, so this run takes a table
	// and a migration version of its own and removes both on the way out.
	// The option is meant for a database that is the test's alone; the
	// bookkeeping here is what a shared server costs instead.
	version := time.Now().UnixNano()
	table := fmt.Sprintf("counter_%d", version)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Error(err)
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM goose_db_version WHERE version_id = $1", version); err != nil {
			t.Error(err)
		}
	})
	migrationDir, seedDir := counterFixture(t, table, version)

	server := TestRun(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value int
		// WithConn resolves the handed pool through the same executor seam a
		// generated query uses, so this is the native path end to end.
		err := postgres.WithConn(r.Context(), func(conn *pgx.Conn) error {
			return conn.QueryRow(r.Context(), "SELECT value FROM "+table).Scan(&value)
		})
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, value)
	}), withUndialableDatabase("postgres://nobody:nobody@127.0.0.1:1/nothing?sslmode=disable"),
		WithDatabase(postgres.FromPool(pool)), WithMigrations(migrationDir), WithSeedDir(seedDir))

	if body := getBody(t, server); body != "7" {
		t.Fatalf("migrated counter = %q", body)
	}
	server.Seed(t, "counter")
	if body := getBody(t, server); body != "42" {
		t.Fatalf("seeded counter = %q", body)
	}
	server.AssertDB(t, "counter")
	server.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("the handed pool was closed with the server: %v", err)
	}
}
