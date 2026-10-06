package pwruntime

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"strings"

	"github.com/shibukawa/tinybind-go/htmlbind"
)

// CSRFCookieName is the companion cookie the browser runtime reads a token from.
//
// It is a constant rather than configuration because it is a contract between
// the framework and the shipped script, not a deployment choice. The runtime is
// a module script, so it cannot read a name off its own tag the way a classic
// script can; a renamed cookie would leave it looking for one that is not
// there, and nothing would say so.
const CSRFCookieName = "pw_csrf"

// CSRFHeaderName is where the runtime puts the token. It is outside the
// framework's own header namespace because it is a name middleware already
// looks for.
const CSRFHeaderName = "X-CSRF-Token"

// CSRFSecretBytes is the raw entropy of a session's CSRF secret. It matches the
// session token, because both are the same kind of unguessable value.
const CSRFSecretBytes = 32

// csrfTokenBytes is the decoded length of an emitted token: the pad, then the
// pad combined with the secret.
const csrfTokenBytes = CSRFSecretBytes * 2

// NewCSRFSecret returns a session's CSRF secret, encoded the way every other
// opaque value in this framework is.
func NewCSRFSecret(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	raw := make([]byte, CSRFSecretBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// CSRFToken derives the value a browser receives from the session's secret.
//
// The pad is fresh for every call, and the emitted bytes therefore differ on
// every response. That is the whole point: a stable token reflected into a
// compressed body beside attacker-influenced input is what a compression oracle
// extracts one byte at a time, which is why Rails and Django both mask.
//
// The pad travels in the clear because it has to: verification recomputes the
// expected value from it. Knowing the pad reveals nothing, since producing the
// rest of the token still requires the secret.
func CSRFToken(secret string, random io.Reader) (string, error) {
	raw, ok := decodeCSRFSecret(secret)
	if !ok {
		return "", nil
	}
	if random == nil {
		random = rand.Reader
	}
	var pad [CSRFSecretBytes]byte
	if _, err := io.ReadFull(random, pad[:]); err != nil {
		return "", err
	}
	return encodeCSRFToken(pad[:], raw), nil
}

// ExpectedCSRFToken rebuilds the token a correct client would have sent, using
// the pad carried by the one that arrived.
//
// The comparison stays an equality against a value this framework computed,
// which is what lets the masked form work with a verifier that only compares.
// A malformed token yields an empty string, so it cannot match and cannot panic.
func ExpectedCSRFToken(secret, presented string) string {
	raw, ok := decodeCSRFSecret(secret)
	if !ok {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(presented)
	if err != nil || len(decoded) != csrfTokenBytes {
		return ""
	}
	return encodeCSRFToken(decoded[:CSRFSecretBytes], raw)
}

// VerifyCSRFToken reports whether presented unmasks to secret.
//
// It exists so a caller that does not route through the update options still
// compares in constant time rather than with ==.
func VerifyCSRFToken(secret, presented string) bool {
	expected := ExpectedCSRFToken(secret, presented)
	if expected == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(presented)) == 1
}

func encodeCSRFToken(pad, secret []byte) string {
	// A stack array rather than a make: the encoder copies out into the
	// returned string, so nothing here needs to live past the call.
	var out [csrfTokenBytes]byte
	copy(out[:], pad)
	for index := range secret {
		out[CSRFSecretBytes+index] = pad[index] ^ secret[index]
	}
	return base64.RawURLEncoding.EncodeToString(out[:])
}

func decodeCSRFSecret(secret string) ([]byte, bool) {
	if secret == "" {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(raw) != CSRFSecretBytes {
		return nil, false
	}
	return raw, true
}

// csrfSecretKey carries the request's CSRF secret.
//
// It is deliberately not a field of SessionView. A handler reads the view, and
// the secret is the one piece of a session record that must never reach one:
// policy:csrf-protection keeps it out of the request view, out of template
// scope, and out of every log.
type csrfSecretKey struct{}

// WithCSRFSecret carries the session's CSRF secret for the framework code that
// emits and verifies tokens. The session middleware sets it; nothing else does.
func WithCSRFSecret(ctx context.Context, secret string) context.Context {
	if secret == "" {
		return ctx
	}
	return context.WithValue(ctx, csrfSecretKey{}, secret)
}

// CSRFSecret returns the request's CSRF secret, if the request carries a
// session that has one.
func CSRFSecret(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	secret, ok := ctx.Value(csrfSecretKey{}).(string)
	return secret, ok && secret != ""
}

// StoreCSRFSecret records the request's CSRF secret on a request value that
// carries its own state, which is WithCSRFSecret for a transport that cannot
// derive a context.
func StoreCSRFSecret(store ValueStore, secret string) {
	if secret == "" {
		return
	}
	store.SetUserValue(csrfSecretKey{}, secret)
}

// AppendCSRFOption adds what a render has to be told about the token.
//
// Every render entry of both runtimes calls it, and that is the point of it
// being one function: a render told nothing fails on the first unsafe form it
// reaches, so an entry that forgets the option does not render a form at all.
// The document entry had it and, one at a time, the others turned out not to —
// a fragment, an action response, a redraw, the error page, and every entry of
// the second runtime — each answering 500 for markup the page itself rendered.
//
// There are three answers. A token is carried. No token with the check turned
// off says so, so a deployment that disabled the check gets the form it asked
// for rather than a failed render. No token with the check on adds nothing,
// which fails a render that reaches a form: policy:csrf-protection asks for
// that rather than for a form nothing protects.
func AppendCSRFOption(options []htmlbind.Option, token string, disabled bool) []htmlbind.Option {
	switch {
	case token != "":
		return append(options, htmlbind.WithCSRFToken(token))
	case disabled:
		return append(options, htmlbind.WithoutCSRFToken())
	}
	return options
}

// CSRFRendersHTML reports whether a safe request announces that it will
// render the page's templates, and therefore needs the token an unsafe form in
// them carries. It takes the four header values it reads — Sec-Fetch-Dest,
// Accept, UpdateRenderHeader, and ResponseModeHeader — rather than a reader, so
// a transport whose header values are not strings converts only what is there.
//
// A document request says so itself, through CSRFHTMLRequest. The browser
// runtime's own requests do not: a partial update and a live delivery are
// fetches, so they arrive with no text/html in Accept and a Sec-Fetch-Dest of
// empty, and they render the same chain the document did. Reading them as
// "renders no HTML" left the secret out of the request, and a page whose
// layout holds a sign-out form answered 500 to every update of itself.
//
// Neither header mints anything a document request would not have: a page
// that sends one was rendered from this origin and already holds the secret.
//
// This is the prediction and not the rule. A render no header announced asks
// for the secret itself when it runs, so answering false here costs a page
// nothing but the session read it would have made anyway.
func CSRFRendersHTML(secFetchDest, accept, updateRender, responseMode string) bool {
	return CSRFHTMLRequest(secFetchDest, accept) ||
		strings.TrimSpace(updateRender) != "" || strings.TrimSpace(responseMode) != ""
}

// CSRFSafeMethod reports whether a method is one the cross-site check lets
// through — GET, HEAD, and OPTIONS, the set HTTP defines as not changing
// state. TRACE is deliberately absent: nothing in this framework routes it,
// and a state-changing handler bound to it would otherwise pass unchecked.
// Both transports call this one function so the set cannot drift.
func CSRFSafeMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "OPTIONS":
		return true
	}
	return false
}

// CSRFHTMLRequest reports whether a safe request is expected to render HTML,
// given its Sec-Fetch-Dest and Accept header values. Browsers send either an
// HTML Accept value or a document navigation target; a generic */* request
// does not justify allocating session state merely in case the handler might
// render a form. Both transports call this one function so the answer cannot
// drift.
//
// CSRFRendersHTML is the question a middleware asks; this is the half of it a
// browser's own navigation answers.
func CSRFHTMLRequest(secFetchDest, accept string) bool {
	if strings.EqualFold(strings.TrimSpace(secFetchDest), "document") {
		return true
	}
	for remainder := accept; remainder != ""; {
		var mediaRange string
		mediaRange, remainder, _ = strings.Cut(remainder, ",")
		mediaType, _, _ := strings.Cut(mediaRange, ";")
		if strings.EqualFold(strings.TrimSpace(mediaType), "text/html") {
			return true
		}
	}
	return false
}
