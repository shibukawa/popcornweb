// Package pwdatabase opens the database pools a configuration named, and owns
// them for the life of the process.
//
// It is the second of the four layers requirement:alternate-http-backend-readiness
// names, after pwconfig: a connection pool is not a transport concern, and a
// build serving on either transport has to be able to open the databases its
// settings describe without linking a runtime it does not serve with.
//
// The split against pwconfig is which question is being answered. pwconfig
// decides what the file asked for — which groups exist, which one receives a
// migration, which DSN each names — and does it without opening anything. This
// opens them, pings them, and closes them again.
//
// Nothing here imports a transport.
package pwdatabase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/shibukawa/popcornweb/database"
	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwruntime"
)

// state holds the configured or caller-supplied pool set. It is process-wide because a pool
// is, and it is replaced rather than assumed written once, because framework
// initialization runs more than once in one process — most obviously in tests.
var state struct {
	sync.RWMutex
	connections *pwruntime.ConnectionSet
	owned       bool
	// db and driver mirror the default group's connection for callers that
	// predate the connection set, such as the readiness probe.
	db     *sql.DB
	driver string
}

// Start opens every configured pool, once.
//
// A second call with pools already open closes nothing and opens nothing: the
// process has one set, and framework initialization running twice must not
// leave the first one orphaned. A disabled configuration opens nothing and
// reports no error, because a project with no database is not a misconfigured
// one.
func Start(config pwconfig.RDBConfig) error {
	if !config.Enabled {
		return nil
	}
	state.RLock()
	alreadyOpen := state.connections != nil
	state.RUnlock()
	if alreadyOpen {
		return nil
	}
	set, err := Open(config)
	if err != nil {
		return err
	}
	state.Lock()
	defer state.Unlock()
	if state.connections != nil {
		// Another caller won the race. Closing what this one opened is the only
		// way the loser leaves no pool behind.
		_ = set.Close()
		return nil
	}
	state.connections = set
	state.owned = true
	if primary, ok := defaultConnection(set); ok {
		state.db = primary.DB
		state.driver = primary.Driver
	}
	return nil
}

// Close releases pools opened by Start, or forgets caller-owned installed pools.
func Close() error {
	state.Lock()
	set, owned := state.connections, state.owned
	state.connections, state.db, state.driver, state.owned = nil, nil, "", false
	state.Unlock()
	if set == nil || !owned {
		return nil
	}
	return set.Close()
}

// Install replaces the process connection set with caller-owned pools. It closes
// a previous set opened by Start, but never a set installed by the caller.
// Close forgets an installed set without closing its pools. Application startup
// and replacement must be serialized; handlers already using a set retain it.
func Install(set *pwruntime.ConnectionSet) error {
	if set == nil || set.Count() == 0 {
		return errors.New("popcornweb: cannot install an empty connection set")
	}
	state.Lock()
	previous, owned := state.connections, state.owned
	state.connections, state.owned = set, false
	state.db, state.driver = nil, ""
	if primary, ok := defaultConnection(set); ok {
		state.db, state.driver = primary.DB, primary.Driver
	}
	state.Unlock()
	if previous != nil && previous != set && owned {
		return previous.Close()
	}
	return nil
}

// Connections returns the open pool set, or nil when none was opened.
func Connections() *pwruntime.ConnectionSet {
	state.RLock()
	defer state.RUnlock()
	return state.connections
}

// Default returns the default group's handle and driver, which is what a caller
// predating the connection set reads.
//
// The handle is nil for an engine that bypasses database/sql, which is the same
// answer pwruntime.DB gives and for the same reason: there is no *sql.DB to
// hand back, and inventing one would be a handle that fails on first use.
func Default() (*sql.DB, string) {
	state.RLock()
	defer state.RUnlock()
	return state.db, state.driver
}

func defaultConnection(set *pwruntime.ConnectionSet) (*pwruntime.Connection, bool) {
	for _, connection := range set.Connections() {
		if connection.Group == set.DefaultGroup() {
			return connection, true
		}
	}
	return nil, false
}

// Open opens every configured pool and pings it. A failure closes what was
// already opened, so a partial set never reaches a request.
func Open(config pwconfig.RDBConfig) (*pwruntime.ConnectionSet, error) {
	connections, err := pwconfig.ResolveConnections(config)
	if err != nil {
		return nil, err
	}
	defaultGroup, err := pwconfig.ResolveDefaultGroup(config, connections)
	if err != nil {
		return nil, err
	}
	opened := make([]pwruntime.Connection, 0, len(connections))
	closeOpened := func() {
		for _, connection := range opened {
			_ = connection.Close()
		}
	}
	ordinals := make(map[string]int, len(connections))
	for _, connection := range connections {
		ordinals[connection.Group]++
		label := connection.Group + "#" + strconv.Itoa(ordinals[connection.Group])
		runtimeConnection, openErr := openConnection(connection, label)
		if openErr != nil {
			closeOpened()
			return nil, openErr
		}
		opened = append(opened, runtimeConnection)
	}
	set, err := pwruntime.NewConnectionSet(defaultGroup, opened)
	if err != nil {
		closeOpened()
		return nil, err
	}
	return set, nil
}

// OpenOne opens a single configured pool under a label, which is what a test
// bridge needs: it runs against one connection rather than the whole set, and
// it has to open that one the same way a request pool is opened.
func OpenOne(config pwconfig.RDBConnectionConfig, label string) (pwruntime.Connection, error) {
	return openConnection(config, label)
}

func openConnection(config pwconfig.RDBConnectionConfig, label string) (pwruntime.Connection, error) {
	connection := pwruntime.Connection{
		Group:    config.Group,
		Label:    label,
		ReadOnly: config.ReadOnly,
	}
	target, err := pwconfig.Target(config.DSN)
	if err != nil {
		return connection, fmt.Errorf("popcornweb: connection %s: %w", label, err)
	}
	connection.Driver = target.Dialect
	connection.DataSource = config.DSN
	ctx, cancel := context.WithTimeout(context.Background(), config.ConnectTimeout)
	defer cancel()
	if target.Native() {
		// The bounds travel with the open call, because a native pool is
		// configured at construction rather than adjusted afterwards.
		native, err := target.OpenNative(ctx, database.PoolBounds{
			MaxOpenConns:    config.MaxOpenConns,
			MaxIdleConns:    config.MaxIdleConns,
			ConnMaxLifetime: config.ConnMaxLifetime,
			ConnMaxIdleTime: config.ConnMaxIdleTime,
		})
		if err != nil {
			return connection, fmt.Errorf("popcornweb: open database %s: %w", label, err)
		}
		if err := native.Ping(ctx); err != nil {
			_ = native.Close()
			return connection, fmt.Errorf("popcornweb: connect database %s: %w", label, err)
		}
		connection.Native = native
		return connection, nil
	}
	db, err := target.Open()
	if err != nil {
		return connection, fmt.Errorf("popcornweb: open database %s: %w", label, err)
	}
	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)
	db.SetConnMaxIdleTime(config.ConnMaxIdleTime)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return connection, fmt.Errorf("popcornweb: connect database %s: %w", label, err)
	}
	connection.DB = db
	return connection, nil
}

// SelectWriteDB pins the connection group that framework-owned writes use:
// middleware.rdb.write_group, or the only group holding a writable connection.
//
// A replica can never be selected this way, so a caller that must write does
// not have to know the deployment topology.
func SelectWriteDB(ctx context.Context) (context.Context, error) {
	group, err := writableGroupFor(ctx, "", "middleware.rdb.write_group")
	if err != nil {
		return ctx, err
	}
	return pwruntime.SelectDB(ctx, group), nil
}

// SelectSessionDB pins the connection group holding the session table:
// session.rdb.group, falling back to the framework write group.
func SelectSessionDB(ctx context.Context) (context.Context, error) {
	group, err := writableGroupFor(ctx,
		pwruntime.ResolveConfig[pwconfig.SessionConfig](ctx).RDB.Group, "session.rdb.group")
	if err != nil {
		return ctx, err
	}
	return pwruntime.SelectDB(ctx, group), nil
}

// writableGroupMemo caches group resolutions against the configuration they
// were resolved from. The answer cannot change unless the configuration does,
// and the configuration itself is the dirty flag: every call compares what
// this request resolves against the cached copy, which also covers a
// per-request override and a test's Swap without any registry hook. groups is
// keyed by configured plus key — the two call sites ask two questions — and
// the memo is replaced whole, never mutated, so readers take it lock-free.
type writableGroupMemo struct {
	rdb    pwconfig.RDBConfig
	groups map[string]string
}

var writableGroupState atomic.Pointer[writableGroupMemo]

func writableGroupFor(ctx context.Context, configured, key string) (string, error) {
	config := pwruntime.ResolveConfig[pwconfig.MiddlewareConfig](ctx).RDB
	if !config.Enabled {
		return "", errors.New("popcornweb: middleware.rdb.enabled is false")
	}
	trimmed := strings.TrimSpace(configured)
	lookup := trimmed + "\x00" + key
	memo := writableGroupState.Load()
	if memo != nil && sameRDBConfig(memo.rdb, config) {
		if group, ok := memo.groups[lookup]; ok {
			return group, nil
		}
	} else {
		memo = nil
	}
	connections, err := pwconfig.ResolveConnections(config)
	if err != nil {
		return "", fmt.Errorf("popcornweb: %w", err)
	}
	group, err := pwconfig.ResolveWritableGroup(config, connections, trimmed, key)
	if err != nil {
		return "", fmt.Errorf("popcornweb: %w", err)
	}
	next := &writableGroupMemo{rdb: config, groups: map[string]string{lookup: group}}
	// The connection list is cloned because the registered configuration
	// shares its backing array with every copy handed out: an element edited
	// in place would otherwise change the cached copy too, and read as
	// "unchanged".
	next.rdb.Connections = slices.Clone(config.Connections)
	if memo != nil {
		for held, answer := range memo.groups {
			if held != lookup {
				next.groups[held] = answer
			}
		}
	}
	writableGroupState.Store(next)
	return group, nil
}

// sameRDBConfig reports whether two resolved configurations would answer the
// group question identically. Every field is comparable, so this is direct
// comparison rather than a fingerprint.
func sameRDBConfig(a, b pwconfig.RDBConfig) bool {
	return a.Enabled == b.Enabled &&
		a.DefaultGroup == b.DefaultGroup &&
		a.WriteGroup == b.WriteGroup &&
		a.MigrationGroup == b.MigrationGroup &&
		slices.Equal(a.Connections, b.Connections)
}
