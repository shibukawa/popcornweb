package pwfast

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/tinybind-go/htmlbind"
	"github.com/shibukawa/tinybind-go/htmlbind/delta"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// formFragment is the shape generation emits for a component holding an unsafe
// form: the hidden field is the first child, and no author wrote it.
func formFragment() HTMLFragment {
	ops := htmlbind.Builder[struct{}]{}
	return (&htmlbind.Plan[struct{}]{Ops: []htmlbind.Op[struct{}]{
		ops.Static(`<form id="order-form" method="post" action="/orders">`),
		ops.CSRFField("_csrf"),
		ops.Static(`<button>buy</button></form>`),
	}}).Bind(struct{}{})
}

var hiddenValue = regexp.MustCompile(`name="_csrf" value="([^"]*)"`)

type shellParams struct{ Children htmlbind.Fragment }

// formShell is a document shell holding the sign-out form a signed-in page
// usually has, and declaring a scoped script the way a navigation component
// does.
func formShell() HTMLWrapper {
	ops := htmlbind.Builder[shellParams]{}
	plan := &htmlbind.Plan[shellParams]{
		Assets: []htmlbind.Asset{{
			ID: "/public/generated/shell.script.js", Type: htmlbind.AssetTypeScript,
			URL: "/public/generated/shell.script.js", Scope: "app.shell.Shell",
		}},
		Ops: []htmlbind.Op[shellParams]{
			ops.Static(`<main><form method="post" action="/auth/logout">`),
			ops.CSRFField("_csrf"),
			ops.Static(`<button>sign out</button></form>`),
			ops.Slot(func(p shellParams) htmlbind.Fragment { return p.Children }, nil),
			ops.Static(`</main>`),
		},
	}
	return plan.BindWrapper(shellParams{}, func(target *shellParams, children htmlbind.Fragment) {
		target.Children = children
	})
}

type panelParams struct{ ID string }

// formPanelPlan is a component holding an unsafe form behind an update
// boundary, which is what makes it addressable: a delta sends an operation
// for a boundary and for nothing else.
func formPanelPlan() *htmlbind.Plan[panelParams] {
	ops := htmlbind.Builder[panelParams]{}
	return &htmlbind.Plan[panelParams]{
		Boundary: &htmlbind.Boundary[panelParams]{
			ComponentID: "pwfast.test.Panel@v1",
			Attr:        "data-tb-id",
			Instance:    func(p panelParams) string { return p.ID },
			Input:       func(p panelParams) string { return delta.CanonString(p.ID) },
		},
		Ops: []htmlbind.Op[panelParams]{
			ops.Static(`<form method="post" action="/orders"`),
			ops.Attr("id", func(p panelParams) (string, bool) { return htmlbind.Escape(p.ID), true }),
			ops.BoundaryAttr(),
			ops.Static(`>`),
			ops.CSRFField("_csrf"),
			ops.Static(`<button>buy</button></form>`),
		},
	}
}

// formPanel is that component published for redraw.
func formPanel(kind string) pwruntime.UpdateReloadable {
	plan := formPanelPlan()
	return pwruntime.UpdateReloadable{
		KindID: kind,
		Render: func(_ context.Context, instanceID string, _ url.Values) (htmlbind.Fragment, error) {
			return plan.Bind(panelParams{ID: instanceID}), nil
		},
	}
}

// withCSRFCheck says whether the project the next requests are served for
// turned the check on.
func withCSRFCheck(t *testing.T, enabled bool) {
	t.Helper()
	security := pwconfig.SecurityConfig{}
	security.CSRF.Enabled = enabled
	_, restore := pwconfig.Swap(security)
	t.Cleanup(restore)
}

// firstRegion reads the markup of the first operation of an update body, the
// way the client reads it.
func firstRegion(body string) string {
	var decoded struct {
		Ops []struct {
			HTML string `json:"html"`
		} `json:"ops"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil || len(decoded.Ops) == 0 {
		return body
	}
	return decoded.Ops[0].HTML
}

// renderEntries is every way this runtime renders the page's own markup. Each
// takes the secret the check recorded on the request, or none.
//
// They are one table because they were one defect: the check recorded a secret
// and verified submissions against it, and no render was ever handed the
// token, so this build answered 500 for anything holding an unsafe form.
var renderEntries = map[string]struct {
	headers map[string]string
	handler func(r *fasthttp.RequestCtx)
	markup  func(body string) string
	status  int
}{
	"document": {
		handler: func(r *fasthttp.RequestCtx) { WriteHTMLChain(r, nil, formFragment()) },
		status:  fasthttp.StatusOK,
	},
	"fragment": {
		handler: func(r *fasthttp.RequestCtx) { WriteHTMLFragment(r, formFragment()) },
		status:  fasthttp.StatusOK,
	},
	"action response": {
		headers: map[string]string{"Pw-Render": "action", "Pw-Build": "test-build"},
		handler: func(r *fasthttp.RequestCtx) {
			// The documented answer to a rejected submission: the form again,
			// with its errors, under the status that says it was refused.
			WriteUpdate(r, fasthttp.StatusUnprocessableEntity, Replace("order-form", formFragment()))
		},
		markup: firstRegion,
		status: fasthttp.StatusUnprocessableEntity,
	},
	"redraw": {
		headers: map[string]string{
			"Pw-Render": "redraw", "Pw-Build": "test-build",
			"Pw-Kind": "fixture.entries.Panel", "Pw-Instance": "panel-1",
		},
		handler: func(r *fasthttp.RequestCtx) {
			if !RedrawComponents(r, formPanel("fixture.entries.Panel")) {
				WriteProblem(r, InternalServerError(nil))
			}
		},
		markup: firstRegion,
		status: fasthttp.StatusOK,
	},
	"navigation delta": {
		headers: map[string]string{"Pw-Render": "navigation", "Pw-Build": "test-build"},
		handler: func(r *fasthttp.RequestCtx) {
			if !ServeUpdate(r, nil, formPanelPlan().Bind(panelParams{ID: "panel-1"})) {
				WriteProblem(r, InternalServerError(nil))
			}
		},
		// A record stream carries the markup JSON-escaped, one record a line.
		markup: func(body string) string {
			for _, line := range strings.Split(body, "\n") {
				var record struct {
					HTML string `json:"html"`
				}
				if json.Unmarshal([]byte(line), &record) == nil && record.HTML != "" {
					return record.HTML
				}
			}
			return body
		},
		status: fasthttp.StatusOK,
	},
	"error page": {
		headers: map[string]string{"Accept": "text/html"},
		handler: func(r *fasthttp.RequestCtx) { WriteProblem(r, NotFound()) },
		status:  fasthttp.StatusNotFound,
	},
}

func serveEntry(t *testing.T, name, secret string) (int, string, string) {
	t.Helper()
	entry := renderEntries[name]
	withUpdateSettings(t)
	if name == "error page" {
		previousPage := pwruntime.RegisteredHTMLErrorPage()
		previousDocument := pwruntime.SwapHTMLDocument([]HTMLWrapper{formShell()})
		t.Cleanup(func() {
			pwruntime.RegisterHTMLErrorPage(previousPage)
			pwruntime.SwapHTMLDocument(previousDocument)
		})
		pwruntime.RegisterHTMLErrorPage(func(Problem) HTMLFragment { return staticFragment(`<h1>not here</h1>`) })
	}
	status, header, body := serveWith(t, func(r *fasthttp.RequestCtx) {
		pwruntime.StoreCSRFSecret(r, secret)
		entry.handler(r)
	}, entry.headers)
	if entry.markup != nil {
		body = entry.markup(body)
	}
	return status, header, body
}

func TestEveryRenderEntryCarriesThePagesToken(t *testing.T) {
	for name, entry := range renderEntries {
		t.Run(name, func(t *testing.T) {
			withCSRFCheck(t, true)
			secret, err := pwruntime.NewCSRFSecret(nil)
			if err != nil {
				t.Fatal(err)
			}
			status, _, markup := serveEntry(t, name, secret)
			if status != entry.status {
				t.Fatalf("status = %d, want %d\n%s", status, entry.status, markup)
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

// A deployment that turned the check off gets the form on every entry, as it
// does on the other build: generation writes the field whatever the setting
// says, so a render refusing it would leave such a project with no forms.
func TestEveryRenderEntryRendersWhenTheCheckIsOff(t *testing.T) {
	for name, entry := range renderEntries {
		t.Run(name, func(t *testing.T) {
			withCSRFCheck(t, false)
			status, _, markup := serveEntry(t, name, "")
			if status != entry.status {
				t.Fatalf("status = %d, want %d\n%s", status, entry.status, markup)
			}
			if match := hiddenValue.FindStringSubmatch(markup); match == nil || match[1] != "" {
				t.Errorf("want the field, empty; got %v in:\n%s", match, markup)
			}
		})
	}
}

// With the check on and no secret to be had, no entry emits a field nothing
// could verify.
func TestEveryRenderEntryStillFailsWithoutASession(t *testing.T) {
	for name := range renderEntries {
		t.Run(name, func(t *testing.T) {
			withCSRFCheck(t, true)
			_, _, markup := serveEntry(t, name, "")
			if match := hiddenValue.FindStringSubmatch(markup); match != nil {
				t.Errorf("an unverifiable field reached the response: %q", match[1])
			}
		})
	}
}

// A document names its scoped scripts, and so does the delta a navigation
// arrives as. This build renders every document buffered and wrote neither,
// so no component script ever started on it — and a navigation, by carrying no
// chain, told the client to release whatever it had.
func TestADocumentAndItsDeltaCarryTheScopeCatalog(t *testing.T) {
	withUpdateSettings(t)
	withCSRFCheck(t, false)
	const catalog = "app.shell.Shell:/public/generated/shell.script.js"
	wrappers := []HTMLWrapper{formShell()}

	status, _, body := serve(t, func(r *fasthttp.RequestCtx) {
		WriteHTMLChain(r, wrappers, staticFragment(`<h1>orders</h1>`))
	}, "/orders")
	if status != fasthttp.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	if want := `<tb-scopes value="` + catalog + `"></tb-scopes>`; !strings.HasSuffix(body, want) {
		t.Errorf("the document does not end with its scope catalog:\n%s", body)
	}

	_, header, _ := serveWith(t, func(r *fasthttp.RequestCtx) {
		if !ServeUpdate(r, wrappers, staticFragment(`<h1>orders</h1>`)) {
			t.Error("a navigation request was not answered as an update")
		}
	}, map[string]string{"Pw-Render": "navigation", "Pw-Build": "test-build"})
	if !strings.Contains(header, pwruntime.ScopeChainHeader+": "+catalog) {
		t.Errorf("the delta does not name its scope chain:\n%s", header)
	}

	// A composition with no scoped script writes no marker at all.
	_, _, plain := serve(t, func(r *fasthttp.RequestCtx) {
		WriteHTMLChain(r, nil, staticFragment(`<h1>orders</h1>`))
	}, "/orders")
	if strings.Contains(plain, "tb-scopes") {
		t.Errorf("a page with no component script carries a marker:\n%s", plain)
	}
}

// A render asks for the secret when the check did not hand the request one,
// end to end: a client that does not say it wants a page still gets the page,
// and the token in it is one the check then accepts.
func TestARenderAsksForTheSecretTheRequestWasNotHanded(t *testing.T) {
	withCSRFCheck(t, true)
	registry := session.NewRegistry()
	if err := registry.Register[CSRFSecret](CSRFSecretSlot,
		session.Private, nil, session.ResetOnRotate()); err != nil {
		t.Fatal(err)
	}
	keys, err := session.NewKeyring(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	cookie := session.CookieOptions{Name: "pwsession", Path: "/", HTTPOnly: true}
	manager, err := session.NewManager(registry, nil, session.Options{Cookie: cookie, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	check, err := CSRF(protectingEverything(), cookie, http.SameSiteLaxMode, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Building the check publishes its issuer; the next test must not ask one
	// that belongs to this test's session manager.
	t.Cleanup(func() { _, _ = CSRF(CSRFConfig{}, session.CookieOptions{}, http.SameSiteLaxMode, nil, nil) })
	handler := Compose(func(r *fasthttp.RequestCtx) {
		if string(r.Method()) == "POST" {
			r.SetStatusCode(fasthttp.StatusNoContent)
			return
		}
		WriteHTMLChain(r, nil, formFragment())
	},
		Frame{Slot: SlotSession, Middleware: Session(manager, nil)},
		Frame{Slot: SlotCSRF, Middleware: check})

	// What a client sends when it does not say it wants a page.
	status, header, body := serveRaw(t, handler, "/orders/new", "Accept: */*\r\n")
	if status != fasthttp.StatusOK {
		t.Fatalf("status = %d, want the page\n%s", status, body)
	}
	match := hiddenValue.FindStringSubmatch(body)
	if match == nil || match[1] == "" {
		t.Fatalf("the form carried no token:\n%s", body)
	}
	if got := strings.Count(header, pwruntime.CSRFCookieName+"="); got != 1 {
		t.Errorf("the response set the runtime's token cookie %d times, want once:\n%s", got, header)
	}

	// The browser posts the token back with the cookies that response set.
	var cookies []string
	for _, line := range strings.Split(header, "\r\n") {
		if value, ok := strings.CutPrefix(line, "Set-Cookie: "); ok {
			pair, _, _ := strings.Cut(value, ";")
			cookies = append(cookies, pair)
		}
	}
	form := "_csrf=" + match[1]
	answered, _, _ := serveRequest(t, handler, "POST", "/orders",
		"Content-Type: application/x-www-form-urlencoded\r\nContent-Length: "+itoa(len(form))+"\r\n"+
			"Origin: http://example.test\r\nCookie: "+strings.Join(cookies, "; ")+"\r\n", form)
	if answered != fasthttp.StatusNoContent {
		t.Fatalf("the submission was answered %d: the rendered token did not verify", answered)
	}
}
