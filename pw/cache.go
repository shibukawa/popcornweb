package pw

import (
	"context"
	"net/http"

	"github.com/shibukawa/popcornweb/pwruntime"
)

// The data cache an application reaches from a handler.
//
// Every symbol here is the runtime's, re-exported so an application writes pw
// and links no transport it is not serving on. The operations are methods on
// the store handle — Get, Has, Set, Invalidate, InvalidateScope and
// InvalidateTag — and they reach this package through the CacheStore alias
// below rather than through a wrapper. They were package functions until the
// module moved to Go 1.27, the first release letting a method declare its own
// type parameter; the handle was resolved ahead of that on purpose, so the
// line that acquires a store did not change when the operations moved onto it.

type (
	// CacheStore is a handle to one configured store, resolved by name.
	CacheStore = pwruntime.CacheStore
	// CacheKey is what a Memo key type implements: the type's identity followed
	// by the framed encoding of every field marked with the cache tag.
	//
	// It is tinybind's own interface, so a key method that generator emitted
	// satisfies it directly. A hand-written key implements the same method and
	// frames its fields with the cachekeybind helpers; nothing is re-exported
	// here, because that package is stdlib-only and its helper set is wider
	// than a copy here would stay in step with.
	CacheKey = pwruntime.CacheKey
	// CacheTagger is the optional half of a key type, naming the tags whose
	// invalidation drops the entry.
	CacheTagger = pwruntime.CacheTagger
	// CacheStats is what one store has answered.
	CacheStats = pwruntime.CacheStats
)

// MemoStore resolves a configured store by name.
//
// A disabled cache section returns no store and no error, and every operation
// on a nil store falls through to its fetch, so a deployment removes caching
// without editing a call site. An unconfigured name is an error naming what is
// configured.
func MemoStore(r *http.Request, name string) (*CacheStore, error) {
	return pwruntime.MemoStore(r.Context(), name)
}

// MemoStoreContext is MemoStore for code below the handler, and for a
// resolution done once at startup rather than per request.
func MemoStoreContext(ctx context.Context, name string) (*CacheStore, error) {
	return pwruntime.MemoStore(ctx, name)
}
