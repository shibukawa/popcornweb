package pwruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shibukawa/tinybind-go/cachekeybind"
)

// The data cache: what a handler fetched, reused for equal typed keys.
//
// It is the other half of the render cache. That one stores a component's
// rendered bytes and never touches the query that produced its parameters;
// this one stores what the query returned. The two share the key framing and
// the private-by-default scope and nothing else, because their misses cost
// different things — a duplicate render costs local CPU, and a duplicate fetch
// costs an upstream call.

// CacheConfig is the named store set, configured as [[cache.stores]].
//
// A store is configured rather than constructed because it outlives every
// request, carries an operational size, and one day may address a process
// outside this one. That is the same set of properties a database pool has, so
// it is configured the way one is.
type CacheConfig struct {
	Enabled bool `default:"false" help:"reuse what a fetch returned for equal keys"`
	// Stores is the array-of-tables form. An element takes no CLI option and no
	// environment variable, because its identity is its position in the file.
	Stores []CacheStoreConfig `dependon:".enabled" help:"cache store set, one element per store"`
}

// CacheStoreConfig is one store of the set.
type CacheStoreConfig struct {
	// Name is what a call site addresses this store by.
	Name string `help:"name this store is addressed by"`
	// Backend names where entries live. Only memory is implemented; any other
	// value is refused rather than ignored, because a store that silently fell
	// back to memory would be a shared cache that is not shared.
	Backend string `default:"memory" help:"where entries live; memory is the only implemented backend"`
	// TTL is how long an entry is fresh.
	TTL time.Duration `default:"1m" help:"how long an entry is fresh"`
	// Stale is how long past fresh a held entry may still answer while one
	// revalidation runs. Zero disables the window.
	Stale time.Duration `default:"0s" help:"how long a stale entry may answer while it revalidates"`
	// Scope is private or public. Private prefixes every key with the reader's
	// identity, which is the default for the same reason it is the render
	// cache's: a shared entry holding one reader's data is the failure that
	// does not degrade.
	Scope string `default:"private" help:"private keys entries per reader; public shares them"`
	// MaxEntries bounds the in-process store. Zero or less is unbounded.
	MaxEntries int `default:"1024" help:"maximum entries this store holds"`
	// FetchTimeout bounds a coalesced fetch. It exists because the shared fetch
	// runs on a context detached from every waiter, so nothing else would ever
	// stop it and a hung upstream would leak one goroutine per cold key.
	FetchTimeout time.Duration `default:"30s" help:"bound on a fetch running detached from its waiters"`
}

// CacheKey is what a Memo key type implements: the type's identity followed by
// the framed encoding of every field marked with the cache tag.
//
// It is system:tinybind's interface rather than one of ours, so a key method
// emitted by that generator satisfies this without an adapter. A key may also
// be written by hand against the same package.
//
// It is a method rather than a shape this framework walks because walking would
// mean reflection, and because the set of fields an entry depends on is exactly
// what has to be visible for the cache to be correct.
type CacheKey = cachekeybind.CacheKey

// CacheTagger is the optional half of a key type: the tags whose invalidation
// drops this entry. It is ours rather than the generator's, because a tag is an
// invalidation policy and the key method is an identity.
//
// A key type implementing nothing is invalidated by key or by scope alone.
type CacheTagger interface {
	CacheTags() []string
}

// cacheEntry is one stored result.
//
// It holds two deadlines rather than one because a stale window is the
// difference between an upstream outage degrading a page and taking it down.
type cacheEntry struct {
	value []byte
	fresh time.Time
	stale time.Time
	tags  []string
}

// CacheStore is a handle to one configured store.
//
// It holds no request state, so one handle serves every request and may be
// resolved once at setup or per call. The scope and the trace come from the
// context passed to each operation instead.
//
// The typed operations are package functions rather than methods because a Go
// method may not declare its own type parameters. When that changes they become
// Get, Has, and Set on this type, and no call site that already holds a handle
// has to move. cache_go127.go carries them written already, behind the build tag
// for the release expected to allow them.
type CacheStore struct {
	name    string
	ttl     time.Duration
	stale   time.Duration
	scoped  bool
	timeout time.Duration

	mu      sync.RWMutex
	entries map[string]cacheEntry
	// order approximates insertion age. Eviction pops from the front by moving
	// head, and a key already removed keeps its slot and is skipped.
	order []string
	head  int
	max   int
	// tagged maps one tag to the keys carrying it, so tag invalidation is a
	// lookup rather than a scan. It is the reason this store does not reuse the
	// render cache's interface, which has no reverse index.
	tagged map[string]map[string]struct{}

	// flights coalesces concurrent misses on one key.
	flights sync.Map

	hits      atomic.Int64
	misses    atomic.Int64
	coalesced atomic.Int64
	staleHits atomic.Int64

	now func() time.Time
}

// Name reports which configured store this handle addresses.
func (s *CacheStore) Name() string { return s.name }

// CacheStats is what one store has answered, for a diagnostic or a test.
type CacheStats struct {
	Hits      int64
	Misses    int64
	Coalesced int64
	StaleHits int64
	Entries   int
}

// Stats reports this store's counters. A hit count alone cannot distinguish a
// working cache from one nothing is eligible for, so every counter is reported
// together.
func (s *CacheStore) Stats() CacheStats {
	if s == nil {
		return CacheStats{}
	}
	s.mu.Lock()
	entries := len(s.entries)
	s.mu.Unlock()
	return CacheStats{
		Hits:      s.hits.Load(),
		Misses:    s.misses.Load(),
		Coalesced: s.coalesced.Load(),
		StaleHits: s.staleHits.Load(),
		Entries:   entries,
	}
}

// key assembles the stored key, or reports that this request stores nothing.
//
// The scope is prepended so one reader's entries share a prefix that scope
// invalidation can delete as a range. The build identity follows it, which is
// what keeps a rebuilt binary from answering out of entries the previous code
// wrote — the render cache gets that from a digest of its generated plan, and a
// hand-written fetch compiles to nothing that could be digested.
//
// A private store reached without a reader stores nothing rather than storing
// under a blank scope, because an entry under an empty identity is a shared
// entry wearing a private label.
func (s *CacheStore) key(ctx context.Context, k CacheKey) (string, bool) {
	scope := ""
	if s.scoped {
		scope = RequestAuthentication(ctx).Subject
		if scope == "" {
			return "", false
		}
	}
	return cachekeybind.KeyString(scope) + cachekeybind.KeyString(UpdateBuildID()) + k.CacheKey(), true
}

// scopePrefix is what every entry belonging to one reader starts with.
func (s *CacheStore) scopePrefix(scope string) string { return cachekeybind.KeyString(scope) }

func (s *CacheStore) get(key string) (cacheEntry, bool) {
	// The overwhelmingly common answer is a pure read — a hit, or a plain
	// miss — so concurrent readers of one store share a read lock and only an
	// entry past its stale deadline pays for the write lock that removes it.
	s.mu.RLock()
	entry, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok {
		return cacheEntry{}, false
	}
	// The stale deadline is the last moment this entry may answer at all. Past
	// it the entry is gone, whether or not a revalidation ever ran.
	if !s.now().Before(entry.stale) {
		s.mu.Lock()
		// Re-read under the write lock: another goroutine may have removed or
		// replaced the entry between the two acquisitions.
		if current, still := s.entries[key]; still && !s.now().Before(current.stale) {
			s.removeLocked(key)
		}
		s.mu.Unlock()
		return cacheEntry{}, false
	}
	return entry, true
}

func (s *CacheStore) put(key string, value []byte, tags []string) {
	now := s.now()
	entry := cacheEntry{value: value, fresh: now.Add(s.ttl), stale: now.Add(s.ttl + s.stale), tags: tags}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.entries[key]; !exists {
		s.order = append(s.order, key)
	} else {
		s.untagLocked(key)
	}
	s.entries[key] = entry
	for _, tag := range tags {
		keys := s.tagged[tag]
		if keys == nil {
			keys = map[string]struct{}{}
			s.tagged[tag] = keys
		}
		keys[key] = struct{}{}
	}
	// Insertion order approximates age well enough here and costs nothing on a
	// hit, which a true LRU would not.
	for s.max > 0 && len(s.entries) > s.max && s.head < len(s.order) {
		oldest := s.order[s.head]
		s.head++
		s.removeLocked(oldest)
	}
	s.compactLocked()
}

// removeLocked drops one entry and its tag references. The caller holds the lock.
func (s *CacheStore) removeLocked(key string) {
	s.untagLocked(key)
	delete(s.entries, key)
}

// untagLocked removes this key from every tag index it appears in.
func (s *CacheStore) untagLocked(key string) {
	entry, ok := s.entries[key]
	if !ok {
		return
	}
	for _, tag := range entry.tags {
		keys := s.tagged[tag]
		if keys == nil {
			continue
		}
		delete(keys, key)
		if len(keys) == 0 {
			delete(s.tagged, tag)
		}
	}
}

// compactLocked rebuilds order once most of it no longer names a live entry, so
// the queue cannot grow past the map it approximates.
func (s *CacheStore) compactLocked() {
	if len(s.order)-s.head <= len(s.entries)*2+16 {
		return
	}
	kept := s.order[:0]
	for _, key := range s.order[s.head:] {
		if _, ok := s.entries[key]; ok {
			kept = append(kept, key)
		}
	}
	s.order, s.head = kept, 0
}

func (s *CacheStore) delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(key)
}

func (s *CacheStore) deletePrefix(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.entries {
		if strings.HasPrefix(key, prefix) {
			s.removeLocked(key)
		}
	}
	s.compactLocked()
}

func (s *CacheStore) deleteTag(tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// removeLocked rather than a bare map delete, because an entry may carry
	// more than one tag: dropped without untagging, its key would stay in the
	// other tags' indexes, and a later invalidation of one of those would
	// delete whatever entry had reused the key since — an entry that never
	// carried the invalidated tag at all.
	for key := range s.tagged[tag] {
		s.removeLocked(key)
	}
	delete(s.tagged, tag)
	s.compactLocked()
}

// cacheFlight is one fetch several callers are waiting on.
type cacheFlight struct {
	done  chan struct{}
	value []byte
	err   error
}

// do runs fetch once for this key however many callers miss on it at once.
//
// The fetch runs on a context detached from every waiter. That is the whole
// point: the render cache deliberately does not coalesce, on the ground that
// coalescing would tie one request's cancellation to another request's work,
// and detaching is precisely the removal of that reach. A waiter that goes away
// stops waiting and stops nothing else.
//
// Detaching keeps context values — the reader identity a private key was built
// from, and the trace — and loses only the lifetime, which is why the store
// supplies a timeout of its own.
// The fetch stores its own result rather than leaving that to whoever was
// waiting. A waiter may cancel — that is the point of detaching — and if the
// store happened after the wait, a fetch every waiter abandoned would run to
// completion and throw its result away, which is the one outcome detaching was
// supposed to prevent.
//
// The order is store, then deregister, then wake. A caller arriving in the gap
// then finds either the flight or the entry, and never a window with neither.
func (s *CacheStore) do(ctx context.Context, key string, tags []string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	flight := &cacheFlight{done: make(chan struct{})}
	existing, loaded := s.flights.LoadOrStore(key, flight)
	if loaded {
		flight = existing.(*cacheFlight)
		s.coalesced.Add(1)
	} else {
		// Run in its own goroutine so the starter waits exactly as every other
		// caller does. A starter that ran the fetch inline would be the one
		// caller whose cancellation could not be honoured.
		go func() {
			defer func() {
				s.flights.Delete(key)
				close(flight.done)
			}()
			// Read the store again before fetching. A caller misses, and only
			// then registers this flight, so a previous flight can finish and
			// deregister itself in between — and without this check that caller
			// fetches a value another one has already stored.
			if entry, found := s.get(key); found && s.now().Before(entry.fresh) {
				flight.value = entry.value
				return
			}
			detached := context.WithoutCancel(ctx)
			if s.timeout > 0 {
				var cancel context.CancelFunc
				detached, cancel = context.WithTimeout(detached, s.timeout)
				defer cancel()
			}
			flight.value, flight.err = fetch(detached)
			if flight.err == nil {
				s.put(key, flight.value, tags)
			}
		}()
	}
	select {
	case <-flight.done:
		return flight.value, flight.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// revalidate refreshes a stale entry in the background, coalesced like a miss
// and waited on by nobody. The fetch stores its own result, so this starts the
// work and keeps no reference to it.
func (s *CacheStore) revalidate(ctx context.Context, key string, tags []string, fetch func(context.Context) ([]byte, error)) {
	if _, busy := s.flights.Load(key); busy {
		return
	}
	go s.do(context.WithoutCancel(ctx), key, tags, fetch) //nolint:errcheck // nobody is waiting on a revalidation
}

// Get returns what fetch produced for this key, reusing a stored result while
// it is fresh and coalescing concurrent misses onto one fetch.
//
// A nil store, or a private store reached by an anonymous request, calls fetch
// and returns. No call site branches on whether caching is on: a nil pointer is
// a legal receiver, and the check is here.
//
// The fetch receives a context detached from every waiter, which is what lets
// one request's cancellation leave the shared work alone. Do not capture the
// request context inside the closure instead: that would pin the fetch to
// whichever caller happened to miss first.
func (s *CacheStore) Get[K CacheKey, T any](ctx context.Context, key K, fetch func(context.Context) (T, error)) (T, error) {
	if s == nil {
		return fetch(ctx)
	}
	stored, ok := s.key(ctx, key)
	if !ok {
		return fetch(ctx)
	}
	tags := cacheTagsOf(key)
	encode := func(ctx context.Context) ([]byte, error) {
		value, err := fetch(ctx)
		if err != nil {
			// Never stored: an error is not a result, and a stored one would be
			// replayed for the whole TTL.
			return nil, err
		}
		return json.Marshal(value)
	}
	if entry, found := s.get(stored); found {
		if s.now().Before(entry.fresh) {
			if value, err := decodeCached[T](entry.value); err == nil {
				s.hits.Add(1)
				return value, nil
			}
			// A value this build cannot read came from another one. Treat it as
			// a miss rather than as an error the caller has to handle.
			s.delete(stored)
		} else {
			if value, err := decodeCached[T](entry.value); err == nil {
				s.staleHits.Add(1)
				s.revalidate(ctx, stored, tags, encode)
				return value, nil
			}
			s.delete(stored)
		}
	}
	s.misses.Add(1)
	encoded, err := s.do(ctx, stored, tags, encode)
	if err != nil {
		var zero T
		return zero, err
	}
	return decodeCached[T](encoded)
}

// Has reports whether this key currently has a fresh entry. A stale one
// answers false, because the useful question here is whether the held value is
// current rather than whether a read would block.
//
// It is racy by nature — the entry may expire between the answer and the next
// read — so it answers a diagnostic or a decision to skip expensive work, never
// control flow that assumes the following read hits.
func (s *CacheStore) Has[K CacheKey](ctx context.Context, key K) bool {
	if s == nil {
		return false
	}
	stored, ok := s.key(ctx, key)
	if !ok {
		return false
	}
	entry, found := s.get(stored)
	return found && s.now().Before(entry.fresh)
}

// Set writes an entry without consulting one, which is how a writer refreshes
// what it just made wrong.
//
// It bypasses the fetch and with it the coalescing, so not storing an error is
// the caller's to keep here. The lifetime comes from the store, so a call
// cannot mint a longer-lived entry than the configuration allows.
func (s *CacheStore) Set[K CacheKey, T any](ctx context.Context, key K, value T) error {
	if s == nil {
		return nil
	}
	stored, ok := s.key(ctx, key)
	if !ok {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("cache %s: encode: %w", s.name, err)
	}
	s.put(stored, encoded, cacheTagsOf(key))
	return nil
}

// Invalidate drops one entry, taking the key the read took.
func (s *CacheStore) Invalidate[K CacheKey](ctx context.Context, key K) {
	if s == nil {
		return
	}
	if stored, ok := s.key(ctx, key); ok {
		s.delete(stored)
	}
}

// InvalidateScope drops everything one reader holds, which the prepended
// scope makes a prefix rather than a scan.
func (s *CacheStore) InvalidateScope(scope string) {
	if s == nil || !s.scoped {
		return
	}
	s.deletePrefix(s.scopePrefix(scope))
}

// InvalidateTag drops everything a tag names.
//
// It is the axis a prefix cannot serve: the scope comes first, so every entry
// of one key type across all readers is not a range. A tag is the reverse index
// that answers it.
func (s *CacheStore) InvalidateTag(tag string) {
	if s == nil {
		return
	}
	s.deleteTag(tag)
}

func cacheTagsOf(key any) []string {
	if tagger, ok := key.(CacheTagger); ok {
		return tagger.CacheTags()
	}
	return nil
}

func decodeCached[T any](data []byte) (T, error) {
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		var zero T
		return zero, err
	}
	return value, nil
}
