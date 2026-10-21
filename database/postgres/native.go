package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/shibukawa/popcornweb/database"
	"github.com/shibukawa/tinybind-go/sqlbind"
	"github.com/shibukawa/tinygodriver/database/pgx"
	"github.com/shibukawa/tinygodriver/database/pgx/pgxpool"
)

// openNative builds the pgxpool-backed handle the request-time query path
// runs on. pgxpool.ParseConfig installs the CancelRequest cancellation
// watcher and, under TinyGo, the fd-carrying dialer that makes sslmode work,
// so this function only maps the framework pool bounds onto it.
func openNative(ctx context.Context, dsn string, bounds database.PoolBounds) (database.NativeDB, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if bounds.MaxOpenConns > 0 {
		config.MaxConns = int32(bounds.MaxOpenConns)
	}
	if bounds.ConnMaxLifetime > 0 {
		config.MaxConnLifetime = bounds.ConnMaxLifetime
	}
	if bounds.ConnMaxIdleTime > 0 {
		config.MaxConnIdleTime = bounds.ConnMaxIdleTime
	}
	// MaxIdleConns has no pgxpool equivalent: the pool keeps connections up to
	// MaxConns and prunes them by MaxConnIdleTime instead.
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	return &nativeDB{pool: pool}, nil
}

// nativeDB adapts a pgxpool.Pool to the framework's native handle. The
// embedded UnimplementedQuerier answers the QueryContext half of the executor
// contract, because only database/sql can construct a *sql.Rows; every real
// query dispatches through QueryRows.
type nativeDB struct {
	sqlbind.UnimplementedQuerier
	pool     *pgxpool.Pool
	borrowed bool
}

func (db *nativeDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	tag, err := db.pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return commandResult(tag), nil
}

func (db *nativeDB) QueryRows(ctx context.Context, query string, args ...any) (sqlbind.Rows, error) {
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return nativeRows{rows: rows}, nil
}

func (db *nativeDB) BeginTx(ctx context.Context, options database.NativeTxOptions) (database.NativeTx, error) {
	txOptions := pgx.TxOptions{}
	if options.ReadOnly {
		txOptions.AccessMode = pgx.ReadOnly
	}
	tx, err := db.pool.BeginTx(ctx, txOptions)
	if err != nil {
		return nil, err
	}
	return &nativeTx{tx: tx}, nil
}

func (db *nativeDB) Ping(ctx context.Context) error { return db.pool.Ping(ctx) }

func (db *nativeDB) Close() error {
	if !db.borrowed {
		db.pool.Close()
	}
	return nil
}

// nativeTx adapts a pgx.Tx. Rollback after Commit reports success, matching
// the sql.ErrTxDone tolerance callers already rely on for deferred rollbacks.
type nativeTx struct {
	sqlbind.UnimplementedQuerier
	tx pgx.Tx
}

func (tx *nativeTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	tag, err := tx.tx.Exec(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return commandResult(tag), nil
}

func (tx *nativeTx) QueryRows(ctx context.Context, query string, args ...any) (sqlbind.Rows, error) {
	rows, err := tx.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return nativeRows{rows: rows}, nil
}

func (tx *nativeTx) Commit(ctx context.Context) error { return tx.tx.Commit(ctx) }

func (tx *nativeTx) Rollback(ctx context.Context) error {
	if err := tx.tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return err
	}
	return nil
}

// commandResult presents a pgx CommandTag as a sql.Result.
type commandResult pgx.CommandTag

func (result commandResult) LastInsertId() (int64, error) {
	return 0, errors.New("popcornweb/database/postgres: LastInsertId is not supported by PostgreSQL; use RETURNING")
}

func (result commandResult) RowsAffected() (int64, error) {
	return pgx.CommandTag(result).RowsAffected(), nil
}

// nativeRows presents pgx.Rows as the sqlbind cursor. Close returns nil
// because pgx reports iteration failures only through Err, which generated
// code and the sqlbind scanners already check.
type nativeRows struct {
	rows pgx.Rows
}

func (rows nativeRows) Next() bool             { return rows.rows.Next() }
func (rows nativeRows) Scan(dest ...any) error { return rows.rows.Scan(dest...) }
func (rows nativeRows) Err() error             { return rows.rows.Err() }

func (rows nativeRows) Close() error {
	rows.rows.Close()
	return nil
}

func (rows nativeRows) Columns() ([]string, error) {
	descriptions := rows.rows.FieldDescriptions()
	names := make([]string, len(descriptions))
	for i, description := range descriptions {
		names[i] = description.Name
	}
	return names, nil
}
