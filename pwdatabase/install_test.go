package pwdatabase

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/database"
	_ "github.com/shibukawa/popcornweb/database/sqlite"
	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/sqlbind"
)

// handedPool stands in for a pool the application built and owns, counting
// the closes the framework sends it.
type handedPool struct {
	sqlbind.UnimplementedQuerier
	closed int
}

func (pool *handedPool) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("unused")
}

func (pool *handedPool) QueryRows(context.Context, string, ...any) (sqlbind.Rows, error) {
	return nil, errors.New("unused")
}

func (pool *handedPool) BeginTx(context.Context, database.NativeTxOptions) (database.NativeTx, error) {
	return nil, errors.New("unused")
}

func (pool *handedPool) Ping(context.Context) error { return nil }

func (pool *handedPool) Close() error {
	pool.closed++
	return nil
}

func handedSet(t *testing.T, pool *handedPool) *pwruntime.ConnectionSet {
	t.Helper()
	set, err := pwruntime.NewConnectionSet("", []pwruntime.Connection{{Native: pool, Driver: "handed"}})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestInstallReplacesTheSetAndClosesOnlyWhatStartOpened(t *testing.T) {
	t.Cleanup(func() { _ = Close() })
	if err := Start(pwconfig.RDBConfig{
		Enabled: true,
		Connections: []pwconfig.RDBConnectionConfig{{
			DSN: "sqlite://:memory:", ConnectTimeout: time.Second, MaxOpenConns: 1, MaxIdleConns: 1,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	opened, _ := Default()
	if opened == nil {
		t.Fatal("Start left no default handle")
	}

	// The set Start opened has no other closer, so replacing it closes it.
	first := &handedPool{}
	if err := Install(handedSet(t, first)); err != nil {
		t.Fatal(err)
	}
	if err := opened.Ping(); err == nil {
		t.Fatal("the pool Start opened is still open after Install replaced it")
	}
	if Connections().Connections()[0].Native != first {
		t.Fatal("Connections does not answer the installed set")
	}
	if db, driver := Default(); db != nil || driver != "handed" {
		t.Fatalf("Default = (%v, %q)", db, driver)
	}

	// An installed set belongs to whoever built it, so the next Install
	// leaves it open.
	second := &handedPool{}
	if err := Install(handedSet(t, second)); err != nil {
		t.Fatal(err)
	}
	if first.closed != 0 {
		t.Fatalf("the first handed pool was closed %d times by the second Install", first.closed)
	}
	// A configured Start with a set already installed opens nothing, as with
	// a set it opened itself.
	if err := Start(pwconfig.RDBConfig{Enabled: true, Connections: []pwconfig.RDBConnectionConfig{{DSN: "sqlite:///no/such/dir/app.db"}}}); err != nil {
		t.Fatalf("Start beside an installed set dialed the configuration: %v", err)
	}
	if Connections().Connections()[0].Native != second {
		t.Fatal("Start replaced the installed set")
	}

	// Close forgets the installed set without closing caller-owned handles.
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	if second.closed != 0 {
		t.Fatalf("Close reached the handed pool %d times", second.closed)
	}
	if Connections() != nil {
		t.Fatal("Close left a set behind")
	}
}

func TestInstallRefusesANilSet(t *testing.T) {
	if err := Install(nil); err == nil {
		t.Fatal("nil set was accepted")
	}
}
