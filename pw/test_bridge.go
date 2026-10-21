package pw

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"

	"github.com/shibukawa/popcornweb/internal/pwtestbridge"
	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwdatabase"
	"github.com/shibukawa/popcornweb/pwruntime"
)

// SnapshotTestConfigs copies the registered configuration for testutil. It is a
// direct call rather than an init-registered hook so a production binary that
// does not import testutil lets the linker discard this entire test seam.
func SnapshotTestConfigs() (pwtestbridge.Configs, error) {
	if err := ParseConfig(); err != nil {
		return nil, err
	}
	configs := make(pwtestbridge.Configs)
	for typ, value := range pwconfig.Snapshot() {
		configs[typ] = value
	}
	return configs, nil
}

// PrepareTestRuntime builds an isolated runtime for testutil. Production code
// has no reference to it, so its database and reflection paths are dead code in
// an application binary.
func PrepareTestRuntime(handler http.Handler, configs pwtestbridge.Configs, options pwtestbridge.Options) (pwtestbridge.Prepared, error) {
	if handler == nil {
		return pwtestbridge.Prepared{}, fmt.Errorf("popcornweb: nil test handler")
	}
	server := testConfigValue[ServerConfig](configs)
	security := testConfigValue[SecurityConfig](configs)
	middleware := testConfigValue[MiddlewareConfig](configs)
	observability := testConfigValue[ObservabilityConfig](configs)
	if err := validateRuntimeConfig(server, security, middleware, observability); err != nil {
		return pwtestbridge.Prepared{}, err
	}
	if err := validateOperationalEndpointCollisions(handler, server); err != nil {
		return pwtestbridge.Prepared{}, err
	}
	// A test borrows the supplied connection or opens the migration group.
	// Schema, seed data and the test transaction live in that one database.
	// Its single-connection set collapses every group name onto the same pool,
	// so SelectDB with a replica group needs no test-only branch.
	var testConnection RDBConnectionConfig
	if options.Database != nil && !middleware.RDB.Enabled {
		return pwtestbridge.Prepared{}, fmt.Errorf("popcornweb: WithDatabase requires middleware.rdb.enabled")
	}
	if middleware.RDB.Enabled {
		resolved, err := testDatabaseConnection(middleware.RDB)
		if err != nil {
			return pwtestbridge.Prepared{}, err
		}
		testConnection = resolved
	}
	// Savepoint support decides whether a test transaction can host the
	// application's own transactions, so it is checked before opening a pool.
	if options.Transaction {
		if !middleware.RDB.Enabled {
			return pwtestbridge.Prepared{}, fmt.Errorf("popcornweb: test transaction requires middleware.rdb.enabled")
		}
		target, err := pwconfig.Target(testConnection.DSN)
		if err != nil {
			return pwtestbridge.Prepared{}, err
		}
		dialect := target.Dialect
		if options.Database != nil {
			dialect = options.Database.Driver
		}
		if !pwruntime.SupportsSavepoint(dialect) {
			return pwtestbridge.Prepared{}, fmt.Errorf("popcornweb: test transaction requires a driver with savepoint support, got %q", dialect)
		}
	}
	var dbClose func() error
	var db = (*sql.DB)(nil)
	var driver string
	var connections *pwruntime.ConnectionSet
	var scope *pwruntime.TransactionScope
	if middleware.RDB.Enabled {
		var connection pwruntime.Connection
		var err error
		if options.Database != nil {
			connection = *options.Database
		} else {
			connection, err = pwdatabase.OpenOne(testConnection, testConnection.Group)
		}
		if err != nil {
			return pwtestbridge.Prepared{}, err
		}
		driver = connection.Driver
		db = connection.DB
		var closers []func() error
		if options.Database == nil {
			closers = append(closers, connection.Close)
		}
		closeOpened := func() {
			for _, close := range closers {
				_ = close()
			}
		}
		if connection.Native != nil {
			// Requests run natively, but schema preparation, seeding, and
			// assertions are database/sql tooling, which the engine's *sql.DB
			// opener still serves. That second handle is what the test side of
			// the bridge sees as DB.
			dsn := connection.DataSource
			if dsn == "" && options.Database == nil {
				dsn = testConnection.DSN
			}
			if dsn == "" {
				closeOpened()
				return pwtestbridge.Prepared{}, fmt.Errorf("popcornweb: supplied native database needs DataSource for tooling")
			}
			target, targetErr := pwconfig.Target(dsn)
			if targetErr == nil {
				db, targetErr = target.Open()
			}
			if targetErr != nil {
				closeOpened()
				return pwtestbridge.Prepared{}, fmt.Errorf("popcornweb: open tooling database: %w", targetErr)
			}
			closers = append(closers, db.Close)
		}
		set, err := pwruntime.NewConnectionSet("", []pwruntime.Connection{connection})
		if err != nil {
			for _, closer := range closers {
				_ = closer()
			}
			return pwtestbridge.Prepared{}, err
		}
		connections = set
		dbClose = func() error {
			var errs []error
			for _, closer := range closers {
				if err := closer(); err != nil {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		}
		if options.Transaction {
			// The scope runs on the connection the requests use, whichever
			// kind of pool backs it; the test side reaches the same
			// transaction through TxScope.ActiveExecutor.
			scope = connection.TransactionScope()
		}
	}
	if options.PrepareDatabase != nil {
		if err := options.PrepareDatabase(db); err != nil {
			if dbClose != nil {
				_ = dbClose()
			}
			return pwtestbridge.Prepared{}, err
		}
	}
	resources := pwruntime.Resources{
		Configs: map[reflect.Type]any(configs),
		Log:     pwruntime.NewLogBackend(pwruntime.LevelInfo, pwruntime.NewSlogSink(slog.Default().Handler())),
		DB:      db,
		// The single-connection set collapses every group name onto the one
		// pool, exactly as the nil set did through the DB fallback, and it is
		// what carries a native connection to the executor seam.
		Connections: connections,
		DBDriver:    driver,
		TxScope:     scope,
		Query:       resolveQueryDiagnostics(testConfigValue[ObservabilityConfig](configs), Development()),
		// A test bridge exports nothing, so auto resolves off here and a suite
		// that wants the span tree names observability.trace.enabled on.
		Trace: resolveTracing(testConfigValue[ObservabilityConfig](configs), false),
	}
	wrapped, err := buildRuntimeHandler(handler, server, security, middleware, resources, false)
	if err != nil {
		if dbClose != nil {
			_ = dbClose()
		}
		return pwtestbridge.Prepared{}, err
	}
	if dbClose == nil {
		dbClose = func() error { return nil }
	}
	return pwtestbridge.Prepared{
		Handler:   wrapped,
		DB:        db,
		Driver:    driver,
		TxScope:   scope,
		Resources: resources,
		Close:     dbClose,
	}, nil
}

// testDatabaseConnection picks the one connection a test runs against: the
// migration group, where the schema and the seed data are.
func testDatabaseConnection(config RDBConfig) (RDBConnectionConfig, error) {
	connections, err := pwconfig.ResolveConnections(config)
	if err != nil {
		return RDBConnectionConfig{}, err
	}
	group, err := pwconfig.ResolveMigrationGroup(config, connections)
	if err != nil {
		return RDBConnectionConfig{}, err
	}
	for _, connection := range connections {
		if connection.Group == group {
			return connection, nil
		}
	}
	return RDBConnectionConfig{}, fmt.Errorf("popcornweb: migration group %q has no connection", group)
}

func testConfigValue[T any](configs pwtestbridge.Configs) T {
	if value, ok := configs[reflect.TypeFor[T]()].(T); ok {
		return value
	}
	var zero T
	return zero
}
