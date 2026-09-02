package session

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Registry holds every piece of per-browser state an application declared.
//
// A slot is declared once, as a Go type with a Placement, and read back by that
// type. The type is the key, so a misspelled name is a compile error rather
// than a missing value, and a package reads its own state without importing the
// package that owns the layout. Two packages wanting one slot share the type,
// which makes the sharing visible in the import graph.
//
// Registration happens at startup, before any request decodes anything. A
// Registry is safe for concurrent use once a Manager has been built over it;
// registering after that point is refused.
type Registry struct {
	mu     sync.RWMutex
	byType map[reflect.Type]*slot
	byKey  map[string]*slot
	frozen bool

	// snapshot is the immutable byType map published at freeze, so per-request
	// lookups after that point take no lock.
	snapshot atomic.Pointer[map[reflect.Type]*slot]
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		byType: map[reflect.Type]*slot{},
		byKey:  map[string]*slot{},
	}
}

// slot is one registered piece of state. The generic parts of a slot are
// captured as closures at registration, so the manager can carry every slot in
// one non-generic map.
type slot struct {
	key       string
	placement Placement
	typ       reflect.Type

	// index is this slot's position in the frozen order, assigned at freeze. It
	// is what lets a request keep its slot values in a slice instead of a map
	// keyed by reflect.Type.
	index int

	// encode and decode move the typed value across the storage boundary. They
	// close over the slot's codec.
	encode func(any) ([]byte, error)
	decode func([]byte) (any, error)

	// newCookie builds the per-slot browser cookie of a cookie-placed slot. It
	// is nil for Private and ServerOnly, which share the session record.
	newCookie func(JarOptions) (cookieSlot, error)

	// expiry bounds this slot on its own, shorter than the session that carries
	// it. Zero leaves it bounded by the session alone.
	expiry time.Duration
	// outlives exempts the slot from the destruction of the session. Only a
	// cookie-placed slot can set it.
	outlives bool
	// resetOnRotate drops the slot at a rotation rather than carrying it, for a
	// value whose whole point is to change when the session does.
	resetOnRotate bool
}

// cookieSlot is the non-generic view of one cookie-placed slot's Jar. load
// takes the already-extracted cookie value, so the middleware parses the
// Cookie header once for every jar-backed slot instead of once per slot.
type cookieSlot interface {
	load(value string) (any, error)
	save(Carrier, any) error
	clear(Carrier)
}

// jarSlot adapts a typed Jar to cookieSlot.
type jarSlot[T any] struct{ jar *Jar[T] }

func (s jarSlot[T]) load(value string) (any, error) {
	typed, err := s.jar.loadValue(value)
	if err != nil {
		return nil, err
	}
	return typed, nil
}

func (s jarSlot[T]) save(carrier Carrier, value any) error {
	typed, ok := value.(T)
	if !ok {
		return fmt.Errorf("%w: slot value type", ErrCodec)
	}
	return s.jar.SaveTo(carrier, typed)
}

func (s jarSlot[T]) clear(carrier Carrier) { s.jar.ClearFrom(carrier) }

// Register declares one piece of per-browser state.
//
// key is the browser cookie name for a cookie-placed slot, and the field name
// inside the session record for a server-placed one. codec may be nil, which
// uses JSONCodec[T]. The type is inferred from a non-nil codec; a slot taking
// the default codec passes nil and writes the type:
//
//	registry.Register[Account]("account", session.Private, nil)
//
// Call it from main, after every package init has run, exactly as
// RegisterConfig requires: the registry must be complete before the first
// request decodes anything, and an init-time call cannot see the configuration
// that places it.
//
// A duplicate Go type and a duplicate key are each an error rather than a
// silent replacement.
func (registry *Registry) Register[T any](key string, placement Placement, codec Codec[T], options ...SlotOption) error {
	if registry == nil {
		return fmt.Errorf("%w: nil registry", ErrInvalidOptions)
	}
	if !placement.valid() {
		return fmt.Errorf("%w: placement of slot %q", ErrInvalidOptions, key)
	}
	if !validCookieName(key) {
		// The key names a cookie for a cookie-placed slot, so one character set
		// serves both placements and a slot cannot be moved between them by
		// renaming.
		return fmt.Errorf("%w: slot key %q", ErrInvalidOptions, key)
	}
	if codec == nil {
		codec = JSONCodec[T]{}
	}
	typ := reflect.TypeFor[T]()

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.frozen {
		return fmt.Errorf("%w: slot %q registered after the session manager was built", ErrInvalidOptions, key)
	}
	if existing, ok := registry.byType[typ]; ok {
		return fmt.Errorf("%w: type %s is already registered as slot %q", ErrInvalidOptions, typ, existing.key)
	}
	if existing, ok := registry.byKey[key]; ok {
		return fmt.Errorf("%w: slot key %q is already registered for type %s", ErrInvalidOptions, key, existing.typ)
	}

	entry := &slot{
		key:       key,
		placement: placement,
		typ:       typ,
		encode: func(value any) ([]byte, error) {
			typed, ok := value.(T)
			if !ok {
				return nil, fmt.Errorf("%w: slot value type", ErrCodec)
			}
			return codec.Encode(typed)
		},
		decode: func(encoded []byte) (any, error) {
			value, err := codec.Decode(encoded)
			if err != nil {
				return nil, err
			}
			return value, nil
		},
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(entry); err != nil {
			return err
		}
	}
	if placement.cookiePlaced() {
		entry.newCookie = func(options JarOptions) (cookieSlot, error) {
			jar, err := NewJar[T](codec, options)
			if err != nil {
				return nil, err
			}
			return jarSlot[T]{jar: jar}, nil
		}
	}
	registry.byType[typ] = entry
	registry.byKey[key] = entry
	return nil
}

// lookup returns the slot registered for T.
func (r *Registry) lookup(typ reflect.Type) (*slot, bool) {
	// After freeze the registry is immutable, so the published snapshot answers
	// per-request lookups without touching the lock.
	if snapshot := r.snapshot.Load(); snapshot != nil {
		entry, ok := (*snapshot)[typ]
		return entry, ok
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.byType[typ]
	return entry, ok
}

// freeze closes the registry to further registration and returns its slots in a
// stable order, so a record encodes identically across processes.
func (r *Registry) freeze() []*slot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
	slots := make([]*slot, 0, len(r.byKey))
	for _, entry := range r.byKey {
		slots = append(slots, entry)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].key < slots[j].key })
	// The key order is deterministic, so a second freeze assigns the same
	// indexes and an existing manager keeps reading the right elements.
	for index, entry := range slots {
		entry.index = index
	}
	frozen := make(map[reflect.Type]*slot, len(r.byType))
	for typ, entry := range r.byType {
		frozen[typ] = entry
	}
	r.snapshot.Store(&frozen)
	return slots
}

// needsKeyring reports whether any registered slot lives in a protected
// browser cookie. ReadOnly always does; Private does during its anonymous
// phase unless the host deliberately places that phase on the server. Shared
// protects nothing, while ServerOnly and RequestScope never need a cookie key.
func (r *Registry) needsKeyring(serverSideAnonymous bool) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.byKey {
		if entry.placement == ReadOnly || (!serverSideAnonymous && entry.placement == Private) {
			return true
		}
	}
	return false
}

// hasServerOnly reports whether any registered slot demands a revocable
// placement, which the cookie backend cannot provide.
func (r *Registry) hasServerOnly() (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, 1)
	for key, entry := range r.byKey {
		if entry.placement == ServerOnly {
			names = append(names, key)
		}
	}
	if len(names) == 0 {
		return "", false
	}
	sort.Strings(names)
	return names[0], true
}
