# Caller-owned pgmem databases

Run `go test -v ./...` in this directory. This separate module keeps pgmem out
of Popcorn Web's core dependencies. `TestMain` prepares a pgmem fixture; tests
borrow its pools with `testutil.WithDatabase(postgres.FromPool(pool))`.

The tests use real PostgreSQL and HTTP to verify migration, seed/assert helpers,
native `WithConn`, transaction rollback, independent databases, and ownership.
Closing a test server closes its extra `database/sql` tooling handle and leaves
the supplied pool open. The fixture closes its pools and pgmem forks afterwards.

`testutil.WithDatabase` replaces the test's copied connection configuration, so
only `middleware.rdb.enabled` must be set; no connection string is needed in a
configuration file. The pool's DSN is used for migration and tooling connections.
The DSN must reach the same database. A custom dialer on the original pool is not
automatically applied to those separate connections.
