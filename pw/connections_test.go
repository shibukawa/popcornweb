package pw

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pwdatabase"
)

// writer and replica are the two shapes of a configured pool these tests build
// topologies out of. They are duplicated in pwconfig rather than shared,
// because the two halves test different things about the same shape and a
// helper reaching across a package boundary would be the only thing tying them
// together.
func writer(dsn string) RDBConnectionConfig {
	return RDBConnectionConfig{Group: "writer", DSN: dsn, ConnectTimeout: 5 * time.Second}
}

func replica(dsn string) RDBConnectionConfig {
	return RDBConnectionConfig{Group: "replica", DSN: dsn, ReadOnly: true, ConnectTimeout: 5 * time.Second}
}

// The startup summary is read to find out which database this process talks to,
// so a DSN is not masked whole: the credential goes and the address stays. The
// value reaching this point is the resolved one, because configbind expands
// ${VAR} while merging the file.
func TestConnectionBootEntriesShowTheAddressAndHideTheCredential(t *testing.T) {
	result := loadResult(t, `
[middleware.rdb]
enabled = true

[[middleware.rdb.connections]]
group = "writer"
dsn = "postgres://app:s3cret@writer.internal:5432/app?sslmode=verify-full"

[[middleware.rdb.connections]]
group = "replica"
dsn = "postgres://app:s3cret@replica.internal:5432/app"
readonly = true
`)
	found := map[string]string{}
	for _, entry := range bootEntries(result) {
		found[entry.key] = entry.value
	}
	for key, want := range map[string]string{
		"middleware.rdb.connections[0].dsn": "postgres://" + redactedValue + "@writer.internal:5432/app",
		"middleware.rdb.connections[1].dsn": "postgres://" + redactedValue + "@replica.internal:5432/app",
	} {
		value, ok := found[key]
		if !ok {
			t.Fatalf("%s missing from the startup summary: %v", key, found)
		}
		if value != want {
			t.Fatalf("%s = %q, want %q", key, value, want)
		}
		if strings.Contains(value, "s3cret") {
			t.Fatalf("%s printed the password: %q", key, value)
		}
	}
	// Everything else about a connection is operational detail worth reporting.
	if found["middleware.rdb.connections[1].readonly"] != "true" {
		t.Fatalf("readonly was not reported: %v", found)
	}
	if found["middleware.rdb.connections[0].group"] != "writer" {
		t.Fatalf("group was not reported: %v", found)
	}
}

func TestValidateRDBConfigRejectsBadConnectionSets(t *testing.T) {
	base := func(connections ...RDBConnectionConfig) RDBConfig {
		return RDBConfig{Enabled: true, DefaultGroup: "replica", Connections: connections}
	}
	for name, config := range map[string]RDBConfig{
		"upper-case group": base(RDBConnectionConfig{Group: "Writer", DSN: "sqlite://w.db", ConnectTimeout: time.Second}),
		"mixed drivers": base(
			RDBConnectionConfig{Group: "replica", DSN: "sqlite://r.db", ConnectTimeout: time.Second, ReadOnly: true},
			RDBConnectionConfig{Group: "replica", DSN: "postgres://host/db", ConnectTimeout: time.Second, ReadOnly: true},
			writer("sqlite://w.db"),
		),
		"shared memory database": base(
			RDBConnectionConfig{Group: "replica", DSN: "sqlite://:memory:", ConnectTimeout: time.Second, MaxOpenConns: 1, ReadOnly: true},
			RDBConnectionConfig{Group: "replica", DSN: "sqlite://:memory:", ConnectTimeout: time.Second, MaxOpenConns: 1, ReadOnly: true},
			writer("sqlite://w.db"),
		),
		"idle above open": base(RDBConnectionConfig{
			Group: "replica", DSN: "sqlite://r.db", ConnectTimeout: time.Second,
			MaxOpenConns: 2, MaxIdleConns: 3, ReadOnly: true,
		}, writer("sqlite://w.db")),
		"zero connect timeout": base(RDBConnectionConfig{Group: "replica", DSN: "sqlite://r.db", ReadOnly: true}),
		"malformed dsn":        base(RDBConnectionConfig{Group: "replica", DSN: "r.db", ConnectTimeout: time.Second, ReadOnly: true}),
	} {
		if err := validateRDBConfig(config); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestValidateRDBConfigAcceptsAReaderWriterTopology(t *testing.T) {
	config := RDBConfig{
		Enabled:      true,
		DefaultGroup: "replica",
		Connections: []RDBConnectionConfig{
			writer("sqlite://writer.db"),
			replica("sqlite://replica-1.db"),
			replica("sqlite://replica-2.db"),
		},
	}
	if err := validateRDBConfig(config); err != nil {
		t.Fatal(err)
	}
	// One element naming no group is the single-database project, and it
	// validates without naming a default, write, or migration group.
	single := RDBConfig{
		Enabled: true,
		Connections: []RDBConnectionConfig{
			{DSN: "sqlite://app.db", ConnectTimeout: 5 * time.Second},
		},
	}
	if err := validateRDBConfig(single); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRuntimeConnectionsOpensEveryPool(t *testing.T) {
	dir := t.TempDir()
	config := RDBConfig{
		Enabled:      true,
		DefaultGroup: "replica",
		Connections: []RDBConnectionConfig{
			writer("sqlite://" + filepath.Join(dir, "writer.db")),
			replica("sqlite://" + filepath.Join(dir, "replica-1.db")),
			replica("sqlite://" + filepath.Join(dir, "replica-2.db")),
		},
	}
	set, err := pwdatabase.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	if set.DefaultGroup() != "replica" {
		t.Fatalf("default group = %q", set.DefaultGroup())
	}
	labels := make([]string, 0, 3)
	for _, connection := range set.Connections() {
		labels = append(labels, connection.Label)
	}
	want := []string{"writer#1", "replica#1", "replica#2"}
	for index := range want {
		if labels[index] != want[index] {
			t.Fatalf("labels = %v, want %v", labels, want)
		}
	}
	if set.Collapsed() {
		t.Fatal("a three-connection set reported itself collapsed")
	}
	// A failure closes what was already opened rather than leaving a partial
	// set behind, so the error is the only thing the caller has to handle.
	broken := config
	broken.Connections = append(append([]RDBConnectionConfig{}, config.Connections...),
		RDBConnectionConfig{Group: "writer", DSN: "nosuchdriver://host/db", ConnectTimeout: time.Second})
	if _, err := pwdatabase.Open(broken); err == nil {
		t.Fatal("an unregistered driver was accepted")
	}
}

func TestConfiguredDatabaseDSNUsesTheMigrationGroup(t *testing.T) {
	replaceMiddlewareConfig(t, RDBConfig{
		Enabled:      true,
		DefaultGroup: "replica",
		Connections: []RDBConnectionConfig{
			replica("sqlite://replica.db"),
			writer("sqlite://writer.db"),
		},
	})
	dsn, err := configuredDatabaseDSN()
	if err != nil {
		t.Fatal(err)
	}
	if dsn != "sqlite://writer.db" {
		t.Fatalf("dsn = %q, want the writer group", dsn)
	}
}
