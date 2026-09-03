package pw

import (
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/sessionconfig"
)

func TestWorkerProcessStateRefusalNamesEveryOffendingKey(t *testing.T) {
	err := refuseWorkerProcessState(
		pwruntime.CacheConfig{Enabled: true},
		SessionConfig{Enabled: true, Backend: sessionconfig.SessionBackendDevVolatile},
		RateLimitConfig{Enabled: true, Backend: "memory"},
		RDBConfig{Enabled: true, Connections: []RDBConnectionConfig{{Group: "default", DSN: "sqlite://app.db"}, {Group: "edge", DSN: "d1://DB"}}},
		pwruntime.StorageConfig{Enabled: true, Buckets: []pwruntime.StorageBucketConfig{{Name: "uploads", Backend: "local", Directory: "uploads"}, {Name: "media", Backend: "r2", Binding: "MEDIA"}}},
	)
	if err == nil {
		t.Fatal("process state accepted")
	}
	for _, want := range []string{"cache.enabled", "session.backend = \"dev-volatile\"", "ratelimit.backend", "connections[default]: a sqlite://", "storage.buckets[uploads]: the local backend"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal misses %q:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "connections[edge]") || strings.Contains(err.Error(), "buckets[media]") {
		t.Errorf("a reachable backend was refused:\n%s", err)
	}
}

func TestWorkerProcessStateRefusalAcceptsAWorkerConfiguration(t *testing.T) {
	err := refuseWorkerProcessState(
		pwruntime.CacheConfig{},
		SessionConfig{Enabled: true, Backend: sessionconfig.SessionBackendCookie},
		RateLimitConfig{Enabled: true, Backend: "cloudflarekv"},
		RDBConfig{Enabled: true, Connections: []RDBConnectionConfig{{Group: "default", DSN: "d1://DB"}}},
		pwruntime.StorageConfig{Enabled: true, Buckets: []pwruntime.StorageBucketConfig{{Name: "media", Backend: "r2", Binding: "MEDIA"}}},
	)
	if err != nil {
		t.Errorf("refused: %v", err)
	}
}
