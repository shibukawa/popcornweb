package auth

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shibukawa/popcornweb/authstate"
	"github.com/shibukawa/popcornweb/pwdatabase"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/sqlbind"
)

// Backend names. rdb is the default and the behavior every project had before
// a second one existed.
const (
	BackendRDB       = "rdb"
	BackendDynamo    = "dynamo"
	BackendFirestore = "firestore"
)

// Resources are what the framework has already opened by the time a backend is
// asked for its stores. A backend that needs none of them ignores them, which
// is what a store reading its client from the request context does.
type Resources struct {
	// DB is the session-group database/sql handle. It is nil when
	// middleware.rdb is not enabled, and also for an engine whose request-time
	// path bypasses database/sql, which is how PostgreSQL is served; Executor
	// is the handle that exists on every relational connection.
	DB *sql.DB
	// Executor is the pool-level statement surface of the same connection: DB
	// itself for a database/sql engine, the native pool for one that bypasses
	// it, and nil when middleware.rdb is not enabled. A backend that only
	// issues statements reads this instead of DB.
	Executor sqlbind.SQLExecutor
	// DBDriver is the engine the DSN resolved to, empty when there is no
	// relational connection.
	DBDriver string
}

// executor is the handle the relational backend runs on. A caller that filled
// in only DB, which is every caller written before Executor existed, is served
// by it.
func (r Resources) executor() sqlbind.SQLExecutor {
	if r.Executor != nil {
		return r.Executor
	}
	if r.DB != nil {
		return r.DB
	}
	return nil
}

// Backend is one storage implementation of the four stores this package owns.
//
// The ceremony store is opened per namespace rather than supplied whole,
// because this package keeps three kinds of ceremony record and two of their
// types are unexported: a backend package could not name them to build a
// typed store, so it supplies raw storage and the codec is added back here.
type Backend struct {
	// OpenState opens the ceremony store of one namespace.
	OpenState func(ctx context.Context, namespace string) (authstate.RawStore, error)
	// Allowlist, Credentials, and Bootstrap are the account-side stores. Each
	// may be nil when the selected mode never reads it.
	Allowlist   AllowlistStore
	Credentials CredentialStore
	Bootstrap   BootstrapStore
}

// statePruner is implemented by a ceremony store whose expired records have to
// be swept.
//
// A store that decides expiry on read and leaves deletion to the service
// implements nothing, and the sweep skips it. That is not an omission: on
// DynamoDB the equivalent of a bounded DELETE is a Scan over every record ever
// written, which costs more than the records it would remove.
type statePruner interface {
	Prune(ctx context.Context, before time.Time, limit int) (int64, error)
}

// BackendFactory opens one backend. It is registered under a name, and
// auth.backend selects which name is used.
type BackendFactory func(ctx context.Context, config Config, resources Resources) (Backend, error)

var backendState struct {
	sync.RWMutex
	factories map[string]BackendFactory
}

// RegisterBackend records a storage backend under its configuration name.
//
// A backend package registers itself from init, so a project links the backend
// it runs and no other. Registering the same name twice is a programming error
// rather than a configuration one.
func RegisterBackend(name string, factory BackendFactory) {
	if strings.TrimSpace(name) == "" {
		panic("popcornweb/plugin/auth: empty backend name")
	}
	if factory == nil {
		panic("popcornweb/plugin/auth: backend " + name + " has no factory")
	}
	backendState.Lock()
	defer backendState.Unlock()
	if backendState.factories == nil {
		backendState.factories = make(map[string]BackendFactory)
	}
	if _, taken := backendState.factories[name]; taken {
		panic("popcornweb/plugin/auth: backend " + name + " is already registered")
	}
	backendState.factories[name] = factory
}

func backendFactory(name string) (BackendFactory, bool) {
	backendState.RLock()
	defer backendState.RUnlock()
	factory, present := backendState.factories[name]
	return factory, present
}

func registeredBackends() []string {
	backendState.RLock()
	defer backendState.RUnlock()
	names := make([]string, 0, len(backendState.factories))
	for name := range backendState.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// openBackend resolves the configured name and opens it.
//
// An unknown name names what is linked rather than what exists, because the
// difference between the two is an import line the deployment can add.
func openBackend(ctx context.Context, config Config, resources Resources) (Backend, error) {
	name := config.backendName()
	factory, present := backendFactory(name)
	if !present {
		return Backend{}, fmt.Errorf(
			"auth.backend = %q is not linked; registered backends are %s. "+
				"Import the package that provides it, or register one with auth.RegisterBackend",
			name, strings.Join(registeredBackends(), ", "))
	}
	return factory(ctx, config, resources)
}

// openState opens one ceremony store, keeps the raw store for the sweep, and
// puts the codec back on for the caller.
func openState[T any](ctx context.Context, rt *runtime, namespace string, codec authstate.Codec[T]) (authstate.Store[T], error) {
	if rt.backend.OpenState == nil {
		return nil, fmt.Errorf("auth: backend opened no ceremony store for %q", namespace)
	}
	raw, err := rt.backend.OpenState(ctx, namespace)
	if err != nil {
		return nil, err
	}
	if pruner, sweeps := raw.(statePruner); sweeps {
		rt.pruners = append(rt.pruners, pruner)
	}
	return authstate.Typed[T](raw, codec), nil
}

func init() {
	RegisterBackend(BackendRDB, openRelationalBackend)
}

// openRelationalBackend is the framework default: the popcornweb_ tables of
// rule:framework-owned-tables, over the middleware database.
func openRelationalBackend(ctx context.Context, config Config, resources Resources) (Backend, error) {
	db := resources.executor()
	if db == nil {
		return Backend{}, errAuthNeedsRDB
	}
	schemaCtx, cancel := context.WithTimeout(ctx, schemaTimeout)
	defer cancel()
	// The tables this package owns are migration-owned, so startup verifies
	// them instead of creating them.
	if err := verifyTables(schemaCtx, db, resources.DBDriver, config); err != nil {
		return Backend{}, err
	}

	return Backend{
		OpenState: func(ctx context.Context, namespace string) (authstate.RawStore, error) {
			store, err := authstate.NewSQLRawStore(db, authstate.SQLOptions{
				Dialect:   resources.DBDriver,
				Namespace: namespace,
			})
			if err != nil {
				return nil, err
			}
			// The table already exists, so this validates its column layout
			// only.
			if err := store.EnsureSchema(ctx); err != nil {
				return nil, fmt.Errorf("auth state schema: %w", err)
			}
			return store, nil
		},
		Allowlist:   resolveAllowlistStore(db, resources.DBDriver),
		Credentials: resolveCredentialStore(db, resources.DBDriver),
		Bootstrap:   resolveBootstrapStore(db, resources.DBDriver),
	}, nil
}

func resolveCredentialStore(db sqlbind.SQLExecutor, dialect string) CredentialStore {
	if store := installedCredentialStore(); store != nil {
		return store
	}
	return dbStore{db: db, dialect: dialect}
}

func resolveBootstrapStore(db sqlbind.SQLExecutor, dialect string) BootstrapStore {
	if store := installedBootstrapStore(); store != nil {
		return store
	}
	return bootstrapStore{db: db, dialect: dialect}
}

// schemaTimeout bounds the startup verification a backend performs.
const schemaTimeout = 10 * time.Second

// errAuthNeedsRDB is the relational backend saying what it needs. It is the
// backend's error rather than the package's: a project on another backend never
// reaches it.
var errAuthNeedsRDB = fmt.Errorf(
	`auth.backend = %q requires middleware.rdb.enabled = true`, BackendRDB)

// backendResources opens what a backend might want. A project with no
// relational middleware gets an empty set rather than an error, because whether
// that is a problem is the selected backend's question to answer.
func backendResources(ctx context.Context) (Resources, error) {
	// The executor is what is asked for, not the *sql.DB: a connection served
	// by a native pool has no *sql.DB, and reading that as "no database" is
	// what once refused the relational backend on PostgreSQL.
	if _, enabled := pwruntime.ConnectionExecutor(ctx); !enabled {
		return Resources{}, nil
	}
	// The auth tables are written on every login, so they live in the session
	// group rather than in the default group, which is normally a replica.
	sessionCtx, err := pwdatabase.SelectSessionDB(ctx)
	if err != nil {
		return Resources{}, err
	}
	executor, present := pwruntime.ConnectionExecutor(sessionCtx)
	if !present {
		return Resources{}, nil
	}
	db, _ := pwruntime.DB(sessionCtx)
	driver, _ := pwruntime.DBDriver(sessionCtx)
	return Resources{DB: db, Executor: executor, DBDriver: driver}, nil
}

// backendName is the selected backend, defaulting to the relational one so a
// configuration written before the key existed keeps its behavior.
func (c Config) backendName() string {
	if c.Backend == "" {
		return BackendRDB
	}
	return c.Backend
}
