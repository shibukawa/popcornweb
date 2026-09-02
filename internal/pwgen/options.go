package pwgen

import "github.com/shibukawa/tinybind-go/generator"

const (
	pwPackage          = "github.com/shibukawa/popcornweb/pw"
	pwruntimePackage   = "github.com/shibukawa/popcornweb/pwruntime"
	pwConfigPackage    = "github.com/shibukawa/popcornweb/pwconfig"
	pwRuntimePackage   = "github.com/shibukawa/popcornweb/pwruntime"
	pwDynamoPackage    = "github.com/shibukawa/popcornweb/database/dynamo"
	pwFirestorePackage = "github.com/shibukawa/popcornweb/database/firestore"
	// pwAttributePrefix mirrors pw.UpdateAttributePrefix. It is repeated rather
	// than imported because this package is a host-side tool and pw is the
	// runtime; a test asserts the two agree.
	//
	// It is the module's own default rather than this framework's brand. The
	// original reason was that system:tinybind routetree did not thread the
	// prefix into the templates it compiled, so branding it would have given a
	// page tree the default while a registered-router template took the brand.
	// v0.5.6 closed that and both paths now pass this value, so branding is a
	// decision that can be taken rather than one the toolchain refuses — it
	// would rewrite every generated marker and every document, which is why it
	// is not taken here in passing.
	pwAttributePrefix = "tb"
)

// Options returns TinyBind generator options extended with the stable pw API.
// Options builds the generator configuration. sqlDialect names the target
// database for .pw.sql sources; it is required whenever the run discovers one,
// because a silently assumed dialect emits placeholders the engine rejects.
// The Popcorn Web source suffixes. They are the generator's discovery globs
// and the formatter's, so they live here rather than in either caller.
const (
	HTMLTemplatePattern   = "*.pw.html"
	SQLTemplatePattern    = "*.pw.sql"
	DynamoTemplatePattern = "*.pw.dynamo"
	// FirestoreTemplatePattern is the base-name glob of a Firestore query
	// declaration. It follows the .pw. convention every other declaration
	// source here uses rather than the module's own .tb. default.
	FirestoreTemplatePattern = "*.pw.firestore"
)

func Options(sqlDialect string) (generator.Options, error) {
	registry := generator.NewCallRegistry()
	patterns := []generator.CallPattern{
		generator.RequestBindCall(
			generator.Function(pwPackage, "Parse"),
			generator.GenericType("request", 0),
			generator.RequestArgument(0),
		),
		generator.ResponseWriteCall(
			generator.Function(pwPackage, "WriteAPI"),
			generator.GenericType("response", 0),
			generator.WriterArgument(0),
			generator.RequestArgument(1),
		),
		generator.ResponseWriteStatusCall(
			generator.Function(pwPackage, "WriteStatus"),
			generator.GenericType("response", 0),
			generator.Argument("status", 2),
			generator.WriterArgument(0),
			generator.RequestArgument(1),
		),
		generator.StreamCreateCall(
			generator.Function(pwPackage, "WriteStream"),
			generator.GenericType("stream", 0),
			generator.WriterArgument(0),
			generator.RequestArgument(1),
		),
		generator.ConfigBindCall(
			generator.Function(pwPackage, "RegisterConfig"),
			generator.GenericType("config", 0),
			generator.Argument("prefix", 0),
		),
	}
	// A socket entry is the one call carrying two model types at once, and the
	// two roles are opposite operations rather than one applied twice: the
	// inbound type is decoded and never encoded, the outbound one encoded and
	// never decoded. That is why it takes two patterns against one target, which
	// the registry admits for these two operations and for no other pair.
	//
	// Without them a socket compiles, opens, and fails on its first message with
	// a missing-codec error, on a connection that has already been accepted.
	for _, name := range []string{"WebSocket", "WebSocketWith"} {
		patterns = append(patterns,
			generator.SocketReceiveCall(
				generator.Function(pwPackage, name),
				generator.GenericType("socket-in", 0),
				generator.WriterArgument(0),
				generator.RequestArgument(1),
			),
			generator.SocketSendCall(
				generator.Function(pwPackage, name),
				generator.GenericType("socket-out", 1),
				generator.WriterArgument(0),
				generator.RequestArgument(1),
			),
		)
	}
	patterns = append(patterns, []generator.CallPattern{
		// The framework's own bindings register through the shared package
		// rather than through pw, because a settings file is not a transport
		// concern and the runtime that binds it need not be the one that
		// serves. An application still writes pw.RegisterConfig above.
		generator.ConfigBindCall(
			generator.Function(pwConfigPackage, "Register"),
			generator.GenericType("config", 0),
			generator.Argument("prefix", 0),
		),
		generator.ConfigSubCommandCall(
			generator.Function(pwPackage, "RegisterSubCommand"),
			generator.GenericType("config", 0),
			generator.Argument("name", 0),
			generator.Argument("help", 1),
		),
		generator.ConfigSubCommandCall(
			generator.Function(pwPackage, "SubCommand"),
			generator.GenericType("config", 0),
			generator.Argument("name", 0),
			generator.Argument("help", 1),
		),
		// The portable spelling, for the same reason pwconfig.Register is
		// registered above: a subcommand is a fact about the command line
		// rather than about a transport, so the file declaring one is compiled
		// by both builds and must not name either runtime.
		generator.ConfigSubCommandCall(
			generator.Function(pwConfigPackage, "RegisterSubCommand"),
			generator.GenericType("config", 0),
			generator.Argument("name", 0),
			generator.Argument("help", 1),
		),
		generator.ErrorResponseCall(
			generator.Function(pwPackage, "BadRequest"),
			generator.Constant("status", 400),
			generator.Constant("error_name", "BadRequest"),
		),
		generator.ErrorResponseCall(
			generator.Function(pwPackage, "Validation"),
			generator.Constant("status", 400),
			generator.Constant("error_name", "Validation"),
		),
		generator.ErrorResponseCall(
			generator.Function(pwPackage, "Unauthorized"),
			generator.Constant("status", 401),
			generator.Constant("error_name", "Unauthorized"),
		),
		generator.ErrorResponseCall(
			generator.Function(pwPackage, "Forbidden"),
			generator.Constant("status", 403),
			generator.Constant("error_name", "Forbidden"),
		),
		generator.ErrorResponseCall(
			generator.Function(pwPackage, "NotFound"),
			generator.Constant("status", 404),
			generator.Constant("error_name", "NotFound"),
		),
		generator.ErrorResponseCall(
			generator.Function(pwPackage, "Conflict"),
			generator.Constant("status", 409),
			generator.Constant("error_name", "Conflict"),
		),
		generator.ErrorResponseCall(
			generator.Function(pwPackage, "PayloadTooLarge"),
			generator.Constant("status", 413),
			generator.Constant("error_name", "PayloadTooLarge"),
		),
		generator.ErrorResponseCall(
			generator.Function(pwPackage, "InternalServerError"),
			generator.Constant("status", 500),
			generator.Constant("error_name", "InternalServerError"),
		),
	}...)
	// Every data cache entry that takes a key, so the key method is emitted for
	// the types an application actually passes. They are methods on the store
	// handle, whose receiver is the pwruntime type behind the pw and pwfast
	// aliases, so one pattern per method serves either transport.
	//
	// The key role reads the argument type rather than a type parameter: these
	// calls are generic over the result being cached, and the key is the value
	// beside it. Every one of them takes the context and then the key, so the
	// index is the same for all four.
	//
	// Registering the reads alone would leave a key type discovered by Get and
	// missed by the invalidation that has to produce the identical key, so all
	// four are named.
	for _, name := range []string{"Get", "Has", "Set", "Invalidate"} {
		patterns = append(patterns, generator.CacheKeyCall(
			generator.Method(pwruntimePackage, name, pwruntimePackage, "CacheStore"),
			generator.ArgumentType("key", 1),
		))
	}
	// Every remaining pw entry that takes the transport and names no model the
	// generator binds or encodes. Without a pattern each of these looks to the
	// rewriter exactly like an untraceable third-party call, and every handler
	// making one is refused with a remedy only this framework can supply — which
	// is the difference between a second backend an application can adopt and
	// one it cannot.
	//
	// The module's own WriteError needed this shape first, and found it by
	// refusing every handler that reported an error.
	for _, transport := range []struct {
		name    string
		writer  int
		request int
	}{
		// Response writers. The fragment they take is already bound, so there is
		// no model here for the generator to know about — only the two arguments
		// that collapse.
		{name: "WriteProblem", writer: 0, request: 1},
		{name: "WriteHTML", writer: 0, request: 1},
		{name: "WriteHTMLPage", writer: 0, request: 1},
		{name: "WriteHTMLChain", writer: 0, request: 1},
		{name: "WriteHTMLFragment", writer: 0, request: 1},
		// The route a metric is keyed by. It takes both arguments for the same
		// reason the writers above do: the pattern is read from the request and
		// recorded on the writer, because those are the two values a handler and
		// the frame that records the metric both hold.
		{name: "SetRoute", writer: 0, request: 1},
		// The update surface, whose entries answer with records rather than a
		// value of a declared type.
		{name: "WriteUpdate", writer: 0, request: 1},
		{name: "WriteUpdateNavigate", writer: 0, request: 1},
		{name: "Redraw", writer: 0, request: 1},
		{name: "RedrawComponents", writer: 0, request: 1},
		// Predicates and the specification endpoint, which read the request and
		// write nothing a type describes.
		{name: "Redirect", writer: 0, request: 1},
		{name: "RedirectSeeOther", writer: 0, request: 1},
		{name: "WantsUpdate", writer: -1, request: 0},
		{name: "QueryValue", writer: -1, request: 0},
		{name: "FormValue", writer: -1, request: 0},
		{name: "IsBot", writer: -1, request: 0},
		// The mode arrives in a header, so a page asking about it through the
		// framework is a page either transport can serve.
		{name: "WantsLive", writer: -1, request: 0},
		{name: "OpenAPIJSON", writer: 0, request: 1},
		// The two accessors a generated route decoder reads through. They take
		// the request and no writer, and they exist so a decoder never reaches
		// into the request value itself — which is the read no second transport
		// can follow.
		{name: "PathValue", writer: -1, request: 0},
		{name: "Queries", writer: -1, request: 0},
		// The two calls a generated page dispatcher makes. They are here for the
		// same reason as everything above and for one more: a project cannot
		// avoid them by writing its handlers differently, because generation is
		// what emits the call. An unregistered pattern here would refuse the
		// generated registry of every page tree declaring a form action.
		{name: "ActionSelector", writer: -1, request: 0},
		{name: "DispatchAction", writer: 0, request: 1},
		// The third branch of an action handler, beside WantsUpdate. A handler
		// asking which caller it has is a handler either transport can serve.
		{name: "WantsValue", writer: -1, request: 0},
		// The request-scoped accessors, per policy:request-scoped-accessor-shape.
		// Each is the base form of a pair whose other half takes a context and
		// needs no pattern, having no transport argument to drop. They are the
		// largest block here because a handler reads more than it writes: the
		// configuration it was started with, the pool it queries, the logger and
		// the span it reports through, who the reader is, and where a cached
		// result lives.
		{name: "Context", writer: -1, request: 0},
		{name: "Config", writer: -1, request: 0},
		{name: "Logger", writer: -1, request: 0},
		{name: "DB", writer: -1, request: 0},
		{name: "DBDriver", writer: -1, request: 0},
		{name: "SelectDB", writer: -1, request: 0},
		{name: "SelectWriteDB", writer: -1, request: 0},
		{name: "SelectSessionDB", writer: -1, request: 0},
		{name: "Transaction", writer: -1, request: 0},
		{name: "RequestAuthentication", writer: -1, request: 0},
		{name: "Authenticated", writer: -1, request: 0},
		{name: "StartSpan", writer: -1, request: 0},
		{name: "StartSpanKind", writer: -1, request: 0},
		{name: "TraceID", writer: -1, request: 0},
		{name: "SpanID", writer: -1, request: 0},
		{name: "Traced", writer: -1, request: 0},
		{name: "MemoStore", writer: -1, request: 0},
		{name: "RequestLocale", writer: -1, request: 0},
		{name: "LocalePath", writer: -1, request: 0},
		// The locale switching surface. LocaleChoices reads the request because
		// a choice names this same page in another language, and SetLocale
		// writes the cookie a reader's explicit choice is stored in. Neither
		// reaches into the request or the writer itself, which is what lets one
		// switcher template serve either transport.
		{name: "LocaleChoices", writer: -1, request: 0},
		{name: "SetLocale", writer: 0, request: -1},
	} {
		var options []generator.CallPatternOption
		if transport.request >= 0 {
			options = append(options, generator.RequestArgument(transport.request))
		}
		if transport.writer >= 0 {
			options = append(options, generator.WriterArgument(transport.writer))
		}
		patterns = append(patterns, generator.TransportCall(
			generator.Function(pwPackage, transport.name), options...))
	}
	// The page runtime's one render entry. A handler-rung page composes its own
	// chain and calls it, which is the rung the scaffold itself writes, so a
	// pattern for it is what keeps a page tree analyzable at all.
	patterns = append(patterns, generator.TransportCall(
		generator.Function(pwPagePackage, "Render"),
		generator.WriterArgument(0), generator.RequestArgument(1)))
	if err := registry.Register(patterns...); err != nil {
		return generator.Options{}, err
	}
	options, err := registry.Options(generator.DefaultOptions())
	if err != nil {
		return generator.Options{}, err
	}
	// Nothing this project generates is an input to what it reads. TinyBind
	// recognizes its own header and no other, so the Popcorn Web prefix is
	// registered here: without it a generated page registry is analyzed as if a
	// developer had written it, and its page registrations become documented API
	// routes.
	options.GeneratedHeaders = []string{GeneratedHeaderPrefix}
	// One prefix names the generated boundary attributes, the placeholder
	// element, and the boundary ids. The browser runtime is built for it, so a
	// document holding the module's default beside this one would address
	// regions the runtime never looks for.
	options.DataAttributePrefix = pwAttributePrefix
	options.HTMLTemplatePattern = HTMLTemplatePattern
	options.SQLTemplatePattern = SQLTemplatePattern
	options.DynamoTemplatePattern = DynamoTemplatePattern
	options.FirestoreTemplatePattern = FirestoreTemplatePattern
	options.SQLDialect = sqlDialect
	options.SQLContextOnlyAPI = true
	options.SQLExecutorResolver = &generator.SymbolPattern{
		PackagePath: pwRuntimePackage,
		Name:        "SQLExecutor",
	}
	// Generated NoSQL queries resolve the process handle through the framework
	// function instead of tinybind's own context key, so a request context
	// carries no tinybind-owned node and a call site pays no context.Value
	// walk, per the same seam SQLExecutorResolver uses.
	options.DynamoHandleResolver = &generator.SymbolPattern{
		PackagePath: pwDynamoPackage,
		Name:        "Handle",
	}
	options.FirestoreHandleResolver = &generator.SymbolPattern{
		PackagePath: pwFirestorePackage,
		Name:        "Handle",
	}
	return options, nil
}

// AttributePrefix is the data attribute prefix generation compiles into every
// boundary marker, so a caller that compiles templates through another entry
// point spells them the same way.
//
// It is the module default rather than this framework's brand, because a page
// tree and a registered-router template must produce one spelling in one
// document; that is settled in the catalog and this is only where it is read.
func AttributePrefix() string { return pwAttributePrefix }
