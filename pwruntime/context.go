// Package pwruntime contains the narrow runtime contract used by generated
// Popcorn Web code. Handwritten applications should normally import pw.
package pwruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"sync"

	"github.com/shibukawa/tinybind-go/sqlbind"
)

type contextKey struct{}

// Resources is the immutable process/request state installed by pw.
type Resources struct {
	Configs map[reflect.Type]any
	// Log is the process emission policy. A nil backend falls back to a plain
	// stderr text handler so that a request served outside pw still logs.
	Log *LogBackend
	// LogAttributes are the stable request attributes every record carries,
	// such as the request correlation ID.
	LogAttributes []Attribute
	// DB is the pool of the default group. It stays the whole database for a
	// configuration that declares no connection set.
	DB *sql.DB
	// DBDriver is the driver scheme of DSN, used to decide savepoint support.
	DBDriver string
	// Connections is the configured connection set. A nil value means the
	// single DB above is the only database.
	Connections *ConnectionSet
	// Group is the group pinned by SelectDB. Empty selects the default.
	Group string
	// picked memoizes the round-robin choice per group for one request.
	picked *connectionMemo
	// single is the sole connection of a configuration without a connection
	// set, built once in WithResources so that statement resolution does not
	// allocate one per call.
	single *Connection
	// instrumented caches the latest statement wrapper, shared down the
	// capsule chain; instrument revalidates every input before a hit.
	instrumented *instrumentCache
	// TxScope is the active transaction scope, installed by the framework only.
	TxScope *TransactionScope
	// Query enables development query diagnostics. A nil value leaves the
	// resolved executor undecorated.
	Query *QueryDiagnostics
	// Trace enables the framework's own spans. A nil value creates none.
	Trace *Tracing
	// Metrics is the framework instrument set. A nil value records nothing, and
	// it is independent of Trace: a deployment sampling one trace in ten still
	// counts every request.
	Metrics *Metrics
	// Authentication is the verified request authentication result, finalized
	// by authentication middleware before handler dispatch.
	Authentication Authentication
	// parent is the capsule this one was derived from, nil for the capsule
	// installed at request start. One context lookup reaches the innermost
	// capsule; its ancestors are a pointer chase from here, never a second
	// walk of the context chain.
	parent *Resources
}

// Parent returns the capsule this one was derived from, or nil for the
// request root capsule.
func (r *Resources) Parent() *Resources {
	if r == nil {
		return nil
	}
	return r.parent
}

// A ValueStore is a request value that carries its own state instead of being
// replaced by a derived copy for each frame of the chain.
//
// net/http middleware derives a context and hands it to the next handler, so a
// frame changes what the rest of the chain sees by returning something new.
// fasthttp has one request value, which is itself the context, and a frame
// changes what the chain sees by writing into it. Only the write side differs:
// that value answers Value from the same store, so every reader in this package
// works on both transports unchanged.
//
// It is declared structurally rather than by naming the type, so this leaf
// stays free of the fasthttp fork the way it stays free of anything else a
// net/http project should not have to build.
type ValueStore interface {
	context.Context
	SetUserValue(key, value any)
}

// StoreResources writes the request resources into a value store, which is
// WithResources for a transport that cannot derive.
func StoreResources(store ValueStore, resources Resources) {
	store.SetUserValue(contextKey{}, prepareResources(resources))
}

// PreparedResources is one injector frame's capsule, prepared at chain
// construction rather than per request.
//
// The injectors hand every request the same Resources value, and prepareResources
// copied it to the heap once per request to do so. Most deployments' capsule is
// request-independent — the per-request fields prepare would add are a
// connection set's round-robin memo and the statement-wrapper cache, and a
// capsule without either is frozen after preparation, with every later change
// travelling through a derived copy. Such a capsule is prepared here once and
// shared by every request; one that does need per-request state keeps being
// prepared per request, unchanged.
type PreparedResources struct {
	shared *Resources
	source Resources
}

// PrepareResources readies resources for an injector frame.
func PrepareResources(resources Resources) PreparedResources {
	prepared := prepareResources(resources)
	if prepared.picked == nil && prepared.instrumented == nil {
		return PreparedResources{shared: prepared}
	}
	return PreparedResources{source: resources}
}

// Attach returns ctx carrying the capsule, the injector's WithResources.
func (p PreparedResources) Attach(ctx context.Context) context.Context {
	if p.shared != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		return context.WithValue(ctx, contextKey{}, p.shared)
	}
	return WithResources(ctx, p.source)
}

// StoreOn records the capsule on a request value, the injector's StoreResources.
func (p PreparedResources) StoreOn(store ValueStore) {
	if p.shared != nil {
		store.SetUserValue(contextKey{}, p.shared)
		return
	}
	StoreResources(store, p.source)
}

// StoreLogAttributes is WithLogAttributes for a value store.
func StoreLogAttributes(store ValueStore, attributes ...Attribute) {
	if len(attributes) == 0 {
		return
	}
	current := derive(store)
	current.LogAttributes = mergeAttributes(current.LogAttributes, attributes)
	StoreResources(store, current)
}

// DeriveResources copies the capsule a request carries, so a caller can change
// one field and store it back.
func DeriveResources(ctx context.Context) Resources { return derive(ctx) }

func WithResources(ctx context.Context, resources Resources) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey{}, prepareResources(resources))
}

// prepareResources fills the per-request caches a capsule needs before anything
// reads it. Both ways of installing one go through it, so a request served by
// either transport carries the same thing.
func prepareResources(resources Resources) *Resources {
	if resources.Log == nil {
		resources.Log = fallbackBackend()
	}
	// The memo is per request, not per process. InjectResources hands every
	// request the same Resources value, whose memo is nil, so each request
	// builds its own here and every child context inherits that one pointer.
	if resources.picked == nil && resources.Connections != nil {
		resources.picked = newConnectionMemo()
	}
	// The group and label stay empty: without a connection set there is no
	// second group to compare against and no second label to log, so nothing
	// observes them.
	if resources.single == nil && resources.Connections == nil && resources.DB != nil {
		resources.single = &Connection{DB: resources.DB, Driver: resources.DBDriver}
	}
	if resources.instrumented == nil && (resources.Query != nil || (resources.Trace != nil && resources.Trace.Database)) {
		resources.instrumented = &instrumentCache{}
	}
	return &resources
}

// SelectDB pins group onto ctx so generated SQL and Transaction use it instead
// of the default group.
//
// An unknown name is not rejected here. The returned context fails at its first
// executor resolution, DB call, or Transaction with ErrUnknownConnectionGroup.
func SelectDB(ctx context.Context, group string) context.Context {
	current := derive(ctx)
	current.Group = group
	return WithResources(ctx, current)
}

// derive copies the capsule of ctx and records the copied capsule as the
// parent of the copy, so every derived capsule can walk its ancestry without
// touching the context again.
func derive(ctx context.Context) Resources {
	current := resources(ctx)
	child := *current
	child.parent = current
	return child
}

// effectiveGroup is the group a statement on ctx runs against.
//
// The active transaction outranks the default group, so unpinned SQL inside a
// writer transaction stays on the writer instead of leaking to a replica.
func (r *Resources) effectiveGroup() string {
	if r.Group != "" {
		return r.Group
	}
	if r.TxScope.Active() {
		return r.TxScope.Group()
	}
	return r.Connections.DefaultGroup()
}

// connection resolves the pool backing the effective group.
func (r *Resources) connection() (*Connection, error) {
	if r.Connections == nil {
		if r.single != nil {
			return r.single, nil
		}
		if r.DB == nil {
			return nil, errors.New("popcornweb: database is not available in context")
		}
		// A capsule that never passed WithResources, which only a test can
		// assemble; the request path always has the memoized connection above.
		return &Connection{DB: r.DB, Driver: r.DBDriver}, nil
	}
	return r.picked.resolve(r.Connections, r.effectiveGroup())
}

func resources(ctx context.Context) *Resources {
	if ctx != nil {
		if value, ok := ctx.Value(contextKey{}).(*Resources); ok && value != nil {
			return value
		}
	}
	return &Resources{Log: fallbackBackend()}
}

// fallbackBackend serves a context that never passed through pw, which is what
// a unit test and an unconfigured tool both look like. Info to stderr is the
// least surprising thing to do there, and it is built once rather than per
// lookup because an accessor on the request path must not allocate a handler.
var fallbackBackend = sync.OnceValue(func() *LogBackend {
	return NewLogBackend(LevelInfo, NewSlogSink(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
})

func Config[T any](ctx context.Context) (T, bool) {
	var zero T
	value, ok := resources(ctx).Configs[reflect.TypeFor[T]()]
	if !ok {
		return zero, false
	}
	typed, ok := value.(T)
	return typed, ok
}

// ReadLogger returns a logger bound to the emission policy of ctx, its stable
// request attributes, and the span active on it. It never returns a logger that
// cannot be called: a context with nothing installed still yields a usable one.
//
// Acquire again inside a child span to correlate with that span, because the
// correlation is captured here rather than at each call.
func ReadLogger(ctx context.Context) Logger {
	current := resources(ctx)
	return NewLogger(ctx, current.Log, current.LogAttributes...)
}

// WithLogAttributes adds stable request attributes to every record taken from
// ctx afterwards, preserving the other runtime resources already installed.
func WithLogAttributes(ctx context.Context, attributes ...Attribute) context.Context {
	if len(attributes) == 0 {
		return ctx
	}
	current := derive(ctx)
	current.LogAttributes = mergeAttributes(current.LogAttributes, attributes)
	return WithResources(ctx, current)
}

// WithLogBackend replaces only the emission policy, which is what a test needs
// to capture records without rebuilding every other resource.
func WithLogBackend(ctx context.Context, backend *LogBackend) context.Context {
	current := derive(ctx)
	current.Log = backend
	return WithResources(ctx, current)
}

// DB returns the pool of the effective group, which is the group pinned by
// SelectDB, otherwise the group of an active transaction, otherwise the default
// group.
//
// A connection whose engine bypasses database/sql has no *sql.DB, so DB
// reports false for it rather than fabricating a handle; ConnectionExecutor
// is the surface that exists on every connection.
func DB(ctx context.Context) (*sql.DB, bool) {
	connection, err := resources(ctx).connection()
	if err != nil {
		return nil, false
	}
	return connection.DB, connection.DB != nil
}

// ConnectionExecutor returns the pool-level statement surface of the effective
// group, undecorated and outside any transaction. It exists for framework
// storage that holds one executor for the process lifetime, such as the rdb
// session backend; request statements resolve through SQLExecutor instead.
func ConnectionExecutor(ctx context.Context) (sqlbind.SQLExecutor, bool) {
	connection, err := resources(ctx).connection()
	if err != nil {
		return nil, false
	}
	executor := connection.Executor()
	return executor, executor != nil
}

// DBDriver reports the driver scheme of the effective connection, which
// dialect-specific storage needs before it issues SQL.
func DBDriver(ctx context.Context) (string, bool) {
	connection, err := resources(ctx).connection()
	if err != nil {
		return "", false
	}
	return connection.Driver, connection.Driver != ""
}

// SQLExecutor is used by generated .pw.sql context wrappers. It is the one
// seam every generated statement passes through, so query diagnostics attach
// here instead of in generated code or in a wrapping driver.
func SQLExecutor(ctx context.Context) (sqlbind.SQLExecutor, error) {
	current := resources(ctx)
	executor, connection, err := baseSQLExecutor(ctx, current)
	if err != nil {
		return nil, err
	}
	return instrument(current, connection, executor, ReadLogger(ctx)), nil
}

// baseSQLExecutor also returns the connection it resolved, when the chosen
// path resolved one, so instrument does not take the memo lock a second time
// for the same answer.
func baseSQLExecutor(ctx context.Context, current *Resources) (sqlbind.SQLExecutor, *Connection, error) {
	group := current.effectiveGroup()
	if current.TxScope.Active() {
		if current.Connections.Collapsed() || current.TxScope.Group() == group {
			return readOnlyExecutor(current.TxScope.executor(), current.TxScope.ReadOnly()), nil, nil
		}
		// SelectDB named another group inside a transaction. The context
		// executor installed by withScope belongs to that transaction, so it is
		// deliberately skipped: these statements run outside it.
		connection, err := current.connection()
		if err != nil {
			return nil, nil, err
		}
		if !connection.ReadOnly {
			return nil, nil, fmt.Errorf(
				"popcornweb: group %q is writable and cannot be selected inside a transaction on group %q",
				group, current.TxScope.Group())
		}
		return readOnlyExecutor(connection.Executor(), true), connection, nil
	}
	if executor, err := sqlbind.SQLExecutorFromContext(ctx); err == nil {
		return unwrapExecutor(executor), nil, nil
	}
	connection, err := current.connection()
	if err != nil {
		return nil, nil, err
	}
	return readOnlyExecutor(connection.Executor(), connection.ReadOnly), connection, nil
}

// activeScope returns the transaction scope holding an open transaction.
func activeScope(ctx context.Context) *TransactionScope {
	scope := resources(ctx).TxScope
	if scope.Active() {
		return scope
	}
	return nil
}

// withScope installs scope as the request transaction state. Generated SQL
// resolves the scope's executor from the capsule, so no second context node is
// installed beside it: the sqlbind executor key stays an input seam for an
// externally opened transaction, never an output of the framework.
func withScope(ctx context.Context, scope *TransactionScope) context.Context {
	current := derive(ctx)
	current.TxScope = scope
	return WithResources(ctx, current)
}
