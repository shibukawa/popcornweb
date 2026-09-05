package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/session"
	"github.com/shibukawa/popcornweb/sessionconfig"
)

// SignInHint is what the login screen may show about the last person who used
// this browser. It is not authentication: a request carrying one is anonymous,
// and policy:authenticated-path-protection denies it exactly as it denies any
// other.
//
// Its value is what no protocol can supply. An identity provider can name which
// of its own accounts a returning visitor holds, through the select_account
// prompt, but it knows nothing about the other providers a deployment offers.
// Which issuer was used last is therefore local knowledge or no knowledge, and
// a passkey deployment has no provider to ask at all.
type SignInHint struct {
	// DisplayName and LoginID are for rendering. Mask what you render: the
	// disclosure risk is what the next person on the device reads, not what
	// the sealed cookie holds.
	DisplayName string `json:"name,omitempty"`
	LoginID     string `json:"login_id,omitempty"`
	// Issuer is the provider of the last successful login, so a login screen
	// offering several can skip its picker.
	Issuer string `json:"iss,omitempty"`
	// LastLoginAt bounds the hint by inactivity, independently of the absolute
	// bound the cookie itself carries.
	LastLoginAt int64 `json:"at,omitempty"`
}

// hintJar builds the sealed cookie the hint rides in, or nil when the
// deployment leaves the hint off.
func hintJar(config HintConfig, policy sessionconfig.SessionCookieConfig) (*session.Jar[SignInHint], error) {
	if !config.Enabled || config.TTL == 0 {
		return nil, nil
	}
	keys, err := session.ParseKeyring(append([]string{config.Secret}, config.PreviousSecrets...)...)
	if err != nil {
		return nil, err
	}
	cookie := session.CookieOptions{
		Name:   config.Name,
		Path:   "/",
		Domain: policy.Domain,
		Secure: policy.Secure,
		// The session policy already resolved its own Secure, so a false here
		// is the deployment's written loopback decision and carries through.
		AllowInsecure: !policy.Secure,
		HTTPOnly:      true,
		// The hint must survive the top-level navigation back from a provider,
		// so it matches what the transaction cookie carries rather than
		// tightening past it.
		SameSite: http.SameSiteLaxMode,
	}
	return session.NewJar[SignInHint](nil, session.JarOptions{
		// Sealed rather than signed: the payload names a person, and a signed
		// cookie stays readable by anything that can read cookies.
		Mode:   session.CookieSealed,
		Keys:   keys,
		Cookie: cookie,
		MaxAge: config.TTL,
	})
}

// rememberSignIn writes the hint after a completed login. A deployment with the
// hint off has no jar and this does nothing.
func (rt *runtime) rememberSignIn(x Exchange, data SessionData) {
	if rt.hint == nil {
		return
	}
	// The login identifier is whichever one this login actually produced. An
	// email address is what an OIDC directory returns; a provider login returns
	// a handle and usually no address at all, and an empty hint field would
	// leave the login screen with nothing to render beside the name.
	loginID := data.Email
	if loginID == "" {
		loginID = data.Username
	}
	_ = rt.hint.SaveTo(x, SignInHint{
		DisplayName: data.DisplayName,
		LoginID:     loginID,
		Issuer:      data.Issuer,
		LastLoginAt: time.Now().Unix(),
	})
}

// readSignInHint returns the hint of this browser, if it has one that is still
// within both bounds.
//
// A hint past either bound is discarded rather than shown: the browser drops to
// anonymous, where the login screen offers no account and no issuer, which is
// the state a deployment that never enabled the hint is always in.
func (rt *runtime) readSignInHint(x Exchange) (SignInHint, bool) {
	if rt.hint == nil {
		return SignInHint{}, false
	}
	value, err := rt.hint.LoadFrom(x)
	if err != nil {
		return SignInHint{}, false
	}
	idle := rt.config.Assurance.Hint.IdleTimeout
	if idle > 0 && (value.LastLoginAt <= 0 || time.Since(time.Unix(value.LastLoginAt, 0)) > idle) {
		rt.hint.ClearFrom(x)
		return SignInHint{}, false
	}
	return value, true
}

// forgetSignIn clears the hint. It needs no session and no authentication,
// because it is what the not-me control on a login screen calls and the person
// pressing it is by definition not signed in.
func (rt *runtime) forgetSignIn(x Exchange) {
	if rt.hint != nil {
		rt.hint.ClearFrom(x)
	}
}

// Hint returns the sign-in hint of the request, for a login screen to render.
// The bool is false when the deployment keeps no hint, when this browser has
// none, or when the one it had has expired.
func Hint(w http.ResponseWriter, r *http.Request) (SignInHint, bool) {
	return HintOn(HTTPExchange(w, r))
}

// HintOn is Hint over the transport seam.
func HintOn(x Exchange) (SignInHint, bool) {
	instance := activeRuntime()
	if instance == nil {
		return SignInHint{}, false
	}
	return instance.readSignInHint(x)
}

// MaskIdentifier renders an identifier for a login screen without showing the
// whole of it, because a shared browser shows it to whoever comes next.
//
// An issuer is deliberately not maskable: a login screen either offers the
// button or does not, so there is no partial form of it. Whether an issuer may
// be remembered at all is HintConfig.Enabled and its lifetime, not a rendering
// choice.
func MaskIdentifier(value string) string {
	local, domain, isAddress := strings.Cut(value, "@")
	masked := maskRun(local)
	if !isAddress {
		return masked
	}
	return masked + "@" + domain
}

// maskRun keeps the first character and replaces the rest with a fixed run,
// rather than one mark per character: a mask whose length tracks the original
// discloses the length, which is one of the few things worth guessing about an
// identifier.
func maskRun(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:1]) + "•••••"
}
