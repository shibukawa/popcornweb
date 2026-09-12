package pwruntime

import (
	"context"
	"strings"
	"testing"
)

func newSecret(t *testing.T) string {
	t.Helper()
	secret, err := NewCSRFSecret(nil)
	if err != nil {
		t.Fatalf("NewCSRFSecret: %v", err)
	}
	return secret
}

// The emitted bytes differ on every call. That is the whole reason the token is
// masked: a stable value reflected into a compressed body beside attacker input
// is what a compression oracle extracts.
func TestCSRFTokenDiffersPerEmissionAndStillVerifies(t *testing.T) {
	secret := newSecret(t)
	first, err := CSRFToken(secret, nil)
	if err != nil {
		t.Fatalf("CSRFToken: %v", err)
	}
	second, err := CSRFToken(secret, nil)
	if err != nil {
		t.Fatalf("CSRFToken: %v", err)
	}
	if first == second {
		t.Fatal("two emissions produced identical bytes, so the pad is not fresh")
	}
	for _, token := range []string{first, second} {
		if !VerifyCSRFToken(secret, token) {
			t.Errorf("a token this secret emitted did not verify")
		}
	}
}

func TestCSRFTokenRejectsForeignAndMalformedValues(t *testing.T) {
	secret := newSecret(t)
	other := newSecret(t)
	token, err := CSRFToken(other, nil)
	if err != nil {
		t.Fatalf("CSRFToken: %v", err)
	}
	if VerifyCSRFToken(secret, token) {
		t.Error("a token from another secret verified")
	}
	// A wrong length, a wrong encoding, and an empty value must all be refused
	// without panicking, since every one of them is reachable from a caller.
	for name, presented := range map[string]string{
		"empty":      "",
		"short":      "abc",
		"not base64": strings.Repeat("!", 86),
		"right size, wrong bytes": func() string {
			flipped := []byte(token)
			flipped[len(flipped)-1] ^= 'A' ^ 'B'
			return string(flipped)
		}(),
	} {
		if VerifyCSRFToken(secret, presented) {
			t.Errorf("%s verified", name)
		}
		if got := ExpectedCSRFToken(secret, presented); got == presented && presented != "" {
			t.Errorf("%s produced a matching expected value", name)
		}
	}
}

func TestCSRFTokenRefusesAnUnusableSecret(t *testing.T) {
	for name, secret := range map[string]string{"empty": "", "wrong length": "abc"} {
		token, err := CSRFToken(secret, nil)
		if err != nil {
			t.Fatalf("%s: CSRFToken: %v", name, err)
		}
		if token != "" {
			t.Errorf("%s produced a token %q", name, token)
		}
		if VerifyCSRFToken(secret, "anything") {
			t.Errorf("%s verified something", name)
		}
	}
}

// The secret reaches the render path and the middleware through the context and
// nothing else, so an absent one has to be distinguishable from an empty one.
func TestCSRFSecretContextRoundTrip(t *testing.T) {
	if _, ok := CSRFSecret(context.Background()); ok {
		t.Error("an empty context reported a secret")
	}
	if _, ok := CSRFSecret(WithCSRFSecret(context.Background(), "")); ok {
		t.Error("an empty secret was carried")
	}
	secret := newSecret(t)
	got, ok := CSRFSecret(WithCSRFSecret(context.Background(), secret))
	if !ok || got != secret {
		t.Errorf("CSRFSecret = %q, %v", got, ok)
	}
}
