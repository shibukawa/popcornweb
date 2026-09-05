package auth

import "testing"

func TestResolveProviderRedirectURIFromLoopbackRequest(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		host string
		want string
	}{
		{"localhost default", "", "localhost:8080", "http://localhost:8080/auth/callback"},
		{"ipv4 path", "/auth/callback", "127.0.0.1:8080", "http://127.0.0.1:8080/auth/callback"},
		{"ipv6 default", "", "[::1]:8080", "http://[::1]:8080/auth/callback"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveProviderRedirectURI(test.raw, "/auth/callback", true, "http", test.host, "auth.oidc")
			if err != nil || got != test.want {
				t.Fatalf("resolve = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestResolveProviderRedirectURIRefusesNonLoopbackHost(t *testing.T) {
	if _, err := resolveProviderRedirectURI("", "/auth/callback", true, "https", "app.example", "auth.oidc"); err == nil {
		t.Fatal("non-loopback Host was accepted")
	}
}

func TestResolveProviderRedirectURIKeepsAbsoluteConfiguration(t *testing.T) {
	const configured = "https://app.example/auth/callback"
	got, err := resolveProviderRedirectURI(configured, "/auth/callback", false, "http", "localhost:8080", "auth.oauth")
	if err != nil || got != configured {
		t.Fatalf("resolve = %q, %v; want configured URL", got, err)
	}
}
