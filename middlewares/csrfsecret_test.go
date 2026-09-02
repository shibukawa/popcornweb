package middlewares

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/session"
)

// csrfDeployment builds the session a project with the check turned on gets:
// one framework slot, on the cookie backend so the test needs no store.
func csrfDeployment(t *testing.T) *session.Manager {
	t.Helper()
	registry := session.NewRegistry()
	if err := registry.Register[CSRFSecret](CSRFSecretSlot, session.Private, nil,
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
	return manager
}

func csrfHandler(t *testing.T, manager *session.Manager, config CSRFConfig) http.Handler {
	t.Helper()
	check, err := CSRF(config, session.CookieOptions{Path: "/"}, http.SameSiteLaxMode, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager.Middleware(nil)(check(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
}

func htmlRequest(method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Accept", "text/html")
	return request
}

// A visitor with no login gets a secret, and it costs no server record: the
// slot rides the sealed cookie a Private slot uses while a session is anonymous.
func TestAnAnonymousVisitorGetsASecretWithoutAServerRecord(t *testing.T) {
	manager := csrfDeployment(t)
	recorder := httptest.NewRecorder()
	csrfHandler(t, manager, enabledCSRF()).ServeHTTP(recorder, htmlRequest(http.MethodGet, "/"))

	var runtime, sessionCookie *http.Cookie
	for _, cookie := range recorder.Result().Cookies() {
		switch cookie.Name {
		case pwruntime.CSRFCookieName:
			runtime = cookie
		case "pw_session":
			sessionCookie = cookie
		}
	}
	if runtime == nil || runtime.Value == "" {
		t.Fatal("no companion token cookie was written for an anonymous visitor")
	}
	if runtime.HttpOnly {
		t.Fatal("the companion cookie is HttpOnly, so the runtime cannot read it")
	}
	if sessionCookie == nil {
		t.Fatal("the secret was not written into a session slot")
	}
}

func TestSafeAPIGetDoesNotCreateSessionOrCSRFCookies(t *testing.T) {
	manager := csrfDeployment(t)
	request := httptest.NewRequest(http.MethodGet, "/api/todos", nil)
	request.Header.Set("Accept", "application/json")
	recorder := httptest.NewRecorder()
	csrfHandler(t, manager, enabledCSRF()).ServeHTTP(recorder, request)
	if cookies := recorder.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("safe API GET created browser state: %#v", cookies)
	}
}

// The token the runtime reads verifies against the secret the slot holds, which
// is the whole point of the pair.
func TestAnAnonymousTokenVerifies(t *testing.T) {
	manager := csrfDeployment(t)
	handler := csrfHandler(t, manager, enabledCSRF())

	issue := httptest.NewRecorder()
	handler.ServeHTTP(issue, htmlRequest(http.MethodGet, "/"))

	post := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	post.Header.Set("Origin", "http://"+post.Host)
	var token string
	for _, cookie := range issue.Result().Cookies() {
		post.AddCookie(cookie)
		if cookie.Name == pwruntime.CSRFCookieName {
			token = cookie.Value
		}
	}
	post.Header.Set(DefaultCSRF().Header, token)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, post)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("an issued token was refused: status = %d", recorder.Code)
	}
}

// A request presenting nothing is refused, so the check is not satisfied merely
// by the issuance that precedes it.
func TestAnUnsignedPostIsRefused(t *testing.T) {
	manager := csrfDeployment(t)
	handler := csrfHandler(t, manager, enabledCSRF())

	issue := httptest.NewRecorder()
	handler.ServeHTTP(issue, htmlRequest(http.MethodGet, "/"))

	post := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	post.Header.Set("Origin", "http://"+post.Host)
	for _, cookie := range issue.Result().Cookies() {
		post.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, post)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("a post with no token was accepted: status = %d", recorder.Code)
	}
}

// A token minted before a sign-in cannot be presented after one: rotation mints
// a fresh secret with the slot it moves.
func TestRotationInvalidatesATokenMintedBeforeIt(t *testing.T) {
	manager := csrfDeployment(t)
	handler := csrfHandler(t, manager, enabledCSRF())

	issue := httptest.NewRecorder()
	handler.ServeHTTP(issue, htmlRequest(http.MethodGet, "/"))
	var beforeToken string
	carried := issue.Result().Cookies()
	for _, cookie := range carried {
		if cookie.Name == pwruntime.CSRFCookieName {
			beforeToken = cookie.Value
		}
	}

	// A login rotates, which revokes the record the secret was in.
	login := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, cookie := range carried {
		login.AddCookie(cookie)
	}
	rotated := httptest.NewRecorder()
	manager.Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := manager.Rotate(w, r); err != nil {
			t.Fatalf("Rotate: %v", err)
		}
	})).ServeHTTP(rotated, login)

	post := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	post.Header.Set("Origin", "http://"+post.Host)
	for _, cookie := range rotated.Result().Cookies() {
		if cookie.MaxAge >= 0 && cookie.Value != "" {
			post.AddCookie(cookie)
		}
	}
	post.Header.Set(DefaultCSRF().Header, beforeToken)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, post)
	if recorder.Code == http.StatusNoContent {
		t.Fatal("a token minted before the rotation was accepted after it")
	}
}
