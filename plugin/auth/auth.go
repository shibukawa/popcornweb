// Package auth adds browser authentication and bearer-token API authentication
// to a Popcorn Web application. Importing it registers the [auth]
// configuration binding and the framework extensions that authenticate
// requests and guard protected paths.
//
//	import _ "github.com/shibukawa/popcornweb/plugin/auth"
//
// Browser modes use OIDC, passkeys, or both and establish a session in the
// backend session.backend selects. auth.mode = "oauth_only" instead signs a
// person in through a provider that speaks OAuth 2.0 and issues no ID Token,
// such as X, and lands in the same session. auth.mode = "jwt_only" verifies an
// Authorization bearer token on every request and creates no session or login
// endpoint.
//
// session.backend = cookie warns outside dev rather than refusing: a login this
// package can end on demand needs a record on the server, and a browser keeps
// what it was given. In dev that is the right trade and nothing is said.
//
// This package imports no storage plugin. It asks pw for the configured
// backend, so an application links the storage it configured and no more.
// General server backends need blank imports; cookie and both development
// intent modes are built into pw.
//
// # What it reads through pw and what it no longer does
//
// The settings and the environment come from popcornweb/pwconfig, and the
// request state from popcornweb/pwruntime, so the decisions in this package
// are reachable from a build serving on either transport — which is what
// popcornweb/plugin/auth/authfast then does.
//
// What is still pw's is what is genuinely the net/http runtime's: the extension
// registry this registers into, the session manager it drives, and the
// connection group a session's storage is pinned to. Each is a layer of its
// own, and each has to move before this package can be linked without pw.
package auth

import (
	"context"
	"errors"
	"fmt"
	"github.com/shibukawa/popcornweb/pwsession"
	"sync"
	"time"

	"github.com/shibukawa/popcornweb/authstate"
	"github.com/shibukawa/popcornweb/contrib/oauth"
	"github.com/shibukawa/popcornweb/contrib/oauthprofile"
	"github.com/shibukawa/popcornweb/contrib/oidc"
	"github.com/shibukawa/popcornweb/contrib/passkey"
	"github.com/shibukawa/popcornweb/internal/pathpattern"
	"github.com/shibukawa/popcornweb/internal/requestorigin"
	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwextension"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/popcornweb/sessionconfig"
)

// Authentication method names recorded on a session and reported by
// pw.RequestAuthentication. An application compares against these rather than
// against a literal.
const (
	// MethodOIDC labels sessions created by the OIDC flow.
	MethodOIDC = "oidc"
	// MethodPasskey labels sessions created by a passkey assertion.
	MethodPasskey = "passkey"
	// MethodOAuth labels sessions created by a plain OAuth 2.0 provider login.
	// Which provider is on the session itself, as SessionData.Provider: the
	// method says how the identity was proved, and a deployment runs one
	// provider, so ranking or distinguishing them here would say nothing.
	MethodOAuth = "oauth"
)

// admissionFor returns the admission rule of the mode this runtime serves.
func (rt *runtime) admissionFor() admissionRule { return rt.config.admission() }

// The correlation records of each provider flow are isolated from one another
// in the shared auth state table. A process runs one mode, so the two never
// meet at runtime; they are separate so that a table outliving a mode change
// cannot answer a callback of the flow that is no longer mounted.
const (
	stateNamespace      = "auth-oidc"
	oauthStateNamespace = "auth-oauth"
)

// stateNamespaceFor names the correlation records of the mounted flow.
func (c Config) stateNamespaceFor() string {
	if c.usesOAuth() {
		return oauthStateNamespace
	}
	return stateNamespace
}

// authFastPackage is where this capability is served on the second transport.
// It is a string rather than an import, because importing it from here would
// put the fasthttp runtime into every net/http build.
const authFastPackage = "github.com/shibukawa/popcornweb/plugin/auth/authfast"

func init() {
	registerSessionSlot()
	// Both name the fasthttp half, so a build serving on that transport leaves
	// them alone: authfast.Setup performs this startup and installs the frames
	// there, and doing it from here as well would build the runtime twice.
	pwextension.Register(pwextension.Extension{
		Name:            "auth.endpoints",
		Slot:            pwruntime.SlotAuthentication,
		Setup:           setupAuthentication,
		Close:           closeRuntime,
		SecondTransport: authFastPackage,
	})
	pwextension.Register(pwextension.Extension{
		Name:            "auth.guard",
		Slot:            pwruntime.SlotGuard,
		Setup:           setupGuard,
		SecondTransport: authFastPackage,
	})
}

// runtime is the state shared by the three extensions of this package. It is
// rebuilt whenever framework initialization runs.
type runtime struct {
	config  Config
	manager *session.Manager
	// sessionClose releases a client the backend opened. A backend that
	// borrowed the middleware database leaves it nil.
	sessionClose func(context.Context) error
	backend      Backend
	// pruners are the ceremony stores that need sweeping, which is none of
	// them on a backend whose expiry is decided on read.
	pruners    []statePruner
	stateStore authstate.Store[oauth.Transaction]
	// hint is the sealed sign-in hint cookie, nil unless the deployment turned
	// it on. It carries no authority; see SignInHint.
	hint         *session.Jar[SignInHint]
	allowlist    AllowlistStore
	cookiePolicy sessionconfig.SessionCookieConfig
	include      []pathpattern.Pattern
	exclude      []pathpattern.Pattern
	// accounts re-reads the account behind a live session, so that suspending
	// or removing one ends the sessions it already has rather than only
	// stopping the next login.
	accounts *accountGate
	// trustedOrigins are the origins this deployment has already declared
	// elsewhere in its own configuration: the passkey origin allowlist and the
	// origin of the OIDC redirect URL. They exist so that a deployment behind a
	// TLS-terminating proxy, which reconstructs an http origin for an https
	// browser, is not refused by its own login endpoints.
	trustedOrigins map[string]bool
	// proxies is the declared peer set whose X-Forwarded-Proto is read when
	// reconstructing this deployment's own origin. It resolves what this
	// deployment calls itself and never widens what trustedOrigins accepts.
	proxies requestorigin.Proxies
	// passkeyFlow is nil unless the selected mode mounts api:passkey-endpoints.
	passkeyFlow *passkey.SessionFlow
	// oauthProvider is the resolved definition of ModeOAuthOnly, and the zero
	// value in every other mode. It is looked up once at startup, so a provider
	// name nothing defines fails there rather than at the first login.
	oauthProvider oauthprofile.Provider
	// credentials and bootstrap are the installed stores, or the framework
	// defaults over the tables this package owns.
	credentials CredentialStore
	bootstrap   BootstrapStore
	// enrollment holds the restricted tickets a redeemed bootstrap credential
	// grants. It is nil outside passkey_only.
	enrollment authstate.Store[enrollmentTicket]
	// passkeyPaths maps a mounted ceremony path to its endpoint suffix.
	passkeyPaths map[string]string
	// bearer and revocations are nil outside ModeJWTOnly. That mode mounts no
	// endpoint and creates no session, so almost none of the state above
	// applies to it.
	bearer      *bearerVerifier
	revocations *RevocationStore
	// stopPruning ends the background expiry sweep during shutdown.
	stopPruning chan struct{}

	// discovery is deferred to the first login so that application startup
	// does not depend on the identity provider being reachable. Clients are
	// cheap request-specific wrappers around the shared provider: loopback
	// development can derive a different redirect URI for localhost and
	// 127.0.0.1 without repeating discovery.
	discovery sync.Mutex
	provider  *oidc.Provider
}

var current struct {
	sync.RWMutex
	runtime *runtime
}

func activeRuntime() *runtime {
	current.RLock()
	defer current.RUnlock()
	return current.runtime
}

// setupAuthentication validates configuration, opens the state this package
// owns, and returns the middleware that finalizes the request authentication
// and serves the login endpoints.
//
// It runs at SlotAuthentication, after the framework has already resolved the
// session at SlotSession. Storage is not this package's job: it reaches the
// manager pw prepared and drives it.
// unrevocableSessionBackend reports the warning a session backend that cannot
// take a record back deserves, and the empty string when there is none.
//
// A login is the state whose revocability this package depends on: logout,
// account suspension, and the account re-read all end a session by removing the
// record behind it. The cookie backend has no record to remove, so a copy taken
// beforehand keeps authenticating until its sealed expiry and each of those
// acts becomes advisory.
//
// It is a warning rather than a refusal because the pairing is the right one in
// development, where the point of that backend is a login that needs no
// infrastructure at all, and the exposure it carries needs a browser someone
// else is holding. Outside dev the deployment is told, at every startup and
// again by pw doctor, and the judgement stays with the deployment.
//
// Dev says nothing, rather than saying it quietly: a warning printed on every
// local run is one an operator learns to scroll past, and this has to still be
// readable on the day it appears in a staging log.
//
// The silence needs development to have been declared, not merely resolved. A
// deployment that never set APP_ENV lands on "dev" by default, and that is
// exactly the deployment that should hear this.
func unrevocableSessionBackend(backend string, development bool) string {
	if backend != sessionconfig.SessionBackendCookie || development {
		return ""
	}
	return "session.backend = cookie keeps the login in the browser, so logout and account suspension cannot end a " +
		"session that was already issued; use rdb, redis, or dynamo where sessions must end on demand"
}

// A Step is the transport-free authentication frame.
//
// It finalizes the request authentication, serves the login endpoints, and
// calls next for every request neither of those answered. Each transport wraps
// one in its own middleware shape, which is the whole of what differs.
type Step func(x Exchange, next func())

// setupAuthentication is the net/http extension's Setup. It wraps the neutral
// step in this transport's middleware shape and nothing else.
func setupAuthentication(ctx context.Context) (pwextension.Middleware, error) {
	step, err := Setup(ctx)
	if err != nil || step == nil {
		return nil, err
	}
	return httpFrame(step), nil
}

// Endpoints returns the authentication step of the runtime this process already
// installed, or nil when auth is disabled or no setup has run.
//
// It is how a second transport serves the same login as the first without a
// second startup. Calling Setup again would open a second set of stores, start
// a second expiry sweep, and leave the first runtime's serving closures pointed
// at storage nothing sweeps; reading what is installed shares one runtime, which
// is what a deployment serving two transports actually has.
func Endpoints() Step {
	instance := activeRuntime()
	if instance == nil {
		return nil
	}
	if instance.config.usesJWT() {
		return instance.serveBearer
	}
	return instance.serve
}

// Setup validates the configuration, opens the state this package owns, and
// returns the authentication step, or nil when auth is disabled.
//
// An application normally never calls it: importing this package registers the
// framework extension that does. It is exported for a transport that assembles
// its own chain rather than reading the extension registry, which is what the
// fasthttp half does. Calling it twice replaces the runtime, so one process
// calls it once.
func Setup(ctx context.Context) (Step, error) {
	replaceRuntime(nil)

	config := pwruntime.ResolveConfig[Config](ctx)
	if !config.Enabled {
		return nil, nil
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	if err := checkDevRelaxation(config.JWT); err != nil {
		return nil, err
	}
	if config.usesJWT() {
		// A bearer request carries its own credential, so this mode needs
		// neither a session backend nor the correlation storage every ceremony
		// mode opens. It branches before all of it.
		return setupBearer(ctx, config)
	}
	sessionConfig := pwruntime.ResolveConfig[sessionconfig.SessionConfig](ctx)
	if !sessionConfig.Enabled {
		return nil, errors.New("auth requires session.enabled = true")
	}
	if warning := unrevocableSessionBackend(sessionConfig.Backend, pwconfig.Development()); warning != "" {
		pwruntime.ReadLogger(ctx).Log(ctx, pwruntime.LevelWarn, "sessions cannot be ended on demand",
			pwruntime.String("setting", "session.backend"),
			pwruntime.String("environment", pwconfig.Env()),
			pwruntime.String("consequence", warning))
	}
	manager := pwsession.Manager()
	if manager == nil {
		return nil, errors.New("auth requires session.enabled = true")
	}
	// The stores this package owns come from the selected backend. Only the
	// relational one needs a database handle, and it is the one that says so:
	// a project on DynamoDB reaches none of this.
	resources, err := backendResources(ctx)
	if err != nil {
		return nil, err
	}
	schemaCtx, cancel := context.WithTimeout(ctx, schemaTimeout)
	defer cancel()
	backend, err := openBackend(schemaCtx, config, resources)
	if err != nil {
		return nil, err
	}
	include, err := pathpattern.Compile(config.Protection.Include)
	if err != nil {
		return nil, err
	}
	exclude, err := pathpattern.Compile(config.Protection.Exclude)
	if err != nil {
		return nil, err
	}
	// The declared origins below stay the strong half of the comparison; this
	// only resolves what this deployment calls itself, which behind a
	// TLS-terminating proxy is not what r.TLS says.
	proxies, err := requestorigin.Compile(pwruntime.ResolveConfig[pwconfig.ServerConfig](ctx).TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("server.trusted_proxies %w", err)
	}
	instance := &runtime{
		config:         config,
		manager:        manager,
		backend:        backend,
		allowlist:      backend.Allowlist,
		cookiePolicy:   sessionConfig.Cookie,
		include:        include,
		exclude:        exclude,
		stopPruning:    make(chan struct{}),
		passkeyPaths:   config.passkeyPaths(),
		trustedOrigins: config.trustedOrigins(),
		proxies:        proxies,
		accounts:       newAccountGate(),
	}
	if config.usesOAuth() {
		// validate already refused an undefined name, so this cannot fail; the
		// check stays because a lookup whose failure is impossible is exactly
		// the one that becomes possible when somebody edits the other file.
		provider, ok := oauthprofile.Lookup(config.OAuth.Provider)
		if !ok {
			return nil, fmt.Errorf("auth.oauth.provider %q is not defined", config.OAuth.Provider)
		}
		instance.oauthProvider = provider
	}
	if instance.stateStore, err = openState(schemaCtx, instance, config.stateNamespaceFor(), oauth.TransactionCodec{}); err != nil {
		return nil, err
	}
	if instance.hint, err = hintJar(config.Assurance.Hint, sessionConfig.Cookie); err != nil {
		return nil, err
	}
	if config.usesPasskey() {
		if err := instance.setupPasskey(schemaCtx); err != nil {
			return nil, err
		}
	}
	// Ceremonies that are never completed only expire logically, so a sweep
	// keeps the table bounded. A backend whose store needs none registers no
	// pruner and the goroutine idles.
	go instance.prune()
	replaceRuntime(instance)
	return instance.serve, nil
}

// replaceRuntime installs instance and releases the runtime it replaces.
// Repeated framework initialization, which tests perform, must not leave an
// earlier sweep running or an earlier backend client open.
func replaceRuntime(instance *runtime) {
	current.Lock()
	defer current.Unlock()
	if previous := current.runtime; previous != nil {
		close(previous.stopPruning)
		if previous.sessionClose != nil {
			_ = previous.sessionClose(context.Background())
		}
	}
	current.runtime = instance
}

// pruneInterval bounds how often expired records are swept.
const pruneInterval = 10 * time.Minute

// pruneBatch bounds one sweep so a large backlog cannot hold connections.
const pruneBatch = 256

func (rt *runtime) prune() {
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-rt.stopPruning:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			now := time.Now()
			// The session store sweeps itself; these are the ceremony records
			// this package owns.
			for _, pruner := range rt.pruners {
				_, _ = pruner.Prune(ctx, now, pruneBatch)
			}
			cancel()
		}
	}
}

func setupGuard(context.Context) (pwextension.Middleware, error) {
	instance := activeRuntime()
	if instance == nil || len(instance.include) == 0 {
		return nil, nil
	}
	return httpFrame(instance.guard), nil
}

// closeRuntime stops the expiry sweep and closes an owned backend client
// during shutdown. The database pool belongs to the RDB middleware, so only
// this package's own state is released.
func closeRuntime(context.Context) error {
	replaceRuntime(nil)
	return nil
}
