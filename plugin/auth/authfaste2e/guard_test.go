package authfaste2e

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The guard observes the authentication the frame two positions above it
// recorded. On this transport that recording is a write into the pooled request
// value rather than a derived context, which is the one thing about the port
// that could have gone wrong silently: a guard reading nothing would refuse
// every signed-in visitor, and a guard reading a stale value would admit the
// wrong one.
func TestTheGuardSeesTheAuthenticationTheFrameRecorded(t *testing.T) {
	browser := newBrowser(t).noRedirect()

	response, _ := browser.get("/private")
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("an anonymous request to a protected path answered %d, want 303", response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Path != "/auth/login" {
		t.Errorf("the guard sent the browser to %q, want the login path", location.Path)
	}
	// The return path is what brings the visitor back to what they asked for,
	// and it is validated as a local path before it is echoed.
	if next := location.Query().Get("next"); next != "/private" {
		t.Errorf("return path = %q, want /private", next)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q; a guard decision is per-visitor and must not be cached",
			response.Header.Get("Cache-Control"))
	}

	browser.login()

	response, payload := browser.get("/private")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("a signed-in request to a protected path answered %d", response.StatusCode)
	}
	if !strings.HasPrefix(string(payload), "private:") || string(payload) == "private:" {
		t.Fatalf("the protected handler answered %q", payload)
	}
}

// A request that does not ask for a page is refused where it stands, and never
// reaches the login: nothing it could begin there is left behind. The
// transaction cookie is the evidence, because only the login endpoint writes it.
func TestARequestForNoPageIsRefusedBeforeTheLogin(t *testing.T) {
	browser := newBrowser(t).noRedirect()

	for _, path := range append([]string{"/private"}, unpromptedPaths...) {
		response, _ := browser.fetch(path, "*/*", "")
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s answered %d, want 401", path, response.StatusCode)
		}
		if location := response.Header.Get("Location"); location != "" {
			t.Errorf("%s was sent to %q", path, location)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q; a guard decision is per-visitor and must not be cached",
				path, response.Header.Get("Cache-Control"))
		}
		for _, cookie := range response.Cookies() {
			if strings.HasSuffix(cookie.Name, "_txn") {
				t.Errorf("%s began an authorization: %#v", path, cookie)
			}
		}
	}
}

// Either way a browser announces a page is enough on its own: the destination
// every current browser sends, and the Accept the older ones do.
func TestEitherAnnouncementOfAPageIsSentToTheLogin(t *testing.T) {
	browser := newBrowser(t).noRedirect()

	for name, ask := range map[string]func() (*http.Response, []byte){
		"a document destination alone": func() (*http.Response, []byte) { return browser.fetch("/private", "", "document") },
		"an Accept that names HTML":    func() (*http.Response, []byte) { return browser.fetch("/private", "text/html", "") },
	} {
		response, _ := ask()
		if response.StatusCode != http.StatusSeeOther {
			t.Errorf("%s answered %d, want 303", name, response.StatusCode)
		}
		if location := response.Header.Get("Location"); !strings.HasPrefix(location, "/auth/login") {
			t.Errorf("%s was sent to %q, want the login", name, location)
		}
	}
}

// The framework's own login path stays reachable whatever the include patterns
// say, or a deployment that protected everything could never sign anyone in.
func TestTheLoginPathIsNeverProtected(t *testing.T) {
	browser := newBrowser(t).noRedirect()

	response, _ := browser.get("/auth/login")
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("the login path answered %d, want the provider redirect", response.StatusCode)
	}
	if strings.Contains(response.Header.Get("Location"), "/auth/login") {
		t.Fatal("the guard redirected the login path to itself")
	}
}

// A path a router could resolve two ways is refused rather than decided about.
// The canonical check reads the path before this transport decoded it, so the
// encoded separator is still visible when the decision is made.
func TestAnEncodedSeparatorIsRefused(t *testing.T) {
	browser := newBrowser(t).noRedirect()

	response, _ := browser.get("/private%2Fthing")
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("an encoded separator answered %d, want 400", response.StatusCode)
	}
	// An encoded slash inside a query value is not an ambiguous path, and used
	// to be refused as if it were. A login return path is exactly that shape.
	response, _ = browser.get("/private?next=%2Fdashboard")
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("an encoded slash in the query answered %d, want the guard redirect", response.StatusCode)
	}
}

// The presence endpoint takes one bit and ends the session on absence. It is
// the only endpoint that reads a JSON body without a cookie beside it, so it is
// where a body-reading mistake would show first.
func TestPresenceEndsAnAbsentSession(t *testing.T) {
	browser := newBrowser(t)
	browser.login()

	response, payload := browser.post("/auth/logout/presence", map[string]any{"active": true})
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("a presence report answered %d: %s", response.StatusCode, payload)
	}
	if len(payload) != 0 {
		t.Errorf("a 204 carried a body: %q", payload)
	}
	if identity := browser.whoami(); identity == "anonymous" {
		t.Fatal("reporting presence ended the session")
	}

	response, _ = browser.post("/auth/logout/presence", map[string]any{"active": false})
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("an absence report answered %d", response.StatusCode)
	}
	if identity := browser.whoami(); identity != "anonymous" {
		t.Fatalf("an absence report left the session alive: %q", identity)
	}
}

// An anonymous presence report is answered without saying whether a session
// existed, and a malformed one is a bad request rather than a silent success.
func TestPresenceRefusesWhatItCannotRead(t *testing.T) {
	browser := newBrowser(t)

	response, _ := browser.post("/auth/logout/presence", map[string]any{"active": true})
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("an anonymous presence report answered %d, want 204", response.StatusCode)
	}

	browser.login()
	request, err := http.NewRequest(http.MethodPost, browser.base+"/auth/logout/presence",
		strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", browser.base)
	response, _ = browser.do(request)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("an unreadable presence report answered %d, want 400", response.StatusCode)
	}
	if identity := browser.whoami(); identity == "anonymous" {
		t.Fatal("an unreadable report ended the session")
	}
}
