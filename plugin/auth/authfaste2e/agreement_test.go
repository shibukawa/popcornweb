package authfaste2e

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Both listeners answer the same question the same way.
//
// This is what the transport seam is for, stated as a test. The two chains are
// assembled separately and read one authentication runtime, so a difference
// here would be a difference in what a transport does with a decision rather
// than a difference of opinion about the decision — which is the only kind of
// disagreement the seam still permits, and the kind nothing else would catch.
func TestBothTransportsAnswerAlike(t *testing.T) {
	deployment := start(t)

	for _, probe := range []struct {
		name   string
		ask    func(*browser) (*http.Response, []byte)
		status int
		// headers are compared exactly on both sides. Names are matched
		// case-insensitively by net/http's reader, which is what saves this from
		// depending on how each transport canonicalises them.
		headers []string
	}{
		{
			name:    "an anonymous request to a protected path is sent to the login",
			ask:     func(b *browser) (*http.Response, []byte) { return b.get("/private") },
			status:  http.StatusSeeOther,
			headers: []string{"Location", "Cache-Control"},
		},
		{
			name: "an anonymous request for something that is not a page is refused where it stands",
			ask: func(b *browser) (*http.Response, []byte) {
				return b.fetch("/apple-touch-icon.png", imageAccept, "image")
			},
			status:  http.StatusUnauthorized,
			headers: []string{"Cache-Control", "Content-Type"},
		},
		{
			name:    "a document destination with no Accept is sent to the login too",
			ask:     func(b *browser) (*http.Response, []byte) { return b.fetch("/private", "", "document") },
			status:  http.StatusSeeOther,
			headers: []string{"Location", "Cache-Control"},
		},
		{
			name:    "a navigation to the logout endpoint is refused by method",
			ask:     func(b *browser) (*http.Response, []byte) { return b.get("/auth/logout") },
			status:  http.StatusMethodNotAllowed,
			headers: []string{"Allow", "Content-Type"},
		},
		{
			name:    "an uncorrelated callback is a bad request",
			ask:     func(b *browser) (*http.Response, []byte) { return b.get("/auth/callback?state=x&code=y") },
			status:  http.StatusBadRequest,
			headers: []string{"Content-Type"},
		},
		{
			name: "a cross-origin logout is refused",
			ask: func(b *browser) (*http.Response, []byte) {
				request, err := http.NewRequest(http.MethodPost, b.base+"/auth/logout", nil)
				if err != nil {
					b.t.Fatal(err)
				}
				request.Header.Set("Origin", "https://elsewhere.example")
				return b.do(request)
			},
			status:  http.StatusForbidden,
			headers: []string{"Content-Type"},
		},
		{
			name:    "an anonymous browser reports itself as one",
			ask:     func(b *browser) (*http.Response, []byte) { return b.get("/whoami") },
			status:  http.StatusOK,
			headers: nil,
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			fastResponse, fastBody := probe.ask(newBrowser(t).noRedirect())
			slowResponse, slowBody := probe.ask(browserAt(t, deployment.slow).noRedirect())

			if fastResponse.StatusCode != probe.status || slowResponse.StatusCode != probe.status {
				t.Fatalf("status: fasthttp %d, net/http %d, want %d",
					fastResponse.StatusCode, slowResponse.StatusCode, probe.status)
			}
			if string(fastBody) != string(slowBody) {
				t.Errorf("body:\n fasthttp %q\n net/http %q", fastBody, slowBody)
			}
			for _, name := range probe.headers {
				fast, slow := fastResponse.Header.Get(name), slowResponse.Header.Get(name)
				if fast != slow {
					t.Errorf("%s: fasthttp %q, net/http %q", name, fast, slow)
				}
				if fast == "" {
					t.Errorf("%s was not set on either", name)
				}
			}
		})
	}
}

// The login endpoint issues the same transaction cookie on both, which is the
// value the whole OIDC correlation rests on. Only its value differs, because a
// correlation key is minted per request.
func TestBothTransportsIssueTheSameTransactionCookie(t *testing.T) {
	deployment := start(t)

	fast := transactionCookie(t, newBrowser(t).noRedirect())
	slow := transactionCookie(t, browserAt(t, deployment.slow).noRedirect())

	if fast.Name != slow.Name {
		t.Errorf("name: fasthttp %q, net/http %q", fast.Name, slow.Name)
	}
	if fast.Path != slow.Path {
		t.Errorf("path: fasthttp %q, net/http %q", fast.Path, slow.Path)
	}
	if fast.MaxAge != slow.MaxAge {
		t.Errorf("max-age: fasthttp %d, net/http %d", fast.MaxAge, slow.MaxAge)
	}
	if fast.HttpOnly != slow.HttpOnly {
		t.Errorf("http-only: fasthttp %v, net/http %v", fast.HttpOnly, slow.HttpOnly)
	}
	if fast.Secure != slow.Secure {
		t.Errorf("secure: fasthttp %v, net/http %v", fast.Secure, slow.Secure)
	}
	if fast.SameSite != slow.SameSite {
		t.Errorf("same-site: fasthttp %v, net/http %v", fast.SameSite, slow.SameSite)
	}
	if fast.Value == slow.Value {
		t.Error("both listeners issued the same correlation key, which is minted per request")
	}
}

func transactionCookie(t *testing.T, b *browser) *http.Cookie {
	t.Helper()
	response, _ := b.get("/auth/login")
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("login on %s answered %d", b.base, response.StatusCode)
	}
	for _, cookie := range response.Cookies() {
		if strings.HasSuffix(cookie.Name, "_txn") {
			return cookie
		}
	}
	t.Fatalf("no transaction cookie on %s: %v", b.base, response.Cookies())
	return nil
}

// The authorization request itself is assembled by one client on both sides, so
// what a comparison proves is that neither transport lost a parameter on the
// way into the Location header.
func TestBothTransportsSendTheSameAuthorizationRequest(t *testing.T) {
	deployment := start(t)

	fast := authorizationRequest(t, newBrowser(t).noRedirect())
	slow := authorizationRequest(t, browserAt(t, deployment.slow).noRedirect())

	if fast.Host != slow.Host || fast.Path != slow.Path {
		t.Fatalf("endpoint: fasthttp %q, net/http %q", fast, slow)
	}
	fastQuery, slowQuery := fast.Query(), slow.Query()
	for _, name := range []string{"client_id", "redirect_uri", "response_type", "scope", "code_challenge_method"} {
		if fastQuery.Get(name) != slowQuery.Get(name) {
			t.Errorf("%s: fasthttp %q, net/http %q", name, fastQuery.Get(name), slowQuery.Get(name))
		}
		if fastQuery.Get(name) == "" {
			t.Errorf("%s is absent from both", name)
		}
	}
	// state and code_challenge are per-request secrets, so equality would be the
	// failure rather than the assertion.
	for _, name := range []string{"state", "code_challenge"} {
		if fastQuery.Get(name) == "" {
			t.Errorf("%s is absent", name)
		}
		if fastQuery.Get(name) == slowQuery.Get(name) {
			t.Errorf("%s was reused across two authorization requests", name)
		}
	}
}

func authorizationRequest(t *testing.T, b *browser) *url.URL {
	t.Helper()
	response, _ := b.get("/auth/login")
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("login on %s answered %d", b.base, response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return location
}
