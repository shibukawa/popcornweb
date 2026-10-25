package pwruntime

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shibukawa/popcornweb/contrib/otel"
	"github.com/shibukawa/popcornweb/contrib/otel/metric"
	"github.com/shibukawa/popcornweb/contrib/otel/trace"
	"github.com/shibukawa/tinybind-go/sqlbind"
)

// queryMessage is the stable message of a query diagnostics record.
const queryMessage = "sql executed"

// QueryDiagnostics is the resolved query diagnostics setting installed by pw.
//
// A nil value disables the feature entirely, so SQLExecutor returns the bare
// database handle and no statement is timed.
type QueryDiagnostics struct {
	// Level is the severity of an ordinary statement record.
	Level Level
	// SlowLevel is the severity once a statement exceeds SlowThreshold.
	SlowLevel Level
	// SlowThreshold is the duration above which a statement is slow. Zero
	// disables slow detection, and with it EXPLAIN and reproduction.
	SlowThreshold time.Duration
	// BindValues allows argument values into the record. It is the only path by
	// which application row data reaches a framework SQL log.
	BindValues bool
	// Explain captures a plan-only EXPLAIN for a slow statement.
	Explain bool
	// Reproduction renders a paste-able rerun snippet for a slow statement.
	Reproduction bool
	// MaxSQLLength bounds the logged statement text.
	MaxSQLLength int
	// MaxValueLength bounds each logged argument value.
	MaxValueLength int
}

// unwrapper is implemented by an executor that decorates another one.
type unwrapper interface {
	Unwrap() sqlbind.SQLExecutor
}

// unwrapExecutor returns the executor underneath any instrumentation, so a
// caller that needs the concrete transaction handle can reach past it.
func unwrapExecutor(executor sqlbind.SQLExecutor) sqlbind.SQLExecutor {
	for {
		wrapper, ok := executor.(unwrapper)
		if !ok {
			return executor
		}
		inner := wrapper.Unwrap()
		if inner == nil {
			return executor
		}
		executor = inner
	}
}

// instrumentCache remembers the wrapper of the latest executor resolution.
// Generated code resolves one executor per statement, and between two
// statements of one request every wrapper input is normally identical, so the
// cache turns a per-statement allocation into one per change of input. The
// wrapper is immutable once built, which is what makes the racing stores of
// two goroutines sharing a request harmless: either wrapper is correct.
type instrumentCache struct {
	latest atomic.Pointer[instrumentedExecutor]
}

func (cache *instrumentCache) load() *instrumentedExecutor {
	if cache == nil {
		return nil
	}
	return cache.latest.Load()
}

func (cache *instrumentCache) store(wrapper *instrumentedExecutor) {
	if cache != nil {
		cache.latest.Store(wrapper)
	}
}

// sameExecutor compares two executors without panicking on an uncomparable
// implementation, which the == operator alone would.
func sameExecutor(a, b sqlbind.SQLExecutor) bool {
	kind := reflect.TypeOf(a)
	return kind == reflect.TypeOf(b) && kind.Comparable() && a == b
}

// instrument decorates executor when diagnostics or database spans are
// enabled. The wrapper is built once per executor resolution rather than once
// per statement.
//
// The two settings are independent. A deployment commonly wants the span, which
// costs a fixed handful of attributes and lands in a trace beside the request
// that issued it, without the per-statement record; a development run usually
// wants both.
func instrument(current *Resources, connection *Connection, executor sqlbind.SQLExecutor, logger Logger) sqlbind.SQLExecutor {
	config := current.Query
	tracing := current.Trace
	if tracing != nil && !tracing.Database {
		tracing = nil
	}
	// The duration histogram is a third consumer of this one seam, and it is
	// independent of the other two: a deployment that samples almost no traces
	// and writes no per-statement record still counts every statement.
	var duration *metric.Histogram
	if current.Metrics != nil {
		duration = current.Metrics.QueryDuration
	}
	if executor == nil || (config == nil && tracing == nil && duration == nil) {
		return executor
	}
	driver, label := current.DBDriver, ""
	// The caller passes the connection when resolving the executor already
	// resolved one, so the memo lock is not taken twice for the same answer.
	if connection == nil {
		if resolved, err := current.connection(); err == nil {
			connection = resolved
		}
	}
	if connection != nil {
		driver = connection.Driver
		// One connection needs no label: it would repeat on every record and
		// name the only database there is.
		if current.Connections.Count() > 1 {
			label = connection.Label
		}
	}
	scope := current.TxScope
	if cached := current.instrumented.load(); cached != nil &&
		cached.config == config && cached.tracing == tracing &&
		cached.duration == duration &&
		cached.driver == driver && cached.connection == label &&
		cached.scope == scope &&
		sameExecutor(cached.inner, executor) && cached.logger.equivalent(logger) {
		return cached
	}
	wrapper := &instrumentedExecutor{
		inner:      executor,
		config:     config,
		tracing:    tracing,
		duration:   duration,
		logger:     logger,
		driver:     driver,
		connection: label,
		scope:      scope,
	}
	current.instrumented.store(wrapper)
	return wrapper
}

// instrumentedExecutor observes one statement per call without changing what
// the wrapped executor does with it. It is immutable after instrument builds
// it; the cache above depends on that.
type instrumentedExecutor struct {
	inner sqlbind.SQLExecutor
	// config is the query log setting, nil when only the span is wanted.
	config *QueryDiagnostics
	// tracing is the span setting, nil when only the record is wanted.
	tracing *Tracing
	// duration is db.client.operation.duration, nil when metrics are off. It is
	// recorded whatever the other two are, and whatever the sampler decided.
	duration *metric.Histogram
	logger   Logger
	driver   string
	// connection labels which pool ran the statement. Empty when only one is
	// configured.
	connection string
	// scope answers the transaction placement of each statement at execution
	// time, so a cached wrapper never reports a stale savepoint depth.
	scope *TransactionScope
}

// Unwrap returns the observed executor.
func (executor *instrumentedExecutor) Unwrap() sqlbind.SQLExecutor { return executor.inner }

func (executor *instrumentedExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	spanCtx, span := executor.startSpan(ctx, "exec", query)
	start := time.Now()
	result, err := executor.inner.ExecContext(ctx, query, args...)
	elapsed := time.Since(start)
	affected := int64(-1)
	if err == nil && result != nil {
		if value, affectedErr := result.RowsAffected(); affectedErr == nil {
			affected = value
		}
	}
	executor.endSpan(span, elapsed, affected, err)
	executor.measure("exec", query, elapsed, err)
	executor.record(spanCtx, "exec", query, args, elapsed, affected, err)
	return result, err
}

func (executor *instrumentedExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	spanCtx, span := executor.startSpan(ctx, "query", query)
	start := time.Now()
	rows, err := executor.inner.QueryContext(ctx, query, args...)
	// The executor contract returns a concrete *sql.Rows, so there is nothing to
	// decorate: the row count and the scan time belong to the caller's loop and
	// are deliberately outside this measurement.
	elapsed := time.Since(start)
	executor.endSpan(span, elapsed, -1, err)
	executor.measure("query", query, elapsed, err)
	executor.record(spanCtx, "query", query, args, elapsed, -1, err)
	return rows, err
}

// QueryRows keeps the wrapper on the driver-agnostic dispatch path: without
// it, sqlbind.Query would fall back to QueryContext, which on a native
// executor is the UnimplementedQuerier stub. Dispatching through
// sqlbind.Query here keeps one code path for both executor kinds, and the
// span and record treatment matches QueryContext exactly.
func (executor *instrumentedExecutor) QueryRows(ctx context.Context, query string, args ...any) (sqlbind.Rows, error) {
	spanCtx, span := executor.startSpan(ctx, "query", query)
	start := time.Now()
	rows, err := sqlbind.Query(ctx, executor.inner, query, args...)
	elapsed := time.Since(start)
	executor.endSpan(span, elapsed, -1, err)
	executor.measure("query", query, elapsed, err)
	executor.record(spanCtx, "query", query, args, elapsed, -1, err)
	return rows, err
}

// startSpan opens the client span of one statement, and returns the context
// carrying it so the record below correlates with it rather than with the
// request root.
//
// The observed call still runs on the caller's context. A span in the context
// handed to a driver buys nothing — no driver here reads one — and passing the
// original keeps the call unchanged when tracing is off and on.
func (executor *instrumentedExecutor) startSpan(ctx context.Context, operation, query string) (context.Context, *trace.Span) {
	if executor.tracing == nil {
		return ctx, nil
	}
	statement := sqlOperation(query)
	attributes := make([]otel.Attribute, 0, 6)
	if executor.driver != "" {
		attributes = append(attributes, otel.String("db.system.name", executor.driver))
	}
	if statement != "" {
		attributes = append(attributes, otel.String("db.operation.name", statement))
	}
	if executor.tracing.Statement {
		text, truncated := truncateText(query, executor.tracing.MaxSQLLength)
		attributes = append(attributes, otel.String("db.query.text", text))
		if truncated {
			attributes = append(attributes, otel.Bool("pw.db.query_truncated", true))
		}
	}
	if executor.connection != "" {
		attributes = append(attributes, otel.String("pw.db.connection", executor.connection))
	}
	if inTx, depth := executor.scope.state(); inTx {
		attributes = append(attributes, otel.Int64("pw.db.tx_depth", int64(depth)))
	}
	// A span name has to stay low cardinality, so it is the statement keyword
	// rather than the statement: the text is an attribute, where a backend
	// groups on it only if asked to.
	name := statement
	if name == "" {
		name = operation
	}
	return trace.Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attributes...))
}

// measure records db.client.operation.duration for one statement.
//
// It reads the same values the span does and starts no timer of its own, so a
// metric that disagrees with the trace beside it is a bug rather than a
// difference of method. What it deliberately does not carry is the statement
// text, which is unbounded, and the transaction depth, which would split every
// series by how deeply nested the caller happened to be.
//
// The returned-row count is absent because it cannot be had here: the executor
// contract returns a concrete *sql.Rows that nothing may decorate, so the count
// belongs to the caller's loop.
func (executor *instrumentedExecutor) measure(operation, query string, elapsed time.Duration, callErr error) {
	if executor.duration == nil {
		return
	}
	attributes := make([]otel.Attribute, 0, 4)
	if executor.driver != "" {
		attributes = append(attributes, otel.String("db.system.name", executor.driver))
	}
	name := sqlOperation(query)
	if name == "" {
		name = operation
	}
	attributes = append(attributes, otel.String("db.operation.name", name))
	if executor.connection != "" {
		attributes = append(attributes, otel.String("pw.db.connection", executor.connection))
	}
	if callErr != nil {
		attributes = append(attributes, otel.String("error.type", ErrorType(callErr)))
	}
	executor.duration.Record(context.Background(), elapsed.Seconds(), attributes...)
}

// endSpan closes the statement span with its outcome.
//
// It runs before the record, so the diagnostics the record may add — an EXPLAIN
// round trip above all — stay outside the duration of the statement they
// describe.
func (executor *instrumentedExecutor) endSpan(span *trace.Span, elapsed time.Duration, affected int64, callErr error) {
	if span == nil {
		return
	}
	if affected >= 0 {
		span.SetAttributes(otel.Int64("pw.db.rows_affected", affected))
	}
	if config := executor.config; config != nil && config.SlowThreshold > 0 && elapsed >= config.SlowThreshold {
		// The plan, the values, and the rerun snippet stay on the record. A
		// trace is retained longer and read more widely than a log, and the flag
		// is what a waterfall needs anyway: it says which span to open the
		// correlated record for.
		span.SetAttributes(otel.Bool("pw.db.slow", true))
	}
	if callErr != nil {
		span.RecordError(callErr)
		span.SetStatus(trace.StatusError, "")
	}
	span.End()
}

// sqlStatementKeywords are the leading words a statement span may be named
// after. An allowlist rather than "the first token" is what bounds cardinality:
// a generated statement always starts with one of these, and anything else
// falls back to the executor operation rather than putting an unknown word into
// a span name.
var sqlStatementKeywords = map[string]string{
	"select": "SELECT", "insert": "INSERT", "update": "UPDATE", "delete": "DELETE",
	"with": "WITH", "merge": "MERGE", "replace": "REPLACE", "upsert": "UPSERT",
	"create": "CREATE", "alter": "ALTER", "drop": "DROP", "truncate": "TRUNCATE",
	"begin": "BEGIN", "commit": "COMMIT", "rollback": "ROLLBACK",
	"savepoint": "SAVEPOINT", "release": "RELEASE",
	"pragma": "PRAGMA", "explain": "EXPLAIN", "call": "CALL", "set": "SET",
	"vacuum": "VACUUM", "analyze": "ANALYZE", "show": "SHOW",
}

// sqlOperation reads the leading keyword of a statement, or returns empty for
// one that starts with anything else. Leading comments are skipped, because a
// generated statement may carry one.
func sqlOperation(query string) string {
	for {
		query = strings.TrimLeft(query, " \t\r\n")
		rest, found := strings.CutPrefix(query, "--")
		if found {
			_, query, _ = strings.Cut(rest, "\n")
			continue
		}
		rest, found = strings.CutPrefix(query, "/*")
		if !found {
			break
		}
		_, query, _ = strings.Cut(rest, "*/")
	}
	word := query
	if index := strings.IndexAny(word, " \t\r\n(;"); index >= 0 {
		word = word[:index]
	}
	return sqlKeyword(word)
}

// sqlKeyword looks word up in the keyword table with the ASCII case folded in
// place instead of through strings.ToLower, which would allocate per traced
// statement. Only a letter folds to a letter under |0x20, so the fold cannot
// turn a non-keyword into a keyword.
func sqlKeyword(word string) string {
	const longest = len("savepoint")
	if len(word) > longest {
		return ""
	}
	var folded [longest]byte
	for i := 0; i < len(word); i++ {
		folded[i] = word[i] | 0x20
	}
	return sqlStatementKeywords[string(folded[:len(word)])]
}

// record emits at most one log record for one execution. A wrapper built for
// the span alone writes none.
func (executor *instrumentedExecutor) record(ctx context.Context, operation, query string, args []any, elapsed time.Duration, affected int64, callErr error) {
	config := executor.config
	if config == nil {
		return
	}
	slow := config.SlowThreshold > 0 && elapsed >= config.SlowThreshold
	level := config.Level
	if slow {
		level = config.SlowLevel
	}
	if !executor.logger.Enabled(level) {
		return
	}
	// The logger was bound when the executor was resolved, which was before the
	// statement span existed. Rebinding correlates the record with that span
	// instead of with the request root, which is what lets a waterfall entry
	// lead to the values, the plan, and the rerun snippet the span does not
	// carry. Without a statement span this is the correlation it already had.
	logger := executor.logger.withTraceOf(ctx)

	statement, statementTruncated := truncateText(query, config.MaxSQLLength)
	// Sized for every conditional append below, because the slice escapes into
	// the record and each reallocation would escape with it.
	attrs := make([]Attribute, 0, 16)
	attrs = append(attrs,
		String("sql", statement),
		Duration("duration", elapsed),
		String("operation", operation),
	)
	if statementTruncated {
		attrs = append(attrs, Bool("sql_truncated", true))
	}
	if executor.driver != "" {
		attrs = append(attrs, String("driver", executor.driver))
	}
	if executor.connection != "" {
		attrs = append(attrs, String("connection", executor.connection))
	}
	if inTx, depth := executor.scope.state(); inTx {
		attrs = append(attrs, Int("tx_depth", depth))
	}
	if affected >= 0 {
		attrs = append(attrs, Int64("rows_affected", affected))
	}
	if callErr != nil {
		attrs = append(attrs, String("outcome", "error"))
		if config.BindValues {
			attrs = append(attrs, String("error", callErr.Error()))
		} else {
			// A driver error message embeds the offending value — PostgreSQL's
			// "Key (email)=(user@example.com) already exists" — which is the
			// request data BindValues is documented to keep out of the log. With
			// it off, log the classification only, the same the span and metrics
			// paths already use.
			attrs = append(attrs, String("error.type", ErrorType(callErr)))
		}
	} else {
		attrs = append(attrs, String("outcome", "ok"))
	}

	rendered, valuesComplete := renderArgs(args, config)
	if config.BindValues && len(args) > 0 {
		// One joined string rather than a list, because a record attribute is a
		// scalar: a consumer that wants the values separately reads the
		// reproduction snippet, which is the runnable form anyway.
		attrs = append(attrs, String("args", strings.Join(rendered, ", ")))
		if !valuesComplete {
			attrs = append(attrs, Bool("args_truncated", true))
		}
	}

	if slow {
		attrs = append(attrs, Bool("slow", true))
		if config.Explain {
			plan, planErr := executor.explain(ctx, query, args)
			switch {
			case planErr != nil && config.BindValues:
				attrs = append(attrs, String("explain_error", planErr.Error()))
			case planErr != nil:
				attrs = append(attrs, String("explain_error.type", ErrorType(planErr)))
			case plan != "":
				attrs = append(attrs, String("explain", plan))
			}
		}
		// A snippet without its arguments cannot run, and a snippet built from a
		// truncated value reproduces a different query.
		if config.Reproduction && config.BindValues && valuesComplete {
			if snippet := reproductionSnippet(executor.driver, query, args); snippet != "" {
				attrs = append(attrs, String("reproduction", snippet))
			}
		}
	}

	logger.Log(ctx, level, queryMessage, attrs...)
}

// explain captures a plan-only EXPLAIN on the observed executor, so the plan
// sees the same transaction and snapshot as the statement it describes.
func (executor *instrumentedExecutor) explain(ctx context.Context, query string, args []any) (string, error) {
	prefix, ok := explainPrefixes[executor.driver]
	if !ok {
		return "", nil
	}
	// A statement that was slow because its deadline expired cannot be explained
	// on the same context, and inventing a fresh one would run unbounded work.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// sqlbind.Query rather than QueryContext, so the plan is captured on a
	// native executor the same way the observed statement ran.
	rows, err := sqlbind.Query(ctx, executor.inner, prefix+query, args...)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	return scanPlan(rows)
}

// explainPrefixes lists drivers with a known plan-only EXPLAIN form. ANALYZE is
// never used, because it would execute the observed statement a second time.
var explainPrefixes = map[string]string{
	"sqlite":     "EXPLAIN QUERY PLAN ",
	"sqlite3":    "EXPLAIN QUERY PLAN ",
	"postgres":   "EXPLAIN (FORMAT JSON) ",
	"postgresql": "EXPLAIN (FORMAT JSON) ",
	"pgx":        "EXPLAIN (FORMAT JSON) ",
	"mysql":      "EXPLAIN FORMAT=JSON ",
}

// SupportsExplain reports whether driver has a known plan-only EXPLAIN form.
// An unsupported driver keeps the query log and loses only the plan.
func SupportsExplain(driver string) bool {
	_, ok := explainPrefixes[driver]
	return ok
}

// scanPlan renders plan rows without interpreting them, because every dialect
// shapes its own columns.
func scanPlan(rows sqlbind.Rows) (string, error) {
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	cells := make([]any, len(columns))
	holders := make([]any, len(columns))
	for i := range cells {
		holders[i] = &cells[i]
	}
	var plan strings.Builder
	for rows.Next() {
		if err := rows.Scan(holders...); err != nil {
			return "", err
		}
		if plan.Len() > 0 {
			plan.WriteByte('\n')
		}
		// A single-column plan is already prose or JSON, so naming the column
		// would only add noise. A wider one needs its column names to be read.
		first := true
		for i, cell := range cells {
			// SQLite names one of its plan columns "notused". Taking that at its
			// word is reading the dialect, not interpreting the plan.
			if columns[i] == "notused" {
				continue
			}
			if !first {
				plan.WriteByte(' ')
			}
			first = false
			if len(columns) > 1 {
				plan.WriteString(columns[i])
				plan.WriteByte('=')
			}
			plan.WriteString(planCell(cell))
		}
	}
	return plan.String(), rows.Err()
}

func planCell(cell any) string {
	switch typed := cell.(type) {
	case nil:
		return ""
	case []byte:
		return string(typed)
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

// renderArgs formats bind values for the record and reports whether every value
// survived intact, which is what decides if a rerun snippet is honest.
func renderArgs(args []any, config *QueryDiagnostics) ([]string, bool) {
	if !config.BindValues || len(args) == 0 {
		return nil, true
	}
	rendered := make([]string, len(args))
	complete := true
	for i, arg := range args {
		text, truncated := boundedDisplayValue(arg, config.MaxValueLength)
		rendered[i] = text
		if truncated {
			complete = false
		}
	}
	return rendered, complete
}

// boundedDisplayValue is truncateText over displayValue, without paying for
// the part the limit cuts off. A byte slice is the one kind whose text costs
// time in proportion to its length, so only the bytes that reach the limit are
// encoded: a megabyte argument would otherwise become two megabytes of hex to
// keep the first 256 characters of it.
func boundedDisplayValue(value any, limit int) (string, bool) {
	raw, ok := value.([]byte)
	if !ok || limit <= 0 || 2+hex.EncodedLen(len(raw)) <= limit {
		return truncateText(displayValue(value), limit)
	}
	// (limit-1)/2 bytes give at least limit-2 hex digits, so with the 0x prefix
	// they cover the limit, including the high nibble an odd limit ends on.
	head := raw[:(limit-1)/2]
	return ("0x" + hex.EncodeToString(head))[:limit], true
}

// displayValue renders one argument as a scalar. A non-scalar becomes a type
// marker rather than a dump of application state.
func displayValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "NULL"
	case string:
		return typed
	case []byte:
		return "0x" + hex.EncodeToString(typed)
	case bool:
		return strconv.FormatBool(typed)
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	// strconv rather than fmt for the numeric kinds: this runs once per bind
	// value on every recorded statement, and the text is identical.
	case int:
		return strconv.FormatInt(int64(typed), 10)
	case int8:
		return strconv.FormatInt(int64(typed), 10)
	case int16:
		return strconv.FormatInt(int64(typed), 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint8:
		return strconv.FormatUint(uint64(typed), 10)
	case uint16:
		return strconv.FormatUint(uint64(typed), 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float32:
		return strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	default:
		return fmt.Sprintf("<%T>", value)
	}
}

func truncateText(text string, limit int) (string, bool) {
	if limit <= 0 || len(text) <= limit {
		return text, false
	}
	return text[:limit], true
}
