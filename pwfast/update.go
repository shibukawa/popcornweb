package pwfast

import (
	"context"
	"errors"
	"net/http"

	"github.com/shibukawa/popcornweb/internal/safeurl"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/fasthttpupdate"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// UpdateRegion pairs a target element id with the fragment replacing it.
type UpdateRegion = pwruntime.UpdateRegion

// Replace names one region an action response rewrites. The rendered root
// element must carry the same id, or the region becomes unaddressable after the
// first update.
func Replace(targetID string, fragment HTMLFragment) UpdateRegion {
	return fasthttpupdate.Replace(targetID, fragment)
}

// RegisterReloadable publishes generated components as redraw endpoints,
// reaching the one registry decision:shared-runtime-leaf keeps.
func RegisterReloadable(components ...pwruntime.UpdateReloadable) error {
	return pwruntime.RegisterReloadable(components...)
}

// updateOptions builds this runtime's options from the resolved configuration.
//
// The configuration is read once by whichever runtime resolved it and published
// transport-free, because a settings file is not a transport concern and this
// half has no reader of its own. Absent settings mean nothing enabled updates,
// which every entry below treats as "not an update request" rather than as an
// error: a project that never turned the feature on should see the ordinary
// response, not a failure about a feature it does not use.
func updateOptions() (fasthttpupdate.Options, bool) {
	settings, ok := pwruntime.ResolvedUpdateSettings()
	if !ok || !settings.Enabled {
		return fasthttpupdate.Options{}, false
	}
	return fasthttpupdate.Options{
		Key:                 []byte(settings.ValidatorKey),
		HeaderPrefix:        settings.HeaderPrefix,
		DataAttributePrefix: settings.DataAttributePrefix,
		GlobalName:          settings.GlobalName,
		PathPrefix:          settings.PathPrefix,
		BuildID:             settings.BuildID,
		MaxManifestBytes:    settings.MaxManifestBytes,
		CSRFHeaderName:      settings.CSRFHeaderName,
		CallerOwnsRuntime:   settings.CallerOwnsRuntime,
		OnFailure:           pwruntime.LogUpdateRefusal,
	}, true
}

// WantsUpdate reports whether the caller can apply an update response.
//
// It is the one branch point of an action handler. An ordinary form submission
// and a non-browser client cannot apply one, so they take the response the
// handler already wrote, and a page carrying the runtime takes the regions
// instead. Keeping it to one predicate is what stops the two paths from
// drifting apart.
func WantsUpdate(r *fasthttp.RequestCtx) bool {
	options, ok := updateOptions()
	if !ok {
		return false
	}
	return options.WantsUpdate(r)
}

// WriteUpdate answers a mutating request with the regions its action changed,
// so one round trip both performs the action and refreshes the page.
//
// The status is the handler's own. The browser applies the regions whatever it
// says, because a rejected submission returns 4xx and the regions it carries
// are the validation errors — showing them is the point. That is the opposite
// of a redraw, where a non-2xx means the render failed.
func WriteUpdate(r *fasthttp.RequestCtx, status int, regions ...UpdateRegion) {
	options, ok := updateOptions()
	if !ok {
		WriteProblem(r, InternalServerError(errUpdatesDisabled))
		return
	}
	// Nothing is written until every region rendered, so a failure here can
	// still choose its own status.
	response, err := options.WriteUpdateStatus(r, status, regions)
	if err != nil {
		WriteProblem(r, InternalServerError(err))
		return
	}
	writeUpdateResponse(r, response, updateCacheControl)
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
func WriteUpdateNavigate(r *fasthttp.RequestCtx, url string) {
	if !safeurl.Navigable(url) {
		// The URL itself stays out of the error: it is request-derived, and a
		// 5xx body is sanitized anyway, so repeating it would only risk placing
		// it somewhere that is not.
		WriteProblem(r, InternalServerError(errUnsafeNavigation))
		return
	}
	options, ok := updateOptions()
	if !ok {
		WriteProblem(r, InternalServerError(errUpdatesDisabled))
		return
	}
	response, err := options.WriteNavigate(url)
	if err != nil {
		WriteProblem(r, InternalServerError(err))
		return
	}
	writeUpdateResponse(r, response, updateCacheControl)
}

// RedrawComponents answers a redraw request for the components named here, and
// reports whether it did. A caller that gets true has had its whole response
// written.
//
// It belongs at the top of a handler, after that handler's own authorization: a
// redraw reaches the component through the same route, so whatever guards the
// page guards the region.
func RedrawComponents(r *fasthttp.RequestCtx, components ...pwruntime.UpdateReloadable) bool {
	options, ok := updateOptions()
	if !ok {
		return false
	}
	registry := &pwruntime.UpdateRegistry{}
	for _, component := range components {
		if err := registry.Register(component); err != nil {
			// A duplicate kind or an oversized head is a defect in what this
			// handler named rather than anything the request did, so it is
			// reported through the failure path like any other refusal.
			failure := pwruntime.UpdateFailure{
				Kind:    fasthttpupdate.FailureRenderFailed,
				Status:  http.StatusInternalServerError,
				Message: "redraw registry",
				Err:     err,
			}
			pwruntime.LogUpdateRefusal(r, failure)
			writeUpdateResponse(r, fasthttpupdate.FailureResponse(failure), "")
			return true
		}
	}
	return answerRedraw(r, options, registry)
}

// Redraw answers a redraw from the process-wide published set, which is what a
// generated page route calls after its own Load has run.
func Redraw(r *fasthttp.RequestCtx) bool {
	options, ok := updateOptions()
	if !ok {
		return false
	}
	registry := pwruntime.ReloadableRegistry()
	if registry == nil {
		return false
	}
	return answerRedraw(r, options, registry)
}

// answerRedraw sends what the module composed, when it composed anything.
//
// The bool is unchanged in meaning: false says the request was not a redraw at
// all, and the caller falls through to the page. A refusal is an answer rather
// than a false, and it has already reached the failure hook by the time it
// arrives here.
func answerRedraw(r *fasthttp.RequestCtx, options fasthttpupdate.Options, registry *pwruntime.UpdateRegistry) bool {
	response, answered := options.Redraw(r, registry)
	if !answered {
		return false
	}
	writeUpdateResponse(r, response, redrawCacheControl)
	return true
}

// writeUpdateResponse sends a composed answer under this framework's cache
// policy.
//
// The module builds a response and leaves the sending to its caller, which is
// what lets this half exist: the value carries a status, a header set, and a
// body, and none of the three names a transport. Only this function does — and
// the policy is one of the things it has to write, because the module writes
// none and a response leaving here without one is a per-reader body a shared
// cache may hold under the page's own URL.
//
// A refusal is never stored whatever the caller asked for, because its body says
// why one request failed and nothing else may be answered with it.
//
// The conditional request is answered here for the same reason the policy is
// written here: a 304 is a cache decision, and the module stopped making them.
func writeUpdateResponse(r *fasthttp.RequestCtx, response fasthttpupdate.Response, cacheControl string) {
	if response.Failure != nil || cacheControl == "" {
		cacheControl = updateCacheControl
	}
	r.Response.Header.Set("Cache-Control", cacheControl)
	// A refusal carries no axes of its own, and it answers from the page's URL
	// like everything else here, so the shared ones go on unconditionally.
	varyOnUpdateHeaders(r)
	applyHeader(r, response.Header)
	if response.NotModified(r) {
		r.SetStatusCode(fasthttp.StatusNotModified)
		return
	}
	status := response.Status
	if status == 0 {
		status = fasthttp.StatusOK
	}
	r.SetStatusCode(status)
	_, _ = r.Write(response.Body)
}

// varyOnUpdateHeaders names the request headers every response from a page's URL
// depends on, whichever of them this request turns out to be.
//
// It goes on before anything branches. A page, its deltas and its redraws share
// one URL, so a cache that stored the page under that URL alone would answer all
// three with it — and the page is the response most likely to be storable, since
// it is the only one here that carries no per-request validator.
//
// The render header is what discriminates: every update request names its mode
// there and a document names nothing, so these two axes are enough to keep the
// page separate from all of it. The narrower axes a redraw needs on top of these
// come from the module, which is what knows them. The prefix is the shared
// leaf's constant rather than the resolved settings', because it is what
// updateOptions negotiates on and the two must name one namespace.
func varyOnUpdateHeaders(r *fasthttp.RequestCtx) {
	addVaryHeader(r, pwruntime.UpdateHeaderPrefix+"-Render")
	addVaryHeader(r, pwruntime.UpdateHeaderPrefix+"-Build")
}

// applyHeader copies a composed header set onto the response.
//
// The header type is net/http's on both sides because the module composes it,
// and it is an ordinary map rather than a transport: nothing here reads a
// request or writes a body. Only the destination differs, which is the whole
// reason a second copier exists.
//
// It is not fasthttpupdate.ApplyTo, which adds every field: Vary goes through
// this framework's own de-duplicating path, so an axis already named does not
// appear twice, and everything else is replaced, so a second value cannot appear
// beside the one a caller had already chosen.
func applyHeader(r *fasthttp.RequestCtx, header http.Header) {
	for name, values := range header {
		if http.CanonicalHeaderKey(name) == "Vary" {
			for _, value := range values {
				addVaryHeader(r, value)
			}
			continue
		}
		r.Response.Header.Del(name)
		for _, value := range values {
			r.Response.Header.Add(name, value)
		}
	}
}

// errUnsafeNavigation reports a navigation target this framework will not hand
// to a browser. It is a programming error rather than a request error: the
// handler chose the target, so the fix is in the handler.
var errUnsafeNavigation = errors.New("popcornweb: navigation target is not a URL a browser can follow without running script")

// errUpdatesDisabled reports an update entry called by a project that never
// enabled the feature, which is a wiring mistake rather than a request one.
var errUpdatesDisabled = errors.New("popcornweb: partial updates are not enabled; set html.update.enabled")

// ServeUpdate answers a negotiated streamed navigation with the delta of one
// chain, and reports whether it did.
//
// The module owns the comparison and the framing; what this supplies is the
// headers a stream must carry before its first record, and the request value it
// writes into. Both come from the same entry pw calls, so the two transports
// send the same records rather than two implementations that agree.
func ServeUpdate(r *fasthttp.RequestCtx, wrappers []HTMLWrapper, leaf HTMLFragment, options ...HTMLOption) bool {
	settings, ok := pwruntime.ResolvedUpdateSettings()
	if !ok || !settings.Enabled {
		return false
	}
	update, ok := updateOptions()
	if !ok {
		return false
	}
	if update.Negotiate(r).Mode != fasthttpupdate.ModeNavigation {
		return false
	}
	// A stream commits with its first record, so everything the response has to
	// carry goes on before the render starts: the axes that keep a cache from
	// answering a document request with a delta, the framing, and the mode
	// echoed back.
	applyHeader(r, update.StreamHeaders(r, wrappers, leaf))
	r.Response.Header.Set("Cache-Control", updateCacheControl)
	ctx, cancel := boundedRenderContext(r, settings)
	defer cancel()
	render := append(settings.RenderOptions(ctx), options...)
	if err := update.RenderStreamAsync(ctx, r, wrappers, leaf, render...); err != nil {
		// A delta commits with its first record, so a failure after that can
		// only travel in band; the module writes it there and returns it here
		// for the log. Before the first record nothing is committed and the
		// ordinary problem path still applies.
		if r.Response.Header.StatusCode() != 0 && len(r.Response.Body()) > 0 {
			pwruntime.ReadLogger(ctx).Log(ctx, pwruntime.LevelError,
				"update stream failed after commit", pwruntime.Err(err))
			return true
		}
		WriteProblem(r, InternalServerError(err))
	}
	return true
}

// boundedRenderContext applies the configured boundary bound to a render.
//
// A streamed answer settles its await boundaries as it goes, and without this a
// chain whose source never answers would hold the response open until the
// request context ended, which is the stall the timeout exists to prevent.
func boundedRenderContext(r *fasthttp.RequestCtx, settings pwruntime.UpdateSettings) (context.Context, context.CancelFunc) {
	if settings.AsyncTimeout <= 0 {
		return r, func() {}
	}
	return context.WithTimeout(r, settings.AsyncTimeout)
}

// The cache policy of every update response this transport writes.
//
// The module computes what only it can know — which request headers an answer
// depends on, what its body is, which mode was served, what it digests to — and
// writes no policy at all, because what a deployment decides arrives here. These
// are pw's values, kept identical: one request answered under two policies by
// two transports is exactly the drift the shared decisions exist to prevent.
const (
	// An update body restates validators for one document under ambient
	// credentials, so it is never shareable and never worth storing. The
	// streamed navigation delta is sent under it too, and a live delivery under
	// the same value written where that response commits.
	updateCacheControl = "no-store"
	// A redraw renders per-user content, so it stays out of every shared cache.
	// It is no-cache rather than no-store because no-store would forbid the
	// conditional request its entity tag exists for: a browser that may not keep
	// the bytes can never ask whether they changed.
	redrawCacheControl = "private, no-cache"
)
