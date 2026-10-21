package authfaste2e

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// unpromptedPaths are what Safari asks for without being asked, with the
// cookies of the site it is on. A deployment that protects the whole site has
// them under the guard, and a browser follows whatever the guard answers.
var unpromptedPaths = []string{"/favicon.ico", "/apple-touch-icon.png", "/apple-touch-icon-precomposed.png"}

// imageAccept is the shape a browser gives the Accept of an image it fetches:
// image types first, and anything last at a low weight.
const imageAccept = "image/webp,image/avif,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"

// A logout leaves a mark that makes the next sign-in ask again, and a request
// that begins an authorization and never finishes one must not spend it.
//
// This is the failure the guard and the login endpoint produced together. The
// guard sent the icons Safari fetches for itself to the login, the browser
// followed with the mark's cookie, and the login endpoint spent the mark on a
// request nobody would ever see the end of — so the person's own click, a
// moment later, was answered silently from the provider session and the
// sign-out looked as if it had not worked.
func TestAnUnpromptedFetchNeverSpendsTheReconfirmMark(t *testing.T) {
	deployment := start(t)

	for name, origin := range map[string]string{"fasthttp": deployment.fast, "net/http": deployment.slow} {
		t.Run(name, func(t *testing.T) {
			browser := browserAt(t, origin)

			// A logout asks nothing of the session, so it leaves the mark whether
			// or not one was open.
			browser.postForm("/auth/logout", "")
			if !browser.reconfirmPending() {
				t.Fatal("logout left no reconfirm mark")
			}

			for _, path := range unpromptedPaths {
				for _, request := range []struct{ name, accept, dest string }{
					{"fetched as an image", imageAccept, "image"},
					{"asked for by a tool that wants anything", "*/*", ""},
					{"sent with no headers at all", "", ""},
				} {
					response, _ := browser.fetch(path, request.accept, request.dest)
					// The browser follows redirects, so a redirect would have ended
					// somewhere other than the path it asked for.
					if got := response.Request.URL.Path; got != path {
						t.Fatalf("%s %s was sent on to %s", path, request.name, got)
					}
					if response.StatusCode != http.StatusUnauthorized {
						t.Fatalf("%s %s answered %d, want 401", path, request.name, response.StatusCode)
					}
				}
			}
			if !browser.reconfirmPending() {
				t.Fatal("an unprompted fetch spent the reconfirm mark")
			}

			// A login page opened and left is no more a sign-in than an icon is:
			// every start still asks the provider to demand proof.
			browser.noRedirect()
			for attempt := 1; attempt <= 2; attempt++ {
				response, _ := browser.get("/auth/login")
				if response.StatusCode != http.StatusSeeOther {
					t.Fatalf("login start %d answered %d, want the provider redirect", attempt, response.StatusCode)
				}
				authorization, err := url.Parse(response.Header.Get("Location"))
				if err != nil {
					t.Fatal(err)
				}
				if prompt := authorization.Query().Get("prompt"); prompt != "select_account login" {
					t.Fatalf("login start %d carried prompt %q, want the account picker and a login", attempt, prompt)
				}
				if !browser.reconfirmPending() {
					t.Fatalf("login start %d spent the mark before any sign-in completed", attempt)
				}
			}
		})
	}
}

// The sign-in that completes ends the mark, and until it does the provider is
// made to ask: the account picker is what the mark is for.
func TestACompletedSignInEndsTheReconfirmMark(t *testing.T) {
	browser := newBrowser(t)
	browser.login()

	response, _ := browser.noRedirect().postForm("/auth/logout", "")
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want 303", response.StatusCode)
	}
	browser.client.CheckRedirect = nil
	if !browser.reconfirmPending() {
		t.Fatal("logout left no reconfirm mark")
	}

	// The provider still holds the session this logout could not reach, and
	// answers silently unless it is told not to. With the mark it shows who it
	// knows instead.
	page, body := browser.get("/auth/login")
	if page.StatusCode != http.StatusOK || !strings.Contains(string(body), "Select a development user") {
		t.Fatalf("the provider answered %d without asking who: %.200q", page.StatusCode, body)
	}
	// Asking is not completing.
	if !browser.reconfirmPending() {
		t.Fatal("the authorization request spent the mark before the sign-in completed")
	}

	landed, payload := browser.chooseAccount(page, body, "alice-subject")
	if landed.StatusCode != http.StatusOK {
		t.Fatalf("the sign-in landed on %d: %.200q", landed.StatusCode, payload)
	}
	if identity := string(payload); !strings.HasPrefix(identity, "oidc:") || identity == "oidc:" {
		t.Fatalf("the landing page reported %q, want an oidc session naming an account", identity)
	}
	if browser.reconfirmPending() {
		t.Fatal("a completed sign-in left the reconfirm mark")
	}
}
