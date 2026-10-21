package pwtestbridge

import (
	"database/sql"
	"net/http"
	"reflect"

	"github.com/shibukawa/popcornweb/pwruntime"
)

type Configs map[reflect.Type]any

// Options selects optional test runtime behavior.
type Options struct {
	// Transaction wraps every request of the prepared handler in one shared
	// transaction scope that the caller begins and rolls back.
	Transaction bool
	// Database is borrowed from the caller and never closed by the test runtime.
	Database *pwruntime.Connection
	// PrepareDatabase runs against the opened pool before the runtime handler
	// is built. Extensions verify their tables while that handler is
	// assembled, so a test that installs a schema has to do it here rather
	// than after Prepare returns.
	PrepareDatabase func(*sql.DB) error
}

type Prepared struct {
	Handler http.Handler
	DB      *sql.DB
	Driver  string
	// TxScope is non-nil when Options.Transaction was requested and the
	// configured runtime has a database.
	TxScope *pwruntime.TransactionScope
	// Resources is the same runtime state the prepared handler installs, so
	// tests can build a context equivalent to a request context.
	Resources pwruntime.Resources
	Close     func() error
}
