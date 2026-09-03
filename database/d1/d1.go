//go:build js && wasm

// Package d1 registers Cloudflare D1 for a d1:// DSN.
//
// D1 is SQLite, reached through the binding a Worker's environment carries,
// so the engine registers under the sqlite dialect: the session store, the
// authentication state store, the savepoint and EXPLAIN rules and the
// generated queries all take the SQLite path, and only the opener differs.
// The rest of the DSN is the wrangler binding name, never a path or a URL.
//
// The generated Cloudflare Workers entry point links this package, so an
// application names a d1:// connection in config.prod.toml and changes no
// source. Outside a Worker the scheme resolves but cannot open, which is what
// the host build of this package says.
package d1

import (
	"database/sql"

	"github.com/shibukawa/popcornweb/database"
	_ "github.com/syumai/workers/cloudflare/d1"
)

// Dialect is the dialect this engine reports, which is SQLite's.
const Dialect = "sqlite"

// Scheme is the DSN prefix that selects this engine.
const Scheme = "d1"

func init() {
	database.Register(database.Engine{
		Dialect: Dialect,
		Schemes: []string{Scheme},
		// The data source is the binding name; the driver looks it up on the
		// Worker env the loader supplied.
		Open: func(dataSource string) (*sql.DB, error) {
			return sql.Open("d1", dataSource)
		},
	})
}
