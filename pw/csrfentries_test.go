package pw

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// formShell is a layout holding the unsafe form a signed-in page usually has:
// the sign-out control. It carries a scoped script as well, which is what a
// navigation component in a shell looks like to the scope catalog.
type formShell struct{ Children htmlbind.Fragment }

func formShellWrapper() HTMLWrapper {
	ops := htmlbind.Builder[formShell]{}
	plan := &htmlbind.Plan[formShell]{
		Assets: []htmlbind.Asset{scriptAsset("app.shell.Shell", "/public/generated/shell.script.js")},
		Ops: []htmlbind.Op[formShell]{
			ops.Static(`<main><form method="post" action="/auth/logout">`),
			ops.CSRFField("_csrf"),
			ops.Static(`<button>sign out</button></form>`),
			ops.Slot(func(p formShell) htmlbind.Fragment { return p.Children }, nil),
			ops.Static(`</main>`),
		},
	}
	return plan.BindWrapper(formShell{}, func(target *formShell, children htmlbind.Fragment) {
		target.Children = children
	})
}

// entryRequest is a request to a project with updates on. protect says whether
// that project turned the check on, and secret is what the check handed the
// request when it did.
func entryRequest(t *testing.T, method string, protect bool, secret string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, "/orders", nil)
	request.Header.Set("Accept", "text/html")
	security := SecurityConfig{}
	security.CSRF.Enabled = protect
	ctx := pwruntime.WithResources(request.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{
			reflect.TypeFor[HTMLConfig]():     updateConfig(),
			reflect.TypeFor[SecurityConfig](): security,
		},
	})
	return request.WithContext(pwruntime.WithCSRFSecret(ctx, secret))
}

// renderEntries is every way this runtime renders the page's own markup other
// than the document chain, each returning the markup it answered with.
//
// They are one table because they failed as one defect, found one entry at a
// time: the document chain was handed the token and nothing else was, so a
// form that rendered on the page answered 500 from a fragment, from the action
// response carrying its validation errors, and from a redraw with the check
// off, and took the application's error page down to a problem document.
var renderEntries = map[string]func(t *testing.T, request *http.Request) (status int, markup string){
	"fragment": func(t *testing.T, request *http.Request) (int, string) {
		recorder := httptest.NewRecorder()
		WriteHTMLFragment(recorder, request, formFragment())
		return recorder.Code, recorder.Body.String()
	},
	"action response": func(t *testing.T, request *http.Request) (int, string) {
		request.Method = http.MethodPost
		request.Header.Set("Pw-Render", "action")
		request.Header.Set("Pw-Build", updateOptions(updateConfig()).RuntimeConfig().Build)
		recorder := httptest.NewRecorder()
		// The documented answer to a rejected submission: the form again, with
		// its errors, under the status that says it was refused.
		WriteUpdate(recorder, request, http.StatusUnprocessableEntity, Replace("order-form", formFragment()))
		var body struct {
			Ops []struct {
				HTML string `json:"html"`
			} `json:"ops"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || len(body.Ops) == 0 {
			return recorder.Code, recorder.Body.String()
		}
		return recorder.Code, body.Ops[0].HTML
	},
	"redraw": func(t *testing.T, request *http.Request) (int, string) {
		request.Header.Set("Pw-Render", "redraw")
		request.Header.Set("Pw-Kind", "fixture.entries.Panel")
		request.Header.Set("Pw-Instance", "panel-1")
		request.Header.Set("Pw-Build", updateOptions(updateConfig()).RuntimeConfig().Build)
		recorder := httptest.NewRecorder()
		if !RedrawComponents(recorder, request, formComponent("fixture.entries.Panel")) {
			t.Fatal("a redraw request was not answered")
		}
		var body struct {
			Ops []struct {
				HTML string `json:"html"`
			} `json:"ops"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || len(body.Ops) == 0 {
			return recorder.Code, recorder.Body.String()
		}
		return recorder.Code, body.Ops[0].HTML
	},
	"error page": func(t *testing.T, request *http.Request) (int, string) {
		previous := registeredHTMLErrorPage()
		t.Cleanup(func() { RegisterHTMLErrorPage(previous) })
		RegisterHTMLErrorPage(func(Problem) HTMLFragment { return staticFragment(`<h1>not here</h1>`) })
		recorder := httptest.NewRecorder()
		writeHTMLProblem(recorder, request, []HTMLWrapper{formShellWrapper()}, NotFound())
		// The status is the failure's own either way, so what says whether the
		// application's page was rendered is the representation: an error page
		// whose own render failed is answered with the problem document.
		if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/html") {
			return http.StatusInternalServerError, recorder.Body.String()
		}
		return recorder.Code, recorder.Body.String()
	},
}

var wantEntryStatus = map[string]int{
	"fragment":        http.StatusOK,
	"action response": http.StatusUnprocessableEntity,
	"redraw":          http.StatusOK,
	"error page":      http.StatusNotFound,
}

func TestEveryRenderEntryCarriesThePagesToken(t *testing.T) {
	for name, render := range renderEntries {
		t.Run(name, func(t *testing.T) {
			secret, err := pwruntime.NewCSRFSecret(nil)
			if err != nil {
				t.Fatal(err)
			}
			status, markup := render(t, entryRequest(t, http.MethodGet, true, secret))
			if status != wantEntryStatus[name] {
				t.Fatalf("status = %d, want %d\n%s", status, wantEntryStatus[name], markup)
			}
			match := hiddenValue.FindStringSubmatch(markup)
			if match == nil {
				t.Fatalf("the form carried no token:\n%s", markup)
			}
			if !pwruntime.VerifyCSRFToken(secret, match[1]) {
				t.Error("the token does not verify against the session secret")
			}
		})
	}
}

// A deployment that turned the check off gets the form on every entry, for the
// reason the document gives: generation writes the field whatever the setting
// says, so a render refusing it would mean such a project cannot use a form.
func TestEveryRenderEntryRendersWhenTheCheckIsOff(t *testing.T) {
	for name, render := range renderEntries {
		t.Run(name, func(t *testing.T) {
			status, markup := render(t, entryRequest(t, http.MethodGet, false, ""))
			if status != wantEntryStatus[name] {
				t.Fatalf("status = %d, want %d\n%s", status, wantEntryStatus[name], markup)
			}
			if match := hiddenValue.FindStringSubmatch(markup); match == nil || match[1] != "" {
				t.Errorf("want the field, empty; got %v in:\n%s", match, markup)
			}
		})
	}
}

// With the check on and no secret anywhere, every entry still refuses to emit
// a field nothing can verify. Supplying the option everywhere must not have
// turned a missing session into an unprotected form.
func TestEveryRenderEntryStillFailsWithoutASession(t *testing.T) {
	for name, render := range renderEntries {
		t.Run(name, func(t *testing.T) {
			_, markup := render(t, entryRequest(t, http.MethodGet, true, ""))
			if match := hiddenValue.FindStringSubmatch(markup); match != nil {
				t.Errorf("an unverifiable field reached the response: %q", match[1])
			}
		})
	}
}

// The error document names the shell's scoped scripts, like every other
// document. Without the marker a layout's component rendered on a 404 and its
// script never started.
func TestTheErrorDocumentCarriesItsScopeCatalog(t *testing.T) {
	previous := registeredHTMLErrorPage()
	t.Cleanup(func() { RegisterHTMLErrorPage(previous) })
	RegisterHTMLErrorPage(func(Problem) HTMLFragment { return staticFragment(`<h1>not here</h1>`) })
	secret, err := pwruntime.NewCSRFSecret(nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	writeHTMLProblem(recorder, entryRequest(t, http.MethodGet, true, secret), []HTMLWrapper{formShellWrapper()}, NotFound())
	const want = `<tb-scopes value="app.shell.Shell:/public/generated/shell.script.js"></tb-scopes>`
	if !strings.HasSuffix(recorder.Body.String(), want) {
		t.Errorf("the error document does not end with its scope catalog:\n%s", recorder.Body.String())
	}
	// The marker is part of the body the length was declared for.
	if got := recorder.Header().Get("Content-Length"); got != strconv.Itoa(recorder.Body.Len()) {
		t.Errorf("Content-Length = %s for a body of %d bytes", got, recorder.Body.Len())
	}
}

// csrfChain is the two frames a project with the check on puts around a
// handler: the session, and the check below it.
func csrfChain(t *testing.T, handler http.Handler) http.Handler {
	t.Helper()
	registry := session.NewRegistry()
	if err := registry.Register[middlewares.CSRFSecret](middlewares.CSRFSecretSlot, session.Private, nil,
		session.ResetOnRotate()); err != nil {
		t.Fatal(err)
	}
	keys, err := session.ParseKeyring("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := session.NewManager(registry, nil, session.Options{
		Cookie: session.CookieOptions{Name: "pw_session", Path: "/", HTTPOnly: true},
		Keys:   keys,
	})
	if err != nil {
		t.Fatal(err)
	}
	config := middlewares.DefaultCSRF()
	config.Enabled = true
	check, err := middlewares.CSRF(config, session.CookieOptions{Path: "/"}, http.SameSiteLaxMode, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Building the check publishes its issuer, and the next test must not ask
	// one that belongs to this test's session manager.
	t.Cleanup(func() {
		off := middlewares.DefaultCSRF()
		off.Enabled = false
		_, _ = middlewares.CSRF(off, session.CookieOptions{}, http.SameSiteLaxMode, nil, nil)
	})
	return manager.Middleware(nil)(check(handler))
}

// A render asks for the secret when the check did not hand the request one.
//
// The check gives a secret to the safe requests that announce a page, so that
// an API read never touches the session. Every other client that renders a page
// reached the templates without one: curl, a Go test's http.Get, a script's
// fetch, an htmx swap. This is each of those, end to end — the page renders,
// and the token it carries is one the check then accepts.
func TestARenderAsksForTheSecretTheRequestWasNotHanded(t *testing.T) {
	handler := csrfChain(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		r = protectedRequest(r)
		if r.URL.Path == "/fragment" {
			WriteHTMLFragment(w, r, formFragment())
			return
		}
		WriteHTMLChain(w, r, nil, formFragment())
	}))
	for name, target := range map[string]string{"page": "/orders/new", "fragment": "/fragment"} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, target, nil)
			// What a client sends when it does not say it wants a page.
			request.Header.Set("Accept", "*/*")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want the page\n%s", recorder.Code, recorder.Body.String())
			}
			match := hiddenValue.FindStringSubmatch(recorder.Body.String())
			if match == nil || match[1] == "" {
				t.Fatalf("the form carried no token:\n%s", recorder.Body.String())
			}
			cookies := recorder.Result().Cookies()
			runtimeCookies := 0
			for _, cookie := range cookies {
				if cookie.Name == CSRFCookieName {
					runtimeCookies++
				}
			}
			if runtimeCookies != 1 {
				t.Errorf("the response set the runtime's token cookie %d times, want once", runtimeCookies)
			}

			// The token is real: the browser posts it back with the cookies this
			// response set, and the check lets the submission through.
			form := url.Values{"_csrf": {match[1]}}
			post := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(form.Encode()))
			post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			post.Header.Set("Origin", "http://example.com")
			for _, cookie := range cookies {
				post.AddCookie(cookie)
			}
			answered := httptest.NewRecorder()
			handler.ServeHTTP(answered, post)
			if answered.Code != http.StatusNoContent {
				t.Fatalf("the submission was answered %d: the rendered token did not verify", answered.Code)
			}
		})
	}
}

// A request that renders nothing still pays for nothing, which is what the
// check's prediction was protecting and what asking lazily must not give up.
func TestARequestThatRendersNothingStillTouchesNoSession(t *testing.T) {
	handler := csrfChain(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	request.Header.Set("Accept", "*/*")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if cookies := recorder.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("an API read created browser state: %v", cookies)
	}
}
