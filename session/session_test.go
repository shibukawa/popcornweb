package session

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type payload struct {
	AccountID string `json:"account_id"`
}

type cart struct {
	Items []string `json:"items"`
}

type locale struct {
	Tag string `json:"tag"`
}

type density struct {
	Compact bool `json:"compact"`
}

// mapStore is a minimal in-process RawStore used to exercise Manager semantics.
type mapStore struct {
	sync.Mutex
	records map[string]RawRecord
	failing bool
	puts    int
	touches int
}

func newMapStore() *mapStore {
	return &mapStore{records: make(map[string]RawRecord)}
}

func (s *mapStore) Put(_ context.Context, keyHash string, record RawRecord) error {
	s.Lock()
	defer s.Unlock()
	if s.failing {
		return ErrUnavailable
	}
	s.puts++
	s.records[keyHash] = record
	return nil
}

func (s *mapStore) Get(_ context.Context, keyHash string) (RawRecord, error) {
	s.Lock()
	defer s.Unlock()
	if s.failing {
		return RawRecord{}, ErrUnavailable
	}
	record, ok := s.records[keyHash]
	if !ok {
		return RawRecord{}, ErrNotFound
	}
	return record, nil
}

func (s *mapStore) Touch(_ context.Context, keyHash string, lastSeenAt, idleExpiresAt time.Time) error {
	s.Lock()
	defer s.Unlock()
	record, ok := s.records[keyHash]
	if !ok {
		return ErrNotFound
	}
	s.touches++
	record.LastSeenAt = lastSeenAt
	record.IdleExpiresAt = idleExpiresAt
	s.records[keyHash] = record
	return nil
}

func (s *mapStore) Delete(_ context.Context, keyHash string) error {
	s.Lock()
	defer s.Unlock()
	delete(s.records, keyHash)
	return nil
}

func (s *mapStore) len() int {
	s.Lock()
	defer s.Unlock()
	return len(s.records)
}

type clock struct {
	sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.Lock()
	defer c.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.Lock()
	defer c.Unlock()
	c.now = c.now.Add(d)
}

func defaultOptions(t *testing.T, now func() time.Time) Options {
	t.Helper()
	return Options{
		TTL:             time.Hour,
		IdleTimeout:     30 * time.Minute,
		RenewalInterval: 5 * time.Minute,
		Cookie:          CookieOptions{Secure: true, HTTPOnly: true},
		Keys:            testKeyring(t, 1),
		Now:             now,
	}
}

// accountRegistry registers one Private slot, the shape plugin/auth uses.
func accountRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := NewRegistry()
	if err := registry.Register[payload]("account", Private, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return registry
}

func testManager(t *testing.T, registry *Registry, backend RawStore, options Options) *Manager {
	t.Helper()
	manager, err := NewManager(registry, backend, options)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return manager
}

// sessionCookie returns the named cookie written to the recorded response.
func sessionCookie(t *testing.T, recorder *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	return cookieOf(recorder, name)
}

// cookieOf returns the named cookie written to the recorded response.
func cookieOf(recorder *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

// run serves one request through the manager middleware and returns the
// response. handler receives the request whose context carries the session.
func run(manager *Manager, cookies []*http.Cookie, handler func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	manager.Middleware(nil)(http.HandlerFunc(handler)).ServeHTTP(recorder, request)
	return recorder
}

// carry turns a response into the cookies the browser would send next. A later
// Set-Cookie replaces an earlier one of the same name, which is what makes a
// rotation inside one response leave exactly one token behind.
func carry(recorder *httptest.ResponseRecorder) []*http.Cookie {
	client := newBrowser()
	client.accept(recorder)
	return client.list()
}

func TestBareReadIssuesNothing(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	manager := testManager(t, accountRegistry(t), store, defaultOptions(t, c.Now))

	recorder := run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		if _, ok := Load[payload](r.Context()); ok {
			t.Error("a browser with no session reported one")
		}
	})
	if len(recorder.Result().Cookies()) != 0 {
		t.Fatalf("a visitor who wrote nothing received cookies: %#v", recorder.Result().Cookies())
	}
	if store.len() != 0 {
		t.Fatalf("a visitor who wrote nothing occupies storage: %d", store.len())
	}
}

func TestAnonymousPrivateSlotRidesTheCookieAndNotTheServer(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	manager := testManager(t, accountRegistry(t), store, defaultOptions(t, c.Now))

	first := run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		handle, ok := Value[payload](r.Context())
		if !ok {
			t.Fatal("no slot handle")
		}
		if err := handle.Set(payload{AccountID: "anonymous-cart"}); err != nil {
			t.Fatalf("Set: %v", err)
		}
	})
	token := cookieOf(first, DefaultCookieName)
	if token == nil || !validToken(token.Value) {
		t.Fatalf("token cookie = %#v", token)
	}
	if cookieOf(first, DefaultDataCookieName) == nil {
		t.Fatal("anonymous record was not written to the sealed cookie")
	}
	if store.len() != 0 {
		t.Fatalf("anonymous write reached the server: %d records", store.len())
	}

	// The value survives the round trip through the browser.
	run(manager, carry(first), func(_ http.ResponseWriter, r *http.Request) {
		value, ok := Load[payload](r.Context())
		if !ok || value.AccountID != "anonymous-cart" {
			t.Fatalf("resolved = %#v ok=%v", value, ok)
		}
	})
}

func TestRotatePromotesPrivateSlotToTheServer(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	manager := testManager(t, accountRegistry(t), store, defaultOptions(t, c.Now))

	anonymous := run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		handle, _ := Value[payload](r.Context())
		if err := handle.Set(payload{AccountID: "kept"}); err != nil {
			t.Fatal(err)
		}
	})
	anonymousToken := cookieOf(anonymous, DefaultCookieName)

	login := run(manager, carry(anonymous), func(w http.ResponseWriter, r *http.Request) {
		if err := manager.Rotate(w, r); err != nil {
			t.Fatalf("Rotate: %v", err)
		}
	})
	loginToken := cookieOf(login, DefaultCookieName)
	if loginToken == nil || loginToken.Value == anonymousToken.Value {
		t.Fatal("rotation reused the previous token")
	}
	if store.len() != 1 {
		t.Fatalf("promotion did not reach the server: %d records", store.len())
	}
	if record := cookieOf(login, DefaultDataCookieName); record == nil || record.MaxAge >= 0 {
		t.Fatalf("the anonymous record cookie was not expired: %#v", record)
	}

	// The value written before the login is still there, now from the server.
	run(manager, carry(login), func(_ http.ResponseWriter, r *http.Request) {
		value, ok := Load[payload](r.Context())
		if !ok || value.AccountID != "kept" {
			t.Fatalf("promoted value = %#v ok=%v", value, ok)
		}
	})

	// A later write stays on the server rather than falling back to a cookie.
	before := store.puts
	after := run(manager, carry(login), func(_ http.ResponseWriter, r *http.Request) {
		handle, _ := Value[payload](r.Context())
		if err := handle.Set(payload{AccountID: "changed"}); err != nil {
			t.Fatal(err)
		}
	})
	if store.puts == before {
		t.Fatal("a write after promotion did not reach the server")
	}
	if record := cookieOf(after, DefaultDataCookieName); record != nil && record.MaxAge >= 0 {
		t.Fatal("a write after promotion resurrected the record cookie")
	}
}

func TestServerOnlySlotReachesTheServerWhileAnonymous(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	registry := NewRegistry()
	if err := registry.Register[payload]("creds", ServerOnly, nil); err != nil {
		t.Fatal(err)
	}
	manager := testManager(t, registry, store, defaultOptions(t, c.Now))

	response := run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		handle, _ := Value[payload](r.Context())
		if err := handle.Set(payload{AccountID: "a"}); err != nil {
			t.Fatal(err)
		}
	})
	if store.len() != 1 {
		t.Fatalf("ServerOnly write did not reach the server: %d", store.len())
	}
	if cookieOf(response, DefaultDataCookieName) != nil {
		t.Fatal("ServerOnly value was written to a cookie")
	}
}

func TestServerOnlySlotIsRefusedOnTheCookieBackend(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register[payload]("creds", ServerOnly, nil); err != nil {
		t.Fatal(err)
	}
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	_, err := NewManager(registry, nil, defaultOptions(t, c.Now))
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("error = %v, want a refusal naming the slot", err)
	}
}

func TestCookiePlacedTiersCarryTheirOwnCookies(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	registry := NewRegistry()
	if err := registry.Register[density]("density", Shared, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register[locale]("locale", ReadOnly, nil); err != nil {
		t.Fatal(err)
	}
	manager := testManager(t, registry, store, defaultOptions(t, c.Now))

	response := run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		shared, _ := Value[density](r.Context())
		if err := shared.Set(density{Compact: true}); err != nil {
			t.Fatal(err)
		}
		readOnly, _ := Value[locale](r.Context())
		if err := readOnly.Set(locale{Tag: "ja"}); err != nil {
			t.Fatal(err)
		}
	})
	// A cookie-placed slot needs no session token and no record.
	if cookieOf(response, DefaultCookieName) != nil {
		t.Fatal("a cookie-placed write issued a session token")
	}
	if store.len() != 0 {
		t.Fatalf("a cookie-placed write reached the server: %d", store.len())
	}
	for _, name := range []string{"density", "locale"} {
		cookie := cookieOf(response, name)
		if cookie == nil {
			t.Fatalf("slot %q wrote no cookie", name)
		}
		if cookie.HttpOnly {
			t.Fatalf("slot %q is HttpOnly, so the front end cannot read it", name)
		}
	}
	run(manager, carry(response), func(_ http.ResponseWriter, r *http.Request) {
		if value, ok := Load[density](r.Context()); !ok || !value.Compact {
			t.Fatalf("shared = %#v ok=%v", value, ok)
		}
		if value, ok := Load[locale](r.Context()); !ok || value.Tag != "ja" {
			t.Fatalf("read-only = %#v ok=%v", value, ok)
		}
	})
}

func TestReadOnlySlotRejectsAClientEditAndSharedAcceptsIt(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	registry := NewRegistry()
	if err := registry.Register[density]("density", Shared, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register[locale]("locale", ReadOnly, nil); err != nil {
		t.Fatal(err)
	}
	manager := testManager(t, registry, store, defaultOptions(t, c.Now))

	// A client-forged value in the plain wire format, which is exactly what the
	// plain mode accepts and the signed mode must not.
	plain := func(body string) string {
		return "1.p." + base64.RawURLEncoding.EncodeToString([]byte(body))
	}
	forged := []*http.Cookie{
		{Name: "density", Value: plain(`{"compact":true}`)},
		{Name: "locale", Value: plain(`{"tag":"fr"}`)},
	}
	run(manager, forged, func(_ http.ResponseWriter, r *http.Request) {
		if value, ok := Load[density](r.Context()); !ok || !value.Compact {
			t.Fatalf("shared value rejected a client edit: %#v ok=%v", value, ok)
		}
		if value, ok := Load[locale](r.Context()); ok {
			t.Fatalf("read-only value accepted a client edit: %#v", value)
		}
	})
}

func TestDestroyEndsEveryPlacement(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	registry := NewRegistry()
	if err := registry.Register[payload]("account", Private, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register[locale]("locale", ReadOnly, nil); err != nil {
		t.Fatal(err)
	}
	manager := testManager(t, registry, store, defaultOptions(t, c.Now))

	live := run(manager, nil, func(w http.ResponseWriter, r *http.Request) {
		account, _ := Value[payload](r.Context())
		if err := account.Set(payload{AccountID: "a"}); err != nil {
			t.Fatal(err)
		}
		tag, _ := Value[locale](r.Context())
		if err := tag.Set(locale{Tag: "ja"}); err != nil {
			t.Fatal(err)
		}
		if err := manager.Rotate(w, r); err != nil {
			t.Fatal(err)
		}
	})
	if store.len() != 1 {
		t.Fatalf("records before destroy = %d", store.len())
	}

	out := run(manager, carry(live), func(w http.ResponseWriter, r *http.Request) {
		if err := manager.Destroy(w, r); err != nil {
			t.Fatalf("Destroy: %v", err)
		}
	})
	if store.len() != 0 {
		t.Fatalf("records after destroy = %d", store.len())
	}
	for _, name := range []string{DefaultCookieName, DefaultDataCookieName, "locale"} {
		cookie := cookieOf(out, name)
		if cookie == nil || cookie.MaxAge >= 0 {
			t.Fatalf("cookie %q survived the destroy: %#v", name, cookie)
		}
	}
}

func TestExpiredRecordIsClearedAndTheRequestContinues(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	manager := testManager(t, accountRegistry(t), store, defaultOptions(t, c.Now))

	live := run(manager, nil, func(w http.ResponseWriter, r *http.Request) {
		handle, _ := Value[payload](r.Context())
		if err := handle.Set(payload{AccountID: "a"}); err != nil {
			t.Fatal(err)
		}
		if err := manager.Rotate(w, r); err != nil {
			t.Fatal(err)
		}
	})

	c.advance(2 * time.Hour)
	seen := true
	recorder := run(manager, carry(live), func(_ http.ResponseWriter, r *http.Request) {
		_, seen = Load[payload](r.Context())
	})
	if seen {
		t.Fatal("expired session was accepted")
	}
	cleared := cookieOf(recorder, DefaultCookieName)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("stale cookie was not cleared: %#v", cleared)
	}
	if store.len() != 0 {
		t.Fatalf("expired record was not removed: %d", store.len())
	}
}

func TestMiddlewareFailsClosedWhenBackendIsUnavailable(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	manager := testManager(t, accountRegistry(t), store, defaultOptions(t, c.Now))
	live := run(manager, nil, func(w http.ResponseWriter, r *http.Request) {
		handle, _ := Value[payload](r.Context())
		if err := handle.Set(payload{AccountID: "a"}); err != nil {
			t.Fatal(err)
		}
		if err := manager.Rotate(w, r); err != nil {
			t.Fatal(err)
		}
	})

	store.failing = true
	reached := false
	recorder := run(manager, carry(live), func(http.ResponseWriter, *http.Request) { reached = true })
	if reached {
		t.Fatal("handler ran while the session backend was unavailable")
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestRenewalIsBoundedByIntervalAndAbsoluteExpiry(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	manager := testManager(t, accountRegistry(t), store, defaultOptions(t, c.Now))
	live := run(manager, nil, func(w http.ResponseWriter, r *http.Request) {
		handle, _ := Value[payload](r.Context())
		if err := handle.Set(payload{AccountID: "a"}); err != nil {
			t.Fatal(err)
		}
		if err := manager.Rotate(w, r); err != nil {
			t.Fatal(err)
		}
	})
	cookies := carry(live)
	visit := func() { run(manager, cookies, func(http.ResponseWriter, *http.Request) {}) }

	c.advance(time.Minute)
	visit()
	if store.touches != 0 {
		t.Fatalf("touches before the renewal interval = %d", store.touches)
	}
	c.advance(10 * time.Minute)
	visit()
	if store.touches != 1 {
		t.Fatalf("touches after the renewal interval = %d", store.touches)
	}

	// Idle renewal must never move the record past its absolute expiry.
	c.advance(25 * time.Minute)
	visit()
	var token string
	for _, cookie := range cookies {
		if cookie.Name == DefaultCookieName {
			token = cookie.Value
		}
	}
	record, err := store.Get(context.Background(), keyHash(token))
	if err != nil {
		t.Fatal(err)
	}
	if record.IdleExpiresAt.After(record.ExpiresAt) {
		t.Fatalf("idle expiry %s passed absolute expiry %s", record.IdleExpiresAt, record.ExpiresAt)
	}
}

func TestVersionMismatchInvalidatesRecord(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	options := defaultOptions(t, c.Now)
	manager := testManager(t, accountRegistry(t), store, options)
	live := run(manager, nil, func(w http.ResponseWriter, r *http.Request) {
		handle, _ := Value[payload](r.Context())
		if err := handle.Set(payload{AccountID: "a"}); err != nil {
			t.Fatal(err)
		}
		if err := manager.Rotate(w, r); err != nil {
			t.Fatal(err)
		}
	})

	options.Version = 2
	upgraded := testManager(t, accountRegistry(t), store, options)
	authenticated := false
	run(upgraded, carry(live), func(_ http.ResponseWriter, r *http.Request) {
		_, authenticated = Load[payload](r.Context())
	})
	if authenticated {
		t.Fatal("record of an older version was accepted")
	}
	if store.len() != 0 {
		t.Fatal("record of an older version was kept")
	}
}

func TestManagerRejectsUnsafeOptions(t *testing.T) {
	keys := testKeyring(t, 1)
	cases := map[string]Options{
		"idle beyond ttl": {TTL: time.Hour, IdleTimeout: 2 * time.Hour, Keys: keys},
		"insecure same-site none": {
			TTL:    time.Hour,
			Keys:   keys,
			Cookie: CookieOptions{SameSite: http.SameSiteNoneMode, AllowInsecure: true},
		},
		"invalid cookie name":  {TTL: time.Hour, Keys: keys, Cookie: CookieOptions{Name: "bad name"}},
		"relative cookie path": {TTL: time.Hour, Keys: keys, Cookie: CookieOptions{Path: "relative"}},
		"missing keyring":      {TTL: time.Hour},
	}
	for name, options := range cases {
		if _, err := NewManager(accountRegistry(t), newMapStore(), options); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("%s: error = %v", name, err)
		}
	}
}

func TestSharedOnlyRegistryNeedsNoKeyring(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register[density]("density", Shared, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(registry, nil, Options{TTL: time.Hour}); err != nil {
		t.Fatalf("a registry of only Shared slots needs no secret: %v", err)
	}
}

func TestMalformedCookieNeverReachesTheStore(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	manager := testManager(t, accountRegistry(t), store, defaultOptions(t, c.Now))
	store.failing = true

	reached := false
	recorder := run(manager, []*http.Cookie{{Name: DefaultCookieName, Value: "not-a-token"}},
		func(http.ResponseWriter, *http.Request) { reached = true })
	if !reached || recorder.Code != http.StatusOK {
		t.Fatalf("malformed cookie was sent to the backend: status=%d reached=%v", recorder.Code, reached)
	}
}

func TestRegistryRefusesDuplicates(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register[payload]("account", Private, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register[payload]("other", Private, nil); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("duplicate type error = %v", err)
	}
	if err := registry.Register[cart]("account", Private, nil); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("duplicate key error = %v", err)
	}
	if err := registry.Register[cart]("bad name", Private, nil); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("invalid key error = %v", err)
	}
	if err := registry.Register[cart]("cart", Placement(0), nil); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("invalid placement error = %v", err)
	}
}

func TestRegistrationAfterTheManagerIsRefused(t *testing.T) {
	registry := accountRegistry(t)
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	testManager(t, registry, newMapStore(), defaultOptions(t, c.Now))
	if err := registry.Register[cart]("cart", Private, nil); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("late registration error = %v", err)
	}
}

func TestUnregisteredTypeHasNoHandle(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	manager := testManager(t, accountRegistry(t), store, defaultOptions(t, c.Now))
	run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		if _, ok := Value[cart](r.Context()); ok {
			t.Fatal("an unregistered type produced a handle")
		}
	})
}

func TestOversizedAnonymousPrivateWriteIsRefusedRatherThanSpilled(t *testing.T) {
	store := newMapStore()
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	registry := NewRegistry()
	if err := registry.Register[cart]("cart", Private, nil); err != nil {
		t.Fatal(err)
	}
	manager := testManager(t, registry, store, defaultOptions(t, c.Now))

	big := cart{Items: make([]string, 0, 256)}
	for range 256 {
		big.Items = append(big.Items, "0123456789abcdef0123456789abcdef")
	}
	run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		handle, _ := Value[cart](r.Context())
		if err := handle.Set(big); !errors.Is(err, ErrCookieTooLarge) {
			t.Fatalf("oversized anonymous write error = %v, want ErrCookieTooLarge", err)
		}
	})
	if store.len() != 0 {
		t.Fatalf("an oversized anonymous write spilled to the server: %d", store.len())
	}
}

func TestJSONCodecRejectsTrailingBytes(t *testing.T) {
	codec := JSONCodec[payload]{}
	encoded, err := codec.Encode(payload{AccountID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Decode(encoded); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if _, err := codec.Decode(append(encoded, '{')); !errors.Is(err, ErrCodec) {
		t.Fatalf("trailing bytes error = %v", err)
	}
}

func TestSlotMapCodecRoundTrip(t *testing.T) {
	codec := slotMapCodec{order: []string{"a", "b"}}
	encoded, err := codec.Encode(slotMap{"a": []byte("one"), "b": []byte("two")})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded["a"]) != "one" || string(decoded["b"]) != "two" {
		t.Fatalf("decoded = %#v", decoded)
	}
	if _, err := codec.Decode([]byte{9, 9, 9}); !errors.Is(err, ErrCodec) {
		t.Fatalf("malformed payload error = %v", err)
	}
}
