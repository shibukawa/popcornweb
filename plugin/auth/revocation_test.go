package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	_ "github.com/shibukawa/tinygodriver/database/sql/sqlite"
)

func revocationStore(t *testing.T, mode string) *RevocationStore {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(revocationSchemaSQL("sqlite")); err != nil {
		t.Fatal(err)
	}
	config := validJWTConfig().JWT
	config.Revocation.Mode = mode
	return newRevocationStore(db, "sqlite", config)
}

func TestRevocationCacheIsBounded(t *testing.T) {
	store := revocationStore(t, RevocationToken)
	store.config.MaxPropagationDelay = time.Minute
	store.cache = make(map[string]cachedRevocation)

	// The key carries an identifier taken out of a presented token, so the
	// number of distinct entries is chosen by whoever presents tokens.
	for i := range maxRevocationCacheEntries * 2 {
		store.remember("issuer\x00token\x00"+strconv.Itoa(i), cachedRevocation{})
		if len(store.cache) > maxRevocationCacheEntries {
			t.Fatalf("cache grew to %d entries, past the %d cap", len(store.cache), maxRevocationCacheEntries)
		}
	}
	if len(store.cache) == 0 {
		t.Fatal("the cache retained nothing at all")
	}
}

func TestRevocationCacheDropsWhatTheDelayExpired(t *testing.T) {
	store := revocationStore(t, RevocationToken)
	store.config.MaxPropagationDelay = time.Minute
	store.cache = map[string]cachedRevocation{
		"stale": {answeredAt: time.Now().Add(-2 * time.Minute)},
		"fresh": {answeredAt: time.Now()},
	}

	// Reading an entry the delay has already retired removes it, rather than
	// leaving it to be re-judged on every later read.
	if _, ok := store.cached("stale"); ok {
		t.Fatal("an entry past the delay was used")
	}
	if _, found := store.cache["stale"]; found {
		t.Fatal("an entry past the delay was kept")
	}
	if _, ok := store.cached("fresh"); !ok {
		t.Fatal("an entry within the delay was discarded")
	}
}

func bearerIdentity(issuer, subject, tokenID string, issuedAt time.Time) Identity {
	identity := testIdentity(issuer, subject, nil)
	identity.TokenID = tokenID
	identity.IssuedAt = issuedAt
	return identity
}

func TestTokenRevocationWithdrawsOneToken(t *testing.T) {
	store := revocationStore(t, RevocationToken)
	ctx := context.Background()
	issuer := "https://issuer.example"
	revoked := bearerIdentity(issuer, "caller-1", "token-1", time.Now())
	other := bearerIdentity(issuer, "caller-1", "token-2", time.Now())

	if err := store.check(ctx, revoked); err != nil {
		t.Fatalf("a token was refused before anything revoked it: %v", err)
	}
	if err := store.write(ctx, issuer, revocationKindToken, "token-1", "leaked"); err != nil {
		t.Fatal(err)
	}
	if err := store.check(ctx, revoked); !errors.Is(err, ErrRevokedToken) {
		t.Fatalf("the revoked token was still accepted: err = %v", err)
	}
	// The narrow act must stay narrow: the caller's other token is unaffected.
	if err := store.check(ctx, other); err != nil {
		t.Fatalf("revoking one token refused another: %v", err)
	}
}

// The subject form ends the credentials an identity holds without ending the
// identity, so a token minted after the revocation works.
func TestSubjectRevocationEndsOutstandingTokensOnly(t *testing.T) {
	store := revocationStore(t, RevocationSubject)
	ctx := context.Background()
	issuer := "https://issuer.example"

	before := bearerIdentity(issuer, "caller-1", "token-1", time.Now().Add(-time.Minute))
	if err := store.write(ctx, issuer, revocationKindSubject, "caller-1", "compromised"); err != nil {
		t.Fatal(err)
	}
	if err := store.check(ctx, before); !errors.Is(err, ErrRevokedToken) {
		t.Fatalf("a token issued before the revocation was accepted: err = %v", err)
	}

	after := bearerIdentity(issuer, "caller-1", "token-2", time.Now().Add(time.Minute))
	if err := store.check(ctx, after); err != nil {
		t.Fatalf("a token issued after the revocation was refused: %v", err)
	}

	// Another identity under the same issuer is untouched.
	stranger := bearerIdentity(issuer, "caller-2", "token-3", time.Now().Add(-time.Minute))
	if err := store.check(ctx, stranger); err != nil {
		t.Fatalf("revoking one identity refused another: %v", err)
	}
}

// A revocation is scoped by issuer, so the same subject value at another issuer
// is a different person.
func TestRevocationIsScopedByIssuer(t *testing.T) {
	store := revocationStore(t, RevocationSubject)
	ctx := context.Background()
	if err := store.write(ctx, "https://issuer.example", revocationKindSubject, "caller-1", ""); err != nil {
		t.Fatal(err)
	}
	elsewhere := bearerIdentity("https://other.example", "caller-1", "token-1", time.Now().Add(-time.Minute))
	if err := store.check(ctx, elsewhere); err != nil {
		t.Fatalf("a revocation leaked across issuers: %v", err)
	}
}

// Neither form substitutes for the other, so "both" must consult both.
func TestBothFormsAreConsulted(t *testing.T) {
	ctx := context.Background()
	issuer := "https://issuer.example"

	store := revocationStore(t, RevocationBoth)
	if err := store.write(ctx, issuer, revocationKindToken, "token-1", ""); err != nil {
		t.Fatal(err)
	}
	byToken := bearerIdentity(issuer, "caller-1", "token-1", time.Now())
	if err := store.check(ctx, byToken); !errors.Is(err, ErrRevokedToken) {
		t.Fatalf("the token form was not consulted: err = %v", err)
	}

	store = revocationStore(t, RevocationBoth)
	if err := store.write(ctx, issuer, revocationKindSubject, "caller-2", ""); err != nil {
		t.Fatal(err)
	}
	bySubject := bearerIdentity(issuer, "caller-2", "token-9", time.Now().Add(-time.Minute))
	if err := store.check(ctx, bySubject); !errors.Is(err, ErrRevokedToken) {
		t.Fatalf("the subject form was not consulted: err = %v", err)
	}
}

// A store that cannot answer has not said the token is valid.
func TestRevocationFailsClosedWhenTheStoreIsUnreachable(t *testing.T) {
	store := revocationStore(t, RevocationBoth)
	_ = store.db.(*sql.DB).Close()
	identity := bearerIdentity("https://issuer.example", "caller-1", "token-1", time.Now())

	err := store.check(context.Background(), identity)
	if err == nil {
		t.Fatal("an unreachable store answered that the token was fine")
	}
	if errors.Is(err, ErrRevokedToken) {
		t.Fatal("an outage was reported as a revocation rather than as an unknown")
	}
	if refusal := store.onUnavailable(); !errors.Is(refusal, ErrInvalidToken) {
		t.Fatalf("the default did not fail closed: %v", refusal)
	}
}

// The escape hatch keeps serving during an outage, which is what makes it an
// incident lever rather than a posture.
func TestRevocationAdmitOverrideKeepsServing(t *testing.T) {
	store := revocationStore(t, RevocationBoth)
	store.config.OnUnavailable = RevocationAdmit
	if refusal := store.onUnavailable(); refusal != nil {
		t.Fatalf("the admit override still refused: %v", refusal)
	}
}

// Revocation off means the lookup never runs, so a closed database is not an
// error a deployment that revokes nothing should ever see.
func TestRevocationOffConsultsNothing(t *testing.T) {
	store := revocationStore(t, RevocationOff)
	_ = store.db.(*sql.DB).Close()
	identity := bearerIdentity("https://issuer.example", "caller-1", "token-1", time.Now())
	if err := store.check(context.Background(), identity); err != nil {
		t.Fatalf("a deployment with revocation off consulted the store: %v", err)
	}
}

// A token with no jti cannot be named, so the token form has to refuse it
// rather than find no row and admit.
func TestTokenRevocationRefusesATokenWithNoIdentifier(t *testing.T) {
	store := revocationStore(t, RevocationToken)
	anonymous := bearerIdentity("https://issuer.example", "caller-1", "", time.Now())
	if err := store.check(context.Background(), anonymous); !errors.Is(err, ErrRevokedToken) {
		t.Fatalf("a token nobody can name was accepted under token revocation: err = %v", err)
	}
}

// Revoking twice moves the stamp forward; it is what an operator does when the
// first attempt did not obviously work.
func TestRevokingTwiceMovesTheStampForward(t *testing.T) {
	store := revocationStore(t, RevocationSubject)
	ctx := context.Background()
	issuer := "https://issuer.example"

	if err := store.write(ctx, issuer, revocationKindSubject, "caller-1", "first"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	// A token minted here survives the first revocation, because it was issued
	// after it.
	middle := time.Now().UTC()
	between := bearerIdentity(issuer, "caller-1", "token-1", middle)
	if err := store.check(ctx, between); err != nil {
		t.Fatalf("a token issued after the first revocation was refused: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	if err := store.write(ctx, issuer, revocationKindSubject, "caller-1", "second"); err != nil {
		t.Fatalf("a second revocation failed instead of replacing the first: %v", err)
	}
	// The same token is now caught, which is only possible if the stamp moved.
	if err := store.check(ctx, between); !errors.Is(err, ErrRevokedToken) {
		t.Fatalf("the stamp did not move forward: err = %v", err)
	}
}

// A cache must not hide a revocation this process just wrote.
func TestWritingARevocationDropsItsCachedAnswer(t *testing.T) {
	store := revocationStore(t, RevocationToken)
	store.config.MaxPropagationDelay = time.Hour
	store.cache = map[string]cachedRevocation{}
	ctx := context.Background()
	issuer := "https://issuer.example"
	identity := bearerIdentity(issuer, "caller-1", "token-1", time.Now())

	// Warm the cache with a "not revoked" answer.
	if err := store.check(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.write(ctx, issuer, revocationKindToken, "token-1", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.check(ctx, identity); !errors.Is(err, ErrRevokedToken) {
		t.Fatalf("the cache hid a revocation this process wrote: err = %v", err)
	}
}

func TestPruneRemovesEntriesThatOutlivedTheirTokens(t *testing.T) {
	store := revocationStore(t, RevocationToken)
	ctx := context.Background()
	issuer := "https://issuer.example"
	if err := store.write(ctx, issuer, revocationKindToken, "token-1", ""); err != nil {
		t.Fatal(err)
	}
	// The entry is retained for one token lifetime; sweep past it.
	if err := store.prune(ctx, time.Now().Add(2*store.lifetime)); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := queryRow(ctx, store.db, `SELECT COUNT(*) FROM `+RevocationTable).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("prune left %d expired entries", remaining)
	}
}

// Reinstating removes the entry, so an unexpired token the revocation was
// refusing works again at the next request.
func TestReinstateRemovesARevocation(t *testing.T) {
	store := revocationStore(t, RevocationToken)
	ctx := context.Background()
	issuer := "https://issuer.example"
	identity := bearerIdentity(issuer, "caller-1", "token-1", time.Now())

	if err := store.write(ctx, issuer, revocationKindToken, "token-1", "mistake"); err != nil {
		t.Fatal(err)
	}
	if err := store.check(ctx, identity); !errors.Is(err, ErrRevokedToken) {
		t.Fatalf("setup: the token was not revoked: %v", err)
	}
	if err := store.reinstate(ctx, issuer, revocationKindToken, "token-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.check(ctx, identity); err != nil {
		t.Fatalf("a reinstated token was still refused: %v", err)
	}
}

// An administrative view must not guess, so the state read bypasses the cache.
func TestRevocationStateReportsTheStamp(t *testing.T) {
	store := revocationStore(t, RevocationToken)
	ctx := context.Background()
	issuer := "https://issuer.example"

	if _, found, err := store.state(ctx, issuer, revocationKindToken, "token-1"); err != nil || found {
		t.Fatalf("an unrevoked token reported found = %v err = %v", found, err)
	}
	if err := store.write(ctx, issuer, revocationKindToken, "token-1", ""); err != nil {
		t.Fatal(err)
	}
	stamp, found, err := store.state(ctx, issuer, revocationKindToken, "token-1")
	if err != nil || !found {
		t.Fatalf("a revoked token reported found = %v err = %v", found, err)
	}
	if stamp.IsZero() {
		t.Fatal("the revocation carries no stamp")
	}
}
