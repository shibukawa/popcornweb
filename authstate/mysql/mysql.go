// Package mysql registers the MySQL dialect of the authentication state store.
//
//	import _ "github.com/shibukawa/popcornweb/authstate/mysql"
//
// The database connection itself comes from popcornweb/database/mysql, which
// an application imports separately.
package mysql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/shibukawa/popcornweb/authstate"
	"github.com/shibukawa/tinybind-go/sqlbind"
)

// Dialect is the registered engine name, which is also what a mysql:// DSN
// resolves to.
const Dialect = "mysql"

func init() {
	authstate.Register(authstate.Dialect{
		Name:        Dialect,
		CreateTable: createTable,
		Insert:      insert,
		Take:        take,
		Prune:       prune,
		Columns:     columns,
	})
}

// createTable gives both key columns an explicit length, because MySQL indexes
// no unbounded text, and keeps the pair short enough for one index.
func createTable() string {
	return `CREATE TABLE IF NOT EXISTS ` + authstate.TableName + ` (
	namespace VARCHAR(128) NOT NULL,
	` + "`key`" + ` VARCHAR(256) NOT NULL,
	expires_at_ms BIGINT NOT NULL,
	payload MEDIUMBLOB NOT NULL,
	PRIMARY KEY (namespace, ` + "`key`" + `)
)`
}

// transactor is the part of *sql.DB the two transactional operations need. A
// MySQL pool is always a database/sql one, so anything else reaching this
// dialect is a wiring mistake rather than an engine to support.
type transactor interface {
	BeginTx(ctx context.Context, options *sql.TxOptions) (*sql.Tx, error)
}

func begin(ctx context.Context, db sqlbind.SQLExecutor) (*sql.Tx, error) {
	pool, ok := db.(transactor)
	if !ok {
		return nil, errors.New("authstate/mysql: the executor opens no database/sql transaction")
	}
	return pool.BeginTx(ctx, nil)
}

// insert runs in a transaction, because MySQL puts no condition on an upsert:
// the expired row is removed first, and the insert that follows fails on a
// duplicate key exactly when a live record still holds it.
func insert(ctx context.Context, db sqlbind.SQLExecutor, record authstate.SQLRecord) (bool, error) {
	tx, err := begin(ctx, db)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM `+authstate.TableName+`
		WHERE namespace = ? AND `+"`key`"+` = ? AND expires_at_ms <= ?`,
		record.Namespace, record.Key, record.NowMS); err != nil {
		return false, err
	}
	var held int
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM `+authstate.TableName+`
		WHERE namespace = ? AND `+"`key`"+` = ? FOR UPDATE`,
		record.Namespace, record.Key).Scan(&held)
	if err != nil {
		return false, err
	}
	if held > 0 {
		return false, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO `+authstate.TableName+`(namespace, `+"`key`"+`, expires_at_ms, payload)
		VALUES(?, ?, ?, ?)`,
		record.Namespace, record.Key, record.ExpiresAtMS, record.Payload); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// take reads and deletes in one transaction, because MySQL has no RETURNING.
// The row is locked for the read, so two callers cannot both consume it.
func take(ctx context.Context, db sqlbind.SQLExecutor, namespace, key string) (int64, []byte, error) {
	tx, err := begin(ctx, db)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var expiresAtMS int64
	var payload []byte
	err = tx.QueryRowContext(ctx, `
		SELECT expires_at_ms, payload FROM `+authstate.TableName+`
		WHERE namespace = ? AND `+"`key`"+` = ? FOR UPDATE`, namespace, key).
		Scan(&expiresAtMS, &payload)
	if err != nil {
		// A missing row is sql.ErrNoRows here too, which is what the store
		// reports as ErrNotFound.
		return 0, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM `+authstate.TableName+`
		WHERE namespace = ? AND `+"`key`"+` = ?`, namespace, key); err != nil {
		return 0, nil, err
	}
	return expiresAtMS, payload, tx.Commit()
}

// prune deletes in place, because MySQL refuses a subquery that reads the
// table being deleted from but does accept ORDER BY and LIMIT here.
func prune(ctx context.Context, db sqlbind.SQLExecutor, namespace string, beforeMS int64, limit int) (int64, error) {
	result, err := db.ExecContext(ctx, `
		DELETE FROM `+authstate.TableName+`
		WHERE namespace = ? AND expires_at_ms <= ?
		ORDER BY expires_at_ms, `+"`key`"+` LIMIT ?`,
		namespace, beforeMS, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func columns(ctx context.Context, db sqlbind.SQLExecutor) ([]string, error) {
	rows, err := sqlbind.Query(ctx, db, `
		SELECT column_name FROM information_schema.columns
		WHERE table_name = ? AND table_schema = database()
		ORDER BY ordinal_position`, authstate.TableName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return authstate.ScanColumns(rows)
}
