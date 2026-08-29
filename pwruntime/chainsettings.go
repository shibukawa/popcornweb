package pwruntime

import (
	"context"
	"sync/atomic"
	"time"
)

// ChainSettings is what building the request chain needs from configuration,
// in a form that names no transport.
//
// It is the fourth thing to travel this way, after the update settings, the bot
// settings and the configuration lookup, and for the same reason each time: a
// configuration file is not a transport concern, and the runtime that read it
// and the runtime that serves the request need not be the same one.
//
// It is a flat value rather than the configuration structs themselves because
// those carry the binder's tags, the scaffold help text and the defaults, none
// of which a chain builder reads. What a chain builder reads is this.
type ChainSettings struct {
	// The three frames a deployment turns on and off.
	RequestID bool
	AccessLog bool
	Recovery  bool
	// RequestTimeout and MaxRequestBody install their frames when positive.
	RequestTimeout time.Duration
	MaxRequestBody int64
	// SecurityHeaders is installed when Enabled says so, which is the switch
	// the configuration keeps beside the values rather than a separate one.
	SecurityHeaders SecurityHeadersConfig
	// CORS is answered by the frame SecurityHeaders installs rather than by one
	// of its own, so either half being enabled is what puts that frame in the
	// chain. They are separate settings and one position.
	CORS CORSConfig
	// TrustedProxies are the networks whose forwarding headers this deployment
	// reads, as configured. They are strings rather than parsed networks
	// because that is what the configuration carries and parsing them is the
	// caller's, which already has to report a bad one against a config key.
	TrustedProxies []string
	// Health and Readiness are the paths the two probes answer on, empty when
	// a deployment turned one off.
	Health    string
	Readiness string
	// OpenAPI, APIDoc and APIDocPath are the document path, the UI kind, and
	// the path that UI is read at. All three empty means the chain answers no
	// documentation at all, which is the common case.
	OpenAPI    string
	APIDoc     string
	APIDocPath string
	// APICatalog and APICatalogOrigin are the RFC 9727 endpoint's switch and
	// the origin its links are built from; unset writes them relative. The path
	// is the standard's, so it is pwruntime.APICatalogPath rather than a
	// setting.
	APICatalog       bool
	APICatalogOrigin string
	// CSRF is the cross-site check's configuration, carried whole because the
	// check reads most of it: the scope patterns, the token names, the cookie
	// name and the lifetime.
	CSRF CSRFConfig
	// Public is the static asset configuration. The tree itself is not here
	// because an embed is a fact of the binary rather than of a settings file.
	Public PublicAssetSettings
	// RateLimit is the limiter's configuration, carried whole because a chain
	// builder reads most of it.
	//
	// Two frames come out of it, at two slots: the ceiling out where a refusal
	// costs least, and the identity bucket below whatever establishes
	// authentication. Neither can be the other's position.
	RateLimit RateLimitConfig
}

var chainSettingsState atomic.Pointer[ChainSettings]

// PublishChainSettings records the resolved configuration for whichever runtime
// builds a chain from it.
func PublishChainSettings(settings ChainSettings) {
	chainSettingsState.Store(&settings)
}

// ResolvedChainSettings returns the published configuration, and whether
// anything published one.
//
// A runtime that finds none has no configuration to build a chain from and says
// so, rather than composing a chain out of zero values. Zero here would mean no
// recovery frame, no request ID, no security headers — a chain that serves
// requests and looks like a chain, with the frames a deployment configured
// silently missing.
func ResolvedChainSettings() (ChainSettings, bool) {
	if settings := chainSettingsState.Load(); settings != nil {
		return *settings, true
	}
	return ChainSettings{}, false
}

// DatabasesReady reports whether every configured connection answers.
//
// It is here rather than in either runtime because readiness is a fact about
// the process rather than about the request that asked: the same probe on
// either transport must give the same answer, and a second implementation could
// disagree about the timeout or about which connections count.
//
// The bound is one second. A readiness probe that hangs is worse than one that
// answers unavailable, because an orchestrator waiting on it cannot tell a slow
// database from a wedged process.
func DatabasesReady(parent context.Context, resources Resources) bool {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	if connections := resources.Connections.Connections(); len(connections) > 0 {
		for _, connection := range connections {
			if connection.Ping(ctx) != nil {
				return false
			}
		}
		return true
	}
	if resources.DB != nil && resources.DB.PingContext(ctx) != nil {
		return false
	}
	return true
}
