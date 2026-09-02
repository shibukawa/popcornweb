package session

import (
	"net/http"
	"testing"
	"time"
)

// A zero-value policy must come out Secure and HttpOnly: leaving either off is
// written as AllowInsecure or ScriptReadable, never as an omission.
func TestCookiePolicyDefaultsToSecureHTTPOnly(t *testing.T) {
	cookie, _, err := normalizeCookie(CookieOptions{}, DefaultCookieName)
	if err != nil {
		t.Fatal(err)
	}
	if !cookie.Secure || !cookie.HTTPOnly {
		t.Fatalf("zero-value policy resolved to Secure=%v HTTPOnly=%v, want both true",
			cookie.Secure, cookie.HTTPOnly)
	}

	optedOut, _, err := normalizeCookie(CookieOptions{
		AllowInsecure: true, ScriptReadable: true,
	}, DefaultCookieName)
	if err != nil {
		t.Fatal(err)
	}
	if optedOut.Secure || optedOut.HTTPOnly {
		t.Fatalf("opt-outs resolved to Secure=%v HTTPOnly=%v, want both false",
			optedOut.Secure, optedOut.HTTPOnly)
	}
}

// A RecordCookie carrying only a path must still inherit the rest of the token
// cookie policy instead of dropping Secure, HttpOnly, and SameSite.
func TestPartialRecordCookieInheritsThePolicy(t *testing.T) {
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	registry := NewRegistry()
	if err := registry.Register[locale]("locale", Private, nil); err != nil {
		t.Fatal(err)
	}
	options := defaultOptions(t, c.Now)
	options.RecordCookie = CookieOptions{Path: "/app"}
	manager := testManager(t, registry, nil, options)

	response := run(manager, nil, func(_ http.ResponseWriter, r *http.Request) {
		value, _ := Value[locale](r.Context())
		if err := value.Set(locale{Tag: "ja"}); err != nil {
			t.Fatal(err)
		}
	})
	record := cookieOf(response, DefaultDataCookieName)
	if record == nil {
		t.Fatal("no record cookie was written")
	}
	if record.Path != "/app" {
		t.Fatalf("record cookie path = %q, want the configured /app", record.Path)
	}
	if !record.Secure || !record.HttpOnly {
		t.Fatalf("record cookie Secure=%v HttpOnly=%v, want both true", record.Secure, record.HttpOnly)
	}
	if record.SameSite != http.SameSiteLaxMode {
		t.Fatalf("record cookie SameSite = %v, want Lax", record.SameSite)
	}
}
