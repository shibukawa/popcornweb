package pw

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/shibukawa/popcornweb/internal/safeurl"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
	"github.com/shibukawa/tinybind-go/htmlupdate"
)

// Update protocol names.
//
// These are the framework's contracts with the runtime it ships to the browser
// — the header namespace, the attribute prefix, the installed name — and they
// are the shared leaf's, so both transports describe one document.
const (
	// UpdateHeaderPrefix yields Pw-Render, Pw-Manifest, and Pw-Build.
	UpdateHeaderPrefix = pwruntime.UpdateHeaderPrefix
	// UpdateAttributePrefix names the boundary attributes generation writes and
	// the placeholder element the render option spells.
	UpdateAttributePrefix = pwruntime.UpdateAttributePrefix
	// UpdateGlobalName is the browser namespace api:client-update-api installs.
	UpdateGlobalName = pwruntime.UpdateGlobalName
)

// updateBuildID identifies the binary that rendered a page. It is the shared
// leaf's, because a page rendered by one transport and updated by the other is
// the same page and has to answer the same question.
var updateBuildID = pwruntime.UpdateBuildID

// UpdateBuildID is the identity a rendered page carries and every update
// request is checked against, so a page from another build is answered with a
// complete document and a redraw from one is refused.
//
// It is the effective value rather than the stamp: an unstamped binary has no
// vcs.revision, and the module's per-process identity stands in, which costs a
// complete document after a restart and never a wrong delta. Reading it is what
// a diagnostic and a test both need, since neither can derive it.
func UpdateBuildID() string {
	return updateOptions(ConfigContext[HTMLConfig](nil)).RuntimeConfig().Build
}

// updateOptions builds the transport configuration for one process.
//
// Everything the module could decide for itself is decided here instead, so a
// project reads one framework rather than one framework and its dependency.
func updateOptions(config HTMLConfig) htmlupdate.Options {
	return updateEntry(config).options
}

// updateOptionsEntry is one resolved configuration, cached because it is a
// pure function of the update section and rebuilt several times per rendered
// page otherwise.
type updateOptionsEntry struct {
	config  HTMLUpdateConfig
	options htmlupdate.Options
	// configJSON is the marshaled token-free runtime configuration, and
	// spliceable records whether appending a csrf member reproduces the
	// module's own encoding. Checked once here, so a change of field order or
	// tags upstream degrades to the per-render marshal instead of emitting a
	// wrong document.
	configJSON string
	spliceable bool
}

var updateOptionsCache atomic.Pointer[updateOptionsEntry]

func updateEntry(config HTMLConfig) *updateOptionsEntry {
	if cached := updateOptionsCache.Load(); cached != nil && cached.config == config.Update {
		return cached
	}
	entry := &updateOptionsEntry{
		config: config.Update,
		options: htmlupdate.Options{
			Key:                 []byte(config.Update.ValidatorKey),
			HeaderPrefix:        UpdateHeaderPrefix,
			DataAttributePrefix: UpdateAttributePrefix,
			GlobalName:          UpdateGlobalName,
			PathPrefix:          updatePathPrefix,
			BuildID:             updateBuildID(),
			MaxManifestBytes:    config.Update.MaxManifestBytes,
			CSRFHeaderName:      pwruntime.CSRFHeaderName,
			// The merged asset of requirement:unified-update-runtime is this
			// framework's, so the module serves none and emits no tag of its
			// own.
			CallerOwnsRuntime: true,
			OnFailure:         observeUpdateFailure,
		},
	}
	// Published for the other runtime, which resolves no configuration of its
	// own: the transport that read the file and the transport that serves the
	// request need not be the same one.
	pwruntime.PublishUpdateSettings(pwruntime.UpdateSettings{
		Enabled:             config.Update.Enabled,
		ValidatorKey:        config.Update.ValidatorKey,
		HeaderPrefix:        UpdateHeaderPrefix,
		DataAttributePrefix: UpdateAttributePrefix,
		GlobalName:          UpdateGlobalName,
		PathPrefix:          updatePathPrefix,
		BuildID:             updateBuildID(),
		MaxManifestBytes:    config.Update.MaxManifestBytes,
		CSRFHeaderName:      pwruntime.CSRFHeaderName,
		CallerOwnsRuntime:   true,
		AsyncTimeout:        config.AsyncTimeout,
		AsyncConcurrency:    config.AsyncConcurrency,
		LiveMaxResponses:    config.LiveMaxResponses,
		LiveMaxBoundaries:   config.LiveMaxBoundaries,
		LiveMaxSignalBytes:  config.LiveMaxSignalBytes,
		LiveMaxDuration:     config.LiveMaxDuration,
		LiveDurationJitter:  config.LiveDurationJitter,
		LiveIdleTimeout:     config.LiveIdleTimeout,
	})
	// Bot detection travels with them. It is not an update concern, but this is
	// where a resolved HTMLConfig arrives, and the other runtime needs the same
	// two values to answer IsBot the way this one does.
	pwruntime.PublishBotSettings(pwruntime.BotSettings{
		Enabled:    config.BotDetection,
		UserAgents: config.BotUserAgents,
	})
	if encoded, err := json.Marshal(entry.options.RuntimeConfig()); err == nil {
		entry.configJSON = string(encoded)
		probe, err := json.Marshal(entry.options.RuntimeConfigFor("pw-splice-probe"))
		entry.spliceable = err == nil && string(probe) == spliceCSRF(entry.configJSON, "pw-splice-probe")
	}
	updateOptionsCache.Store(entry)
	return entry
}

// spliceCSRF appends the csrf member to the cached token-free configuration.
func spliceCSRF(configJSON, token string) string {
	encodedToken, err := json.Marshal(token)
	if err != nil || len(configJSON) < 2 || configJSON[len(configJSON)-1] != '}' {
		return ""
	}
	var b strings.Builder
	b.Grow(len(configJSON) + len(`,"csrf":`) + len(encodedToken))
	b.WriteString(configJSON[:len(configJSON)-1])
	b.WriteString(`,"csrf":`)
	b.Write(encodedToken)
	b.WriteByte('}')
	return b.String()
}

// updateHeadNodes contributes the runtime reference and its configuration.
//
// They are contributed at the render call rather than written into the
// scaffolded document shell, because that shell is an application file the
// author owns and may rewrite. A removed script tag would disable every client
// capability with no error, no console message, and no failed request; the one
// shell edit this cannot survive is deleting the head element itself, which
// rule:route-and-template-checks reports.
//
// The configuration travels as an inert escaped meta rather than as attributes
// on the tag, because the runtime is a module script and cannot read its own.
//
// Only a document render asks for these. A delta and a live delivery are
// answers to a request the runtime issued, so the tags they would carry are
// tags the page already has, and both of the ways that went wrong were real:
// the pair cost 624 bytes on the head record of every navigation, a quarter of
// an encoded delta; and because the token is freshly masked per render, the meta
// differed every time, so the client's install step could not recognize it as
// one it already had and appended another to document.head on every click. A
// component's own head tags are unaffected — they come from the templates, and
// a delta still installs the ones the document never carried.
func updateHeadNodes(config HTMLConfig, csrfToken string) []htmlbind.HeadNode {
	if !config.Update.Enabled {
		return nil
	}
	entry := updateEntry(config)
	// The configuration is process-static and marshaled once; only the token
	// varies per render, and it is spliced in rather than paying a reflective
	// marshal of the whole struct on every page.
	var content string
	switch {
	case entry.configJSON == "":
		return nil
	case csrfToken == "":
		content = entry.configJSON
	case entry.spliceable:
		content = spliceCSRF(entry.configJSON, csrfToken)
	}
	if content == "" {
		encoded, err := json.Marshal(entry.options.RuntimeConfigFor(csrfToken))
		if err != nil {
			return nil
		}
		content = string(encoded)
	}
	return []htmlbind.HeadNode{
		htmlbind.HeadMeta(
			htmlbind.HeadAttr{Name: "name", Value: updateConfigMetaName},
			htmlbind.HeadAttr{Name: "content", Value: content},
		),
		htmlbind.HeadScript(
			htmlbind.HeadAttr{Name: "src", Value: RuntimeScriptURL()},
			htmlbind.HeadAttr{Name: "type", Value: "module"},
		),
	}
}

// updateConfigMetaName is what the bootstrap half of the merged runtime looks
// for. It is a contract between this file and updateboot.js.
const updateConfigMetaName = "pw-runtime"

// updatePathPrefix is the reserved namespace every framework endpoint lives
// under, so one routing, caching, and access rule covers the whole surface.
const updatePathPrefix = pwruntime.UpdatePathPrefix

// ErrUpdateKeyMissing reports updates enabled with nothing to key validators
// with.
var ErrUpdateKeyMissing = errors.New("popcornweb: html.update.validator_key is required when html.update.enabled is true")

// ErrUpdateKeyTooShort reports a validator key too small to key anything. A
// guessable key is equivalent to no key: anyone who can render the same page
// and guess it recomputes every digest, which is the attack the key exists to
// stop. The session keyring enforces the same 32-byte floor.
var ErrUpdateKeyTooShort = errors.New("popcornweb: html.update.validator_key must carry at least 32 bytes of key material")

// validateUpdateConfig refuses a configuration that would serve unkeyed
// validators.
//
// An unkeyed digest of low-entropy content lets anyone confirm a guess by
// comparing digests, so this fails startup rather than degrading quietly.
func validateUpdateConfig(config HTMLConfig) error {
	if !config.Update.Enabled {
		return nil
	}
	if config.Update.ValidatorKey == "" {
		return ErrUpdateKeyMissing
	}
	if pwruntime.DigestKeyMaterial(config.Update.ValidatorKey) < 32 {
		return ErrUpdateKeyTooShort
	}
	return updateOptions(config).Validate()
}

// observeUpdateFailure records a refused update request.
//
// It only observes. Since system:tinybind v0.4.7 every entry returns the refusal
// it computed rather than writing one, so the response travels back with the
// answer and this hook exists for the log line — which is otherwise lost, since
// a status alone cannot say whether a page was stale or a render failed.
//
// Since v0.5.1 it receives the context rather than the request: the module reads
// nothing transport-shaped to call it, so it stopped asking for something
// transport-shaped to call it with. That is the same move that made the update
// entries portable, applied to the hook.
//
// Version skew is the ordinary case here: a page loaded before a deploy asks for
// a component whose markup has changed, gets a 404, and reloads. It is recorded
// rather than treated as a fault.
//
// A stale build is no longer one of these. Since system:tinybind v0.3.5 a redraw
// is answered at the page's own URL, so a request from another build is not
// refused at all: the caller renders the page it was going to render, which
// costs a reload instead of a refusal followed by one.
func observeUpdateFailure(ctx context.Context, failure htmlupdate.Failure) {
	pwruntime.LogUpdateRefusal(ctx, failure)
}

// writeUpdateFailure reports a refusal this framework raised itself, in the
// shape the module's own refusals arrive in.
//
// The module never saw these, so nothing has logged them yet and the hook is
// called here rather than left to the entry that did not run.
func writeUpdateFailure(w http.ResponseWriter, r *http.Request, failure htmlupdate.Failure) {
	observeUpdateFailure(requestContext(r), failure)
	writeUpdateResponse(w, r, htmlupdate.FailureResponse(failure), "")
}

// serveUpdate answers a negotiated update request and reports whether it did.
//
// It runs after the live mode has been tested, because both travel on the same
// header and the module resolves anything it does not recognize to a complete
// document: an untested live request would be answered as a page rather than a
// delivery stream.
//
// Every branch here is a mode the client asked for. Absent, unknown, from
// another build, or on a method a delta may not use, the caller keeps the
// document path it was already on, so nothing about an ordinary request changes.
func serveUpdate(w http.ResponseWriter, r *http.Request, wrappers []HTMLWrapper, leaf HTMLFragment,
	config HTMLConfig, options []HTMLOption, async, live bool,
) bool {
	update := updateOptions(config)
	if update.Negotiate(r).Mode != htmlupdate.ModeNavigation {
		return false
	}
	// The delta is written by the module, which reports no boundary back here,
	// so this render opens the one span it can measure honestly: the whole
	// comparison and every region it decided to send.
	ctx, render := startChainRenderTrace(requestContext(r), renderModeNavigate,
		renderLayers(wrappers), async, live, false)
	defer render.end()
	// A stream commits with its first record, so everything the response has to
	// carry goes on before the render starts: the axes that keep a cache from
	// answering a document request with a delta, the framing, the mode echoed
	// back, and the marker that tells a client this route delivers live.
	applyUpdateHeaders(w, update.StreamHeaders(r, wrappers, leaf))
	w.Header().Set("Cache-Control", updateCacheControl)
	// The scope chain of the composition this navigation arrives at. It travels
	// as a header rather than as a record because the delta body is the module's
	// to write and carries no field this framework can add; a header is the half
	// of this response that is still ours.
	//
	// It is written before the render for the same reason every other header
	// here is: a stream commits with its first record, and the chain is known
	// from the composition rather than from anything the render produces.
	if scopes := encodeScopeChain(scopeCatalog(wrappers, leaf)); scopes != "" {
		w.Header().Set(ScopeChainHeader, scopes)
	}
	// The delta is negotiated for a content coding like any other body. It was
	// the one response on this wire that was not, and the asymmetry inverted the
	// comparison the whole feature rests on: the document it replaces travels
	// encoded, so an unencoded delta of a quarter the source bytes still arrived
	// several times larger than the page. A record stream is JSON carrying
	// markup, which is the most compressible thing this framework sends.
	target, finish := encodedBodyWriter(w, r)
	ctx, cancel := boundedRenderContext(ctx, config, async, false)
	defer cancel()
	// The streaming entry is the one that takes render options, and it is also
	// the one that settles an await boundary rather than dropping it: a delta
	// for a chain with a boundary has to resolve it like the document does.
	// The request is passed unchanged: the module reads it for negotiation and
	// headers, and the render context it hands to template work is the one
	// above, which already carries the span.
	if err := update.RenderStreamAsync(ctx, target, r, wrappers, leaf,
		append(renderOptions(ctx, config, false, nil), options...)...); err != nil {
		render.failed(err)
		// A delta commits with its first record, so a failure after that can
		// only travel in band; the module writes it there and returns it here
		// for the log. Before the first record nothing is committed and the
		// ordinary problem path still applies.
		if responseCommitted(w) {
			finish(true)
			LoggerContext(ctx).Log(ctx, LevelError, "update delta failed after commit", Err(err))
			return true
		}
		// Nothing reached the client, so the frame is discarded and the coding
		// header with it: the problem document replacing this body is written
		// unencoded.
		finish(false)
		WriteProblem(w, r, InternalServerError(err))
		return true
	}
	finish(true)
	return true
}

// reloadableState holds the components this deployment publishes for redraw.
//
// Nothing is registered implicitly. Being exported and single rooted is not
// enough, because registration publishes an HTTP endpoint whose parameters
// anyone can supply: a component that only formats values handed to it is safe,
// while one that loads a record by identifier must check ownership itself.
// Registration is the review point, so it is a deliberate call.

// RegisterReloadable publishes one generated component as a redraw endpoint.
//
// A repeated kind is a startup error rather than a silent overwrite: the kind
// covers a component's name, parameters, and markup but not its package, so two
// identical templates in different packages produce the same one and the wrong
// component could answer.
//
// The failure is also kept, because the ordinary caller is a generated init
// beside the component it registers. An init has nowhere to return an error to,
// and panicking there would end the process before any of the framework's
// logging exists to say which component collided, so startup reports it instead.
// A project registering by hand still gets it here and can decide for itself.
func RegisterReloadable(components ...htmlupdate.Reloadable) error {
	return pwruntime.RegisterReloadable(components...)
}

// validateReloadableRegistration reports a registration that failed before main
// ran.
//
// It is checked whatever html.update.enabled says. A collision is not a
// deployment choice but a defect in what generation produced: two components
// asked to publish one endpoint, and whichever registered second is unreachable
// on every deployment that ever turns updates on. Refusing here is the whole
// point of returning an error from a call an init cannot handle.
func validateReloadableRegistration() error {
	return pwruntime.ReloadableRegistrationFailure()
}

// reloadableRegistry is the set the redraw endpoint serves, or nil when a
// project publishes none.
func reloadableRegistry() *htmlupdate.Registry {
	return pwruntime.ReloadableRegistry()
}

// Redraw answers a redraw request for the components named here, and reports
// whether it did. A caller that gets true has had its whole response written.
//
// It belongs at the top of a handler, after that handler's own authorization:
//
//	func orders(w http.ResponseWriter, r *http.Request) {
//		if !mayView(r) { pw.WriteProblem(w, r, pw.Forbidden(nil)); return }
//		if pw.Redraw(w, r, templates.OrderRowReloadable) { return }
//		// ordinary page render
//	}
//
// The address is the page's own URL, which is the whole reason this is a call
// rather than an endpoint. Path protection is configured by path pattern, so a
// redraw on a reserved path needs a second pattern kept in step with the one
// protecting the page the component sits on — two rules that must agree with
// nothing forcing them to. Here the redraw is the same request as the page, so
// it inherits the page's protection, and placed after the handler's own checks
// it inherits those too rather than only the middleware's.
//
// Naming the components is what bounds the surface: this handler answers for
// these and nothing else, so a page cannot be asked to render a component it
// never shows. That set is readable in Go beside the check that guards it.
//
// It stays the caller's job to authorize the arguments. Every parameter but the
// instance id arrives from whoever issued the request, so a component that loads
// a record by identifier verifies ownership itself exactly as a handler does.
// ReloadablePage is implemented by a generated component's parameter struct when
// that component's markup can contain a reloadable one.
//
// Nobody writes it. Generation folds each component's call graph and emits the
// method on the components it reaches something from, which is what lets Redraw
// take the page itself rather than a list the author has to keep in step with
// the template.
type ReloadablePage interface {
	PwReloadables() []htmlupdate.Reloadable
}

// Redraw answers a redraw for what this page's markup can contain, and reports
// whether it did. A caller that gets true has had its whole response written.
//
// It takes the page component, so the set comes from the template rather than
// from a list beside it:
//
//	func orders(w http.ResponseWriter, r *http.Request) {
//		if !mayView(r) { pw.WriteProblem(w, r, pw.Forbidden(nil)); return }
//		if pw.Redraw(w, r, templates.OrdersPage) { return }
//		// the query the page needs, which a redraw has now skipped
//	}
//
// The component is named, not called, so nothing builds its parameters and the
// data behind them is never fetched. That is the point of answering here rather
// than inside the page render, where the same redraw costs the whole page's
// preparation.
//
// A page whose markup reaches no reloadable component does not satisfy
// ReloadablePage and will not compile here. That is the honest answer: there is
// nothing on that page to redraw.
//
// It belongs after the handler's own authorization. The address is the page's
// own URL, so the redraw already inherits what guards the page; placing the call
// below the checks is what extends that to the handler's own.
func Redraw[P ReloadablePage](w http.ResponseWriter, r *http.Request, page func(P) HTMLFragment) bool {
	_ = page
	config := ConfigContext[HTMLConfig](requestContext(r))
	if !config.Update.Enabled {
		return false
	}
	// The axes go on whichever way this request turns out, because the page this
	// handler is about to render otherwise leaves through a cache that would
	// answer the next redraw with it.
	varyOnUpdateHeaders(w.Header())
	options := updateOptions(config)
	// The mode is tested before the registry is touched, because this call sits
	// on the ordinary page path and runs on every request to it.
	if options.Negotiate(r).Mode != htmlupdate.ModeRedraw {
		return false
	}
	// The set is derived statically from the page type, so the registry — whose
	// registration encodes each component's head to check its bound — is built
	// once per page type rather than per redraw.
	key := reflect.TypeFor[P]()
	registry, ok := redrawRegistries.Load(key)
	if !ok {
		var declared P
		built := &htmlupdate.Registry{}
		for _, component := range declared.PwReloadables() {
			if err := built.Register(component); err != nil {
				writeRedrawRegistryFailure(w, r, err)
				return true
			}
		}
		registry, _ = redrawRegistries.LoadOrStore(key, built)
	}
	ctx, trace := startRenderTrace(requestContext(r), renderModeRedraw, Int("pw.render.layers", 1))
	defer trace.end()
	answer, _ := options.Redraw(trace.request(r), registry.(*htmlupdate.Registry),
		redrawRenderOptions(w, r, ctx, config)...)
	writeUpdateResponse(w, r, answer, redrawCacheControl)
	return true
}

// redrawRegistries caches the built registry of each page type handed to
// Redraw. The set is bounded by the program's page types, so nothing evicts.
var redrawRegistries sync.Map

// writeRedrawRegistryFailure reports a set that cannot be registered: a
// duplicate kind or an oversized head is a defect in what the handler named
// rather than anything the request did.
func writeRedrawRegistryFailure(w http.ResponseWriter, r *http.Request, err error) {
	writeUpdateFailure(w, r, htmlupdate.Failure{
		Kind:    htmlupdate.FailureRenderFailed,
		Status:  http.StatusInternalServerError,
		Message: "redraw registry",
		Err:     err,
	})
}

// RedrawComponents answers a redraw for the components named here.
//
// It is the escape hatch behind Redraw, for a handler that renders something
// other than a generated page component, or that publishes a narrower set than
// its template reaches. Prefer Redraw, which cannot fall out of step with the
// markup.
func RedrawComponents(w http.ResponseWriter, r *http.Request, components ...htmlupdate.Reloadable) bool {
	if len(components) == 0 {
		return false
	}
	config := ConfigContext[HTMLConfig](requestContext(r))
	if !config.Update.Enabled {
		return false
	}
	varyOnUpdateHeaders(w.Header())
	options := updateOptions(config)
	// The mode is tested before the registry is built, because this call sits on
	// the ordinary page path and runs on every request to it. Registration
	// encodes each component's head to check its bound, which is work no
	// document request should pay for.
	if options.Negotiate(r).Mode != htmlupdate.ModeRedraw {
		return false
	}
	registry := &htmlupdate.Registry{}
	for _, component := range components {
		if err := registry.Register(component); err != nil {
			// A duplicate kind or an oversized head is a defect in what this
			// handler named rather than anything the request did, so it is
			// reported through the failure path like any other refusal.
			writeRedrawRegistryFailure(w, r, err)
			return true
		}
	}
	// The mode is already known here, so the span opens without negotiating a
	// second time.
	ctx, trace := startRenderTrace(requestContext(r), renderModeRedraw, Int("pw.render.layers", 1))
	defer trace.end()
	answer, _ := options.Redraw(trace.request(r), registry, redrawRenderOptions(w, r, ctx, config)...)
	writeUpdateResponse(w, r, answer, redrawCacheControl)
	return true
}

// Update response policy.
//
// The wire is this framework's and so is every response on it. Since
// system:tinybind v0.4.7 the module writes no header, no status, and no body it
// was not asked for: it computes what only it can know — which request headers
// an answer depends on, what its body is, which mode was served, what it digests
// to — and hands that back. What a deployment decides arrives here.
//
// The cache policy is the whole of what that leaves. These are the values the
// module used to write, kept because they were right rather than because they
// were inherited.
const (
	// An update body restates validators for one document under ambient
	// credentials, so it is never shareable and never worth storing.
	updateCacheControl = "no-store"
	// A redraw renders per-user content, so it stays out of every shared cache.
	// It is no-cache rather than no-store because no-store would forbid the
	// conditional request its entity tag exists for: a browser that may not keep
	// the bytes can never ask whether they changed.
	redrawCacheControl = "private, no-cache"
	// A sequence derives from the template rather than from the request, which
	// is what makes it the one response here a shared cache may hold. It is
	// addressed by a digest of its own content, so a template change produces a
	// new address rather than a new body at the old one.
	sequenceCacheControl = "public, max-age=31536000, immutable"
	// updateSequenceMode is what a sequence response says it is. The client
	// checks the echo, so it is part of the wire rather than a diagnostic, and
	// this names it here so a test asserts the value rather than the agreement.
	updateSequenceMode = "sequence"
)

// varyOnUpdateHeaders names the request headers every response from a page's URL
// depends on, whichever of them this request turns out to be.
//
// It goes on before anything branches. A page, its deltas, its redraws, and its
// sequences share one URL, so a cache that stored the page under that URL alone
// would answer all four with it — and the page is the response most likely to be
// storable, since it is the only one here that carries no per-request validator.
//
// The render header is what discriminates: every update request names its mode
// there and a document names nothing, so these two axes are enough to keep the
// page separate from all of it. The narrower axes a redraw and a sequence need on
// top of these come from the module, which is what knows them.
func varyOnUpdateHeaders(header http.Header) {
	addVaryHeader(header, UpdateHeaderPrefix+"-Render")
	addVaryHeader(header, UpdateHeaderPrefix+"-Build")
}

// applyUpdateHeaders puts a header set the module computed onto a response.
//
// It is not htmlupdate.ApplyTo, which adds every field: Vary goes through this
// framework's own de-duplicating path, so an axis already named does not appear
// twice, and everything else is set, so a second Content-Type cannot appear
// beside the one a caller had already chosen.
func applyUpdateHeaders(w http.ResponseWriter, header http.Header) {
	target := w.Header()
	for name, values := range header {
		if http.CanonicalHeaderKey(name) == "Vary" {
			for _, value := range values {
				addVaryHeader(target, value)
			}
			continue
		}
		target.Del(name)
		for _, value := range values {
			target.Add(name, value)
		}
	}
}

// writeUpdateResponse sends an answer the module computed, under this
// framework's cache policy.
//
// A refusal is never stored whatever the caller asked for, because its body says
// why one request failed and nothing else may be answered with it.
//
// The conditional request is answered here for the same reason the policy is
// written here: a 304 is a cache decision, and the module stopped making them.
func writeUpdateResponse(w http.ResponseWriter, r *http.Request, answer htmlupdate.Response, cacheControl string) {
	if responseCommitted(w) {
		return
	}
	if answer.Failure != nil || cacheControl == "" {
		cacheControl = updateCacheControl
	}
	w.Header().Set("Cache-Control", cacheControl)
	// A refusal carries no axes of its own, and it answers from the page's URL
	// like everything else here, so the shared ones go on unconditionally.
	varyOnUpdateHeaders(w.Header())
	applyUpdateHeaders(w, answer.Header)
	if answer.NotModified(r) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	status := answer.Status
	if status == 0 {
		status = http.StatusOK
	}
	// Unlike the delta stream, this body is already assembled, so whether a
	// coding is worth opening is a question that can be answered rather than
	// assumed. A refusal is excluded for the reason a problem document is: it
	// says why one request failed and is too small for any coding to shrink.
	target := w
	finish := func(bool) {}
	if answer.Failure == nil && len(answer.Body) >= minEncodedBodyBytes {
		target, finish = encodedBodyWriter(w, r)
	}
	target.WriteHeader(status)
	if _, err := target.Write(answer.Body); err != nil {
		finish(false)
		ctx := requestContext(r)
		LoggerContext(ctx).Log(ctx, LevelError, "update response write failed", Err(err))
		return
	}
	finish(true)
}

// minEncodedBodyBytes is the size below which a known-length body is sent as it
// stands.
//
// Every coding has a frame, and a dictionary built from a few hundred bytes has
// nothing to say, so a small body comes out larger than it went in. The
// streaming paths cannot apply this — their length is unknown when the frame has
// to be opened — but a sequence tree, a redraw, and an action response are all
// assembled first, and the smallest of them is the one this protects.
const minEncodedBodyBytes = 512

// redrawRenderOptions is what a redrawn component is rendered with.
//
// It is the page's own set. A component that renders one way inside its page and
// another in the response replacing it is a defect with no symptom until someone
// compares them, and one holding an unsafe form does not render at all without a
// token: htmlbind.Builder.CSRFField fails the render rather than emitting an
// unprotected field.
//
// The document's head contribution is deliberately absent. The runtime tag and
// its configuration are already in the page a redraw lands in, and a component's
// own head tags travel in the response body for the client to install.
//
// A deployment with the check turned off is told so, exactly as the page is.
// This entry used to carry a token when there was one and say nothing
// otherwise, so the same component rendered on the page and failed in the
// redraw wherever a project had disabled the check.
func redrawRenderOptions(w http.ResponseWriter, r *http.Request, ctx context.Context, config HTMLConfig) []htmlbind.Option {
	return appendCSRFOption(renderOptions(ctx, config, false, nil), w, r)
}

// serveSequence answers a request for the static half of one fragment.
//
// It lives on the page's own URL for the same reason a redraw does: this
// framework mounts no path of its own, and a request that arrives here has
// already passed whatever guards the page. The difference is that a sequence
// needs none of that — it derives from the template rather than from the
// request, which is what makes it the one response on this wire that can be
// public, immutable, and served from an edge.
//
// It is tested before the redraw and before the update modes because it renders
// nothing. An address this process has never rendered is answered not-found, and
// the client asks for the assembled form instead; a sequence is an optimization
// over markup that is always available, never something a screen depends on.
func serveSequence(w http.ResponseWriter, r *http.Request, config HTMLConfig) bool {
	// No span: a sequence answers from a lookup table and reaches no template,
	// no database, and no handler, so a render span around it would report a
	// render that did not happen.
	answer, ok := updateOptions(config).Sequence(r)
	if !ok {
		return false
	}
	// The Vary is what stops this response from replacing the page. A sequence
	// is public, immutable, and a year long — right for what it is, and
	// catastrophic without it, because the response is served from the page's
	// own URL: a cache storing it under that URL alone answers every later
	// request for the page with a JSON body, and a browser that fetched one
	// sequence stops being able to load the page until its cache is cleared.
	writeUpdateResponse(w, r, answer, sequenceCacheControl)
	return true
}

// serveRegisteredRedraw answers a redraw from the process-wide published set.
//
// It is the page tree's half of the same capability. A generated route handler
// runs its Load and then calls the render entry, so the branch lands there: the
// page's authorization has already run and already returned its own error, and
// the redraw is refused exactly when the page would be. The cost is the one data
// fetch the redraw did not need, which is the price of not having a seam of its
// own inside a generated handler.
//
// Calling Redraw first is the faster path and the narrower one. It is answered
// before whatever the handler does to build its page, so a redraw pays for no
// data the page needed and none it did not, and the set it names bounds what the
// URL can be asked to render. This is the same capability either way; the
// difference is what the redraw costs and how much of the published set one URL
// exposes.
func serveRegisteredRedraw(w http.ResponseWriter, r *http.Request, config HTMLConfig) bool {
	registry := reloadableRegistry()
	if registry == nil {
		return false
	}
	ctx := requestContext(r)
	varyOnUpdateHeaders(w.Header())
	options := updateOptions(config)
	// The mode is decided here so no span is left open around a call that turned
	// out to be a document request.
	if options.Negotiate(r).Mode != htmlupdate.ModeRedraw {
		return false
	}
	// The component is rendered with the page's own options, and the ETag the
	// answer carries is the module's: it digests the body the module assembled,
	// which a caller cannot produce without rendering the component twice.
	if !renderTraced(ctx) {
		answer, _ := options.Redraw(r, registry, redrawRenderOptions(w, r, ctx, config)...)
		writeUpdateResponse(w, r, answer, redrawCacheControl)
		return true
	}
	ctx, trace := startRenderTrace(ctx, renderModeRedraw, Int("pw.render.layers", 1))
	defer trace.end()
	answer, _ := options.Redraw(trace.request(r), registry, redrawRenderOptions(w, r, ctx, config)...)
	writeUpdateResponse(w, r, answer, redrawCacheControl)
	return true
}

// WantsUpdate reports whether the caller can apply an update response.
//
// It is the one branch point of an action handler. An ordinary form submission
// and a non-browser client cannot apply one, so they take the response the
// handler already wrote — a redirect, or JSON — and a page with the runtime
// takes the regions instead. Keeping it to one predicate is what stops the two
// paths from drifting apart.
func WantsUpdate(r *http.Request) bool {
	config := ConfigContext[HTMLConfig](requestContext(r))
	if !config.Update.Enabled {
		return false
	}
	return updateOptions(config).WantsUpdate(r)
}

// UpdateRegion pairs a target element id with the fragment replacing it.
type UpdateRegion = htmlupdate.Update

// Replace names one region an action response rewrites. The rendered root
// element must carry the same id, or the region becomes unaddressable after the
// first update.
func Replace(targetID string, fragment HTMLFragment) UpdateRegion {
	return htmlupdate.Replace(targetID, fragment)
}

// WriteUpdate answers a mutating request with the regions its action changed,
// so one round trip both performs the action and refreshes the page.
//
// The status is the handler's own. The browser applies the regions whatever it
// says, because a rejected submission returns 4xx and the regions it carries
// are the validation errors — showing them is the point. That is the opposite
// of a redraw, where a non-2xx means the render failed.
func WriteUpdate(w http.ResponseWriter, r *http.Request, status int, regions ...UpdateRegion) {
	SetRoute(w, r)
	ctx := requestContext(r)
	config := ConfigContext[HTMLConfig](ctx)
	// The options are the ones every other render path gets. Until system:tinybind
	// v0.4.4 this entry took none, and a region holding a form could not render
	// at all: CSRFField refuses a render that supplied no token, so the
	// documented way to answer a rejected submission — 422 carrying the form
	// with its errors — answered 500 instead.
	//
	// The token is one of those options, and it was the one that comment was
	// about and this call still left out: the shared builder has never carried
	// it, because the document chain needs the same token for its head and
	// supplies it itself. So the 422 kept answering 500.
	answer, err := updateOptions(config).WriteUpdateStatus(r, status, regions,
		appendCSRFOption(renderOptions(ctx, config, false, nil), w, r)...)
	if err != nil {
		// Nothing is written until every region rendered, so a failure here can
		// still choose its own status.
		WriteProblem(w, r, InternalServerError(err))
		return
	}
	// An action response carries what one request changed, under ambient
	// credentials, so it is never cacheable and never shared.
	writeUpdateResponse(w, r, answer, updateCacheControl)
}

// WriteUpdateNavigate tells the browser to leave the page, which is how an
// action that changed where the user belongs stays correct without guessing
// which regions to rewrite.
//
// The target is refused unless it is one a browser can follow without running
// script. The value reaching here is commonly a return path taken from the
// request — the shape of a post-login redirect — and the browser runtime hands
// it to location.assign, which executes a javascript: URL rather than
// navigating to it. Refusing here means an application cannot turn its own
// redirect into script execution by forwarding a parameter it did not check.
func WriteUpdateNavigate(w http.ResponseWriter, r *http.Request, url string) {
	if !safeurl.Navigable(url) {
		// The URL itself stays out of the error: it is request-derived, and a
		// 5xx body is sanitized anyway, so repeating it would only risk placing
		// it somewhere that is not.
		WriteProblem(w, r, InternalServerError(errUnsafeNavigation))
		return
	}
	config := ConfigContext[HTMLConfig](requestContext(r))
	answer, err := updateOptions(config).WriteNavigate(url)
	if err != nil {
		WriteProblem(w, r, InternalServerError(err))
		return
	}
	writeUpdateResponse(w, r, answer, updateCacheControl)
}

// errUnsafeNavigation reports a navigation target this framework will not hand
// to a browser. It is a programming error rather than a request error: the
// handler chose the target, so the fix is in the handler.
var errUnsafeNavigation = errors.New("popcornweb: navigation target is not a URL a browser can follow without running script")
