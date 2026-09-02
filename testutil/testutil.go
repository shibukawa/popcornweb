// Package testutil runs Popcorn Web applications from isolated copies of the
// registered runtime configuration.
package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/shibukawa/popcornweb/contrib/devidp"
	"github.com/shibukawa/popcornweb/internal/dbseed"
	"github.com/shibukawa/popcornweb/internal/pwtestbridge"
	"github.com/shibukawa/popcornweb/migrate"
	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/pwruntime"
)

// TestingT is the subset of testing.T used by TestRun.
//
// It stays an interface so this shipped package never imports testing.
// Fatalf reports setup failure that invalidates the test; Errorf reports an
// assertion failure that lets the test continue.
type TestingT interface {
	Helper()
	Cleanup(func())
	Fatalf(string, ...any)
	Errorf(string, ...any)
}

// Config is an isolated copy of all registered framework and application
// configuration values. Its three operations are methods, so a test reads
// config.Update rather than naming this package twice on one line.
type Config struct {
	values pwtestbridge.Configs
}

// Get returns one typed value from a copied configuration. It is the one entry
// with nothing to infer the type from, so the call site writes it:
// config.Get[pw.ServerConfig](). A nil configuration answers the zero value.
func (c *Config) Get[T any]() T {
	if c != nil {
		if value, ok := c.values[reflect.TypeFor[T]()].(T); ok {
			return deepClone(value)
		}
	}
	var zero T
	return zero
}

// Set replaces one typed value in a copied configuration.
func (c *Config) Set[T any](value T) {
	if c == nil {
		panic("testutil: nil Config")
	}
	c.values[reflect.TypeFor[T]()] = deepClone(value)
}

// Update edits one typed value in a copied configuration, inferring the type
// from the edit's parameter.
func (c *Config) Update[T any](edit func(*T)) {
	if edit == nil {
		return
	}
	value := c.Get[T]()
	edit(&value)
	c.Set(value)
}

type runSettings struct {
	migration   []migrate.Option
	transaction bool
	seedDir     string
	seedFiles   []string
	idp         *idpSettings
}

// RunOption configures TestRun resources.
type RunOption func(*runSettings) error

// WithMigrations installs the migrated schema from a migration directory.
//
// The schema is installed by replaying a snapshot rather than by running every
// migration, so the cost is paid once per test binary and an in-memory database
// works on both the host and the TinyGo execution path.
func WithMigrations(directory string) RunOption {
	return func(settings *runSettings) error {
		if strings.TrimSpace(directory) == "" {
			return fmt.Errorf("testutil: empty migration directory")
		}
		settings.migration = append(settings.migration, migrate.WithDir(directory))
		return nil
	}
}

// WithMigrationsFS installs the migrated schema from an embedded migration tree.
func WithMigrationsFS(sources fs.FS) RunOption {
	return func(settings *runSettings) error {
		if sources == nil {
			return fmt.Errorf("testutil: nil migration filesystem")
		}
		settings.migration = append(settings.migration, migrate.WithFS(sources))
		return nil
	}
}

// WithTransaction runs every request of this test server inside one
// transaction that is rolled back when the test finishes, so tests sharing one
// database stay independent and may run in parallel. Framework transactions
// started by the application nest into it as savepoints, which requires a
// driver with savepoint support.
func WithTransaction(enabled bool) RunOption {
	return func(settings *runSettings) error {
		settings.transaction = enabled
		return nil
	}
}

// Server is a running application created by TestRun.
type Server struct {
	URL    string
	Port   int
	Config *Config
	DB     *sql.DB
	server *http.Server
	scope  *pwruntime.TransactionScope
	ctx    context.Context

	once        sync.Once
	close       func() error
	rollbackErr error

	seedDir     string
	transaction bool
	idp         *devidp.Server
	idpInfo     IdPInfo
}

// Context returns a context carrying the same runtime resources the server
// installs on requests, including the WithTransaction transaction. Use it to
// prepare or assert data inside the test transaction.
func (server *Server) Context() context.Context {
	return server.ctx
}

// Client returns an HTTP client configured for the test server.
func (server *Server) Client() *http.Client {
	return &http.Client{}
}

// Close stops the server, rolls back a WithTransaction transaction, and
// releases its copied runtime resources.
func (server *Server) Close() {
	server.once.Do(func() {
		_ = server.server.Close()
		server.rollbackErr = server.scope.Rollback()
		if server.close != nil {
			_ = server.close()
		}
	})
}

// TestRun copies every registered configuration, defaults the copied server
// port to -1, applies customize, initializes copied runtime resources, and
// starts the application. Port -1 selects an available loopback port.
func TestRun(t TestingT, handler http.Handler, customize func(*Config), options ...RunOption) *Server {
	t.Helper()
	snapshot, err := pw.SnapshotTestConfigs()
	if err != nil {
		t.Fatalf("copy Popcorn Web configuration: %v", err)
		return nil
	}
	config := &Config{values: cloneConfigs(snapshot)}
	config.Update(func(server *pw.ServerConfig) {
		server.Port = -1
	})
	if customize != nil {
		customize(config)
	}
	settings := runSettings{}
	for _, apply := range options {
		if apply == nil {
			continue
		}
		if err := apply(&settings); err != nil {
			t.Fatalf("configure Popcorn Web TestRun: %v", err)
			return nil
		}
	}

	// The identity provider starts before the application so its issuer and
	// generated client credentials can reach the copied configuration.
	var idpServer *devidp.Server
	var idpInfo IdPInfo
	if settings.idp != nil {
		idpServer, idpInfo, err = startIdentityProvider(settings.idp, config)
		if err != nil {
			t.Fatalf("start Popcorn Web TestRun identity provider: %v", err)
			return nil
		}
		t.Cleanup(func() { _ = idpServer.Close() })
	}

	serverConfig := config.Get[pw.ServerConfig]()
	if serverConfig.Port < -1 || serverConfig.Port > 65535 {
		t.Fatalf("listen for Popcorn Web TestRun: port must be -1 or between 0 and 65535")
		return nil
	}
	address := "127.0.0.1:0"
	if serverConfig.Port >= 0 {
		address = net.JoinHostPort("127.0.0.1", strconv.Itoa(serverConfig.Port))
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listen for Popcorn Web TestRun: %v", err)
		return nil
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	serverConfig.Port = actualPort
	config.Set(serverConfig)

	// The schema is installed inside Prepare, because extensions verify their
	// own tables while the runtime handler is built.
	prepareDatabase := func(db *sql.DB) error {
		if len(settings.migration) == 0 {
			return nil
		}
		if db == nil {
			return fmt.Errorf("configured RDB is disabled")
		}
		// The migrations run against the group that receives them, which is
		// where the seeds and pw migrate also write.
		dsn, err := config.Get[pw.MiddlewareConfig]().RDB.MigrationDSN()
		if err != nil {
			return err
		}
		return installMigrations(context.Background(), db, dsn, settings.migration)
	}
	prepared, err := pw.PrepareTestRuntime(handler, config.values, pwtestbridge.Options{
		Transaction: settings.transaction, PrepareDatabase: prepareDatabase,
	})
	if err != nil {
		_ = listener.Close()
		t.Fatalf("initialize Popcorn Web TestRun: %v", err)
		return nil
	}
	seedDir := settings.seedDir
	if seedDir == "" {
		seedDir = dbseed.DefaultDir
	}
	// Seeding commits before the test transaction opens, so datasets are the
	// shared baseline and only per-test writes are rolled back. It also keeps
	// seeding off the connection the test transaction holds.
	if len(settings.seedFiles) > 0 {
		if err := applySeed(config, dbseed.FromSQL(prepared.DB), false, seedDir, settings.seedFiles); err != nil {
			_ = listener.Close()
			_ = prepared.Close()
			t.Fatalf("initialize Popcorn Web TestRun seed: %v", err)
			return nil
		}
	}
	// The test transaction opens after schema setup so DDL never runs inside
	// it and never competes with it for the single held connection.
	if settings.transaction {
		if err := prepared.TxScope.Begin(context.Background(), nil); err != nil {
			_ = listener.Close()
			_ = prepared.Close()
			t.Fatalf("begin Popcorn Web TestRun transaction: %v", err)
			return nil
		}
	}
	instance := &http.Server{
		Addr:              listener.Addr().String(),
		Handler:           prepared.Handler,
		ReadHeaderTimeout: serverConfig.ReadHeaderTimeout,
		ReadTimeout:       serverConfig.ReadTimeout,
		WriteTimeout:      serverConfig.WriteTimeout,
		IdleTimeout:       serverConfig.IdleTimeout,
	}
	result := &Server{
		URL:         "http://" + listener.Addr().String(),
		Port:        actualPort,
		Config:      config,
		DB:          prepared.DB,
		server:      instance,
		scope:       prepared.TxScope,
		ctx:         pwruntime.WithResources(context.Background(), prepared.Resources),
		close:       prepared.Close,
		seedDir:     seedDir,
		transaction: settings.transaction,
		idp:         idpServer,
		idpInfo:     idpInfo,
	}
	t.Cleanup(result.Close)
	if settings.transaction {
		// Registered last so it runs first and can report the rollback result.
		t.Cleanup(func() {
			result.Close()
			if result.rollbackErr != nil {
				t.Fatalf("roll back Popcorn Web TestRun transaction: %v", result.rollbackErr)
			}
		})
	}
	go func() {
		_ = instance.Serve(listener)
	}()
	return result
}

func cloneConfigs(source pwtestbridge.Configs) pwtestbridge.Configs {
	result := make(pwtestbridge.Configs, len(source))
	for typ, value := range source {
		result[typ] = cloneReflect(reflect.ValueOf(value)).Interface()
	}
	return result
}

func deepClone[T any](value T) T {
	cloned := cloneReflect(reflect.ValueOf(value))
	if !cloned.IsValid() {
		var zero T
		return zero
	}
	return cloned.Interface().(T)
}

func cloneReflect(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := cloneReflect(value.Elem())
		result := reflect.New(value.Type()).Elem()
		result.Set(cloned)
		return result
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.New(value.Type().Elem())
		result.Elem().Set(cloneReflect(value.Elem()))
		return result
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			result.Index(index).Set(cloneReflect(value.Index(index)))
		}
		return result
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			result.SetMapIndex(cloneReflect(iterator.Key()), cloneReflect(iterator.Value()))
		}
		return result
	case reflect.Array:
		result := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			result.Index(index).Set(cloneReflect(value.Index(index)))
		}
		return result
	case reflect.Struct:
		result := reflect.New(value.Type()).Elem()
		result.Set(value)
		for index := 0; index < value.NumField(); index++ {
			if result.Field(index).CanSet() && value.Type().Field(index).IsExported() {
				result.Field(index).Set(cloneReflect(value.Field(index)))
			}
		}
		return result
	default:
		return value
	}
}
