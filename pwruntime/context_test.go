package pwruntime

import (
	"context"
	"testing"
)

func TestCapsuleParentChain(t *testing.T) {
	ctx := WithResources(context.Background(), Resources{})
	root := resources(ctx)
	if root.Parent() != nil {
		t.Fatal("request root capsule has a parent")
	}

	ctx = SelectDB(ctx, "reader")
	pinned := resources(ctx)
	if pinned.Parent() != root {
		t.Fatal("SelectDB capsule does not point at the capsule it was derived from")
	}

	ctx = WithLogAttributes(ctx, String("request_id", "r-1"))
	tagged := resources(ctx)
	if tagged.Parent() != pinned || tagged.Parent().Parent() != root {
		t.Fatal("capsule ancestry is not a pointer chain back to the root")
	}
	if tagged.Group != "reader" {
		t.Fatalf("derived capsule lost its state, group = %q", tagged.Group)
	}

	if (*Resources)(nil).Parent() != nil {
		t.Fatal("nil capsule Parent is not nil-safe")
	}
}

// A capsule with no per-request state is prepared once and shared: every
// request reads the same pointer, and a request that adds to it derives a copy
// rather than reaching the frozen root — which is what makes the sharing safe.
func TestPrepareResourcesSharesARequestIndependentCapsule(t *testing.T) {
	prepared := PrepareResources(Resources{})
	first := resources(prepared.Attach(context.Background()))
	second := resources(prepared.Attach(context.Background()))
	if first != second {
		t.Errorf("a request-independent capsule was re-prepared per request")
	}

	// One request's added attributes must not appear on the next: the write
	// travels through a derived copy, never through the shared root.
	ctx := WithLogAttributes(prepared.Attach(context.Background()), String("request_id", "r1"))
	if got := resources(ctx); len(got.LogAttributes) == 0 {
		t.Fatal("the derived capsule lost its attribute")
	}
	if fresh := resources(prepared.Attach(context.Background())); len(fresh.LogAttributes) != 0 {
		t.Errorf("an attribute added by one request reached the shared root: %v", fresh.LogAttributes)
	}
}

// A capsule that does need per-request state — the round-robin memo of a
// connection set — is prepared per request, so two requests never share one
// memo.
func TestPrepareResourcesKeepsPerRequestStatePerRequest(t *testing.T) {
	set, _ := newGroupedDB(t, "", Connection{Group: "main", Label: "main"})
	prepared := PrepareResources(Resources{Connections: set})
	first := resources(prepared.Attach(context.Background()))
	second := resources(prepared.Attach(context.Background()))
	if first == second {
		t.Fatal("two requests share one capsule despite a connection set")
	}
	if first.picked == nil || second.picked == nil {
		t.Fatal("a prepared capsule with a connection set is missing its memo")
	}
	if first.picked == second.picked {
		t.Errorf("two requests share one round-robin memo")
	}
}
