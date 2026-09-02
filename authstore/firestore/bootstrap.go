package firestore

import (
	"context"
	"errors"
	"time"

	"github.com/shibukawa/popcornweb/plugin/auth"
	"github.com/shibukawa/tinybind-go/firestorebind"
	"github.com/shibukawa/tinygodriver/nosql/datastore"
)

// Bootstrap property names, each named once so the encoder and the decoder
// cannot drift.
const (
	bootstrapAccountProperty  = "account_id"
	secretDigestProperty      = "secret_digest"
	purposeProperty           = "purpose"
	issuedAtProperty          = "issued_at"
	bootstrapExpiresProperty  = "expires_at"
	attemptsRemainingProperty = "attempts_remaining"
	consumedAtProperty        = "consumed_at"
)

// bootstrapEntity is one issued bootstrap credential.
//
// A redemption starts from the login ID an operator handed out, so that is the
// key and there is no second question to index for. expires_at is the property
// a TTL policy expires on; nothing here applies or verifies one, and a kind
// without a policy retains every spent credential forever.
type bootstrapEntity struct {
	credential auth.BootstrapCredential
	version    int64
}

var (
	_ firestorebind.EntityEncoder = bootstrapEntity{}
	_ firestorebind.EntityDecoder = (*bootstrapEntity)(nil)
	_ firestorebind.Keyer         = bootstrapEntity{}
	_ firestorebind.Versioner     = bootstrapEntity{}
	_ firestorebind.Kinder        = bootstrapEntity{}
	_ firestorebind.Expirer       = bootstrapEntity{}
)

func (bootstrapEntity) Kind() string { return DeclaredBootstrapKind }

func (e bootstrapEntity) EntityKey() datastore.Key {
	return datastore.NameKey(DeclaredBootstrapKind, e.credential.LoginID)
}

func (e bootstrapEntity) EntityVersion() int64 { return e.version }

// ExpiryProperty names the property a TTL policy for this kind expires on. It
// changes no bytes; it is the declaration the published list is derived from.
func (bootstrapEntity) ExpiryProperty() (string, bool) { return bootstrapExpiresProperty, true }

// EncodeEntity renders the credential. Nothing filters on any of it — a
// redemption arrives with the login ID, which is the key — so every property is
// unindexed, and the secret digest doubly so: an indexed value over 1500 bytes
// is silently stored without an index rather than refused.
func (e bootstrapEntity) EncodeEntity() datastore.Entity {
	c := e.credential
	out := datastore.NewEntity(e.EntityKey()).
		Set(bootstrapAccountProperty, datastore.Unindexed(datastore.String(c.AccountID))).
		Set(secretDigestProperty, datastore.Unindexed(datastore.Blob(c.SecretDigest))).
		Set(purposeProperty, datastore.Unindexed(datastore.String(c.Purpose))).
		Set(issuedAtProperty, datastore.Unindexed(datastore.Time(c.IssuedAt))).
		Set(bootstrapExpiresProperty, datastore.Time(c.ExpiresAt)).
		Set(attemptsRemainingProperty, datastore.Unindexed(datastore.Int(c.AttemptsRemaining)))
	if !c.ConsumedAt.IsZero() {
		out = out.Set(consumedAtProperty, datastore.Unindexed(datastore.Time(c.ConsumedAt)))
	}
	return out
}

func (e *bootstrapEntity) DecodeEntity(stored datastore.Entity) error {
	e.credential = auth.BootstrapCredential{
		AccountID:         readString(stored, bootstrapAccountProperty),
		SecretDigest:      readBytes(stored, secretDigestProperty),
		Purpose:           readString(stored, purposeProperty),
		IssuedAt:          readTime(stored, issuedAtProperty),
		ExpiresAt:         readTime(stored, bootstrapExpiresProperty),
		AttemptsRemaining: int(readInt(stored, attemptsRemainingProperty)),
		ConsumedAt:        readTime(stored, consumedAtProperty),
	}
	if stored.Key != nil {
		if path := stored.Key.Path; len(path) > 0 {
			e.credential.LoginID = path[len(path)-1].Name
		}
	}
	e.version = stored.Version
	return nil
}

// Bootstrap is the Firestore store of issued bootstrap credentials.
type Bootstrap struct{}

var _ auth.BootstrapStore = Bootstrap{}

// NewBootstrap builds the store. It opens nothing: the client comes from the
// request context, installed by the database/firestore middleware.
func NewBootstrap() Bootstrap { return Bootstrap{} }

// Issue records a new credential. Insert refuses a login ID that is already
// live rather than overwriting it, so re-issuing to the same ID cannot silently
// extend someone else's window.
func (Bootstrap) Issue(ctx context.Context, credential auth.BootstrapCredential) error {
	if credential.LoginID == "" || credential.AccountID == "" || len(credential.SecretDigest) == 0 {
		return errors.New("auth: bootstrap credential needs a login ID, an account, and a secret digest")
	}
	entity := bootstrapEntity{credential: credential}
	if size := entitySize(entity); size > datastore.MaxEntityBytes {
		return errors.New("auth: bootstrap credential is over the Datastore entity limit")
	}
	if _, err := firestorebind.Insert(ctx, entity); err != nil {
		if errors.Is(err, datastore.ErrAlreadyExists) {
			return errors.New("auth: bootstrap login ID is already issued")
		}
		return unavailable("issue bootstrap credential", err)
	}
	return nil
}

// Find returns an unconsumed credential.
//
// An unknown login ID and a consumed one return the same error, so a caller
// cannot tell them apart and enumerate accounts. The read is strongly
// consistent, which is the default here, so a credential issued moments ago is
// redeemable immediately.
func (Bootstrap) Find(ctx context.Context, loginID string) (auth.BootstrapCredential, error) {
	if loginID == "" {
		return auth.BootstrapCredential{}, auth.ErrUnknownBootstrap
	}
	loaded, err := firestorebind.Load[bootstrapEntity](
		ctx, datastore.NameKey(DeclaredBootstrapKind, loginID))
	switch {
	case errors.Is(err, datastore.ErrNoSuchEntity):
		return auth.BootstrapCredential{}, auth.ErrUnknownBootstrap
	case err != nil:
		return auth.BootstrapCredential{}, unavailable("read bootstrap credential", err)
	}
	if !loaded.credential.ConsumedAt.IsZero() {
		return auth.BootstrapCredential{}, auth.ErrUnknownBootstrap
	}
	credential := loaded.credential
	// The consumed timestamp is not handed back: it is always zero here, and a
	// caller reading it would be reading a field this store filters on.
	credential.ConsumedAt = time.Time{}
	return credential, nil
}

// RecordAttempt spends one attempt and reports what is left.
//
// It is a transaction because the decrement is a predicate over a stored value
// and there is no server-side arithmetic on this wire — property
// transformations are excluded by the driver, as the non-idempotent-retry
// hazard they are. The transaction is what makes the contract hold anyway: two
// parallel guesses cannot both spend the last attempt, because the loser aborts
// and re-runs against the record the winner wrote.
func (Bootstrap) RecordAttempt(ctx context.Context, loginID string) (int, error) {
	if loginID == "" {
		return 0, auth.ErrUnknownBootstrap
	}
	key := datastore.NameKey(DeclaredBootstrapKind, loginID)
	remaining := 0
	err := firestorebind.Run(ctx, func(tx *firestorebind.Tx) error {
		loaded, err := tx.Load[bootstrapEntity](ctx, key)
		if err != nil {
			return err
		}
		// An exhausted budget and a consumed credential are the same error as
		// an unknown login ID, per the contract, so a caller cannot enumerate.
		if !loaded.credential.ConsumedAt.IsZero() || loaded.credential.AttemptsRemaining <= 0 {
			return auth.ErrUnknownBootstrap
		}
		loaded.credential.AttemptsRemaining--
		remaining = loaded.credential.AttemptsRemaining
		tx.Store(loaded)
		return nil
	})
	switch {
	case errors.Is(err, datastore.ErrNoSuchEntity), errors.Is(err, auth.ErrUnknownBootstrap):
		return 0, auth.ErrUnknownBootstrap
	case err != nil:
		return 0, unavailable("record bootstrap attempt", err)
	}
	return remaining, nil
}

// Consume marks the credential spent.
//
// When the context carries the transaction a first enrollment opened, the write
// joins it, so the credential insert and this spend are one commit and neither
// can happen without the other. Outside one it is its own transaction, which is
// what a recovery flow spending a credential on its own gets.
func (Bootstrap) Consume(ctx context.Context, loginID string, at time.Time) error {
	if loginID == "" {
		return auth.ErrUnknownBootstrap
	}
	key := datastore.NameKey(DeclaredBootstrapKind, loginID)

	spend := func(tx *firestorebind.Tx) error {
		loaded, err := tx.Load[bootstrapEntity](ctx, key)
		if err != nil {
			return err
		}
		if !loaded.credential.ConsumedAt.IsZero() {
			// A second redemption of one issued secret fails here, which is
			// what makes the enrollment sequence single-use.
			return auth.ErrUnknownBootstrap
		}
		loaded.credential.ConsumedAt = at.UTC()
		tx.Store(loaded)
		return nil
	}

	var err error
	if outer, joined := txFrom(ctx); joined {
		err = spend(outer)
	} else {
		err = firestorebind.Run(ctx, spend)
	}
	switch {
	case errors.Is(err, datastore.ErrNoSuchEntity), errors.Is(err, auth.ErrUnknownBootstrap):
		return auth.ErrUnknownBootstrap
	case err != nil:
		return unavailable("consume bootstrap credential", err)
	}
	return nil
}
