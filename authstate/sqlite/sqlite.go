// Package sqlite registers the SQLite dialect of the authentication state
// store.
//
//	import _ "github.com/shibukawa/popcornweb/authstate/sqlite"
//
// It registers no database/sql driver: an engine package under
// popcornweb/database does that separately.
package sqlite

import (
	"context"

	"github.com/shibukawa/popcornweb/authstate"
	"github.com/shibukawa/tinybind-go/sqlbind"
)

// Dialect is the registered engine name, which is also what a sqlite:// DSN
// resolves to.
const Dialect = "sqlite"

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

func createTable() string {
	return `CREATE TABLE IF NOT EXISTS ` + authstate.TableName + ` (
	namespace TEXT NOT NULL,
	"key" TEXT NOT NULL,
	expires_at_ms INTEGER NOT NULL,
	payload BLOB NOT NULL,
	PRIMARY KEY (namespace, "key")
) WITHOUT ROWID`
}

// insert refuses to overwrite a record that has not expired, which is what
// makes one ceremony key usable once.
func insert(ctx context.Context, db sqlbind.SQLExecutor, record authstate.SQLRecord) (bool, error) {
	result, err := db.ExecContext(ctx, `
		INSERT INTO `+authstate.TableName+`(namespace, "key", expires_at_ms, payload)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(namespace, "key") DO UPDATE SET
			expires_at_ms = excluded.expires_at_ms,
			payload = excluded.payload
		WHERE `+authstate.TableName+`.expires_at_ms <= ?`,
		record.Namespace, record.Key, record.ExpiresAtMS, record.Payload, record.NowMS)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// take consumes the record in one statement, which SQLite supports through
// RETURNING.
func take(ctx context.Context, db sqlbind.SQLExecutor, namespace, key string) (int64, []byte, error) {
	rows, err := sqlbind.Query(ctx, db, `
		DELETE FROM `+authstate.TableName+`
		WHERE namespace = ? AND "key" = ?
		RETURNING expires_at_ms, payload`, namespace, key)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = rows.Close() }()
	return authstate.ScanRecord(rows)
}

func prune(ctx context.Context, db sqlbind.SQLExecutor, namespace string, beforeMS int64, limit int) (int64, error) {
	result, err := db.ExecContext(ctx, `
		DELETE FROM `+authstate.TableName+`
		WHERE namespace = ? AND "key" IN (
			SELECT "key" FROM `+authstate.TableName+`
			WHERE namespace = ? AND expires_at_ms <= ?
			ORDER BY expires_at_ms, "key" LIMIT ?
		) AND expires_at_ms <= ?`,
		namespace, namespace, beforeMS, limit, beforeMS)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func columns(ctx context.Context, db sqlbind.SQLExecutor) ([]string, error) {
	rows, err := sqlbind.Query(ctx, db, `PRAGMA table_info(`+authstate.TableName+`)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
