// Package redis stores Popcorn Web login sessions in Redis or Valkey.
//
// The store owns its own key space under a configured prefix and never scans
// or enumerates keys. Expiry is the server's: every record is written with a
// TTL, so a session that is never revoked disappears without a sweep.
package redis

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/shibukawa/popcornweb/session"
)

const (
	// DefaultKeyPrefix namespaces the keys owned by this package.
	DefaultKeyPrefix = "pw:session:"

	// recordFormat versions the encoded record. A record written by another
	// version is rejected rather than reinterpreted.
	recordFormat = 1
	// headerBytes is the fixed-width part of a record: format, five
	// timestamps, the payload version, and the method length.
	headerBytes = 1 + 5*8 + 4 + 1

	defaultMaxPayloadBytes = 64 << 10
	hardMaxPayloadBytes    = 1 << 20
	maxPrefixBytes         = 128
	maxMethodBytes         = 64
	keyHashLength          = 64
)

// Options bounds record size and selects the owned key space.
type Options struct {
	// KeyPrefix isolates these keys from every other user of the server. It
	// defaults to DefaultKeyPrefix.
	KeyPrefix       string
	Now             func() time.Time
	MaxPayloadBytes int
}

// commandClient is the subset of the client this store uses. Narrowing it
// keeps the store testable and documents the commands a deployment must allow.
type commandClient interface {
	Get(ctx context.Context, key string) *goredis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *goredis.StatusCmd
	SetXX(ctx context.Context, key string, value any, expiration time.Duration) *goredis.BoolCmd
	Del(ctx context.Context, keys ...string) *goredis.IntCmd
}

// Store is a Redis or Valkey backed session.RawStore. The caller owns client
// and is responsible for closing it.
type Store struct {
	client          commandClient
	prefix          string
	now             func() time.Time
	maxPayloadBytes int
}

// NewStore constructs a Store over client. Redis and Valkey are both accepted;
// the store uses only GET, SET with an expiry, SET XX, and DEL. Wrap the result
// with session.Typed to give a Manager the payload type it stores.
func NewStore(client goredis.UniversalClient, options Options) (*Store, error) {
	return newStore(client, options)
}

func newStore(client commandClient, options Options) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: nil dependency", session.ErrInvalidOptions)
	}
	prefix := options.KeyPrefix
	if prefix == "" {
		prefix = DefaultKeyPrefix
	}
	if !validPrefix(prefix) {
		return nil, fmt.Errorf("%w: key prefix", session.ErrInvalidOptions)
	}
	if options.MaxPayloadBytes < 0 || options.MaxPayloadBytes > hardMaxPayloadBytes {
		return nil, fmt.Errorf("%w: bounds", session.ErrInvalidOptions)
	}
	maxPayloadBytes := options.MaxPayloadBytes
	if maxPayloadBytes == 0 {
		maxPayloadBytes = defaultMaxPayloadBytes
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Store{
		client:          client,
		prefix:          prefix,
		now:             now,
		maxPayloadBytes: maxPayloadBytes,
	}, nil
}

// KeyPrefix reports the owned key space.
func (s *Store) KeyPrefix() string { return s.prefix }

// Put replaces one key atomically. The TTL comes from the record deadline, so
// the server forgets a session at the same moment the framework would.
func (s *Store) Put(ctx context.Context, keyHash string, record session.RawRecord) error {
	if !s.ready() || ctx == nil {
		return fmt.Errorf("%w: store", session.ErrInvalidOptions)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validKeyHash(keyHash) {
		return session.ErrInvalidKey
	}
	encoded, ttl, err := s.encode(record)
	if err != nil {
		return err
	}
	if err := s.client.Set(ctx, s.prefix+keyHash, encoded, ttl).Err(); err != nil {
		return unavailable(ctx)
	}
	return nil
}

// Get returns the stored record. Stored expiry is authoritative, so a record
// the server has not collected yet is still reported as expired.
func (s *Store) Get(ctx context.Context, keyHash string) (session.RawRecord, error) {
	var zero session.RawRecord
	if !s.ready() || ctx == nil {
		return zero, fmt.Errorf("%w: store", session.ErrInvalidOptions)
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !validKeyHash(keyHash) {
		return zero, session.ErrInvalidKey
	}
	record, _, err := s.load(ctx, keyHash)
	if err != nil {
		return zero, err
	}
	return record, nil
}

// Touch renews an existing record. It never revives a missing or expired one
// and never renews past the absolute expiry.
func (s *Store) Touch(ctx context.Context, keyHash string, lastSeenAt, idleExpiresAt time.Time) error {
	if !s.ready() || ctx == nil {
		return fmt.Errorf("%w: store", session.ErrInvalidOptions)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validKeyHash(keyHash) {
		return session.ErrInvalidKey
	}
	record, payload, err := s.load(ctx, keyHash)
	if err != nil {
		// A renewal of a record that is gone or finished is not a failure of
		// the request; it is a session that ended.
		if errors.Is(err, session.ErrExpired) {
			return session.ErrNotFound
		}
		return err
	}
	// The zero-time guard matches the other stores: a record carrying only an
	// idle bound has no absolute deadline to renew past.
	if !record.ExpiresAt.IsZero() && idleExpiresAt.After(record.ExpiresAt) {
		return session.ErrNotFound
	}
	record.LastSeenAt = lastSeenAt
	record.IdleExpiresAt = idleExpiresAt
	encoded, ttl, err := s.encodeWithPayload(record, payload)
	if err != nil {
		return err
	}
	// XX keeps a renewal from recreating a key that expired between the read
	// and the write.
	renewed, err := s.client.SetXX(ctx, s.prefix+keyHash, encoded, ttl).Result()
	if err != nil {
		return unavailable(ctx)
	}
	if !renewed {
		return session.ErrNotFound
	}
	return nil
}

// TouchRecord is Touch with the record already in hand, saving the GET Touch
// opens with: a renewal follows the read that produced record, so that round
// trip re-answered what the caller just read. The same SET XX keeps the
// no-revival guarantee — a key that expired in between refuses the write.
func (s *Store) TouchRecord(ctx context.Context, keyHash string, record session.RawRecord, lastSeenAt, idleExpiresAt time.Time) error {
	if !s.ready() || ctx == nil {
		return fmt.Errorf("%w: store", session.ErrInvalidOptions)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validKeyHash(keyHash) {
		return session.ErrInvalidKey
	}
	// The zero-time guard is Touch's, for the reason Touch states: a record
	// carrying only an idle bound has no absolute deadline to renew past, and
	// comparing against the zero time would refuse every renewal of one.
	if !record.ExpiresAt.IsZero() && idleExpiresAt.After(record.ExpiresAt) {
		return session.ErrNotFound
	}
	record.LastSeenAt = lastSeenAt
	record.IdleExpiresAt = idleExpiresAt
	encoded, ttl, err := s.encode(record)
	if err != nil {
		return err
	}
	renewed, err := s.client.SetXX(ctx, s.prefix+keyHash, encoded, ttl).Result()
	if err != nil {
		return unavailable(ctx)
	}
	if !renewed {
		return session.ErrNotFound
	}
	return nil
}

// Delete is idempotent.
func (s *Store) Delete(ctx context.Context, keyHash string) error {
	if !s.ready() || ctx == nil {
		return fmt.Errorf("%w: store", session.ErrInvalidOptions)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validKeyHash(keyHash) {
		return session.ErrInvalidKey
	}
	if err := s.client.Del(ctx, s.prefix+keyHash).Err(); err != nil {
		return unavailable(ctx)
	}
	return nil
}

// load reads one record and returns its still encoded payload, which a renewal
// rewrites unchanged.
func (s *Store) load(ctx context.Context, keyHash string) (session.RawRecord, []byte, error) {
	var zero session.RawRecord
	encoded, err := s.client.Get(ctx, s.prefix+keyHash).Bytes()
	if errors.Is(err, goredis.Nil) {
		return zero, nil, session.ErrNotFound
	}
	if err != nil {
		return zero, nil, unavailable(ctx)
	}
	record, payload, err := s.decode(encoded)
	if err != nil {
		return zero, nil, err
	}
	if !record.Deadline().After(s.now()) {
		return zero, nil, session.ErrExpired
	}
	return record, payload, nil
}

func (s *Store) encode(record session.RawRecord) ([]byte, time.Duration, error) {
	return s.encodeWithPayload(record, record.Payload)
}

func (s *Store) encodeWithPayload(record session.RawRecord, payload []byte) ([]byte, time.Duration, error) {
	if len(payload) == 0 || len(payload) > s.maxPayloadBytes {
		return nil, 0, fmt.Errorf("%w: payload size", session.ErrCodec)
	}
	if len(record.Method) > maxMethodBytes {
		return nil, 0, fmt.Errorf("%w: method label", session.ErrCodec)
	}
	ttl := record.Deadline().Sub(s.now())
	if ttl <= 0 {
		return nil, 0, fmt.Errorf("%w: expiry", session.ErrInvalidOptions)
	}
	encoded := make([]byte, 0, headerBytes+len(record.Method)+len(payload))
	encoded = append(encoded, recordFormat)
	for _, stamp := range []time.Time{
		record.CreatedAt, record.AuthenticatedAt, record.LastSeenAt,
		record.ExpiresAt, record.IdleExpiresAt,
	} {
		encoded = binary.BigEndian.AppendUint64(encoded, milliOf(stamp))
	}
	encoded = binary.BigEndian.AppendUint32(encoded, uint32(record.Version))
	encoded = append(encoded, byte(len(record.Method)))
	encoded = append(encoded, record.Method...)
	return append(encoded, payload...), ceilMillisecond(ttl), nil
}

func (s *Store) decode(encoded []byte) (session.RawRecord, []byte, error) {
	var zero session.RawRecord
	if len(encoded) < headerBytes || encoded[0] != recordFormat {
		return zero, nil, fmt.Errorf("%w: record layout", session.ErrCodec)
	}
	stamps := make([]time.Time, 0, 5)
	for index := range 5 {
		offset := 1 + index*8
		stamps = append(stamps, timeOf(binary.BigEndian.Uint64(encoded[offset:offset+8])))
	}
	version := int(binary.BigEndian.Uint32(encoded[41:45]))
	methodLength := int(encoded[45])
	if len(encoded) < headerBytes+methodLength {
		return zero, nil, fmt.Errorf("%w: record layout", session.ErrCodec)
	}
	payload := encoded[headerBytes+methodLength:]
	if len(payload) == 0 || len(payload) > s.maxPayloadBytes {
		return zero, nil, fmt.Errorf("%w: payload size", session.ErrCodec)
	}
	return session.RawRecord{
		Payload:         payload,
		CreatedAt:       stamps[0],
		AuthenticatedAt: stamps[1],
		LastSeenAt:      stamps[2],
		ExpiresAt:       stamps[3],
		IdleExpiresAt:   stamps[4],
		Method:          string(encoded[headerBytes : headerBytes+methodLength]),
		Version:         version,
	}, payload, nil
}

func (s *Store) ready() bool {
	return s != nil && s.client != nil && s.now != nil &&
		s.prefix != "" && s.maxPayloadBytes > 0
}

func milliOf(stamp time.Time) uint64 {
	if stamp.IsZero() || stamp.UnixMilli() < 0 {
		return 0
	}
	return uint64(stamp.UnixMilli())
}

func timeOf(milli uint64) time.Time {
	if milli == 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(milli))
}

func validPrefix(value string) bool {
	if value == "" || len(value) > maxPrefixBytes {
		return false
	}
	for index := range len(value) {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

// validKeyHash reports whether value has the syntax of a store key. Rejecting
// foreign syntax keeps a malformed cookie away from the server.
func validKeyHash(value string) bool {
	if len(value) != keyHashLength {
		return false
	}
	for index := range len(value) {
		c := value[index]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// ceilMillisecond rounds a TTL up, so a sub-millisecond remainder never
// shortens a record below its own deadline.
func ceilMillisecond(duration time.Duration) time.Duration {
	if remainder := duration % time.Millisecond; remainder != 0 {
		if duration <= time.Duration(1<<63-1)-(time.Millisecond-remainder) {
			duration += time.Millisecond - remainder
		}
	}
	return duration
}

// unavailable reports a backend failure without copying server text, which can
// carry a key or a value, into the response path.
func unavailable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%w: redis", session.ErrUnavailable)
}

var _ session.RawStore = (*Store)(nil)
