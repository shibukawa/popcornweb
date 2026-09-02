package pwfast

import (
	"context"
	"database/sql"

	"github.com/shibukawa/popcornweb/contrib/otel/trace"
	"github.com/shibukawa/popcornweb/pwdatabase"
	"github.com/shibukawa/popcornweb/pwruntime"
)

// The request-scoped accessors this transport answers, and the half of
// policy:request-scoped-accessor-shape that makes the pair portable.
//
// The other runtime spells each of these twice: a base form taking
// *http.Request and a Context form taking context.Context. Here one body serves
// both names, because this transport's request value already is a
// context.Context. A rewritten handler reaches the base name with the collapsed
// request value, code below it reaches the Context name with an ordinary
// context, and both arrive at the same place.
//
// Both names have to exist rather than one: the transform rewrites the import
// and leaves the selector alone, so whichever spelling the authored source used
// is the spelling that lands here.

// Span is one unit of traced work.
type Span = trace.Span

// SpanKind describes what relationship a span has to its parent.
type SpanKind = trace.SpanKind

// Locale identifies one locale the project declared in its i18n block.
type Locale = pwruntime.Locale

// CacheStore is one configured data cache.
type CacheStore = pwruntime.CacheStore

// Context returns the request's context.Context, which on this transport is
// the request value itself.
func Context(ctx context.Context) context.Context { return ctx }

// ConfigContext is Config for code below the handler.
func ConfigContext[T any](ctx context.Context) T { return pwruntime.ResolveConfig[T](ctx) }

// LoggerContext is Logger for code below the handler.
func LoggerContext(ctx context.Context) pwruntime.Logger { return pwruntime.ReadLogger(ctx) }

// RequestAuthenticationContext is RequestAuthentication for code below the
// handler.
func RequestAuthenticationContext(ctx context.Context) Authentication {
	return pwruntime.RequestAuthentication(ctx)
}

// AuthenticatedContext is Authenticated for code below the handler.
func AuthenticatedContext(ctx context.Context) bool {
	return pwruntime.RequestAuthentication(ctx).Authenticated
}

// DB returns the pool of the effective connection group.
func DB(ctx context.Context) (*sql.DB, bool) { return pwruntime.DB(ctx) }

// DBContext is DB for code below the handler.
func DBContext(ctx context.Context) (*sql.DB, bool) { return pwruntime.DB(ctx) }

// DBDriver reports the driver scheme of the effective framework database pool.
func DBDriver(ctx context.Context) (string, bool) { return pwruntime.DBDriver(ctx) }

// DBDriverContext is DBDriver for code below the handler.
func DBDriverContext(ctx context.Context) (string, bool) { return pwruntime.DBDriver(ctx) }

// SelectDB pins a connection group onto the context generated SQL takes.
func SelectDB(ctx context.Context, group string) context.Context {
	return pwruntime.SelectDB(ctx, group)
}

// SelectDBContext is SelectDB for code below the handler.
func SelectDBContext(ctx context.Context, group string) context.Context {
	return pwruntime.SelectDB(ctx, group)
}

// SelectWriteDB pins the connection group framework-owned writes use.
func SelectWriteDB(ctx context.Context) (context.Context, error) {
	return pwdatabase.SelectWriteDB(ctx)
}

// SelectWriteDBContext is SelectWriteDB for code below the handler.
func SelectWriteDBContext(ctx context.Context) (context.Context, error) {
	return pwdatabase.SelectWriteDB(ctx)
}

// SelectSessionDB pins the connection group holding the session table.
func SelectSessionDB(ctx context.Context) (context.Context, error) {
	return pwdatabase.SelectSessionDB(ctx)
}

// SelectSessionDBContext is SelectSessionDB for code below the handler.
func SelectSessionDBContext(ctx context.Context) (context.Context, error) {
	return pwdatabase.SelectSessionDB(ctx)
}

// Transaction runs fn inside a database transaction on the effective group.
func Transaction(ctx context.Context, fn func(context.Context) error) error {
	return pwruntime.Transaction(ctx, fn)
}

// TransactionContext is Transaction for code below the handler.
func TransactionContext(ctx context.Context, fn func(context.Context) error) error {
	return pwruntime.Transaction(ctx, fn)
}

// StartSpan opens a child of the active span and returns a context carrying it.
func StartSpan(ctx context.Context, name string, attributes ...Attribute) (context.Context, *Span) {
	return trace.Start(ctx, name, trace.WithAttributes(attributes...))
}

// StartSpanContext is StartSpan for code below the handler.
func StartSpanContext(ctx context.Context, name string, attributes ...Attribute) (context.Context, *Span) {
	return trace.Start(ctx, name, trace.WithAttributes(attributes...))
}

// StartSpanKind is StartSpan for work that is not internal.
func StartSpanKind(ctx context.Context, name string, kind SpanKind, attributes ...Attribute) (context.Context, *Span) {
	return trace.Start(ctx, name, trace.WithSpanKind(kind), trace.WithAttributes(attributes...))
}

// StartSpanKindContext is StartSpanKind for code below the handler.
func StartSpanKindContext(ctx context.Context, name string, kind SpanKind, attributes ...Attribute) (context.Context, *Span) {
	return trace.Start(ctx, name, trace.WithSpanKind(kind), trace.WithAttributes(attributes...))
}

// TraceID returns the current trace ID, or an empty string outside a trace.
func TraceID(ctx context.Context) string { return trace.SpanContextFromContext(ctx).TraceID() }

// TraceIDContext is TraceID for code below the handler.
func TraceIDContext(ctx context.Context) string { return trace.SpanContextFromContext(ctx).TraceID() }

// SpanID returns the current span ID, or an empty string outside a trace.
func SpanID(ctx context.Context) string { return trace.SpanContextFromContext(ctx).SpanID() }

// SpanIDContext is SpanID for code below the handler.
func SpanIDContext(ctx context.Context) string { return trace.SpanContextFromContext(ctx).SpanID() }

// Traced reports whether a valid span context is active.
func Traced(ctx context.Context) bool { return trace.SpanContextFromContext(ctx).IsValid() }

// TracedContext is Traced for code below the handler.
func TracedContext(ctx context.Context) bool { return trace.SpanContextFromContext(ctx).IsValid() }

// MemoStore resolves a configured data cache by name.
func MemoStore(ctx context.Context, name string) (*CacheStore, error) {
	return pwruntime.MemoStore(ctx, name)
}

// MemoStoreContext is MemoStore for code below the handler.
func MemoStoreContext(ctx context.Context, name string) (*CacheStore, error) {
	return pwruntime.MemoStore(ctx, name)
}

// RequestLocale returns the locale resolved for this request.
func RequestLocale(ctx context.Context) Locale { return pwruntime.LocaleContext(ctx) }

// LocaleContext is RequestLocale for code below the handler.
func LocaleContext(ctx context.Context) Locale { return pwruntime.LocaleContext(ctx) }

// LocalePath builds the URL of a path in a locale.
func LocalePath(ctx context.Context, locale Locale, path string) string {
	return pwruntime.LocalePath(locale, pwruntime.LocaleModeContext(ctx), path)
}

// LocalePathContext is LocalePath for code below the handler.
func LocalePathContext(ctx context.Context, locale Locale, path string) string {
	return pwruntime.LocalePath(locale, pwruntime.LocaleModeContext(ctx), path)
}

// The data cache types. The operations are methods on the store handle, reached
// through the CacheStore alias, so a rewritten handler resolving a store through
// MemoStore goes on to call them on the value it holds; nothing here needs a
// call pattern, and the key types they take are found through the pwruntime
// method patterns pwgen registers.

// CacheKey is the identity a cached result is stored under.
type CacheKey = pwruntime.CacheKey

// CacheTagger is the optional half of a key type, naming the tags whose
// invalidation drops the entry.
type CacheTagger = pwruntime.CacheTagger

// CacheStats is what one store has answered.
type CacheStats = pwruntime.CacheStats
