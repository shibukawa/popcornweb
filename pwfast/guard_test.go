package pwfast

import (
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

func guarded(policy GuardPolicy) fasthttp.RequestHandler {
	return Compose(func(r *fasthttp.RequestCtx) { _, _ = r.WriteString("protected page") },
		Frame{Slot: SlotGuard, Middleware: Guard(policy)})
}

// navigation is what a browser sends when it opens a page, spelled as the raw
// header lines serveRaw takes. A redirect to the login is an answer to this
// request and to none that lacks it, so a test of the redirect has to send it.
const navigation = "Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8\r\n"

func TestAnUnauthenticatedRequestIsRedirectedWhenConfigured(t *testing.T) {
	handler := guarded(GuardPolicy{
		Protected: func(path string) bool { return strings.HasPrefix(path, "/admin") },
		LoginURL:  func(path string) string { return "/login?next=" + path },
		Redirect:  true,
	})

	status, header, body := serveRaw(t, handler, "/admin/users", navigation)
	if status != fasthttp.StatusSeeOther {
		t.Errorf("status = %d, want 303", status)
	}
	if body == "protected page" {
		t.Error("the protected page was served to an unauthenticated caller")
	}
	if !strings.Contains(header, "Location: /login?next=/admin/users") {
		t.Errorf("no login redirect:\n%s", header)
	}
	// Never cached: a shared cache holding this would show a login redirect to
	// a signed-in reader, or worse the other way round.
	if !strings.Contains(header, "Cache-Control: no-store") {
		t.Errorf("the guard answer was cacheable:\n%s", header)
	}
}

// A redirect is an answer to a person. A browser fetching an icon follows it as
// readily as one opening a page, and the login endpoint it lands on does what a
// visit there does, so a request that does not ask for a page is refused where
// it stands even when the deployment redirects the ones that do.
//
// Safari asks for /apple-touch-icon.png and its siblings unprompted, with the
// cookies of the site it is on. The same shape covers an image, a script's
// fetch, and a tool that never said what it wanted.
func TestARequestThatDoesNotAskForAPageIsRefusedNotRedirected(t *testing.T) {
	handler := guarded(GuardPolicy{
		Protected: func(string) bool { return true },
		LoginURL:  func(path string) string { return "/login?next=" + path },
		Redirect:  true,
	})

	for name, headers := range map[string]string{
		"no headers at all":             "",
		"a tool that asks for anything": "Accept: */*\r\n",
		"an icon fetched for itself": "Accept: image/webp,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5\r\n" +
			"Sec-Fetch-Dest: image\r\n",
		"a script's fetch":             "Accept: application/json\r\nSec-Fetch-Dest: empty\r\n",
		"a stylesheet":                 "Accept: text/css,*/*;q=0.1\r\nSec-Fetch-Dest: style\r\n",
		"a text type that is not HTML": "Accept: text/plain\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			status, header, body := serveRaw(t, handler, "/apple-touch-icon.png", headers)
			if status != fasthttp.StatusUnauthorized {
				t.Errorf("status = %d, want 401", status)
			}
			if strings.Contains(strings.ToLower(header), "location:") {
				t.Errorf("a request for no page was sent to the login:\n%s", header)
			}
			if body == "protected page" {
				t.Error("the protected page was served to an unauthenticated caller")
			}
			// Per visitor and per request, so never cached.
			if !strings.Contains(header, "Cache-Control: no-store") {
				t.Errorf("the refusal was cacheable:\n%s", header)
			}
		})
	}
}

// The other half of the same rule: whatever announces a page is still sent to
// the login, in either of the two ways a browser announces one, and so is the
// browser runtime's own traffic.
func TestARequestThatAsksForAPageIsStillRedirected(t *testing.T) {
	handler := guarded(GuardPolicy{
		Protected: func(string) bool { return true },
		LoginURL:  func(path string) string { return "/login?next=" + path },
		Redirect:  true,
	})

	for name, headers := range map[string]string{
		"a navigation":                        navigation,
		"a document destination alone":        "Sec-Fetch-Dest: document\r\n",
		"an Accept that names HTML alone":     "Accept: text/html\r\n",
		"an Accept that names HTML late":      "Accept: application/json, text/html;q=0.1\r\n",
		"a destination with a generic Accept": "Accept: */*\r\nSec-Fetch-Dest: document\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			status, header, _ := serveRaw(t, handler, "/admin/users", headers)
			if status != fasthttp.StatusSeeOther {
				t.Fatalf("status = %d, want 303", status)
			}
			if !strings.Contains(header, "Location: /login?next=/admin/users") {
				t.Errorf("no login redirect:\n%s", header)
			}
		})
	}
}

// The runtime's partial updates and live deliveries are fetches that name no
// document and no HTML, and a redirect answered to one is turned into a
// navigation by the runtime rather than followed. A refusal would leave the
// page it belongs to showing a session that ended under it.
func TestTheRuntimesOwnRequestsAreNotRefusedForNamingNoPage(t *testing.T) {
	handler := guarded(GuardPolicy{
		Protected: func(string) bool { return true },
		LoginURL:  func(path string) string { return "/login?next=" + path },
		Redirect:  true,
	})

	for name, header := range map[string]string{
		"a partial update": pwruntime.UpdateRenderHeader,
		"a live delivery":  pwruntime.ResponseModeHeader,
	} {
		t.Run(name, func(t *testing.T) {
			status, _, _ := serveRaw(t, handler, "/admin/users",
				"Accept: */*\r\nSec-Fetch-Dest: empty\r\n"+header+": 1\r\n")
			if status == fasthttp.StatusUnauthorized {
				t.Errorf("the runtime's request was refused: status = %d", status)
			}
		})
	}
}

// Without Redirect nothing is sent to a login whatever it asks for: an API-only
// deployment answers a browser's navigation with the same 401 as a fetch.
func TestWithoutRedirectEvenAPageRequestIsRefused(t *testing.T) {
	handler := guarded(GuardPolicy{
		Protected: func(string) bool { return true },
		LoginURL:  func(path string) string { return "/login?next=" + path },
	})
	status, header, _ := serveRaw(t, handler, "/admin/users", navigation)
	if status != fasthttp.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
	if strings.Contains(strings.ToLower(header), "location:") {
		t.Errorf("a deployment that does not redirect redirected:\n%s", header)
	}
}

func TestAnUnauthenticatedAPIRequestIs401WithAChallenge(t *testing.T) {
	handler := guarded(GuardPolicy{
		Protected:   func(string) bool { return true },
		BearerRealm: "example",
	})
	status, header, _ := serve(t, handler, "/api/items")
	if status != fasthttp.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
	// Case-insensitively: this transport canonicalises the name as
	// Www-Authenticate and the other as WWW-Authenticate, and a client does not
	// care which. The shared test seam reads headers this way for exactly this
	// reason; a raw-header assertion has to do it by hand.
	if !strings.Contains(strings.ToLower(header), `www-authenticate: bearer realm="example"`) {
		t.Errorf("no challenge was sent:\n%s", header)
	}
}

func TestAnUnprotectedPathIsServed(t *testing.T) {
	handler := guarded(GuardPolicy{Protected: func(path string) bool { return path == "/admin" }})
	if _, _, body := serve(t, handler, "/about"); body != "protected page" {
		t.Errorf("an unprotected path was refused: %q", body)
	}
}

// A policy protecting nothing installs no frame, so a deployment that has not
// configured protection pays nothing and cannot half-have it.
func TestAnEmptyPolicyInstallsNoFrame(t *testing.T) {
	if _, _, body := serve(t, guarded(GuardPolicy{}), "/admin"); body != "protected page" {
		t.Errorf("an unconfigured guard refused: %q", body)
	}
}

// The login target is a URL a browser is sent to, and it carries a path taken
// from the request, so it goes through the same refusal every redirect does.
func TestAGuardWillNotRedirectToAScriptURL(t *testing.T) {
	handler := guarded(GuardPolicy{
		Protected: func(string) bool { return true },
		LoginURL:  func(string) string { return "javascript:alert(1)" },
		Redirect:  true,
	})
	// A navigation, because a request that asked for no page is refused before
	// the target is ever looked at and would pass this without testing it.
	status, header, _ := serveRaw(t, handler, "/admin", navigation)
	if strings.Contains(strings.ToLower(header), "location: javascript:") {
		t.Errorf("the guard sent a script URL:\n%s", header)
	}
	if status == fasthttp.StatusSeeOther {
		t.Errorf("status = %d, want a refusal", status)
	}
}

// The same canonicalisation the CSRF check uses, for the same reason: a path
// that cannot be matched unambiguously could select a different target than the
// one the policy decided about.
func TestAnAmbiguousPathIsRefusedByTheGuard(t *testing.T) {
	handler := guarded(GuardPolicy{Protected: func(string) bool { return true }})
	if status, _, body := serve(t, handler, "/admin//deep"); status == fasthttp.StatusOK {
		t.Errorf("an ambiguous path was served: %d %q", status, body)
	}
}

// The guard reaches the chain from options, positioned by its slot — below the
// session, so it observes whatever the session and authentication established.
func TestTheChainInstallsTheGuardWhenAPolicyIsGiven(t *testing.T) {
	publishChainSettings(t, pwruntime.ChainSettings{})
	handler, err := Middlewares(func(r *fasthttp.RequestCtx) {
		_, _ = r.WriteString("protected page")
	}, RuntimeOptions{Guard: GuardPolicy{
		Protected: func(path string) bool { return strings.HasPrefix(path, "/admin") },
	}})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, body := serve(t, handler, "/admin"); status != fasthttp.StatusUnauthorized {
		t.Errorf("a protected path answered %d %q", status, body)
	}
	if _, _, body := serve(t, handler, "/about"); body != "protected page" {
		t.Errorf("an unprotected path was refused: %q", body)
	}
}

// The canonical path check reads the undecoded path, and only the path.
//
// It used to be handed the whole request target, so a query parameter carrying
// an encoded slash — which is what a return path looks like — refused a request
// whose path was ordinary. A login redirect carries exactly that parameter, so
// this is the shape of request the guard sends a browser away with.
func TestTheGuardReadsTheUndecodedPath(t *testing.T) {
	handler := Guard(GuardPolicy{
		Protected: func(path string) bool { return strings.HasPrefix(path, "/admin") },
	})(func(r *fasthttp.RequestCtx) { r.SetStatusCode(fasthttp.StatusOK) })

	// An encoded separator is refused: it decodes to a path this decided about
	// and a router may resolve somewhere else.
	if status, _, _ := serve(t, handler, "/a%2Fb"); status != fasthttp.StatusBadRequest {
		t.Errorf("an encoded separator was not refused: status = %d", status)
	}
	// One in a query value is not a path at all, and used to be refused anyway.
	if status, _, _ := serve(t, handler, "/open?next=%2Fdashboard"); status != fasthttp.StatusOK {
		t.Errorf("an encoded slash in the query refused the request: status = %d", status)
	}
}
