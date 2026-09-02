package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/contrib/passkey"
	"github.com/shibukawa/popcornweb/contrib/passkey/passkeytest"
	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/testutil"
)

// TestPasskeyWorksOnACookieBackedSession proves the ceremony does not depend on
// where the session lives.
//
// The two are separate pieces of state and only one of them is negotiable: a
// session may be sealed into a cookie and kept nowhere on the server, but a
// ceremony challenge must stay server-side or the response could be replayed
// against a challenge the client chose. This runs the whole enrollment and
// login with session.backend = "cookie", so the database holds no session row
// at all while the ceremony records still go through authstate.
func TestPasskeyWorksOnACookieBackedSession(t *testing.T) {
	RegisterAccounts()
	port := reservePort(t)
	origin := fmt.Sprintf("http://localhost:%d", port)

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	server := passkeyServer(t, port, origin, func(config *testutil.Config) {
		config.Update(func(session *pw.SessionConfig) {
			// The cookie backend needs no import: it stores nothing, so there
			// is no storage plugin to link.
			session.Backend = pw.SessionBackendCookie
			session.CookieStore.Name = "pw_session_data"
			// The secret is the keyring's rather than this backend's: one key
			// signs and seals everything the browser carries, so a deployment
			// rotating it does not have to find every slot that took one.
			session.Keyring.Secret = base64.StdEncoding.EncodeToString(secret)
		})
	})
	_ = server

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	browser := &ceremony{t: t, client: client, origin: origin}

	// The provider login still bootstraps the account; only its storage moved.
	if body := fetch(t, client, origin+"/mypage"); !strings.Contains(body, "Hanako Yamada") {
		t.Fatalf("provider login did not reach the protected page: %s", body)
	}

	authenticator, err := passkeytest.NewAuthenticator(passkeytest.WithOrigin(origin))
	if err != nil {
		t.Fatal(err)
	}
	var creation passkey.CreationOptions
	browser.postJSON("/auth/passkey/register/begin", nil, &creation)
	registration, err := authenticator.Create(creation)
	if err != nil {
		t.Fatal(err)
	}
	browser.post("/auth/passkey/register/finish", registration, http.StatusOK)

	browser.logout()
	if body := fetch(t, client, origin+"/"); strings.Contains(body, "Hanako Yamada") {
		t.Fatalf("the session survived the logout: %s", body)
	}

	var request passkey.RequestOptions
	browser.postJSON("/auth/passkey/login/begin", nil, &request)
	assertion, err := authenticator.Get(request)
	if err != nil {
		t.Fatal(err)
	}
	browser.post("/auth/passkey/login/finish", assertion, http.StatusOK)
	if body := fetch(t, client, origin+"/mypage"); !strings.Contains(body, "Hanako Yamada") {
		t.Fatalf("passkey login did not reach the account: %s", body)
	}

	// The session is in the browser, not the database: no row backs it.
	var sessions int
	if err := server.DB.QueryRowContext(server.Context(),
		"SELECT COUNT(*) FROM popcornweb_session").Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("cookie-backed sessions wrote %d rows, want none", sessions)
	}

	// The ceremony records are the half that must stay server-side, and they
	// are consumed rather than left behind.
	var ceremonies int
	if err := server.DB.QueryRowContext(server.Context(),
		"SELECT COUNT(*) FROM popcornweb_authstate WHERE namespace = 'auth-passkey'").Scan(&ceremonies); err != nil {
		t.Fatalf("count ceremony records: %v", err)
	}
	if ceremonies != 0 {
		t.Fatalf("%d ceremony records survived, want every one consumed", ceremonies)
	}
}
