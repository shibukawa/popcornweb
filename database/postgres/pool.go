package postgres

import (
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/database/pgx/pgxpool"
)

// FromPool serves requests on a pool the caller built, as the connection
// pw.WithDatabase and testutil.WithDatabase accept.
//
// The framework opens its own pools from configuration, which is the right
// shape for a deployed process and the wrong one for a test that starts a
// fresh database per test case: that test already holds the pool, and the
// database it points at exists nowhere in configuration. This hands that
// pool over on the same native path a configured PostgreSQL connection
// takes, so generated queries, pw.Transaction, and WithConn run on it
// unchanged.
//
//	pool, err := pgxpool.New(ctx, server.DSN())
//	...
//	app := testutil.TestRun(t, Handlers(), nil,
//		testutil.WithDatabase(postgres.FromPool(pool)),
//		testutil.WithMigrations("../migrations"))
//
// The pool stays the caller's: the framework never closes it, so a pool
// shared by several test cases survives each one's shutdown. Its connection
// string is recorded as the connection's DataSource, which is what migration
// and seeding tooling dial for the database/sql handle they need. That is
// the string the pool's configuration was parsed from, so it has to be a
// URL, the form the framework's own DSNs take. If connection settings or
// DialFunc were changed after parsing, set DataSource to a URL that tooling
// can use to reach that same database. A custom dialer is not reconstructed.
// The group is
// left empty, which NewConnectionSet reads as the default group; set Group,
// ReadOnly, or Label on the result before passing it when the connection
// stands in for another.
func FromPool(pool *pgxpool.Pool) pwruntime.Connection {
	connection := pwruntime.Connection{Driver: Dialect}
	if pool == nil {
		// A nil handle is refused by NewConnectionSet with a message naming
		// the group, which beats a nil dereference on the first query.
		return connection
	}
	connection.Native = &nativeDB{pool: pool, borrowed: true}
	if config := pool.Config(); config != nil && config.ConnConfig != nil {
		connection.DataSource = config.ConnConfig.ConnString()
	}
	return connection
}
