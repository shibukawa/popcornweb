package authfaste2e

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The whole OIDC round trip, over fasthttp. This is the test the package exists
// for: the transaction cookie is written by one request and consumed by
// another, the browser leaves for the provider and comes back, and the session
// the callback rotates into place is the one the next request presents.
func TestALoginCompletesOverFastHTTP(t *testing.T) {
	browser := newBrowser(t)
	if identity := browser.whoami(); identity != "anonymous" {
		t.Fatalf("a fresh browser reported %q", identity)
	}

	browser.login()

	identity := browser.whoami()
	if !strings.HasPrefix(identity, "oidc:") || identity == "oidc:" {
		t.Fatalf("whoami after login = %q, want an oidc session naming an account", identity)
	}
	// The same browser reports the same account on a second request, which is
	// what proves the session cookie the callback rotated into place is the one
	// being presented rather than a fresh anonymous one.
	if again := browser.whoami(); again != identity {
		t.Fatalf("a second request reported %q, want %q", again, identity)
	}
}

// The login endpoint sends the browser to the provider with a transaction
// cookie it will need on the way back, and the cookie is scoped to the callback
// path so an ordinary request never carries it.
func TestTheLoginRedirectCarriesAScopedTransactionCookie(t *testing.T) {
	browser := newBrowser(t).noRedirect()

	response, _ := browser.get("/auth/login")
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303", response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Host == "" || !strings.Contains(location.Path, "authorize") {
		t.Fatalf("login did not redirect to the provider: %q", response.Header.Get("Location"))
	}
	if got := location.Query().Get("code_challenge"); got == "" {
		t.Error("the authorization request carries no PKCE challenge")
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", response.Header.Get("Cache-Control"))
	}

	var transaction *http.Cookie
	for _, cookie := range response.Cookies() {
		if strings.HasSuffix(cookie.Name, "_txn") {
			transaction = cookie
		}
	}
	if transaction == nil {
		t.Fatalf("no transaction cookie was set: %v", response.Cookies())
	}
	if transaction.Path != "/auth/callback" {
		t.Errorf("transaction cookie path = %q, want the callback path", transaction.Path)
	}
	if !transaction.HttpOnly {
		t.Error("the transaction cookie is readable by script")
	}
	if transaction.SameSite != http.SameSiteLaxMode {
		t.Errorf("transaction cookie SameSite = %v, want Lax so it survives the provider redirect", transaction.SameSite)
	}
}

// The endpoints answer only the methods they serve, and say which.
func TestTheEndpointsRefuseTheWrongMethod(t *testing.T) {
	browser := newBrowser(t).noRedirect()

	response, _ := browser.post("/auth/login", nil)
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /auth/login answered %d, want 405", response.StatusCode)
	}
	// The Allow header has to survive the problem document being written, which
	// on this transport is the one thing a naive error path loses.
	if allow := response.Header.Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q, want the methods the endpoint serves", allow)
	}

	response, _ = browser.get("/auth/logout")
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /auth/logout answered %d, want 405", response.StatusCode)
	}
	if allow := response.Header.Get("Allow"); allow != "POST" {
		t.Errorf("Allow = %q, want POST", allow)
	}
}

// Logout ends the session and the browser is anonymous again.
func TestLogoutEndsTheSession(t *testing.T) {
	browser := newBrowser(t)
	browser.login()

	response, _ := browser.noRedirect().postForm("/auth/logout", "")
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want 303", response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != "/" {
		t.Errorf("logout sent the browser to %q, want the root", location)
	}
	if identity := browser.whoami(); identity != "anonymous" {
		t.Fatalf("whoami after logout = %q", identity)
	}
}

// A logout submitted from somewhere else is refused, which is what stops a
// third-party page from signing a visitor out.
func TestACrossOriginLogoutIsRefused(t *testing.T) {
	browser := newBrowser(t)
	browser.login()
	before := browser.whoami()

	request, err := http.NewRequest(http.MethodPost, browser.base+"/auth/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "https://elsewhere.example")
	response, _ := browser.do(request)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("a cross-origin logout answered %d, want 403", response.StatusCode)
	}
	if identity := browser.whoami(); identity != before {
		t.Fatalf("a refused logout changed the session: %q, was %q", identity, before)
	}
}

// A logout with no Origin and no Referer is refused too: treating an absent
// header as trust would make the check optional for anything able to omit one.
func TestALogoutWithNoOriginIsRefused(t *testing.T) {
	browser := newBrowser(t)
	browser.login()

	request, err := http.NewRequest(http.MethodPost, browser.base+"/auth/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, _ := browser.do(request)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("a logout carrying no origin answered %d, want 403", response.StatusCode)
	}
}
