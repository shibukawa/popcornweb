package pwconfig

import "github.com/shibukawa/popcornweb/pwruntime"

// ChainSettings reduces the resolved configuration to what building a request
// chain needs.
//
// The reduction lives here rather than in either runtime because both perform
// it and it is the same reduction: which frames a deployment turned on, what
// the two probes answer, what the cross-site check compares. A second copy
// would be a chain that differs between the transports for no reason a response
// would show.
func ChainSettings(server ServerConfig, security SecurityConfig, middleware MiddlewareConfig) pwruntime.ChainSettings {
	return pwruntime.ChainSettings{
		RequestID:        middleware.RequestID,
		AccessLog:        middleware.AccessLog,
		Recovery:         middleware.Recovery,
		RequestTimeout:   middleware.RequestTimeout,
		MaxRequestBody:   server.MaxRequestBody,
		SecurityHeaders:  security.Headers,
		CORS:             security.CORS,
		TrustedProxies:   server.TrustedProxies,
		Health:           server.Health,
		Readiness:        server.Readiness,
		OpenAPI:          server.OpenAPI,
		APIDoc:           server.APIDoc,
		APIDocPath:       server.APIDocPath,
		APICatalog:       server.APICatalog,
		APICatalogOrigin: server.APICatalogOrigin,
		CSRF:             security.CSRF,
		Public:           server.Public,
		RateLimit:        Value[RateLimitConfig](),
	}
}

// PublishChainSettings publishes the reduction of what the registry holds, so a
// runtime that binds no configuration of its own can compose a chain from
// something other than zero values.
//
// Parse calls it, which is what makes the settings available to a build that
// never links a runtime performing startup. A runtime that hands a chain
// builder configuration of its own publishes that instead, through
// ChainSettings above.
func PublishChainSettings() {
	pwruntime.PublishChainSettings(ChainSettings(
		Value[ServerConfig](), Value[SecurityConfig](), Value[MiddlewareConfig]()))
}

// Value returns the resolved value of one registered binding, or the zero value
// when nothing registered it.
//
// It reads the process-wide value rather than a request's, which is what
// startup and a chain builder need. A request reads through
// pwruntime.ResolveConfig instead, so a middleware that recorded configuration
// on the request is what that request sees.
func Value[T any]() T {
	value, _ := pwruntime.RegisteredConfig[T]()
	return value
}
