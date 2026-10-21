package pw

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwdatabase"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/configbind"
)

// handedSQLiteConnection is a pool the test owns, holding one counter row so
// a request can tell which database answered it.
func handedSQLiteConnection(t *testing.T, value int) pwruntime.Connection {
	t.Helper()
	connection, err := pwdatabase.OpenOne(RDBConnectionConfig{
		DSN: "sqlite://:memory:", ConnectTimeout: time.Second, MaxOpenConns: 1, MaxIdleConns: 1,
	}, "handed")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if _, err := connection.DB.Exec("CREATE TABLE counter (value INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.DB.Exec("INSERT INTO counter VALUES (?)", value); err != nil {
		t.Fatal(err)
	}
	return connection
}

// counterHandler answers the one counter row of the effective database.
func counterHandler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		db, ok := DB(r)
		if !ok {
			t.Error("no database on the request")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var value int
		if err := db.QueryRowContext(r.Context(), "SELECT value FROM counter").Scan(&value); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, value)
	})
}

func counterBody(t *testing.T, handler http.Handler) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	return recorder.Body.String()
}

// handedDatabaseConfig parses the process configuration, whichever options
// another test froze it with, and points the bound middleware value at a DSN
// that cannot be opened: a Middlewares call that dialed the configuration
// instead of the handed pool would fail.
//
// This file sorts after pw_test.go on purpose: the test there sets the load
// options unguarded, so it has to be the one that parses first.
func handedDatabaseConfig(t *testing.T, enabled bool) {
	t.Helper()
	func() {
		defer func() { _ = recover() }()
		SetConfigLoadOptions(configbind.LoadOptions{
			Vendor: "popcornweb-test", Tool: "pw-test", FileName: "missing.toml",
			Args: []string{}, Environ: []string{},
		})
	}()
	if err := ParseConfig(); err != nil {
		t.Fatal(err)
	}
	bound, ok := pwconfig.Bound[MiddlewareConfig]()
	if !ok {
		t.Fatal("MiddlewareConfig is not registered")
	}
	previous := *bound
	t.Cleanup(func() { *bound = previous })
	bound.RDB = RDBConfig{
		Enabled: enabled,
		Connections: []RDBConnectionConfig{{
			DSN: "sqlite:///no/such/popcornweb-dir/app.db", ConnectTimeout: time.Second, MaxOpenConns: 1, MaxIdleConns: 1,
		}},
	}
}

func TestWithDatabaseServesTheHandedPoolAndReplacesItPerCall(t *testing.T) {
	handedDatabaseConfig(t, true)
	t.Cleanup(func() { _ = pwdatabase.Close() })
	publicFS := WithPublicFS(fstest.MapFS{".keep": {Data: nil}})

	first := handedSQLiteConnection(t, 1)
	handler, err := Middlewares(counterHandler(t), publicFS, WithDatabase(first))
	if err != nil {
		t.Fatal(err)
	}
	if body := counterBody(t, handler); body != "1" {
		t.Fatalf("first handed database answered %q", body)
	}

	// The next call is a different test case with a different database, so
	// it replaces the set rather than reusing the first one.
	second := handedSQLiteConnection(t, 2)
	handler, err = Middlewares(counterHandler(t), publicFS, WithDatabase(second))
	if err != nil {
		t.Fatal(err)
	}
	if body := counterBody(t, handler); body != "2" {
		t.Fatalf("second handed database answered %q", body)
	}
	// The first pool is its owner's, so the replacement did not close it.
	if err := first.DB.Ping(); err != nil {
		t.Fatalf("the replaced handed pool was closed: %v", err)
	}
	if got := pwdatabase.Connections().Connections()[0].DB; got != second.DB {
		t.Fatal("the process set is not the pool handed over last")
	}
}

func TestWithDatabaseNeedsTheDatabaseEnabled(t *testing.T) {
	handedDatabaseConfig(t, false)
	t.Cleanup(func() { _ = pwdatabase.Close() })
	_, err := Middlewares(counterHandler(t), WithPublicFS(fstest.MapFS{".keep": {Data: nil}}),
		WithDatabase(handedSQLiteConnection(t, 1)))
	if err == nil || !strings.Contains(err.Error(), "middleware.rdb.enabled") {
		t.Fatalf("err = %v, want the disabled database named", err)
	}
}

func TestWithDatabaseNeedsAConnection(t *testing.T) {
	handedDatabaseConfig(t, true)
	_, err := Middlewares(counterHandler(t), WithPublicFS(fstest.MapFS{".keep": {Data: nil}}), WithDatabase())
	if err == nil || !strings.Contains(err.Error(), "at least one connection") {
		t.Fatalf("err = %v", err)
	}
}
