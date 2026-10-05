package pwcli

import (
	"fmt"
	"strings"

	"github.com/shibukawa/tinybind-go/templates/sqlbind"
)

// The database engines a project can be scaffolded against. The name is the
// canonical dialect the framework registry reports, so one string selects the
// driver, the goose dialect, the savepoint rules, and the EXPLAIN form.
const (
	engineSQLite   = "sqlite"
	enginePostgres = "postgres"
	engineMySQL    = "mysql"
)

// engineOrder lists the engines in the order the wizard offers them, which puts
// the one needing no server first.
var engineOrder = []string{engineSQLite, enginePostgres, engineMySQL}

// databaseEngine is everything selecting one engine changes in a scaffold.
// Keeping it in one table means adding an engine is one entry rather than a
// branch in each of the scaffold, the wizard, and pw add.
type databaseEngine struct {
	// Label and summary are the wizard-facing text.
	Label   string
	Summary string
	// DSN builds the development DSN for a project of this name.
	DSN func(project string) string
	// DevboxPackage is the development server, added to devbox.json the way
	// Valkey is. An embedded engine names none.
	DevboxPackage string
	// DriverImport links the engine into the application binary. Every engine is
	// opt-in, including SQLite, so a project with no RDB carries no driver bytes.
	DriverImport string
	// Schema is the starter migration, written for this dialect.
	Schema string
	// MaxOpenConns and MaxIdleConns size the scaffolded pool. SQLite serializes
	// writers on one file, so it takes a single connection; a server engine is
	// bounded by its own connection limit instead.
	MaxOpenConns int
	MaxIdleConns int
	// SQLDialect is the system:tinybind dialect .pw.sql sources generate for.
	// It is a separate field rather than the engine name because the two
	// spellings only mostly agree: tinybind calls PostgreSQL postgresql.
	SQLDialect string
}

var databaseEngines = map[string]databaseEngine{
	engineSQLite: {
		Label:        "SQLite",
		Summary:      "an embedded file database; nothing to run beside the application",
		DSN:          func(project string) string { return "sqlite://" + project + ".db" },
		DriverImport: "github.com/shibukawa/popcornweb/database/sqlite",
		Schema:       starterSchema("id INTEGER PRIMARY KEY", "name TEXT NOT NULL"),

		MaxOpenConns: 1,
		MaxIdleConns: 1,
		SQLDialect:   sqlbind.DialectSQLite,
	},
	enginePostgres: {
		Label:   "PostgreSQL",
		Summary: "a server database; pw dev starts it from the Devbox environment",
		DSN: func(project string) string {
			return "postgres://" + project + ":" + project + "@127.0.0.1:5432/" + project + "?sslmode=disable"
		},
		DevboxPackage: "postgresql@latest",
		DriverImport:  "github.com/shibukawa/popcornweb/database/postgres",
		Schema:        starterSchema("id INTEGER PRIMARY KEY", "name TEXT NOT NULL"),

		MaxOpenConns: 10,
		MaxIdleConns: 5,
		SQLDialect:   sqlbind.DialectPostgreSQL,
	},
	engineMySQL: {
		Label:   "MySQL or MariaDB",
		Summary: "a server database; pw dev starts it from the Devbox environment",
		DSN: func(project string) string {
			return "mysql://" + project + ":" + project + "@tcp(127.0.0.1:3306)/" + project
		},
		DevboxPackage: "mysql80@latest",
		DriverImport:  "github.com/shibukawa/popcornweb/database/mysql",
		Schema:        starterSchema("id INT PRIMARY KEY", "name VARCHAR(255) NOT NULL"),

		MaxOpenConns: 10,
		MaxIdleConns: 5,
		SQLDialect:   sqlbind.DialectMySQL,
	},
}

// starterSchema is migration version 1, and it creates nothing. The example it
// carries is written for this dialect and commented out, so a project that
// selected a database gets the shape of a migration without also getting a
// table it never asked for and has to drop before writing its own.
//
// The columns are passed in because the two lines are the whole difference
// between the dialects, and repeating the surrounding comment three times to
// vary them would put the explanation in three places.
func starterSchema(columns ...string) string {
	var body strings.Builder
	body.WriteString(`-- Migration version 1.
--
-- This file is empty on purpose: pw creates no table for you. The example
-- below is the shape of a migration, written for this project's engine.
-- Uncomment it, or replace it with your own schema, and pw dev applies it.
--
-- Sample rows do not belong here. A migration runs in every environment,
-- including production, and an applied one cannot be edited. Use pw seed and
-- a dataset for development data.

-- +goose Up
-- CREATE TABLE example (
`)
	for index, column := range columns {
		separator := ","
		if index == len(columns)-1 {
			separator = ""
		}
		fmt.Fprintf(&body, "--     %s%s\n", column, separator)
	}
	body.WriteString(`-- );

-- +goose Down
-- DROP TABLE example;
`)
	return body.String()
}

// engineFor returns the scaffold description of an engine, falling back to
// SQLite so a zero-valued option cannot produce a project without a DSN.
func engineFor(name string) databaseEngine {
	if engine, known := databaseEngines[name]; known {
		return engine
	}
	return databaseEngines[engineSQLite]
}

// engineDialect is the engine a project's migrations are written for. It is
// the same string the store dialects register under, and an unset value means
// the scaffold default.
func engineDialect(name string) string {
	if _, known := databaseEngines[name]; known {
		return name
	}
	return engineSQLite
}

// validEngine reports whether a name selects an engine the scaffold knows.
func validEngine(name string) bool {
	_, known := databaseEngines[name]
	return known
}

// engineNames renders the accepted values for a usage or error message.
func engineNames() string {
	return fmt.Sprintf("%s, %s, or %s", engineSQLite, enginePostgres, engineMySQL)
}

// engineCursor maps an engine onto its position in the wizard choice list.
func engineCursor(name string) int {
	for index, candidate := range engineOrder {
		if candidate == name {
			return index
		}
	}
	return 0
}

// databaseEngineNotice tells the operator what a server engine still needs from
// them. A scripted run never sees the wizard say it, and the first pw dev is
// where a missing server or an unwritable database turns into a startup error.
func databaseEngineNotice(options initOptions) string {
	if !options.Database {
		return ""
	}
	engine := engineFor(options.Engine)
	if engine.DevboxPackage == "" {
		return ""
	}
	notice := "\n" + engine.Label + " runs beside the application:\n"
	switch {
	case options.Devbox && len(databaseDevboxSetup(options).Script) > 0:
		notice += "  devbox run " + devboxDatabaseScript + " creates the cluster (UTF-8) and the role and database the\n" +
			"  middleware.rdb connection names; run it once, before the first pw dev starts the server\n"
	case options.Devbox:
		notice += "  devbox services up starts it; create the role and database the middleware.rdb connection names once\n"
	case options.Engine == enginePostgres:
		notice += "  install and start it yourself, then create what the middleware.rdb connection names:\n" +
			"    psql -U postgres -c " + shellQuote(postgresCreateRole(options.Name)) + "\n" +
			"    createdb -U postgres -O " + options.Name + " -E UTF8 -T template0 " + options.Name + "\n"
	default:
		notice += "  install and start it yourself, then create the role and database the middleware.rdb connection names\n"
	}
	return notice
}

// postgresCreateRole is the statement creating the role of the scaffolded DSN.
// A project name is letters, digits, hyphen, and underscore, so it is quoted as
// an identifier and needs no escaping as a literal.
func postgresCreateRole(project string) string {
	return `CREATE ROLE "` + project + `" LOGIN PASSWORD '` + project + `' CREATEDB`
}

// databaseDevboxSetup is what the Devbox environment needs so that the
// development server can serve the scaffolded DSN.
//
// Only PostgreSQL has one. Its Devbox plugin starts a server and nothing else:
// the cluster, the role, and the database are left to the developer, and the
// plain initdb a first-time user reaches for creates a cluster whose databases
// are SQL_ASCII, which stores a non-ASCII string without knowing what it is.
// The script does all three and can be run again: each step asks before it
// creates.
//
// It starts a server of its own when none is running, on a socket in a
// directory it makes for the purpose and on no TCP port, so it neither collides
// with a server already on 5432 nor depends on the project path being short
// enough to hold a Unix socket.
//
// PGUSER is set for the whole environment because the plugin's readiness probe
// is a bare pg_isready, which connects as the operating system user. No such
// role exists in a cluster whose superuser is postgres, and the server logged a
// FATAL for the probe every ten seconds, burying everything else pw dev prints.
func databaseDevboxSetup(options initOptions) devboxSetup {
	if !options.Database || !options.Devbox || options.Engine != enginePostgres {
		return devboxSetup{}
	}
	name := options.Name
	psql := `psql -h "$host" -d postgres`
	return devboxSetup{
		Env: map[string]string{"PGUSER": "postgres"},
		Script: []string{
			`set -e`,
			`[ -f "$PGDATA/PG_VERSION" ] || initdb -U postgres --auth=trust -E UTF8 --locale=C >/dev/null`,
			`host="$PGHOST"; started=`,
			`pg_ctl status >/dev/null 2>&1 || { host="$(mktemp -d)"; pg_ctl -w -l "$PGDATA/db-init.log" -o "-k '$host' -c listen_addresses=''" start >/dev/null; started=1; }`,
			psql + ` -tAc "SELECT 1 FROM pg_roles WHERE rolname = '` + name + `'" | grep -q 1 || ` +
				psql + ` -qc "` + strings.ReplaceAll(postgresCreateRole(name), `"`, `\"`) + `"`,
			psql + ` -tAc "SELECT 1 FROM pg_database WHERE datname = '` + name + `'" | grep -q 1 || ` +
				`createdb -h "$host" -O ` + name + ` ` + name,
			`[ -z "$started" ] || pg_ctl -w stop -m fast >/dev/null`,
			`echo 'PostgreSQL is ready: role and database ` + name + ` (UTF8)'`,
		},
	}
}
