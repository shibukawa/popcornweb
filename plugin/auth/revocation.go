package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shibukawa/tinybind-go/sqlbind"
)

// RevocationTable holds the tokens and identities this deployment has withdrawn.
//
// A row is a positive statement that something is revoked. Absence means not
// revoked, so a lookup that cannot run is not an absence: it is an unknown, and
// policy:token-revocation fails closed on it.
const RevocationTable = "popcornweb_auth_revocation"

// Revocation kinds, stored in the kind column.
const (
	revocationKindToken   = "token"
	revocationKindSubject = "subject"
)

// revocationSchemaSQL is the DDL of the revocation table.
//
// The key column holds a jti for a token row and the identity claim value for a
// subject row, which is why the kind is part of the primary key rather than
// derived from the shape of the value. expires_at is when the row may be swept:
// a revocation only has to outlive the tokens it refuses, and
// auth.jwt.max_token_lifetime is how long that is.
func revocationSchemaSQL(dialect string) string {
	key, timestamp := keyType(dialect), timestampType(dialect)
	return `CREATE TABLE IF NOT EXISTS ` + RevocationTable + ` (
	issuer ` + key + ` NOT NULL,
	kind ` + key + ` NOT NULL,
	key_value ` + key + ` NOT NULL,
	revoked_at ` + timestamp + ` NOT NULL,
	expires_at ` + timestamp + ` NOT NULL,
	note TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (issuer, kind, key_value)
)`
}

func revocationExpiryIndexSQL() string {
	return `CREATE INDEX IF NOT EXISTS ` + RevocationTable + `_expires
	ON ` + RevocationTable + ` (expires_at)`
}

// RevocationStore reads and writes the withdrawal list.
//
// It is exported because revoking is an application act: the framework knows
// how to refuse a revoked token, but only the application knows that an account
// was compromised.
type RevocationStore struct {
	db     sqlbind.SQLExecutor
	config JWTRevocationConfig
	// dialect selects the placeholder style and the upsert spelling. The three
	// supported engines disagree on both, and a statement that runs on the
	// developer's sqlite and fails on the deployment's MySQL is a revocation
	// that silently stops working.
	dialect string
	// lifetime is auth.jwt.max_token_lifetime, which bounds how long a written
	// entry must be retained.
	lifetime time.Duration

	// cache holds recent answers when max_propagation_delay allows one. It is
	// keyed by the same tuple as the table.
	mu    sync.Mutex
	cache map[string]cachedRevocation
}

type cachedRevocation struct {
	revokedAt  time.Time
	found      bool
	answeredAt time.Time
}

func newRevocationStore(db sqlbind.SQLExecutor, dialect string, config JWTConfig) *RevocationStore {
	store := &RevocationStore{
		db:       db,
		config:   config.Revocation,
		dialect:  dialect,
		lifetime: config.MaxTokenLifetime,
	}
	if config.Revocation.MaxPropagationDelay > 0 {
		store.cache = make(map[string]cachedRevocation)
	}
	return store
}

// placeholder renders parameter n, counting from one. Postgres numbers its
// placeholders and the other two engines do not.
func (s *RevocationStore) placeholder(n int) string {
	if s.dialect == "postgres" {
		return "$" + strconv.Itoa(n)
	}
	return "?"
}

// placeholders renders count parameters starting at first.
func (s *RevocationStore) placeholders(first, count int) string {
	rendered := make([]string, count)
	for i := range rendered {
		rendered[i] = s.placeholder(first + i)
	}
	return strings.Join(rendered, ", ")
}

// check reports whether a verified identity's token has been withdrawn.
//
// Both forms are consulted when both are configured, because they answer
// different questions: the token form ends one leaked credential, and the
// subject form ends every credential of a compromised identity. Neither
// substitutes for the other.
func (s *RevocationStore) check(ctx context.Context, identity Identity) error {
	if s == nil || !s.config.enabled() {
		return nil
	}
	if s.config.revokesTokens() {
		revoked, err := s.revokedToken(ctx, identity)
		if err != nil {
			return err
		}
		if revoked {
			return ErrRevokedToken
		}
	}
	if s.config.revokesSubjects() {
		revoked, err := s.revokedSubject(ctx, identity)
		if err != nil {
			return err
		}
		if revoked {
			return ErrRevokedToken
		}
	}
	return nil
}

// revokedToken answers the token form. A token with no jti cannot be named, and
// verification already refused one when this form is configured.
func (s *RevocationStore) revokedToken(ctx context.Context, identity Identity) (bool, error) {
	if identity.TokenID == "" {
		return true, nil
	}
	entry, err := s.lookup(ctx, identity.Issuer, revocationKindToken, identity.TokenID)
	if err != nil {
		return false, err
	}
	return entry.found, nil
}

// revokedSubject answers the subject form by comparing the token's iat against
// the stored stamp.
//
// A token minted after the revocation is admitted. That is what makes the form
// usable: revoking an identity ends the credentials it currently holds without
// ending the identity, so the caller works again as soon as it re-authenticates.
func (s *RevocationStore) revokedSubject(ctx context.Context, identity Identity) (bool, error) {
	entry, err := s.lookup(ctx, identity.Issuer, revocationKindSubject, identity.Key)
	if err != nil {
		return false, err
	}
	if !entry.found {
		return false, nil
	}
	if identity.IssuedAt.IsZero() {
		// Verification requires iat, so this is unreachable through the normal
		// path. Refusing rather than admitting keeps it that way if it ever
		// becomes reachable.
		return true, nil
	}
	return !identity.IssuedAt.After(entry.revokedAt), nil
}

// lookup reads one row, through the cache when the deployment allowed one.
func (s *RevocationStore) lookup(ctx context.Context, issuer, kind, key string) (cachedRevocation, error) {
	cacheKey := issuer + "\x00" + kind + "\x00" + key
	if entry, ok := s.cached(cacheKey); ok {
		return entry, nil
	}
	entry, err := s.read(ctx, issuer, kind, key)
	if err != nil {
		return cachedRevocation{}, err
	}
	s.remember(cacheKey, entry)
	return entry, nil
}

// maxRevocationCacheEntries bounds how many answers this process holds.
//
// The delay above bounds how long one answer is trusted; this bounds how many
// there can be. Both are needed, because the key is built from the issuer and
// an identifier taken out of a presented token, so whoever presents tokens
// chooses how many distinct entries the map is asked to hold, and nothing about
// that caller has been authenticated at the point the lookup runs.
const maxRevocationCacheEntries = 4096

func (s *RevocationStore) cached(key string) (cachedRevocation, bool) {
	if s.cache == nil {
		return cachedRevocation{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[key]
	if !ok {
		return cachedRevocation{}, false
	}
	if time.Since(entry.answeredAt) > s.config.MaxPropagationDelay {
		// Drop it rather than leave it to be re-judged on every later read: an
		// entry past the delay can never be used again under this key.
		delete(s.cache, key)
		return cachedRevocation{}, false
	}
	return entry, true
}

func (s *RevocationStore) remember(key string, entry cachedRevocation) {
	if s.cache == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) >= maxRevocationCacheEntries {
		s.evictLocked(now)
	}
	entry.answeredAt = now
	s.cache[key] = entry
}

// evictLocked drops what the delay has already made unusable, and then
// everything if that was not enough.
//
// Discarding the whole map is acceptable here where an eviction policy would
// normally be wanted: an entry is only an optimization, it is valid for
// max_propagation_delay at most, and losing one costs a single indexed read.
// Growing without a bound is what is not acceptable.
func (s *RevocationStore) evictLocked(now time.Time) {
	for key, entry := range s.cache {
		if now.Sub(entry.answeredAt) > s.config.MaxPropagationDelay {
			delete(s.cache, key)
		}
	}
	if len(s.cache) >= maxRevocationCacheEntries {
		clear(s.cache)
	}
}

// read runs the lookup. A backend failure is an error rather than "not
// revoked", so an outage cannot silently readmit every withdrawn token.
func (s *RevocationStore) read(ctx context.Context, issuer, kind, key string) (cachedRevocation, error) {
	if s.db == nil {
		return cachedRevocation{}, errors.New("auth: revocation store is not available")
	}
	var revokedAt time.Time
	err := queryRow(ctx, s.db,
		`SELECT revoked_at FROM `+RevocationTable+
			` WHERE issuer = `+s.placeholder(1)+
			` AND kind = `+s.placeholder(2)+
			` AND key_value = `+s.placeholder(3),
		issuer, kind, key).Scan(&revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return cachedRevocation{found: false}, nil
	}
	if err != nil {
		return cachedRevocation{}, fmt.Errorf("auth: read revocation: %w", err)
	}
	return cachedRevocation{found: true, revokedAt: revokedAt.UTC()}, nil
}

// RevokeToken withdraws one access token by its jti.
//
// It is the narrow act: a credential leaked, and the identity that holds it is
// otherwise fine. The entry is retained for one token lifetime, which is how
// long a token carrying that jti could still be presented.
func (rt *runtime) RevokeToken(ctx context.Context, issuer, tokenID, note string) error {
	return rt.revocations.write(ctx, issuer, revocationKindToken, tokenID, note)
}

// RevokeSubject withdraws every token issued to an identity before now.
//
// It is the broad act: the identity is compromised, and enumerating the
// outstanding jti values is exactly what nobody can do. The identity keeps
// working once it authenticates again, because the comparison is against iat.
func (rt *runtime) RevokeSubject(ctx context.Context, issuer, identityKey, note string) error {
	return rt.revocations.write(ctx, issuer, revocationKindSubject, identityKey, note)
}

// write records a revocation, replacing an earlier one for the same key so a
// second call moves the stamp forward rather than failing on the primary key.
func (s *RevocationStore) write(ctx context.Context, issuer, kind, key, note string) error {
	if s == nil || s.db == nil {
		return errors.New("auth: revocation store is not available")
	}
	if !s.config.enabled() {
		return fmt.Errorf("auth: revocation is off; set auth.jwt.revocation.mode to %q, %q, or %q",
			RevocationToken, RevocationSubject, RevocationBoth)
	}
	if issuer == "" || key == "" {
		return errors.New("auth: revocation needs an issuer and a key")
	}
	now := time.Now().UTC()
	// A second revocation of the same key moves the stamp forward rather than
	// failing on the primary key: revoking twice is what an operator does when
	// the first attempt did not obviously work.
	statement := `INSERT INTO ` + RevocationTable +
		` (issuer, kind, key_value, revoked_at, expires_at, note) VALUES (` +
		s.placeholders(1, 6) + `) `
	if s.dialect == "mysql" {
		statement += `ON DUPLICATE KEY UPDATE
		   revoked_at = VALUES(revoked_at),
		   expires_at = VALUES(expires_at),
		   note = VALUES(note)`
	} else {
		statement += `ON CONFLICT (issuer, kind, key_value) DO UPDATE SET
		   revoked_at = excluded.revoked_at,
		   expires_at = excluded.expires_at,
		   note = excluded.note`
	}
	_, err := s.db.ExecContext(ctx, statement, issuer, kind, key, now, now.Add(s.lifetime), note)
	if err != nil {
		return fmt.Errorf("auth: write revocation: %w", err)
	}
	s.forget(issuer + "\x00" + kind + "\x00" + key)
	return nil
}

// reinstate removes an entry, for a revocation issued in error.
//
// It is a separate act from never having revoked: the entry is deleted, so
// every token the revocation was refusing works again immediately if it has not
// expired. That is the intended effect, and the reason this is not offered as
// an "undo" that only the writer may perform.
func (s *RevocationStore) reinstate(ctx context.Context, issuer, kind, key string) error {
	if s == nil || s.db == nil {
		return errors.New("auth: revocation store is not available")
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM `+RevocationTable+
			` WHERE issuer = `+s.placeholder(1)+
			` AND kind = `+s.placeholder(2)+
			` AND key_value = `+s.placeholder(3),
		issuer, kind, key)
	if err != nil {
		return fmt.Errorf("auth: reinstate: %w", err)
	}
	s.forget(issuer + "\x00" + kind + "\x00" + key)
	return nil
}

// state reports the current entry, bypassing the cache.
//
// An administrative view must not guess, and the cache exists to bound
// per-request latency rather than to answer questions about the list itself.
func (s *RevocationStore) state(ctx context.Context, issuer, kind, key string) (time.Time, bool, error) {
	if s == nil || s.db == nil {
		return time.Time{}, false, errors.New("auth: revocation store is not available")
	}
	entry, err := s.read(ctx, issuer, kind, key)
	if err != nil {
		return time.Time{}, false, err
	}
	return entry.revokedAt, entry.found, nil
}

// forget drops a cached answer so a revocation this process just wrote is not
// hidden by its own cache.
func (s *RevocationStore) forget(key string) {
	if s.cache == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, key)
}

// prune deletes entries that have outlived every token they could refuse.
//
// The delete is unbounded rather than batched. The table holds one row per
// revoked token or identity for one token lifetime, which is a list an operator
// wrote by hand: it does not reach the size that made batching necessary for
// the ceremony records, and a batched delete would need a subquery the three
// engines spell three ways.
func (s *RevocationStore) prune(ctx context.Context, before time.Time) error {
	if s == nil || s.db == nil || !s.config.enabled() {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM `+RevocationTable+` WHERE expires_at < `+s.placeholder(1),
		before.UTC())
	return err
}

// onUnavailable decides what a lookup failure means.
//
// The default refuses: a store that cannot answer has not said the token is
// valid. A deployment may choose to keep serving instead, which makes
// revocation advisory for the duration of the outage — an incident lever, not a
// posture, and rule:configuration-advisories reports it as an error.
func (s *RevocationStore) onUnavailable() error {
	if s.config.OnUnavailable == RevocationAdmit {
		return nil
	}
	return ErrInvalidToken
}
