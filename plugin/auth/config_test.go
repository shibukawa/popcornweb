package auth

import (
	"strings"
	"testing"
	"time"
)

// baseConfig is a configuration whose non-mode settings are all valid, so a
// test changes exactly the field it is about.
func baseConfig(mode string) Config {
	config := Config{
		Enabled:          true,
		Mode:             mode,
		LoginPath:        "/auth/login",
		CallbackPath:     "/auth/callback",
		LogoutPath:       "/auth/logout",
		PostLoginPath:    "/",
		RecentAuthMaxAge: 5 * time.Minute,
		Protection:       ProtectionConfig{Unauthenticated: UnauthenticatedRedirect},
		Bootstrap:        BootstrapConfig{IssueTTL: 24 * time.Hour, EnrollmentTTL: 10 * time.Minute, MaxAttempts: 5},
	}
	if mode == ModeOIDCOnly || mode == ModeOIDCPasskey {
		config.OIDC = OIDCConfig{
			Issuer: "https://issuer.example", ClientID: "client", ClientSecret: "secret",
			RedirectURL:   "https://app.example/auth/callback",
			IdentityClaim: ClaimSubject, Admission: AdmissionAuthenticated,
			LogoutScope: LogoutScopeReconfirm,
		}
	}
	if mode == ModeOAuthOnly {
		config.OAuth = OAuthConfig{
			Provider: "x", ClientID: "client", ClientSecret: "secret",
			RedirectURL:   "https://app.example/auth/callback",
			IdentityClaim: ClaimSubject, Admission: AdmissionAuthenticated,
		}
	}
	if mode == ModeOIDCPasskey || mode == ModePasskeyOnly {
		config.Passkey = PasskeyConfig{
			Path: "/auth/passkey", RPID: "app.example", RPName: "Example",
			Origins:          []string{"https://app.example"},
			UserVerification: UserVerificationRequired,
			Discoverable:     DiscoverablePreferred,
		}
		config.Recovery = RecoveryConfig{Policy: RecoveryAdministrator}
		config.Registration = RegistrationConfig{Policy: RegistrationAdministrator}
	}
	return config
}

func TestEveryModeAcceptsItsOwnValidConfiguration(t *testing.T) {
	for _, mode := range []string{ModeOIDCOnly, ModeOIDCPasskey, ModePasskeyOnly} {
		t.Run(mode, func(t *testing.T) {
			if err := baseConfig(mode).validateShape(); err != nil {
				t.Fatalf("validateShape: %v", err)
			}
		})
	}
}

func TestOIDCRedirectMayFollowTheLoopbackRequest(t *testing.T) {
	for _, redirect := range []string{"", "/auth/callback"} {
		config := baseConfig(ModeOIDCOnly)
		config.OIDC.RedirectURL = redirect
		config.OIDC.AllowLoopbackHTTP = true
		if err := config.validateShape(); err != nil {
			t.Fatalf("redirect %q: %v", redirect, err)
		}
	}
}

func TestRequestRelativeOIDCRedirectRequiresExplicitLoopbackMode(t *testing.T) {
	config := baseConfig(ModeOIDCOnly)
	config.OIDC.RedirectURL = ""
	assertInvalid(t, config, "allow_loopback_http")

	config.OIDC.RedirectURL = "/auth/callback"
	assertInvalid(t, config, "allow_loopback_http")
}

func TestPathOnlyOIDCRedirectMustMatchMountedCallback(t *testing.T) {
	config := baseConfig(ModeOIDCOnly)
	config.OIDC.RedirectURL = "/another/callback"
	config.OIDC.AllowLoopbackHTTP = true
	assertInvalid(t, config, "must match auth.callback_path")
}

// A mode reads only its own settings, so a setting it cannot honor is an error
// rather than a value that is silently ignored.
func TestModeRefusesSettingsItCannotHonor(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		mode    string
		wantSub string
	}{
		{"oidc_only rejects rp id", func(c *Config) { c.Passkey.RPID = "app.example" }, ModeOIDCOnly, "auth.passkey.rp_id"},
		{"oidc_only rejects rp name", func(c *Config) { c.Passkey.RPName = "Example" }, ModeOIDCOnly, "auth.passkey.rp_name"},
		{"oidc_only rejects origins", func(c *Config) { c.Passkey.Origins = []string{"https://app.example"} }, ModeOIDCOnly, "auth.passkey.origins"},
		{"passkey_only rejects issuer", func(c *Config) { c.OIDC.Issuer = "https://issuer.example" }, ModePasskeyOnly, "auth.oidc.issuer"},
		{"passkey_only rejects client id", func(c *Config) { c.OIDC.ClientID = "client" }, ModePasskeyOnly, "auth.oidc.client_id"},
		{"passkey_only rejects client secret", func(c *Config) { c.OIDC.ClientSecret = "secret" }, ModePasskeyOnly, "auth.oidc.client_secret"},
		{"oidc_only rejects an oauth provider", func(c *Config) { c.OAuth.Provider = "x" }, ModeOIDCOnly, "auth.oauth.provider"},
		{"oidc_only rejects an oauth client id", func(c *Config) { c.OAuth.ClientID = "client" }, ModeOIDCOnly, "auth.oauth.client_id"},
		{"passkey_only rejects an oauth client secret", func(c *Config) { c.OAuth.ClientSecret = "secret" }, ModePasskeyOnly, "auth.oauth.client_secret"},
		{"jwt_only rejects an oauth redirect", func(c *Config) { c.OAuth.RedirectURL = "https://app.example/auth/callback" }, ModeJWTOnly, "auth.oauth.redirect_url"},
		{"oauth_only rejects an issuer", func(c *Config) { c.OIDC.Issuer = "https://issuer.example" }, ModeOAuthOnly, "auth.oidc.issuer"},
		{"oauth_only rejects rp id", func(c *Config) { c.Passkey.RPID = "app.example" }, ModeOAuthOnly, "auth.passkey.rp_id"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := baseConfig(testCase.mode)
			testCase.mutate(&config)
			assertInvalid(t, config, testCase.wantSub)
		})
	}
}

// The provider login states one provider name, and everything else about the
// provider follows from the definition it selects.
func TestOAuthModeRequiresAUsableProvider(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{"missing provider", func(c *Config) { c.OAuth.Provider = "" }, "auth.oauth.provider must be set"},
		{"undefined provider", func(c *Config) { c.OAuth.Provider = "twitter" }, "is not defined"},
		{"missing client id", func(c *Config) { c.OAuth.ClientID = "" }, "client_id and client_secret"},
		{"missing client secret", func(c *Config) { c.OAuth.ClientSecret = "" }, "client_id and client_secret"},
		{"unknown admission", func(c *Config) { c.OAuth.Admission = "whoever" }, "auth.oauth.admission"},
		{"existing admission provisions", func(c *Config) {
			c.OAuth.Admission = AdmissionExisting
			c.OAuth.AutoProvision = true
		}, "requires auto_provision = false"},
		{"claim admission with no rule", func(c *Config) { c.OAuth.Admission = AdmissionClaim }, "claim.path and claim.values"},
		{"missing redirect url", func(c *Config) { c.OAuth.RedirectURL = "" }, "auth.oauth.redirect_url may be omitted"},
		{"path redirect off the callback", func(c *Config) {
			c.OAuth.RedirectURL = "/elsewhere"
			c.OAuth.AllowLoopbackHTTP = true
		}, "must match auth.callback_path"},
		// A scope list written as one space-separated string would reach the
		// provider percent-encoded as a single scope nothing grants.
		{"scopes as one string", func(c *Config) { c.OAuth.Scopes = []string{"users.read tweet.read"} }, "not a scope token"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := baseConfig(ModeOAuthOnly)
			testCase.mutate(&config)
			assertInvalid(t, config, testCase.wantSub)
		})
	}
}

// A claim name is checked against what the provider definition actually
// reports, because the alternative is a deployment that starts and then refuses
// every login. The refusal names the claims that do arrive.
func TestOAuthModeRefusesAClaimTheProviderNeverReports(t *testing.T) {
	config := baseConfig(ModeOAuthOnly)
	// X's own API reference calls the handle "username"; this package reports it
	// as preferred_username, and copying the provider's spelling is the mistake
	// worth catching at startup.
	config.OAuth.IdentityClaim = "username"
	assertInvalid(t, config, "is not reported by provider")
	assertInvalid(t, config, "preferred_username")

	config = baseConfig(ModeOAuthOnly)
	config.OAuth.Admission = AdmissionRegistered
	config.OAuth.RegisteredClaims = []string{"email"}
	assertInvalid(t, config, "auth.oauth.registered_claims")

	// The claims the definition does report are accepted.
	config = baseConfig(ModeOAuthOnly)
	config.OAuth.IdentityClaim = "preferred_username"
	if err := config.validate(); err != nil {
		t.Fatalf("a reported claim was refused: %v", err)
	}
}

// policy:provider-session-scope already says a mode reaching no provider session
// refuses the settings that claim to end one. Only a typed value is refused;
// every mode binds the default.
func TestLogoutScopeIsRefusedWhereNoProviderSessionExists(t *testing.T) {
	for _, mode := range []string{ModeOAuthOnly, ModePasskeyOnly} {
		t.Run(mode, func(t *testing.T) {
			config := baseConfig(mode)
			if err := config.validate(); err != nil {
				t.Fatalf("the default scope was refused: %v", err)
			}
			config.OIDC.LogoutScope = LogoutScopeGlobal
			assertInvalid(t, config, "auth.oidc.logout_scope")

			config = baseConfig(mode)
			config.OIDC.AllowGlobalLogoutRequest = true
			assertInvalid(t, config, "allow_global_logout_request")
		})
	}
	// The OIDC modes keep both, and jwt_only reads neither because it mounts no
	// logout at all.
	config := baseConfig(ModeOIDCOnly)
	config.OIDC.LogoutScope = LogoutScopeGlobal
	config.OIDC.AllowGlobalLogoutRequest = true
	if err := config.validate(); err != nil {
		t.Fatalf("an OIDC deployment was refused a global sign-out: %v", err)
	}
}

// The shared-device mode couples settings a plain OAuth provider cannot honor:
// there is no global sign-out and no way to demand a fresh login, so only the
// local half would happen while the configuration read as the whole mode.
func TestSharedDeviceIsRefusedUnderTheProviderLoginThatCannotHonorIt(t *testing.T) {
	config := baseConfig(ModeOAuthOnly)
	config.SharedDevice = true
	assertInvalid(t, config, "auth.shared_device is not available")
}

func TestPasskeyModesRequireRelyingPartyRegistration(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{"missing rp id", func(c *Config) { c.Passkey.RPID = "" }, "auth.passkey.rp_id"},
		{"ip literal rp id", func(c *Config) {
			c.Passkey.RPID = "127.0.0.1"
			c.Passkey.Origins = []string{"http://127.0.0.1:8080"}
		}, "never an IP address"},
		{"missing rp name", func(c *Config) { c.Passkey.RPName = "" }, "auth.passkey.rp_name"},
		{"missing origins", func(c *Config) { c.Passkey.Origins = nil }, "auth.passkey.origins"},
		{"http origin outside loopback", func(c *Config) { c.Passkey.Origins = []string{"http://app.example"} }, "must be https"},
		{"origin outside rp id", func(c *Config) { c.Passkey.Origins = []string{"https://other.example"} }, "outside auth.passkey.rp_id"},
		{"origin with a path", func(c *Config) { c.Passkey.Origins = []string{"https://app.example/login"} }, "bare scheme and host"},
		{"unknown user verification", func(c *Config) { c.Passkey.UserVerification = "maybe" }, "auth.passkey.user_verification"},
		{"unknown discoverable", func(c *Config) { c.Passkey.Discoverable = "sometimes" }, "auth.passkey.discoverable"},
		{"relative passkey path", func(c *Config) { c.Passkey.Path = "auth/passkey" }, "auth.passkey.path"},
		{"zero recent auth window", func(c *Config) { c.RecentAuthMaxAge = 0 }, "auth.recent_auth_max_age"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := baseConfig(ModePasskeyOnly)
			testCase.mutate(&config)
			assertInvalid(t, config, testCase.wantSub)
		})
	}
}

// Loopback development is the case a developer hits first, so it must pass
// without TLS while an IP literal still fails.
func TestLoopbackDevelopmentOriginIsAccepted(t *testing.T) {
	config := baseConfig(ModePasskeyOnly)
	config.Passkey.RPID = "localhost"
	config.Passkey.Origins = []string{"http://localhost:8080"}
	if err := config.validateShape(); err != nil {
		t.Fatalf("validateShape: %v", err)
	}

	config.Passkey.RPID = "app.localhost"
	config.Passkey.Origins = []string{"http://app.localhost:8080"}
	if err := config.validateShape(); err != nil {
		t.Fatalf("subdomain of localhost: %v", err)
	}
}

// passkey_only has no provider to fall back on, so both lifecycle policies must
// be chosen rather than defaulted.
func TestPasskeyOnlyRequiresExplicitLifecyclePolicies(t *testing.T) {
	config := baseConfig(ModePasskeyOnly)
	config.Registration.Policy = ""
	assertInvalid(t, config, "auth.registration.policy is required")

	config = baseConfig(ModePasskeyOnly)
	config.Recovery.Policy = ""
	assertInvalid(t, config, "auth.recovery.policy is required")

	config = baseConfig(ModePasskeyOnly)
	config.Recovery.Policy = RecoveryOIDC
	assertInvalid(t, config, "needs an identity provider")
}

// oidc_passkey still has to name a recovery authority, because a lost passkey
// is not automatically recoverable just because a provider exists.
func TestOIDCPasskeyRequiresRecoveryButNotRegistration(t *testing.T) {
	config := baseConfig(ModeOIDCPasskey)
	config.Registration.Policy = ""
	if err := config.validateShape(); err != nil {
		t.Fatalf("registration may default under oidc_passkey: %v", err)
	}

	config = baseConfig(ModeOIDCPasskey)
	config.Recovery.Policy = ""
	assertInvalid(t, config, "auth.recovery.policy is required")
}

func TestBootstrapSettingsAreBoundedWhenAnIssuedCredentialOpensEnrollment(t *testing.T) {
	for _, policy := range []string{RegistrationAdministrator, RegistrationInvite} {
		t.Run(policy, func(t *testing.T) {
			for _, testCase := range []struct {
				name    string
				mutate  func(*Config)
				wantSub string
			}{
				{"credential ttl", func(c *Config) { c.Bootstrap.IssueTTL = 0 }, "auth.bootstrap.issue_ttl"},
				{"session ttl", func(c *Config) { c.Bootstrap.EnrollmentTTL = 0 }, "auth.bootstrap.enrollment_ttl"},
				{"max attempts", func(c *Config) { c.Bootstrap.MaxAttempts = 0 }, "auth.bootstrap.max_attempts"},
			} {
				t.Run(testCase.name, func(t *testing.T) {
					config := baseConfig(ModePasskeyOnly)
					config.Registration.Policy = policy
					testCase.mutate(&config)
					assertInvalid(t, config, testCase.wantSub)
				})
			}
		})
	}

	// A policy that issues no credential does not need the bounds.
	config := baseConfig(ModePasskeyOnly)
	config.Registration.Policy = RegistrationDisabled
	config.Bootstrap = BootstrapConfig{}
	if err := config.validateShape(); err != nil {
		t.Fatalf("disabled registration needs no bootstrap bounds: %v", err)
	}
}

func TestUnknownModeIsRefused(t *testing.T) {
	config := baseConfig(ModeOIDCOnly)
	config.Mode = "password"
	assertInvalid(t, config, "auth.mode must be")
}

func TestEveryModeIsServed(t *testing.T) {
	for _, mode := range []string{ModeOIDCOnly, ModeOIDCPasskey, ModePasskeyOnly} {
		if err := baseConfig(mode).validate(); err != nil {
			t.Fatalf("%s validate: %v", mode, err)
		}
	}
}

// The ceremony paths hang off one setting, and only passkey_only carries the
// bootstrap endpoint.
func TestMountedCeremonyPathsFollowTheMode(t *testing.T) {
	if paths := baseConfig(ModeOIDCOnly).passkeyPaths(); len(paths) != 0 {
		t.Fatalf("oidc_only mounted %v", paths)
	}
	oidcPasskey := baseConfig(ModeOIDCPasskey).passkeyPaths()
	if len(oidcPasskey) != 4 {
		t.Fatalf("oidc_passkey mounted %d paths, want 4", len(oidcPasskey))
	}
	for _, want := range []string{
		"/auth/passkey/login/begin", "/auth/passkey/login/finish",
		"/auth/passkey/register/begin", "/auth/passkey/register/finish",
	} {
		if _, ok := oidcPasskey[want]; !ok {
			t.Fatalf("oidc_passkey did not mount %s", want)
		}
	}
	if _, ok := oidcPasskey["/auth/passkey/bootstrap"]; ok {
		t.Fatal("oidc_passkey mounted the bootstrap endpoint, which needs no issued credential")
	}
	if _, ok := baseConfig(ModePasskeyOnly).passkeyPaths()["/auth/passkey/bootstrap"]; !ok {
		t.Fatal("passkey_only did not mount the bootstrap endpoint")
	}
}

func assertInvalid(t *testing.T, config Config, wantSubstring string) {
	t.Helper()
	err := config.validateShape()
	if err == nil {
		t.Fatalf("validateShape accepted a configuration that should fail; wanted %q", wantSubstring)
	}
	if !strings.Contains(err.Error(), wantSubstring) {
		t.Fatalf("validateShape error = %q, want it to mention %q", err, wantSubstring)
	}
}
