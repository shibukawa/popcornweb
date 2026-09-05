package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/session"
)

// A Requirement states how recently the identity of a request must have been
// proved. It is a type rather than a time.Duration because the sources of a
// window are plural and Go has no overloading: a literal in the code, a name a
// deployment tunes, or a wall-clock deadline computed per request.
//
// There is deliberately no minimum authentication strength. The framework
// cannot rank the methods it mounts: in a mode with one method an ordering has
// nothing to order, and in a mode with two neither is stronger in general, so
// any default would be a claim the framework is not entitled to make.
type Requirement interface {
	// resolve returns the window for one request. The bool reports whether the
	// requirement could be resolved at all.
	resolve(Exchange, Config) (window, bool)
}

// window is a resolved requirement: how old a proof may be, and whether a
// login counts as one.
type window struct {
	maxAge time.Duration
	// confirmed demands a re-proof the guard asked for, so an ordinary login
	// never satisfies it however recent it is.
	//
	// Signing in and confirming an operation are different acts. A login
	// happened for its own reasons and says nothing about the person now
	// asking to move money; a step-up happened because this operation
	// demanded it. Freshness alone conflates them, and a window wide enough
	// to be usable lets a sign-in stand in for the confirmation.
	confirmed bool
}

// needsReproof reports whether satisfying this window requires proving the
// identity again during the session, rather than measuring how long ago the
// login was. A confirmation always does, and so does a zero window, which is
// read as a confirmation for the current attempt.
func (w window) needsReproof() bool { return w.confirmed || w.maxAge == 0 }

// MaxAge admits any proof no older than d, including the login that started
// the session. Use it where recency is the point: a session that has been
// sitting open all afternoon should not reach an administration area, but the
// person who just signed in already proved who they are.
func MaxAge(d time.Duration) Requirement {
	return literalRequirement{window: window{maxAge: d}}
}

// Confirmed admits only a re-proof this guard asked for, within d. A login,
// however recent, never satisfies it.
//
// Use it where the act matters rather than the clock: moving money, deleting a
// tenant, exporting a customer list. Somebody who signed in a minute ago to
// read their dashboard has not agreed to any of those.
//
// A zero duration means confirm for this attempt, and two consecutive
// operations then require two confirmations.
func Confirmed(d time.Duration) Requirement {
	return literalRequirement{window: window{maxAge: d, confirmed: true}}
}

// Policy reads the window from auth.assurance.policy, so the same handler code
// serves a consumer deployment with a long window and an internal one with a
// short window. An undefined name is refused at startup by Config.validate
// rather than at the request that needed it.
func Policy(name string) Requirement { return namedRequirement(name) }

// Dynamic computes the window per request, for a deadline that no fixed
// duration expresses. An internal system re-confirming after every midnight
// returns the time elapsed since the most recent midnight, so a proof older
// than that boundary no longer counts.
func Dynamic(compute func(*http.Request) time.Duration) Requirement {
	return dynamicRequirement{compute: compute}
}

// DynamicOn is Dynamic over the transport seam, for a deployment whose requests
// are not *http.Request.
func DynamicOn(compute func(Exchange) time.Duration) Requirement {
	return dynamicRequirement{over: compute}
}

// Default is the window of auth.recent_auth_max_age, which is also what the
// passkey enrollment guard has always used.
func Default() Requirement { return defaultRequirement{} }

type literalRequirement struct{ window window }

func (r literalRequirement) resolve(Exchange, Config) (window, bool) {
	return r.window, true
}

type namedRequirement string

func (r namedRequirement) resolve(_ Exchange, config Config) (window, bool) {
	for _, policy := range config.Assurance.Policy {
		if policy.Name == string(r) {
			return window{maxAge: policy.MaxAge, confirmed: policy.Confirm}, true
		}
	}
	return window{}, false
}

type dynamicRequirement struct {
	// compute is the net/http form and over is the transport-neutral one.
	// Exactly one is set.
	compute func(*http.Request) time.Duration
	over    func(Exchange) time.Duration
}

// resolve calls whichever form the caller supplied.
//
// A requirement written against *http.Request cannot be evaluated on a request
// that is not one, so it reports unresolved there rather than substituting a
// window nobody asked for. The guard answers 503 and logs which requirement
// could not be resolved, which is the honest reading: the deployment named a
// rule this transport cannot apply, and admitting on that basis would open the
// guard the rule exists to close.
func (r dynamicRequirement) resolve(x Exchange, _ Config) (window, bool) {
	var value time.Duration
	switch {
	case r.over != nil:
		value = r.over(x)
	case r.compute != nil:
		request, ok := x.(*httpExchange)
		if !ok {
			return window{}, false
		}
		value = r.compute(request.request)
	default:
		return window{}, false
	}
	if value < 0 {
		return window{}, false
	}
	return window{maxAge: value}, true
}

type defaultRequirement struct{}

func (defaultRequirement) resolve(_ Exchange, config Config) (window, bool) {
	return window{maxAge: config.RecentAuthMaxAge}, true
}

// ErrNoAssurance reports that assurance could not be evaluated because the auth
// plugin is not running. It is returned rather than assumed, because assuming
// either answer would be wrong: assuming satisfied opens the guard, and
// assuming unsatisfied sends an anonymous deployment into a login it has no
// endpoint for.
var ErrNoAssurance = errors.New("auth: assurance is unavailable")

// Ensure wraps a page route: a request whose proof is older than the
// requirement is redirected into re-proof and returns to this operation.
//
// Guarding a read route is user experience rather than a boundary, because a
// client can post directly to the write route. Guard both, and give the write
// the more generous window so a user who reads, fills a long form, and submits
// does not lose the input to a guard the read had just satisfied.
func Ensure(handler http.HandlerFunc, requirement Requirement) http.HandlerFunc {
	return httpAssuranceGuard(handler, requirement, false)
}

// EnsureAPI wraps an API route: an unmet requirement answers 401 with a problem
// document naming the window, so a client can start the step-up itself.
//
// This is a separate function rather than an option with a default because the
// two failure modes are not symmetric. Answering an API call with a redirect is
// followed transparently by an XHR, so the client receives 200 and an HTML
// login page instead of an error; answering a page with 401 is merely ugly. A
// route that forgot to pass an option would take the silent failure.
func EnsureAPI(handler http.HandlerFunc, requirement Requirement) http.HandlerFunc {
	return httpAssuranceGuard(handler, requirement, true)
}

// zeroWindowAdmission serializes reading a zero-window proof and spending it.
//
// The two were separate operations, so two requests arriving together both read
// the same unspent proof and both were admitted: one confirmation, two
// transfers, which is precisely what a zero window exists to prevent. Nothing in
// the session handle offers a compare-and-swap, so the read and the write are
// made one critical section here instead.
//
// One mutex for every zero-window route is deliberate. They guard destructive
// operations behind a confirmation a person just performed, so they are rare by
// construction, and the section holds only a slot read and a slot write — never
// the handler. A lock keyed by session would be faster in a way nothing here can
// measure and wrong in ways that would take a while to find.
//
// It is per process. A deployment running several still narrows this to the
// store's own write, and the double-submit it closes — one browser, one tick,
// two fetches — lands in one process anyway.
var zeroWindowAdmission sync.Mutex

func httpAssuranceGuard(handler http.HandlerFunc, requirement Requirement, api bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		exchange := HTTPExchange(w, r)
		if AdmitAssurance(exchange, requirement, api) {
			handler(w, r)
		}
	}
}

// AdmitAssurance evaluates the requirement and, when it is unmet, writes the
// response that starts the step-up. It reports whether the handler may run.
//
// It is exported because the frame that wraps a handler is each transport's
// own, while what the requirement means is this package's.
func AdmitAssurance(x Exchange, requirement Requirement, api bool) bool {
	ok, err := admitAssurance(x, requirement)
	if err != nil {
		logger(x).Log(x.Context(), pwruntime.LevelError, "assurance check failed", pwruntime.Err(err))
		x.Problem(pwruntime.ServiceUnavailable())
		return false
	}
	if ok {
		return true
	}
	challenge(x, requirement, api)
	return false
}

// admitAssurance reports whether the requirement is met, spending a zero-window
// proof in the same breath so that it admits one operation and no other.
func admitAssurance(x Exchange, requirement Requirement) (bool, error) {
	if !zeroWindowRequirement(x, requirement) {
		// Every other window is satisfied by elapsed time, which nothing can
		// spend and no two requests can race over.
		return satisfied(x, requirement)
	}
	zeroWindowAdmission.Lock()
	defer zeroWindowAdmission.Unlock()
	ok, err := satisfied(x, requirement)
	if err != nil || !ok {
		return false, err
	}
	if err := consumeAdmission(x, requirement); err != nil {
		// The proof could not be spent, so it is not known to be spent, and
		// admitting on that basis is how one confirmation authorizes two
		// operations. Refusing costs the user a second confirmation.
		return false, err
	}
	return true, nil
}

// zeroWindowRequirement reports whether this requirement rests on a stored proof
// rather than on elapsed time.
func zeroWindowRequirement(x Exchange, requirement Requirement) bool {
	instance := activeRuntime()
	if instance == nil {
		return false
	}
	want, resolved := requirement.resolve(x, instance.config)
	return resolved && want.maxAge == 0
}

// IsRecent reports whether the requirement is met and writes nothing. Use it
// with Challenge when the window depends on something only the handler knows,
// such as a payment amount. It is split from Challenge because a function that
// both returns a decision and writes a response hides control flow.
func IsRecent(r *http.Request, requirement Requirement) bool {
	return IsRecentOn(HTTPExchange(nil, r), requirement)
}

// IsRecentOn is IsRecent over the transport seam.
func IsRecentOn(x Exchange, requirement Requirement) bool {
	ok, err := satisfied(x, requirement)
	return err == nil && ok
}

// Challenge writes the response an unmet requirement produces and starts the
// step-up. Pass api = true from a route whose caller is not a browser.
func Challenge(w http.ResponseWriter, r *http.Request, requirement Requirement, api bool) {
	challenge(HTTPExchange(w, r), requirement, api)
}

// ChallengeOn is Challenge over the transport seam.
func ChallengeOn(x Exchange, requirement Requirement, api bool) {
	challenge(x, requirement, api)
}

// LastProvedAt reports when the identity of the request was last actually
// proved, so a page can warn before the user commits to a long form. The bool
// is false for a request carrying no session.
func LastProvedAt(ctx context.Context) (time.Time, bool) {
	view, ok := Session(ctx)
	if !ok {
		return time.Time{}, false
	}
	return view.provenAt(), true
}

// satisfied evaluates the requirement against the request's session.
func satisfied(x Exchange, requirement Requirement) (bool, error) {
	instance := activeRuntime()
	if instance == nil {
		return false, ErrNoAssurance
	}
	view, ok := Session(x.Context())
	if !ok {
		// An anonymous request is a login problem rather than an assurance
		// problem, and the path guard answers it first. Reaching here means the
		// route is unguarded, so the challenge sends the user to log in.
		return false, nil
	}
	want, resolved := requirement.resolve(x, instance.config)
	if !resolved {
		return false, fmt.Errorf("%w: requirement could not be resolved", ErrNoAssurance)
	}
	if want.needsReproof() && !instance.config.reprovable() {
		// The mode has no way to prove this identity a second time, so the
		// requirement can be neither met nor remedied. Reporting it as unmet
		// would send the browser into a re-proof that returns it exactly as
		// unproved as it left, forever; reporting it as met would admit the
		// operation the guard exists to hold. It is an error, which answers 503
		// and names the deployment mistake in the log.
		return false, fmt.Errorf("%w: auth.mode %q cannot re-prove an identity, so a confirmed or zero-window requirement can never be satisfied",
			ErrNoAssurance, instance.config.Mode)
	}
	if want.confirmed {
		return confirmedWithin(view, want.maxAge), nil
	}
	if want.maxAge == 0 {
		// Elapsed time can never satisfy a zero window: the redirect to the
		// provider and back always consumes more than zero seconds, so a
		// timestamp comparison would challenge again immediately after a
		// successful re-proof and never converge. Read it as a confirmation
		// for this attempt, which is what it was always meant to say.
		return stepUpAdmitted(view), nil
	}
	return time.Since(view.provenAt()) <= want.maxAge, nil
}

// confirmedWithin reports whether a re-proof the guard asked for happened
// inside the window. The login that started the session does not count, however
// recent it is: it proved an identity, not an intention.
func confirmedWithin(data SessionData, maxAge time.Duration) bool {
	if maxAge == 0 {
		return stepUpAdmitted(data)
	}
	if data.StepUpAt <= 0 {
		return false
	}
	return time.Since(time.Unix(data.StepUpAt, 0)) <= maxAge
}

// consumeAdmission spends a zero-window admission the guard just accepted.
//
// The window that bounds it is a backstop rather than the mechanism: a proof
// asked for one operation admits that operation and no other. Writing the slot
// is an ordinary session write, which is what makes exact single use possible
// here; before per-slot writes existed, spending it would have meant rotating
// the session inside a read, and the stamp was left to expire instead.
//
// It runs only where the guard admits. A handler that calls IsRecent and
// Challenge itself keeps the time-bounded behavior, because IsRecent writes
// nothing by contract and owns its own denial.
func consumeAdmission(x Exchange, requirement Requirement) error {
	instance := activeRuntime()
	if instance == nil {
		return nil
	}
	want, resolved := requirement.resolve(x, instance.config)
	if !resolved || want.maxAge != 0 {
		// Only a zero window rests on the admission; every other window is
		// satisfied by elapsed time, which nothing can spend.
		return nil
	}
	handle, ok := session.Value[SessionData](x.Context())
	if !ok {
		return nil
	}
	data, present := handle.Get()
	if !present || data.StepUpAt <= 0 {
		return nil
	}
	data.StepUpAt = 0
	// A failed write is reported rather than swallowed. Leaving the stamp
	// standing was described as costing a redundant confirmation; it does the
	// opposite, because the stamp is what grants the next admission. The caller
	// refuses instead.
	return handle.Set(data)
}

// stepUpAdmissionWindow bounds how long a completed zero-window proof admits an
// operation. It is the backstop for an admission nobody spent, such as one the
// user abandoned; consumeAdmission is what makes it single use.
const stepUpAdmissionWindow = 30 * time.Second

func stepUpAdmitted(data SessionData) bool {
	if data.StepUpAt <= 0 {
		return false
	}
	return time.Since(time.Unix(data.StepUpAt, 0)) <= stepUpAdmissionWindow
}

// challenge answers an unmet requirement. A page route is sent into re-proof
// and comes back to this operation; an API route is told what was missing and
// starts the step-up itself.
//
// The status is 401 rather than 403 because the remedy exists and the client is
// meant to prove again and retry, which is also what RFC 9470 returns for
// insufficient_user_authentication. The Bearer challenge header of that
// specification is not emitted here: these routes authenticate with a cookie
// and are not in the Bearer scheme.
func challenge(x Exchange, requirement Requirement, api bool) {
	instance := activeRuntime()
	if instance == nil {
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	want, resolved := requirement.resolve(x, instance.config)
	if !resolved {
		logger(x).Log(x.Context(), pwruntime.LevelError, "assurance requirement could not be resolved")
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	if want.needsReproof() && !instance.config.reprovable() {
		// The same refusal satisfied makes, for the handler that evaluates a
		// requirement itself and then asks for the challenge. Redirecting into a
		// login that cannot re-prove anything is the loop this avoids.
		logger(x).Log(x.Context(), pwruntime.LevelError, "assurance requirement cannot be re-proved in this mode",
			pwruntime.String("mode", instance.config.Mode))
		x.Problem(pwruntime.ServiceUnavailable())
		return
	}
	if api {
		x.Problem(pwruntime.Unauthorized())
		return
	}
	instance.redirect(x, instance.stepUpPath(x, want.maxAge))
}
