package testutil

import (
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/sqlbind"
)

// A server database is reachable by DSN, so WithMigrations applies its
// migrations directly instead of replaying the SQLite snapshot, whose script is
// SQLite DDL no other engine would accept. The test is opt-in because it needs
// a live server; see database/integration_test.go for the containers.
//
// The DSN must name a database dedicated to this suite. goose records applied
// versions by number, so a database carrying another project's version 1 makes
// this migration look applied and the schema never arrives.
func TestRunMigratesAServerDatabase(t *testing.T) {
	for _, engine := range []struct{ name, env string }{
		{name: "postgres", env: "PW_POSTGRES_TEST_DSN"},
		{name: "mysql", env: "PW_MYSQL_TEST_DSN"},
	} {
		t.Run(engine.name, func(t *testing.T) {
			dsn := os.Getenv(engine.env)
			if dsn == "" {
				t.Skipf("set %s to run this engine", engine.env)
			}
			// The read goes through the request executor rather than pw.DB,
			// which reports no pool on PostgreSQL: requests there run on a
			// native pgx pool with no *sql.DB behind it. sqlbind.Query is the
			// dispatch that serves both kinds of connection, so one handler
			// covers every engine this test loops over.
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				executor, err := pwruntime.SQLExecutor(r.Context())
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				rows, err := sqlbind.Query(r.Context(), executor, "SELECT COUNT(*) FROM notes")
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				defer func() { _ = rows.Close() }()
				var count int
				if !rows.Next() {
					http.Error(w, "no count row", http.StatusInternalServerError)
					return
				}
				if err := rows.Scan(&count); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			server := TestRun(t, handler, func(config *Config) {
				config.Update(func(value *pw.ServerConfig) {
					value.Public.Enabled = false
				})
				config.Update(func(value *pw.MiddlewareConfig) {
					value.RDB = pw.RDBConfig{
						Enabled: true,
						Connections: []pw.RDBConnectionConfig{{
							DSN:            dsn,
							ConnectTimeout: 10 * time.Second,
							MaxOpenConns:   4,
							MaxIdleConns:   2,
						}},
					}
				})
			}, WithMigrations("testdata/servermigrations"))

			// The handler reads the migrated table, so a 204 proves the schema
			// reached the server rather than a scratch SQLite file.
			response, err := server.Client().Get(server.URL + "/")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusNoContent {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d: %s", response.StatusCode, body)
			}

			// Applying twice must be a no-op, which is what lets several
			// TestRun calls share one prepared server database.
			second := TestRun(t, handler, func(config *Config) {
				config.Update(func(value *pw.ServerConfig) {
					value.Public.Enabled = false
				})
				config.Update(func(value *pw.MiddlewareConfig) {
					value.RDB = pw.RDBConfig{
						Enabled: true,
						Connections: []pw.RDBConnectionConfig{{
							DSN:            dsn,
							ConnectTimeout: 10 * time.Second,
							MaxOpenConns:   4,
							MaxIdleConns:   2,
						}},
					}
				})
			}, WithMigrations("testdata/servermigrations"))
			if second == nil {
				t.Fatal("second TestRun did not start")
			}
		})
	}
}
