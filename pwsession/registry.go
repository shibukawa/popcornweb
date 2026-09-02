package pwsession

import (
	"fmt"
	"strings"
	"sync"

	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/popcornweb/sessionconfig"
)

// slotState holds the declared session storage of this process.
//
// A registration is kept as a closure rather than applied immediately, because
// a session.Registry freezes when its manager is built and framework
// initialization may run more than once in one process, most obviously in
// tests. Replaying the closures gives every initialization a fresh registry
// carrying the same declarations.
var slotState struct {
	sync.Mutex
	register []func(*session.Registry) error
}

// RegisterStore declares one piece of per-browser state, as a Go type
// with a placement, and is the only place either is stated.
//
//	pw.RegisterStore[Cart]("cart", session.Private)
//	cart, ok := session.Load[Cart](ctx)
//
// The placement states what the client may do with the value and where its
// bytes live: session.Shared is a plain cookie the front end reads and writes,
// session.ReadOnly a signed one it may read, session.Private is sealed and
// moves from a cookie to the configured backend at the login rotation,
// session.ServerOnly is sealed and always on the server because it must stay
// revocable, and session.RequestScope lives in process memory for one request
// and is never persisted. The deployment is left with one choice, which server
// backend session.backend names.
//
// One example per placement, in the same order: a display-density toggle the
// front end flips is session.Shared; the locale the server chose and the front
// end may read is session.ReadOnly; a cart an anonymous visitor starts and a
// logged-in user keeps is session.Private; a stored secret the client must
// never hold even sealed — the refresh token taken at login — or a draft that
// outgrows the cookie budget is session.ServerOnly; and the scope set a bearer
// token resolves to against the authentication database — read fresh on every
// request precisely so a revocation is seen immediately — is
// session.RequestScope. A preference that should follow the account across
// browsers is not session state at all: a session names one browser and dies
// at logout, so that belongs in the application's own database.
//
// How long the value lives is the second, independent question. Stating nothing
// ties it to the session. session.ExpiresAfter ends it earlier, which is what a
// rotating secret or a short admission window wants. session.OutlivesSession
// exempts it from a sign-out, which is what a display language wants, and is
// available only to the two cookie-placed values because a record cannot
// outlive its own destruction.
//
// Call it from main, after every package init has run, exactly as
// RegisterConfig requires: the declarations must be complete before the first
// request decodes anything.
//
// A duplicate Go type and a duplicate key are each a panic at startup rather
// than a silent replacement, on the same grounds as RegisterBackend.
func RegisterStore[T any](key string, placement session.Placement, options ...session.SlotOption) {
	slotState.Lock()
	defer slotState.Unlock()
	slotState.register = append(slotState.register, func(registry *session.Registry) error {
		return registry.Register[T](key, placement, nil, options...)
	})
}

// newRegistry replays every declaration into a fresh registry.
func newRegistry() (*session.Registry, error) {
	slotState.Lock()
	defer slotState.Unlock()
	registry := session.NewRegistry()
	for _, register := range slotState.register {
		if err := register(registry); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// NewRegistry builds the registry of everything declared through
// RegisterStore, plus the slots the caller adds.
//
// The framework half of a login is one such slot, so plugin/auth passes its own
// declaration here rather than being privileged in storage.
func NewRegistry(extra ...func(*session.Registry) error) (*session.Registry, error) {
	registry, err := newRegistry()
	if err != nil {
		return nil, err
	}
	for _, register := range extra {
		if err := register(registry); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// Keyring reads the secret that protects everything the browser carries.
//
// One secret serves both protections a slot can carry: session.ReadOnly signs
// and session.Private seals, and session.Keyring derives a purpose-separated
// subkey per mode. It is therefore required unless every declared slot is
// session.Shared, which protects nothing, or session.RequestScope, which never
// leaves process memory.
//
// The secret itself never reaches an error message or a log.
func Keyring(config sessionconfig.SessionKeyringConfig) (*session.Keyring, error) {
	if strings.TrimSpace(config.Secret) == "" {
		return nil, nil
	}
	secrets := append([]string{config.Secret}, config.PreviousSecrets...)
	keys, err := session.ParseKeyring(secrets...)
	if err != nil {
		return nil, fmt.Errorf("session.keyring.secret: %w", err)
	}
	return keys, nil
}
