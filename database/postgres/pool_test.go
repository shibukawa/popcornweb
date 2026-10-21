package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/database/pgx"
	"github.com/shibukawa/tinygodriver/database/pgx/pgxpool"
)

func TestFromPoolRefusesNothingButServesNothingForANilPool(t *testing.T) {
	connection := FromPool(nil)
	if connection.Native != nil || connection.Driver != Dialect {
		t.Fatalf("FromPool(nil) = %+v", connection)
	}
	if _, err := pwruntime.NewConnectionSet("", []pwruntime.Connection{connection}); err == nil {
		t.Fatal("a nil pool made it into a connection set")
	}
}

// TestFromPoolHandsOverAPoolTheCallerKeeps needs a server; see rawconn_test.
func TestFromPoolHandsOverAPoolTheCallerKeeps(t *testing.T) {
	dsn := os.Getenv("PW_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set PW_POSTGRES_TEST_DSN to run this test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	connection := FromPool(pool)
	if connection.Driver != Dialect {
		t.Fatalf("Driver = %q", connection.Driver)
	}
	if connection.DataSource != dsn {
		t.Fatalf("DataSource = %q, want the pool's connection string", connection.DataSource)
	}
	set, err := pwruntime.NewConnectionSet("", []pwruntime.Connection{connection})
	if err != nil {
		t.Fatal(err)
	}
	requestCtx := pwruntime.WithResources(ctx, pwruntime.Resources{Connections: set, DBDriver: Dialect})
	var one int
	if err := WithConn(requestCtx, func(conn *pgx.Conn) error {
		return conn.QueryRow(ctx, "SELECT 1").Scan(&one)
	}); err != nil {
		t.Fatal(err)
	}
	if one != 1 {
		t.Fatalf("SELECT 1 = %d", one)
	}
	// The framework's close is the owner's to ignore.
	if err := set.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("closing the set closed the handed pool: %v", err)
	}
}
