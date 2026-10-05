package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/shibukawa/tinybind-go/sqlbind"
)

// AllowlistCandidate is one verified claim of a login offered to the store as a
// possible pre-registration match.
type AllowlistCandidate struct {
	Claim string
	Value string
}

// AllowlistStore answers the pre-registration question of the registered
// admission mode.
//
// Registered receives every compared claim the verified identity carries, so
// one login is one lookup whatever backs the store. A lookup failure is an
// error and never a denial: reporting an outage as "not registered" would turn
// it into a silent access change.
type AllowlistStore interface {
	Registered(ctx context.Context, issuer string, candidates []AllowlistCandidate) (bool, error)
}

var allowlistState struct {
	sync.RWMutex
	store AllowlistStore
}

// SetAllowlistStore installs the application allowlist store. Call it from main
// before pw.Run. Installing one means the framework creates and verifies no
// table for this capability, exactly as SetCredentialStore does.
func SetAllowlistStore(store AllowlistStore) {
	allowlistState.Lock()
	defer allowlistState.Unlock()
	allowlistState.store = store
}

func installedAllowlistStore() AllowlistStore {
	allowlistState.RLock()
	defer allowlistState.RUnlock()
	return allowlistState.store
}

// allowlistCandidates reads the compared claims from the verified identity.
//
// The configured identity claim is compared by default, because that is the
// value a deployment knows in advance. Listing further claims lets a deployment
// also recognize someone it registered by another attribute, such as an email
// address. A claim the identity does not carry is omitted rather than compared
// as empty.
func allowlistCandidates(claims []string, identity Identity) []AllowlistCandidate {
	if len(claims) == 0 {
		claims = []string{identity.KeyClaim}
	}
	candidates := make([]AllowlistCandidate, 0, len(claims))
	for _, claim := range claims {
		value, ok := claimLookupValue(claim, identity)
		if !ok {
			continue
		}
		candidates = append(candidates, AllowlistCandidate{Claim: claim, Value: value})
	}
	return candidates
}

// resolveAllowlistStore prefers the application store and falls back to the
// framework table, matching how the credential and bootstrap stores resolve.
func resolveAllowlistStore(db sqlbind.SQLExecutor, dialect string) AllowlistStore {
	if store := installedAllowlistStore(); store != nil {
		return store
	}
	return sqlAllowlist{db: db, dialect: dialect}
}

// sqlAllowlist reads the pre-registration table owned by this package. It is
// used only when the application installed no store of its own.
type sqlAllowlist struct {
	db      sqlbind.SQLExecutor
	dialect string
}

// Registered matches each candidate as one (issuer, claim, value) row. The
// whole set is one statement, so a login costs one round trip however many
// claims a deployment compares.
func (a sqlAllowlist) Registered(ctx context.Context, issuer string, candidates []AllowlistCandidate) (bool, error) {
	if a.db == nil {
		return false, errors.New("auth: allowlist is not available")
	}
	conditions := make([]string, 0, len(candidates))
	arguments := make([]any, 0, len(candidates)*2+1)
	arguments = append(arguments, issuer)
	for _, candidate := range candidates {
		conditions = append(conditions, `(claim = ? AND value = ?)`)
		arguments = append(arguments, candidate.Claim, candidate.Value)
	}
	query := rebind(a.dialect, `SELECT 1 FROM `+AllowlistTable+` WHERE issuer = ? AND (`+
		strings.Join(conditions, " OR ")+`) LIMIT 1`)
	var found int
	err := queryRow(ctx, a.db, query, arguments...).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		// A backend failure must not be reported as "not registered", which
		// would turn an outage into a silent access change.
		return false, fmt.Errorf("auth: read allowlist: %w", err)
	}
	return true, nil
}
