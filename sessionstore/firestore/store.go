// Package firestore stores login sessions in Firestore in Datastore mode.
//
// It exists so a deployment on Google Cloud with no relational database can
// still log a user in. Importing it registers the backend; the client itself
// belongs to database/firestore, which this package reads from the request
// context:
//
//	import _ "github.com/shibukawa/popcornweb/database/firestore"
//	import _ "github.com/shibukawa/popcornweb/sessionstore/firestore"
//
// Nothing here sweeps expired records. A record is judged expired when it is
// read and cannot be renewed once dead, so correctness never waits for a
// deletion. Removing the bytes is a Firestore TTL policy on the dead_at
// property, which a deployment applies to the kind it owns with
//
//	gcloud firestore fields ttls update dead_at --collection-group=popcornweb_session --enable-ttl
//
// A kind without that policy keeps every session forever. The store is correct
// either way, and unbounded in size without it.
package firestore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shibukawa/popcornweb/database/firestore"
	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/tinybind-go/firestorebind"
	"github.com/shibukawa/tinygodriver/nosql/datastore"
)

// DeclaredKind is the entity kind. A kind is intrinsic to the type rather than
// a deployment fact, so nothing maps it onto another name.
const DeclaredKind = "popcornweb_session"

// Property names. Each is named once and read by both the encoder and the
// decoder, so the two cannot drift.
const (
	dataProperty            = "data"
	createdAtProperty       = "created_at"
	authenticatedAtProperty = "authenticated_at"
	lastSeenAtProperty      = "last_seen_at"
	expiresAtProperty       = "expires_at"
	idleExpiresAtProperty   = "idle_expires_at"
	deadAtProperty          = "dead_at"
	methodProperty          = "method"
	versionProperty         = "version"
)

// Options configures a store.
type Options struct {
	// Kind is the entity kind. Empty means DeclaredKind.
	Kind string
	// Now is injectable for tests.
	Now func() time.Time
}

// Store is the Firestore session backend. It implements session.RawStore, so it
// never sees the application payload type: the host adds that back with
// session.Typed.
type Store struct {
	kind    string
	nowFunc func() time.Time
}

var _ session.RawStore = (*Store)(nil)

// NewStore builds a store. It opens nothing: the client comes from the request
// context, installed by the database/firestore middleware.
func NewStore(options Options) *Store {
	kind := options.Kind
	if kind == "" {
		kind = DeclaredKind
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Store{kind: kind, nowFunc: now}
}

func (store *Store) now() time.Time { return store.nowFunc().UTC() }

// entity is one stored session.
//
// It is handwritten rather than generated: this package is the only reader and
// writer of the entity, so the drift a generated codec closes cannot occur, and
// generating for one internal type would put a code generation step in the
// framework's own build.
//
// Version carries the Datastore entity version a read returned, which is what
// makes a renewal conditional. It is not session.RawRecord.Version, which is a
// schema-invalidation stamp the framework owns; the two are never read for each
// other.
type entity struct {
	kind    string
	keyHash string
	record  session.RawRecord
	version int64
}

var (
	_ firestorebind.EntityEncoder = entity{}
	_ firestorebind.EntityDecoder = (*entity)(nil)
	_ firestorebind.Keyer         = entity{}
	_ firestorebind.Versioner     = entity{}
	_ firestorebind.Kinder        = entity{}
	_ firestorebind.Expirer       = entity{}
)

// Kind names the entity kind.
func (e entity) Kind() string {
	if e.kind == "" {
		return DeclaredKind
	}
	return e.kind
}

// EntityKey addresses one session. There is no ancestor, so every session is
// its own entity group and two sessions never contend with one another.
func (e entity) EntityKey() datastore.Key {
	return datastore.NameKey(e.Kind(), e.keyHash)
}

// EntityVersion reports the version a read returned. A zero version writes
// unconditionally, which is what a first Put wants.
func (e entity) EntityVersion() int64 { return e.version }

// ExpiryProperty names the property a TTL policy for this kind expires on.
//
// It changes no bytes: dead_at is written as an ordinary timestamp either way.
// It exists so the list a deployment is handed comes from the same declaration
// the codec reads, rather than from a list maintained beside it that a rename
// would silently leave behind.
func (e entity) ExpiryProperty() (string, bool) { return deadAtProperty, true }

// EncodeEntity renders the record.
//
// The payload is stored unindexed. Nothing filters on it, and an indexed value
// over 1500 bytes is silently stored without an index rather than refused, so a
// property that is sometimes indexed would be worse than one that never is.
func (e entity) EncodeEntity() datastore.Entity {
	out := datastore.NewEntity(e.EntityKey()).
		Set(dataProperty, datastore.Unindexed(datastore.Blob(e.record.Payload))).
		Set(createdAtProperty, datastore.Time(e.record.CreatedAt)).
		Set(lastSeenAtProperty, datastore.Time(e.record.LastSeenAt)).
		Set(expiresAtProperty, datastore.Time(e.record.ExpiresAt)).
		Set(deadAtProperty, datastore.Time(e.record.Deadline())).
		Set(methodProperty, datastore.String(e.record.Method)).
		Set(versionProperty, datastore.Int(e.record.Version))
	if !e.record.AuthenticatedAt.IsZero() {
		out = out.Set(authenticatedAtProperty, datastore.Time(e.record.AuthenticatedAt))
	}
	if !e.record.IdleExpiresAt.IsZero() {
		out = out.Set(idleExpiresAtProperty, datastore.Time(e.record.IdleExpiresAt))
	}
	return out
}

// DecodeEntity rebuilds a record. A malformed entity is a codec failure rather
// than a partially filled record.
func (e *entity) DecodeEntity(stored datastore.Entity) error {
	payload, err := readBytes(stored, dataProperty)
	if err != nil {
		return err
	}
	version, err := readInt(stored, versionProperty)
	if err != nil {
		return err
	}
	expiresAt, err := readTime(stored, expiresAtProperty)
	if err != nil {
		return err
	}
	if expiresAt.IsZero() {
		return fmt.Errorf("%w: %s", session.ErrCodec, expiresAtProperty)
	}
	method, _ := valueOf(stored, methodProperty).AsString()

	created, err := readTime(stored, createdAtProperty)
	if err != nil {
		return err
	}
	authenticated, err := readTime(stored, authenticatedAtProperty)
	if err != nil {
		return err
	}
	lastSeen, err := readTime(stored, lastSeenAtProperty)
	if err != nil {
		return err
	}
	idle, err := readTime(stored, idleExpiresAtProperty)
	if err != nil {
		return err
	}

	e.record = session.RawRecord{
		Payload:         payload,
		CreatedAt:       created,
		AuthenticatedAt: authenticated,
		LastSeenAt:      lastSeen,
		ExpiresAt:       expiresAt,
		IdleExpiresAt:   idle,
		Method:          method,
		Version:         int(version),
	}
	// The version a write conditions on, and the key, both come from the read
	// rather than from the properties: Datastore keeps identity beside the
	// entity, not among it.
	e.version = stored.Version
	if stored.Key != nil {
		e.kind = stored.Key.Kind()
		if path := stored.Key.Path; len(path) > 0 {
			e.keyHash = path[len(path)-1].Name
		}
	}
	return nil
}

// Put replaces one key. A commit replaces the whole entity atomically, and the
// entity carries no version, so nothing conditions the write.
func (store *Store) Put(ctx context.Context, keyHash string, record session.RawRecord) error {
	if !validKeyHash(keyHash) {
		return fmt.Errorf("%w: key syntax", session.ErrInvalidKey)
	}
	// The payload arrives already encoded: session.Typed owns the codec, so a
	// backend never sees the application type.
	if size := len(record.Payload); size > datastore.MaxEntityBytes {
		return fmt.Errorf("%w: session payload is %d bytes, over the Datastore entity limit of %d",
			session.ErrCodec, size, datastore.MaxEntityBytes)
	}
	handle, err := firestore.Handle(ctx)
	if err != nil {
		return storeError(err)
	}
	if _, err := handle.Store(ctx, entity{
		kind:    store.kind,
		keyHash: keyHash,
		record:  record,
	}); err != nil {
		return storeError(err)
	}
	return nil
}

// Get returns the stored record.
//
// The read is strongly consistent, which is the default in Datastore mode, so
// there is no false miss after a login rotation and nothing to retry.
func (store *Store) Get(ctx context.Context, keyHash string) (session.RawRecord, error) {
	if !validKeyHash(keyHash) {
		return session.RawRecord{}, fmt.Errorf("%w: key syntax", session.ErrInvalidKey)
	}
	loaded, err := store.load(ctx, keyHash)
	if err != nil {
		return session.RawRecord{}, err
	}
	// Expiry is decided here rather than trusted to a deletion: a TTL policy
	// removes an entity within about 24 hours of its deadline, so a store that
	// waited for it would serve expired sessions for a day.
	if !store.now().Before(loaded.record.Deadline()) {
		return session.RawRecord{}, session.ErrExpired
	}
	return loaded.record, nil
}

// Touch renews an existing record.
//
// There is no partial update on this wire and no predicate over a stored value,
// so a renewal reads, decides in Go, and writes the whole entity back with the
// version the read returned. That version carries the guarantee the DynamoDB
// backend gets from a condition: a record rotated or deleted in between is at a
// different version, and the write is refused rather than reviving it.
func (store *Store) Touch(ctx context.Context, keyHash string, lastSeenAt, idleExpiresAt time.Time) error {
	if !validKeyHash(keyHash) {
		return fmt.Errorf("%w: key syntax", session.ErrInvalidKey)
	}
	loaded, err := store.load(ctx, keyHash)
	if err != nil {
		return err
	}
	// A dead record is never renewed. The contract says a renewal must not
	// revive one, and this is the read half of that promise; the version
	// precondition is the write half.
	if !store.now().Before(loaded.record.Deadline()) {
		return session.ErrNotFound
	}

	loaded.record.LastSeenAt = lastSeenAt.UTC()
	if !idleExpiresAt.IsZero() {
		// The caller has already clamped the renewal to the absolute expiry, so
		// the new idle expiry is taken outright and Deadline recomputes dead_at
		// from it.
		loaded.record.IdleExpiresAt = idleExpiresAt.UTC()
	}

	handle, err := firestore.Handle(ctx)
	if err != nil {
		return storeError(err)
	}
	if _, err := handle.Store(ctx, *loaded); err != nil {
		if errors.Is(err, datastore.ErrFailedPrecondition) {
			// The entity moved under the read: rotated, deleted, or renewed by
			// another request. Either way the contract says this renewal must
			// not recreate it.
			return session.ErrNotFound
		}
		return storeError(err)
	}
	return nil
}

// Delete removes one record. A delete of an absent key succeeds, which is the
// idempotence the contract asks for.
func (store *Store) Delete(ctx context.Context, keyHash string) error {
	if !validKeyHash(keyHash) {
		return fmt.Errorf("%w: key syntax", session.ErrInvalidKey)
	}
	handle, err := firestore.Handle(ctx)
	if err != nil {
		return storeError(err)
	}
	if err := handle.Remove(ctx, entity{kind: store.kind, keyHash: keyHash}); err != nil {
		return storeError(err)
	}
	return nil
}

// load reads one entity and reports a miss as the contract's not-found.
func (store *Store) load(ctx context.Context, keyHash string) (*entity, error) {
	key := datastore.NameKey(store.kind, keyHash)
	handle, err := firestore.Handle(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	loaded, err := handle.Load[entity](ctx, key)
	switch {
	case errors.Is(err, datastore.ErrNoSuchEntity):
		return nil, session.ErrNotFound
	case err != nil:
		return nil, storeError(err)
	}
	loaded.kind = store.kind
	loaded.keyHash = keyHash
	return &loaded, nil
}

// valueOf returns a property, or the zero value when it is absent. An absent
// property and a null one are different things to Datastore, and both decode to
// nothing here.
func valueOf(stored datastore.Entity, name string) datastore.Value {
	value, _ := stored.Get(name)
	return value
}

func readBytes(stored datastore.Entity, name string) ([]byte, error) {
	raw, held := valueOf(stored, name).AsBytes()
	if !held {
		return nil, fmt.Errorf("%w: %s", session.ErrCodec, name)
	}
	return raw, nil
}

func readInt(stored datastore.Entity, name string) (int64, error) {
	number, held := valueOf(stored, name).AsInt()
	if !held {
		return 0, fmt.Errorf("%w: %s", session.ErrCodec, name)
	}
	return number, nil
}

// readTime reads an optional timestamp. An absent property is the zero time,
// and a property of the wrong kind is a codec failure rather than one.
func readTime(stored datastore.Entity, name string) (time.Time, error) {
	value, present := stored.Get(name)
	if !present || value.IsNull() {
		return time.Time{}, nil
	}
	at, held := value.AsTime()
	if !held {
		return time.Time{}, fmt.Errorf("%w: %s", session.ErrCodec, name)
	}
	return at.UTC(), nil
}

// storeError maps a driver or binding failure onto the contract error while
// keeping the driver sentinel reachable through errors.Is.
func storeError(err error) error {
	if errors.Is(err, firestorebind.ErrNoClient) {
		return fmt.Errorf(
			"%w: no Datastore client in context; import database/firestore and enable middleware.firestore",
			session.ErrUnavailable)
	}
	return fmt.Errorf("%w: %w", session.ErrUnavailable, err)
}

// validKeyHash reports whether value has the syntax of a store key. Rejecting
// foreign syntax keeps a malformed cookie away from the server, the same guard
// every other backend applies before a key reaches storage.
func validKeyHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for index := range len(value) {
		c := value[index]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
