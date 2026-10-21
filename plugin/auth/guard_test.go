package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/internal/pathpattern"
	"github.com/shibukawa/popcornweb/pwruntime"
)

// navigationAccept is what a browser sends when it opens a page.
const navigationAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

// guardRuntime is a deployment that protects everything but its own login
// paths, which is the shape that sweeps in the icons a browser asks for
// unprompted.
func guardRuntime(t *testing.T, unauthenticated string) *runtime {
	t.Helper()
	include, err := pathpattern.Compile([]string{"/**"})
	if err != nil {
		t.Fatal(err)
	}
	config := baseConfig(ModeOIDCOnly)
	config.Protection.Unauthenticated = unauthenticated
	return &runtime{config: config, include: include}
}

// askGuard runs one request through the guard and reports whether the handler
// behind it was reached.
func askGuard(rt *runtime, target string, headers map[string]string, signedIn bool) (*httptest.ResponseRecorder, bool) {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	exchange := HTTPExchange(recorder, request)
	if signedIn {
		exchange.RecordAuthentication(pwruntime.Authentication{Authenticated: true})
	}
	reached := false
	rt.guard(exchange, func() { reached = true })
	return recorder, reached
}

// A redirect to the login is an answer to a person. Safari asks for
// /apple-touch-icon.png and its siblings unprompted, with the cookies of the
// site it is on, and follows whatever it is told; the login endpoint it was
// sent to would then begin an authorization and use up the one-time mark a
// sign-out left behind. So a request that does not ask for a page is refused
// where it stands, and the login is reached only by one that does.
func TestTheGuardRedirectsOnlyARequestThatAsksForAPage(t *testing.T) {
	rt := guardRuntime(t, UnauthenticatedRedirect)

	for _, testCase := range []struct {
		name    string
		headers map[string]string
		page    bool
	}{
		{"a navigation", map[string]string{"Accept": navigationAccept, "Sec-Fetch-Dest": "document"}, true},
		{"a document destination alone", map[string]string{"Sec-Fetch-Dest": "document"}, true},
		{"an Accept that names HTML alone", map[string]string{"Accept": "text/html"}, true},
		{"a partial update from the browser runtime",
			map[string]string{"Accept": "*/*", "Sec-Fetch-Dest": "empty", pwruntime.UpdateRenderHeader: "1"}, true},
		{"a live delivery from the browser runtime",
			map[string]string{"Accept": "*/*", "Sec-Fetch-Dest": "empty", pwruntime.ResponseModeHeader: "1"}, true},

		{"a request that sent nothing", nil, false},
		{"a tool that asks for anything", map[string]string{"Accept": "*/*"}, false},
		{"an icon fetched for itself", map[string]string{
			"Accept": "image/webp,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5", "Sec-Fetch-Dest": "image"}, false},
		{"a script's fetch", map[string]string{"Accept": "application/json", "Sec-Fetch-Dest": "empty"}, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder, reached := askGuard(rt, "/apple-touch-icon.png", testCase.headers, false)
			if reached {
				t.Fatal("the handler behind the guard was reached by an unauthenticated request")
			}
			if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store: the answer depends on who asks and what for", got)
			}
			if testCase.page {
				if recorder.Code != http.StatusSeeOther {
					t.Fatalf("status = %d, want 303", recorder.Code)
				}
				if location := recorder.Header().Get("Location"); location != "/auth/login?next=%2Fapple-touch-icon.png" {
					t.Errorf("Location = %q, want the login carrying the return path", location)
				}
				return
			}
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", recorder.Code)
			}
			if location := recorder.Header().Get("Location"); location != "" {
				t.Errorf("a request that asked for no page was sent to %q", location)
			}
		})
	}
}

// A deployment that answers 401 answers it to a page request too; naming a page
// is what makes a request eligible for the redirect, never what makes it
// owed one.
func TestTheGuardAnswers401ToEveryoneWhenTheDeploymentDoesNotRedirect(t *testing.T) {
	rt := guardRuntime(t, UnauthenticatedUnauthorized)

	recorder, reached := askGuard(rt, "/account", map[string]string{"Accept": navigationAccept}, false)
	if reached {
		t.Fatal("the handler behind the guard was reached by an unauthenticated request")
	}
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if location := recorder.Header().Get("Location"); location != "" {
		t.Errorf("a deployment that does not redirect sent a browser to %q", location)
	}
}

// What the guard does not decide stays decided: a signed-in request passes
// whatever it asks for, and a path that is not protected is never asked about.
func TestTheGuardStillAdmitsWhoIsSignedInAndWhatIsNotProtected(t *testing.T) {
	rt := guardRuntime(t, UnauthenticatedRedirect)

	for _, testCase := range []struct {
		name     string
		target   string
		headers  map[string]string
		signedIn bool
	}{
		{"a signed-in request for an icon", "/apple-touch-icon.png", map[string]string{"Accept": "image/*"}, true},
		{"a signed-in navigation", "/account", map[string]string{"Accept": navigationAccept}, true},
		{"the login path, which is never protected", "/auth/login", nil, false},
		{"the callback path, which is never protected", "/auth/callback", nil, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder, reached := askGuard(rt, testCase.target, testCase.headers, testCase.signedIn)
			if !reached {
				t.Fatalf("the request was stopped: %d %s", recorder.Code, strings.TrimSpace(recorder.Body.String()))
			}
		})
	}
}
