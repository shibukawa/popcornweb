package pgmemexample

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/shibukawa/pgmem/pgmemtest"
	"github.com/shibukawa/popcornweb/database/postgres"
	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/pwdatabase"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/testutil"
	"github.com/shibukawa/tinybind-go/configbind"
	"github.com/shibukawa/tinybind-go/sqlbind"
	"github.com/shibukawa/tinygodriver/database/pgx"
)

var fixture *pgmemtest.Fixture

// The fixture owns PostgreSQL and its pools, outside individual application
// servers. Nothing in popcornweb imports or starts pgmem.
func TestMain(m *testing.M) {
	pw.SetConfigLoadOptions(configbind.LoadOptions{
		Vendor: "popcornweb-test", Tool: "pgmem-example", FileName: "missing.toml",
		Args: []string{}, Environ: []string{},
	})
	os.Exit(pgmemtest.Run(m, pgmemtest.Options{MaxForks: 2}, func(f *pgmemtest.Fixture) { fixture = f }))
}

func configure(c *testutil.Config) {
	c.Update(func(s *pw.ServerConfig) { s.Public.Enabled = false })
	c.Update(func(m *pw.MiddlewareConfig) {
		// No DSN in configuration: the connection supplied below determines it.
		m.RDB = pw.RDBConfig{Enabled: true}
	})
}

func sources(t *testing.T) (string, string) {
	t.Helper()
	migrations, seeds := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(migrations, "1_counter.sql"), []byte("-- +goose Up\nCREATE TABLE counter (value INTEGER NOT NULL);\nINSERT INTO counter VALUES (7);\n\n-- +goose Down\nDROP TABLE counter;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seeds, "counter.yaml"), []byte("counter:\n- { value: 42 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return migrations, seeds
}

func body(t *testing.T, s *testutil.Server) string {
	t.Helper()
	r, err := s.Client().Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d: %s", r.StatusCode, b)
	}
	return string(b)
}

func TestFixturePoolMigrationsSeedsAndNativeTransactions(t *testing.T) {
	pool := fixture.PgxPool(t)
	migrations, seeds := sources(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rollback := errors.New("rollback")
		err := pw.Transaction(r, func(ctx context.Context) error {
			return postgres.WithConn(ctx, func(c *pgx.Conn) error {
				if _, err := c.Exec(ctx, "UPDATE counter SET value = 99"); err != nil {
					return err
				}
				return rollback
			})
		})
		if !errors.Is(err, rollback) {
			t.Errorf("transaction: %v", err)
			w.WriteHeader(500)
			return
		}
		// Generated queries use this same executor and sqlbind.Query seam.
		executor, err := pwruntime.SQLExecutor(r.Context())
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		rows, err := sqlbind.Query(r.Context(), executor, "SELECT value FROM counter")
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		defer rows.Close()
		var value int
		if !rows.Next() {
			t.Errorf("no counter row: %v", rows.Err())
			w.WriteHeader(500)
			return
		}
		if err := rows.Scan(&value); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, value)
	})
	s := testutil.TestRun(t, handler, configure,
		testutil.WithDatabase(postgres.FromPool(pool)), testutil.WithMigrations(migrations), testutil.WithSeedDir(seeds))
	if got := body(t, s); got != "7" {
		t.Fatalf("migration/rollback: %s", got)
	}
	s.Seed(t, "counter")
	s.AssertDB(t, "counter")
	if got := body(t, s); got != "42" {
		t.Fatalf("seed/rollback: %s", got)
	}
	s.Close()
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatalf("caller pool closed: %v", err)
	}
	if err := s.DB.Ping(); err == nil {
		t.Fatal("framework tooling handle left open")
	}
	if pwdatabase.Connections() != nil {
		t.Fatal("TestRun changed process database state")
	}
}

func TestFixturePoolsRemainIndependentAndRollbackAtClose(t *testing.T) {
	first, second := fixture.PgxPool(t), fixture.PgxPool(t)
	migrations, _ := sources(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := pw.Transaction(r, func(ctx context.Context) error {
			return postgres.WithConn(ctx, func(c *pgx.Conn) error {
				_, err := c.Exec(ctx, "UPDATE counter SET value = value + 1")
				return err
			})
		})
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, "ok")
	})
	a := testutil.TestRun(t, handler, configure, testutil.WithDatabase(postgres.FromPool(first)), testutil.WithMigrations(migrations), testutil.WithTransaction(true))
	b := testutil.TestRun(t, handler, configure, testutil.WithDatabase(postgres.FromPool(second)), testutil.WithMigrations(migrations))
	if body(t, a) != "ok" || body(t, b) != "ok" {
		t.Fatal("request failed")
	}
	a.Close()
	b.Close()
	var firstValue, secondValue int
	if err := first.QueryRow(t.Context(), "SELECT value FROM counter").Scan(&firstValue); err != nil {
		t.Fatal(err)
	}
	if err := second.QueryRow(t.Context(), "SELECT value FROM counter").Scan(&secondValue); err != nil {
		t.Fatal(err)
	}
	if firstValue != 7 || secondValue != 8 {
		t.Fatalf("independent pools: first=%d second=%d", firstValue, secondValue)
	}
}

func TestParallelFixtureServers(t *testing.T) {
	for _, n := range []int{11, 22} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			t.Parallel()
			pool := fixture.PgxPool(t)
			migrations, _ := sources(t)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var value int
				err := postgres.WithConn(r.Context(), func(c *pgx.Conn) error {
					return c.QueryRow(r.Context(), "SELECT value FROM counter").Scan(&value)
				})
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				fmt.Fprint(w, value)
			})
			s := testutil.TestRun(t, handler, configure, testutil.WithDatabase(postgres.FromPool(pool)), testutil.WithMigrations(migrations))
			if _, err := pool.Exec(t.Context(), "UPDATE counter SET value = $1", n); err != nil {
				t.Fatal(err)
			}
			for range 5 {
				if got := body(t, s); got != fmt.Sprint(n) {
					t.Fatalf("crossed databases: got %s want %d", got, n)
				}
			}
		})
	}
}
