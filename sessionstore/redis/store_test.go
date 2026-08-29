package redis

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/shibukawa/popcornweb/session"
)

const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeClient is an in-process stand-in for a server: it applies TTLs against a
// test clock so expiry can be exercised without sleeping.
type fakeClient struct {
	mu      sync.Mutex
	values  map[string][]byte
	expiry  map[string]time.Time
	now     func() time.Time
	err     error
	lastTTL time.Duration
}

func newFakeClient(now func() time.Time) *fakeClient {
	return &fakeClient{values: map[string][]byte{}, expiry: map[string]time.Time{}, now: now}
}

func (c *fakeClient) collect() {
	for key, expiry := range c.expiry {
		if !expiry.After(c.now()) {
			delete(c.values, key)
			delete(c.expiry, key)
		}
	}
}

func (c *fakeClient) Get(_ context.Context, key string) *goredis.StringCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return goredis.NewStringResult("", c.err)
	}
	c.collect()
	value, ok := c.values[key]
	if !ok {
		return goredis.NewStringResult("", goredis.Nil)
	}
	return goredis.NewStringResult(string(value), nil)
}

func (c *fakeClient) Set(_ context.Context, key string, value any, expiration time.Duration) *goredis.StatusCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return goredis.NewStatusResult("", c.err)
	}
	c.collect()
	c.store(key, value, expiration)
	return goredis.NewStatusResult("OK", nil)
}

func (c *fakeClient) SetXX(_ context.Context, key string, value any, expiration time.Duration) *goredis.BoolCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return goredis.NewBoolResult(false, c.err)
	}
	c.collect()
	if _, ok := c.values[key]; !ok {
		return goredis.NewBoolResult(false, nil)
	}
	c.store(key, value, expiration)
	return goredis.NewBoolResult(true, nil)
}

func (c *fakeClient) store(key string, value any, expiration time.Duration) {
	c.lastTTL = expiration
	c.values[key] = append([]byte(nil), value.([]byte)...)
	c.expiry[key] = c.now().Add(expiration)
}

func (c *fakeClient) Del(_ context.Context, keys ...string) *goredis.IntCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return goredis.NewIntResult(0, c.err)
	}
	removed := 0
	for _, key := range keys {
		if _, ok := c.values[key]; ok {
			removed++
		}
		delete(c.values, key)
		delete(c.expiry, key)
	}
	return goredis.NewIntResult(int64(removed), nil)
}

func (c *fakeClient) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

func (c *fakeClient) keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.values))
	for key := range c.values {
		names = append(names, key)
	}
	return names
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func testStore(t *testing.T, c *clock) (*Store, *fakeClient) {
	t.Helper()
	client := newFakeClient(c.Now)
	store, err := newStore(client, Options{Now: c.Now})
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	return store, client
}

// testPayload is what a host codec would have produced. A backend stores
// bytes, so these tests hold the only codec in play.
var testPayload = []byte(`{"account_id":"account-1"}`)

func testRecord(now time.Time) session.RawRecord {
	return session.RawRecord{
		Payload:         testPayload,
		CreatedAt:       now,
		AuthenticatedAt: now,
		LastSeenAt:      now,
		ExpiresAt:       now.Add(time.Hour),
		IdleExpiresAt:   now.Add(30 * time.Minute),
		Method:          "oidc",
		Version:         3,
	}
}

func TestStoreRoundTripsARecord(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_800_000_000_000)}
	store, client := testStore(t, c)
	record := testRecord(c.Now())

	if err := store.Put(t.Context(), testKey, record); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// The key space is the configured prefix and the hash, never a raw token.
	names := client.keys()
	if len(names) != 1 || names[0] != DefaultKeyPrefix+testKey {
		t.Fatalf("keys = %v", names)
	}
	// The TTL is the record deadline, so an abandoned session leaves nothing
	// to sweep.
	if client.lastTTL != 30*time.Minute {
		t.Fatalf("ttl = %v", client.lastTTL)
	}

	loaded, err := store.Get(t.Context(), testKey)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(loaded.Payload) != string(record.Payload) || loaded.Method != "oidc" || loaded.Version != 3 {
		t.Fatalf("record = %#v", loaded)
	}
	if !loaded.CreatedAt.Equal(record.CreatedAt) || !loaded.ExpiresAt.Equal(record.ExpiresAt) ||
		!loaded.IdleExpiresAt.Equal(record.IdleExpiresAt) || !loaded.LastSeenAt.Equal(record.LastSeenAt) {
		t.Fatalf("timestamps = %#v", loaded)
	}
}

func TestStoreReportsStoredExpiryBeforeTheServerCollectsIt(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_800_000_000_000)}
	store, client := testStore(t, c)
	record := testRecord(c.Now())
	record.IdleExpiresAt = time.Time{}
	if err := store.Put(t.Context(), testKey, record); err != nil {
		t.Fatal(err)
	}

	// A server that has not collected the key yet must not extend the session:
	// the stored expiry is authoritative.
	c.advance(2 * time.Hour)
	client.mu.Lock()
	client.expiry[DefaultKeyPrefix+testKey] = c.Now().Add(time.Hour)
	client.mu.Unlock()
	if _, err := store.Get(t.Context(), testKey); !errors.Is(err, session.ErrExpired) {
		t.Fatalf("Get error = %v", err)
	}
}

func TestStoreTouchRenewsWithoutRewritingThePayload(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_800_000_000_000)}
	store, client := testStore(t, c)
	record := testRecord(c.Now())
	if err := store.Put(t.Context(), testKey, record); err != nil {
		t.Fatal(err)
	}

	c.advance(10 * time.Minute)
	renewed := c.Now().Add(30 * time.Minute)
	if err := store.Touch(t.Context(), testKey, c.Now(), renewed); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if client.lastTTL != 30*time.Minute {
		t.Fatalf("renewed ttl = %v", client.lastTTL)
	}
	loaded, err := store.Get(t.Context(), testKey)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !loaded.IdleExpiresAt.Equal(renewed) || !loaded.LastSeenAt.Equal(c.Now()) {
		t.Fatalf("renewed record = %#v", loaded)
	}
	if string(loaded.Payload) != string(record.Payload) || !loaded.ExpiresAt.Equal(record.ExpiresAt) {
		t.Fatalf("renewal changed the payload or the absolute expiry: %#v", loaded)
	}
}

func TestStoreTouchRefusesToReviveOrOverextend(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_800_000_000_000)}
	store, _ := testStore(t, c)
	record := testRecord(c.Now())
	if err := store.Put(t.Context(), testKey, record); err != nil {
		t.Fatal(err)
	}

	// Past the absolute expiry there is nothing to renew.
	if err := store.Touch(t.Context(), testKey, c.Now(), record.ExpiresAt.Add(time.Minute)); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("overextending Touch error = %v", err)
	}
	c.advance(2 * time.Hour)
	if err := store.Touch(t.Context(), testKey, c.Now(), c.Now().Add(time.Minute)); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("expired Touch error = %v", err)
	}
	// A record that was never written is not created by a renewal.
	missing := strings.Repeat("a", keyHashLength)
	if err := store.Touch(t.Context(), missing, c.Now(), c.Now().Add(time.Minute)); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("missing Touch error = %v", err)
	}
}

// TouchRecord is the renewal a Manager takes when it already holds the record,
// so it must keep every rule Touch keeps. It has its own tests because it has
// its own copy of those rules: the caller supplies the record instead of the
// store reading one, and a guard dropped from this copy is a guard nothing else
// enforces.
func TestStoreTouchRecordRenewsFromTheRecordInHand(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_800_000_000_000)}
	store, client := testStore(t, c)
	record := testRecord(c.Now())
	if err := store.Put(t.Context(), testKey, record); err != nil {
		t.Fatal(err)
	}

	c.advance(10 * time.Minute)
	renewed := c.Now().Add(30 * time.Minute)
	if err := store.TouchRecord(t.Context(), testKey, record, c.Now(), renewed); err != nil {
		t.Fatalf("TouchRecord: %v", err)
	}
	if client.lastTTL != 30*time.Minute {
		t.Fatalf("renewed ttl = %v", client.lastTTL)
	}
	loaded, err := store.Get(t.Context(), testKey)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !loaded.IdleExpiresAt.Equal(renewed) || !loaded.LastSeenAt.Equal(c.Now()) {
		t.Fatalf("renewed record = %#v", loaded)
	}
	if string(loaded.Payload) != string(record.Payload) || !loaded.ExpiresAt.Equal(record.ExpiresAt) {
		t.Fatalf("renewal changed the payload or the absolute expiry: %#v", loaded)
	}
}

// A record bounded only by inactivity has no absolute deadline to renew past,
// which is what the zero-time guard in every store means. Comparing an idle
// deadline against the zero time instead refuses every renewal of such a
// record, and the session then expires under a reader who never stopped using
// it — silently, because the Manager reads a refused renewal as a session that
// ended rather than as a failure.
func TestStoreTouchRecordRenewsARecordBoundedOnlyByIdle(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_800_000_000_000)}
	store, _ := testStore(t, c)
	record := testRecord(c.Now())
	record.ExpiresAt = time.Time{}
	if err := store.Put(t.Context(), testKey, record); err != nil {
		t.Fatal(err)
	}

	c.advance(10 * time.Minute)
	renewed := c.Now().Add(30 * time.Minute)
	// Touch answers this record, so TouchRecord has to answer it the same way:
	// the two are one renewal reached two ways, not two policies.
	if err := store.Touch(t.Context(), testKey, c.Now(), renewed); err != nil {
		t.Fatalf("Touch on an idle-only record: %v", err)
	}
	if err := store.TouchRecord(t.Context(), testKey, record, c.Now(), renewed); err != nil {
		t.Fatalf("TouchRecord on an idle-only record: %v", err)
	}
	loaded, err := store.Get(t.Context(), testKey)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !loaded.IdleExpiresAt.Equal(renewed) {
		t.Fatalf("renewed record = %#v", loaded)
	}
}

func TestStoreTouchRecordRefusesToReviveOrOverextend(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_800_000_000_000)}
	store, _ := testStore(t, c)
	record := testRecord(c.Now())
	if err := store.Put(t.Context(), testKey, record); err != nil {
		t.Fatal(err)
	}

	// Past the absolute expiry there is nothing to renew.
	if err := store.TouchRecord(t.Context(), testKey, record, c.Now(),
		record.ExpiresAt.Add(time.Minute)); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("overextending TouchRecord error = %v", err)
	}
	// A record that was never written is not created by a renewal, which is what
	// the conditional write is for: the caller's record is not evidence the key
	// is still there.
	missing := strings.Repeat("a", keyHashLength)
	if err := store.TouchRecord(t.Context(), missing, record, c.Now(),
		c.Now().Add(time.Minute)); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("missing TouchRecord error = %v", err)
	}
	// And one the server has collected is gone, however fresh the record the
	// caller is holding looks.
	c.advance(2 * time.Hour)
	if err := store.TouchRecord(t.Context(), testKey, record, c.Now(),
		c.Now().Add(time.Minute)); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("expired TouchRecord error = %v", err)
	}
}

func TestStoreDeleteIsIdempotent(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_800_000_000_000)}
	store, _ := testStore(t, c)
	if err := store.Put(t.Context(), testKey, testRecord(c.Now())); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.Delete(t.Context(), testKey); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	}
	if _, err := store.Get(t.Context(), testKey); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("Get after Delete error = %v", err)
	}
}

func TestStoreRejectsForeignKeysAndUnusableRecords(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_800_000_000_000)}
	store, client := testStore(t, c)

	// A value that is not a canonical key hash never reaches the server.
	for _, key := range []string{"", "not-a-hash", strings.Repeat("A", keyHashLength)} {
		if _, err := store.Get(t.Context(), key); !errors.Is(err, session.ErrInvalidKey) {
			t.Fatalf("Get(%q) error = %v", key, err)
		}
	}
	if len(client.keys()) != 0 {
		t.Fatal("a malformed key reached the server")
	}

	expired := testRecord(c.Now())
	expired.ExpiresAt = c.Now().Add(-time.Minute)
	expired.IdleExpiresAt = time.Time{}
	if err := store.Put(t.Context(), testKey, expired); !errors.Is(err, session.ErrInvalidOptions) {
		t.Fatalf("expired Put error = %v", err)
	}

	oversized := testRecord(c.Now())
	oversized.Payload = []byte(strings.Repeat("x", defaultMaxPayloadBytes+1))
	if err := store.Put(t.Context(), testKey, oversized); !errors.Is(err, session.ErrCodec) {
		t.Fatalf("oversized Put error = %v", err)
	}
}

func TestStoreSanitizesBackendFailures(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_800_000_000_000)}
	store, client := testStore(t, c)
	if err := store.Put(t.Context(), testKey, testRecord(c.Now())); err != nil {
		t.Fatal(err)
	}
	client.fail(errors.New("dial tcp 10.0.0.1:6379: connect: refused for user:hunter2"))

	for name, call := range map[string]func() error{
		"put":    func() error { return store.Put(t.Context(), testKey, testRecord(c.Now())) },
		"get":    func() error { _, err := store.Get(t.Context(), testKey); return err },
		"touch":  func() error { return store.Touch(t.Context(), testKey, c.Now(), c.Now().Add(time.Minute)) },
		"delete": func() error { return store.Delete(t.Context(), testKey) },
	} {
		err := call()
		if !errors.Is(err, session.ErrUnavailable) {
			t.Fatalf("%s error = %v", name, err)
		}
		// Server text can carry a DSN or a value, so it never reaches the
		// error the request path sees.
		if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "10.0.0.1") {
			t.Fatalf("%s error leaks backend text: %v", name, err)
		}
	}
}

func TestStoreHonorsCancellation(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_800_000_000_000)}
	store, _ := testStore(t, c)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.Get(ctx, testKey); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Get error = %v", err)
	}
	if err := store.Delete(ctx, testKey); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Delete error = %v", err)
	}
}

func TestNewStoreValidatesOptions(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_800_000_000_000)}
	client := newFakeClient(c.Now)
	cases := map[string]Options{
		"space in prefix":    {KeyPrefix: "pw session:"},
		"oversized bounds":   {MaxPayloadBytes: hardMaxPayloadBytes + 1},
		"negative bounds":    {MaxPayloadBytes: -1},
		"unprintable prefix": {KeyPrefix: "pw\x00:"},
	}
	for name, options := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := newStore(client, options); !errors.Is(err, session.ErrInvalidOptions) {
				t.Fatalf("newStore error = %v", err)
			}
		})
	}
	if _, err := newStore(nil, Options{}); !errors.Is(err, session.ErrInvalidOptions) {
		t.Fatalf("nil client error = %v", err)
	}
}
