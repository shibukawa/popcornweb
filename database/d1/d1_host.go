//go:build !(js && wasm)

// Package d1 registers Cloudflare D1 for a d1:// DSN.
//
// This is the host build: the scheme resolves, so configuration carrying a
// d1:// connection validates and reports the sqlite dialect, but opening it
// fails, because the binding exists only inside a Worker. Development runs on
// a sqlite:// file, which is the same dialect.
package d1

import (
	"database/sql"
	"errors"

	"github.com/shibukawa/popcornweb/database"
)

// Dialect is the dialect this engine reports, which is SQLite's.
const Dialect = "sqlite"

// Scheme is the DSN prefix that selects this engine.
const Scheme = "d1"

// ErrOutsideWorker is returned by Open on a host that is not a Cloudflare
// Worker.
var ErrOutsideWorker = errors.New("popcornweb/database/d1: a d1:// connection opens only inside a Cloudflare Worker; use a sqlite:// file in development")

func init() {
	database.Register(database.Engine{
		Dialect: Dialect,
		Schemes: []string{Scheme},
		Open: func(string) (*sql.DB, error) {
			return nil, ErrOutsideWorker
		},
	})
}
