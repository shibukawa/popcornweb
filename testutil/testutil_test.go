package testutil

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/tinybind-go/configbind"
)

type fixtureConfig struct {
	Name   string
	Labels []string
}

func init() {
	configbind.Register[fixtureConfig](configbind.Definition{
		TypeName:  "github.com/shibukawa/popcornweb/testutil.fixtureConfig",
		Prefix:    "fixture",
		KnownKeys: []string{"fixture.name"},
		Defaults:  map[string]string{"fixture.name": "global"},
		Apply: func(dst any, overlay *configbind.Overlay) error {
			config := dst.(*fixtureConfig)
			config.Name, _ = overlay.GetString("fixture.name")
			config.Labels = []string{"original"}
			return nil
		},
	})
	pw.RegisterConfig[fixtureConfig]("fixture")
}

func TestRunCopiesAndCustomizesArbitraryConfig(t *testing.T) {
	migrationDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(migrationDir, "00001_counter.sql"), []byte(
		"-- +goose Up\nCREATE TABLE counter (value INTEGER NOT NULL);\n\n-- +goose Down\nDROP TABLE counter;\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(migrationDir, "00002_seed.sql"), []byte(
		"-- +goose Up\nINSERT INTO counter VALUES (7);\n\n-- +goose Down\nDELETE FROM counter;\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	var sawDefaultPort bool
	server := TestRun(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		config := pw.Config[fixtureConfig](r)
		runtimeServer := pw.Config[pw.ServerConfig](r)
		db, ok := pw.DB(r)
		if !ok {
			t.Fatal("test database is missing")
		}
		var value int
		if err := db.QueryRowContext(r.Context(), "SELECT value FROM counter").Scan(&value); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(config.Name + ":" + strings.Join(config.Labels, ",") + ":" +
			strconv.Itoa(runtimeServer.Port) + ":" + strconv.Itoa(value)))
	}), func(config *Config) {
		sawDefaultPort = config.Get[pw.ServerConfig]().Port == -1
		config.Update(func(value *fixtureConfig) {
			value.Name = "copied"
			value.Labels[0] = "isolated"
		})
		config.Update(func(value *pw.ServerConfig) {
			value.Public.Enabled = false
		})
		config.Update(func(value *pw.MiddlewareConfig) {
			value.RDB = pw.RDBConfig{
				Enabled: true,
				Connections: []pw.RDBConnectionConfig{{
					DSN:            "sqlite://:memory:",
					ConnectTimeout: time.Second,
					MaxOpenConns:   1,
					MaxIdleConns:   1,
				}},
			}
		})
	}, WithMigrations(migrationDir))
	if !sawDefaultPort {
		t.Fatal("customizer did not observe default port -1")
	}
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	buffer, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(buffer)
	actualPort := server.Config.Get[pw.ServerConfig]().Port
	want := "copied:isolated:" + strconv.Itoa(actualPort) + ":7"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
	global := pw.ConfigContext[fixtureConfig](nil)
	if global.Name != "global" || strings.Join(global.Labels, ",") != "original" {
		t.Fatalf("global config was mutated: %#v", global)
	}
}
