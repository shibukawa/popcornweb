package handlers

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	_ "github.com/shibukawa/popcornweb/database/sqlite"
	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/testutil"
	_ "helloworld"
	_ "helloworld/templates"
)

func TestHomeRendersNestedDocumentAndIncrementsCounter(t *testing.T) {
	server := testutil.TestRun(t, Handlers(), func(config *testutil.Config) {
		config.Update(func(middleware *pw.MiddlewareConfig) {
			middleware.RDB = pw.RDBConfig{
				Enabled: true,
				Connections: []pw.RDBConnectionConfig{{
					DSN:            "sqlite://:memory:",
					ConnectTimeout: time.Second,
					MaxOpenConns:   1,
					MaxIdleConns:   1,
				}},
			}
		})
	}, testutil.WithMigrations("../migrations"))

	for visit, expected := range []string{">1</strong>", ">2</strong>"} {
		response, err := server.Client().Get(server.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		bodyBytes, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("visit %d status = %d body=%q", visit+1, response.StatusCode, bodyBytes)
		}
		body := string(bodyBytes)
		for _, fragment := range []string{
			"<!doctype html>",
			"<title>Hello World · Popcorn Web</title>",
			"Hello, ",
			"World!",
			expected,
		} {
			if !strings.Contains(body, fragment) {
				t.Fatalf("visit %d body is missing %q: %s", visit+1, fragment, body)
			}
		}
	}
}
