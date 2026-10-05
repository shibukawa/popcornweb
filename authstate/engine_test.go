//go:build !tinygo

package authstate_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/authstate"
	"github.com/shibukawa/popcornweb/database"
	"github.com/shibukawa/tinybind-go/sqlbind"

	_ "github.com/shibukawa/popcornweb/authstate/mysql"
	_ "github.com/shibukawa/popcornweb/authstate/postgres"
	_ "github.com/shibukawa/popcornweb/authstate/sqlite"
	_ "github.com/shibukawa/popcornweb/database/mysql"
	_ "github.com/shibukawa/popcornweb/database/postgres"
	_ "github.com/shibukawa/popcornweb/database/sqlite"
)

// The server engines need a live server, so their runs are opt-in.
// scripts/test-session-engines.sh starts both and runs this file.
const (
	postgresDSNEnv = "PW_POSTGRES_TEST_DSN"
	mysqlDSNEnv    = "PW_MYSQL_TEST_DSN"
)

// TestEngineContract runs one suite against every engine. A ceremony record is
// single use, and that is the property most at risk of being spelled
// differently per engine: MySQL has no RETURNING and no conditional upsert, so
// it reaches the same behavior through a transaction.
func TestEngineContract(t *testing.T) {
	for _, engine := range []struct {
		name, dialect, dsn string
		// native runs the suite on the pool the request path uses for an
		// engine that bypasses database/sql, which is the one a running
		// application hands this store.
		native bool
	}{
		{name: "sqlite", dialect: "sqlite", dsn: "sqlite://" + filepath.Join(t.TempDir(), "contract.db")},
		{name: "postgres", dialect: "postgres", dsn: os.Getenv(postgresDSNEnv)},
		{name: "postgres-native", dialect: "postgres", dsn: os.Getenv(postgresDSNEnv), native: true},
		{name: "mysql", dialect: "mysql", dsn: os.Getenv(mysqlDSNEnv)},
	} {
		t.Run(engine.name, func(t *testing.T) {
			if engine.dsn == "" {
				t.Skipf("set %s to run this engine", engine.dialect)
			}
			db := openEngine(t, engine.dsn, engine.native)
			now := time.Now().Truncate(time.Millisecond)
			clock := func() time.Time { return now }
			store, err := authstate.NewSQLStore[string](db, stringCodec{}, authstate.SQLOptions{
				Dialect: engine.dialect, Namespace: "contract", Now: clock,
			})
			if err != nil {
				t.Fatal(err)
			}
			// A record can only be written with a future expiry, so a stale
			// one is written by a store whose clock is an hour behind.
			past, err := authstate.NewSQLStore[string](db, stringCodec{}, authstate.SQLOptions{
				Dialect: engine.dialect, Namespace: "contract",
				Now: func() time.Time { return now.Add(-time.Hour) },
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if err := store.EnsureSchema(ctx); err != nil {
				t.Fatalf("EnsureSchema: %v", err)
			}
			t.Cleanup(func() {
				_, _ = db.ExecContext(context.Background(), `DELETE FROM `+authstate.TableName)
			})

			if err := store.Put(ctx, "ceremony", "value", now.Add(time.Minute)); err != nil {
				t.Fatalf("Put: %v", err)
			}
			// A live record is never replaced, which is what makes one
			// ceremony key usable once.
			if err := store.Put(ctx, "ceremony", "second", now.Add(time.Minute)); !errors.Is(err, authstate.ErrAlreadyExists) {
				t.Fatalf("duplicate Put = %v", err)
			}
			value, err := store.Take(ctx, "ceremony")
			if err != nil || value != "value" {
				t.Fatalf("Take = (%q, %v)", value, err)
			}
			if _, err := store.Take(ctx, "ceremony"); !errors.Is(err, authstate.ErrNotFound) {
				t.Fatalf("second Take = %v", err)
			}

			// An expired record is replaceable, because the key is free again.
			if err := past.Put(ctx, "stale", "old", now.Add(-time.Minute)); err != nil {
				t.Fatalf("Put stale: %v", err)
			}
			if err := store.Put(ctx, "stale", "new", now.Add(time.Minute)); err != nil {
				t.Fatalf("replacing an expired record: %v", err)
			}
			if value, err = store.Take(ctx, "stale"); err != nil || value != "new" {
				t.Fatalf("replaced record = (%q, %v)", value, err)
			}
			// Taking an expired record reports the expiry rather than the
			// payload, and consumes it either way.
			if err := past.Put(ctx, "gone", "old", now.Add(-time.Minute)); err != nil {
				t.Fatalf("Put expired: %v", err)
			}
			if _, err := store.Take(ctx, "gone"); !errors.Is(err, authstate.ErrExpired) {
				t.Fatalf("expired Take = %v", err)
			}

			if err := past.Put(ctx, "swept", "old", now.Add(-time.Minute)); err != nil {
				t.Fatalf("Put: %v", err)
			}
			if err := store.Put(ctx, "kept", "live", now.Add(time.Minute)); err != nil {
				t.Fatalf("Put: %v", err)
			}
			removed, err := store.Prune(ctx, now, 16)
			if err != nil {
				t.Fatalf("Prune: %v", err)
			}
			if removed != 1 {
				t.Fatalf("Prune removed %d records, want 1", removed)
			}
			if value, err = store.Take(ctx, "kept"); err != nil || value != "live" {
				t.Fatalf("Prune removed a live record: (%q, %v)", value, err)
			}
		})
	}
}

func openEngine(t *testing.T, dsn string, native bool) sqlbind.SQLExecutor {
	t.Helper()
	target, err := database.Resolve(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if native {
		pool, err := target.OpenNative(t.Context(), database.PoolBounds{MaxOpenConns: 4})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = pool.Close() })
		if err := pool.Ping(t.Context()); err != nil {
			t.Fatal(err)
		}
		return pool
	}
	db, err := target.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db
}
