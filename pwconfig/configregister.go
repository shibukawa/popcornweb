package pwconfig

// Binding the framework's own configuration has to happen after the generated
// definitions exist, because configbind.Bind panics on a type it has no
// definition for. Go initializes a package's files in lexical file name order,
// and this file sorts after configbind_gen.go; keep it that way when renaming
// either file.
func init() {
	Register[ServerConfig]("server")
	Register[SecurityConfig]("security")
	Register[RateLimitConfig]("ratelimit")
	Register[SessionConfig]("session")
	Register[ObservabilityConfig]("observability")
	Register[MiddlewareConfig]("middleware")
	Register[HTMLConfig]("html")
	Register[CacheConfig]("cache")
	Register[StorageConfig]("storage")
	Seed(defaultHTMLConfig)
}
