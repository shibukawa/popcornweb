package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/authstate"
)

// memoryState is the smallest RawStore that satisfies the contract, so a
// backend test asserts wiring rather than storage.
type memoryState struct {
	records map[string][]byte
}

func (s *memoryState) Put(_ context.Context, key string, payload []byte, _ time.Time) error {
	if s.records == nil {
		s.records = map[string][]byte{}
	}
	s.records[key] = payload
	return nil
}

func (s *memoryState) Take(_ context.Context, key string) ([]byte, error) {
	payload, present := s.records[key]
	if !present {
		return nil, authstate.ErrNotFound
	}
	delete(s.records, key)
	return payload, nil
}

func registerTestBackend(t *testing.T, name string, backend Backend) {
	t.Helper()
	RegisterBackend(name, func(context.Context, Config, Resources) (Backend, error) {
		return backend, nil
	})
	t.Cleanup(func() {
		backendState.Lock()
		defer backendState.Unlock()
		delete(backendState.factories, name)
	})
}

func TestTheDefaultBackendIsRelational(t *testing.T) {
	if (Config{}).backendName() != BackendRDB {
		t.Fatalf("unset backend = %q", (Config{}).backendName())
	}
	if _, linked := backendFactory(BackendRDB); !linked {
		t.Fatal("the relational backend is not registered")
	}
}

// A project with no relational middleware must hear it from the backend that
// needs one, not from the package.
func TestTheRelationalBackendIsTheOneThatNeedsADatabase(t *testing.T) {
	_, err := openBackend(context.Background(), Config{Backend: BackendRDB}, Resources{})
	if !errors.Is(err, errAuthNeedsRDB) {
		t.Fatalf("relational backend without a database = %v", err)
	}
	if !strings.Contains(err.Error(), "middleware.rdb.enabled") {
		t.Fatalf("the error does not name what to enable: %v", err)
	}

	// Another backend reaches none of that.
	registerTestBackend(t, "test-storeless", Backend{
		OpenState: func(context.Context, string) (authstate.RawStore, error) {
			return &memoryState{}, nil
		},
	})
	if _, err := openBackend(context.Background(), Config{Backend: "test-storeless"}, Resources{}); err != nil {
		t.Fatalf("a backend needing no database = %v", err)
	}
}

func TestAnUnlinkedBackendIsRefusedAtValidation(t *testing.T) {
	config := Config{
		Enabled: true, Backend: "nowhere", Mode: ModeOIDCOnly,
		LoginPath: "/auth/login", CallbackPath: "/auth/callback",
		LogoutPath: "/auth/logout", PostLoginPath: "/",
	}
	err := config.validate()
	if err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("unlinked backend = %v", err)
	}
	// The message names what is available, because the fix is an import line.
	if !strings.Contains(err.Error(), BackendRDB) {
		t.Fatalf("the error does not list the linked backends: %v", err)
	}
}

// auth.backend is a registered name rather than an enum. It was once bound as
// the enum "rdb,dynamo", which refused the name of a backend the application
// registered itself — and the framework's own firestore backend with it —
// before validation ever asked what was linked.
func TestAnApplicationRegisteredBackendIsSelectable(t *testing.T) {
	registerTestBackend(t, "test-app-owned", Backend{})
	config := Config{
		Enabled: true, Backend: "test-app-owned", Mode: ModeOIDCOnly,
		LoginPath: "/auth/login", CallbackPath: "/auth/callback",
		LogoutPath: "/auth/logout", PostLoginPath: "/",
	}
	if err := config.validateShape(); err != nil && strings.Contains(err.Error(), "auth.backend") {
		t.Fatalf("an application-registered backend was refused: %v", err)
	}
}

// A store that decides expiry on read registers no pruner, and the sweep is
// simply empty rather than special-cased.
func TestAStoreThatNeedsNoSweepRegistersNoPruner(t *testing.T) {
	instance := &runtime{backend: Backend{
		OpenState: func(context.Context, string) (authstate.RawStore, error) {
			return &memoryState{}, nil
		},
	}}
	if _, err := openState(context.Background(), instance, stateNamespace, testCodec{}); err != nil {
		t.Fatalf("open = %v", err)
	}
	if len(instance.pruners) != 0 {
		t.Fatalf("a store with no Prune registered %d pruners", len(instance.pruners))
	}
}

type testCodec struct{}

func (testCodec) Encode(value string) ([]byte, error)   { return []byte(value), nil }
func (testCodec) Decode(encoded []byte) (string, error) { return string(encoded), nil }

// The codec is put back on above the raw store, so a caller still gets the
// typed contract whichever backend supplied the storage.
func TestOpenStatePutsTheCodecBackOn(t *testing.T) {
	instance := &runtime{backend: Backend{
		OpenState: func(context.Context, string) (authstate.RawStore, error) {
			return &memoryState{}, nil
		},
	}}
	store, err := openState(context.Background(), instance, stateNamespace, testCodec{})
	if err != nil {
		t.Fatalf("open = %v", err)
	}
	ctx := context.Background()
	if err := store.Put(ctx, "key", "value", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("put = %v", err)
	}
	value, err := store.Take(ctx, "key")
	if err != nil || value != "value" {
		t.Fatalf("take = %q err = %v", value, err)
	}
	if _, err := store.Take(ctx, "key"); !errors.Is(err, authstate.ErrNotFound) {
		t.Fatalf("second take = %v", err)
	}
}

// jwt_only reads the allowlist and the revocation list directly rather than
// through the backend, and neither has a non-relational implementation. The
// pair is refused rather than silently ignoring the key.
func TestJWTOnlyRefusesANonRelationalBackend(t *testing.T) {
	registerTestBackend(t, "test-elsewhere", Backend{})

	// Revocation is on in the valid configuration, which is enough on its own
	// to reach the relational store.
	revoking := validJWTConfig()
	revoking.Backend = "test-elsewhere"
	if err := revoking.validate(); err == nil || !strings.Contains(err.Error(), BackendRDB) {
		t.Fatalf("revocation on a non-relational backend = %v", err)
	}

	// The registered admission mode reaches the other relational table.
	registered := validJWTConfig()
	registered.Backend = "test-elsewhere"
	registered.JWT.Revocation.Mode = RevocationOff
	registered.JWT.Admission = AdmissionRegistered
	if err := registered.validate(); err == nil || !strings.Contains(err.Error(), BackendRDB) {
		t.Fatalf("registered admission on a non-relational backend = %v", err)
	}

	// A deployment reading neither table has nothing relational left, so the
	// key is not in its way.
	neither := validJWTConfig()
	neither.Backend = "test-elsewhere"
	neither.JWT.Revocation.Mode = RevocationOff
	if err := neither.validate(); err != nil {
		t.Fatalf("jwt_only reading no store = %v", err)
	}

	// And the relational backend is unaffected by any of this.
	relational := validJWTConfig()
	relational.Backend = BackendRDB
	if err := relational.validate(); err != nil {
		t.Fatalf("jwt_only on the relational backend = %v", err)
	}
}
