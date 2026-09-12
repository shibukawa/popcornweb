package passkeytest_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/shibukawa/popcornweb/contrib/passkey"
	"github.com/shibukawa/popcornweb/contrib/passkey/passkeytest"
)

const (
	testRPID   = "example.com"
	testOrigin = "https://example.com"
)

func newRelyingParty(t *testing.T) *passkey.RelyingParty {
	t.Helper()
	rp, err := passkey.New(passkey.Config{
		RPID: testRPID, RPName: "Example", Origins: []string{testOrigin},
	})
	if err != nil {
		t.Fatalf("passkey.New: %v", err)
	}
	return rp
}

func testUser() passkey.User {
	return passkey.User{ID: []byte("user-handle-1"), Name: "alice", DisplayName: "Alice"}
}

// register runs a whole registration ceremony through the relying party and the
// authenticator, exchanging only the JSON a browser would exchange.
func register(t *testing.T, rp *passkey.RelyingParty, a *passkeytest.Authenticator) passkey.CredentialRecord {
	t.Helper()
	creation, state, err := rp.BeginRegistration(testUser(), passkey.RegistrationOptions{RequireUserVerification: true})
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	response, err := a.Create(creation)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	result, err := rp.FinishRegistration(state, response)
	if err != nil {
		t.Fatalf("FinishRegistration: %v", err)
	}
	return result.Credential
}

func authenticate(t *testing.T, rp *passkey.RelyingParty, a *passkeytest.Authenticator, credential passkey.CredentialRecord) (passkey.AuthenticationResult, error) {
	t.Helper()
	request, state, err := rp.BeginAuthentication(nil, passkey.AuthenticationOptions{RequireUserVerification: true})
	if err != nil {
		t.Fatalf("BeginAuthentication: %v", err)
	}
	response, err := a.Get(request)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return rp.FinishAuthentication(state, response, credential)
}

func TestRegistrationAndAuthenticationRoundTrip(t *testing.T) {
	rp := newRelyingParty(t)
	authenticator, err := passkeytest.NewAuthenticator()
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}

	credential := register(t, rp, authenticator)
	if credential.Algorithm != passkey.ES256 {
		t.Fatalf("algorithm = %d, want %d", credential.Algorithm, passkey.ES256)
	}
	if !bytes.Equal(credential.UserHandle, testUser().ID) {
		t.Fatalf("user handle = %q, want %q", credential.UserHandle, testUser().ID)
	}
	if !credential.BackupEligible {
		t.Fatal("backup eligible = false, want true for the default authenticator")
	}

	result, err := authenticate(t, rp, authenticator, credential)
	if err != nil {
		t.Fatalf("FinishAuthentication: %v", err)
	}
	if result.CounterRisk {
		t.Fatal("counter risk on a first assertion")
	}
	if result.SignCount != 1 {
		t.Fatalf("sign count = %d, want 1", result.SignCount)
	}

	// A second assertion must advance the counter again.
	credential.SignCount = result.SignCount
	second, err := authenticate(t, rp, authenticator, credential)
	if err != nil {
		t.Fatalf("second FinishAuthentication: %v", err)
	}
	if second.SignCount != 2 || second.CounterRisk {
		t.Fatalf("second assertion = %+v, want sign count 2 without risk", second)
	}
}

func TestRegistrationFaultsAreRejected(t *testing.T) {
	cases := []struct {
		fault passkeytest.Fault
		want  error
	}{
		{passkeytest.FaultOrigin, passkey.ErrOrigin},
		{passkeytest.FaultRPID, passkey.ErrRPID},
		{passkeytest.FaultChallenge, passkey.ErrChallenge},
		{passkeytest.FaultUserPresence, passkey.ErrFlags},
		{passkeytest.FaultUserVerification, passkey.ErrFlags},
		{passkeytest.FaultBackupState, passkey.ErrFlags},
		{passkeytest.FaultAlgorithm, passkey.ErrAlgorithm},
	}
	for _, testCase := range cases {
		t.Run(string(testCase.fault), func(t *testing.T) {
			rp := newRelyingParty(t)
			authenticator, err := passkeytest.NewAuthenticator(passkeytest.WithFault(testCase.fault))
			if err != nil {
				t.Fatalf("NewAuthenticator: %v", err)
			}
			creation, state, err := rp.BeginRegistration(testUser(), passkey.RegistrationOptions{RequireUserVerification: true})
			if err != nil {
				t.Fatalf("BeginRegistration: %v", err)
			}
			response, err := authenticator.Create(creation)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if _, err := rp.FinishRegistration(state, response); !errors.Is(err, testCase.want) {
				t.Fatalf("FinishRegistration error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestSeedReproducesCredentialsButNotSignatures(t *testing.T) {
	first := registerWithSeed(t, "shared-seed")
	second := registerWithSeed(t, "shared-seed")
	if !bytes.Equal(first.ID, second.ID) {
		t.Fatal("seeded credential IDs differ")
	}
	if !bytes.Equal(first.PublicKeyX, second.PublicKeyX) || !bytes.Equal(first.PublicKeyY, second.PublicKeyY) {
		t.Fatal("seeded public keys differ")
	}

	other := registerWithSeed(t, "another-seed")
	if bytes.Equal(first.ID, other.ID) {
		t.Fatal("different seeds produced the same credential ID")
	}
}

func registerWithSeed(t *testing.T, seed string) passkey.CredentialRecord {
	t.Helper()
	authenticator, err := passkeytest.NewAuthenticator(passkeytest.WithSeed(seed))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return register(t, newRelyingParty(t), authenticator)
}

func TestAllowCredentialsSelectsAmongStoredCredentials(t *testing.T) {
	rp := newRelyingParty(t)
	first, err := passkeytest.NewAuthenticator(passkeytest.WithSeed("device-one"))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	second, err := passkeytest.NewAuthenticator(passkeytest.WithSeed("device-two"))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	firstCredential := register(t, rp, first)
	secondCredential := register(t, rp, second)
	if bytes.Equal(firstCredential.ID, secondCredential.ID) {
		t.Fatal("two devices produced one credential ID")
	}

	// Each device answers only for the credential it holds, which is what a
	// relying party relies on when it narrows a ceremony with allowCredentials.
	for _, device := range []struct {
		name          string
		authenticator *passkeytest.Authenticator
		credential    passkey.CredentialRecord
		other         passkey.CredentialRecord
	}{
		{"first", first, firstCredential, secondCredential},
		{"second", second, secondCredential, firstCredential},
	} {
		t.Run(device.name, func(t *testing.T) {
			request, state, err := rp.BeginAuthentication(nil, passkey.AuthenticationOptions{
				RequireUserVerification: true,
				AllowCredentials: []passkey.CredentialDescriptor{
					descriptor(device.credential.ID),
				},
			})
			if err != nil {
				t.Fatalf("BeginAuthentication: %v", err)
			}
			response, err := device.authenticator.Get(request)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if _, err := rp.FinishAuthentication(state, response, device.credential); err != nil {
				t.Fatalf("FinishAuthentication: %v", err)
			}

			// The other device cannot answer for a credential it never stored.
			otherRequest, _, err := rp.BeginAuthentication(nil, passkey.AuthenticationOptions{
				RequireUserVerification: true,
				AllowCredentials:        []passkey.CredentialDescriptor{descriptor(device.other.ID)},
			})
			if err != nil {
				t.Fatalf("BeginAuthentication: %v", err)
			}
			if _, err := device.authenticator.Get(otherRequest); !errors.Is(err, passkeytest.ErrNoCredential) {
				t.Fatalf("Get error = %v, want %v", err, passkeytest.ErrNoCredential)
			}
		})
	}
}

func descriptor(id []byte) passkey.CredentialDescriptor {
	return passkey.CredentialDescriptor{
		Type: "public-key",
		ID:   base64.RawURLEncoding.EncodeToString(id),
	}
}

func TestExcludeCredentialsRefusesSecondRegistration(t *testing.T) {
	rp := newRelyingParty(t)
	authenticator, err := passkeytest.NewAuthenticator()
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	credential := register(t, rp, authenticator)

	creation, _, err := rp.BeginRegistration(testUser(), passkey.RegistrationOptions{
		RequireUserVerification: true,
		ExcludeCredentials:      []passkey.CredentialDescriptor{descriptor(credential.ID)},
	})
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	if _, err := authenticator.Create(creation); !errors.Is(err, passkeytest.ErrExcluded) {
		t.Fatalf("Create error = %v, want %v", err, passkeytest.ErrExcluded)
	}
}

// A non-discoverable credential omits the user handle, which is the only
// difference a relying party observes between the two kinds.
func TestNonDiscoverableCredentialOmitsUserHandle(t *testing.T) {
	rp := newRelyingParty(t)
	authenticator, err := passkeytest.NewAuthenticator(passkeytest.WithDiscoverable(false))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	credential := register(t, rp, authenticator)

	request, state, err := rp.BeginAuthentication(credential.UserHandle, passkey.AuthenticationOptions{
		RequireUserVerification: true,
		AllowCredentials:        []passkey.CredentialDescriptor{descriptor(credential.ID)},
	})
	if err != nil {
		t.Fatalf("BeginAuthentication: %v", err)
	}
	response, err := authenticator.Get(request)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if response.Response.UserHandle != "" {
		t.Fatalf("user handle = %q, want empty for a non-discoverable credential", response.Response.UserHandle)
	}
	if _, err := rp.FinishAuthentication(state, response, credential); err != nil {
		t.Fatalf("FinishAuthentication: %v", err)
	}

	// Discoverable authentication has no account to name, so it must refuse the
	// same credential.
	discoverableRequest, discoverableState, err := rp.BeginAuthentication(nil, passkey.AuthenticationOptions{RequireUserVerification: true})
	if err != nil {
		t.Fatalf("BeginAuthentication: %v", err)
	}
	if _, err := authenticator.Get(discoverableRequest); !errors.Is(err, passkeytest.ErrNoCredential) {
		t.Fatalf("Get error = %v, want %v", err, passkeytest.ErrNoCredential)
	}
	_ = discoverableState
}

func TestCredentialsReportStoredState(t *testing.T) {
	rp := newRelyingParty(t)
	authenticator, err := passkeytest.NewAuthenticator()
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	if len(authenticator.Credentials()) != 0 {
		t.Fatal("a fresh authenticator reported a credential")
	}
	credential := register(t, rp, authenticator)
	if _, err := authenticate(t, rp, authenticator, credential); err != nil {
		t.Fatalf("FinishAuthentication: %v", err)
	}

	stored := authenticator.Credentials()
	if len(stored) != 1 {
		t.Fatalf("stored credentials = %d, want 1", len(stored))
	}
	if !bytes.Equal(stored[0].ID, credential.ID) {
		t.Fatal("stored credential ID does not match the registered one")
	}
	if stored[0].RPID != testRPID {
		t.Fatalf("stored RP ID = %q, want %q", stored[0].RPID, testRPID)
	}
	if stored[0].SignCount != 1 {
		t.Fatalf("stored sign count = %d, want 1", stored[0].SignCount)
	}
}

func TestUnusableOptionsAreRefused(t *testing.T) {
	authenticator, err := passkeytest.NewAuthenticator()
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	if _, err := authenticator.Create(passkey.CreationOptions{}); !errors.Is(err, passkeytest.ErrOptions) {
		t.Fatalf("Create error = %v, want %v", err, passkeytest.ErrOptions)
	}
	if _, err := authenticator.Get(passkey.RequestOptions{}); !errors.Is(err, passkeytest.ErrOptions) {
		t.Fatalf("Get error = %v, want %v", err, passkeytest.ErrOptions)
	}
	if _, err := passkeytest.NewAuthenticator(passkeytest.WithOrigin("")); !errors.Is(err, passkeytest.ErrOptions) {
		t.Fatalf("WithOrigin error = %v, want %v", err, passkeytest.ErrOptions)
	}
}
