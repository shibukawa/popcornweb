package pwruntime

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shibukawa/popcornweb/internal/pathpattern"
)

// CORSConfig is the cross-origin admission policy.
//
// It lives in the shared leaf beside SecurityHeadersConfig for the reason that
// one does: this is arithmetic over configuration with no request in it, both
// chains read it, and one declaration means one set of binder tags with nothing
// that can drift between two readers of the same policy.
//
// Enabled is false by default, and a deployment that turns it on names the
// origins in the same breath. An admission policy with an empty list is a frame
// that marks nothing, which reads as cross-origin access that is not there.
type CORSConfig struct {
	Enabled bool `default:"false"`
	// Include and Exclude scope the policy, in the segment grammar the
	// authenticated-path check and the CSRF check already share.
	//
	// Scope earns its keep with credentials rather than without them: an
	// unscoped credentialed grant covers every authenticated page, every
	// side-effect-free GET the update runtime issues, and the live stream —
	// not the API the deployment meant to open. That is why AllowCredentials
	// refuses the default include rather than quietly inheriting it.
	Include []string `default:"/**" dependon:".enabled"`
	Exclude []string `env:"-" dependon:".enabled"`
	// AllowedOrigins are exact scheme://host[:port] values, or the single
	// literal "*".
	//
	// There is no pattern language. A subdomain wildcard is an expression in
	// framework configuration, which this framework refuses for the reason the
	// bearer admission policy gives: an expression is a second thing to get
	// right, evaluated on every request, whose mistakes are silent.
	AllowedOrigins []string `env:"-" dependon:".enabled"`
	// AllowCredentials lets a listed origin read a response the browser sent
	// cookies with.
	//
	// It grants a read and never a write. The CSRF token travels in a cookie
	// only same-origin script can read, so a cross-origin page cannot attach
	// the header the check wants and every unsafe request is refused whatever
	// this says.
	AllowCredentials bool `default:"false" dependon:".enabled"`
	// AllowedMethods are the methods a preflight admits. The default stops at
	// the three a browser can already send without one, so admitting a write is
	// something a deployment states rather than inherits.
	AllowedMethods []string `default:"GET,HEAD,POST" dependon:".enabled"`
	// AllowedHeaders are the request headers a preflight admits. The configured
	// CSRF header is added to this set on its own while credentials are on,
	// because a header that has to be remembered is the one that is forgotten.
	AllowedHeaders []string `default:"Content-Type,Authorization" dependon:".enabled"`
	// ExposedHeaders are the response headers script may read.
	//
	// The default is the set framework frames write and no browser exposes on
	// its own: the correlation identifier, and the four fields a 429 carries to
	// say when to come back. Without them a cross-origin client cannot be told
	// to back off, and keeps retrying at the rate that limited it.
	ExposedHeaders []string `default:"X-Request-ID,Retry-After,X-RateLimit-Limit,X-RateLimit-Remaining,X-RateLimit-Reset" dependon:".enabled"`
	// MaxAge bounds how long a browser may cache one preflight. Browsers cap it
	// themselves — Safari at ten minutes, Chrome at two hours — so a larger
	// value is reduced rather than honoured, and the default is the smallest
	// cap so every engine keeps the same answer.
	MaxAge time.Duration `default:"10m" dependon:".enabled" help:"how long a browser may cache one preflight"`
}

// corsWildcard is the one origin value that is not an origin.
const corsWildcard = "*"

// OpenAPIDocumentOrigin is the marking the generated OpenAPI document carries,
// whatever security.cors says and whether or not it is enabled at all.
//
// This one endpoint is answered by what it is rather than by a policy a
// deployment writes. The document describes a contract already chosen for
// publication, holds nothing that varies per visitor, and is read by tools
// whose origins nobody can enumerate in advance: a documentation UI hosted
// somewhere else, a client generator, a linter in someone's CI.
//
// It is safe on a protected document too. A wildcard forbids credentials, so a
// cross-origin page reading one that sits behind the authenticated-path check
// receives the unauthenticated answer and learns nothing it could not have
// learned by asking for the path itself.
//
// No Vary goes with it: the answer is the same for every caller, which is what
// keeps the document shared-cacheable.
var OpenAPIDocumentOrigin = ResponseHeader{Name: "Access-Control-Allow-Origin", Value: corsWildcard}

// DefaultCORS returns the shipped defaults: off, and admitting the reads a
// bearer API needs once turned on.
func DefaultCORS() CORSConfig {
	return CORSConfig{
		Include:        []string{"/**"},
		AllowedMethods: []string{"GET", "HEAD", "POST"},
		AllowedHeaders: []string{"Content-Type", "Authorization"},
		ExposedHeaders: []string{
			"X-Request-ID", "Retry-After",
			"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset",
		},
		MaxAge: 10 * time.Minute,
	}
}

// Validate rejects a policy that cannot mean what it says.
//
// Every refusal here is a configuration a browser would answer by dropping the
// response, or a grant wider than the deployment can have meant. Both fail at
// startup rather than per request, because the failure mode of a wrong CORS
// policy is a network error in somebody else's browser.
func (c CORSConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if len(c.AllowedOrigins) == 0 {
		return fmt.Errorf("popcornweb: security.cors.allowed_origins is empty; an enabled policy admitting no origin marks nothing")
	}
	wildcard := false
	for _, origin := range c.AllowedOrigins {
		if origin == corsWildcard {
			wildcard = true
			continue
		}
		if err := validateOrigin(origin); err != nil {
			return err
		}
	}
	if wildcard && len(c.AllowedOrigins) > 1 {
		return fmt.Errorf("popcornweb: security.cors.allowed_origins mixes %q with named origins; the wildcard is the whole list or none of it", corsWildcard)
	}
	if err := validateTokens("security.cors.allowed_methods", c.AllowedMethods); err != nil {
		return err
	}
	if err := validateTokens("security.cors.allowed_headers", c.AllowedHeaders); err != nil {
		return err
	}
	if err := validateTokens("security.cors.exposed_headers", c.ExposedHeaders); err != nil {
		return err
	}
	if c.AllowCredentials {
		if wildcard {
			return fmt.Errorf("popcornweb: security.cors.allow_credentials cannot be used with the %q origin; a browser drops a credentialed response carrying it", corsWildcard)
		}
		for _, header := range c.AllowedHeaders {
			if header == corsWildcard {
				return fmt.Errorf("popcornweb: security.cors.allowed_headers cannot use %q with allow_credentials, for the same reason the origin cannot", corsWildcard)
			}
		}
		for _, pattern := range c.Include {
			if pattern == "/**" {
				return fmt.Errorf("popcornweb: security.cors.allow_credentials with an include of \"/**\" grants a cross-origin read of every authenticated page and the live stream; name the paths being opened")
			}
		}
	}
	if c.MaxAge < 0 {
		return fmt.Errorf("popcornweb: security.cors.max_age cannot be negative")
	}
	return nil
}

// validateOrigin rejects everything that is not exactly scheme://host[:port].
//
// A path, a trailing slash, a query, or userinfo all mean the deployment wrote
// a URL where an origin was wanted, and every one of them fails to match the
// value a browser actually sends. The literal null is refused outright: a
// sandboxed frame and a data URL both present it, and neither is an origin a
// deployment can have meant to name.
func validateOrigin(origin string) error {
	if origin == "" || strings.EqualFold(origin, "null") {
		return fmt.Errorf("popcornweb: security.cors.allowed_origins entry %q is not an origin", origin)
	}
	if !validHeaderValue(origin) {
		return fmt.Errorf("popcornweb: security.cors.allowed_origins entry %q contains an invalid header value", origin)
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("popcornweb: security.cors.allowed_origins entry %q is not a URL: %w", origin, err)
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("popcornweb: security.cors.allowed_origins entry %q must use http or https", origin)
	}
	if parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("popcornweb: security.cors.allowed_origins entry %q must be scheme://host[:port] with no path, query, fragment, or userinfo", origin)
	}
	return nil
}

// validateTokens rejects a method or header name that is not one.
func validateTokens(key string, values []string) error {
	for _, value := range values {
		if value == corsWildcard {
			continue
		}
		if value == "" || !isHTTPToken(value) {
			return fmt.Errorf("popcornweb: %s entry %q is not a valid HTTP token", key, value)
		}
	}
	return nil
}

func isHTTPToken(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}

// ResolvedCORS is a validated policy reduced to what a request needs.
//
// Everything a response can carry is precomputed: the header values are joined
// once here rather than per request, and the two lookups a request performs are
// map reads.
type ResolvedCORS struct {
	enabled  bool
	include  []pathpattern.Pattern
	exclude  []pathpattern.Pattern
	origins  map[string]string
	wildcard bool
	// credentials is whether a listed origin may read a credentialed response.
	credentials bool
	methods     map[string]bool
	methodValue string
	headers     map[string]bool
	headerAny   bool
	headerValue string
	exposeValue string
	maxAgeValue string
	// varyOrigin is false only for the wildcard policy, whose answer is the
	// same for every caller and therefore stays shared-cacheable.
	varyOrigin bool
	budget     *corsLogBudget
}

// Enabled reports whether the policy installs anything.
func (c ResolvedCORS) Enabled() bool { return c.enabled }

// ResolveCORS validates a policy and reduces it to the values a response
// carries.
//
// csrfHeader is the configured CSRF header name. It is admitted with the
// configured headers while credentials are on, because a credentialed origin
// that may write has to send it, and a name that must be remembered in a second
// place is the one that is forgotten. Admitting the name grants nothing on its
// own: the token check still runs, and still refuses a request that cannot
// present a value derived from the session's own secret.
func ResolveCORS(config CORSConfig, csrfHeader string) (ResolvedCORS, error) {
	if err := config.Validate(); err != nil {
		return ResolvedCORS{}, err
	}
	if !config.Enabled {
		return ResolvedCORS{}, nil
	}
	include, err := pathpattern.Compile(config.Include)
	if err != nil {
		return ResolvedCORS{}, fmt.Errorf("popcornweb: security.cors.include: %w", err)
	}
	exclude, err := pathpattern.Compile(config.Exclude)
	if err != nil {
		return ResolvedCORS{}, fmt.Errorf("popcornweb: security.cors.exclude: %w", err)
	}
	resolved := ResolvedCORS{
		enabled:     true,
		include:     include,
		exclude:     exclude,
		origins:     make(map[string]string, len(config.AllowedOrigins)),
		credentials: config.AllowCredentials,
		methods:     make(map[string]bool, len(config.AllowedMethods)),
		headers:     make(map[string]bool, len(config.AllowedHeaders)+1),
		budget:      &corsLogBudget{},
	}
	for _, origin := range config.AllowedOrigins {
		if origin == corsWildcard {
			resolved.wildcard = true
			continue
		}
		// Matched folded and echoed as configured. A browser sends a lowercase
		// scheme and host, and echoing the validated value rather than the
		// caller's keeps a header this frame writes out of a caller's reach.
		resolved.origins[strings.ToLower(origin)] = origin
	}
	resolved.varyOrigin = !resolved.wildcard
	methods := make([]string, 0, len(config.AllowedMethods))
	for _, method := range config.AllowedMethods {
		upper := strings.ToUpper(method)
		if resolved.methods[upper] {
			continue
		}
		resolved.methods[upper] = true
		methods = append(methods, upper)
	}
	resolved.methodValue = strings.Join(methods, ", ")
	headers := make([]string, 0, len(config.AllowedHeaders)+1)
	addHeader := func(name string) {
		if name == "" {
			return
		}
		if name == corsWildcard {
			resolved.headerAny = true
		}
		folded := strings.ToLower(name)
		if resolved.headers[folded] {
			return
		}
		resolved.headers[folded] = true
		headers = append(headers, name)
	}
	for _, header := range config.AllowedHeaders {
		addHeader(header)
	}
	if config.AllowCredentials {
		addHeader(csrfHeader)
	}
	resolved.headerValue = strings.Join(headers, ", ")
	resolved.exposeValue = strings.Join(config.ExposedHeaders, ", ")
	if config.MaxAge > 0 {
		resolved.maxAgeValue = strconv.FormatInt(int64(config.MaxAge/time.Second), 10)
	}
	return resolved, nil
}

// The reasons a request was not marked. They name which half did not match, so
// the record says what to fix; the response never says, because the browser was
// going to answer the question anyway and a precise refusal only helps a caller
// enumerate the policy.
const (
	CORSDeclinedOrigin = "origin"
	CORSDeclinedMethod = "method"
	CORSDeclinedHeader = "header"
)

// CORSDecision is what one request produced.
//
// It is a value rather than a set of calls into a response, because the two
// transports write headers differently and decide identically. Everything below
// is what to write; nothing here writes it.
type CORSDecision struct {
	// Preflight is true when the frame answers 204 and stops. The rest of the
	// chain never sees the request: a preflight carries no cookie, no
	// Authorization and no token, so every frame below would read it as an
	// anonymous caller asking for something it may not have.
	Preflight bool
	// Headers are set on the response before anything downstream commits one,
	// so a refusal written by any frame below carries them and its status
	// reaches the caller instead of an opaque network error.
	Headers []ResponseHeader
	// Vary is appended to the response's Vary header.
	Vary []string
	// Declined names which half did not match, empty when nothing did.
	Declined string
	// Origin is the caller's Origin, carried for the record.
	Origin string
}

// Decide answers one request.
//
// path and rawPath are the decoded and raw request paths; a path that cannot be
// matched unambiguously is left unmarked rather than refused, since deciding
// about a target whose identity depends on who resolves it is the mistake the
// canonical form exists to prevent.
func (c ResolvedCORS) Decide(path, rawPath, method, origin, requestMethod, requestHeaders string) CORSDecision {
	if !c.enabled {
		return CORSDecision{}
	}
	canonical, ok := pathpattern.CanonicalPathOf(path, rawPath)
	if !ok || !pathpattern.Protected(c.include, c.exclude, canonical) {
		return CORSDecision{}
	}
	decision := CORSDecision{Origin: origin}
	// A browser sends Origin on a preflight without exception, so an OPTIONS
	// carrying the request-method header and no Origin is not one, and belongs
	// to whatever else answers OPTIONS.
	decision.Preflight = method == "OPTIONS" && requestMethod != "" && origin != ""
	if c.varyOrigin {
		decision.Vary = append(decision.Vary, "Origin")
		if decision.Preflight {
			decision.Vary = append(decision.Vary,
				"Access-Control-Request-Method", "Access-Control-Request-Headers")
		}
	}
	if origin == "" {
		// Not a cross-origin request. The Vary above still stands: the decision
		// read Origin, and a shared cache keyed on nothing would hand this
		// unmarked response to a caller that would have been marked.
		return decision
	}
	allowed, ok := c.allow(origin)
	if !ok {
		decision.Declined = CORSDeclinedOrigin
		return decision
	}
	decision.Headers = append(decision.Headers, ResponseHeader{Name: "Access-Control-Allow-Origin", Value: allowed})
	if c.credentials {
		decision.Headers = append(decision.Headers, ResponseHeader{Name: "Access-Control-Allow-Credentials", Value: "true"})
	}
	if !decision.Preflight {
		if c.exposeValue != "" {
			decision.Headers = append(decision.Headers, ResponseHeader{Name: "Access-Control-Expose-Headers", Value: c.exposeValue})
		}
		return decision
	}
	// The admitted sets go out whether or not this particular preflight asked
	// for something inside them, so a developer reading the response sees what
	// was allowed rather than an empty answer to debug from.
	if c.methodValue != "" {
		decision.Headers = append(decision.Headers, ResponseHeader{Name: "Access-Control-Allow-Methods", Value: c.methodValue})
	}
	if c.headerValue != "" {
		decision.Headers = append(decision.Headers, ResponseHeader{Name: "Access-Control-Allow-Headers", Value: c.headerValue})
	}
	if c.maxAgeValue != "" {
		decision.Headers = append(decision.Headers, ResponseHeader{Name: "Access-Control-Max-Age", Value: c.maxAgeValue})
	}
	switch {
	case !c.methods[strings.ToUpper(requestMethod)]:
		decision.Declined = CORSDeclinedMethod
	case !c.admitsHeaders(requestHeaders):
		decision.Declined = CORSDeclinedHeader
	}
	return decision
}

// allow reports the value to echo for a caller's origin.
func (c ResolvedCORS) allow(origin string) (string, bool) {
	if c.wildcard {
		return corsWildcard, true
	}
	value, ok := c.origins[strings.ToLower(origin)]
	return value, ok
}

// admitsHeaders reports whether every header a preflight asked about is
// admitted. The list is comma-separated and case-insensitive.
func (c ResolvedCORS) admitsHeaders(requested string) bool {
	if c.headerAny {
		return true
	}
	for remainder := requested; remainder != ""; {
		var name string
		name, remainder, _ = strings.Cut(remainder, ",")
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if !c.headers[name] {
			return false
		}
	}
	return true
}

// corsDeclineRecordsPerSecond bounds the records a declined request writes.
//
// The origin is chosen by the caller, so a scanner walking origins produces one
// record per request. The bound is the shape the report endpoint already uses
// for the same problem: drop past it, count what was dropped, and say so on the
// next record that gets through.
const corsDeclineRecordsPerSecond = 10

type corsLogBudget struct {
	window  atomic.Int64
	count   atomic.Int64
	dropped atomic.Int64
}

func (b *corsLogBudget) admit(now int64) (bool, int64) {
	if b.window.Swap(now) != now {
		b.count.Store(0)
	}
	if b.count.Add(1) > corsDeclineRecordsPerSecond {
		b.dropped.Add(1)
		return false, 0
	}
	return true, b.dropped.Swap(0)
}

// RecordCORSDecline writes the one account of a refusal that exists.
//
// A declined request is served like any other: the status is 200 or 204, the
// access log records a request that worked, and the browser reports the failure
// to its own console and to nobody else. Unlike a CSP violation this is not a
// Reporting API type, so no report is ever delivered anywhere — the frame that
// declined is the only thing that knows, and this is it saying so.
//
// The level is info rather than debug because the shipped severity floor is
// info, and a record nobody sees is not an improvement on no record. The bound
// above is what makes that affordable.
func (c ResolvedCORS) RecordCORSDecline(ctx context.Context, decision CORSDecision, path string) {
	if decision.Declined == "" || c.budget == nil {
		return
	}
	admit, dropped := c.budget.admit(time.Now().Unix())
	if !admit {
		return
	}
	attributes := []Attribute{
		String("reason", decision.Declined),
		String("origin", decision.Origin),
		String("path", path),
	}
	if dropped > 0 {
		attributes = append(attributes, Int64("dropped", dropped))
	}
	ReadLogger(ctx).Log(ctx, LevelInfo, "cors policy declined a request", attributes...)
}
