package firestore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/authstate"
	"github.com/shibukawa/popcornweb/internal/firestoretest"
	"github.com/shibukawa/tinybind-go/firestorebind"
	"github.com/shibukawa/tinygodriver/cloud/google"
	"github.com/shibukawa/tinygodriver/nosql/datastore"
)

func newStore(t *testing.T, options ...func(*Options)) (context.Context, *Store, *firestoretest.Server) {
	t.Helper()
	fake := firestoretest.New(t)
	client, err := datastore.New("demo",
		datastore.WithEndpoint(fake.Endpoint()),
		datastore.WithTokenSource(google.StaticTokenSource(google.Token{Value: "test"})),
		datastore.WithRetry(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	settings := Options{Namespace: "passkey"}
	for _, option := range options {
		option(&settings)
	}
	store, err := NewRawStore(settings)
	if err != nil {
		t.Fatal(err)
	}
	return firestorebind.WithClient(context.Background(), client), store, fake
}

func TestPutTakeRoundTrip(t *testing.T) {
	ctx, store, _ := newStore(t)
	if err := store.Put(ctx, "challenge", []byte("state"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	payload, err := store.Take(ctx, "challenge")
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "state" {
		t.Errorf("payload: got %q", payload)
	}
}

func TestTakeConsumesTheRecord(t *testing.T) {
	ctx, store, _ := newStore(t)
	if err := store.Put(ctx, "challenge", []byte("state"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Take(ctx, "challenge"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Take(ctx, "challenge"); !errors.Is(err, authstate.ErrNotFound) {
		t.Fatalf("second take: got %v, want ErrNotFound", err)
	}
}

// The single-use guarantee. Two takes of one key must not both succeed, however
// they interleave.
func TestTwoConcurrentTakesReturnTheValueExactlyOnce(t *testing.T) {
	ctx, store, _ := newStore(t)
	if err := store.Put(ctx, "challenge", []byte("state"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	var group sync.WaitGroup
	results := make([][]byte, 2)
	failures := make([]error, 2)
	start := make(chan struct{})
	for index := range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results[index], failures[index] = store.Take(ctx, "challenge")
		}()
	}
	close(start)
	group.Wait()

	won := 0
	for index := range 2 {
		if failures[index] == nil {
			won++
			if string(results[index]) != "state" {
				t.Errorf("winner %d got %q", index, results[index])
			}
			continue
		}
		if !errors.Is(failures[index], authstate.ErrNotFound) {
			t.Errorf("loser %d: got %v, want ErrNotFound", index, failures[index])
		}
	}
	if won != 1 {
		t.Fatalf("%d of 2 takes succeeded; exactly one must", won)
	}
}

func TestPutRefusesAnUnexpiredCollisionWithoutOverwriting(t *testing.T) {
	ctx, store, _ := newStore(t)
	if err := store.Put(ctx, "challenge", []byte("first"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	err := store.Put(ctx, "challenge", []byte("second"), time.Now().Add(time.Minute))
	if !errors.Is(err, authstate.ErrAlreadyExists) {
		t.Fatalf("got %v, want ErrAlreadyExists", err)
	}
	payload, err := store.Take(ctx, "challenge")
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "first" {
		t.Errorf("the refused write overwrote the record: %q", payload)
	}
}

// An expired record does not hold its key. The replacement costs a transaction,
// and only a real collision pays for it.
func TestPutOverAnExpiredRecordSucceeds(t *testing.T) {
	now := time.Now()
	clock := now
	ctx, store, fake := newStore(t, func(o *Options) { o.Now = func() time.Time { return clock } })

	if err := store.Put(ctx, "challenge", []byte("first"), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	commits := fake.Calls("commit")
	clock = now.Add(time.Hour)
	if err := store.Put(ctx, "challenge", []byte("second"), clock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if fake.Calls("commit") <= commits {
		t.Error("the replacement wrote nothing")
	}
	payload, err := store.Take(ctx, "challenge")
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "second" {
		t.Errorf("payload: got %q, want the replacement", payload)
	}
}

func TestTakeAfterExpiryReturnsTheContractErrorAndLeavesNothing(t *testing.T) {
	now := time.Now()
	clock := now
	ctx, store, fake := newStore(t, func(o *Options) { o.Now = func() time.Time { return clock } })

	if err := store.Put(ctx, "challenge", []byte("state"), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(time.Hour)
	if _, err := store.Take(ctx, "challenge"); !errors.Is(err, authstate.ErrExpired) {
		t.Fatalf("got %v, want ErrExpired", err)
	}
	// The record stays consumed. It was single-use, and handing it back would
	// be worse than losing it.
	if count := fake.Len(DeclaredKind); count != 0 {
		t.Errorf("%d expired records survived the take", count)
	}
}

func TestPutValidatesItsArguments(t *testing.T) {
	ctx, store, fake := newStore(t)
	now := time.Now()
	if err := store.Put(ctx, "", []byte("state"), now.Add(time.Minute)); !errors.Is(err, authstate.ErrInvalidKey) {
		t.Errorf("empty key: got %v", err)
	}
	if err := store.Put(ctx, "k", []byte("state"), now.Add(-time.Minute)); !errors.Is(err, authstate.ErrInvalidExpiry) {
		t.Errorf("past expiry: got %v", err)
	}
	if err := store.Put(ctx, "k", nil, now.Add(time.Minute)); !errors.Is(err, authstate.ErrCodec) {
		t.Errorf("empty payload: got %v", err)
	}
	if err := store.Put(ctx, "k", make([]byte, hardMaxValueBytes+1), now.Add(time.Minute)); !errors.Is(err, authstate.ErrLimitExceeded) {
		t.Errorf("oversized payload: got %v", err)
	}
	if fake.Calls("commit") != 0 {
		t.Error("an invalid argument reached the service")
	}
}

func TestARawStoreNeedsANamespace(t *testing.T) {
	if _, err := NewRawStore(Options{}); !errors.Is(err, authstate.ErrInvalidOptions) {
		t.Fatalf("got %v, want ErrInvalidOptions", err)
	}
	// The separator is what keeps a namespace and key pair from colliding with
	// another, so a namespace carrying one is refused.
	if _, err := NewRawStore(Options{Namespace: "pass:key"}); !errors.Is(err, authstate.ErrInvalidOptions) {
		t.Fatalf("namespace with a separator: got %v", err)
	}
}

// Two protocols share the kind and never see each other's keys.
func TestTwoNamespacesDoNotCollide(t *testing.T) {
	ctx, passkey, _ := newStore(t)
	oidc, err := NewRawStore(Options{Namespace: "oidc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := passkey.Put(ctx, "shared", []byte("passkey"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := oidc.Put(ctx, "shared", []byte("oidc"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := passkey.Take(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "passkey" {
		t.Errorf("passkey namespace read %q", got)
	}
}

func TestWithoutAClientTheStoreIsUnavailable(t *testing.T) {
	store, err := NewRawStore(Options{Namespace: "passkey"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "k", []byte("v"), time.Now().Add(time.Minute)); !errors.Is(err, authstate.ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
}

// The typed wrapper is what a caller owning a codec uses; the raw store is what
// a backend supplies for a type it cannot name.
func TestTypedStoreRoundTrip(t *testing.T) {
	ctx, _, _ := newStore(t)
	typed, err := NewStore[string](stringCodec{}, Options{Namespace: "oauth"})
	if err != nil {
		t.Fatal(err)
	}
	if err := typed.Put(ctx, "k", "value", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := typed.Take(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if got != "value" {
		t.Errorf("got %q", got)
	}
}

type stringCodec struct{}

func (stringCodec) Encode(value string) ([]byte, error) { return []byte(value), nil }
func (stringCodec) Decode(raw []byte) (string, error)   { return string(raw), nil }
