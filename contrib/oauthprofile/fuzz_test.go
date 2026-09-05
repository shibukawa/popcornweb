package oauthprofile

import "testing"

// FuzzProfileResponse drives the decoder over whatever a provider might answer
// with. Nothing here may panic, and a decoded profile must always carry the
// subject that a caller is about to link an account to.
func FuzzProfileResponse(f *testing.F) {
	f.Add(`{"data":{"id":"2244994945","name":"X Dev","username":"XDevelopers"}}`)
	f.Add(`{"data":{"id":42}}`)
	f.Add(`{"data":[]}`)
	f.Add(`{"errors":[{"message":"not found"}]}`)
	f.Add(`{"data":{"id":null,"username":{"nested":true}}}`)
	f.Add(`[]`)
	provider, _ := Lookup(ProviderX)
	f.Fuzz(func(t *testing.T, body string) {
		claims, err := provider.claims([]byte(body))
		if err != nil {
			return
		}
		profile, err := profileFrom(claims)
		if err != nil {
			return
		}
		if profile.Subject == "" {
			t.Fatalf("a decoded profile carried no subject: %q", body)
		}
	})
}
