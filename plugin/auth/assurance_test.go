package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/session"
)

func TestNamedPolicyResolvesItsWindow(t *testing.T) {
	config := baseConfig(ModeOIDCOnly)
	config.Assurance = AssuranceConfig{Policy: []AssurancePolicy{
		{Name: "admin", MaxAge: 15 * time.Minute},
		{Name: "danger", MaxAge: 0},
	}}
	request := httptest.NewRequest("GET", "/admin", nil)
	for name, want := range map[string]time.Duration{"admin": 15 * time.Minute, "danger": 0} {
		got, ok := Policy(name).resolve(HTTPExchange(nil, request), config)
		if !ok || got.maxAge != want {
			t.Fatalf("policy %q = %v, %v; want %v", name, got, ok, want)
		}
	}
	if _, ok := Policy("absent").resolve(HTTPExchange(nil, request), config); ok {
		t.Fatal("an undefined policy resolved")
	}
}

// An undefined name must fail at startup rather than at the request that needed
// it, because a guard that cannot resolve its own requirement is a route with no
// guard at all.
func TestAnUndefinedPolicyNameIsRefusedAtStartup(t *testing.T) {
	config := baseConfig(ModeOIDCOnly)
	config.Assurance = AssuranceConfig{Policy: []AssurancePolicy{
		{Name: "admin", MaxAge: time.Minute},
		{Name: "admin", MaxAge: time.Hour},
	}}
	err := config.validateShape()
	if err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Fatalf("duplicate policy name = %v", err)
	}

	config.Assurance = AssuranceConfig{Policy: []AssurancePolicy{{MaxAge: time.Minute}}}
	if err := config.validateShape(); err == nil || !strings.Contains(err.Error(), "name must be set") {
		t.Fatalf("unnamed policy = %v", err)
	}

	config.Assurance = AssuranceConfig{Policy: []AssurancePolicy{{Name: "x", MaxAge: -time.Second}}}
	if err := config.validateShape(); err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Fatalf("negative window = %v", err)
	}
}

func TestDefaultRequirementReadsRecentAuthMaxAge(t *testing.T) {
	config := baseConfig(ModeOIDCOnly)
	config.RecentAuthMaxAge = 90 * time.Second
	got, ok := Default().resolve(HTTPExchange(nil, httptest.NewRequest("GET", "/", nil)), config)
	if !ok || got.maxAge != 90*time.Second {
		t.Fatalf("default = %v, %v", got, ok)
	}
}

// A wall-clock deadline is not a fixed duration, so the window is computed per
// request. This is the shape an internal system re-confirming after every
// midnight needs: the proof must postdate the boundary, so the window is the
// time elapsed since it.
func TestDynamicRequirementComputesItsWindow(t *testing.T) {
	config := baseConfig(ModeOIDCOnly)
	request := httptest.NewRequest("GET", "/", nil)
	sinceMidnight := func(*http.Request) time.Duration {
		now := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return now.Sub(midnight)
	}
	got, ok := Dynamic(sinceMidnight).resolve(HTTPExchange(nil, request), config)
	if !ok || got.maxAge != 9*time.Hour+30*time.Minute {
		t.Fatalf("dynamic window = %v, %v", got, ok)
	}
	if _, ok := Dynamic(nil).resolve(HTTPExchange(nil, request), config); ok {
		t.Fatal("a nil resolver produced a window")
	}
	if _, ok := Dynamic(func(*http.Request) time.Duration { return -time.Second }).resolve(HTTPExchange(nil, request), config); ok {
		t.Fatal("a negative window was accepted")
	}
}

// Signing in and confirming an operation are different acts. Freshness alone
// conflates them: a window wide enough to be usable lets the login that started
// the session stand in for a confirmation nobody asked the user for.
func TestAFreshLoginDoesNotCountAsAConfirmation(t *testing.T) {
	justSignedIn := SessionData{ProviderAuthTime: time.Now().Unix()}

	if !confirmedWithin(SessionData{StepUpAt: time.Now().Unix()}, 15*time.Minute) {
		t.Fatal("a step-up inside the window was refused")
	}
	if confirmedWithin(justSignedIn, 15*time.Minute) {
		t.Fatal("a login one second old satisfied a confirmation requirement")
	}
	stale := time.Now().Add(-16 * time.Minute).Unix()
	if confirmedWithin(SessionData{StepUpAt: stale}, 15*time.Minute) {
		t.Fatal("a confirmation older than the window was accepted")
	}
}

// A zero duration means confirm for this attempt, whichever constructor asked,
// so Confirmed(0) and an unconfirmed zero window agree.
func TestAZeroConfirmationWindowMeansThisAttempt(t *testing.T) {
	if confirmedWithin(SessionData{}, 0) {
		t.Fatal("a session with no step-up satisfied a zero confirmation window")
	}
	if !confirmedWithin(SessionData{StepUpAt: time.Now().Unix()}, 0) {
		t.Fatal("a just-completed step-up was refused")
	}
}

// The two constructors differ only in whether a login counts, which is the
// whole of the distinction.
func TestConfirmedAndMaxAgeDifferOnlyOnWhoMayProve(t *testing.T) {
	config := baseConfig(ModeOIDCOnly)
	request := httptest.NewRequest("GET", "/", nil)
	plain, _ := MaxAge(15*time.Minute).resolve(HTTPExchange(nil, request), config)
	strict, _ := Confirmed(15*time.Minute).resolve(HTTPExchange(nil, request), config)
	if plain.maxAge != strict.maxAge {
		t.Fatalf("windows differ: %v and %v", plain.maxAge, strict.maxAge)
	}
	if plain.confirmed || !strict.confirmed {
		t.Fatalf("confirmed = %v and %v", plain.confirmed, strict.confirmed)
	}
}

// A named policy carries the same distinction, so a deployment tunes both the
// window and whether a login may fill it.
func TestANamedPolicyCanDemandAConfirmation(t *testing.T) {
	config := baseConfig(ModeOIDCOnly)
	config.Assurance = AssuranceConfig{Policy: []AssurancePolicy{
		{Name: "admin", MaxAge: 15 * time.Minute},
		{Name: "transfer", MaxAge: 5 * time.Minute, Confirm: true},
	}}
	request := httptest.NewRequest("GET", "/", nil)
	if got, _ := Policy("admin").resolve(HTTPExchange(nil, request), config); got.confirmed {
		t.Fatal("admin demanded a confirmation it did not declare")
	}
	got, ok := Policy("transfer").resolve(HTTPExchange(nil, request), config)
	if !ok || !got.confirmed || got.maxAge != 5*time.Minute {
		t.Fatalf("transfer = %+v, %v", got, ok)
	}
}

// stepUpAdmitted is what a zero window rests on, because elapsed time can never
// satisfy one: the redirect to the provider and back always costs more than
// zero seconds.
func TestAZeroWindowIsSatisfiedOnlyByACompletedStepUp(t *testing.T) {
	if stepUpAdmitted(SessionData{}) {
		t.Fatal("a session with no step-up was admitted")
	}
	if !stepUpAdmitted(SessionData{StepUpAt: time.Now().Unix()}) {
		t.Fatal("a just-completed step-up was refused, which would loop forever")
	}
	stale := time.Now().Add(-stepUpAdmissionWindow - time.Second).Unix()
	if stepUpAdmitted(SessionData{StepUpAt: stale}) {
		t.Fatal("a step-up older than the admission window was admitted")
	}
}

// Freshness is measured from what the provider reported, not from when the
// login landed: a provider may satisfy an authorization request from a single
// sign-on session established much earlier.
func TestFreshnessPrefersTheProviderProofTime(t *testing.T) {
	arrival := time.Now()
	proved := arrival.Add(-8 * time.Hour)
	data := SessionData{AuthenticatedAt: arrival, ProviderAuthTime: proved.Unix()}
	if got := data.provenAt(); got.Unix() != proved.Unix() {
		t.Fatalf("provenAt = %v, want the reported %v", got, proved)
	}
	if got := (SessionData{AuthenticatedAt: arrival}).provenAt(); !got.Equal(arrival) {
		t.Fatalf("provenAt with no provider time = %v, want the fallback %v", got, arrival)
	}
}

// A zero window means prove again for this operation. Before per-slot writes
// existed the proof was only time-bounded, so two destructive operations inside
// the window shared one confirmation; spending it on use closes that.
func TestAZeroWindowAdmissionIsSpentOnUse(t *testing.T) {
	registry := session.NewRegistry()
	if err := registry.Register[SessionData](sessionSlotKey, session.Private, nil); err != nil {
		t.Fatal(err)
	}
	keys, err := session.ParseKeyring("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := session.NewManager(registry, nil, session.Options{
		TTL:    time.Hour,
		Cookie: session.CookieOptions{Name: "pw_session", Path: "/"},
		Keys:   keys,
	})
	if err != nil {
		t.Fatal(err)
	}
	config := baseConfig(ModeOIDCOnly)
	replaceRuntime(&runtime{config: config, stopPruning: make(chan struct{})})
	t.Cleanup(func() { replaceRuntime(nil) })

	// Establish a session carrying a completed zero-window proof.
	issue := httptest.NewRecorder()
	manager.Middleware(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		handle, _ := session.Value[SessionData](r.Context())
		if err := handle.Set(SessionData{
			AccountID: "a", AuthenticatedAt: time.Now(), StepUpAt: time.Now().Unix(),
		}); err != nil {
			t.Fatal(err)
		}
	})).ServeHTTP(issue, httptest.NewRequest(http.MethodGet, "/", nil))

	admitted := 0
	guarded := manager.Middleware(nil)(Ensure(func(http.ResponseWriter, *http.Request) { admitted++ }, MaxAge(0)))
	visit := func(cookies []*http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/danger", nil)
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		guarded.ServeHTTP(recorder, request)
		return recorder
	}

	first := visit(issue.Result().Cookies())
	if admitted != 1 {
		t.Fatalf("the first operation was not admitted: %d", admitted)
	}
	visit(first.Result().Cookies())
	if admitted != 1 {
		t.Fatalf("a second operation reused the same proof: %d admissions", admitted)
	}
}
