package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shibukawa/popcornweb/internal/pathpattern"
	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/sqlbind"
)

// setupBearer builds the ModeJWTOnly runtime.
//
// It shares almost nothing with the ceremony modes. There is no login to begin,
// no callback to correlate, no session to establish, and therefore no session
// backend and no correlation table. What it needs is a key set, an admission
// rule, and — only when the deployment asked for one — a revocation store.
func setupBearer(ctx context.Context, config Config) (Step, error) {
	verifier, err := newBearerVerifier(config.JWT)
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
	instance := &runtime{
		config:      config,
		include:     include,
		exclude:     exclude,
		stopPruning: make(chan struct{}),
		bearer:      verifier,
	}

	// The database is required only by the parts that read it. A deployment
	// admitting every verified identity and revoking nothing needs no table at
	// all, and asking it for one would be asking for storage nothing writes to.
	if db, err := bearerDatabase(ctx, config); err != nil {
		return nil, err
	} else if db != nil {
		schemaCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		driver, _ := pwruntime.DBDriver(ctx)
		if err := verifyTables(schemaCtx, db, driver, config); err != nil {
			return nil, err
		}
		instance.allowlist = resolveAllowlistStore(db, driver)
		if config.JWT.Revocation.enabled() {
			instance.revocations = newRevocationStore(db, driver, config.JWT)
		}
	}

	// The CSRF check demands a secret held in a session slot, and this mode
	// creates no session. Leaving the two configured together would refuse every
	// POST at runtime with a message about a missing session, which is a long
	// way from the setting that caused it.
	//
	// The check is not exempted for bearer requests instead. An exemption keyed
	// on the Authorization header would be a bypass in any deployment that also
	// authenticates by cookie, and refusing the pair here costs a deployment
	// that has no browser nothing.
	if csrf := pwruntime.ResolveConfig[pwconfig.SecurityConfig](ctx).CSRF; csrf.Enabled {
		return nil, fmt.Errorf("auth.mode %q creates no session, so security.csrf.enabled = true has no secret to check against; a bearer API does not need it, because its authority is a header no browser attaches on its own",
			ModeJWTOnly)
	}

	if config.JWT.Admission == AdmissionExisting && installedAccountResolver() == nil {
		// Existing admission forbids provisioning, so it can only be answered
		// by an application that knows its own accounts. Without a resolver it
		// would fall back to the derived account, which admits everyone the
		// issuer verifies — the opposite of what was configured.
		return nil, fmt.Errorf("auth.jwt.admission %q requires auth.SetAccountResolver", AdmissionExisting)
	}

	go instance.pruneBearer()
	replaceRuntime(instance)
	warnDevRelaxation(ctx, config.JWT)
	return instance.serveBearer, nil
}

// bearerDatabase returns the database this configuration reads, or nil when it
// reads none.
func bearerDatabase(ctx context.Context, config Config) (sqlbind.SQLExecutor, error) {
	if !config.JWT.readsAStore() {
		return nil, nil
	}
	db, ok := pwruntime.ConnectionExecutor(ctx)
	if !ok {
		return nil, errors.New("auth.mode \"jwt_only\" requires middleware.rdb.enabled = true for the registered allowlist or the revocation list")
	}
	return db, nil
}

// pruneBearer sweeps revocation entries that have outlived every token they
// could refuse.
func (rt *runtime) pruneBearer() {
	if rt.revocations == nil {
		return
	}
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-rt.stopPruning:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = rt.revocations.prune(ctx, time.Now())
			cancel()
		}
	}
}

// warnDevRelaxation says at every startup that this process is not verifying
// tokens.
//
// It is deliberately an unconditional warning rather than a one-line note: the
// operator reading a startup log is the last person positioned to notice that a
// deployment they believed was verifying is not.
func warnDevRelaxation(ctx context.Context, config JWTConfig) {
	if !devRelaxationBuilt || !config.Dev.TrustUnverifiedTokens {
		return
	}
	pwruntime.ReadLogger(ctx).Log(ctx, pwruntime.LevelWarn,
		"bearer tokens are NOT being verified",
		pwruntime.String("setting", "auth.jwt.dev.trust_unverified_tokens"),
		pwruntime.String("environment", pwconfig.Env()),
		pwruntime.String("reachable_from", "loopback only"),
		pwruntime.String("issuer", config.Issuer))
}

// RevokeToken withdraws one access token by its jti, for the running
// application.
//
// It is the narrow act: a credential leaked and the identity that holds it is
// otherwise fine.
func RevokeToken(ctx context.Context, issuer, tokenID, note string) error {
	instance := activeRuntime()
	if instance == nil {
		return errors.New("auth: not initialized")
	}
	return instance.RevokeToken(ctx, issuer, tokenID, note)
}

// RevokeSubject withdraws every token issued to an identity before now.
//
// It is the broad act, for a compromised account: enumerating the outstanding
// token identifiers is exactly what nobody can do. The identity works again
// once it authenticates afresh, because the stored stamp is compared against
// the token's iat.
func RevokeSubject(ctx context.Context, issuer, identityKey, note string) error {
	instance := activeRuntime()
	if instance == nil {
		return errors.New("auth: not initialized")
	}
	return instance.RevokeSubject(ctx, issuer, identityKey, note)
}

// ReinstateToken and ReinstateSubject remove a revocation issued in error.
//
// Reinstating is not an undo that hides what happened: the entry is deleted, so
// every unexpired token it was refusing works again at the next request.
func ReinstateToken(ctx context.Context, issuer, tokenID string) error {
	return withRevocations(func(store *RevocationStore) error {
		return store.reinstate(ctx, issuer, revocationKindToken, tokenID)
	})
}

func ReinstateSubject(ctx context.Context, issuer, identityKey string) error {
	return withRevocations(func(store *RevocationStore) error {
		return store.reinstate(ctx, issuer, revocationKindSubject, identityKey)
	})
}

// TokenRevoked and SubjectRevoked report the current state and the stamp, for
// an administrative view that must not guess. They read the store rather than
// the request cache.
func TokenRevoked(ctx context.Context, issuer, tokenID string) (time.Time, bool, error) {
	instance := activeRuntime()
	if instance == nil || instance.revocations == nil {
		return time.Time{}, false, errors.New("auth: revocation is not configured")
	}
	return instance.revocations.state(ctx, issuer, revocationKindToken, tokenID)
}

func SubjectRevoked(ctx context.Context, issuer, identityKey string) (time.Time, bool, error) {
	instance := activeRuntime()
	if instance == nil || instance.revocations == nil {
		return time.Time{}, false, errors.New("auth: revocation is not configured")
	}
	return instance.revocations.state(ctx, issuer, revocationKindSubject, identityKey)
}

func withRevocations(run func(*RevocationStore) error) error {
	instance := activeRuntime()
	if instance == nil || instance.revocations == nil {
		return errors.New("auth: revocation is not configured")
	}
	return run(instance.revocations)
}

// BearerClaims returns the frozen verified claim set of a bearer request, for a
// deployment that authorizes from claims rather than from a local account.
func BearerClaims(ctx context.Context) (Claims, bool) {
	identity, ok := Bearer(ctx)
	if !ok {
		return Claims{}, false
	}
	return identity.Identity.Claims, true
}

// Bearer returns the verified caller behind a bearer request.
//
// It reports false for a browser session, because the two modes publish
// different things: a session has an account summary that outlives the request,
// and a bearer request has a token that does not.
func Bearer(ctx context.Context) (BearerIdentity, bool) {
	identity, ok := pwruntime.RequestAuthentication(ctx).Principal.(BearerIdentity)
	return identity, ok
}

// readsAStore reports whether this mode consults a framework table at all.
//
// Both of the tables it can read are relational, which is why auth.backend is
// refused for this mode when either is in play; see Config.validateShape.
func (j JWTConfig) readsAStore() bool {
	return j.Admission == AdmissionRegistered || j.Revocation.enabled()
}
