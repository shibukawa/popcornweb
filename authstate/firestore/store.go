// Package firestore stores single-use authentication ceremony state in
// Firestore in Datastore mode.
//
// It exists so a deployment on Google Cloud with no relational database can run
// the passkey, OAuth, and OIDC ceremonies. Importing it publishes the ceremony
// kind; the client itself belongs to database/firestore, which this package
// reads from the request context:
//
//	import _ "github.com/shibukawa/popcornweb/database/firestore"
//	import _ "github.com/shibukawa/popcornweb/authstate/firestore"
//
// Nothing here sweeps expired records. Take decides expiry from the stored
// deadline, so correctness never waits for a deletion; removing the bytes is a
// TTL policy on the expires_at property, which a deployment applies to the kind
// it owns:
//
//	gcloud firestore fields ttls update expires_at --collection-group=popcornweb_authstate --enable-ttl
//
// The sqlite adapter publishes Prune because a bounded DELETE is cheap there.
// Here the equivalent is a query over every ceremony record ever written, which
// costs more than the records it would remove.
package firestore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/authstate"
	"github.com/shibukawa/popcornweb/database/firestore"
	"github.com/shibukawa/tinybind-go/firestorebind"
	"github.com/shibukawa/tinygodriver/nosql/datastore"
)

// DeclaredKind is the entity kind. A kind is intrinsic to the type rather than
// a deployment fact, so nothing maps it onto another name.
const DeclaredKind = "popcornweb_authstate"

// Property names, each named once so the encoder and the decoder cannot drift.
const (
	payloadProperty   = "payload"
	expiresAtProperty = "expires_at"
)

const (
	defaultMaxKeyBytes   = 256
	defaultMaxValueBytes = 64 << 10
	hardMaxKeyBytes      = 4096
	hardMaxValueBytes    = 1 << 20
	maxNamespaceBytes    = 128
)

func init() {
	firestore.RegisterKind(record{})
}

// Options configures a store.
type Options struct {
	// Kind is the entity kind. Empty means DeclaredKind.
	Kind string
	// Namespace separates the ceremonies of one protocol from another's within
	// the one kind. It is required.
	//
	// It is part of the key rather than a Datastore namespace: a Datastore
	// namespace is a tenancy dimension the deployment owns, and borrowing it
	// for a protocol name would collide with the isolation the test runner and
	// a multi-tenant deployment use it for.
	Namespace string
	// Now is injectable for tests.
	Now           func() time.Time
	MaxKeyBytes   int
	MaxValueBytes int
}

// Store persists expiring, single-use authentication state in Firestore.
//
// It is a raw store: it works on already encoded payloads, so plugin/auth can
// open one for a ceremony type this package cannot name. NewStore puts the
// codec back on for a caller that has one.
type Store struct {
	kind          string
	namespace     string
	now           func() time.Time
	maxKeyBytes   int
	maxValueBytes int
}

var _ authstate.RawStore = (*Store)(nil)

// NewStore builds a typed store over NewRawStore, for a caller that owns a
// codec and wants the ordinary contract.
func NewStore[T any](codec authstate.Codec[T], options Options) (authstate.Store[T], error) {
	if codec == nil {
		return nil, authstate.ErrInvalidOptions
	}
	raw, err := NewRawStore(options)
	if err != nil {
		return nil, err
	}
	return authstate.Typed[T](raw, codec), nil
}

// NewRawStore builds the store. It opens nothing: the client comes from the
// request context, installed by the database/firestore middleware. That is why
// it takes no client argument, unlike the sqlite adapter which takes a pool.
func NewRawStore(options Options) (*Store, error) {
	if !validNamespace(options.Namespace) ||
		options.MaxKeyBytes < 0 || options.MaxKeyBytes > hardMaxKeyBytes ||
		options.MaxValueBytes < 0 || options.MaxValueBytes > hardMaxValueBytes {
		return nil, authstate.ErrInvalidOptions
	}
	kind := options.Kind
	if kind == "" {
		kind = DeclaredKind
	}
	maxKeyBytes := options.MaxKeyBytes
	if maxKeyBytes == 0 {
		maxKeyBytes = defaultMaxKeyBytes
	}
	maxValueBytes := options.MaxValueBytes
	if maxValueBytes == 0 {
		maxValueBytes = defaultMaxValueBytes
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Store{
		kind: kind, namespace: options.Namespace, now: now,
		maxKeyBytes: maxKeyBytes, maxValueBytes: maxValueBytes,
	}, nil
}

// record is one stored ceremony.
//
// The key joins the namespace and the correlation key rather than making the
// namespace an ancestor. Every ceremony of one protocol shares a namespace, and
// an ancestor path would put them in one entity group, whose writes serialize —
// so a login spike would contend with itself. What joining gives up is a cheap
// protocol-scoped listing, which nothing needs once there is no prune.
type record struct {
	kind      string
	key       string
	payload   []byte
	expiresAt time.Time
}

var (
	_ firestorebind.EntityEncoder = record{}
	_ firestorebind.EntityDecoder = (*record)(nil)
	_ firestorebind.Keyer         = record{}
	_ firestorebind.Kinder        = record{}
	_ firestorebind.Expirer       = record{}
)

func (r record) Kind() string {
	if r.kind == "" {
		return DeclaredKind
	}
	return r.kind
}

func (r record) EntityKey() datastore.Key { return datastore.NameKey(r.Kind(), r.key) }

// ExpiryProperty names the property a TTL policy for this kind expires on. It
// changes no bytes; it is the declaration the published list is derived from.
func (r record) ExpiryProperty() (string, bool) { return expiresAtProperty, true }

// EncodeEntity renders the record. The payload is unindexed: nothing filters on
// it, and an indexed value over 1500 bytes is silently stored without an index
// rather than refused.
func (r record) EncodeEntity() datastore.Entity {
	return datastore.NewEntity(r.EntityKey()).
		Set(payloadProperty, datastore.Unindexed(datastore.Blob(r.payload))).
		Set(expiresAtProperty, datastore.Time(r.expiresAt))
}

func (r *record) DecodeEntity(stored datastore.Entity) error {
	payload, held := valueOf(stored, payloadProperty).AsBytes()
	if !held {
		return fmt.Errorf("%w: malformed record", authstate.ErrCodec)
	}
	expiresAt, held := valueOf(stored, expiresAtProperty).AsTime()
	if !held || expiresAt.IsZero() {
		return fmt.Errorf("%w: malformed record", authstate.ErrCodec)
	}
	r.payload = payload
	r.expiresAt = expiresAt.UTC()
	if stored.Key != nil {
		r.kind = stored.Key.Kind()
		if path := stored.Key.Path; len(path) > 0 {
			r.key = path[len(path)-1].Name
		}
	}
	return nil
}

func valueOf(stored datastore.Entity, name string) datastore.Value {
	value, _ := stored.Get(name)
	return value
}

// entityKey joins the namespace and the correlation key. The separator is a
// character validNamespace forbids, so no namespace and key pair can collide
// with another.
func (s *Store) entityKey(key string) string { return s.namespace + ":" + key }

// Put stores a value that has never been stored under this key, or whose
// previous record has already expired.
//
// The uncontended case is one commit: Insert fails if the key is taken. Only an
// actual collision pays for the transaction, which is the one place a predicate
// over a stored value can run — there is no condition expression on this wire,
// so an expired record is replaced by reading it inside a transaction and
// deciding in Go.
func (s *Store) Put(ctx context.Context, key string, payload []byte, expiresAt time.Time) error {
	if !s.ready() || ctx == nil {
		return authstate.ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" || len(key) > s.maxKeyBytes {
		return authstate.ErrInvalidKey
	}
	now := s.now()
	if expiresAt.IsZero() || !expiresAt.After(now) || expiresAt.UnixMilli() <= 0 {
		return authstate.ErrInvalidExpiry
	}
	if len(payload) == 0 {
		return fmt.Errorf("%w: encode", authstate.ErrCodec)
	}
	if len(payload) > s.maxValueBytes {
		return authstate.ErrLimitExceeded
	}
	if len(payload) > datastore.MaxEntityBytes {
		return fmt.Errorf("%w: ceremony payload is %d bytes, over the Datastore entity limit of %d",
			authstate.ErrLimitExceeded, len(payload), datastore.MaxEntityBytes)
	}

	fresh := record{kind: s.kind, key: s.entityKey(key), payload: payload, expiresAt: expiresAt.UTC()}
	handle, err := firestore.Handle(ctx)
	if err != nil {
		return unavailable(ctx, err)
	}
	_, err = handle.Insert(ctx, fresh)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, datastore.ErrAlreadyExists):
		return unavailable(ctx, err)
	}

	// A key collision. Replace the record only when the one holding the key has
	// already expired, which needs a read and a write that share a snapshot.
	err = handle.Run(ctx, func(tx *firestorebind.Tx) error {
		existing, err := tx.Load[record](ctx, fresh.EntityKey())
		switch {
		case errors.Is(err, datastore.ErrNoSuchEntity):
			// It expired and something removed it in between. The key is free.
			tx.Insert(fresh)
			return nil
		case err != nil:
			return err
		}
		if existing.expiresAt.After(s.now()) {
			return authstate.ErrAlreadyExists
		}
		tx.Store(fresh)
		return nil
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authstate.ErrAlreadyExists):
		return authstate.ErrAlreadyExists
	case errors.Is(err, datastore.ErrAlreadyExists):
		// Another caller took the freed key first, which is the same answer.
		return authstate.ErrAlreadyExists
	default:
		return unavailable(ctx, err)
	}
}

// Take removes a record and returns what it removed.
//
// No commit returns a prior entity, so this is a transaction: read, queue the
// delete, commit. Two concurrent takes cannot both commit, and the loser
// re-runs, finds nothing, and reports the miss — which is the single-use
// guarantee the contract asks for.
//
// A malformed, expired, or undecodable record stays consumed: it was
// single-use, and handing it back would be worse than losing it.
func (s *Store) Take(ctx context.Context, key string) ([]byte, error) {
	if !s.ready() || ctx == nil {
		return nil, authstate.ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if key == "" || len(key) > s.maxKeyBytes {
		return nil, authstate.ErrInvalidKey
	}

	entityKey := datastore.NameKey(s.kind, s.entityKey(key))
	handle, err := firestore.Handle(ctx)
	if err != nil {
		return nil, unavailable(ctx, err)
	}
	var taken record
	err = handle.Run(ctx, func(tx *firestorebind.Tx) error {
		// The closure reads and queues a delete and does nothing else, so the
		// re-run a contention abort causes is safe by construction rather than
		// by care.
		loaded, err := tx.Load[record](ctx, entityKey)
		if err != nil {
			return err
		}
		taken = loaded
		tx.Remove(record{kind: s.kind, key: s.entityKey(key)})
		return nil
	})
	switch {
	case errors.Is(err, datastore.ErrNoSuchEntity):
		return nil, authstate.ErrNotFound
	case errors.Is(err, authstate.ErrCodec):
		return nil, err
	case err != nil:
		return nil, unavailable(ctx, err)
	}

	if !taken.expiresAt.After(s.now()) {
		return nil, authstate.ErrExpired
	}
	if len(taken.payload) == 0 {
		return nil, fmt.Errorf("%w: malformed record", authstate.ErrCodec)
	}
	if len(taken.payload) > s.maxValueBytes {
		return nil, authstate.ErrLimitExceeded
	}
	return taken.payload, nil
}

func (s *Store) ready() bool {
	return s != nil && s.now != nil && s.kind != "" && s.namespace != "" &&
		s.maxKeyBytes > 0 && s.maxValueBytes > 0
}

func validNamespace(value string) bool {
	if value == "" || len(value) > maxNamespaceBytes || strings.Contains(value, ":") {
		return false
	}
	for i := range len(value) {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// unavailable maps a driver failure onto the contract error while keeping the
// driver sentinel reachable through errors.Is. A cancelled context is reported
// as itself rather than as a backend failure.
func unavailable(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, firestorebind.ErrNoClient) {
		return fmt.Errorf(
			"%w: no Datastore client in context; import database/firestore and enable middleware.firestore",
			authstate.ErrUnavailable)
	}
	return fmt.Errorf("%w: %w", authstate.ErrUnavailable, err)
}
