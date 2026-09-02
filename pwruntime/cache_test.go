package pwruntime

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shibukawa/tinybind-go/cachekeybind"
)

// The key types below are written by hand in the shape the cachekeybind
// generator emits for a `cache:"key"` marked struct: the derived identity, then
// each marked field framed. They are hand-written only because this package is
// the one under test and generating into it would be circular.
const keyIdentity = "github.com/shibukawa/popcornweb/pwruntime."

type userKey struct {
	ID   string
	Page int
}

func (k userKey) CacheKey() string {
	return cachekeybind.KeyString(keyIdentity+"userKey") +
		cachekeybind.KeyString(k.ID) + cachekeybind.KeyInt(k.Page)
}

// orderKey holds the same field values as userKey and must not reach its
// entries, which is what the derived identity in the method is for.
type orderKey struct {
	ID   string
	Page int
}

func (k orderKey) CacheKey() string {
	return cachekeybind.KeyString(keyIdentity+"orderKey") +
		cachekeybind.KeyString(k.ID) + cachekeybind.KeyInt(k.Page)
}

// taggedKey carries a tag, so a write can drop it without knowing the key. The
// tag method is this framework's rather than the generator's.
type taggedKey struct{ ID string }

func (k taggedKey) CacheKey() string {
	return cachekeybind.KeyString(keyIdentity+"taggedKey") + cachekeybind.KeyString(k.ID)
}

func (k taggedKey) CacheTags() []string { return []string{"user:" + k.ID} }

// testStore builds a store directly, so a test states its own policy without
// going through configuration.
func testStore(t *testing.T, config CacheStoreConfig) *CacheStore {
	t.Helper()
	if config.Name == "" {
		config.Name = "test"
	}
	if config.TTL == 0 {
		config.TTL = time.Minute
	}
	if config.MaxEntries == 0 {
		config.MaxEntries = 64
	}
	if config.FetchTimeout == 0 {
		config.FetchTimeout = time.Minute
	}
	store, err := newCacheStore(config)
	if err != nil {
		t.Fatalf("newCacheStore: %v", err)
	}
	return store
}

// signedIn returns a context carrying a reader identity, which is what a
// private store keys on.
func signedIn(subject string) context.Context {
	return WithAuthentication(context.Background(), Authentication{Subject: subject})
}

func TestAHitDoesNotRunTheFetch(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	ctx := context.Background()
	var calls atomic.Int64
	fetch := func(context.Context) (string, error) {
		calls.Add(1)
		return "value", nil
	}
	for range 3 {
		got, err := store.Get(ctx, userKey{ID: "u1", Page: 1}, fetch)
		if err != nil {
			t.Fatalf("Memo: %v", err)
		}
		if got != "value" {
			t.Fatalf("got %q, want value", got)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("fetch ran %d times, want 1", calls.Load())
	}
	if stats := store.Stats(); stats.Hits != 2 || stats.Misses != 1 {
		t.Errorf("stats = %+v, want 2 hits and 1 miss", stats)
	}
}

func TestADifferentKeyIsADifferentEntry(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	ctx := context.Background()
	fetch := func(value string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return value, nil }
	}
	if _, err := store.Get(ctx, userKey{ID: "u1", Page: 1}, fetch("first")); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, userKey{ID: "u1", Page: 2}, fetch("second"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "second" {
		t.Errorf("got %q, want second; a changed field reached another entry", got)
	}
}

// Two key types holding equal field values must not collide, which is the
// reason the identity lives inside the method rather than at the call site.
func TestTwoKeyTypesWithEqualFieldsDoNotCollide(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	ctx := context.Background()
	if _, err := store.Get(ctx, userKey{ID: "1", Page: 1}, func(context.Context) (string, error) {
		return "user", nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, orderKey{ID: "1", Page: 1}, func(context.Context) (string, error) {
		return "order", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "order" {
		t.Errorf("got %q, want order; one key type read another's entry", got)
	}
}

func TestAnExpiredEntryIsAMiss(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public", TTL: time.Minute})
	now := time.Now()
	store.now = func() time.Time { return now }
	ctx := context.Background()
	var calls atomic.Int64
	fetch := func(context.Context) (string, error) {
		calls.Add(1)
		return "value", nil
	}
	if _, err := store.Get(ctx, userKey{ID: "u1"}, fetch); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := store.Get(ctx, userKey{ID: "u1"}, fetch); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("fetch ran %d times, want 2; the expired entry answered", calls.Load())
	}
}

func TestConcurrentMissesRunTheFetchOnce(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	ctx := context.Background()
	release := make(chan struct{})
	var calls atomic.Int64
	fetch := func(context.Context) (string, error) {
		calls.Add(1)
		<-release
		return "value", nil
	}
	var wg sync.WaitGroup
	results := make([]string, 8)
	for index := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := store.Get(ctx, userKey{ID: "hot"}, fetch)
			if err != nil {
				t.Errorf("Memo: %v", err)
				return
			}
			results[index] = value
		}()
	}
	// Let every caller reach the flight before the fetch returns.
	waitFor(t, func() bool { return calls.Load() == 1 && store.Stats().Coalesced > 0 })
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("fetch ran %d times, want 1", calls.Load())
	}
	for index, got := range results {
		if got != "value" {
			t.Errorf("caller %d got %q, want value", index, got)
		}
	}
}

// A waiter that goes away must stop waiting without stopping the shared fetch,
// which is the whole reason the fetch runs on a detached context.
func TestACancelledWaiterDoesNotStopTheFetch(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	fetch := func(ctx context.Context) (string, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			return "value", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := store.Get(ctx, userKey{ID: "hot"}, fetch)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter returned %v, want context.Canceled", err)
	}
	// The fetch is still running on its own context; letting it finish must
	// fill the entry rather than fail with the waiter's cancellation.
	close(release)
	var calls atomic.Int64
	waitFor(t, func() bool {
		value, err := store.Get(context.Background(), userKey{ID: "hot"}, func(context.Context) (string, error) {
			calls.Add(1)
			return "refetched", nil
		})
		return err == nil && value == "value"
	})
	if calls.Load() != 0 {
		t.Errorf("the entry the abandoned fetch produced was not stored")
	}
}

func TestAPrivateStoreKeysPerReader(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "private"})
	fetch := func(value string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return value, nil }
	}
	if _, err := store.Get(signedIn("alice"), userKey{ID: "shared"}, fetch("alice")); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(signedIn("bob"), userKey{ID: "shared"}, fetch("bob"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "bob" {
		t.Errorf("got %q, want bob; one reader answered from another's entry", got)
	}
}

// An anonymous request on a private store stores nothing, because an entry
// under a blank identity is a shared entry wearing a private label.
func TestAPrivateStoreStoresNothingForAnAnonymousRequest(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "private"})
	ctx := context.Background()
	var calls atomic.Int64
	fetch := func(context.Context) (string, error) {
		calls.Add(1)
		return "value", nil
	}
	for range 2 {
		if _, err := store.Get(ctx, userKey{ID: "u1"}, fetch); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Errorf("fetch ran %d times, want 2; an anonymous request wrote an entry", calls.Load())
	}
	if store.Has(ctx, userKey{ID: "u1"}) {
		t.Errorf("Has reported an entry for an anonymous reader")
	}
}

func TestAPublicStoreSharesOneEntry(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	var calls atomic.Int64
	fetch := func(context.Context) (string, error) {
		calls.Add(1)
		return "shared", nil
	}
	if _, err := store.Get(signedIn("alice"), userKey{ID: "u1"}, fetch); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(signedIn("bob"), userKey{ID: "u1"}, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if got != "shared" || calls.Load() != 1 {
		t.Errorf("got %q after %d fetches, want shared after 1", got, calls.Load())
	}
}

// Inside the stale window the held value answers at once and one revalidation
// runs, which is what turns an upstream outage into a degraded page.
func TestAStaleEntryAnswersAndRevalidates(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public", TTL: time.Minute, Stale: time.Hour})
	now := time.Now()
	var mu sync.Mutex
	store.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	ctx := context.Background()
	value := "first"
	fetch := func(context.Context) (string, error) { return value, nil }
	if _, err := store.Get(ctx, userKey{ID: "u1"}, fetch); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()
	value = "second"
	got, err := store.Get(ctx, userKey{ID: "u1"}, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if got != "first" {
		t.Fatalf("got %q, want the held value first", got)
	}
	if store.Stats().StaleHits != 1 {
		t.Errorf("stale hit not counted: %+v", store.Stats())
	}
	// The revalidation runs detached, so the refreshed value appears without
	// any caller having waited for it.
	waitFor(t, func() bool {
		got, err := store.Get(ctx, userKey{ID: "u1"}, fetch)
		return err == nil && got == "second"
	})
}

func TestAFailedFetchIsNeverStored(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	ctx := context.Background()
	wanted := errors.New("upstream down")
	if _, err := store.Get(ctx, userKey{ID: "u1"}, func(context.Context) (string, error) {
		return "", wanted
	}); !errors.Is(err, wanted) {
		t.Fatalf("got %v, want the fetch error", err)
	}
	got, err := store.Get(ctx, userKey{ID: "u1"}, func(context.Context) (string, error) {
		return "recovered", nil
	})
	if err != nil || got != "recovered" {
		t.Errorf("got %q, %v; the failure was stored", got, err)
	}
}

func TestInvalidationDropsAnEntry(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	ctx := context.Background()
	fetch := func(value string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return value, nil }
	}
	if _, err := store.Get(ctx, userKey{ID: "u1"}, fetch("before")); err != nil {
		t.Fatal(err)
	}
	store.Invalidate(ctx, userKey{ID: "u1"})
	got, err := store.Get(ctx, userKey{ID: "u1"}, fetch("after"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "after" {
		t.Errorf("got %q, want after", got)
	}
}

func TestScopeInvalidationDropsOneReader(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "private"})
	fetch := func(value string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return value, nil }
	}
	for _, subject := range []string{"alice", "bob"} {
		if _, err := store.Get(signedIn(subject), userKey{ID: "u1"}, fetch(subject)); err != nil {
			t.Fatal(err)
		}
	}
	store.InvalidateScope("alice")
	if store.Has(signedIn("alice"), userKey{ID: "u1"}) {
		t.Errorf("alice's entry survived a scope invalidation")
	}
	if !store.Has(signedIn("bob"), userKey{ID: "u1"}) {
		t.Errorf("bob's entry was dropped by alice's scope invalidation")
	}
}

func TestTagInvalidationDropsEveryEntryTheTagNames(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	ctx := context.Background()
	fetch := func(context.Context) (string, error) { return "value", nil }
	for _, id := range []string{"u1", "u2"} {
		if _, err := store.Get(ctx, taggedKey{ID: id}, fetch); err != nil {
			t.Fatal(err)
		}
	}
	store.InvalidateTag("user:u1")
	if store.Has(ctx, taggedKey{ID: "u1"}) {
		t.Errorf("the tagged entry survived")
	}
	if !store.Has(ctx, taggedKey{ID: "u2"}) {
		t.Errorf("an entry the tag does not name was dropped")
	}
}

// groupedKey carries two tags, which is what a list entry naming its
// collection and its owner looks like.
type groupedKey struct{ ID string }

func (k groupedKey) CacheKey() string {
	return cachekeybind.KeyString(keyIdentity+"groupedKey") + cachekeybind.KeyString(k.ID)
}

func (k groupedKey) CacheTags() []string { return []string{"group:a", "user:" + k.ID} }

// regroupedKey re-creates the same entry under an unrelated tag, which is what
// the key looks like after the data it caches moved owners.
type regroupedKey struct{ ID string }

func (k regroupedKey) CacheKey() string {
	return cachekeybind.KeyString(keyIdentity+"groupedKey") + cachekeybind.KeyString(k.ID)
}

func (k regroupedKey) CacheTags() []string { return []string{"other:z"} }

// An entry dropped by one of its tags must also leave the other tags' indexes,
// or the stale reference outlives it: once the key is re-created under new
// tags, invalidating the old tag would delete an entry that never carried it.
func TestTagInvalidationUntagsTheOtherTags(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	ctx := context.Background()
	fetch := func(context.Context) (string, error) { return "value", nil }
	if _, err := store.Get(ctx, groupedKey{ID: "u1"}, fetch); err != nil {
		t.Fatal(err)
	}
	store.InvalidateTag("group:a")
	if store.Has(ctx, groupedKey{ID: "u1"}) {
		t.Fatal("the group invalidation missed the entry")
	}
	if _, err := store.Get(ctx, regroupedKey{ID: "u1"}, fetch); err != nil {
		t.Fatal(err)
	}
	store.InvalidateTag("user:u1")
	if !store.Has(ctx, regroupedKey{ID: "u1"}) {
		t.Errorf("an entry never tagged user:u1 was dropped by that tag")
	}
}

func TestSetWritesWithoutAFetch(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	ctx := context.Background()
	if err := store.Set(ctx, userKey{ID: "u1"}, "written"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, userKey{ID: "u1"}, func(context.Context) (string, error) {
		t.Error("the fetch ran despite a written entry")
		return "", nil
	})
	if err != nil || got != "written" {
		t.Errorf("got %q, %v, want written", got, err)
	}
}

// A nil store is the disabled deployment, and every operation has to fall
// through so that removing caching edits no call site.
func TestANilStoreFallsThroughToTheFetch(t *testing.T) {
	ctx := context.Background()
	// A nil pointer is a legal receiver, and the check is in each method.
	var store *CacheStore
	got, err := store.Get(ctx, userKey{ID: "u1"}, func(context.Context) (string, error) {
		return "direct", nil
	})
	if err != nil || got != "direct" {
		t.Errorf("got %q, %v, want direct", got, err)
	}
	if store.Has(ctx, userKey{ID: "u1"}) {
		t.Errorf("a nil store reported an entry")
	}
	if err := store.Set(ctx, userKey{ID: "u1"}, "x"); err != nil {
		t.Errorf("Set on a nil store: %v", err)
	}
	store.Invalidate(ctx, userKey{ID: "u1"})
	store.InvalidateScope("alice")
	store.InvalidateTag("user:u1")
}

func TestAStructuredValueSurvivesTheRoundTrip(t *testing.T) {
	type summary struct {
		Name  string   `json:"name"`
		Tags  []string `json:"tags"`
		Count int      `json:"count"`
	}
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	ctx := context.Background()
	want := summary{Name: "alice", Tags: []string{"a", "b"}, Count: 3}
	if _, err := store.Get(ctx, userKey{ID: "u1"}, func(context.Context) (summary, error) {
		return want, nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, userKey{ID: "u1"}, func(context.Context) (summary, error) {
		t.Error("the fetch ran on what should have been a hit")
		return summary{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestTheEntryCapEvicts(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public", MaxEntries: 4})
	ctx := context.Background()
	for page := range 10 {
		if _, err := store.Get(ctx, userKey{ID: "u1", Page: page}, func(context.Context) (string, error) {
			return "value", nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if entries := store.Stats().Entries; entries > 4 {
		t.Errorf("store holds %d entries, want at most 4", entries)
	}
}

// waitFor polls until the condition holds, so a test observing detached work
// does not sleep for a fixed interval it would have to guess.
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition never held")
}
