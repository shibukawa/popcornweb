package pwfast

import (
	"net/http"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// csrfChain builds session and CSRF together, which is the only order they work
// in: the check reads a secret out of a session slot, so the session frame has
// to have run.
func csrfChain(t *testing.T, config CSRFConfig) fasthttp.RequestHandler {
	t.Helper()
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
	manager, err := session.NewManager(registry, nil, session.Options{
		Cookie: cookie,
		Keys:   keys,
	})
	if err != nil {
		t.Fatal(err)
	}
	check, err := CSRF(config, cookie, http.SameSiteLaxMode, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return Compose(func(r *fasthttp.RequestCtx) { _, _ = r.WriteString("handled") },
		Frame{Slot: SlotSession, Middleware: Session(manager, nil)},
		Frame{Slot: SlotCSRF, Middleware: check})
}

func protectingEverything() CSRFConfig {
	config := CSRFConfig{Enabled: true, Include: []string{"/**"}}
	return config
}

// A cross-site post is the attack this check exists for, and the origin
// comparison is what refuses it — before any session is allocated.
func TestAPostFromAnotherOriginIsRefused(t *testing.T) {
	handler := csrfChain(t, protectingEverything())
	status, _, _ := serveRequest(t, handler, "POST", "/act",
		"Origin: https://attacker.example\r\nContent-Length: 0\r\n", "")
	if status == fasthttp.StatusOK {
		t.Error("a cross-site post was handled")
	}
	if status != fasthttp.StatusForbidden {
		t.Errorf("status = %d, want 403", status)
	}
}

// A post carrying neither Origin nor Referer is refused rather than allowed,
// which is the direction that matters: an attacker controls whether those
// headers are sent far more easily than what they contain.
func TestAPostWithNoOriginEvidenceIsRefused(t *testing.T) {
	handler := csrfChain(t, protectingEverything())
	if status, _, _ := serveForm(t, handler, "/act", "x=1"); status != fasthttp.StatusForbidden {
		t.Errorf("status = %d, want 403", status)
	}
}

// A safe request is let through, and one that negotiates HTML is given the
// companion cookie so the page it renders can carry a token.
func TestASafeHTMLRequestIsGivenTheRuntimeCookie(t *testing.T) {
	handler := csrfChain(t, protectingEverything())
	status, header, body := serveRaw(t, handler, "/form", "Accept: text/html\r\n")
	if status != fasthttp.StatusOK || body != "handled" {
		t.Fatalf("a safe request answered %d %q", status, body)
	}
	if !strings.Contains(header, "pw_csrf") {
		t.Errorf("no companion cookie was issued:\n%s", header)
	}
}

// A safe request that does not negotiate HTML must not allocate session state
// merely in case the handler might render a form.
func TestASafeAPIRequestAllocatesNothing(t *testing.T) {
	handler := csrfChain(t, protectingEverything())
	_, header, _ := serveRaw(t, handler, "/api/items", "Accept: application/json\r\n")
	if strings.Contains(header, "pw_csrf") {
		t.Errorf("an API read was given a CSRF cookie:\n%s", header)
	}
}

// A path outside the configured scope is not checked at all, and exclude wins
// over include — the precedence is shared so the two transports cannot read one
// policy two ways.
func TestAPathOutsideTheScopeIsNotChecked(t *testing.T) {
	config := CSRFConfig{Enabled: true, Include: []string{"/**"}, Exclude: []string{"/webhooks/**"}}
	handler := csrfChain(t, config)
	if status, _, body := serveForm(t, handler, "/webhooks/stripe", "x=1"); status != fasthttp.StatusOK || body != "handled" {
		t.Errorf("an excluded path was checked: %d %q", status, body)
	}
	if status, _, _ := serveForm(t, handler, "/act", "x=1"); status != fasthttp.StatusForbidden {
		t.Errorf("an included path was not checked: %d", status)
	}
}

// A disabled check installs no frame rather than an inert one.
func TestADisabledCheckInstallsNoFrame(t *testing.T) {
	handler := csrfChain(t, CSRFConfig{Enabled: false, Include: []string{"/**"}})
	if status, _, body := serveForm(t, handler, "/act", "x=1"); status != fasthttp.StatusOK || body != "handled" {
		t.Errorf("a disabled check still refused: %d %q", status, body)
	}
}

// A path that cannot be matched unambiguously is refused, because it could
// select a different routed target than the one the policy decided about.
func TestAnAmbiguousPathIsRefused(t *testing.T) {
	handler := csrfChain(t, protectingEverything())
	for _, target := range []string{"/act/../admin", "/act//deep"} {
		if status, _, _ := serveForm(t, handler, target, "x=1"); status != fasthttp.StatusForbidden {
			t.Errorf("%s answered %d, want 403", target, status)
		}
	}
}

// The check has to accept as well as refuse. Without this, an implementation
// that refused everything would pass every test above it.
func TestASameOriginPostCarryingItsTokenIsAccepted(t *testing.T) {
	handler := csrfChain(t, protectingEverything())

	// A page load establishes the session and hands the runtime its token.
	_, header, _ := serveRaw(t, handler, "/form",
		"Accept: text/html\r\n")
	// Every cookie goes back, the way a browser returns them. Naming only two
	// is what an earlier version of this test did, and it failed: while the
	// visitor is anonymous the CSRF secret rides the sealed record cookie, so
	// dropping that one silently mints a fresh secret and the token presented
	// against the old one is correctly refused.
	returned := allCookies(header)
	token := strings.TrimPrefix(cookieFrom(header, pwruntime.CSRFCookieName),
		pwruntime.CSRFCookieName+"=")
	if returned == "" || token == "" {
		t.Fatalf("the page load issued %q", header)
	}

	status, _, body := serveRequest(t, handler, "POST", "/act",
		"Origin: http://example.test\r\n"+
			"Cookie: "+returned+"\r\n"+
			pwruntime.CSRFHeaderName+": "+token+"\r\n"+
			"Content-Length: 0\r\n", "")

	if status != fasthttp.StatusOK || body != "handled" {
		t.Errorf("a legitimate post answered %d %q", status, body)
	}
}

func cookieFrom(header, name string) string {
	for _, line := range strings.Split(header, "\r\n") {
		if !strings.HasPrefix(strings.ToLower(line), "set-cookie:") {
			continue
		}
		value := strings.TrimSpace(line[len("set-cookie:"):])
		if pair, _, _ := strings.Cut(value, ";"); strings.HasPrefix(pair, name+"=") {
			return pair
		}
	}
	return ""
}

// allCookies collects every cookie a response set, joined the way a browser
// sends them back.
func allCookies(header string) string {
	var pairs []string
	for _, line := range strings.Split(header, "\r\n") {
		if !strings.HasPrefix(strings.ToLower(line), "set-cookie:") {
			continue
		}
		value := strings.TrimSpace(line[len("set-cookie:"):])
		if pair, _, _ := strings.Cut(value, ";"); pair != "" {
			pairs = append(pairs, pair)
		}
	}
	return strings.Join(pairs, "; ")
}
