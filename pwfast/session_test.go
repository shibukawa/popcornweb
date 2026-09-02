package pwfast

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

type visitCount struct {
	Seen int `json:"seen"`
}

// newManager builds a manager over the in-memory store, which is what a
// dev-volatile deployment uses and what keeps this test from needing a database.
func newManager(t *testing.T) *session.Manager {
	t.Helper()
	registry := session.NewRegistry()
	if err := registry.Register[visitCount]("visits", session.ServerOnly, nil); err != nil {
		t.Fatal(err)
	}
	keys, err := session.NewKeyring(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := session.NewManager(registry, session.NewMemoryStore(time.Now), session.Options{
		TTL:    time.Hour,
		Cookie: session.CookieOptions{Name: "pwsession", Path: "/", HTTPOnly: true, SameSite: http.SameSiteLaxMode},
		Keys:   keys,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

// A session is the last thing that should exist twice, so what is asserted is
// that the rules come from the session package rather than from this half: the
// same manager, driven over this transport, mints a token, writes it as a
// cookie, and reads back the value it stored when the browser returns it.
func TestASessionSurvivesARoundTripOverThisTransport(t *testing.T) {
	manager := newManager(t)
	handler := Compose(func(r *fasthttp.RequestCtx) {
		handle, ok := session.Value[visitCount](r)
		if !ok {
			_, _ = r.WriteString("no session slot")
			return
		}
		current, _ := handle.Get()
		current.Seen++
		if err := handle.Set(current); err != nil {
			_, _ = r.WriteString("set failed: " + err.Error())
			return
		}
		_, _ = r.WriteString("seen " + itoa(current.Seen))
	}, Frame{Slot: SlotSession, Name: "session", Middleware: Session(manager, nil)})

	_, header, body := serve(t, handler, "/")
	if body != "seen 1" {
		t.Fatalf("first visit answered %q", body)
	}
	cookie := sessionCookie(t, header)
	if cookie == "" {
		t.Fatalf("no session cookie was set:\n%s", header)
	}

	// The browser returns the token, and the record it names must be the one
	// the first request wrote.
	_, _, body = serveRaw(t, handler, "/", "Cookie: "+cookie+"\r\n")
	if body != "seen 2" {
		t.Errorf("second visit answered %q, want the stored count", body)
	}
}

// A nil manager installs nothing rather than an inert frame, because a session
// frame that silently does nothing is a control that looks installed.
func TestADisabledSessionInstallsNoFrame(t *testing.T) {
	handler := Compose(func(r *fasthttp.RequestCtx) {
		if _, ok := session.Value[visitCount](r); ok {
			_, _ = r.WriteString("a session appeared")
			return
		}
		_, _ = r.WriteString("no session")
	}, Frame{Slot: SlotSession, Middleware: Session(nil, nil)})

	if _, header, body := serve(t, handler, "/"); body != "no session" || strings.Contains(header, "pwsession") {
		t.Errorf("a disabled session left something behind: %q\n%s", body, header)
	}
}

// The cookie attributes are the manager's, translated rather than reinvented,
// and the two that matter are the two a browser enforces.
func TestTheSessionCookieCarriesItsAttributes(t *testing.T) {
	manager := newManager(t)
	handler := Compose(func(r *fasthttp.RequestCtx) {
		handle, _ := session.Value[visitCount](r)
		_ = handle.Set(visitCount{Seen: 1})
	}, Frame{Slot: SlotSession, Middleware: Session(manager, nil)})

	_, header, _ := serve(t, handler, "/")
	lowered := strings.ToLower(header)
	if !strings.Contains(lowered, "httponly") {
		t.Errorf("the session cookie was readable by script:\n%s", header)
	}
	if !strings.Contains(lowered, "path=/") {
		t.Errorf("the session cookie carried no path:\n%s", header)
	}
}

func sessionCookie(t *testing.T, header string) string {
	t.Helper()
	for _, line := range strings.Split(header, "\r\n") {
		if !strings.HasPrefix(strings.ToLower(line), "set-cookie:") {
			continue
		}
		value := strings.TrimSpace(line[len("set-cookie:"):])
		if name, _, _ := strings.Cut(value, ";"); strings.HasPrefix(name, "pwsession=") {
			return name
		}
	}
	return ""
}
