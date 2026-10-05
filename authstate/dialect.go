package authstate

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"sync"

	"github.com/shibukawa/tinybind-go/sqlbind"
)

// TableName is the table the SQL stores own.
const TableName = "popcornweb_authstate"

// Columns are the columns of that table, in the order every dialect declares
// them.
var Columns = []string{"namespace", "key", "expires_at_ms", "payload"}

// Dialect is one engine's implementation of the four operations a SQL-backed
// store performs. These are whole operations rather than statement fragments
// because the engines differ in more than syntax: MySQL has no RETURNING, so
// its single-use read is a transaction where the others are one statement.
//
// The executor is a *sql.DB or the native pool of an engine that bypasses
// database/sql, so an engine reads through sqlbind.Query rather than through
// QueryRowContext, which only database/sql can answer.
type Dialect struct {
	// Name is the dialect identifier rule:rdb-dsn-resolution resolves a DSN
	// scheme to.
	Name string
	// CreateTable is the deterministic DDL of the owned table.
	CreateTable func() string
	// Insert stores one record unless a live one already holds the key, and
	// reports whether it stored.
	Insert func(ctx context.Context, db sqlbind.SQLExecutor, record SQLRecord) (bool, error)
	// Take removes one record and returns what it held. A missing record is
	// sql.ErrNoRows, which the store reports as ErrNotFound.
	Take func(ctx context.Context, db sqlbind.SQLExecutor, namespace, key string) (expiresAtMS int64, payload []byte, err error)
	// Prune removes at most limit records of one namespace that expired
	// before the given instant.
	Prune func(ctx context.Context, db sqlbind.SQLExecutor, namespace string, beforeMS int64, limit int) (int64, error)
	// Columns lists the columns of the owned table in declaration order, or
	// none at all when the table does not exist.
	Columns func(ctx context.Context, db sqlbind.SQLExecutor) ([]string, error)
}

// SQLRecord is one row an Insert writes. NowMS decides whether an existing row
// is stale enough to replace: a live record is never overwritten, which is what
// makes a ceremony key single use.
type SQLRecord struct {
	Namespace   string
	Key         string
	ExpiresAtMS int64
	NowMS       int64
	Payload     []byte
}

var dialects struct {
	sync.RWMutex
	byName map[string]Dialect
}

// Register adds an engine's dialect. An engine package calls it from init, so
// a blank import is what makes the SQL store work against that engine:
//
//	import _ "github.com/shibukawa/popcornweb/authstate/postgres"
//
// A duplicate or incomplete dialect panics: two descriptions of one engine is
// a build mistake, not a runtime condition.
func Register(dialect Dialect) {
	if dialect.Name == "" || dialect.CreateTable == nil || dialect.Insert == nil ||
		dialect.Take == nil || dialect.Prune == nil || dialect.Columns == nil {
		panic("authstate: incomplete dialect")
	}
	dialects.Lock()
	defer dialects.Unlock()
	if dialects.byName == nil {
		dialects.byName = make(map[string]Dialect)
	}
	if _, taken := dialects.byName[dialect.Name]; taken {
		panic(fmt.Sprintf("authstate: dialect %q is already registered", dialect.Name))
	}
	dialects.byName[dialect.Name] = dialect
}

// Dialects lists the registered dialect names in order.
func Dialects() []string {
	dialects.RLock()
	defer dialects.RUnlock()
	names := make([]string, 0, len(dialects.byName))
	for name := range dialects.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DialectFor resolves one engine, explaining the missing import rather than
// the missing map entry.
func DialectFor(name string) (Dialect, error) {
	dialects.RLock()
	dialect, ok := dialects.byName[name]
	dialects.RUnlock()
	if ok {
		return dialect, nil
	}
	if name == "" {
		return Dialect{}, fmt.Errorf("%w: no database dialect", ErrInvalidOptions)
	}
	return Dialect{}, fmt.Errorf(
		"authentication state storage for %q needs its engine; add to the application: import _ %q",
		name, "github.com/shibukawa/popcornweb/authstate/"+name)
}

// SchemaSQL returns the deterministic DDL of the owned table under one engine,
// so a project can carry it in a migration instead of creating it at startup.
func SchemaSQL(dialect string) (string, error) {
	engine, err := DialectFor(dialect)
	if err != nil {
		return "", err
	}
	return engine.CreateTable(), nil
}

// ScanColumns reads one column name per row, which is the shape every engine's
// catalog query is written to return.
func ScanColumns(rows sqlbind.Rows) ([]string, error) {
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// ScanRecord reads the one row a Take statement returns, and reports
// sql.ErrNoRows when it returned none. It is the QueryRowContext an engine
// would otherwise call, written against the rows every executor can produce.
func ScanRecord(rows sqlbind.Rows) (expiresAtMS int64, payload []byte, err error) {
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, nil, err
		}
		return 0, nil, sql.ErrNoRows
	}
	if err := rows.Scan(&expiresAtMS, &payload); err != nil {
		return 0, nil, err
	}
	return expiresAtMS, payload, rows.Err()
}
