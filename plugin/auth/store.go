package auth

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shibukawa/popcornweb/database"
	"github.com/shibukawa/tinybind-go/sqlbind"
)

var (
	// ErrUnknownCredential means no stored credential carries that ID.
	ErrUnknownCredential = errors.New("auth: unknown passkey credential")
	// ErrUnknownBootstrap means no issued bootstrap credential carries that
	// login ID, or the one that does is spent.
	ErrUnknownBootstrap = errors.New("auth: unknown bootstrap credential")
)

// Bootstrap credential purposes, from data:account-bootstrap-credential.
const (
	PurposeInitialPasskey  = "initial_passkey"
	PurposeRecoveryPasskey = "recovery_passkey"
)

// Credential is the stored passkey credential of one account.
//
// A passkey login resolves a credential ID before it knows an account, which is
// the opposite of the question AccountResolver answers, so credentials need a
// lookup of their own.
type Credential struct {
	CredentialID []byte
	AccountID    string
	UserHandle   []byte
	// PublicKey is the normalized COSE key. PublicKeyX and PublicKeyY are the
	// same key as curve points: the relying party verifies with the points and
	// cross-checks them against the COSE blob, so a corrupted row fails closed
	// instead of verifying against something else.
	PublicKey      []byte
	PublicKeyX     []byte
	PublicKeyY     []byte
	Algorithm      int
	SignCount      uint32
	BackupEligible bool
	BackupState    bool
	Transports     []string
	Label          string
	CreatedAt      time.Time
	LastUsedAt     time.Time
}

// CredentialStore persists passkey credentials.
//
// The framework never writes a credential outside a store call. An error fails
// the ceremony closed; it is never downgraded to a warning, because a counter
// that silently fails to persist is exactly what a cloned authenticator needs.
type CredentialStore interface {
	// Find returns the credential of an ID, or ErrUnknownCredential.
	Find(ctx context.Context, credentialID []byte) (Credential, error)
	// ListByAccount supplies excludeCredentials and allowCredentials.
	ListByAccount(ctx context.Context, accountID string) ([]Credential, error)
	// Save persists a new credential and runs within in the same transaction,
	// so a first enrollment can also activate the account and consume the
	// bootstrap credential as one unit. within may be nil.
	Save(ctx context.Context, credential Credential, within func(ctx context.Context) error) error
	// UpdateOnAssertion persists the accepted counter and backup state of a
	// completed assertion.
	//
	// An implementation moves the stored counter forward or changes nothing,
	// in one conditional statement rather than a read and a write, and
	// tolerates an incoming count of zero from an authenticator that keeps
	// none. It reports ErrUnknownCredential when nothing was updated, which
	// covers both a missing credential and a counter that did not advance.
	UpdateOnAssertion(ctx context.Context, credentialID []byte, signCount uint32, backupState bool, usedAt time.Time) error
	// Delete removes one credential of an account.
	Delete(ctx context.Context, accountID string, credentialID []byte) error
}

// FirstEnrollmentStore is implemented by a credential store that cannot make a
// first enrollment one unit of work.
//
// The three writes of a passkey-only registration are spending the bootstrap
// credential, persisting the credential, and activating the account. A store
// with transactions applies them together and needs only Save. A store without
// them receives the two callbacks separately, so it can fix an order whose
// every partial outcome is safe rather than being handed one opaque callback it
// cannot sequence.
//
// The framework prefers this method when the installed store offers it.
type FirstEnrollmentStore interface {
	// SaveFirstCredential applies the enrollment. spend consumes the bootstrap
	// credential that authorized it and activate promotes the provisional
	// account; either may be nil.
	SaveFirstCredential(ctx context.Context, credential Credential, spend, activate func(context.Context) error) error
}

// BootstrapCredential is an issued login ID and secret that opens exactly one
// passkey enrollment. It is not a reusable password.
type BootstrapCredential struct {
	LoginID           string
	AccountID         string
	SecretDigest      []byte
	Purpose           string
	IssuedAt          time.Time
	ExpiresAt         time.Time
	AttemptsRemaining int
	ConsumedAt        time.Time
}

// BootstrapStore persists issued bootstrap credentials.
type BootstrapStore interface {
	Issue(ctx context.Context, credential BootstrapCredential) error
	// Find returns an unconsumed credential, or ErrUnknownBootstrap. It reports
	// nothing about why a lookup failed, so a caller cannot enumerate accounts.
	Find(ctx context.Context, loginID string) (BootstrapCredential, error)
	// RecordAttempt decrements the remaining attempts atomically and returns
	// what is left, so a parallel guess cannot spend the same budget twice.
	RecordAttempt(ctx context.Context, loginID string) (int, error)
	// Consume marks the credential spent. It participates in the transaction
	// CredentialStore.Save opened when the context carries one.
	Consume(ctx context.Context, loginID string, at time.Time) error
}

var storeState struct {
	sync.RWMutex
	credentials CredentialStore
	bootstrap   BootstrapStore
}

// SetCredentialStore installs the application credential store. Call it from
// main before pw.Run. Without one the framework uses its own table, because
// persisting a sign counter atomically is protocol correctness rather than
// application domain, and getting it wrong has no symptom until an attack.
func SetCredentialStore(store CredentialStore) {
	storeState.Lock()
	defer storeState.Unlock()
	storeState.credentials = store
}

// SetBootstrapStore installs the application bootstrap credential store.
func SetBootstrapStore(store BootstrapStore) {
	storeState.Lock()
	defer storeState.Unlock()
	storeState.bootstrap = store
}

func installedCredentialStore() CredentialStore {
	storeState.RLock()
	defer storeState.RUnlock()
	return storeState.credentials
}

func installedBootstrapStore() BootstrapStore {
	storeState.RLock()
	defer storeState.RUnlock()
	return storeState.bootstrap
}

// txKey carries the transaction Save opened, so a default bootstrap store
// sharing the same database joins that unit of work instead of opening its own.
type txKey struct{}

func withTx(ctx context.Context, tx sqlbind.SQLExecutor) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// The framework-owned stores hold a sqlbind.SQLExecutor rather than a *sql.DB,
// because an engine may bypass database/sql at request time: PostgreSQL is
// served by a native pool, and a connection opened that way has no *sql.DB to
// hand out. Both kinds of pool, and both kinds of transaction, satisfy the
// same interface, so the stores are written once.

// singleRow is the one row a statement was expected to return. It stands in
// for QueryRowContext, which only database/sql can answer.
type singleRow struct {
	rows sqlbind.Rows
	err  error
}

func queryRow(ctx context.Context, db sqlbind.SQLExecutor, query string, args ...any) singleRow {
	rows, err := sqlbind.Query(ctx, db, query, args...)
	return singleRow{rows: rows, err: err}
}

// Scan reads the row and reports sql.ErrNoRows when the statement returned
// none, which is the contract of *sql.Row the callers were written against.
func (r singleRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer func() { _ = r.rows.Close() }()
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	if err := r.rows.Scan(dest...); err != nil {
		return err
	}
	return r.rows.Err()
}

// transaction is one open unit of work on either kind of pool.
type transaction struct {
	sqlbind.SQLExecutor
	commit   func() error
	rollback func()
}

// beginTx opens a transaction on the pool a store holds.
//
// The rollback is safe to defer unconditionally: after a commit it reports
// that the transaction is already finished, and that report is dropped.
func beginTx(ctx context.Context, db sqlbind.SQLExecutor) (transaction, error) {
	switch pool := db.(type) {
	case *sql.DB:
		tx, err := pool.BeginTx(ctx, nil)
		if err != nil {
			return transaction{}, err
		}
		return transaction{SQLExecutor: tx, commit: tx.Commit, rollback: func() { _ = tx.Rollback() }}, nil
	case database.NativeDB:
		tx, err := pool.BeginTx(ctx, database.NativeTxOptions{})
		if err != nil {
			return transaction{}, err
		}
		// The commit and the rollback travel without the caller's context, for
		// the reason pwruntime.TransactionScope gives: neither may be abandoned
		// by the cancellation that ended the request.
		return transaction{
			SQLExecutor: tx,
			commit:      func() error { return tx.Commit(context.Background()) },
			rollback:    func() { _ = tx.Rollback(context.Background()) },
		}, nil
	default:
		return transaction{}, errors.New("auth: the database handle opens no transaction")
	}
}

// rebind rewrites ? placeholders into the $n form the Postgres driver
// requires; the other engines keep ? unchanged. The framework-owned stores
// write every statement with ? and rebind it here, the same numbering the
// RevocationStore produces per-parameter.
func rebind(dialect, statement string) string {
	if dialect != "postgres" {
		return statement
	}
	var builder strings.Builder
	builder.Grow(len(statement) + 8)
	index := 0
	for _, r := range statement {
		if r != '?' {
			builder.WriteRune(r)
			continue
		}
		index++
		builder.WriteByte('$')
		builder.WriteString(strconv.Itoa(index))
	}
	return builder.String()
}

// dbStore is the framework-owned store over the popcornweb_ tables. It is used
// only when the application installed no store of its own.
type dbStore struct {
	db      sqlbind.SQLExecutor
	dialect string
}

func (s dbStore) executor(ctx context.Context) sqlbind.SQLExecutor {
	if tx, ok := ctx.Value(txKey{}).(sqlbind.SQLExecutor); ok && tx != nil {
		return tx
	}
	return s.db
}

func (s dbStore) Find(ctx context.Context, credentialID []byte) (Credential, error) {
	if len(credentialID) == 0 {
		return Credential{}, ErrUnknownCredential
	}
	row := queryRow(ctx, s.executor(ctx), rebind(s.dialect, `SELECT credential_id, account_id, user_handle, public_key,
		public_key_x, public_key_y, algorithm, sign_count, backup_eligible, backup_state, transports,
		label, created_at, last_used_at
		FROM `+CredentialTable+` WHERE credential_id = ?`), credentialID)
	return scanCredential(row)
}

func (s dbStore) ListByAccount(ctx context.Context, accountID string) ([]Credential, error) {
	if accountID == "" {
		return nil, nil
	}
	rows, err := sqlbind.Query(ctx, s.executor(ctx), rebind(s.dialect, `SELECT credential_id, account_id, user_handle, public_key,
		public_key_x, public_key_y, algorithm, sign_count, backup_eligible, backup_state, transports,
		label, created_at, last_used_at
		FROM `+CredentialTable+` WHERE account_id = ? ORDER BY created_at`), accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []Credential
	for rows.Next() {
		credential, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, credential)
	}
	return result, rows.Err()
}

func (s dbStore) Save(ctx context.Context, credential Credential, within func(context.Context) error) error {
	if len(credential.CredentialID) == 0 || credential.AccountID == "" {
		return errors.New("auth: credential needs an ID and an account")
	}
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.rollback()
	if _, err := tx.ExecContext(ctx, rebind(s.dialect, `INSERT INTO `+CredentialTable+` (credential_id, account_id, user_handle,
		public_key, public_key_x, public_key_y, algorithm, sign_count, backup_eligible, backup_state,
		transports, label, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`),
		credential.CredentialID, credential.AccountID, credential.UserHandle, credential.PublicKey,
		credential.PublicKeyX, credential.PublicKeyY,
		credential.Algorithm, credential.SignCount, flag(credential.BackupEligible), flag(credential.BackupState),
		strings.Join(credential.Transports, ","), credential.Label, credential.CreatedAt); err != nil {
		return err
	}
	if within != nil {
		// The callback activates the account and consumes the bootstrap
		// credential. A partially applied enrollment is a defect, so it shares
		// this transaction rather than running after it.
		if err := within(withTx(ctx, tx.SQLExecutor)); err != nil {
			return err
		}
	}
	return tx.commit()
}

// UpdateOnAssertion moves the stored counter forward or changes nothing.
//
// The WHERE clause carries the comparison rather than a read followed by a
// write, so two assertions racing on one credential cannot leave the lower
// count stored. A counter that can go backwards detects no clone, which is the
// only thing it is for.
//
// A zero incoming count means the authenticator keeps none, and the comparison
// is dropped for it. Zero rows updated is returned as ErrUnknownCredential
// whether the credential is gone or the counter did not advance: both fail the
// ceremony closed, and neither is downgraded to a warning. The DynamoDB store
// applies the same condition and maps it the same way.
func (s dbStore) UpdateOnAssertion(ctx context.Context, credentialID []byte, signCount uint32, backupState bool, usedAt time.Time) error {
	statement := `UPDATE ` + CredentialTable + `
		SET sign_count = ?, backup_state = ?, last_used_at = ? WHERE credential_id = ?`
	arguments := []any{signCount, flag(backupState), usedAt, credentialID}
	if signCount > 0 {
		statement += ` AND sign_count < ?`
		arguments = append(arguments, signCount)
	}
	result, err := s.executor(ctx).ExecContext(ctx, rebind(s.dialect, statement), arguments...)
	if err != nil {
		return err
	}
	return requireOneRow(result, ErrUnknownCredential)
}

func (s dbStore) Delete(ctx context.Context, accountID string, credentialID []byte) error {
	result, err := s.executor(ctx).ExecContext(ctx,
		rebind(s.dialect, `DELETE FROM `+CredentialTable+` WHERE account_id = ? AND credential_id = ?`), accountID, credentialID)
	if err != nil {
		return err
	}
	return requireOneRow(result, ErrUnknownCredential)
}

// bootstrapStore is the framework-owned BootstrapStore. It is a type of its own
// rather than more methods on dbStore, because both interfaces declare Find and
// they return different records.
type bootstrapStore struct {
	db      sqlbind.SQLExecutor
	dialect string
}

func (s bootstrapStore) executor(ctx context.Context) sqlbind.SQLExecutor {
	return dbStore(s).executor(ctx)
}

func (s bootstrapStore) Issue(ctx context.Context, credential BootstrapCredential) error {
	if credential.LoginID == "" || credential.AccountID == "" || len(credential.SecretDigest) == 0 {
		return errors.New("auth: bootstrap credential needs a login ID, an account, and a secret digest")
	}
	_, err := s.executor(ctx).ExecContext(ctx, rebind(s.dialect, `INSERT INTO `+BootstrapTable+` (login_id, account_id,
		secret_digest, purpose, issued_at, expires_at, attempts_remaining, consumed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`),
		credential.LoginID, credential.AccountID, credential.SecretDigest, credential.Purpose,
		credential.IssuedAt, credential.ExpiresAt, credential.AttemptsRemaining)
	return err
}

func (s bootstrapStore) Find(ctx context.Context, loginID string) (BootstrapCredential, error) {
	if loginID == "" {
		return BootstrapCredential{}, ErrUnknownBootstrap
	}
	var credential BootstrapCredential
	var consumed sql.NullTime
	err := queryRow(ctx, s.executor(ctx), rebind(s.dialect, `SELECT login_id, account_id, secret_digest, purpose,
		issued_at, expires_at, attempts_remaining, consumed_at FROM `+BootstrapTable+`
		WHERE login_id = ? AND consumed_at IS NULL`), loginID).Scan(
		&credential.LoginID, &credential.AccountID, &credential.SecretDigest, &credential.Purpose,
		&credential.IssuedAt, &credential.ExpiresAt, &credential.AttemptsRemaining, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return BootstrapCredential{}, ErrUnknownBootstrap
	}
	if err != nil {
		return BootstrapCredential{}, err
	}
	if consumed.Valid {
		credential.ConsumedAt = consumed.Time
	}
	return credential, nil
}

func (s bootstrapStore) RecordAttempt(ctx context.Context, loginID string) (int, error) {
	// One statement, so two parallel guesses cannot both read the same budget
	// and both decide they have an attempt left.
	result, err := s.executor(ctx).ExecContext(ctx, rebind(s.dialect, `UPDATE `+BootstrapTable+`
		SET attempts_remaining = attempts_remaining - 1
		WHERE login_id = ? AND consumed_at IS NULL AND attempts_remaining > 0`), loginID)
	if err != nil {
		return 0, err
	}
	if err := requireOneRow(result, ErrUnknownBootstrap); err != nil {
		return 0, err
	}
	var remaining int
	if err := queryRow(ctx, s.executor(ctx),
		rebind(s.dialect, `SELECT attempts_remaining FROM `+BootstrapTable+` WHERE login_id = ?`), loginID).Scan(&remaining); err != nil {
		return 0, err
	}
	return remaining, nil
}

func (s bootstrapStore) Consume(ctx context.Context, loginID string, at time.Time) error {
	result, err := s.executor(ctx).ExecContext(ctx, rebind(s.dialect, `UPDATE `+BootstrapTable+`
		SET consumed_at = ? WHERE login_id = ? AND consumed_at IS NULL`), at, loginID)
	if err != nil {
		return err
	}
	return requireOneRow(result, ErrUnknownBootstrap)
}

type rowScanner interface {
	Scan(dest ...any) error
}

// flag spells a boolean the way its column is declared. Every supported engine
// stores one as an integer, and only database/sql converts a Go bool into one
// on the way in and back out: a native pool refuses the pair as a type
// mismatch, so the conversion is written here instead of left to the driver.
func flag(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func scanCredential(row rowScanner) (Credential, error) {
	var credential Credential
	var transports string
	var lastUsed sql.NullTime
	var backupEligible, backupState int64
	err := row.Scan(&credential.CredentialID, &credential.AccountID, &credential.UserHandle,
		&credential.PublicKey, &credential.PublicKeyX, &credential.PublicKeyY,
		&credential.Algorithm, &credential.SignCount,
		&backupEligible, &backupState, &transports, &credential.Label,
		&credential.CreatedAt, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ErrUnknownCredential
	}
	if err != nil {
		return Credential{}, err
	}
	credential.BackupEligible, credential.BackupState = backupEligible != 0, backupState != 0
	if transports != "" {
		credential.Transports = strings.Split(transports, ",")
	}
	if lastUsed.Valid {
		credential.LastUsedAt = lastUsed.Time
	}
	return credential, nil
}

func requireOneRow(result sql.Result, missing error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return missing
	}
	return nil
}
