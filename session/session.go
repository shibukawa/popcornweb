// Package session stores typed per-browser state. It knows nothing about
// login: what proved a session, how strongly, and for how long belongs to
// whatever owns authentication, which is normally popcornweb/plugin/auth.
//
// An application declares each piece of state once, as a Go type with a
// Placement, and reads it back by that type:
//
//	registry.Register[Cart]("cart", session.Private, nil)
//	cart, ok := session.Load[Cart](ctx)
//
// The Placement states what the client may do with the value and where its
// bytes live. Shared is a plain cookie the front end reads and writes,
// ReadOnly a signed one it may read, Private is sealed and moves from a cookie
// to the configured backend at the login rotation, ServerOnly is sealed and
// always on the server because it must stay revocable, and RequestScope lives
// in process memory for one request and is never persisted at all. A host may
// deliberately place anonymous Private state on its server too, as the dev
// memory backend does.
//
// The browser receives only a random token, issued lazily on the first write,
// so a visitor who writes nothing receives no cookie and occupies no storage.
// Handlers never observe the token, the key hash, the placement, or the backend
// client.
//
// Jar remains for one typed cookie deliberately kept outside the session, such
// as a sign-in hint that has to survive the logout it describes.
package session

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("session: record not found")
	ErrExpired        = errors.New("session: record expired")
	ErrCodec          = errors.New("session: codec failure")
	ErrUnavailable    = errors.New("session: backend unavailable")
	ErrInvalidOptions = errors.New("session: invalid options")
	ErrInvalidKey     = errors.New("session: invalid key")
	ErrNoSession      = errors.New("session: no session on request")
)

// Record is the stored session state. Stores persist it under a key hash and
// must treat every field as immutable once written.
type Record[T any] struct {
	// Data is the typed application payload.
	Data T
	// CreatedAt is when this record was written.
	CreatedAt time.Time
	// AuthenticatedAt is when the current authentication strength was
	// established. Rotation after login refreshes it.
	AuthenticatedAt time.Time
	// LastSeenAt is the last renewal timestamp.
	LastSeenAt time.Time
	// ExpiresAt is the authoritative absolute expiry.
	ExpiresAt time.Time
	// IdleExpiresAt is the optional inactivity expiry. It is never later than
	// ExpiresAt.
	IdleExpiresAt time.Time
	// Method records how the session was authenticated, such as oidc.
	Method string
	// Version invalidates records after an incompatible schema or policy
	// change.
	Version int
}

// Store persists session records behind one context-aware contract. Stores
// never accept or return the raw cookie token.
type Store[T any] interface {
	// Put replaces one key atomically.
	Put(ctx context.Context, keyHash string, record Record[T]) error
	// Get returns ErrNotFound for a missing key and ErrExpired for a record
	// past its absolute or idle expiry.
	Get(ctx context.Context, keyHash string) (Record[T], error)
	// Touch renews an existing record. It never revives a missing or expired
	// one.
	Touch(ctx context.Context, keyHash string, lastSeenAt, idleExpiresAt time.Time) error
	// Delete is idempotent.
	Delete(ctx context.Context, keyHash string) error
}

// RequestBinder is the optional contract of a Store whose records live in the
// browser instead of a backend, such as CookieStore. The Manager calls
// BindRequest before every store call it makes on behalf of a request, so the
// store can reach the cookie it reads and the response it writes.
//
// A backend store implements nothing here and is unaffected, which is what
// keeps one Manager, one Options, and one handler working over a cookie, an
// RDB, or a Redis store.
type RequestBinder interface {
	BindRequest(ctx context.Context, carrier Carrier) context.Context
}

// Codec serializes the typed payload of a record for durable stores. Record
// timestamps stay in backend fields so renewal never rewrites the payload.
// Implementations must use a bounded format and must not include payload
// contents in returned errors.
type Codec[T any] interface {
	Encode(value T) ([]byte, error)
	Decode(encoded []byte) (T, error)
}

// deadline is the earliest authoritative expiry of the record.
//
// A zero field means "no such bound" rather than "the epoch", so the earliest of
// the two is the earliest of those that are set. Reading ExpiresAt directly when
// it was unset returned the zero time — which every caller reads as "no deadline"
// — and silently discarded an idle bound that was configured and stamped. A
// session bounded only by inactivity then had no bound at all.
func (r Record[T]) deadline() time.Time {
	if r.ExpiresAt.IsZero() {
		return r.IdleExpiresAt
	}
	if !r.IdleExpiresAt.IsZero() && r.IdleExpiresAt.Before(r.ExpiresAt) {
		return r.IdleExpiresAt
	}
	return r.ExpiresAt
}
