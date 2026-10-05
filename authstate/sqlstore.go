package authstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shibukawa/tinybind-go/sqlbind"
)

const (
	defaultMaxKeyBytes   = 256
	defaultMaxValueBytes = 64 << 10
	defaultMaxPruneBatch = 256
	hardMaxKeyBytes      = 4096
	hardMaxValueBytes    = 1 << 20
	hardMaxPruneBatch    = 4096
	maxNamespaceBytes    = 128
)

// SQLOptions controls record isolation, resource bounds, and expiry behavior,
// and names the engine the statements run against.
type SQLOptions struct {
	// Dialect is the registered engine name, which is the dialect the DSN
	// scheme already resolved to.
	Dialect       string
	Namespace     string
	Now           func() time.Time
	MaxKeyBytes   int
	MaxValueBytes int
	MaxPruneBatch int
}

// SQLStore persists expiring, single-use authentication state in a relational
// database. The engine is supplied by a registered Dialect, so this type
// carries no SQL of its own beyond what every engine shares.
type SQLStore[T any] struct {
	db            sqlbind.SQLExecutor
	dialect       Dialect
	codec         Codec[T]
	namespace     string
	now           func() time.Time
	maxKeyBytes   int
	maxValueBytes int
	maxPruneBatch int
}

// NewSQLStore constructs a store over db under the named engine. db is a
// *sql.DB or the native pool of an engine that bypasses database/sql. The
// caller retains ownership of db and must carry the migration, or call
// EnsureSchema, before serving requests.
func NewSQLStore[T any](db sqlbind.SQLExecutor, codec Codec[T], options SQLOptions) (*SQLStore[T], error) {
	if db == nil || codec == nil || !validNamespace(options.Namespace) ||
		options.MaxKeyBytes < 0 || options.MaxKeyBytes > hardMaxKeyBytes ||
		options.MaxValueBytes < 0 || options.MaxValueBytes > hardMaxValueBytes ||
		options.MaxPruneBatch < 0 || options.MaxPruneBatch > hardMaxPruneBatch {
		return nil, ErrInvalidOptions
	}
	maxKeyBytes := options.MaxKeyBytes
	if maxKeyBytes == 0 {
		maxKeyBytes = defaultMaxKeyBytes
	}
	maxValueBytes := options.MaxValueBytes
	if maxValueBytes == 0 {
		maxValueBytes = defaultMaxValueBytes
	}
	maxPruneBatch := options.MaxPruneBatch
	if maxPruneBatch == 0 {
		maxPruneBatch = defaultMaxPruneBatch
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	dialect, err := DialectFor(options.Dialect)
	if err != nil {
		return nil, err
	}
	return &SQLStore[T]{
		db: db, dialect: dialect, codec: codec, namespace: options.Namespace, now: now,
		maxKeyBytes: maxKeyBytes, maxValueBytes: maxValueBytes, maxPruneBatch: maxPruneBatch,
	}, nil
}

// EnsureSchema creates the owned table when it is missing and verifies its
// column layout, which is what a project carrying the migration relies on at
// startup.
func (s *SQLStore[T]) EnsureSchema(ctx context.Context) error {
	if !s.ready() || ctx == nil {
		return ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, s.dialect.CreateTable()); err != nil {
		return unavailable(ctx)
	}
	found, err := s.dialect.Columns(ctx, s.db)
	if err != nil {
		return unavailable(ctx)
	}
	if len(found) != len(Columns) {
		return ErrInvalidOptions
	}
	for index, name := range found {
		if !strings.EqualFold(name, Columns[index]) {
			return ErrInvalidOptions
		}
	}
	return nil
}

func (s *SQLStore[T]) Put(ctx context.Context, key string, value T, expiresAt time.Time) error {
	if !s.ready() || ctx == nil {
		return ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" || len(key) > s.maxKeyBytes {
		return ErrInvalidKey
	}
	now := s.now()
	if expiresAt.IsZero() || !expiresAt.After(now) || expiresAt.UnixMilli() <= 0 {
		return ErrInvalidExpiry
	}
	payload, err := s.codec.Encode(value)
	if err != nil {
		return fmt.Errorf("%w: encode", ErrCodec)
	}
	if len(payload) == 0 {
		return fmt.Errorf("%w: encode", ErrCodec)
	}
	if len(payload) > s.maxValueBytes {
		return ErrLimitExceeded
	}
	stored, err := s.dialect.Insert(ctx, s.db, SQLRecord{
		Namespace:   s.namespace,
		Key:         key,
		ExpiresAtMS: expiresAt.UnixMilli(),
		NowMS:       now.UnixMilli(),
		Payload:     payload,
	})
	if err != nil {
		return unavailable(ctx)
	}
	if !stored {
		return ErrAlreadyExists
	}
	return nil
}

func (s *SQLStore[T]) Take(ctx context.Context, key string) (T, error) {
	var zero T
	if !s.ready() || ctx == nil {
		return zero, ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if key == "" || len(key) > s.maxKeyBytes {
		return zero, ErrInvalidKey
	}
	expiresAtMS, payload, err := s.dialect.Take(ctx, s.db, s.namespace, key)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrNotFound
	}
	if err != nil {
		return zero, unavailable(ctx)
	}
	if expiresAtMS <= 0 || len(payload) > s.maxValueBytes {
		return zero, fmt.Errorf("%w: malformed record", ErrCodec)
	}
	if !time.UnixMilli(expiresAtMS).After(s.now()) {
		return zero, ErrExpired
	}
	value, err := s.codec.Decode(payload)
	if err != nil {
		return zero, fmt.Errorf("%w: decode", ErrCodec)
	}
	return value, nil
}

// Prune removes at most limit expired records from this Store's namespace.
func (s *SQLStore[T]) Prune(ctx context.Context, before time.Time, limit int) (int64, error) {
	if !s.ready() || ctx == nil || before.IsZero() || limit <= 0 || limit > s.maxPruneBatch {
		return 0, ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	affected, err := s.dialect.Prune(ctx, s.db, s.namespace, before.UnixMilli(), limit)
	if err != nil {
		return 0, unavailable(ctx)
	}
	return affected, nil
}

func (s *SQLStore[T]) ready() bool {
	return s != nil && s.db != nil && s.dialect.Name != "" && s.codec != nil && s.namespace != "" && s.now != nil &&
		s.maxKeyBytes > 0 && s.maxValueBytes > 0 && s.maxPruneBatch > 0
}

func validNamespace(value string) bool {
	if value == "" || len(value) > maxNamespaceBytes {
		return false
	}
	for i := range len(value) {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func unavailable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%w: database", ErrUnavailable)
}

var _ Store[string] = (*SQLStore[string])(nil)

// NewSQLRawStore constructs a SQL store over already encoded payloads, which is
// the form a storage backend supplies for a value type it cannot name.
func NewSQLRawStore(db sqlbind.SQLExecutor, options SQLOptions) (*SQLStore[[]byte], error) {
	return NewSQLStore[[]byte](db, RawCodec{}, options)
}
