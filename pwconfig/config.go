// Package pwconfig holds the framework's own configuration bindings and the
// registry that resolves them.
//
// It lives here rather than in pw for the reason sessionconfig does, one layer
// wider: a settings file is not a transport concern, so the runtime that reads
// it and the runtime that serves the request need not be the same one. pw
// re-exports every type below as a true alias, so an application writes
// pw.ServerConfig and nothing about it changed; what changed is that a build
// serving on another transport can bind the same settings without linking the
// net/http runtime to do it.
//
// The alias must stay an alias. The registry is keyed by reflect.Type, and a
// defined type in pw would be a different one, so every lookup would silently
// miss.
//
// # What is here and what is not
//
// Here: the bindings, their registration, the load, the environment the load
// resolves against, and the group resolution that decides which configured
// database receives a migration. Each is a question a settings file answers.
//
// Not here: the startup summary, the framework subcommands, the database pools,
// and the extension chain. Those act on what was resolved rather than resolving
// it, and each is reached through a hook so that this package does not have to
// know which runtime is above it.
//
// Nothing here imports a transport, which is what keeps it usable from either.
package pwconfig

import (
	"time"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/sessionconfig"
)

// ServerConfig controls the primary HTTP listener and operational endpoints.
//
// The five timeouts and the body cap are rated as detail: each bounds how the
// listener behaves rather than what it serves, and a deployment that has not
// touched one has nothing to say about it. The port is not rated, because it is
// the first thing a reader looks for, and neither are the endpoint paths below.
type ServerConfig struct {
	Port              int           `opt:"port" env:"PORT" default:"8080" help:"HTTP listen port"`
	ReadHeaderTimeout time.Duration `default:"5s" summary:"omit" help:"request header read timeout"`
	ReadTimeout       time.Duration `default:"30s" summary:"omit" help:"request read timeout"`
	WriteTimeout      time.Duration `default:"0s" summary:"omit" help:"response write timeout; zero permits long-lived streams"`
	IdleTimeout       time.Duration `default:"2m" summary:"omit" help:"keep-alive idle timeout"`
	ShutdownTimeout   time.Duration `default:"10s" summary:"omit" help:"graceful shutdown timeout"`
	MaxRequestBody    int64         `default:"10485760" summary:"omit" help:"maximum request body in bytes"`
	// CBORMaxBody is separate from MaxRequestBody because a CBOR body is read
	// whole before decoding, so its cap answers to decode memory rather than
	// to transfer size. It only matters to a build whose generation enabled
	// CBOR bodies; every other build has no read this bounds.
	CBORMaxBody    int64    `default:"0" summary:"omit" help:"maximum CBOR request body in bytes; 0 keeps the 1 MiB default"`
	TrustedProxies []string `help:"trusted proxy IP or CIDR"`
	// Health, Readiness, and OpenAPI are the paths their endpoints serve, and
	// an unset path serves nothing. They carry no default so that the operator
	// reading a deployment's configuration sees every address it answers on;
	// a default would leave three endpoints running that no file mentions.
	Health     string `help:"liveness endpoint path, e.g. /healthz; unset serves none"`
	Readiness  string `help:"readiness endpoint path, e.g. /readyz; unset serves none"`
	OpenAPI    string `key:"openapi" help:"OpenAPI document path, e.g. /openapi.json; unset serves none"`
	APIDoc     string `help:"API documentation UI: scalar, swagger, or empty to disable"`
	APIDocPath string `default:"/docs" dependon:".api_doc" help:"API documentation UI path"`
	// APICatalog answers the RFC 9727 well-known URI with a Linkset over the
	// four settings above. It is a switch and not a path because the standard
	// fixes the location, which is the one endpoint address an operator does
	// not have to read out of a settings file to know.
	//
	// APICatalogOrigin is the absolute scheme-and-host the catalog's links are
	// built from. RFC 9264 asks for references that are not relative, so that a
	// catalog kept after the exchange that delivered it still resolves, and
	// this is the only setting that names the origin this deployment answers
	// on. Unset writes the links relative rather than guessing an origin from
	// the caller's own Host: see the note on pwruntime.buildAPICatalog.
	APICatalog       bool   `help:"answer /.well-known/api-catalog with an RFC 9727 API catalog"`
	APICatalogOrigin string `dependon:".api_catalog" help:"absolute origin the API catalog's links are built from, e.g. https://api.example.com; unset writes them relative"`
	// Public serves the application's embedded static assets.
	Public PublicConfig `help:"framework-owned static asset endpoint"`
}

// HTMLConfig bounds and gates progressive HTML rendering. A template that opens
// an await boundary renders correctly under either setting; this only decides
// whether the fallbacks reach the browser before the work behind them settles.
type HTMLConfig struct {
	// Streaming false forces the buffered branch even when a chain can open a
	// boundary, which is the escape hatch for a proxy that buffers responses.
	Streaming bool `default:"true" help:"Streaming false forces the buffered branch even when a chain can open a boundary, which is the escape hatch for a proxy that buffers responses"`
	// AsyncTimeout bounds one await boundary. Zero leaves the request context as
	// the only deadline.
	AsyncTimeout time.Duration `default:"3s" summary:"omit" help:"AsyncTimeout bounds one await boundary. Zero leaves the request context as the only deadline"`
	// AsyncConcurrency bounds simultaneously running boundary work across one
	// render. Zero or less is unbounded.
	AsyncConcurrency int `default:"0" summary:"omit" help:"AsyncConcurrency bounds simultaneously running boundary work across one render. Zero or less is unbounded"`
	// BotDetection forces the buffered branch for a client that will not run
	// the boundary runtime, so a crawler indexes the page instead of the
	// fallbacks. False skips classification entirely.
	BotDetection bool `default:"true" help:"render the settled document for crawlers and CLI clients"`
	// BotAsyncTimeout bounds one await boundary on a classified bot request,
	// which waits for every boundary before any byte leaves. Zero falls back to
	// AsyncTimeout rather than meaning unbounded, so a misread key cannot hold a
	// crawler connection open for the whole request deadline.
	BotAsyncTimeout time.Duration `default:"5s" dependon:".bot_detection" summary:"omit" help:"await boundary bound for a classified bot request"`
	// BotUserAgents extends the built-in catalog. Entries are appended and
	// matched case-insensitively; they never replace a built-in token.
	BotUserAgents []string `dependon:".bot_detection" help:"additional bot User-Agent substrings"`
	// ScriptlessDetection asks a browser with scripting disabled to identify
	// itself through a noscript redirect, so it receives the settled document
	// rather than fallbacks nothing arrives to replace. It costs such a client
	// one extra round trip on its first page and costs every other client
	// nothing. False leaves that client on today's streamed response.
	ScriptlessDetection bool `default:"true" help:"serve the settled document to a browser with scripting disabled, via a noscript redirect"`
	// Update turns on partial updates: the mode negotiation, the redraw
	// endpoint, and the runtime tag.
	Update HTMLUpdateConfig `help:"Update turns on partial updates: the mode negotiation, the redraw endpoint, and the runtime tag"`
	// Live answers the live mode request that keeps a page updating after its
	// document is complete. False leaves every document valid and static: the
	// content a live boundary committed stays, and no client is told to connect.
	// It depends on streaming, because a buffered document settles its live
	// boundaries in place and holds no placeholder a delivery could replace.
	Live bool `default:"true" dependon:".streaming" help:"answer the live mode request that keeps a page updating after the document is complete"`
	// LiveMaxDuration closes a healthy live response and expects the client to
	// reconnect. A bounded lifetime is what buys back authorization re-checks,
	// deploy rollover, and load rebalancing; the price is one page execution per
	// rollover. Zero leaves the request context as the only bound.
	LiveMaxDuration time.Duration `default:"10m0s" dependon:".live" summary:"omit" help:"maximum lifetime of one live response before it closes and the client reconnects"`
	// LiveDurationJitter spreads that lifetime, as a percentage of it. Without
	// it one restart synchronizes every client and the herd repeats forever,
	// which no client-side backoff can undo because the server chose the moment.
	LiveDurationJitter int `default:"20" dependon:".live" summary:"omit" help:"percentage the live response lifetime is spread by, so clients do not reconnect in lockstep"`
	// LiveIdleTimeout closes a live response no source has delivered on. Zero
	// disables the bound, which is right only where a quiet source is expected
	// to stay quiet for hours.
	LiveIdleTimeout time.Duration `default:"5m0s" dependon:".live" summary:"omit" help:"close a live response after this long with no delivery"`
	// LiveMaxBoundaries bounds how many distinct boundaries one live response
	// may serve. Reaching it closes the response rather than dropping deliveries
	// silently. Zero or less is unbounded.
	LiveMaxBoundaries int `default:"32" dependon:".live" summary:"omit" help:"maximum boundaries one live response may serve"`
	// LiveMaxResponses bounds concurrent live responses per client, so reopening
	// cannot multiply subscriptions. Zero or less is unbounded.
	LiveMaxResponses int `default:"4" dependon:".live" summary:"omit" help:"maximum concurrent live responses per client"`
	// LiveMaxSignalBytes bounds the signal payloads one live response may write.
	//
	// A signal is the one thing on this wire whose size an application chooses
	// directly: a delivery is bounded by what a boundary renders, where a
	// payload is a struct an author names and the module transfers verbatim. On
	// a connection that lives as long as a browser tab, an emitter in a loop is
	// an unbounded write with no render behind it to slow it down.
	//
	// Reaching it closes the response with a retry rather than dropping records,
	// because a screen missing an instruction it was sent is worse than one that
	// reconnects: the reconnect re-executes the page and the source produces the
	// current state again. Zero or less is unbounded.
	LiveMaxSignalBytes int `default:"262144" dependon:".live" summary:"omit" help:"maximum total signal payload bytes one live response may write"`
	// Cache supplies the store behind the template's cache annotation. Rated as
	// detail: it bounds what the process holds, and the annotation rather than
	// this is what makes caching happen.
	Cache HTMLCacheConfig `summary:"omit" help:"Cache supplies the store behind the template's cache annotation"`
}

// defaultHTMLConfig seeds the effective configuration for a runtime that never
// parses a config source. The parse-time defaults live in the struct tags
// above; TestHTMLDefaultsMatchTags holds the two in agreement.
//
// BotAsyncTimeout sits above AsyncTimeout because an indexer waits far longer
// than a browser, and a timeout fallback baked into a buffered document is the
// outcome bot detection exists to prevent. It stays well short of the request
// deadline because a link preview spider abandons a slow response within a few
// seconds, and the buffered branch has no head start to offer it.
var defaultHTMLConfig = HTMLConfig{
	Streaming:           true,
	AsyncTimeout:        3 * time.Second,
	BotDetection:        true,
	BotAsyncTimeout:     5 * time.Second,
	ScriptlessDetection: true,
	Live:                true,
	LiveMaxDuration:     10 * time.Minute,
	LiveDurationJitter:  20,
	LiveIdleTimeout:     5 * time.Minute,
	LiveMaxBoundaries:   32,
	LiveMaxResponses:    4,
	LiveMaxSignalBytes:  256 * 1024,
	Cache:               HTMLCacheConfig{Enabled: true, MaxEntries: 1024},
}

// HTMLUpdateConfig controls partial updates.
//
// Off by default. A project turns it on when it wants a page to refresh a
// region rather than reload, and the validator key is required with it: an
// unkeyed digest of low-entropy content lets a guess be confirmed by comparing
// digests, so startup refuses the combination rather than serving one.
type HTMLUpdateConfig struct {
	Enabled bool `default:"false" help:"answer navigation deltas, redraws, and action responses"`
	// ValidatorKey keys the frame and input validators. Rotating it is not a
	// break: comparisons miss and the next response is a complete document.
	ValidatorKey string `secret:"mask" env:"HTML_UPDATE_VALIDATOR_KEY" dependon:".enabled" help:"base64 or raw secret keying update validators"`
	// MaxManifestBytes caps the hint header a request may carry. An oversized
	// one is dropped rather than rejected, so the response is a larger delta
	// instead of an error.
	MaxManifestBytes int `default:"8192" dependon:".enabled" help:"cap on the update manifest request header"`
}

// HTMLCacheConfig controls the output cache a component asks for with the
// template's cache annotation.
//
// On by default, which is the opposite of every other capability here. The
// opt-in is the annotation: generation refuses one on a component whose stored
// bytes could not stand in for a fresh render, so a template carrying it has
// already been checked and has already asked. A project writing none never
// reaches the store, because no plan carries a policy to consult it with.
//
// The setting is here to bound what the process holds and to be the escape
// hatch for an operator who suspects a stale region, not to be the switch that
// makes the annotation mean something.
type HTMLCacheConfig struct {
	Enabled bool `default:"true" help:"reuse the rendered output of components declared with the cache annotation"`
	// MaxEntries bounds the in-process store. Zero or less is unbounded, which
	// is right only where every cached component has a bounded parameter space:
	// the key covers every declared parameter, so one taking an arbitrary
	// string has as many entries as it has callers.
	MaxEntries int `default:"1024" dependon:".enabled" help:"maximum entries the in-process render cache holds"`
}

// CacheConfig is the named data cache store set, configured as
// [[cache.stores]]. It is separate from html.cache: that store holds rendered
// bytes sized for one entry per parameter set, and this one holds what a fetch
// returned.
type CacheConfig = pwruntime.CacheConfig

// CacheStoreConfig is one store of the data cache set.
type CacheStoreConfig = pwruntime.CacheStoreConfig

// PublicConfig controls the framework-owned static asset endpoint.
type PublicConfig = middlewares.PublicAssetConfig

// SecurityConfig controls framework request and response security policy.
type SecurityConfig struct {
	Headers SecurityHeadersConfig
	// CORS is here rather than under middleware because it is browser policy
	// resolved and validated at startup, like the two beside it, and because it
	// is answered by the same frame the headers are.
	CORS CORSConfig `help:"CORS is here rather than under middleware because it is browser policy resolved and validated at startup, like the two beside it, and because it is answered by the same frame the headers are"`
	CSRF CSRFConfig
}

// CORSConfig controls which other origins may read this deployment.
type CORSConfig = middlewares.CORSConfig

// CSRFConfig controls the synchronizer-token check on unsafe browser requests.
type CSRFConfig = middlewares.CSRFConfig

// RateLimitConfig bounds how often one caller, and the process as a whole, may
// arrive within a window. It is its own binding rather than a member of
// SecurityConfig, because a backend selection carrying a DSN does not belong
// beside a list of response headers.
type RateLimitConfig = middlewares.RateLimitConfig

// RateLimitRedisConfig addresses the shared counter server.
type RateLimitRedisConfig = middlewares.RateLimitRedisConfig

// SecurityHeadersConfig controls browser-facing response headers.
type SecurityHeadersConfig = middlewares.SecurityHeadersConfig

// HSTSConfig controls Strict-Transport-Security on verified HTTPS requests.
type HSTSConfig = middlewares.HSTSConfig

// Session storage backends. They name which server backend a server-placed
// slot uses, never whether a slot is server-placed, which
// RegisterSessionStore states instead.
const (
	SessionBackendRDB         = sessionconfig.SessionBackendRDB
	SessionBackendCookie      = sessionconfig.SessionBackendCookie
	SessionBackendDevVolatile = sessionconfig.SessionBackendDevVolatile
	SessionBackendDevPersist  = sessionconfig.SessionBackendDevPersist
	SessionBackendRedis       = sessionconfig.SessionBackendRedis
	SessionBackendDynamo      = sessionconfig.SessionBackendDynamo
	SessionBackendFirestore   = sessionconfig.SessionBackendFirestore
)

// The [session] binding. Every type is a true alias of the one declared in
// sessionconfig, so pw and popcornweb/plugin/auth name one type from two
// packages without depending on each other, and the reflect.Type keyed
// configuration registry resolves both names to one entry.
//
// The alias must stay an alias. A defined type here would be a different
// reflect.Type, and the registry lookup would silently miss.
type (
	SessionConfig            = sessionconfig.SessionConfig
	SessionCookieConfig      = sessionconfig.SessionCookieConfig
	SessionRDBConfig         = sessionconfig.SessionRDBConfig
	SessionRedisConfig       = sessionconfig.SessionRedisConfig
	SessionDynamoConfig      = sessionconfig.SessionDynamoConfig
	SessionCookieStoreConfig = sessionconfig.SessionCookieStoreConfig
	SessionKeyringConfig     = sessionconfig.SessionKeyringConfig
)

// ObservabilityConfig controls runtime logging, tracing, and service identity.
type ObservabilityConfig struct {
	// MinimumLevel is the severity floor. Records below it cost one comparison.
	MinimumLevel string `default:"info" help:"severity floor: trace, debug, info, warn, error, or off"`
	// StdoutFormat selects the terminal encoding.
	StdoutFormat string `default:"json" enum:"json,plaintext" help:"terminal record encoding: json or plaintext"`
	ServiceName  string `env:"OTEL_SERVICE_NAME"`
	// ResourceAttributes are extra key=value identifiers reported to the
	// collector alongside the service name.
	ResourceAttributes []string `help:"extra key=value identifiers reported with the service name"`
	// BootLog selects the startup summary format: auto, tree, record, or off.
	BootLog string `default:"auto" help:"startup summary: auto, tree, record, or off"`
	// Query configures the development query diagnostics.
	//
	// Rated as detail: every key under it tunes how much a diagnostic record
	// says, which is a question an operator asks of the diagnostics rather than
	// of the deployment. Set any one of them and it comes back.
	Query QueryLogConfig `summary:"omit" help:"Query configures the development query diagnostics"`
	// Trace configures the spans the framework creates inside a request. Rated
	// as detail for the same reason Query is.
	Trace TraceConfig `summary:"omit" help:"Trace configures the spans the framework creates inside a request"`
	// Metrics configures the instruments the framework records. Rated as detail
	// for the same reason Trace is.
	Metrics MetricsConfig `summary:"omit" help:"Metrics configures the instruments the framework records"`
	// Otel configures OpenTelemetry export.
	Otel OtelExportConfig `help:"Otel configures OpenTelemetry trace and log export"`
}

// MetricsConfig selects the instruments the framework records.
//
// It is a separate section from Trace, and its enabled key is independent of
// that one, because the two signals answer different questions: a trace records
// one request and may be sampled away, while an instrument counts every request.
// A deployment running a low sampling ratio is expected to end up with metrics on
// and tracing mostly off, which is the configuration this split exists for.
type MetricsConfig struct {
	// Enabled is auto, on, or off. Auto resolves to on when export is
	// configured and off otherwise, for the reason the trace toggle gives: an
	// aggregation nothing exports is pure cost.
	Enabled string `default:"auto" falsy:"off" help:"record framework metrics: auto, on, or off; auto follows metric export"`
	// Interval is how often a collection is exported, and so how coarse every
	// chart of these instruments becomes. It is separate from
	// otel.flush_interval, which is how often a batch of spans leaves.
	Interval time.Duration `default:"60s" dependon:".enabled" help:"how often metrics are collected and exported"`
	// Temporality is delta or cumulative. Delta reports each interval, which is
	// what the development viewer charts without differencing and what a
	// short-lived instance can report at all; a failed export loses those
	// counts. Cumulative reports the total since process start, so a failed
	// export is repaired by the next one at the cost of a reader that has to
	// notice a restart.
	Temporality string `default:"delta" enum:"delta,cumulative" dependon:".enabled" help:"metric temporality: delta or cumulative"`
	// HTTP records the semantic-convention http.server instruments.
	HTTP bool `default:"true" dependon:".enabled" help:"record http.server request duration, concurrency, and body sizes"`
	// DB records db.client.operation.duration on the statement seam.
	DB bool `default:"true" dependon:".enabled" help:"record db.client operation duration per driver and statement keyword"`
	// Runtime records the go.* runtime instruments. It is the one group with no
	// framework seam, and a deployment already collecting it from its own agent
	// turns it off here.
	Runtime bool `default:"true" dependon:".enabled" help:"record go.* runtime memory, goroutine, and gc instruments"`
	// Render records the pw.render, pw.boundary, and pw.live instruments.
	Render bool `default:"true" dependon:".enabled" help:"record pw.render duration and bytes, boundary settle, and live delivery"`
	// Cache records the component output and data result cache counters.
	Cache bool `default:"true" dependon:".enabled" help:"record component output and data result cache hits and misses"`
}

// TraceConfig selects the spans the framework opens inside a request.
//
// The request root span is not one of them: it belongs to the tracing
// middleware and exists whenever export does, because a trace with no root is
// not a trace. Everything here describes what the framework does inside that
// root, and each key is a separate switch because the two halves answer
// different questions — the render spans say where a response spent its time,
// and the database spans say which statements it ran.
type TraceConfig struct {
	// Enabled is auto, on, or off. Auto resolves to on when trace export is
	// configured and off otherwise, which is the honest default: a span nothing
	// exports is pure cost. Off disables every key below it.
	//
	// On without an exporter is still meaningful — it installs the request root
	// span too, so a project holding its own provider gets a complete tree.
	Enabled string `default:"auto" falsy:"off" help:"open framework spans: auto, on, or off; auto follows trace export"`
	// Render opens a span for one HTML response, with the initial build as its
	// first child.
	Render bool `default:"true" dependon:".enabled" help:"open a span per HTML response, with the initial build inside it"`
	// Boundary opens a span per settled async boundary and per live delivery.
	// It hangs off Render because a boundary span with no render span above it
	// would attach each fragment straight to the request root.
	Boundary bool `default:"true" dependon:".render" help:"open a span per settled async boundary and per live delivery"`
	// Database opens a client span per executed statement.
	Database bool `default:"true" dependon:".enabled" help:"open a client span per executed statement"`
	// Statement puts the statement text on that span. Bind values never reach
	// it whatever this says: they stay on the query record, which the span id
	// correlates.
	Statement bool `default:"true" dependon:".database" help:"put the statement text on the database span; bind values never reach a span"`
	// Sampler decides which traces are recorded, once, at the root span.
	//
	// It carries no default tag because the default is selected by the
	// environment rather than being one value everywhere: development records
	// every trace, because the developer loop's only view of a request is the
	// trace it kept, and every other environment samples, because a process may
	// be exporting straight to a collection backend that bills per span and one
	// request is a dozen of them. Setting this replaces both.
	Sampler string `env:"OTEL_TRACES_SAMPLER" dependon:".enabled" help:"which traces are recorded: always_on, always_off, traceidratio, or a parentbased_ form; defaults by environment"`
	// SamplerArg is the sampler's argument, which for either ratio form is the
	// fraction of traces kept. An unparseable value fails startup rather than
	// falling back to recording everything.
	SamplerArg string `env:"OTEL_TRACES_SAMPLER_ARG" dependon:".enabled" help:"sampler argument; the kept fraction for a traceidratio sampler"`
}

// OtelExportConfig configures OTLP/HTTP export of traces and logs.
//
// The endpoint and header variables are the standard OTLP ones, which is what
// lets api:cli-dev point an application at its local viewer without the project
// committing anything. OTEL_EXPORTER_OTLP_TIMEOUT is deliberately not bound to
// RequestTimeout: it counts milliseconds, while every duration here is a Go
// duration string, and one key cannot mean both.
//
// The defaults restate the bounds the exporter and the batch processors apply to
// a zero value, so a scaffolded file says what the process will do rather than
// showing a zero that means "ask someone else".
type OtelExportConfig struct {
	// Enabled is the switch every other key here answers to, so a process that
	// exports nothing reports one line instead of seven.
	//
	// api:cli-dev injects it along with the endpoint, which is what keeps the
	// injected address visible in the startup summary.
	Enabled bool `default:"false" help:"export traces and logs"`
	// Endpoint is the OTLP/HTTP base URL. /v1/traces and /v1/logs are appended.
	Endpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT" dependon:".enabled" help:"OTLP/HTTP base URL; /v1/traces and /v1/logs are appended"`
	// Headers is a comma-separated key=value list. It is where the collector's
	// credential lives — Authorization, api-key, x-honeycomb-team — so it is
	// masked, and the masking is the tag rather than a promise in this sentence.
	// It used to be only the sentence, and the boot log printed the value.
	Headers string `secret:"mask" env:"OTEL_EXPORTER_OTLP_HEADERS" dependon:".enabled" help:"comma-separated key=value list; values are never logged"`
	// RequestTimeout bounds one export request.
	RequestTimeout time.Duration `default:"10s" dependon:".enabled" help:"bounds one export request"`
	// QueueSize bounds records held in memory; a full queue drops rather than
	// blocking the request goroutine.
	QueueSize int `default:"2048" dependon:".enabled" help:"records held in memory; a full queue drops rather than blocking"`
	// MaxExportSize bounds one exported batch.
	MaxExportSize int `default:"512" dependon:".enabled" help:"bounds one exported batch"`
	// FlushInterval is how often a partial batch is sent.
	FlushInterval time.Duration `default:"5s" dependon:".enabled" help:"how often a partial batch is sent"`
}

// QueryLogConfig controls per-statement query logging, the slow-statement
// EXPLAIN, and the rerun snippet. It lives under observability because it
// produces log records; middleware.rdb stays pool configuration.
type QueryLogConfig struct {
	// Enabled is auto, on, or off. Auto resolves to on in the development
	// environment and off everywhere else. Off disables every key below it.
	Enabled string `default:"auto" falsy:"off" help:"log every generated SQL statement: auto, on, or off; auto is on in dev"`
	// Level is the severity of an ordinary statement record.
	Level string `default:"info" dependon:".enabled" help:"Level is the severity of an ordinary statement record"`
	// SlowThreshold is the duration above which a statement is slow. Zero
	// disables slow detection, and with it EXPLAIN and reproduction; falsy is
	// what lets those three name it as their parent.
	SlowThreshold time.Duration `default:"200ms" falsy:"0s" dependon:".enabled" help:"duration above which a statement is explained; zero disables it"`
	// SlowLevel is the severity of a slow statement record.
	SlowLevel string `default:"warn" dependon:".slow_threshold" help:"SlowLevel is the severity of a slow statement record"`
	// BindValues is auto, on, or off, and follows the same environment rule as
	// Enabled. It is the only path by which row values reach a query record.
	BindValues string `default:"auto" falsy:"off" dependon:".enabled" help:"log argument values: auto, on, or off; auto is on in dev"`
	// Explain captures a plan-only EXPLAIN for a slow statement.
	Explain bool `default:"true" dependon:".slow_threshold" help:"capture a plan-only EXPLAIN for a slow statement"`
	// Reproduction renders a paste-able rerun snippet for a slow statement.
	Reproduction bool `default:"true" dependon:".slow_threshold" help:"emit a paste-able rerun snippet for a slow statement"`
	// MaxSQLLength bounds the logged statement text.
	MaxSQLLength int `default:"4096" dependon:".enabled" help:"MaxSQLLength bounds the logged statement text"`
	// MaxValueLength bounds each logged argument value.
	MaxValueLength int `default:"256" dependon:".enabled" help:"MaxValueLength bounds each logged argument value"`
}

// MiddlewareConfig selects the framework's basic HTTP middleware.
type MiddlewareConfig struct {
	Recovery    bool `default:"true"`
	RequestID   bool `default:"true"`
	AccessLog   bool `default:"true"`
	Compression bool `default:"false"`
	// CompressionCodings orders the content codings a dynamic response may be
	// encoded with, best first. A coding left out is not offered even to a
	// client asking for it, so the one field expresses removal as well as
	// order; turning compression off entirely is Compression rather than an
	// empty list.
	//
	// Which coding to prefer depends on the client mix and the CPU budget in
	// front of the application, which is the one input the framework cannot
	// see. The encoder levels are not configurable for the opposite reason:
	// they answer to a measured throughput cliff that does not move between
	// deployments.
	CompressionCodings []string      `default:"zstd,gzip" dependon:".compression" help:"content codings for dynamic responses, best first"`
	RequestTimeout     time.Duration `default:"0s" summary:"omit"`
	RDB                RDBConfig
}

// RDBConfig controls the framework-owned database pools.
//
// Every database is configured with Connections, one element per pool: a single
// database is one element, and a reader-writer topology is several. There is no
// second form. The keys below name groups within that set, so the section
// carries no DSN of its own.
type RDBConfig struct {
	Enabled bool `default:"false"`
	// DefaultGroup serves statements that pin no group. It is normally the
	// replica group. Required once more than one group is configured.
	DefaultGroup string `dependon:".enabled" help:"connection group for statements that pin none"`
	// WriteGroup serves framework-owned writes. Empty resolves to the only
	// group holding a writable connection.
	WriteGroup string `dependon:".enabled" help:"connection group for framework-owned writes"`
	// MigrationGroup receives migrations and seed data. Empty resolves to
	// WriteGroup.
	MigrationGroup string `dependon:".enabled" help:"connection group for migrations and seeds"`
	// Connections is the array-of-tables form. An element takes no CLI option
	// and no environment variable, because its identity is its position in the
	// file rather than a stable key. The array itself has one, which is what
	// lets a disabled pool drop the whole set from the startup summary.
	Connections []RDBConnectionConfig `dependon:".enabled" help:"connection set, one element per pool"`
}

// RDBConnectionConfig is one pool of the connection set.
type RDBConnectionConfig struct {
	// Group is the name this connection is addressed by. Several connections
	// may share one group, which is what makes round robin expressible.
	Group string `help:"name this connection is addressed by"`
	DSN   string `secret:"mask"`
	// ReadOnly marks a replica: it opens read-only transactions and never
	// serves a framework-owned write.
	ReadOnly        bool          `key:"readonly" default:"false" help:"open read-only transactions and serve no framework write"`
	ConnectTimeout  time.Duration `default:"5s"`
	MaxOpenConns    int           `default:"0"`
	MaxIdleConns    int           `default:"0"`
	ConnMaxLifetime time.Duration `default:"0s"`
	ConnMaxIdleTime time.Duration `default:"0s"`
}

// DefaultHTMLConfig returns the values a runtime that never parses a
// configuration source is seeded with.
//
// It is a function returning a copy rather than an exported variable, because
// the seed is what every unparsed runtime starts from and a variable anything
// could assign to would make that a per-process accident.
func DefaultHTMLConfig() HTMLConfig { return defaultHTMLConfig }
