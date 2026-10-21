package pwruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/shibukawa/popcornweb/database"
	"github.com/shibukawa/tinybind-go/sqlbind"
)

// DefaultConnectionGroup is the group name of a configuration that declares a
// single database without naming any group.
const DefaultConnectionGroup = "default"

// ErrUnknownConnectionGroup reports a group name that no configured connection
// carries. A group name is data, so the failure surfaces at the statement that
// depended on it rather than where the name was written.
var ErrUnknownConnectionGroup = errors.New("popcornweb: unknown database connection group")

// Connection is one configured pool of the connection set. Exactly one of DB
// and Native is set: DB for an engine served through database/sql, Native for
// one whose request-time path bypasses it.
type Connection struct {
	DB *sql.DB
	// DataSource is the framework DSN used to open a separate tooling handle
	// for native connections. It may contain credentials; never log it.
	DataSource string
	// Native is the handle of an engine that bypasses database/sql. A caller
	// that needs a *sql.DB finds none on such a connection; statements run
	// through Executor and transactions through the scope.
	Native database.NativeDB
	// Driver is the resolved driver scheme, which decides savepoint support.
	Driver string
	// Group is the name this connection is addressed by.
	Group string
	// Label identifies this connection inside its group, as group#ordinal.
	Label string
	// ReadOnly marks a replica. It selects a read-only transaction and, once
	// the SQL runtime can classify statements, a read-only executor.
	ReadOnly bool
}

// Executor is the pool-level statement surface of this connection, which is
// what the executor seam stores when no transaction is active.
func (connection *Connection) Executor() sqlbind.SQLExecutor {
	if connection == nil {
		return nil
	}
	if connection.Native != nil {
		return connection.Native
	}
	if connection.DB == nil {
		return nil
	}
	return connection.DB
}

// Close releases the pool, whichever kind it is.
func (connection *Connection) Close() error {
	if connection.Native != nil {
		return connection.Native.Close()
	}
	return connection.DB.Close()
}

// TransactionScope prepares an inactive transaction scope over this
// connection's pool, carrying its group and read-only marking. Begin
// activates it. It exists for the test bridge, whose shared test transaction
// must run on whichever kind of pool the connection holds.
func (connection *Connection) TransactionScope() *TransactionScope {
	return newConnectionScope(connection)
}

// Ping verifies the pool can reach its database.
func (connection *Connection) Ping(ctx context.Context) error {
	if connection.Native != nil {
		return connection.Native.Ping(ctx)
	}
	return connection.DB.PingContext(ctx)
}

// ConnectionSet is the immutable set of connection groups owned by the
// framework. Selection inside a group is round robin.
type ConnectionSet struct {
	order        []string
	groups       map[string][]*Connection
	cursors      map[string]*atomic.Uint64
	defaultGroup string
	count        int
	// collapsed answers every group name with the sole connection. It is what
	// lets a single-database configuration, and a test, run code that selects a
	// replica group without a dedicated branch.
	collapsed bool
}

// NewConnectionSet groups connections by name in configuration order.
//
// defaultGroup may be empty only when exactly one group is present.
func NewConnectionSet(defaultGroup string, connections []Connection) (*ConnectionSet, error) {
	if len(connections) == 0 {
		return nil, errors.New("popcornweb: connection set needs at least one connection")
	}
	set := &ConnectionSet{
		groups:  make(map[string][]*Connection, len(connections)),
		cursors: make(map[string]*atomic.Uint64, len(connections)),
	}
	for index := range connections {
		entry := connections[index]
		if entry.Group == "" {
			entry.Group = DefaultConnectionGroup
		}
		if entry.DB == nil && entry.Native == nil {
			return nil, fmt.Errorf("popcornweb: connection group %q has a nil pool", entry.Group)
		}
		if entry.DB != nil && entry.Native != nil {
			return nil, fmt.Errorf("popcornweb: connection group %q has both a sql and a native pool", entry.Group)
		}
		if _, seen := set.groups[entry.Group]; !seen {
			set.order = append(set.order, entry.Group)
			set.cursors[entry.Group] = new(atomic.Uint64)
		}
		if entry.Label == "" {
			entry.Label = entry.Group + "#" + strconv.Itoa(len(set.groups[entry.Group])+1)
		}
		set.groups[entry.Group] = append(set.groups[entry.Group], &entry)
	}
	if defaultGroup == "" {
		if len(set.order) != 1 {
			return nil, errors.New("popcornweb: default_group is required when more than one connection group is configured")
		}
		defaultGroup = set.order[0]
	}
	if _, ok := set.groups[defaultGroup]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownConnectionGroup, defaultGroup)
	}
	set.defaultGroup = defaultGroup
	set.count = len(connections)
	set.collapsed = len(connections) == 1
	return set, nil
}

// DefaultGroup is the group serving statements that pin no group of their own.
func (set *ConnectionSet) DefaultGroup() string {
	if set == nil {
		return ""
	}
	return set.defaultGroup
}

// Groups lists the configured group names in configuration order.
func (set *ConnectionSet) Groups() []string {
	if set == nil {
		return nil
	}
	return append([]string(nil), set.order...)
}

// Connections lists every connection in configuration order, which is what the
// startup summary and shutdown need.
func (set *ConnectionSet) Connections() []*Connection {
	if set == nil {
		return nil
	}
	all := make([]*Connection, 0, len(set.order))
	for _, group := range set.order {
		all = append(all, set.groups[group]...)
	}
	return all
}

// Count reports how many connections are configured. It exists for callers
// that need only the number, which Connections would answer with a fresh
// slice per call.
func (set *ConnectionSet) Count() int {
	if set == nil {
		return 0
	}
	return set.count
}

// Has reports whether group is configured. A collapsed set answers for every
// name, because it has only one database to answer with.
func (set *ConnectionSet) Has(group string) bool {
	if set == nil {
		return false
	}
	if set.collapsed {
		return true
	}
	_, ok := set.groups[group]
	return ok
}

// Collapsed reports whether every group name resolves to one database.
//
// A single-database configuration and a test both collapse, so application code
// that selects a replica group runs unchanged against one development sqlite
// file and against a reader-writer cluster.
func (set *ConnectionSet) Collapsed() bool {
	return set == nil || set.collapsed
}

// pick advances the round-robin cursor of group and returns the next
// connection. Callers memoize the result per request through Resources, so the
// cursor moves once per group per context chain.
func (set *ConnectionSet) pick(group string) (*Connection, error) {
	if set == nil {
		return nil, ErrUnknownConnectionGroup
	}
	if group == "" {
		group = set.defaultGroup
	}
	members, ok := set.groups[group]
	if !ok {
		if !set.collapsed {
			return nil, fmt.Errorf("%w: %s", ErrUnknownConnectionGroup, group)
		}
		members = set.groups[set.order[0]]
	}
	if len(members) == 1 {
		return members[0], nil
	}
	// Subtracting one makes the first pick index 0, so a two-member group
	// alternates from its first connection rather than its second.
	next := set.cursors[group].Add(1) - 1
	return members[next%uint64(len(members))], nil
}

// Close closes every pool and joins the failures.
func (set *ConnectionSet) Close() error {
	if set == nil {
		return nil
	}
	var errs []error
	for _, connection := range set.Connections() {
		if err := connection.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// connectionMemo pins one connection per group for the life of a request, so
// two reads of one group never straddle two replicas with different lag.
type connectionMemo struct {
	mu     sync.Mutex
	picked map[string]*Connection
}

func newConnectionMemo() *connectionMemo {
	return &connectionMemo{picked: make(map[string]*Connection, 2)}
}

func (memo *connectionMemo) resolve(set *ConnectionSet, group string) (*Connection, error) {
	if memo == nil {
		return set.pick(group)
	}
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if connection, ok := memo.picked[group]; ok {
		return connection, nil
	}
	connection, err := set.pick(group)
	if err != nil {
		return nil, err
	}
	memo.picked[group] = connection
	return connection, nil
}

// readOnlyExecutor is the seam for read-only statement rejection.
//
// The SQL runtime owns statement classification, so enforcement belongs there
// rather than in SQL inspection here. sqlbind has the mechanism —
// sqlbind.AsReadOnly stored on the context, checked by the write resolver — but
// it emits that check only when no framework executor resolver is configured:
// the resolver contract is func(context.Context) (SQLExecutor, error), which
// carries no statement access mode. Popcorn Web configures that resolver for
// transaction scope and query diagnostics, so generated writes never reach the
// check.
//
// Until the contract carries the access mode, a read-only connection relies on
// the read-only transaction opened at depth 0 and on the database itself.
// Wiring the option is a one-place change here once it can take effect.
func readOnlyExecutor(executor sqlbind.SQLExecutor, readOnly bool) sqlbind.SQLExecutor {
	_ = readOnly
	return executor
}
