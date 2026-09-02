package session

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

type preference struct{ Tag string }

// A record-placed slot may die before the session that carries it. This is the
// primitive plugin/auth hand-rolls today for its step-up admission window.
func TestARecordSlotExpiresBeforeItsSession(t *testing.T) {
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	store := newMapStore()
	registry := NewRegistry()
	if err := registry.Register[payload]("account", Private, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register[preference]("admission", Private, nil, ExpiresAfter(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	manager := testManager(t, registry, store, defaultOptions(t, c.Now))

	live := run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		account, _ := Value[payload](r.Context())
		if err := account.Set(payload{AccountID: "a"}); err != nil {
			t.Fatal(err)
		}
		short, _ := Value[preference](r.Context())
		if err := short.Set(preference{Tag: "admitted"}); err != nil {
			t.Fatal(err)
		}
	})
	cookies := carry(live)

	// Inside its own window both slots read.
	c.advance(10 * time.Second)
	run(manager, cookies, func(_ http.ResponseWriter, r *http.Request) {
		if _, ok := Load[preference](r.Context()); !ok {
			t.Fatal("the short-lived slot expired early")
		}
	})

	// Past it the short slot reads as absent while the session continues.
	c.advance(time.Minute)
	run(manager, cookies, func(_ http.ResponseWriter, r *http.Request) {
		if _, ok := Load[preference](r.Context()); ok {
			t.Fatal("the short-lived slot outlived its own deadline")
		}
		if value, ok := Load[payload](r.Context()); !ok || value.AccountID != "a" {
			t.Fatalf("the session died with one of its slots: %#v ok=%v", value, ok)
		}
	})
}

// A browser-scoped slot belongs to the browser rather than to whoever was
// signed in, so a destroy leaves it alone.
func TestAnOutlivingSlotSurvivesDestroy(t *testing.T) {
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	store := newMapStore()
	registry := NewRegistry()
	if err := registry.Register[payload]("account", Private, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register[locale]("locale", ReadOnly, nil, OutlivesSession(BrowserMax)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register[density]("density", Shared, nil); err != nil {
		t.Fatal(err)
	}
	manager := testManager(t, registry, store, defaultOptions(t, c.Now))

	client := newBrowser()
	live := run(manager, client.list(), func(w http.ResponseWriter, r *http.Request) {
		account, _ := Value[payload](r.Context())
		if err := account.Set(payload{AccountID: "a"}); err != nil {
			t.Fatal(err)
		}
		tag, _ := Value[locale](r.Context())
		if err := tag.Set(locale{Tag: "ja"}); err != nil {
			t.Fatal(err)
		}
		seen, _ := Value[density](r.Context())
		if err := seen.Set(density{Compact: true}); err != nil {
			t.Fatal(err)
		}
		if err := manager.Rotate(w, r); err != nil {
			t.Fatal(err)
		}
	})

	client.accept(live)
	out := run(manager, client.list(), func(w http.ResponseWriter, r *http.Request) {
		if err := manager.Destroy(w, r); err != nil {
			t.Fatalf("Destroy: %v", err)
		}
	})
	client.accept(out)
	if cookie := cookieOf(out, "locale"); cookie != nil && cookie.MaxAge < 0 {
		t.Fatal("a slot declared OutlivesSession was cleared by the destroy")
	}
	// A slot that declared nothing still goes with the session.
	if cookie := cookieOf(out, "density"); cookie == nil || cookie.MaxAge >= 0 {
		t.Fatalf("a session-scoped slot survived the destroy: %#v", cookie)
	}

	// The browser keeps the value, so the next visit still reads it.
	run(manager, client.list(), func(_ http.ResponseWriter, r *http.Request) {
		if value, ok := Load[locale](r.Context()); !ok || value.Tag != "ja" {
			t.Fatalf("locale = %#v ok=%v", value, ok)
		}
		if _, ok := Load[payload](r.Context()); ok {
			t.Fatal("the login survived the destroy")
		}
	})
}

// A cookie-placed slot carries its stated lifetime as the cookie lifetime,
// rather than inheriting the session's.
func TestAStatedLifetimeReachesTheCookie(t *testing.T) {
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	registry := NewRegistry()
	if err := registry.Register[locale]("locale", ReadOnly, nil, OutlivesSession(90*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register[density]("density", Shared, nil); err != nil {
		t.Fatal(err)
	}
	manager := testManager(t, registry, newMapStore(), defaultOptions(t, c.Now))

	response := run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		tag, _ := Value[locale](r.Context())
		if err := tag.Set(locale{Tag: "ja"}); err != nil {
			t.Fatal(err)
		}
		seen, _ := Value[density](r.Context())
		if err := seen.Set(density{Compact: true}); err != nil {
			t.Fatal(err)
		}
	})
	stated := cookieOf(response, "locale")
	if stated == nil || stated.MaxAge < int((89*24*time.Hour).Seconds()) {
		t.Fatalf("the stated lifetime did not reach the cookie: %#v", stated)
	}
	// The session TTL is an hour, so a slot that stated nothing tracks it.
	inherited := cookieOf(response, "density")
	if inherited == nil || inherited.MaxAge > int(time.Hour.Seconds()) {
		t.Fatalf("a slot with no stated lifetime did not track the session: %#v", inherited)
	}
}

// The one constraint: a value may die before its session, but only a value the
// browser holds can outlive it.
func TestOutlivingIsRefusedForARecordSlot(t *testing.T) {
	for _, placement := range []Placement{Private, ServerOnly} {
		registry := NewRegistry()
		err := registry.Register[payload]("creds", placement, nil, OutlivesSession(time.Hour))
		if !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("%s: error = %v, want a refusal", placement, err)
		}
	}
	// Dying early is fine everywhere, which is what makes the rule one-directional.
	for _, placement := range []Placement{Shared, ReadOnly, Private, ServerOnly} {
		registry := NewRegistry()
		if err := registry.Register[payload]("creds", placement, nil, ExpiresAfter(time.Hour)); err != nil {
			t.Fatalf("%s: ExpiresAfter was refused: %v", placement, err)
		}
	}
}

func TestLifetimeOptionsAreValidated(t *testing.T) {
	cases := map[string][]SlotOption{
		"zero":                   {ExpiresAfter(0)},
		"negative":               {OutlivesSession(-time.Hour)},
		"beyond the browser cap": {OutlivesSession(BrowserMax + time.Hour)},
		"stated twice":           {ExpiresAfter(time.Hour), OutlivesSession(time.Hour)},
	}
	for name, options := range cases {
		registry := NewRegistry()
		if err := registry.Register[locale]("locale", ReadOnly, nil, options...); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("%s: error = %v", name, err)
		}
	}
}
