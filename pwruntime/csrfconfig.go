package pwruntime

import "time"

// CSRFConfig is the cross-site check's configuration.
//
// It lives in the shared leaf for the reason the security headers do: both
// chains read it, none of it names a transport, and one declaration means one
// set of binder tags and nothing that can drift between two readers of the same
// policy.
//
// Enabled is false by default. A project turns it on together with the include
// patterns that say what it covers, because a middleware installed over nothing
// reads as protection that is not there.
type CSRFConfig struct {
	Enabled   bool     `default:"false"`
	Include   []string `default:"/**" dependon:".enabled"`
	Exclude   []string `env:"-" dependon:".enabled"`
	FormField string   `default:"_csrf" dependon:".enabled"`
	Header    string   `default:"X-CSRF-Token" dependon:".enabled"`
	// CookieName is the companion cookie carrying the masked token the browser
	// runtime reads. It is never HttpOnly, because the runtime has to read it.
	//
	// It belongs here rather than to the session cookie policy: the secret is a
	// registered session slot like any other, and writing this cookie is the
	// check's own job rather than something the session does on its behalf.
	CookieName     string   `default:"pw_csrf" dependon:".enabled" help:"companion cookie carrying the token the browser runtime reads"`
	TrustedOrigins []string `env:"-" dependon:".enabled"`
	// TTL bounds the companion cookie the runtime reads. The secret itself is
	// bounded by the session slot that holds it.
	TTL time.Duration `default:"12h" dependon:".enabled" help:"lifetime of the companion token cookie"`
}

// DefaultCSRF returns the shipped defaults: off, and covering everything once
// turned on.
func DefaultCSRF() CSRFConfig {
	return CSRFConfig{
		Enabled:   false,
		Include:   []string{"/**"},
		FormField: "_csrf",
		Header:    "X-CSRF-Token",
	}
}
