package pw

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	otetrace "github.com/shibukawa/popcornweb/contrib/otel/trace"
	"github.com/shibukawa/popcornweb/internal/requestorigin"
	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwobservability"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/pwsession"
)

func validateRuntimeConfig(server ServerConfig, security SecurityConfig, middleware MiddlewareConfig, observability ObservabilityConfig) error {
	if err := validateServerConfig(server); err != nil {
		return err
	}
	if err := validateSecurityConfig(security); err != nil {
		return err
	}
	if middleware.RequestTimeout < 0 {
		return fmt.Errorf("middleware.request_timeout must not be negative")
	}
	if err := validateCompressionCodings(middleware); err != nil {
		return err
	}
	if err := validateRDBConfig(middleware.RDB); err != nil {
		return err
	}
	switch strings.ToLower(observability.MinimumLevel) {
	case "trace", "debug", "info", "warn", "error", "off":
	default:
		return fmt.Errorf("observability.minimum_level must be trace, debug, info, warn, error, or off")
	}
	switch strings.ToLower(strings.TrimSpace(observability.BootLog)) {
	case "", BootLogAuto, BootLogTree, BootLogRecord, BootLogOff:
	default:
		return fmt.Errorf("observability.boot_log must be %s, %s, %s, or %s", BootLogAuto, BootLogTree, BootLogRecord, BootLogOff)
	}
	if err := validateQueryLogConfig(observability.Query); err != nil {
		return err
	}
	if err := validateTraceConfig(observability.Trace); err != nil {
		return err
	}
	return validateMetricsConfig(observability.Metrics)
}

// validateMetricsConfig rejects a toggle and a temporality nobody can act on,
// before any request is served rather than at the first collection.
func validateMetricsConfig(config MetricsConfig) error {
	if _, err := resolveToggle(config.Enabled, false); err != nil {
		return fmt.Errorf("observability.metrics.enabled %w", err)
	}
	if _, err := pwobservability.ParseTemporality(config.Temporality); err != nil {
		return fmt.Errorf("observability.metrics.temporality: %w", err)
	}
	if config.Interval < 0 {
		return fmt.Errorf("observability.metrics.interval cannot be negative")
	}
	return nil
}

// validateCompressionCodings refuses a coding this framework does not know.
//
// A token that names a real coding but one this build cannot encode is not
// refused here: which encoders are linked is a build-time decision, and it has
// to win over the configuration rather than turn a working file into a startup
// failure on a smaller target. Those are dropped when a response negotiates,
// and reported by compressionCodingsUnavailable.
//
// An empty list is not a way to disable compression, because middleware.
// compression already is one and two spellings of off would be one too many.
// It resolves to the framework's own order instead.
func validateCompressionCodings(config MiddlewareConfig) error {
	for token := range codingTokens(config.CompressionCodings) {
		if !knownResponseCoding(token) {
			return fmt.Errorf("middleware.compression_codings does not accept %q; the dynamic codings are %s and %s", token, zstdContentEncoding, gzipContentEncoding)
		}
	}
	return nil
}

// validateSessionConfig enforces the browser cookie policy, which is diagnosed
// as PW0410 and refused here.
//
// A session cookie without Secure travels over plain http, so anything that
// reads the traffic holds the session. api:cli-init writes secure = false into
// the development configuration on purpose, because loopback development serves
// no TLS; every other environment is a deployment, and the process refuses to
// start rather than serve one session that way. A cross-site cookie is refused
// in every environment, dev included, because no browser accepts it and the
// login would fail there too.
//
// development is whether the development relaxations apply, which an unset
// APP_ENV satisfies: it resolves to development, and running with no environment
// set is what working on an application looks like. A deployment that forgot the
// variable therefore keeps this exception, and hears about it from the startup
// warning rather than from a refusal.
func validateSessionConfig(config SessionConfig, env string, development bool) error {
	if !config.Enabled {
		return nil
	}
	if (config.Backend == SessionBackendDevVolatile || config.Backend == SessionBackendDevPersist) && !development {
		return fmt.Errorf("session.backend = %q is available only when %s is %q", config.Backend, EnvVar, EnvDevelopment)
	}
	if config.Cookie.Secure {
		return nil
	}
	sameSite, err := pwsession.ParseSameSite(config.Cookie.SameSite)
	if err != nil {
		return err
	}
	if sameSite == http.SameSiteNoneMode {
		return fmt.Errorf("session.cookie.same_site is none without session.cookie.secure, which no browser accepts as a cross-site cookie")
	}
	if !development {
		return fmt.Errorf("session.cookie.secure must be true when %s is %q; false is a development-only exception, and it lets the session cookie travel over plain http", EnvVar, env)
	}
	return nil
}

// validateHTMLConfig rejects a negative await bound. Generated apply functions
// only assign, so a rule like this one belongs with the other runtime checks
// rather than in the binding.
func validateHTMLConfig(config HTMLConfig) error {
	for key, value := range map[string]time.Duration{
		"html.async_timeout":     config.AsyncTimeout,
		"html.bot_async_timeout": config.BotAsyncTimeout,
	} {
		if value < 0 {
			return fmt.Errorf("%s must not be negative", key)
		}
	}
	return nil
}

func validateQueryLogConfig(config QueryLogConfig) error {
	for key, value := range map[string]string{
		"observability.query.enabled":     config.Enabled,
		"observability.query.bind_values": config.BindValues,
	} {
		if _, err := resolveToggle(value, false); err != nil {
			return fmt.Errorf("%s %w", key, err)
		}
	}
	for key, value := range map[string]string{
		"observability.query.level":      config.Level,
		"observability.query.slow_level": config.SlowLevel,
	} {
		if _, err := pwobservability.ParseQueryLevel(value); err != nil {
			return fmt.Errorf("%s %w", key, err)
		}
	}
	if config.SlowThreshold < 0 {
		return fmt.Errorf("observability.query.slow_threshold must not be negative")
	}
	// Zero means unset, so a hand-written configuration may omit a bound and
	// take the default. Only a negative bound is meaningless.
	if config.MaxSQLLength < 0 {
		return fmt.Errorf("observability.query.max_sql_length must not be negative")
	}
	if config.MaxValueLength < 0 {
		return fmt.Errorf("observability.query.max_value_length must not be negative")
	}
	return nil
}

// validateTraceConfig rejects a toggle nobody can act on. The keys below it are
// plain booleans the binding already checks.
//
// The sampler is checked here rather than where the provider is built, because a
// process that exports nothing builds no provider and a mistyped sampler would
// then be reported only once an endpoint was added. An empty name is not
// checked: it is the environment's default, and pwobservability resolves it to a
// name this same parser accepts.
func validateTraceConfig(config TraceConfig) error {
	if _, err := resolveToggle(config.Enabled, false); err != nil {
		return fmt.Errorf("observability.trace.enabled %w", err)
	}
	if name := strings.TrimSpace(config.Sampler); name != "" {
		if _, err := otetrace.ParseSampler(name, strings.TrimSpace(config.SamplerArg)); err != nil {
			return fmt.Errorf("observability.trace.sampler: %w", err)
		}
	}
	return nil
}

func validateRDBConfig(config RDBConfig) error {
	if !config.Enabled {
		return nil
	}
	connections, err := pwconfig.ResolveConnections(config)
	if err != nil {
		return err
	}
	members := make(map[string]int, len(connections))
	drivers := make(map[string]string, len(connections))
	for index, connection := range connections {
		key := connectionKey(index)
		if err := validateGroupName(connection.Group, key); err != nil {
			return err
		}
		target, err := pwconfig.Target(connection.DSN)
		if err != nil {
			return fmt.Errorf("%s.dsn: %w", key, err)
		}
		driver := target.Dialect
		// One group is one logical database, so a mixed-driver group would make
		// dialect and savepoint support depend on which replica answered.
		if previous, seen := drivers[connection.Group]; seen && previous != driver {
			return fmt.Errorf("connection group %q mixes the %s and %s drivers", connection.Group, previous, driver)
		}
		drivers[connection.Group] = driver
		if err := validateConnectionPool(connection, key); err != nil {
			return err
		}
		members[connection.Group]++
	}
	for index, connection := range connections {
		if connection.DSN == "sqlite://:memory:" && members[connection.Group] > 1 {
			return fmt.Errorf("%s: sqlite://:memory: cannot share a group, because each such DSN is a separate database", connectionKey(index))
		}
	}
	if _, err := pwconfig.ResolveDefaultGroup(config, connections); err != nil {
		return err
	}
	if _, err := pwconfig.ResolveWriteGroup(config, connections); err != nil {
		return err
	}
	if _, err := pwconfig.ResolveMigrationGroup(config, connections); err != nil {
		return err
	}
	return nil
}

// connectionKey names one connection the way it appears in the file, so an
// error points at the element the operator has to edit.
func connectionKey(index int) string {
	return fmt.Sprintf("middleware.rdb.connections[%d]", index)
}

func validateGroupName(group, key string) error {
	if group == "" {
		return fmt.Errorf("%s.group must not be empty", key)
	}
	for _, r := range group {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return fmt.Errorf("%s.group %q must use lower-case letters, digits, underscore, or hyphen", key, group)
		}
	}
	return nil
}

func validateConnectionPool(connection RDBConnectionConfig, key string) error {
	if connection.ConnectTimeout <= 0 {
		return fmt.Errorf("%s.connect_timeout must be positive", key)
	}
	if connection.MaxOpenConns < 0 || connection.MaxIdleConns < 0 {
		return fmt.Errorf("%s pool sizes must not be negative", key)
	}
	if connection.MaxOpenConns > 0 && connection.MaxIdleConns > connection.MaxOpenConns {
		return fmt.Errorf("%s.max_idle_conns must not exceed max_open_conns", key)
	}
	if connection.ConnMaxLifetime < 0 || connection.ConnMaxIdleTime < 0 {
		return fmt.Errorf("%s connection durations must not be negative", key)
	}
	if connection.DSN == "sqlite://:memory:" && connection.MaxOpenConns != 1 {
		return fmt.Errorf("%s.max_open_conns must be 1 for sqlite://:memory:", key)
	}
	return nil
}

func validateServerConfig(config ServerConfig) error {
	if config.Port < 0 || config.Port > 65535 {
		return fmt.Errorf("server.port must be between 0 and 65535")
	}
	for key, value := range map[string]time.Duration{
		"read_header_timeout": config.ReadHeaderTimeout,
		"read_timeout":        config.ReadTimeout,
		"write_timeout":       config.WriteTimeout,
		"idle_timeout":        config.IdleTimeout,
	} {
		if value < 0 {
			return fmt.Errorf("server.%s must not be negative", key)
		}
	}
	if config.ShutdownTimeout <= 0 {
		return fmt.Errorf("server.shutdown_timeout must be positive")
	}
	if config.MaxRequestBody < 0 {
		return fmt.Errorf("server.max_request_body must not be negative")
	}
	if config.CBORMaxBody < 0 {
		return fmt.Errorf("server.cbor_max_body must not be negative")
	}
	if _, err := compileTrustedProxies(config.TrustedProxies); err != nil {
		return err
	}
	switch config.APIDoc {
	case "", APIDocScalar, APIDocSwagger:
	default:
		return fmt.Errorf("server.api_doc must be %q, %q, or empty", APIDocScalar, APIDocSwagger)
	}
	if config.APIDoc != "" && config.OpenAPI == "" {
		return fmt.Errorf("server.api_doc requires server.openapi")
	}
	// Validated through the shared resolver, so this transport refuses exactly
	// what the other one does rather than through a second reading of the same
	// two rules.
	if _, err := pwruntime.ResolveAPICatalog(pwruntime.APICatalogSettings{
		Enabled: config.APICatalog, Origin: config.APICatalogOrigin,
		OpenAPI: config.OpenAPI, APIDoc: config.APIDoc,
		APIDocPath: config.APIDocPath, Health: config.Health,
	}); err != nil {
		return err
	}
	seen := map[string]string{}
	for key, endpoint := range operationalEndpointPaths(config) {
		if err := validateEndpointPath(key, endpoint); err != nil {
			return err
		}
		if previous, exists := seen[endpoint]; exists {
			return fmt.Errorf("%s duplicates %s: %s", key, previous, endpoint)
		}
		seen[endpoint] = key
	}
	if config.Public.Enabled {
		mount, err := middlewares.NormalizePublicMount(config.Public.Mount)
		if err != nil {
			return err
		}
		for endpoint, key := range seen {
			if strings.HasPrefix(endpoint+"/", mount) || strings.HasPrefix(mount, endpoint+"/") {
				return fmt.Errorf("server.public.mount overlaps %s: %s", key, endpoint)
			}
		}
	}
	return nil
}

// operationalEndpointPaths maps the configuration key of every enabled
// framework-owned endpoint to the path it serves. An empty path is what
// disables an endpoint, so it contributes no entry rather than an invalid one.
func operationalEndpointPaths(config ServerConfig) map[string]string {
	paths := map[string]string{}
	for key, endpoint := range map[string]string{
		"server.health":    config.Health,
		"server.readiness": config.Readiness,
		"server.openapi":   config.OpenAPI,
	} {
		if endpoint != "" {
			paths[key] = endpoint
		}
	}
	if config.APIDoc != "" {
		paths["server.api_doc_path"] = config.APIDocPath
	}
	// The catalog's path is RFC 9727's rather than the deployment's, so it
	// carries no key of its own; it is listed here so it is checked for the
	// same collisions as the endpoints it links to.
	if config.APICatalog {
		paths["server.api_catalog"] = pwruntime.APICatalogPath
	}
	return paths
}

func validateEndpointPath(key, value string) error {
	if value == "" || value[0] != '/' {
		return fmt.Errorf("%s must be an absolute path", key)
	}
	if path.Clean(value) != value {
		return fmt.Errorf("%s must be canonical", key)
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s is invalid", key)
	}
	if hasControl(value) {
		return fmt.Errorf("%s contains control characters", key)
	}
	return nil
}

func validateOperationalEndpointCollisions(handler http.Handler, config ServerConfig) error {
	resolver, ok := handler.(interface {
		Handler(*http.Request) (http.Handler, string)
	})
	if !ok {
		return nil
	}
	for key, endpoint := range operationalEndpointPaths(config) {
		request, err := http.NewRequest(http.MethodGet, "http://popcornweb.invalid"+endpoint, nil)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		_, pattern := resolver.Handler(request)
		if exactRoutePath(pattern) == endpoint {
			return fmt.Errorf("%s collides with application route %q", key, pattern)
		}
	}
	if config.Public.Enabled {
		mount, err := middlewares.NormalizePublicMount(config.Public.Mount)
		if err != nil {
			return err
		}
		for _, requestPath := range []string{strings.TrimSuffix(mount, "/"), mount, mount + "collision"} {
			request, err := http.NewRequest(http.MethodGet, "http://popcornweb.invalid"+requestPath, nil)
			if err != nil {
				return fmt.Errorf("server.public.mount: %w", err)
			}
			_, pattern := resolver.Handler(request)
			route := exactRoutePath(pattern)
			if route != "" && route != "/" && publicPathsOverlap(route, mount) {
				return fmt.Errorf("server.public.mount collides with application route %q", pattern)
			}
		}
	}
	return nil
}

func publicPathsOverlap(route, mount string) bool {
	route = strings.TrimSuffix(route, "{$}")
	route = strings.TrimSuffix(route, "...")
	route = strings.TrimSuffix(route, "/")
	mount = strings.TrimSuffix(mount, "/")
	return route == mount || strings.HasPrefix(route+"/", mount+"/") || strings.HasPrefix(mount+"/", route+"/")
}

func exactRoutePath(pattern string) string {
	if pattern == "" {
		return ""
	}
	fields := strings.Fields(pattern)
	if len(fields) > 0 {
		pattern = fields[len(fields)-1]
	}
	if index := strings.Index(pattern, "/"); index >= 0 {
		return pattern[index:]
	}
	return ""
}

func validateSecurityConfig(config SecurityConfig) error {
	headers := config.Headers
	switch strings.ToLower(headers.FrameOptions) {
	case "deny", "sameorigin", "off":
	default:
		return fmt.Errorf("security.headers.frame_options must be deny, sameorigin, or off")
	}
	switch strings.ToLower(headers.ReferrerPolicy) {
	case "no-referrer", "same-origin", "strict-origin", "strict-origin-when-cross-origin":
	default:
		return fmt.Errorf("security.headers.referrer_policy is invalid")
	}
	for key, value := range map[string]string{
		"content_security_policy":             headers.ContentSecurityPolicy,
		"content_security_policy_report_only": headers.ContentSecurityPolicyReportOnly,
		"permissions_policy":                  headers.PermissionsPolicy,
	} {
		if hasControl(value) {
			return fmt.Errorf("security.headers.%s contains control characters", key)
		}
	}
	if headers.HSTS.MaxAge < 0 {
		return fmt.Errorf("security.headers.hsts.max_age must not be negative")
	}
	if headers.HSTS.Enabled && headers.HSTS.MaxAge <= 0 {
		return fmt.Errorf("security.headers.hsts.max_age must be positive when HSTS is enabled")
	}
	if headers.HSTS.Preload && !headers.HSTS.IncludeSubdomains {
		return fmt.Errorf("security.headers.hsts.preload requires include_subdomains")
	}
	// The cross-origin half is validated by the shared leaf, so this reports
	// the same refusal the frame would raise later and the two cannot disagree
	// about what a policy means.
	return config.CORS.Validate()
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// compileTrustedProxies resolves the declared proxy set, naming the key the
// values came from. The compilation itself is internal/requestorigin, which is
// also what reads the headers this trust set gates.
func compileTrustedProxies(values []string) ([]*net.IPNet, error) {
	proxies, err := requestorigin.Compile(values)
	if err != nil {
		return nil, fmt.Errorf("server.trusted_proxies %w", err)
	}
	return proxies.Networks(), nil
}
