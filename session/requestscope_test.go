package session

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// authScopes is the standing example of a RequestScope value: derived from an
// authoritative source at the top of the request, read below, gone after.
type authScopes struct {
	Scopes []string `json:"scopes"`
}

func requestScopeRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := NewRegistry()
	if err := registry.Register[authScopes]("scopes", RequestScope, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return registry
}

func TestRequestScopeLivesForOneRequestOnly(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	registry := requestScopeRegistry(t)
	if err := registry.Register[payload]("account", Private, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	manager := testManager(t, registry, store, defaultOptions(t, c.Now))

	first := run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		handle, ok := Value[authScopes](r.Context())
		if !ok {
			t.Fatal("no slot handle")
		}
		if err := handle.Set(authScopes{Scopes: []string{"read", "write"}}); err != nil {
			t.Fatalf("Set: %v", err)
		}
		// The write is visible to the rest of this request.
		value, ok := Load[authScopes](r.Context())
		if !ok || len(value.Scopes) != 2 {
			t.Fatalf("resolved = %#v ok=%v", value, ok)
		}
		// The account slot gives the session something persistent, proving the
		// request-scoped value is excluded rather than the whole session inert.
		account, _ := Value[payload](r.Context())
		if err := account.Set(payload{AccountID: "kept"}); err != nil {
			t.Fatalf("Set account: %v", err)
		}
	})

	// The next request carries every cookie the first one wrote, and the
	// request-scoped value is still gone: it was never persisted anywhere.
	run(manager, carry(first), func(_ http.ResponseWriter, r *http.Request) {
		if _, ok := Load[authScopes](r.Context()); ok {
			t.Fatal("a RequestScope value survived into the next request")
		}
		if value, ok := Load[payload](r.Context()); !ok || value.AccountID != "kept" {
			t.Fatalf("the persistent slot was lost alongside: %#v ok=%v", value, ok)
		}
	})
}

func TestRequestScopeWriteTouchesNoCookieAndNoStore(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	manager := testManager(t, requestScopeRegistry(t), store, defaultOptions(t, c.Now))

	recorder := run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		handle, _ := Value[authScopes](r.Context())
		if err := handle.Set(authScopes{Scopes: []string{"admin"}}); err != nil {
			t.Fatalf("Set: %v", err)
		}
	})
	if cookies := recorder.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("a RequestScope write reached the browser: %#v", cookies)
	}
	if store.len() != 0 {
		t.Fatalf("a RequestScope write reached the server: %d records", store.len())
	}
}

func TestRequestScopeOnlyRegistryNeedsNoKeyringAndNoBackend(t *testing.T) {
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	manager, err := NewManager(requestScopeRegistry(t), nil, Options{
		Cookie: CookieOptions{Secure: true, HTTPOnly: true},
		Now:    c.Now,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		handle, _ := Value[authScopes](r.Context())
		if err := handle.Set(authScopes{Scopes: []string{"read"}}); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if value, ok := Load[authScopes](r.Context()); !ok || value.Scopes[0] != "read" {
			t.Fatalf("resolved = %#v ok=%v", value, ok)
		}
	})
}

func TestRequestScopeClearRemovesTheValue(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	manager := testManager(t, requestScopeRegistry(t), store, defaultOptions(t, c.Now))

	run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		handle, _ := Value[authScopes](r.Context())
		if err := handle.Set(authScopes{Scopes: []string{"read"}}); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := handle.Clear(); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		if _, ok := handle.Get(); ok {
			t.Fatal("a cleared RequestScope value is still readable")
		}
	})
}

func TestRequestScopeSurvivesRotateAndDestroyWithinTheRequest(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	registry := requestScopeRegistry(t)
	if err := registry.Register[payload]("account", Private, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	manager := testManager(t, registry, store, defaultOptions(t, c.Now))

	run(manager, nil, func(w http.ResponseWriter, r *http.Request) {
		scopes, _ := Value[authScopes](r.Context())
		if err := scopes.Set(authScopes{Scopes: []string{"read"}}); err != nil {
			t.Fatalf("Set: %v", err)
		}
		account, _ := Value[payload](r.Context())
		if err := account.Set(payload{AccountID: "kept"}); err != nil {
			t.Fatalf("Set account: %v", err)
		}

		// The value was derived from this request, not stored by the session,
		// so neither the login rotation nor the logout takes it away.
		if err := manager.Rotate(w, r); err != nil {
			t.Fatalf("Rotate: %v", err)
		}
		if _, ok := scopes.Get(); !ok {
			t.Fatal("the rotation dropped a RequestScope value")
		}
		if err := manager.Destroy(w, r); err != nil {
			t.Fatalf("Destroy: %v", err)
		}
		if _, ok := scopes.Get(); !ok {
			t.Fatal("the destroy dropped a RequestScope value")
		}
		if _, ok := account.Get(); ok {
			t.Fatal("the destroy kept a session-placed value")
		}
	})
}

func TestRequestScopeRefusesEveryLifetimeOption(t *testing.T) {
	for name, option := range map[string]SlotOption{
		"ExpiresAfter":    ExpiresAfter(time.Minute),
		"OutlivesSession": OutlivesSession(time.Hour),
		"ResetOnRotate":   ResetOnRotate(),
	} {
		registry := NewRegistry()
		err := registry.Register[authScopes]("scopes", RequestScope, nil, option)
		if !errors.Is(err, ErrInvalidOptions) {
			t.Errorf("%s on RequestScope: err = %v, want ErrInvalidOptions", name, err)
		}
	}
}

func TestRequestScopeIsNeverReadFromAStoredRecord(t *testing.T) {
	// A deployment that redeclares a slot's placement may leave old records
	// carrying the key. A key found in a record must never populate a slot
	// that is now RequestScope.
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}

	before := NewRegistry()
	if err := before.Register[authScopes]("scopes", ServerOnly, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	writer := testManager(t, before, store, defaultOptions(t, c.Now))
	first := run(writer, nil, func(_ http.ResponseWriter, r *http.Request) {
		handle, _ := Value[authScopes](r.Context())
		if err := handle.Set(authScopes{Scopes: []string{"stale"}}); err != nil {
			t.Fatalf("Set: %v", err)
		}
	})
	if store.len() != 1 {
		t.Fatalf("the ServerOnly write did not land: %d records", store.len())
	}

	after := requestScopeRegistry(t)
	reader := testManager(t, after, store, defaultOptions(t, c.Now))
	run(reader, carry(first), func(_ http.ResponseWriter, r *http.Request) {
		if value, ok := Load[authScopes](r.Context()); ok {
			t.Fatalf("a stored record populated a RequestScope slot: %#v", value)
		}
	})
}
