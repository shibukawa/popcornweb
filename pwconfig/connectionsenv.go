package pwconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ConnectionsEnv is the environment variable that carries the
// [[middleware.rdb.connections]] array as JSON.
//
// An array of tables has no per-key environment form, and a host with no
// filesystem — a Cloudflare Worker, per requirement:cloudflare-workers-build-target
// — has no file to read it from either. One variable holding the whole array
// is the environment form: pw build writes it from config.prod.toml, and a
// container may set it by hand. When it is set it replaces the array a file
// declared, the way the environment layer replaces a file's scalar.
const ConnectionsEnv = "MIDDLEWARE_RDB_CONNECTIONS"

// connectionEnvEntry is one element of the JSON array, keyed as the TOML
// table is, with durations as the strings a TOML file writes.
type connectionEnvEntry struct {
	Group           string `json:"group"`
	DSN             string `json:"dsn"`
	ReadOnly        bool   `json:"readonly"`
	ConnectTimeout  string `json:"connect_timeout"`
	MaxOpenConns    int    `json:"max_open_conns"`
	MaxIdleConns    int    `json:"max_idle_conns"`
	ConnMaxLifetime string `json:"conn_max_lifetime"`
	ConnMaxIdleTime string `json:"conn_max_idle_time"`
}

// EncodeConnectionsEnv is the value of ConnectionsEnv for a connection list,
// which is what a build writes.
func EncodeConnectionsEnv(connections []RDBConnectionConfig) (string, error) {
	entries := make([]connectionEnvEntry, len(connections))
	for index, connection := range connections {
		entries[index] = connectionEnvEntry{
			Group:           connection.Group,
			DSN:             connection.DSN,
			ReadOnly:        connection.ReadOnly,
			ConnectTimeout:  connection.ConnectTimeout.String(),
			MaxOpenConns:    connection.MaxOpenConns,
			MaxIdleConns:    connection.MaxIdleConns,
			ConnMaxLifetime: connection.ConnMaxLifetime.String(),
			ConnMaxIdleTime: connection.ConnMaxIdleTime.String(),
		}
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// DecodeConnectionsEnv parses the value of ConnectionsEnv. A ${NAME} in a
// DSN is resolved from environ, the way the file layer resolves one, so a
// secret still arrives as a secret rather than as text in the array.
func DecodeConnectionsEnv(value string, environ map[string]string) ([]RDBConnectionConfig, error) {
	var entries []connectionEnvEntry
	if err := json.Unmarshal([]byte(value), &entries); err != nil {
		return nil, fmt.Errorf("%s: %w", ConnectionsEnv, err)
	}
	connections := make([]RDBConnectionConfig, 0, len(entries))
	for index, entry := range entries {
		dsn, err := expandEnvRefs(entry.DSN, environ)
		if err != nil {
			return nil, fmt.Errorf("%s[%d].dsn: %w", ConnectionsEnv, index, err)
		}
		connection := RDBConnectionConfig{Group: entry.Group, DSN: dsn, ReadOnly: entry.ReadOnly,
			MaxOpenConns: entry.MaxOpenConns, MaxIdleConns: entry.MaxIdleConns, ConnectTimeout: 5 * time.Second}
		for _, field := range []struct {
			name  string
			value string
			into  *time.Duration
		}{
			{"connect_timeout", entry.ConnectTimeout, &connection.ConnectTimeout},
			{"conn_max_lifetime", entry.ConnMaxLifetime, &connection.ConnMaxLifetime},
			{"conn_max_idle_time", entry.ConnMaxIdleTime, &connection.ConnMaxIdleTime},
		} {
			if field.value == "" {
				continue
			}
			parsed, err := time.ParseDuration(field.value)
			if err != nil {
				return nil, fmt.Errorf("%s[%d].%s: %w", ConnectionsEnv, index, field.name, err)
			}
			*field.into = parsed
		}
		connections = append(connections, connection)
	}
	return connections, nil
}

// expandEnvRefs replaces every ${NAME} in raw with the value of NAME. An
// undefined name is an error rather than an empty value, matching the file
// layer, because a DSN with a hole in it fails later and less clearly.
func expandEnvRefs(raw string, environ map[string]string) (string, error) {
	if !strings.Contains(raw, "${") {
		return raw, nil
	}
	var out strings.Builder
	rest := raw
	for {
		start := strings.Index(rest, "${")
		if start < 0 {
			out.WriteString(rest)
			return out.String(), nil
		}
		out.WriteString(rest[:start])
		end := strings.Index(rest[start:], "}")
		if end < 0 {
			return "", fmt.Errorf("unterminated ${ in value")
		}
		name := rest[start+2 : start+end]
		value, ok := environ[name]
		if !ok {
			return "", fmt.Errorf("undefined environment variable ${%s}", name)
		}
		out.WriteString(value)
		rest = rest[start+end+1:]
	}
}

// applyConnectionsEnv is the environment layer for the one array of tables
// the runtime reads, run after configbind has loaded the file and the scalar
// environment. environ is the list Parse loaded with, nil meaning the
// process environment.
func applyConnectionsEnv(environ []string) error {
	if environ == nil {
		environ = os.Environ()
	}
	values := make(map[string]string, len(environ))
	for _, line := range environ {
		name, value, _ := strings.Cut(line, "=")
		values[name] = value
	}
	raw, ok := values[ConnectionsEnv]
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	connections, err := DecodeConnectionsEnv(raw, values)
	if err != nil {
		return err
	}
	middleware := boundConfig[MiddlewareConfig]()
	if middleware == nil {
		return nil
	}
	middleware.RDB.Connections = connections
	return nil
}
