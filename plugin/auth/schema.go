package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/shibukawa/popcornweb/authstate"
)

// MigrationName is the stable name of the migration a project carries for the
// tables this package owns, without a version. See rdb.MigrationName for why
// the version belongs to the project rather than to the package.
const MigrationName = "init_popcornweb_auth"

// AllowlistTable holds the identities a deployment registered in advance. It is
// consulted by AdmissionRegistered.
const AllowlistTable = "popcornweb_auth_allowlist"

// allowlistSchemaSQL is the DDL of the pre-registration table under one
// engine.
//
// A row names one verified claim and its expected value, because a local
// deployment usually knows an operator's email or account name before that
// person has ever logged in and therefore before its subject exists. MySQL
// indexes no unbounded text, so its three key columns carry a length.
func allowlistSchemaSQL(dialect string) string {
	text, key := "TEXT", "TEXT"
	if dialect == "mysql" {
		key = "VARCHAR(255)"
	}
	return `CREATE TABLE IF NOT EXISTS ` + AllowlistTable + ` (
	issuer ` + key + ` NOT NULL,
	claim ` + key + ` NOT NULL,
	value ` + key + ` NOT NULL,
	note ` + text + ` NOT NULL DEFAULT '',
	PRIMARY KEY (issuer, claim, value)
)`
}

// MigrationSQL returns the goose migration that creates the tables this
// package owns under one engine: the single-use OIDC correlation records and
// the pre-registration allowlist.
func MigrationSQL(dialect string) (string, error) {
	stateSchema, err := authstate.SchemaSQL(dialect)
	if err != nil {
		return "", err
	}
	return `-- +goose Up
-- Owned by github.com/shibukawa/popcornweb/plugin/auth.
-- Single-use login correlation: state, nonce, and PKCE verifier of a pending
-- ceremony. Rows are consumed by the callback and swept after they expire.
` + stateSchema + `;

-- Identities a deployment registered before their first login. Only consulted
-- when auth.oidc.admission is "registered".
` + allowlistSchemaSQL(dialect) + `;

-- Passkey credentials of the default credential store. Unused when the
-- application installs its own store, and unused entirely in oidc_only.
` + credentialSchemaSQL(dialect) + `;
` + credentialAccountIndexSQL() + `;

-- Issued login IDs and secret digests that open one passkey enrollment.
` + bootstrapSchemaSQL(dialect) + `;

-- Tokens and identities withdrawn before their tokens expire. Only consulted
-- when auth.mode is "jwt_only" and auth.jwt.revocation.mode is not "off". A row
-- is a positive statement, so an unreachable table is an unknown rather than a
-- "not revoked".
` + revocationSchemaSQL(dialect) + `;
` + revocationExpiryIndexSQL() + `;

-- +goose Down
DROP TABLE ` + RevocationTable + `;
DROP TABLE ` + BootstrapTable + `;
DROP TABLE ` + CredentialTable + `;
DROP TABLE ` + AllowlistTable + `;
DROP TABLE ` + authstate.TableName + `;
`, nil
}

// requiredTables lists the tables this package owns and the migration that
// creates each one. The session table is not among them: a session backend
// verifies its own storage, and a cookie or Redis backend has no table here at
// all.

// CredentialTable holds the passkey credentials of the default
// CredentialStore. A deployment that installs its own store owns its own table
// and this one is neither created nor verified.
const CredentialTable = "popcornweb_passkey_credential"

// BootstrapTable holds the issued credentials that open a first passkey
// enrollment, for the default BootstrapStore.
const BootstrapTable = "popcornweb_auth_bootstrap"

// credentialSchemaSQL is the DDL of the passkey credential table.
//
// The key is stored twice on purpose: as the normalized COSE blob the ceremony
// produced, and as the curve points the relying party verifies with. It
// cross-checks the two, so a corrupted row fails closed instead of verifying
// against something else. The counter and backup state live beside them because
// an accepted assertion updates all of it in one statement.
func credentialSchemaSQL(dialect string) string {
	blob, text, key, timestamp := blobType(dialect), "TEXT", keyType(dialect), timestampType(dialect)
	// A credential ID is the primary key, and MySQL cannot index an unbounded
	// blob, so it is the one engine that needs a length.
	credentialKey := blob
	if dialect == "mysql" {
		credentialKey = "VARBINARY(255)"
	}
	return `CREATE TABLE IF NOT EXISTS ` + CredentialTable + ` (
	credential_id ` + credentialKey + ` NOT NULL PRIMARY KEY,
	account_id ` + key + ` NOT NULL,
	user_handle ` + blob + ` NOT NULL,
	public_key ` + blob + ` NOT NULL,
	public_key_x ` + blob + ` NOT NULL,
	public_key_y ` + blob + ` NOT NULL,
	algorithm INTEGER NOT NULL,
	sign_count BIGINT NOT NULL DEFAULT 0,
	backup_eligible ` + boolType(dialect) + ` NOT NULL DEFAULT 0,
	backup_state ` + boolType(dialect) + ` NOT NULL DEFAULT 0,
	transports ` + text + ` NOT NULL,
	label ` + text + ` NOT NULL,
	created_at ` + timestamp + ` NOT NULL,
	last_used_at ` + timestamp + `
)`
}

func credentialAccountIndexSQL() string {
	return `CREATE INDEX IF NOT EXISTS ` + CredentialTable + `_account
	ON ` + CredentialTable + ` (account_id)`
}

// bootstrapSchemaSQL is the DDL of the bootstrap credential table.
//
// Only a digest of the secret is stored, and the attempt budget lives in the
// row so it can be decremented atomically rather than counted in memory.
func bootstrapSchemaSQL(dialect string) string {
	return `CREATE TABLE IF NOT EXISTS ` + BootstrapTable + ` (
	login_id ` + keyType(dialect) + ` NOT NULL PRIMARY KEY,
	account_id ` + keyType(dialect) + ` NOT NULL,
	secret_digest ` + blobType(dialect) + ` NOT NULL,
	purpose ` + keyType(dialect) + ` NOT NULL,
	issued_at ` + timestampType(dialect) + ` NOT NULL,
	expires_at ` + timestampType(dialect) + ` NOT NULL,
	attempts_remaining INTEGER NOT NULL,
	consumed_at ` + timestampType(dialect) + `
)`
}

// The engines differ in how they spell the same column, and only in that: a
// key is indexed so MySQL needs a length, a blob is named differently by each,
// and a boolean is an integer everywhere the framework supports.
func keyType(dialect string) string {
	if dialect == "mysql" {
		return "VARCHAR(255)"
	}
	return "TEXT"
}

func blobType(dialect string) string {
	switch dialect {
	case "postgres":
		return "BYTEA"
	case "mysql":
		return "BLOB"
	default:
		return "BLOB"
	}
}

func boolType(dialect string) string {
	if dialect == "postgres" {
		return "SMALLINT"
	}
	return "INTEGER"
}

func timestampType(dialect string) string {
	if dialect == "postgres" {
		return "TIMESTAMPTZ"
	}
	return "TIMESTAMP"
}

// requiredTables lists the framework tables and the migration that creates
// each one.
//
// A table is required only when the selected mode reads it and the application
// installed no store of its own, so a deployment is never asked for a table
// nothing will ever write to.
func requiredTables(config Config) [][2]string {
	if config.usesJWT() {
		// A bearer request runs no ceremony and creates no session, so the
		// correlation table is not among the tables this mode reads. The
		// allowlist is required only by the admission policy that consults it.
		required := [][2]string{}
		if config.JWT.Admission == AdmissionRegistered {
			required = append(required, [2]string{AllowlistTable, MigrationName})
		}
		if config.JWT.Revocation.enabled() {
			required = append(required, [2]string{RevocationTable, MigrationName})
		}
		return required
	}
	required := [][2]string{
		{authstate.TableName, MigrationName},
	}
	// The allowlist is read only by the registered admission mode, and not at
	// all when the application installed a store of its own. The policy comes
	// from whichever section the mode reads, so an OAuth deployment is asked for
	// the table on the same terms as an OIDC one.
	if config.admission().Admission == AdmissionRegistered && installedAllowlistStore() == nil {
		required = append(required, [2]string{AllowlistTable, MigrationName})
	}
	if !config.usesPasskey() {
		return required
	}
	if installedCredentialStore() == nil {
		required = append(required, [2]string{CredentialTable, MigrationName})
	}
	if installedBootstrapStore() == nil && config.issuesBootstrapCredentials() {
		required = append(required, [2]string{BootstrapTable, MigrationName})
	}
	return required
}

// verifyTables reports a missing framework table together with the migration
// that creates it, so a deployment that skipped the migration fails at startup
// instead of during the first login.
//
// The migration is named without a version, because the version is whatever was
// free in that project when the file was written.
func verifyTables(ctx context.Context, db *sql.DB, config Config) error {
	for _, required := range requiredTables(config) {
		exists, err := tableExists(ctx, db, required[0])
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("table %q is missing: apply the migration named %s with pw migrate up",
				required[0], required[1])
		}
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read schema: %w", err)
	}
	return true, nil
}
