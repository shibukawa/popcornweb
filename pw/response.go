package pw

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pwruntime"
	tinybind "github.com/shibukawa/tinybind-go"
	"github.com/shibukawa/tinybind-go/htmlbind"
	"github.com/shibukawa/tinybind-go/htmlbind/delta"
	"github.com/shibukawa/tinybind-go/htmlupdate"
)

// Problem is the application-facing RFC problem value.
//
// It is declared in pwruntime and aliased here, so the value this package
// builds is the value the other transport runtime inspects and unwraps. A
// second declaration that agreed today would be a second chance to disagree
// later, and the failure would be a silent errors.As that stops matching.
type Problem = pwruntime.Problem

// FieldError describes a single field-level validation failure.
type FieldError = pwruntime.FieldError

// RateLimit describes the compatibility quota fields and standard retry hint
// emitted by RateLimited.
type RateLimit = pwruntime.RateLimit

// Field builds a field-level validation error for Validation.
func Field(field, location, message string) FieldError {
	return pwruntime.Field(field, location, message)
}

// HTMLFragment is a generated template with its parameters already bound.
type HTMLFragment = htmlbind.Fragment

// HTMLWrapper is a generated template wrapper accepted by WriteHTMLChain.
type HTMLWrapper = htmlbind.Wrapper

// HTMLOption tunes one render. The framework supplies the options every
// response needs from HTMLConfig, so a caller passing one is extending that set
// rather than replacing it.
type HTMLOption = htmlbind.Option

// RegisterHTMLDocument installs the generated application document shell.
// It is intended for generated templates/document_pw_gen.go code.
//
// The state is pwruntime's rather than this package's, because the other
// transport runtime registers into the same place: generated registration is
// emitted per build with its import rewritten, and two registries would leave
// one build rendering pages with no document around them.
func RegisterHTMLDocument(wrapper HTMLWrapper) { pwruntime.RegisterHTMLDocument(wrapper) }

func registeredHTMLDocument() []HTMLWrapper { return pwruntime.RegisteredHTMLDocument() }

// The constructors are pwruntime's, re-exported so an application keeps
// naming them through pw and a rewritten call finds the same names on the
// other runtime.
func BadRequest(values ...any) Problem      { return pwruntime.BadRequest(values...) }
func Unauthorized(values ...any) Problem    { return pwruntime.Unauthorized(values...) }
func Forbidden(values ...any) Problem       { return pwruntime.Forbidden(values...) }
func NotFound(values ...any) Problem        { return pwruntime.NotFound(values...) }
func Conflict(values ...any) Problem        { return pwruntime.Conflict(values...) }
func PayloadTooLarge(values ...any) Problem { return pwruntime.PayloadTooLarge(values...) }
func TooManyRequests(values ...any) Problem { return pwruntime.TooManyRequests(values...) }
func RateLimited(rate RateLimit, values ...any) Problem {
	return pwruntime.RateLimited(rate, values...)
}
func ServiceUnavailable(values ...any) Problem  { return pwruntime.ServiceUnavailable(values...) }
func InternalServerError(values ...any) Problem { return pwruntime.InternalServerError(values...) }

// Validation reports a 400 response carrying every detected field failure.
func Validation(fields ...FieldError) Problem { return pwruntime.Validation(fields...) }

func WriteProblem(w http.ResponseWriter, r *http.Request, err error) {
	SetRoute(w, r)
	// A redirect returned rather than written arrives here, because the one
	// path a render's error takes is this one. Redirect applies the safety
	// check and the update branch, so a returned redirect and a written one
	// cannot differ.
	var redirect pwruntime.RedirectError
	if errors.As(err, &redirect) && !responseCommitted(w) {
		Redirect(w, r, redirect.Location, redirect.Status)
		return
	}
	if responseCommitted(w) {
		LoggerContext(requestContext(r)).Log(requestContext(r), LevelError, "problem after response commit", Err(err))
		return
	}
	p := mapProblem(err)
	if p.Status >= 500 {
		LoggerContext(requestContext(r)).Log(requestContext(r), LevelError, "request failed", Err(err))
	}
	p = sanitizedProblem(p)
	if err := pwruntime.ApplyProblemHeaders(w.Header(), p); err != nil {
		LoggerContext(requestContext(r)).Log(requestContext(r), LevelError, "invalid rate limit response metadata", Err(err))
	}
	// One handler answers a browser form post and an API client on the same
	// route. Which representation this failure takes is the client's to say, so
	// it is read from Accept rather than branched on by the caller.
	if acceptsHTML(r) && registeredHTMLErrorPage() != nil {
		writeHTMLProblem(w, r, registeredHTMLDocument(), p)
		return
	}
	writeProblemJSON(w, r, p)
}

// sanitizedProblem drops what a 5xx must never carry out of the process.
//
// It sits here rather than in WriteProblem because both representations are
// written from more than one place: a boundary that failed with no recover
// clause reaches the HTML writer directly, and every path that can answer with
// a server error has to lose the cause on the way out. Applying it twice
// changes nothing, which is what makes it safe to put at each writer.
func sanitizedProblem(p Problem) Problem {
	if p.Status < 500 {
		return p
	}
	// Title is reset too, matching the net/http ErrorHandler: a hand-built 5xx
	// carrying a sensitive custom title (an internal hostname, a DSN) must not
	// survive to the client's error page any more than Message does.
	p.Title = http.StatusText(p.Status)
	if p.Title == "" {
		p.Title = "Internal Server Error"
	}
	p.Message = "internal error"
	p.Code = "internal"
	p.Fields = nil
	return p
}

// writeProblemJSON is the API branch, and the fallback for every way the HTML
// branch can decline: no renderer, no fragment, or a render that failed.
func writeProblemJSON(w http.ResponseWriter, r *http.Request, p Problem) {
	p = sanitizedProblem(p)
	addVaryHeader(w.Header(), "Accept")
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	// Built by hand like the root package's writeProblem: the document is flat
	// and known, and this path must not fail.
	var b strings.Builder
	b.WriteString(`{"type":"about:blank","title":`)
	b.WriteString(strconv.Quote(p.Title))
	b.WriteString(`,"status":`)
	b.WriteString(strconv.Itoa(p.Status))
	b.WriteString(`,"detail":`)
	b.WriteString(strconv.Quote(p.Message))
	b.WriteString(`,"code":`)
	b.WriteString(strconv.Quote(p.Code))
	if len(p.Fields) > 0 {
		b.WriteString(`,"errors":[`)
		for i, field := range p.Fields {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"field":`)
			b.WriteString(strconv.Quote(field.Field))
			b.WriteString(`,"location":`)
			b.WriteString(strconv.Quote(field.Location))
			b.WriteString(`,"message":`)
			b.WriteString(strconv.Quote(field.Message))
			b.WriteByte('}')
		}
		b.WriteByte(']')
	}
	b.WriteString("}\n")
	_, _ = io.WriteString(w, b.String())
}

func responseCommitted(w http.ResponseWriter) bool { return middlewares.Committed(w) }

// htmlBodyPool recycles the page-sized buffers of the buffered render paths.
// An outlier beyond the cap is dropped rather than kept alive for the life of
// the pool.
var htmlBodyPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

const maxPooledBodyBytes = 1 << 20

func getHTMLBody() *bytes.Buffer { return htmlBodyPool.Get().(*bytes.Buffer) }

func putHTMLBody(body *bytes.Buffer) {
	if body.Cap() > maxPooledBodyBytes {
		return
	}
	body.Reset()
	htmlBodyPool.Put(body)
}

// mapProblem turns any error into the problem a response describes.
//
// The mapping is the shared leaf's, because it is about errors rather than
// about a transport: two builds of one application answering one failure with
// two different statuses is exactly what one rule prevents.
func mapProblem(err error) Problem { return pwruntime.MapProblem(err) }

func WriteAPI[T any](w http.ResponseWriter, r *http.Request, value T) {
	SetRoute(w, r)
	target, finish := encodedBodyWriter(w, r)
	if err := tinybind.Write(target, r, value); err != nil {
		finish(false)
		WriteProblem(w, r, err)
		return
	}
	finish(true)
}

// encodedBodyWriter wraps w so a serialized document is encoded on its way out.
//
// The returned finish either commits the frame or discards it. A serializer
// that failed before committing has to leave the headers as if nothing had been
// negotiated, or the problem document replacing the body would be labelled with
// a coding it is not in.
//
// It serves the API writers and the update wire alike. Both hand back a body
// they did not render into a buffer this function can measure — a serialized
// document on one side, a record stream on the other — so both need the frame
// opened before the first byte and closed by whoever knows the write finished.
//
// The problem writers themselves stay uncompressed. Their documents are a few
// hundred bytes built by hand on a path that must not fail, so an encoder there
// would add a way to fail in exchange for no bytes worth saving.
func encodedBodyWriter(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, func(bool)) {
	encoder, err := prepareResponseEncoder(w, r)
	if err != nil {
		LoggerContext(requestContext(r)).Log(requestContext(r), LevelError, "response encoder unavailable", Err(err))
	}
	if encoder == nil {
		return w, func(bool) {}
	}
	return &encodedResponseWriter{ResponseWriter: w, encoder: encoder}, func(committed bool) {
		if !committed {
			encoder.Abort()
			if !responseCommitted(w) {
				w.Header().Del("Content-Encoding")
			}
			return
		}
		if closeErr := encoder.Close(); closeErr != nil {
			LoggerContext(requestContext(r)).Log(requestContext(r), LevelError, "response encoder close failed", Err(closeErr))
		}
	}
}

// encodedResponseWriter is the ResponseWriter half of what flushingEncoder does
// for the render paths. The API writers need a ResponseWriter rather than an
// io.Writer because they set their own content type and status.
type encodedResponseWriter struct {
	http.ResponseWriter
	encoder responseEncoder
}

func (e *encodedResponseWriter) Write(p []byte) (int, error) { return e.encoder.Write(p) }

// Unwrap keeps the wrapper chain walkable, so commit detection and every other
// probe still reach the writer this one stands in front of.
func (e *encodedResponseWriter) Unwrap() http.ResponseWriter { return e.ResponseWriter }

func (e *encodedResponseWriter) Flush() {
	if err := e.encoder.Flush(); err != nil {
		return
	}
	if flusher, ok := e.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// WriteStatus is WriteAPI with the success status made explicit: 201 for a
// creation, 202 for an accepted job, 204 for no content — which writes no
// body. The status must be a literal or a named constant at the call site,
// because the generated OpenAPI document lists one response per static status
// the handler calls this with, and a status computed at runtime is one the
// scanner cannot see.
//
// A 204 writes no body, which the library entry point already implements.
//
// Unlike WriteAPI, this cannot answer a failure with a problem document. The
// status and the content type are written before the value is encoded, so
// every error it can return arrives after the response committed — a 204
// writes no body and cannot fail at all — and a problem written over it would
// leave a 2xx carrying an error document. It is logged instead.
//
// What reaches here is a type whose encoder generation never emitted, which
// means the call site never reached pw generate. That is a build mistake, and
// the log names it as one.
func WriteStatus[T any](w http.ResponseWriter, r *http.Request, status int, value T) {
	if err := tinybind.WriteStatus(w, r, status, value); err != nil {
		ctx := r.Context()
		LoggerContext(ctx).Log(ctx, LevelError, "write status failed after the response committed",
			Int("status", status), Err(err))
	}
}

// WriteHTML renders one generated HTML fragment.
func WriteHTML(w http.ResponseWriter, r *http.Request, leaf HTMLFragment) {
	WriteHTMLChain(w, r, registeredHTMLDocument(), leaf)
}

// WriteHTMLPage renders a page inside its own wrapper chain and the registered
// document shell, with the document outermost. It is intended for generated
// page tree code, which knows the ancestor layouts of a route but must not name
// the document: that one stays the framework's, exactly as it is for a handler
// calling WriteHTML.
func WriteHTMLPage(w http.ResponseWriter, r *http.Request, wrappers []HTMLWrapper, leaf HTMLFragment, options ...HTMLOption) {
	// The combined chain is allocated per call, so appending cannot reach a
	// slice another request is rendering through; the sized variant builds it
	// in one allocation rather than a copy followed by a regrowing append.
	WriteHTMLChain(w, r, pwruntime.RegisteredHTMLDocumentWith(wrappers), leaf, options...)
}

// WriteHTMLChain renders generated wrappers around one leaf without committing
// the response until TinyBind has validated and rendered the complete chain.
//
// A chain that can open an await boundary streams instead: the shell and every
// fallback commit first, and each boundary is written as it settles. Nothing
// about the handler changes, because whether a response streams is a property
// of the templates it composed rather than a decision the handler makes.
//
// A client that will not run the boundary runtime is the exception, and it
// needs no separate path: the buffered branch already blocks until every
// boundary settles, so classifying the client is enough to hand a crawler the
// finished document instead of the fallbacks it can never replace.
func WriteHTMLChain(w http.ResponseWriter, r *http.Request, wrappers []HTMLWrapper, leaf HTMLFragment, options ...HTMLOption) {
	SetRoute(w, r)
	config := ConfigContext[HTMLConfig](requestContext(r))
	// Every unsafe form in this chain carries the session's token, and the
	// document path is where that is supplied: WriteHTMLFragment renders no
	// document and shares only the option builder below, so it is untouched.
	//
	// The framework's options go first, so a caller passing its own still wins.
	//
	// The runtime's own head tags are deliberately not among them here. Every
	// render below needs the token and the boundary prefix; only the branches
	// that produce a document need a tag installing a runtime the other branches
	// are already being driven by. What that separation is worth is written at
	// updateHeadNodes.
	token := csrfRenderToken(requestContext(r))
	options = append(chainRenderOptions(config, token, csrfDisabled(requestContext(r))), options...)
	// The probes are properties of the composed chain, so they run once here
	// and every branch below reads the same two answers. async is the cheapest
	// of the three streaming gates and the only one that can rule streaming out
	// entirely; live answers a different question, whether this screen keeps
	// changing once the document is complete, and never changes which branch
	// runs, because a live block implies an await block.
	async := htmlbind.HasAwaitBlock(wrappers, leaf)
	live := htmlbind.HasLiveBlock(wrappers, leaf)
	// A page that can update answers three ways from one URL, and which one is
	// decided before anything is written. An unrecognized mode resolves to the
	// document, so a crawler, curl, and a browser without the runtime are
	// unaffected by any of this.
	//
	// The two modes that answer without rendering this chain are tested first,
	// on their own, so that the chain's declared axes below reach only the
	// responses that actually depend on them.
	if config.Update.Enabled {
		// A sequence is tested before anything that renders. It is the static
		// half of a fragment, derived from the template rather than from this
		// request, so answering it costs a map lookup and touches neither the
		// chain nor the handler's data.
		if serveSequence(w, r, config) {
			return
		}
		// A redraw is tested next because it answers with one component's
		// subtree and never touches this chain, so there is nothing about the
		// page left to decide once it has been recognized.
		if serveRegisteredRedraw(w, r, config) {
			return
		}
	}
	// Every branch from here renders this chain, so what its components declared
	// applies to whichever one answers: a document, a delta, and a live delivery
	// all depend on whatever a builtin element read to produce them.
	varyOnDeclaredAxes(w.Header(), htmlbind.MergeVary(wrappers, leaf))
	if config.Update.Enabled {
		// A delta carries its own headers, computed for the mode it turned out
		// to be and applied before the stream commits.
		if serveUpdate(w, r, wrappers, leaf, config, options, async, live) {
			return
		}
		// The document answers from the same URL as all three, so it says which
		// request headers told it apart from them. A cache that stored it under
		// the URL alone would answer any of them with a page.
		varyOnUpdateHeaders(w.Header())
	}
	if liveModeRequested(r) {
		// The handler, the layouts, and the binding that produced this chain have
		// already run, which is what makes a reconnect need no continuation: the
		// reconstruction path is the render path.
		serveLive(w, r, wrappers, leaf, config, options...)
		return
	}
	// From here every branch produces a document, which is the one response that
	// has to carry the runtime it will then be driven by.
	if nodes := updateHeadNodes(config, token); len(nodes) > 0 {
		options = append(options, htmlbind.WithHead(nodes...))
	}
	// The route's own server functions, for a component script calling one with
	// no element to read an address off. It rides its own meta rather than the
	// runtime configuration because that value is the module's struct, and
	// because this one varies per route where that one is process-static.
	if nodes := pageActionHeadNodes(r); len(nodes) > 0 {
		options = append(options, htmlbind.WithHead(nodes...))
	}
	// It is also the one response carrying no validator of its own, so its cache
	// policy is decided here rather than inherited. Every branch above wrote its
	// own on the way past: a sequence is immutable, a delta and a live stream are
	// no-store, and a redraw is private against the entity tag it carries.
	writeChainCachePolicy(w, r, wrappers, leaf)
	if live && liveEnabled(config) {
		// One URL now has a document representation and a delivery one. The
		// delivery stream is no-store, so this exists to stop a cache from
		// answering a live request with the stored document.
		addVaryHeader(w.Header(), ResponseModeHeader)
	}
	bot := false
	if async {
		bot = isBotRequest(r, config)
		// Two branches now produce two byte representations of one URL, so a
		// shared cache must not hand a streamed body to a crawler. Only an
		// await-capable chain pays for this: a page with one representation
		// keeps a response that varies on nothing.
		w.Header().Add("Vary", "User-Agent")
	}
	// Updates force the buffered branch, and the reason is a gap rather than a
	// choice: a document a delta will address has to carry its instance
	// attributes, collecting them is what writes them, and system:tinybind
	// exposes a collector for the buffered entry only. The module's own
	// streaming entry buffers the document for the same reason.
	//
	// The cost is real — a page with an await boundary loses progressive
	// delivery when a project turns updates on — and it is the honest ordering:
	// without it every delta misses its targets and falls back to an ordinary
	// navigation, so the feature is off while looking on.
	//
	// It is settled once, above the scriptless probe as well as the branch: a
	// page that is going to be buffered anyway has nothing to learn from asking
	// whether the browser runs script, and asking costs a Vary axis and a head
	// contribution on every request.
	streamable := async && config.Streaming && !bot && !config.Update.Enabled
	// A browser with scripting disabled sends an ordinary User-Agent, so the
	// classification above says browser and it keeps every fallback. Asking it
	// is only worth doing where it would otherwise be wrong, which is exactly
	// the branch below.
	scriptless := false
	if streamable && config.ScriptlessDetection {
		// A third representation of this URL, selected by the marker cookie
		// rather than by the header above.
		w.Header().Add("Vary", "Cookie")
		buffered, handled := resolveScriptless(w, r)
		switch {
		case handled:
			return
		case buffered:
			scriptless = true
		case scriptlessSafeMethod(r):
			options = append(options, htmlbind.WithHead(scriptlessProbeHead(r)))
		}
	}
	if streamable && !scriptless {
		streamHTMLChain(w, r, wrappers, leaf, config, live, options...)
		return
	}
	// The whole render is the initial build on this branch, so it opens no child
	// span: every await boundary settles in place before the first byte, and
	// there is nothing after the pass for a second span to separate.
	ctx, render := startChainRenderTrace(requestContext(r), renderModeBuffered,
		renderLayers(wrappers), async, live, bot)
	defer render.end()
	// A scriptless client waits for every boundary before any byte, which is the
	// same shape a classified bot waits in, so it takes the same longer bound
	// rather than the streaming one it will never benefit from.
	ctx, cancel := boundedRenderContext(ctx, config, async, bot || scriptless)
	defer cancel()
	body := getHTMLBody()
	defer putHTMLBody(body)
	manifest, err := renderDocumentBody(ctx, body, wrappers, leaf, config, options)
	if err != nil {
		render.failed(err)
		// Nothing is committed on this branch, so the same failure the streaming
		// branch can only patch into a 200 still carries its real status here.
		var unrecovered *htmlbind.UnrecoveredError
		if errors.As(err, &unrecovered) {
			LoggerContext(requestContext(r)).Log(requestContext(r), LevelError,
				"await boundary failed with no recover clause", Err(unrecovered.Err))
			writeHTMLProblem(w, r, wrappers, mapProblem(unrecovered.Err))
			return
		}
		WriteProblem(w, r, InternalServerError(err))
		return
	}
	// Written after the document rather than into it, for the reason the streamed
	// marker is: the validators describe boundaries the head was written long
	// before. A failed render never reaches here, so no client is seeded with a
	// manifest for a page that was replaced by an error document.
	if err := writeDocumentManifest(body, config, manifest); err != nil {
		LoggerContext(requestContext(r)).Log(requestContext(r), LevelError,
			"document manifest write failed", Err(err))
	}
	// The scoped scripts of this composition, which nothing else on this branch
	// would tell the client about. It follows the manifest for the same reason
	// the manifest follows the document.
	if err := writeDocumentScopes(body, encodeScopeChain(scopeCatalog(wrappers, leaf))); err != nil {
		LoggerContext(requestContext(r)).Log(requestContext(r), LevelError,
			"document scope catalog write failed", Err(err))
	}
	render.wrote(body.Len())
	commitHTMLBody(w, r, body)
}

// chainRenderOptions is what the framework contributes to every render of a
// page chain, whichever representation it turns out to be: the CSRF token every
// unsafe form carries, and the boundary prefix a delta addresses regions by.
//
// The head nodes are not here. They are added by the document branches of
// WriteHTMLChain, because a delta and a live stream are answers to a client that
// is already running the runtime those nodes install.
//
// It is deliberately not part of the option builder the fragment path shares.
// A fragment response renders no document, so it has no head to merge into and
// decision:fragment-head-rejection refuses one that tries.
func chainRenderOptions(config HTMLConfig, csrfToken string, csrfOff bool) []HTMLOption {
	options := make([]HTMLOption, 0, 3)
	switch {
	case csrfToken != "":
		options = append(options, htmlbind.WithCSRFToken(csrfToken))
	case csrfOff:
		// A deployment that turned the check off gets the form it asked for
		// rather than a failed render. Generation writes the hidden field
		// whatever this setting says, because the mode is not threaded into a
		// page tree's compile, so the only place the two can be reconciled is
		// here.
		//
		// This is not the case requirement:module-native-csrf refuses. What it
		// refuses is treating an absent token as none wanted, which turns a
		// forgotten option into an unprotected form nobody chose. Here the
		// choice is in the configuration and this render is reading it.
		options = append(options, htmlbind.WithoutCSRFToken())
	}
	if config.Update.Enabled {
		// One prefix names the generated attributes, the placeholder element,
		// and the boundary ids, so a document does not hold two spellings.
		options = append(options, htmlbind.WithBoundaryPrefix(UpdateAttributePrefix))
	}
	return options
}

// csrfRenderToken derives the token every unsafe form of this render carries,
// when the request has a secret to take one from.
//
// A request without one yields nothing rather than htmlbind.WithoutCSRFToken:
// that option renders an unsafe form with an empty token, which is right for a
// mail body or a golden test and wrong for a response, where it would put an
// unprotected form on screen and say nothing. Yielding nothing fails the render
// instead, which is the outcome policy:csrf-protection asks for.
// csrfDisabled reports a deployment that turned the check off.
//
// It is the setting rather than the absence of a secret: a project with the
// check on and no secret on this request is misconfigured — no session, or a
// store that failed — and rendering an unprotected form for it would hide that.
func csrfDisabled(ctx context.Context) bool {
	return !ConfigContext[SecurityConfig](ctx).CSRF.Enabled
}

func csrfRenderToken(ctx context.Context) string {
	secret, ok := pwruntime.CSRFSecret(ctx)
	if !ok {
		return ""
	}
	token, err := pwruntime.CSRFToken(secret, nil)
	if err != nil {
		return ""
	}
	return token
}

// WriteHTMLFragment renders one generated template as the whole response, with
// no document shell, no merged head, and no wrapper chain. It answers a partial
// request from an htmx-style swap library, whose target document already exists:
// composing the registered shell around the region would swap a second html,
// head, and body into the live page.
//
// The response is always buffered. The streaming framing is only meaningful
// where the browser parser consumes the response as it arrives, and here the
// swap library holds the body and inserts it, so no marker the framework wrote
// could connect a completion to its placeholder. Blocking on await boundaries
// instead settles them in place and emits no placeholder at all, so a fragment
// carries no boundary id that could duplicate one still pending in the document
// it lands in, and needs no client runtime to be complete.
//
// A fragment carrying head contributions is a programming error rather than a
// silent drop. A component's style block is scoped into the document head, so
// dropping it would swap in an unstyled region with nothing in any log; there is
// no head here to receive it, and inlining the tags would re-emit them on every
// swap with nothing owning or deduplicating them.
func WriteHTMLFragment(w http.ResponseWriter, r *http.Request, fragment HTMLFragment) {
	SetRoute(w, r)
	ctx := requestContext(r)
	config := ConfigContext[HTMLConfig](ctx)
	// Contributions fold upward at generation time, so this covers the leaf's own
	// head element and every component it calls statically.
	if head := fragment.Head(); len(head) > 0 {
		WriteProblem(w, r, InternalServerError(fmt.Errorf(
			"popcornweb: HTML fragment declares head contributions a fragment response cannot deliver: %v", head)))
		return
	}
	// Nothing classifies the client here: one branch means one representation, so
	// this response adds no axis of its own.
	//
	// What the fragment declared is a different question and travels regardless.
	// A component reading a cookie through a registered element depends on that
	// cookie whether or not the framework chose between representations, and this
	// path renders one component rather than a chain, so both accessors below ask
	// the fragment instead of merging over wrappers that are not here.
	varyOnDeclaredAxes(w.Header(), fragment.Vary())
	// A swap target is markup for the screen it lands on, so it carries the same
	// policy that screen does. There is no chain here to assert otherwise: a
	// wrapper is what can declare a whole document shared, and a fragment answers
	// with no wrapper at all, so an undeclared one is private like everything
	// else undeclared.
	if fragment.IsPrivate() {
		w.Header().Set("Cache-Control", privateCacheControl)
	}
	async := fragment.HasAwaitBlock()
	traceCtx, render := startChainRenderTrace(ctx, renderModeFragment, 1, async, false, false)
	defer render.end()
	renderCtx, cancel := boundedRenderContext(traceCtx, config, async, false)
	defer cancel()
	body := getHTMLBody()
	defer putHTMLBody(body)
	if err := htmlbind.Render(body, fragment, renderOptions(renderCtx, config, false, nil)...); err != nil {
		render.failed(err)
		// Nothing is committed yet, so every failure still carries its real
		// status. It goes out as a problem response rather than as the HTML error
		// page: an error document swapped into a region would replace that region
		// with a whole page, and a swap library already reads the status instead.
		var unrecovered *htmlbind.UnrecoveredError
		if errors.As(err, &unrecovered) {
			LoggerContext(ctx).Log(ctx, LevelError, "await boundary failed with no recover clause", Err(unrecovered.Err))
			WriteProblem(w, r, InternalServerError(unrecovered.Err))
			return
		}
		WriteProblem(w, r, InternalServerError(err))
		return
	}
	render.wrote(body.Len())
	commitHTMLBody(w, r, body)
}

// commitHTMLBody sends a fully rendered body. Content-Length is declared only
// when the bytes reach the client unencoded, because a compressing writer knows
// the length only after it has closed.
func commitHTMLBody(w http.ResponseWriter, r *http.Request, body *bytes.Buffer) {
	ctx := requestContext(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer, closeWriter, _, err := prepareHTMLResponse(w, r)
	if err != nil {
		WriteProblem(w, r, InternalServerError(err))
		return
	}
	if writer == w {
		w.Header().Set("Content-Length", strconv.Itoa(body.Len()))
	}
	if _, err := body.WriteTo(writer); err != nil {
		LoggerContext(ctx).Log(ctx, LevelError, "HTML response write failed", Err(err))
	}
	if err := closeWriter(); err != nil {
		LoggerContext(ctx).Log(ctx, LevelError, "HTML response close failed", Err(err))
	}
}

// streamHTMLChain writes the document as htmlbind produces it. The first error
// arrives before anything is written, because chain assembly and the check for
// unset async values both run before the initial pass, so that failure can
// still become a problem response.
func streamHTMLChain(w http.ResponseWriter, r *http.Request, wrappers []HTMLWrapper, leaf HTMLFragment, config HTMLConfig, live bool, options ...HTMLOption) {
	ctx, render := startChainRenderTrace(requestContext(r), renderModeStream,
		renderLayers(wrappers), true, live, false)
	defer render.end()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer, closeWriter, abortWriter, err := prepareHTMLResponse(w, r)
	if err != nil {
		render.failed(err)
		WriteProblem(w, r, InternalServerError(err))
		return
	}
	// The wrapper counts the response and turns htmlbind's own flush, the one
	// that ends the initial pass, into the end of the initial build span.
	writer = render.writer(writer)
	render.initialBuild()
	logger := LoggerContext(ctx)
	failed := false
	// A live connection follows this document and re-renders every boundary on
	// it, including the ones that settled once. Handing that connection the
	// validators of what this document committed is what keeps it from
	// re-transferring the whole page to say nothing changed.
	//
	// The key is resolved once, and only for a response something will follow.
	// A page that ends here computes no digest at all.
	var digester *pwruntime.LiveDigester
	var held []string
	if liveEnabled(config) && live {
		// One HMAC state serves every boundary of this response rather than
		// one per digest.
		digester = pwruntime.NewLiveDigester(liveDigestKey(config))
	}
	for content, err := range htmlbind.RenderChainAsync(ctx, writer, wrappers, leaf, renderOptions(ctx, config, false, options)...) {
		if err != nil {
			render.failed(err)
			// A boundary that failed with no recover clause is reported after the
			// initial pass by construction, so the document is already committed
			// and the page can only be repaired from the inside.
			var unrecovered *htmlbind.UnrecoveredError
			if errors.As(err, &unrecovered) {
				logger.Log(ctx, LevelError, "await boundary failed with no recover clause",
					String("boundary", unrecovered.BoundaryID), Err(unrecovered.Err))
				if err := writeDocumentEscalation(writer, mapProblem(unrecovered.Err)); err != nil {
					logger.Log(ctx, LevelError, "HTML error page write failed", Err(err))
				}
				htmlbind.Flush(writer)
				failed = true
				break
			}
			if !responseCommitted(w) {
				// Nothing reached the client yet, so this response can still be
				// replaced. The encoding was chosen for a document that will not
				// be written now, and the problem body is not encoded, so the
				// header has to go with it. Any partial document still sitting in
				// the encoder is dropped rather than closed, which is the whole
				// reason it is safe to answer at all.
				w.Header().Del("Content-Encoding")
				abortWriter()
				WriteProblem(w, r, InternalServerError(err))
				return
			}
			// The status is already on the wire, so this is for the operator
			// rather than for the client: every committed fallback stays.
			logger.Log(ctx, LevelError, "HTML stream failed after commit", Err(err))
			failed = true
			break
		}
		render.boundarySettled(content.BoundaryID, len(content.HTML))
		if digest := digester.Digest(content.HTML); digest != "" {
			held = append(held, content.BoundaryID+":"+digest)
		}
		if err := writeBoundaryCompletion(writer, content); err != nil {
			render.failed(err)
			logger.Log(ctx, LevelError, "HTML boundary write failed", Err(err))
			failed = true
			break
		}
		htmlbind.Flush(writer)
	}
	if err := writeStreamEnd(writer, streamEndState(config, live, failed), held, encodeScopeChain(scopeCatalog(wrappers, leaf))); err != nil {
		logger.Log(ctx, LevelError, "HTML stream end write failed", Err(err))
	}
	htmlbind.Flush(writer)
	if err := closeWriter(); err != nil {
		logger.Log(ctx, LevelError, "HTML response close failed", Err(err))
	}
}

// renderDocumentBody writes the document a client will hold, collecting the
// instance attributes when this deployment answers updates.
//
// Collecting is what makes a later delta addressable: an operation names an
// instance id, and a client finds it by the attribute on that boundary's root
// element. A document rendered without them is a page every delta misses, and
// the miss is silent — the runtime falls back to an ordinary navigation, so the
// screen is right and the feature simply never happens.
//
// A project with updates off renders exactly what it always did, because the
// attributes are the only difference and nothing would read them.
//
// The manifest it returns describes the document it just wrote, and the caller
// seeds the client with it. Collecting already computes it; discarding it was
// what made the first navigation of every page view cost a whole page.
func renderDocumentBody(ctx context.Context, body io.Writer, wrappers []HTMLWrapper, leaf HTMLFragment,
	config HTMLConfig, options []HTMLOption,
) (delta.Manifest, error) {
	rendered := renderOptions(ctx, config, false, options)
	if !config.Update.Enabled {
		return delta.Manifest{}, htmlbind.RenderChain(body, wrappers, leaf, rendered...)
	}
	// The validator tag has to be the one every delta is computed under, or the
	// digests collected here name the same regions in a different alphabet and
	// nothing ever compares equal. The module seeds it from the build identity so
	// two builds cannot produce comparable digests, and it applies that inside
	// its own entries; this path collects directly, so it applies it here.
	//
	// It is the effective identity rather than the stamp. An unstamped binary has
	// no vcs.revision and the module stands its own per-process value in, so
	// reading the stamp here would tag one side of the comparison with an empty
	// string and the other with that value.
	//
	// Nothing noticed while the manifest was being discarded. It is what a seeded
	// client compares against, so it is load-bearing now.
	rendered = append(rendered, htmlbind.WithValidatorTag(updateOptions(config).RuntimeConfig().Build))
	return delta.CollectChain(body, []byte(config.Update.ValidatorKey), wrappers, leaf, rendered...)
}

// writeDocumentManifest seeds the client with the validators of the document it
// arrived in.
//
// Without it a page load leaves the runtime holding nothing, so its first
// request carries no manifest and is answered with every region of the page.
// The validators are already computed by the collect pass that wrote the
// document; all this adds is the bytes to carry them.
//
// What that trade is worth depends on the page, and not by a little. An entry
// costs about fifty bytes and buys skipping whatever its boundary turns out to
// hold. A stable region that is pure markup is nearly free to re-send already,
// because the sequence split sends its values and not its markup, so seeding
// loses by roughly the marker's own size; a stable region carrying data — a
// folder list with counts, a header naming the signed-in user — is not, and on
// a page measured with one, seeding saved about eight times what the marker
// cost. The loss is bounded by the marker and the win is not, which is what
// makes it worth writing unconditionally.
//
// It is an inert element after the document rather than a meta in the head,
// because the head is written before the boundaries it would describe. The
// runtime reads it at construction and removes it.
//
// A manifest is only valid for the DOM it was produced against, and this one
// describes exactly the document it is written into, which is the case
// decision:manifest-state-ownership calls the safe one.
func writeDocumentManifest(w io.Writer, config HTMLConfig, manifest delta.Manifest) error {
	if len(manifest.Instances) == 0 {
		return nil
	}
	encoded := htmlupdate.EncodeManifest(manifest)
	// A manifest over the bound is ignored by the endpoint that reads it rather
	// than truncated, so writing one costs bytes in the document and buys
	// nothing at all. Seeding nothing leaves the client where it was before this
	// existed, which is the honest way to be over a limit.
	if encoded == "" || len(encoded) > maxManifestBytes(config) {
		return nil
	}
	_, err := io.WriteString(w, `<`+documentManifestElement+` value="`+htmlbind.Escape(encoded)+`"></`+documentManifestElement+`>`)
	return err
}

// maxManifestBytes is the bound the update endpoint applies to a manifest
// header, read here so the document never seeds more than a request can carry.
func maxManifestBytes(config HTMLConfig) int {
	if config.Update.MaxManifestBytes > 0 {
		return config.Update.MaxManifestBytes
	}
	return htmlupdate.DefaultMaxManifestBytes
}

// documentManifestElement is the marker's name. It carries the boundary prefix
// like every other element this framework emits, so a document holds one
// spelling rather than two.
const documentManifestElement = UpdateAttributePrefix + "-manifest"

// writeBoundaryCompletion frames one settled boundary for the browser runtime
// in pw.RuntimeScriptURL. htmlbind yields the bare fragment and the id of the
// placeholder it belongs to; the framing and the script that acts on it are one
// design and both live here.
//
// The trailing marker is what makes the swap safe, and boundaryRuntimeScript
// explains why it cannot be replaced by reacting to the template itself.
func writeBoundaryCompletion(w io.Writer, content htmlbind.Content) error {
	// The ID is build-generated identifier data today, but it is written into
	// an attribute, and every attribute this writer emits is escaped — an
	// invariant simpler to keep than to remember.
	boundaryID := htmlbind.Escape(content.BoundaryID)
	if _, err := io.WriteString(w, `<template data-tb-boundary="`+boundaryID+`">`); err != nil {
		return err
	}
	if _, err := w.Write(content.HTML); err != nil {
		return err
	}
	_, err := io.WriteString(w, `</template><tb-apply for="`+boundaryID+`"></tb-apply>`)
	return err
}

// writeStreamEnd closes a streamed document with an inert marker naming what,
// if anything, follows it.
//
// Completion cannot be inferred from the transport here. A chunked document cut
// off mid-stream is end of file to the parser, which renders what arrived and
// fires DOMContentLoaded and load with nothing surfaced to the page, and the
// response carries no Content-Length to compare against. So the last bytes say
// it explicitly, and a client that finished parsing without seeing them knows
// it holds a truncated page.
//
// The state is also what keeps a screen that will never change again from
// paying for a live request, which costs a whole page execution to answer with
// nothing.
// The manifest attribute is what the live connection this marker invites starts
// from, so the first connection of a page view costs no more than a later one.
// It is written only on the live state: on a document nothing follows, it would
// be bytes describing a conversation that is not going to happen. A failed
// state carries none either, because the boundaries it committed are fallbacks
// that a reconnect should replace rather than keep.
func writeStreamEnd(w io.Writer, state string, held []string, scopes string) error {
	marker := `<tb-stream-end state="` + state + `"`
	if version := renderVersion(); version != "" {
		marker += ` version="` + htmlbind.Escape(version) + `"`
	}
	if state == streamEndLive && len(held) > 0 {
		marker += ` manifest="` + htmlbind.Escape(strings.Join(held, ",")) + `"`
	}
	// The scope chain rides every state, unlike the manifest. A final document
	// changes no further and still has scripts to start: the whole point of a
	// lifecycle is that it runs on a page that is merely on screen.
	if scopes != "" {
		marker += ` ` + scopeChainAttribute + `="` + htmlbind.Escape(scopes) + `"`
	}
	_, err := io.WriteString(w, marker+`></tb-stream-end>`)
	return err
}

// Stream end states. They are named because writeStreamEnd branches on the live
// one, and a marker whose attribute set depended on a bare string literal
// somewhere else is how the two come apart.
const (
	streamEndLive   = "live"
	streamEndFinal  = "final"
	streamEndFailed = "failed"
)

func streamEndState(config HTMLConfig, live, failed bool) string {
	switch {
	case failed:
		// The committed fallbacks this response left behind are not going to be
		// replaced by it, and the client is told so rather than left waiting.
		return streamEndFailed
	case liveEnabled(config) && live:
		return streamEndLive
	default:
		return streamEndFinal
	}
}

// boundaryTimeout is the bound one render applies to the work behind its await
// boundaries. A bot request gets its own value because it waits for every
// boundary before a single byte leaves; the browser bound answers a different
// question, namely how long a fallback may sit on screen.
//
// Zero BotAsyncTimeout falls back to the browser bound rather than meaning
// unbounded, so a misread key cannot hold a crawler connection open for the
// whole request deadline.
func boundaryTimeout(config HTMLConfig, bot bool) time.Duration {
	if bot && config.BotAsyncTimeout > 0 {
		return config.BotAsyncTimeout
	}
	return config.AsyncTimeout
}

// boundedRenderContext applies that bound on the buffered branch, where
// htmlbind.WithAsyncTimeout does not reach: the option is read by the async
// coordinator, and the blocking path never builds one. Without this, a chain
// forced onto this branch — by configuration or by a classified bot — would
// wait on its boundaries until the request context ended, which is precisely
// the stall the timeout exists to prevent.
//
// The bound is per render here rather than per boundary. That is the more
// useful shape for this branch anyway: nothing is on the wire yet, so what
// matters is how long the client waits in total, and the work itself already
// runs concurrently because Go started it before the render began.
func boundedRenderContext(ctx context.Context, config HTMLConfig, async, bot bool) (context.Context, context.CancelFunc) {
	timeout := boundaryTimeout(config, bot)
	if !async || timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// renderOptions builds the htmlbind options for one render. The bound reaches
// the streaming branch as an option and the buffered branch as a context
// deadline, because only one of the two paths reads the option.
func renderOptions(ctx context.Context, config HTMLConfig, bot bool, extra []HTMLOption) []htmlbind.Option {
	options := []htmlbind.Option{
		htmlbind.WithContext(ctx),
		htmlbind.WithErrorReporter(func(err error) {
			LoggerContext(ctx).Log(ctx, LevelError, "await boundary failed", Err(err))
		}),
	}
	if timeout := boundaryTimeout(config, bot); timeout > 0 {
		options = append(options, htmlbind.WithAsyncTimeout(timeout))
	}
	if config.AsyncConcurrency > 0 {
		options = append(options, htmlbind.WithConcurrencyLimit(config.AsyncConcurrency))
	}
	// The store reaches every render path, the redraw included since
	// system:tinybind v0.4.6 gave that entry options to pass. A component cached
	// on the page and uncached in the response replacing it would be two renders
	// of one thing, which is the difference nobody would think to look for.
	if cache := renderCacheOption(ctx, config.Cache); cache != nil {
		options = append(options, cache)
	}
	// The scope rides with the store because it is the other half of the same
	// key. It goes on every path for the reason the store does: a component
	// cached per reader on the page and cached shared in the response replacing
	// it would serve one reader's region to another, and it is the redraw — the
	// narrow response nobody inspects — that would do it.
	if scope := renderCacheScopeOption(ctx); scope != nil {
		options = append(options, scope)
	}
	// Caller options come last so a later one wins, which is what makes them an
	// extension of the configured set rather than a competing source of truth.
	return append(options, extra...)
}

// prepareHTMLResponse negotiates a content coding and hands back the writer the
// render should use, plus the two ways that writer ends: Close commits the
// frame, Abort discards one that was never committed so a problem response can
// take its place.
//
// The identity answer is not a failure. A client naming no coding this build
// can produce receives the bytes it asked for, and everything downstream is
// written the same way either way.
func prepareHTMLResponse(w http.ResponseWriter, r *http.Request) (io.Writer, func() error, func(), error) {
	encoder, err := prepareResponseEncoder(w, r)
	if err != nil {
		return w, noCloseWriter, noAbortWriter, err
	}
	if encoder == nil {
		return w, noCloseWriter, noAbortWriter, nil
	}
	return flushingEncoder{encoder: encoder, downstream: w}, encoder.Close, encoder.Abort, nil
}

// prepareResponseEncoder sets the negotiated headers and returns the encoder to
// write through, or nil when the response is to be sent as it stands.
//
// Vary is added whether or not a body ends up encoded, because the header is a
// statement about how this URL is negotiated rather than about what one
// response happened to be: a cache holding the identity form must not answer a
// request that asked for a coding.
func prepareResponseEncoder(w http.ResponseWriter, r *http.Request) (responseEncoder, error) {
	config := ConfigContext[MiddlewareConfig](requestContext(r))
	if !config.Compression {
		return nil, nil
	}
	addVaryHeader(w.Header(), "Accept-Encoding")
	if r == nil || w.Header().Get("Content-Encoding") != "" {
		return nil, nil
	}
	var scratch [maxResponseCodings]responseCoding
	order := orderedResponseCodings(config.CompressionCodings, &scratch)
	coding, ok := negotiateResponseCoding(r.Header.Values("Accept-Encoding"), order)
	if !ok {
		return nil, nil
	}
	w.Header().Set("Content-Encoding", coding.token)
	w.Header().Del("Content-Length")
	encoder, err := coding.newEncoder(w)
	if err != nil {
		// The headers are already set for a coding that will not be produced,
		// so they have to come back off before the caller writes plain bytes.
		w.Header().Del("Content-Encoding")
		return nil, err
	}
	return encoder, nil
}

func noCloseWriter() error { return nil }

func noAbortWriter() {}

// flushingEncoder chains one flush through both layers. An encoder deliberately
// does not flush its destination, and a completion sitting in either the
// encoder or the server's buffer defeats the point of having sent it early.
//
// Flushing per boundary rather than per write is what keeps the ratio
// reasonable: each flush ends a block, and a block ended early compresses worse.
type flushingEncoder struct {
	encoder    responseEncoder
	downstream http.ResponseWriter
}

func (f flushingEncoder) Write(p []byte) (int, error) { return f.encoder.Write(p) }

func (f flushingEncoder) Flush() {
	if err := f.encoder.Flush(); err != nil {
		return
	}
	if flusher, ok := f.downstream.(http.Flusher); ok {
		flusher.Flush()
	}
}

// splitSeq yields the separator-delimited pieces of value without allocating.
// Unlike strings.SplitSeq it never yields anything for an empty value, which
// is what a header parse wants.
func splitSeq(value string, separator byte) func(func(string) bool) {
	return func(yield func(string) bool) {
		for value != "" {
			var piece string
			if index := strings.IndexByte(value, separator); index >= 0 {
				piece, value = value[:index], value[index+1:]
			} else {
				piece, value = value, ""
			}
			if !yield(piece) {
				return
			}
		}
	}
}

// privateCacheControl is what a response says when the markup it carries
// belongs to one reader.
//
// no-store rather than the no-cache a redraw uses, and what separates them is
// what each response carries. A redraw carries an entity tag, so no-cache buys
// the conditional request no-store would forbid. A document carries no
// validator at all, so there is no 304 to protect and nothing left to weigh
// against the shared machine, where no-store is what keeps a signed-in page off
// the disk after the browser is closed.
const privateCacheControl = "private, no-store"

// writeChainCachePolicy says whether a shared cache may hold this response.
//
// The answer comes from the chain rather than from the request, because the
// header is on the wire before the first body byte and a private component four
// levels down renders long after that. Asking the templates is what makes it
// available that early; asking the render would leave the answer to the
// buffered branch and make a security-relevant header depend on whether
// streaming happened to be on.
//
// Only the private answer is written. A chain declaring itself shared gets no
// header from this framework at all, because freshness is a deployment's to
// choose: a Cache-Control naming no lifetime would either invite heuristic
// caching or invent a TTL nobody asked for. Saying nothing leaves that where it
// belongs and keeps this to the one assertion it can make honestly.
//
// An undeclared chain is private, which is a framework default rather than a
// property of the annotation. A page treated as shared that is per-reader
// serves one reader's markup to another; a page treated as per-reader that is
// shared costs a cache miss. Those are not comparable, so a project wanting the
// shared answer writes it on its document shell, once.
func writeChainCachePolicy(w http.ResponseWriter, r *http.Request, wrappers []HTMLWrapper, leaf HTMLFragment) {
	if !htmlbind.IsPrivate(wrappers, leaf) {
		return
	}
	w.Header().Set("Cache-Control", privateCacheControl)
	// A chain whose outermost member asserted shared and came out private was
	// assembled here rather than generated. The refusal that catches that
	// combination walks a call graph, and a chain composed at run time never
	// appeared in one, so this is the only place it can be reported. The source
	// is the half that matters: the assertion is in the source and what shipped
	// is not, and the answer alone does not say which component to change.
	if len(wrappers) == 0 || wrappers[0].IsPrivate() {
		return
	}
	if source := htmlbind.PrivateSource(wrappers, leaf); source != "" {
		ctx := requestContext(r)
		LoggerContext(ctx).Log(ctx, LevelWarn, "chain declaring public rendered private",
			String("declared_by", source))
	}
}

// varyOnDeclaredAxes names the request properties a render depends on because
// its components said so, rather than because this framework classified
// anything.
//
// The axes are declared by whoever registered a builtin element, since only an
// implementation knows what its provider reads, and generation folds them over
// the call graph and through slot parameters. A component reading a cookie four
// levels down therefore arrives here as one entry, which is the whole point:
// the template says nothing a caller could otherwise see, so without this the
// response would be stored under a key that ignores what produced it.
//
// A chain declaring none passes a nil slice and adds no header, which is most
// of them.
func varyOnDeclaredAxes(header http.Header, axes []string) {
	for _, axis := range axes {
		addVaryHeader(header, axis)
	}
}

func addVaryHeader(header http.Header, value string) {
	for _, line := range header.Values("Vary") {
		for existing := range splitSeq(line, ',') {
			existing = strings.TrimSpace(existing)
			if existing == "*" || strings.EqualFold(existing, value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

func requestContext(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return r.Context()
}
