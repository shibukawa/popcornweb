package pwruntime

import (
	"context"
	"sync/atomic"
	"testing"
)

// The typed operations are methods on the store handle. The tests below cover
// the surface a handler reaches through the handle it resolved: a read that
// stores, a membership test that sees it, an overwrite the next read honours,
// and the two coarse invalidations.

func TestTheStoreMethodsShareOneEntrySet(t *testing.T) {
	store := testStore(t, CacheStoreConfig{Scope: "public"})
	ctx := context.Background()
	var calls atomic.Int64
	fetch := func(context.Context) (string, error) {
		calls.Add(1)
		return "value", nil
	}
	got, err := store.Get(ctx, userKey{ID: "u1"}, fetch)
	if err != nil || got != "value" {
		t.Fatalf("Get = %q, %v, want value", got, err)
	}
	if _, err := store.Get(ctx, userKey{ID: "u1"}, fetch); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Errorf("the fetch ran %d times, want 1", calls.Load())
	}
	if !store.Has(ctx, userKey{ID: "u1"}) {
		t.Errorf("Has did not see the entry Get stored")
	}
	if err := store.Set(ctx, userKey{ID: "u1"}, "written"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err = store.Get(ctx, userKey{ID: "u1"}, func(context.Context) (string, error) {
		t.Error("the fetch ran despite the entry Set wrote")
		return "", nil
	})
	if err != nil || got != "written" {
		t.Errorf("Get = %q, %v, want written", got, err)
	}
	store.Invalidate(ctx, userKey{ID: "u1"})
	if store.Has(ctx, userKey{ID: "u1"}) {
		t.Errorf("the entry survived Invalidate")
	}
}

func TestTheStoreMethodsInvalidateByScopeAndByTag(t *testing.T) {
	value := func(v string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return v, nil }
	}

	private := testStore(t, CacheStoreConfig{Scope: "private"})
	for _, subject := range []string{"alice", "bob"} {
		if _, err := private.Get(signedIn(subject), userKey{ID: "u1"}, value(subject)); err != nil {
			t.Fatal(err)
		}
	}
	private.InvalidateScope("alice")
	if private.Has(signedIn("alice"), userKey{ID: "u1"}) {
		t.Errorf("alice's entry survived InvalidateScope")
	}
	if !private.Has(signedIn("bob"), userKey{ID: "u1"}) {
		t.Errorf("bob's entry was dropped by alice's scope")
	}

	public := testStore(t, CacheStoreConfig{Scope: "public"})
	ctx := context.Background()
	for _, id := range []string{"u1", "u2"} {
		if _, err := public.Get(ctx, taggedKey{ID: id}, value("value")); err != nil {
			t.Fatal(err)
		}
	}
	public.InvalidateTag("user:u1")
	if public.Has(ctx, taggedKey{ID: "u1"}) {
		t.Errorf("the tagged entry survived InvalidateTag")
	}
	if !public.Has(ctx, taggedKey{ID: "u2"}) {
		t.Errorf("an entry the tag does not name was dropped")
	}
}
