package firestore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/plugin/auth"
	"github.com/shibukawa/tinybind-go/firestorebind"
	"github.com/shibukawa/tinygodriver/nosql/datastore"
)

// Credential property names, each named once so the encoder and the decoder
// cannot drift.
const (
	credentialAccountProperty = "account_id"
	userHandleProperty        = "user_handle"
	publicKeyProperty         = "public_key"
	publicKeyXProperty        = "public_key_x"
	publicKeyYProperty        = "public_key_y"
	algorithmProperty         = "algorithm"
	signCountProperty         = "sign_count"
	backupEligibleProperty    = "backup_eligible"
	backupStateProperty       = "backup_state"
	transportsProperty        = "transports"
	labelProperty             = "label"
	credentialCreatedProperty = "created_at"
	lastUsedAtProperty        = "last_used_at"
)

// maxLabelBytes bounds the one credential field an application controls, so a
// long label cannot be the reason a record crosses the entity limit.
const maxLabelBytes = 512

// credentialEntity is one stored passkey credential.
//
// A login resolves a credential ID, so that is the key. Listing an account's
// credentials is the opposite question, and it is a plain equality filter on
// account_id: single-property indexes are automatic here, so unlike the
// DynamoDB store there is no secondary index to declare at creation and no
// table to recreate for want of one.
type credentialEntity struct {
	credential auth.Credential
	version    int64
}

var (
	_ firestorebind.EntityEncoder = credentialEntity{}
	_ firestorebind.EntityDecoder = (*credentialEntity)(nil)
	_ firestorebind.Keyer         = credentialEntity{}
	_ firestorebind.Versioner     = credentialEntity{}
	_ firestorebind.Kinder        = credentialEntity{}
)

func (credentialEntity) Kind() string { return DeclaredCredentialKind }

func (e credentialEntity) EntityKey() datastore.Key {
	return datastore.NameKey(DeclaredCredentialKind, keyName(e.credential.CredentialID))
}

func (e credentialEntity) EntityVersion() int64 { return e.version }

// EncodeEntity renders the credential.
//
// account_id is the only indexed property here, because the listing filters on
// it. Every blob is unindexed: nothing filters on a key or a handle, and an
// indexed value over 1500 bytes is silently stored without an index rather than
// refused, so a property that is sometimes indexed would be worse than one that
// never is.
func (e credentialEntity) EncodeEntity() datastore.Entity {
	c := e.credential
	out := datastore.NewEntity(e.EntityKey()).
		Set(credentialAccountProperty, datastore.String(c.AccountID)).
		Set(userHandleProperty, datastore.Unindexed(datastore.Blob(c.UserHandle))).
		Set(publicKeyProperty, datastore.Unindexed(datastore.Blob(c.PublicKey))).
		Set(publicKeyXProperty, datastore.Unindexed(datastore.Blob(c.PublicKeyX))).
		Set(publicKeyYProperty, datastore.Unindexed(datastore.Blob(c.PublicKeyY))).
		Set(algorithmProperty, datastore.Unindexed(datastore.Int(c.Algorithm))).
		Set(signCountProperty, datastore.Unindexed(datastore.Int(c.SignCount))).
		Set(backupEligibleProperty, datastore.Unindexed(datastore.Bool(c.BackupEligible))).
		Set(backupStateProperty, datastore.Unindexed(datastore.Bool(c.BackupState))).
		Set(transportsProperty, datastore.Unindexed(datastore.String(strings.Join(c.Transports, ",")))).
		Set(labelProperty, datastore.Unindexed(datastore.String(c.Label))).
		Set(credentialCreatedProperty, datastore.Unindexed(datastore.Time(c.CreatedAt)))
	if !c.LastUsedAt.IsZero() {
		out = out.Set(lastUsedAtProperty, datastore.Unindexed(datastore.Time(c.LastUsedAt)))
	}
	return out
}

func (e *credentialEntity) DecodeEntity(stored datastore.Entity) error {
	e.credential = auth.Credential{
		AccountID:      readString(stored, credentialAccountProperty),
		UserHandle:     readBytes(stored, userHandleProperty),
		PublicKey:      readBytes(stored, publicKeyProperty),
		PublicKeyX:     readBytes(stored, publicKeyXProperty),
		PublicKeyY:     readBytes(stored, publicKeyYProperty),
		Algorithm:      int(readInt(stored, algorithmProperty)),
		SignCount:      uint32(readInt(stored, signCountProperty)),
		BackupEligible: readBool(stored, backupEligibleProperty),
		BackupState:    readBool(stored, backupStateProperty),
		Label:          readString(stored, labelProperty),
		CreatedAt:      readTime(stored, credentialCreatedProperty),
		LastUsedAt:     readTime(stored, lastUsedAtProperty),
	}
	if joined := readString(stored, transportsProperty); joined != "" {
		e.credential.Transports = strings.Split(joined, ",")
	}
	// The credential ID comes back from the key rather than from a property:
	// Datastore keeps identity beside the entity, and storing it twice would
	// let the two copies drift.
	if stored.Key != nil {
		if path := stored.Key.Path; len(path) > 0 {
			raw, err := decodeKeyName(path[len(path)-1].Name)
			if err != nil {
				return err
			}
			e.credential.CredentialID = raw
		}
	}
	e.version = stored.Version
	return nil
}

// Credentials is the Firestore passkey credential store.
type Credentials struct {
	now func() time.Time
}

var (
	_ auth.CredentialStore      = (*Credentials)(nil)
	_ auth.FirstEnrollmentStore = (*Credentials)(nil)
)

// CredentialOptions configures a credential store.
type CredentialOptions struct {
	// Now is injectable for tests.
	Now func() time.Time
}

// NewCredentials builds the store. It opens nothing: the client comes from the
// request context, installed by the database/firestore middleware.
func NewCredentials(options CredentialOptions) *Credentials {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Credentials{now: now}
}

// Find reads one credential by ID.
//
// The read is strongly consistent, which is the default here, so a credential
// enrolled moments ago is findable on the next login without asking for
// anything.
func (s *Credentials) Find(ctx context.Context, credentialID []byte) (auth.Credential, error) {
	if len(credentialID) == 0 {
		return auth.Credential{}, auth.ErrUnknownCredential
	}
	loaded, err := firestorebind.Load[credentialEntity](
		ctx, datastore.NameKey(DeclaredCredentialKind, keyName(credentialID)))
	switch {
	case errors.Is(err, datastore.ErrNoSuchEntity):
		return auth.Credential{}, auth.ErrUnknownCredential
	case err != nil:
		return auth.Credential{}, unavailable("read credential", err)
	}
	return loaded.credential, nil
}

// ListByAccount supplies excludeCredentials and allowCredentials.
//
// The query filters on one property and orders on none. An order on a second
// property is exactly what would demand a composite index, and nothing here
// applies one — a query needing an index compiles and fails at run time, so the
// listing stays inside what the automatic indexes cover.
func (s *Credentials) ListByAccount(ctx context.Context, accountID string) ([]auth.Credential, error) {
	if accountID == "" {
		return nil, nil
	}
	query := datastore.NewQuery(DeclaredCredentialKind).
		Filter(credentialAccountProperty, datastore.Equal, datastore.String(accountID))

	var credentials []auth.Credential
	for {
		page, err := firestorebind.QueryPage[credentialEntity](ctx, query)
		if err != nil {
			return nil, unavailable("list credentials", err)
		}
		for _, entity := range page.Values {
			credentials = append(credentials, entity.credential)
		}
		if !page.HasMore() {
			return credentials, nil
		}
		query = query.Start(page.EndCursor)
	}
}

// Save persists a new credential.
//
// Insert makes it an insert: a retried request that already stored the
// credential fails rather than writing a second one.
//
// A non-nil within runs before the write rather than after it. The framework
// uses SaveFirstCredential for the enrollment that needs the two together, so
// this path exists for a caller that bundles its own; running the callback
// first keeps the one invariant that matters either way, which is that no
// credential is stored while the bootstrap credential authorizing it is still
// unspent.
func (s *Credentials) Save(ctx context.Context, credential auth.Credential, within func(context.Context) error) error {
	if within != nil {
		if err := within(ctx); err != nil {
			return err
		}
	}
	entity, err := credentialFor(credential)
	if err != nil {
		return err
	}
	if _, err := firestorebind.Insert(ctx, entity); err != nil {
		if errors.Is(err, datastore.ErrAlreadyExists) {
			return errors.New("auth: credential already exists")
		}
		return unavailable("save credential", err)
	}
	return nil
}

// SaveFirstCredential applies a passkey-only registration as one commit.
//
// The bootstrap credential is consumed and the credential inserted inside one
// transaction, so a credential can never be stored without the secret that
// authorized it being spent, and a spend can never happen without the
// credential. The activation callback stays outside: a contention abort re-runs
// the closure, and an activation that ran twice is a side effect a transaction
// cannot bound.
//
// So one interruption remains reachable — the pair committed and the account
// still provisional — and it cannot create a session, because a provisional
// account cannot. It is resolved by retrying the activation or by an
// administrator, and it is a defined outcome rather than a corrupt state.
func (s *Credentials) SaveFirstCredential(ctx context.Context, credential auth.Credential, spend, activate func(context.Context) error) error {
	entity, err := credentialFor(credential)
	if err != nil {
		return err
	}
	err = firestorebind.Run(ctx, func(tx *firestorebind.Tx) error {
		if spend != nil {
			// The transaction reaches the bootstrap store through the context
			// the framework already threads through this callback, so both
			// writes land in one commit.
			if err := spend(withTx(ctx, tx)); err != nil {
				return err
			}
		}
		tx.Insert(entity)
		return nil
	})
	switch {
	case errors.Is(err, datastore.ErrAlreadyExists):
		return errors.New("auth: credential already exists")
	case errors.Is(err, auth.ErrUnknownBootstrap):
		return auth.ErrUnknownBootstrap
	case err != nil:
		return unavailable("save first credential", err)
	}
	if activate != nil {
		return activate(ctx)
	}
	return nil
}

// UpdateOnAssertion persists the accepted counter and backup state.
//
// The comparison is a predicate over a stored value, and nothing on this wire
// evaluates one, so it runs in Go between a read and a commit that share a
// snapshot. A counter that does not move forward is a replayed or cloned
// authenticator, so the store refuses it rather than leaving the caller to
// notice. A zero incoming count means the authenticator keeps none, which is
// tolerated by comparing only above zero.
func (s *Credentials) UpdateOnAssertion(ctx context.Context, credentialID []byte, signCount uint32, backupState bool, usedAt time.Time) error {
	if len(credentialID) == 0 {
		return auth.ErrUnknownCredential
	}
	key := datastore.NameKey(DeclaredCredentialKind, keyName(credentialID))
	err := firestorebind.Run(ctx, func(tx *firestorebind.Tx) error {
		loaded, err := tx.Load[credentialEntity](ctx, key)
		if err != nil {
			return err
		}
		if signCount > 0 && loaded.credential.SignCount >= signCount {
			return auth.ErrUnknownCredential
		}
		loaded.credential.SignCount = signCount
		loaded.credential.BackupState = backupState
		loaded.credential.LastUsedAt = usedAt.UTC()
		tx.Store(loaded)
		return nil
	})
	switch {
	case errors.Is(err, datastore.ErrNoSuchEntity), errors.Is(err, auth.ErrUnknownCredential):
		// Either the credential is gone or its counter did not advance. Both
		// fail the ceremony closed; neither is downgraded to a warning, because
		// a counter that silently fails to persist is exactly what a cloned
		// authenticator needs.
		return auth.ErrUnknownCredential
	case err != nil:
		return unavailable("update credential", err)
	}
	return nil
}

// Delete removes one credential of an account.
//
// The account is checked inside the transaction, which is what keeps the check
// the relational statement makes in its WHERE clause: one account cannot delete
// another's credential by ID.
func (s *Credentials) Delete(ctx context.Context, accountID string, credentialID []byte) error {
	if accountID == "" || len(credentialID) == 0 {
		return auth.ErrUnknownCredential
	}
	key := datastore.NameKey(DeclaredCredentialKind, keyName(credentialID))
	err := firestorebind.Run(ctx, func(tx *firestorebind.Tx) error {
		loaded, err := tx.Load[credentialEntity](ctx, key)
		if err != nil {
			return err
		}
		if loaded.credential.AccountID != accountID {
			return auth.ErrUnknownCredential
		}
		tx.Remove(loaded)
		return nil
	})
	switch {
	case errors.Is(err, datastore.ErrNoSuchEntity), errors.Is(err, auth.ErrUnknownCredential):
		return auth.ErrUnknownCredential
	case err != nil:
		return unavailable("delete credential", err)
	}
	return nil
}

// credentialFor validates a credential and renders it, before anything is sent.
func credentialFor(credential auth.Credential) (credentialEntity, error) {
	if len(credential.CredentialID) == 0 || credential.AccountID == "" {
		return credentialEntity{}, errors.New("auth: credential needs an ID and an account")
	}
	if len(credential.Label) > maxLabelBytes {
		return credentialEntity{}, fmt.Errorf("auth: credential label is %d bytes, over the limit of %d",
			len(credential.Label), maxLabelBytes)
	}
	entity := credentialEntity{credential: credential}
	if size := entitySize(entity); size > datastore.MaxEntityBytes {
		return credentialEntity{}, fmt.Errorf("auth: credential is %d bytes, over the Datastore entity limit of %d",
			size, datastore.MaxEntityBytes)
	}
	return entity, nil
}
