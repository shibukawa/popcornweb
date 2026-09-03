// Package database resolves a Popcorn Web rdb DSN onto the engine that opens it.
//
// A DSN is scheme://rest, and the scheme names an engine rather than a
// database/sql driver. The distinction matters: the PostgreSQL package builds
// its *sql.DB from a connector and registers no driver name at all, so
// sql.Open could never reach it. The scheme also decides how much of the DSN
// the engine receives, because a libpq URL is a valid DSN on its own while a
// SQLite path and a go-sql-driver DSN are not URLs and have to lose the prefix.
//
// An engine registers itself from init, so a project links only the engine its
// DSN selects:
//
//	import _ "github.com/shibukawa/popcornweb/database/postgres"
//
// An engine may also register a native opener, which the request-time query
// path uses instead of database/sql: the sql.DB pool mutex, the per-conn
// mutex, and driver.Value boxing all disappear from that path. The *sql.DB
// opener stays mandatory beside it, because migration and seeding tooling
// runs on database/sql regardless of how requests are served.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shibukawa/tinybind-go/sqlbind"
)

// OpenFunc opens a pool for one data source. It wraps sql.Open for an engine
// that registers a driver name, and the engine's own constructor for one that
// does not.
type OpenFunc func(dataSource string) (*sql.DB, error)

// PoolBounds carries the configured pool limits to a native opener. A native
// pool is configured at construction rather than adjusted afterwards, which is
// why the bounds travel with the open call instead of being applied to its
// result the way sql.DB setters are.
type PoolBounds struct {
	MaxOpenConns int
	// MaxIdleConns exists for symmetry with the sql.DB setter but has no
	// equivalent on a native pool, which prunes idle connections by
	// ConnMaxIdleTime instead. An engine that cannot express it ignores it.
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// NativeExecutor is the statement surface of a native handle. It satisfies
// sqlbind.SQLExecutor so the framework's executor seam can store it, and adds
// the driver-agnostic query path sqlbind.Query dispatches to; the QueryContext
// half of SQLExecutor is expected to be sqlbind.UnimplementedQuerier, because
// a backend outside database/sql cannot construct a *sql.Rows.
type NativeExecutor interface {
	sqlbind.SQLExecutor
	sqlbind.RowsQuerier
}

// NativeTxOptions selects how a native transaction begins.
type NativeTxOptions struct {
	ReadOnly bool
}

// NativeTx is one open transaction on a NativeDB. Commit and Rollback take a
// context because the native protocol sends them on the wire, unlike sql.Tx
// which bound its context at Begin.
type NativeTx interface {
	NativeExecutor
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// NativeDB is the request-time handle of an engine that bypasses database/sql.
// It is framework-defined so the registry, and everything above it, never sees
// a driver type.
type NativeDB interface {
	NativeExecutor
	BeginTx(ctx context.Context, options NativeTxOptions) (NativeTx, error)
	Ping(ctx context.Context) error
	Close() error
}

// OpenNativeFunc opens a native pool for one data source with the configured
// bounds already applied.
type OpenNativeFunc func(ctx context.Context, dataSource string, bounds PoolBounds) (NativeDB, error)

// Engine describes one database a project can be configured against.
type Engine struct {
	// Dialect is the canonical name reported by pw.DBDriver, and the one that
	// selects savepoint, EXPLAIN, and migration behavior. Every scheme of an
	// engine reports the same dialect, so an alias cannot produce a second one.
	Dialect string
	// Schemes are the DSN prefixes that select this engine.
	Schemes []string
	// Open opens the pool for the resolved data source.
	Open OpenFunc
	// OpenNative, when set, opens the pool the request-time query path uses
	// instead of database/sql. Open stays required beside it, because
	// migration and seeding tooling runs on database/sql regardless.
	OpenNative OpenNativeFunc
	// KeepScheme hands Open the whole configured DSN instead of the part after
	// the scheme, for an engine whose DSN is already a URL.
	KeepScheme bool
}

var registry struct {
	sync.RWMutex
	engines map[string]Engine
}

// Register adds an engine. Engine packages call it from init; registering the
// same dialect twice is harmless, so importing an engine along two paths is
// not an error.
func Register(engine Engine) {
	switch {
	case engine.Dialect == "":
		panic("popcornweb/database: engine has no dialect")
	case engine.Open == nil:
		panic("popcornweb/database: engine " + engine.Dialect + " has no Open")
	case len(engine.Schemes) == 0:
		panic("popcornweb/database: engine " + engine.Dialect + " has no scheme")
	}
	registry.Lock()
	defer registry.Unlock()
	if registry.engines == nil {
		registry.engines = make(map[string]Engine)
	}
	for _, scheme := range engine.Schemes {
		if existing, taken := registry.engines[scheme]; taken && existing.Dialect != engine.Dialect {
			panic(fmt.Sprintf("popcornweb/database: scheme %q is already registered by %q",
				scheme, existing.Dialect))
		}
		registry.engines[scheme] = engine
	}
}

// Target is a resolved DSN: which dialect it names, and what to open.
type Target struct {
	// Dialect is the canonical engine name.
	Dialect string
	// DataSource is the part of the DSN the engine expects.
	DataSource string
	open       OpenFunc
	openNative OpenNativeFunc
}

// Open opens the pool. The caller applies its own pool bounds and ping.
func (target Target) Open() (*sql.DB, error) {
	if target.open == nil {
		return nil, errors.New("popcornweb: database target was not resolved")
	}
	return target.open(target.DataSource)
}

// Native reports whether the engine serves the request-time path natively.
func (target Target) Native() bool {
	return target.openNative != nil
}

// OpenNative opens the native pool with bounds applied. The caller still
// pings, because a native pool connects lazily.
func (target Target) OpenNative(ctx context.Context, bounds PoolBounds) (NativeDB, error) {
	if target.openNative == nil {
		return nil, errors.New("popcornweb: engine " + target.Dialect + " has no native opener")
	}
	return target.openNative(ctx, target.DataSource, bounds)
}

// Scheme splits the framework scheme://rest syntax. It validates the shape
// only; whether an engine serves the scheme is Resolve's answer. The DSN is
// never quoted into the error, because it carries the password.
func Scheme(configured string) (scheme, rest string, err error) {
	scheme, rest, found := strings.Cut(strings.TrimSpace(configured), "://")
	if !found || scheme == "" || rest == "" {
		return "", "", errors.New("a DSN must use driver://dsn syntax")
	}
	return scheme, rest, nil
}

// Resolve selects the engine for a DSN.
func Resolve(configured string) (Target, error) {
	scheme, rest, err := Scheme(configured)
	if err != nil {
		return Target{}, err
	}
	registry.RLock()
	engine, served := registry.engines[scheme]
	registry.RUnlock()
	if !served {
		return Target{}, fmt.Errorf("the DSN names scheme %q, %s", scheme, remedy(scheme))
	}
	dataSource := rest
	if engine.KeepScheme {
		dataSource = strings.TrimSpace(configured)
	}
	return Target{
		Dialect:    engine.Dialect,
		DataSource: dataSource,
		open:       engine.Open,
		openNative: engine.OpenNative,
	}, nil
}

// Dialect reports the canonical engine name for a DSN without opening it.
func Dialect(configured string) (string, error) {
	target, err := Resolve(configured)
	if err != nil {
		return "", err
	}
	return target.Dialect, nil
}

// Schemes lists what this binary can open, for a caller reporting the choice.
func Schemes() []string {
	registry.RLock()
	defer registry.RUnlock()
	names := make([]string, 0, len(registry.engines))
	for scheme := range registry.engines {
		names = append(names, scheme)
	}
	sort.Strings(names)
	return names
}

// shipped names the package that registers each engine the framework ships, so
// an engine that exists but was not linked reports the import to add instead of
// looking like an unknown database.
var shipped = map[string]string{
	"sqlite":     "github.com/shibukawa/popcornweb/database/sqlite",
	"sqlite3":    "github.com/shibukawa/popcornweb/database/sqlite",
	"postgres":   "github.com/shibukawa/popcornweb/database/postgres",
	"postgresql": "github.com/shibukawa/popcornweb/database/postgres",
	"mysql":      "github.com/shibukawa/popcornweb/database/mysql",
	"d1":         "github.com/shibukawa/popcornweb/database/d1",
}

func remedy(scheme string) string {
	if path, known := shipped[scheme]; known {
		return fmt.Sprintf("whose engine is not linked into this binary; add\n\timport _ %q", path)
	}
	registered := Schemes()
	if len(registered) == 0 {
		return "but no database engine is linked into this binary"
	}
	return fmt.Sprintf("which no linked engine serves; this binary opens %s",
		strings.Join(registered, ", "))
}
