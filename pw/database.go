package pw

import (
	"context"
	"fmt"
	"net/http"

	"github.com/shibukawa/popcornweb/pwdatabase"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/storage"
)

func validateConfiguredRuntime() error {
	if err := validateRuntimeConfig(
		ConfigContext[ServerConfig](nil),
		ConfigContext[SecurityConfig](nil),
		ConfigContext[MiddlewareConfig](nil),
		ConfigContext[ObservabilityConfig](nil),
	); err != nil {
		return err
	}
	// The cookie policy is the one rule here that reads the environment: the
	// same value is deliberate on a loopback development machine and a defect
	// anywhere else.
	if err := validateSessionConfig(ConfigContext[SessionConfig](nil), Env(), Development()); err != nil {
		return err
	}
	storageConfig, _ := pwruntime.RegisteredConfig[pwruntime.StorageConfig]()
	if err := storage.Validate(storageConfig); err != nil {
		return fmt.Errorf("popcornweb: %w", err)
	}
	if hostRunsPerRequest {
		cache, _ := pwruntime.RegisteredConfig[pwruntime.CacheConfig]()
		if err := refuseWorkerProcessState(cache, ConfigContext[SessionConfig](nil), ConfigContext[RateLimitConfig](nil), ConfigContext[MiddlewareConfig](nil).RDB, storageConfig); err != nil {
			return err
		}
	}
	return validateHTMLConfig(ConfigContext[HTMLConfig](nil))
}

// initializeRuntimeDatabase opens the configured pools and registers their
// release. Opening is pwdatabase's; what stays here is that a pool this
// process opened is closed by the same shutdown that closes an extension.
func initializeRuntimeDatabase() error {
	config := ConfigContext[MiddlewareConfig](nil).RDB
	if !config.Enabled {
		return nil
	}
	if pwdatabase.Connections() != nil {
		return nil
	}
	if err := pwdatabase.Start(config); err != nil {
		return err
	}
	registerCleanup("database", func(context.Context) error { return pwdatabase.Close() })
	return nil
}

// reportDatabaseConnections names, once at startup, the execution path each
// connection took. The choice is automatic — an engine with a native opener
// always bypasses database/sql — so the log line is what makes the active
// path observable rather than inferred.
func reportDatabaseConnections(connections *pwruntime.ConnectionSet) {
	for _, connection := range connections.Connections() {
		path := "database/sql"
		if connection.Native != nil {
			path = "native"
		}
		processLogger().Info("popcornweb database connection",
			String("connection", connection.Label),
			String("driver", connection.Driver),
			String("path", path),
			Bool("read_only", connection.ReadOnly),
		)
	}
}

// SelectWriteDB pins the connection group that framework-owned writes use:
// middleware.rdb.write_group, or the only group holding a writable connection.
//
// A replica can never be selected this way, so a caller that must write does
// not have to know the deployment topology.
func SelectWriteDB(r *http.Request) (context.Context, error) {
	return pwdatabase.SelectWriteDB(r.Context())
}

// SelectWriteDBContext is SelectWriteDB for code below the handler.
func SelectWriteDBContext(ctx context.Context) (context.Context, error) {
	return pwdatabase.SelectWriteDB(ctx)
}

// SelectSessionDB pins the connection group holding the session table:
// session.rdb.group, falling back to the framework write group.
func SelectSessionDB(r *http.Request) (context.Context, error) {
	return pwdatabase.SelectSessionDB(r.Context())
}

// SelectSessionDBContext is SelectSessionDB for code below the handler, which
// is where the framework's own session storage calls it.
func SelectSessionDBContext(ctx context.Context) (context.Context, error) {
	return pwdatabase.SelectSessionDB(ctx)
}

// configuredDatabaseDSN reports the DSN of the migration group so system:pw-cli
// can migrate and seed without reimplementing configuration precedence.
func configuredDatabaseDSN() (string, error) {
	return ConfigContext[MiddlewareConfig](nil).RDB.MigrationDSN()
}
