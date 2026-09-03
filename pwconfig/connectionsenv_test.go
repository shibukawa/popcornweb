package pwconfig

import (
	"strings"
	"testing"
	"time"
)

func TestConnectionsEnvRoundTripsAndExpandsSecrets(t *testing.T) {
	encoded, err := EncodeConnectionsEnv([]RDBConnectionConfig{
		{Group: "default", DSN: "d1://DB", ConnectTimeout: 5 * time.Second, MaxOpenConns: 1},
		{Group: "replica", DSN: "postgres://app:${DB_PASSWORD}@db/app", ReadOnly: true, ConnMaxLifetime: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "secret-value") {
		t.Fatal("the encoded form carries an expanded secret")
	}
	connections, err := DecodeConnectionsEnv(encoded, map[string]string{"DB_PASSWORD": "secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 2 || connections[0].Group != "default" || connections[0].DSN != "d1://DB" || connections[0].ConnectTimeout != 5*time.Second || connections[0].MaxOpenConns != 1 {
		t.Errorf("first connection: %+v", connections[0])
	}
	if connections[1].DSN != "postgres://app:secret-value@db/app" || !connections[1].ReadOnly || connections[1].ConnMaxLifetime != time.Hour {
		t.Errorf("second connection: %+v", connections[1])
	}
	if _, err := DecodeConnectionsEnv(`[{"group":"x","dsn":"${MISSING}"}]`, map[string]string{}); err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Errorf("undefined reference accepted: %v", err)
	}
	if _, err := DecodeConnectionsEnv(`[{"group":"x","dsn":"d1://DB","connect_timeout":"soon"}]`, map[string]string{}); err == nil {
		t.Error("bad duration accepted")
	}
	if _, err := DecodeConnectionsEnv(`not json`, map[string]string{}); err == nil {
		t.Error("malformed value accepted")
	}
}
