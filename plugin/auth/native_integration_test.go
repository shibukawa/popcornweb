//go:build !tinygo

package auth

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/authstate"
	_ "github.com/shibukawa/popcornweb/authstate/postgres"
	"github.com/shibukawa/popcornweb/database"
	_ "github.com/shibukawa/popcornweb/database/postgres"
)

// nativePostgres opens the pool a PostgreSQL connection is served by at
// request time, which is a native one and therefore has no *sql.DB. The run is
// opt-in for the reason every server engine's is.
func nativePostgres(t *testing.T) database.NativeDB {
	t.Helper()
	dsn := os.Getenv("PW_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set PW_POSTGRES_TEST_DSN to run this test")
	}
	target, err := database.Resolve(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	native, err := target.OpenNative(ctx, database.PoolBounds{MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = native.Close() })
	return native
}

var ownedTables = []string{RevocationTable, BootstrapTable, CredentialTable, AllowlistTable, authstate.TableName}

func dropOwnedTables(t *testing.T, native database.NativeDB) {
	t.Helper()
	for _, table := range ownedTables {
		if _, err := native.ExecContext(context.Background(), `DROP TABLE IF EXISTS `+table); err != nil {
			t.Fatal(err)
		}
	}
}

// migrate applies the statements of the migration a project carries, one at a
// time, so the test runs against the schema a deployment has rather than one
// written for the test.
func migrate(t *testing.T, native database.NativeDB) {
	t.Helper()
	migration, err := MigrationSQL("postgres")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(migration, "-- +goose Down")
	for statement := range strings.SplitSeq(up, ";") {
		var kept []string
		for line := range strings.SplitSeq(statement, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				kept = append(kept, line)
			}
		}
		statement = strings.TrimSpace(strings.Join(kept, "\n"))
		if statement == "" {
			continue
		}
		if _, err := native.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

// The relational backend is the default, and PostgreSQL is served by a native
// pool. A backend that asked the connection for its *sql.DB found none and
// reported that middleware.rdb was disabled, so the default backend did not
// start on one of the three supported engines.
func TestRelationalBackendOnANativePool(t *testing.T) {
	native := nativePostgres(t)
	dropOwnedTables(t, native)
	t.Cleanup(func() { dropOwnedTables(t, native) })
	ctx := t.Context()
	config := Config{
		Mode:         ModeOIDCPasskey,
		OIDC:         OIDCConfig{Admission: AdmissionRegistered},
		Registration: RegistrationConfig{Policy: RegistrationInvite},
	}
	resources := Resources{Executor: native, DBDriver: "postgres"}

	// The migration has not run, and the refusal names it.
	if _, err := openRelationalBackend(ctx, config, resources); err == nil ||
		!strings.Contains(err.Error(), MigrationName) {
		t.Fatalf("a missing table was reported as %v", err)
	}

	migrate(t, native)
	backend, err := openRelationalBackend(ctx, config, resources)
	if err != nil {
		t.Fatal(err)
	}

	state, err := backend.OpenState(ctx, "native")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Put(ctx, "ceremony", []byte("payload"), time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if payload, err := state.Take(ctx, "ceremony"); err != nil || string(payload) != "payload" {
		t.Fatalf("Take = (%q, %v)", payload, err)
	}
	if _, err := state.Take(ctx, "ceremony"); !errors.Is(err, authstate.ErrNotFound) {
		t.Fatalf("second Take = %v", err)
	}

	if _, err := native.ExecContext(ctx, `INSERT INTO `+AllowlistTable+
		` (issuer, claim, value) VALUES ($1, $2, $3)`, "https://issuer.example", "email", "a@example.com"); err != nil {
		t.Fatal(err)
	}
	for value, want := range map[string]bool{"a@example.com": true, "b@example.com": false} {
		registered, err := backend.Allowlist.Registered(ctx, "https://issuer.example",
			[]AllowlistCandidate{{Claim: "sub", Value: "nobody"}, {Claim: "email", Value: value}})
		if err != nil || registered != want {
			t.Fatalf("Registered(%s) = (%v, %v), want %v", value, registered, err, want)
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := backend.Bootstrap.Issue(ctx, BootstrapCredential{
		LoginID: "login-1", AccountID: "account-1", SecretDigest: []byte("digest"),
		Purpose: PurposeInitialPasskey, IssuedAt: now, ExpiresAt: now.Add(time.Hour), AttemptsRemaining: 3,
	}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if remaining, err := backend.Bootstrap.RecordAttempt(ctx, "login-1"); err != nil || remaining != 2 {
		t.Fatalf("RecordAttempt = (%d, %v)", remaining, err)
	}

	credential := testCredential("account-1", 0x21)
	credential.SignCount = 4
	// A failed enrollment leaves neither the credential nor the consumed
	// bootstrap behind: both writes share the native transaction.
	failed := errors.New("enrollment refused")
	err = backend.Credentials.Save(ctx, credential, func(ctx context.Context) error {
		if err := backend.Bootstrap.Consume(ctx, "login-1", now); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("Save = %v", err)
	}
	if _, err := backend.Credentials.Find(ctx, credential.CredentialID); !errors.Is(err, ErrUnknownCredential) {
		t.Fatalf("a rolled back credential was found: %v", err)
	}
	if _, err := backend.Bootstrap.Find(ctx, "login-1"); err != nil {
		t.Fatalf("a rolled back consumption was kept: %v", err)
	}

	err = backend.Credentials.Save(ctx, credential, func(ctx context.Context) error {
		return backend.Bootstrap.Consume(ctx, "login-1", now)
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := backend.Bootstrap.Find(ctx, "login-1"); !errors.Is(err, ErrUnknownBootstrap) {
		t.Fatalf("a consumed bootstrap credential is still offered: %v", err)
	}
	found, err := backend.Credentials.Find(ctx, credential.CredentialID)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found.AccountID != "account-1" || !found.BackupEligible || found.BackupState ||
		found.Algorithm != credential.Algorithm || found.SignCount != 4 ||
		!found.CreatedAt.Equal(credential.CreatedAt) || !found.LastUsedAt.IsZero() ||
		strings.Join(found.Transports, ",") != "internal,hybrid" {
		t.Fatalf("Find = %+v", found)
	}
	if err := backend.Credentials.UpdateOnAssertion(ctx, credential.CredentialID, 5, true, now); err != nil {
		t.Fatalf("UpdateOnAssertion: %v", err)
	}
	// A counter that did not advance fails the ceremony closed.
	if err := backend.Credentials.UpdateOnAssertion(ctx, credential.CredentialID, 5, true, now); !errors.Is(err, ErrUnknownCredential) {
		t.Fatalf("a replayed counter = %v", err)
	}
	listed, err := backend.Credentials.ListByAccount(ctx, "account-1")
	if err != nil || len(listed) != 1 || !listed[0].BackupState || !listed[0].LastUsedAt.Equal(now) {
		t.Fatalf("ListByAccount = (%+v, %v)", listed, err)
	}
	if err := backend.Credentials.Delete(ctx, "account-1", credential.CredentialID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestRevocationOnANativePool(t *testing.T) {
	native := nativePostgres(t)
	dropOwnedTables(t, native)
	t.Cleanup(func() { dropOwnedTables(t, native) })
	migrate(t, native)
	ctx := t.Context()
	config := validJWTConfig()
	config.JWT.Revocation.Mode = RevocationBoth
	if err := verifyTables(ctx, native, "postgres", config); err != nil {
		t.Fatal(err)
	}
	store := newRevocationStore(native, "postgres", config.JWT)
	issuer := "https://issuer.example"
	if err := store.write(ctx, issuer, revocationKindToken, "token-1", ""); err != nil {
		t.Fatal(err)
	}
	// Revoking twice moves the stamp instead of failing on the primary key.
	if err := store.write(ctx, issuer, revocationKindToken, "token-1", "again"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.state(ctx, issuer, revocationKindToken, "token-1"); err != nil || !found {
		t.Fatalf("state = (%v, %v)", found, err)
	}
	if _, found, err := store.state(ctx, issuer, revocationKindToken, "token-2"); err != nil || found {
		t.Fatalf("state of an unrevoked token = (%v, %v)", found, err)
	}
	if err := store.prune(ctx, time.Now().Add(2*store.lifetime)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.state(ctx, issuer, revocationKindToken, "token-1"); err != nil || found {
		t.Fatalf("state after prune = (%v, %v)", found, err)
	}
}
