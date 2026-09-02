// Package pwsession resolves per-browser state for a request, on whichever
// transport carries it.
//
// It is the third of the four layers requirement:alternate-http-backend-readiness
// names. Almost none of a session is transport-shaped: the slot declarations,
// the backend registry, the keyring, the cookie policy, the lifetime arithmetic
// and the expiry sweep are all the same whichever server answered. What each
// transport supplies is one frame that hands the resolved manager a carrier,
// which is session.Carrier and is already shared.
//
// So Setup is here and the frame is not. pw installs manager.Middleware and
// pwfast installs pwfast.Session, and both drive one manager built from one
// reading of the configuration — which matters more than it sounds: two
// implementations of when a token rotates are two chances to leave a session
// valid that should have ended.
//
// Nothing here imports a transport, apart from net/http's SameSite enum, which
// is a spelling of an attribute rather than a transport.
package pwsession

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwdatabase"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/popcornweb/sessionconfig"
)

// state holds the manager of the current initialization, so that a later slot
// can reach the session an earlier slot resolved. Framework initialization runs
// more than once in one process, most obviously in tests, so it is replaced
// rather than assumed to be written once.
var state struct {
	sync.RWMutex
	manager *session.Manager
	close   func(context.Context) error
	prune   func(context.Context, time.Time, int) (int64, error)
	stop    chan struct{}
}

// pruneInterval bounds how often expired records are swept, and pruneBatch
// bounds one sweep so a large backlog cannot hold connections.
const (
	pruneInterval = 10 * time.Minute
	pruneBatch    = 256
)

// sweep removes records that expired without being revoked.
//
// It runs here rather than in an authentication plugin because the records are
// the framework's: a deployment with no login still writes them for a
// session.ServerOnly slot, and abandonment rather than logout is how most
// sessions end. The cutoff exists because session.retention bounds every
// record, per decision:storage-bounded-session-record.
func sweep(prune func(context.Context, time.Time, int) (int64, error), stop <-chan struct{}) {
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, _ = prune(ctx, time.Now(), pruneBatch)
			cancel()
		}
	}
}

// Manager returns the manager the last Setup installed, or nil when session
// storage is disabled.
//
// It is how popcornweb/plugin/auth reaches Rotate and Destroy: the framework
// resolves the session at SlotSession and authentication drives it at
// SlotAuthentication, which is why the manager travels forward and the login
// does not travel back.
func Manager() *session.Manager {
	state.RLock()
	defer state.RUnlock()
	return state.manager
}

// Prune returns the expiry sweep of the configured backend, or nil for a
// backend whose server or browser forgets records on its own.
func Prune() func(context.Context, time.Time, int) (int64, error) {
	state.RLock()
	defer state.RUnlock()
	return state.prune
}

// Setup opens the configured storage and builds the manager, returning nil when
// the deployment disabled sessions.
//
// It knows nothing about login. The durations it enforces are read from the
// [auth.session] binding, which exists only when an authentication plugin is
// linked; without one the session is bounded by the browser alone, which is
// what decision:session-lifetime-owned-by-auth accepts.
func Setup(ctx context.Context) (*session.Manager, error) {
	Replace(nil, nil, nil)

	config := pwruntime.ResolveConfig[sessionconfig.SessionConfig](ctx)
	if !config.Enabled {
		return nil, nil
	}
	if err := ValidateDevelopmentMode(config.Backend); err != nil {
		return nil, err
	}
	registry, err := newRegistry()
	if err != nil {
		return nil, err
	}
	// The CSRF secret is a slot like any other, declared here rather than from
	// an init so that a project with the check off carries no slot and needs no
	// keyring on its account.
	if pwruntime.ResolveConfig[pwconfig.SecurityConfig](ctx).CSRF.Enabled {
		if err := registry.Register[middlewares.CSRFSecret](
			middlewares.CSRFSecretSlot, session.Private, nil,
			session.ResetOnRotate()); err != nil {
			return nil, err
		}
	}
	options, err := Options(config, pwruntime.ResolveConfig[sessionconfig.SessionLifetimeConfig](ctx))
	if err != nil {
		return nil, err
	}
	cookieMode := config.Backend == sessionconfig.SessionBackendCookie ||
		config.Backend == sessionconfig.SessionBackendDevPersist
	if !cookieMode && options.TTL <= 0 {
		// A server record with no deadline is one the sweep has no cutoff for,
		// and the store reads a zero expiry as already past.
		return nil, errors.New(
			"session.retention must be positive for a server backend, because a record with no deadline is never swept")
	}

	var store session.RawStore
	var backend session.Backend
	if !cookieMode {
		resources, err := DatabaseResources(ctx)
		if err != nil {
			return nil, err
		}
		backend, err = OpenBackend(ctx, config, resources)
		if err != nil {
			return nil, err
		}
		store = backend.Store
	}
	options.ServerSideAnonymous = config.Backend == sessionconfig.SessionBackendDevVolatile
	// Cookie and dev-persist are not server stores: the manager seals a record
	// into the browser for an anonymous session already, and either selection
	// means it never moves off there.
	manager, err := session.NewManager(registry, store, options)
	if err != nil {
		return nil, err
	}
	Replace(manager, backend.Close, backend.Prune)
	return manager, nil
}

// Replace installs the resolved session state and releases what it replaces.
//
// It is exported because Close is a runtime's shutdown step rather than this
// package's, and because a test that builds a manager by hand has to be able to
// put the process back.
func Replace(manager *session.Manager, closer func(context.Context) error, prune func(context.Context, time.Time, int) (int64, error)) {
	state.Lock()
	defer state.Unlock()
	// Repeated framework initialization, which tests perform, must not leave an
	// earlier sweep running or an earlier client open.
	if previous := state.stop; previous != nil {
		close(previous)
	}
	if previous := state.close; previous != nil {
		_ = previous(context.Background())
	}
	state.manager, state.close, state.prune = manager, closer, prune
	state.stop = nil
	if prune != nil {
		state.stop = make(chan struct{})
		go sweep(prune, state.stop)
	}
}

// Close releases the storage this process opened for sessions.
func Close(context.Context) error {
	Replace(nil, nil, nil)
	return nil
}

// Options maps the two bindings onto what the session package enforces.
// The placement and the cookie policy come from [session]; every duration comes
// from [auth.session], because an expiry states how long a proof of identity
// stays good and the store holding the bytes has no basis to make it.
func Options(config sessionconfig.SessionConfig, lifetime sessionconfig.SessionLifetimeConfig) (session.Options, error) {
	policy, err := CookiePolicy(config)
	if err != nil {
		return session.Options{}, err
	}
	// One secret protects everything the browser carries: a signed slot and a
	// sealed one derive purpose-separated subkeys from it.
	keys, err := Keyring(config.Keyring)
	if err != nil {
		return session.Options{}, err
	}
	return session.Options{
		TTL:             recordLifetime(config.Retention, lifetime.TTL),
		IdleTimeout:     lifetime.IdleTimeout,
		RenewalInterval: lifetime.RenewalInterval,
		Cookie:          policy,
		RecordCookie:    session.CookieOptions{Name: config.CookieStore.Name},
		Keys:            keys,
	}, nil
}

// recordLifetime is how long a record actually lives: the shorter of what the
// store will hold and what a proof of identity stays good for.
//
// They bound different things, so neither subsumes the other. A zero on either
// side is that side declining to bound it, which leaves the other one in force;
// zero on both leaves the browser as the only bound, which the startup
// validation refuses for a server backend.
func recordLifetime(retention, ttl time.Duration) time.Duration {
	switch {
	case retention <= 0:
		return ttl
	case ttl <= 0:
		return retention
	case ttl < retention:
		return ttl
	default:
		return retention
	}
}

// DatabaseResources opens what a session backend might want.
//
// A project with no relational middleware gets an empty set rather than an
// error: whether a database is needed is the selected backend's question, and
// the rdb backend is the one that answers it. Resolving eagerly here would
// refuse a DynamoDB or Redis session for the absence of something it never
// reads.
func DatabaseResources(ctx context.Context) (Resources, error) {
	if _, enabled := pwruntime.ConnectionExecutor(ctx); !enabled {
		return Resources{}, nil
	}
	// The session record is written on every change, so it lives in the session
	// group rather than in the default group, which is normally a replica.
	sessionCtx, err := pwdatabase.SelectSessionDB(ctx)
	if err != nil {
		return Resources{}, err
	}
	db, _ := pwruntime.DB(sessionCtx)
	executor, _ := pwruntime.ConnectionExecutor(sessionCtx)
	driver, _ := pwruntime.DBDriver(sessionCtx)
	return Resources{DB: db, Executor: executor, DBDriver: driver}, nil
}
