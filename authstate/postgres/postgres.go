// Package postgres registers the PostgreSQL dialect of the authentication
// state store.
//
//	import _ "github.com/shibukawa/popcornweb/authstate/postgres"
//
// The database connection itself comes from popcornweb/database/postgres,
// which an application imports separately.
package postgres

import (
	"context"

	"github.com/shibukawa/popcornweb/authstate"
	"github.com/shibukawa/tinybind-go/sqlbind"
)

// Dialect is the registered engine name, which is also what a postgres:// or
// postgresql:// DSN resolves to.
const Dialect = "postgres"

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
	expires_at_ms BIGINT NOT NULL,
	payload BYTEA NOT NULL,
	PRIMARY KEY (namespace, "key")
)`
}

// insert refuses to overwrite a record that has not expired, which is what
// makes one ceremony key usable once.
func insert(ctx context.Context, db sqlbind.SQLExecutor, record authstate.SQLRecord) (bool, error) {
	result, err := db.ExecContext(ctx, `
		INSERT INTO `+authstate.TableName+`(namespace, "key", expires_at_ms, payload)
		VALUES($1, $2, $3, $4)
		ON CONFLICT(namespace, "key") DO UPDATE SET
			expires_at_ms = excluded.expires_at_ms,
			payload = excluded.payload
		WHERE `+authstate.TableName+`.expires_at_ms <= $5`,
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

// take consumes the record in one statement, which PostgreSQL supports through
// RETURNING.
func take(ctx context.Context, db sqlbind.SQLExecutor, namespace, key string) (int64, []byte, error) {
	rows, err := sqlbind.Query(ctx, db, `
		DELETE FROM `+authstate.TableName+`
		WHERE namespace = $1 AND "key" = $2
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
		WHERE namespace = $1 AND "key" IN (
			SELECT "key" FROM `+authstate.TableName+`
			WHERE namespace = $2 AND expires_at_ms <= $3
			ORDER BY expires_at_ms, "key" LIMIT $4
		) AND expires_at_ms <= $5`,
		namespace, namespace, beforeMS, limit, beforeMS)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func columns(ctx context.Context, db sqlbind.SQLExecutor) ([]string, error) {
	rows, err := sqlbind.Query(ctx, db, `
		SELECT column_name FROM information_schema.columns
		WHERE table_name = $1 AND table_schema = current_schema()
		ORDER BY ordinal_position`, authstate.TableName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return authstate.ScanColumns(rows)
}
