---
title: Web Standards
description: The HTTP, security, authentication, error, API, and caching standards Popcorn Web puts on the wire.
sidebar:
  order: 3
---

A framework can claim to be “standards based” while leaving the consequential
choices to every application. Popcorn Web takes the narrower position: this
page lists behavior the framework actually emits or enforces, and links to the
guide that owns each detail. An RFC number means a published specification;
`X-RateLimit-*` is called out separately because compatibility is useful, but
does not turn a convention into a standard. Publication is not the only test that
matters either. A specification one browser engine ships is a different
proposition from one they all ship, and one that every server can send but no
browser will read is a different proposition again. The last two sections cover
features declined on those grounds.

## Security headers and browser boundaries

The security middleware owns CSP, HSTS, `X-Content-Type-Options`,
`X-Frame-Options`, `Referrer-Policy`, and `Permissions-Policy`. CSRF protection
combines a session-bound token with an origin check; cookie policy controls
`Secure`, `HttpOnly`, `SameSite`, signing, and encryption.

At first, `script-src 'self'` appears to conflict with JavaScript written
directly in a template. Generation removes that conflict. A `<script>` with an
inline body in a `.pw.html` component never reaches the response in that form.
The generator extracts each component script into a content-hashed JavaScript
file and puts a `<script src="…">` reference in the merged head, preserving
authored attributes such as `defer`, `async`, and `type`. The browser executes a
same-origin external script, so the policy needs neither a nonce nor
`'unsafe-inline'`.

Fast template rendering does not reopen the policy through `'unsafe-eval'`.
`pw generate` compiles template expressions and control flow into `_pw_gen.go`,
and the server executes functions compiled by Go. No runtime template
interpreter, browser-side template compiler, or `eval` enters the rendering
path. Generated Go provides the fast path while the narrow CSP remains intact.

Cross-origin access is the same frame's other half. CORS belongs to the WHATWG
Fetch Standard rather than to an RFC, and the framework implements the server
side of it: the preflight is answered before the session, the authentication,
and the guard, because a preflight carries none of the credentials those frames
look for, and the response is marked before every frame that can refuse one. The
second half is what makes it worth having. A browser withholds the status of an
unmarked cross-origin response along with its body, so without the marking every
typed refusal this framework writes — `401`, `403`, `429` with its retry
metadata — arrives at the caller as one indistinguishable network error.

Three things are deliberately outside it. There is no origin pattern language:
an allowed origin is an exact `scheme://host[:port]` or the single wildcard,
because a pattern evaluated per request is a second thing to get wrong and its
mistakes are silent. Private Network Access is not implemented, being a
Chromium-only negotiation. And a WebSocket upgrade is not subject to CORS at
all — it carries cookies across origins with no preflight — so its defence is an
origin check rather than anything configured here.

- [Security response headers](/guides/frontend/security-headers/)
- [Cross-origin requests](/guides/backend/cors/)
- [Template syntax and extracted files](/reference/template-syntax/#extracted-files)
- [CSRF and deployment security](/guides/architecture/security/)
- [Session state and direct cookies](/guides/backend/sessions/#using-cookies-directly)

## Authentication

Enabling authentication does not settle which trust model an application uses.
Popcorn Web keeps browser login separate from Bearer API authentication and
offers five configurations:

| Configuration | Setting | Boundary | Authentication mechanism |
| --- | --- | --- | --- |
| OIDC | `oidc_only` | Browser | Login always goes through an OpenID Provider |
| OIDC + passkey | `oidc_passkey` | Browser | OIDC establishes the account; WebAuthn passkeys handle routine login |
| Passkey | `passkey_only` | Browser | An administrator-issued, one-time credential bootstraps the first passkey |
| JWT | `jwt_only` | API | Every request carries a Bearer access token; there is no login page or session |
| None | `auth.enabled = false` | Public application or custom authentication | No authentication endpoints or guard are installed |

Passwords are not a sixth mode. The framework has no endpoint or store for
accepting, comparing, or resetting them, so it does not create a target for
credential-stuffing attacks. Even the secret used to bootstrap a passkey-only
account is a short-lived, single-use enrollment credential rather than a
reusable password. An OIDC provider may authenticate its users however it
chooses, but the application never receives the provider password.

The OIDC client supports two paths. Browser login uses the Authorization Code
Flow. S256 PKCE is mandatory, with no setting that can disable it, so
intercepting an authorization code is not enough to complete a login. The
client also binds `state` to a single-use transaction and requires `nonce`, then
verifies that the returned ID Token belongs to the login that this browser
started. Browserless or input-constrained clients, including TinyGo devices,
can instead use the [RFC 8628 Device Authorization
Grant](https://www.rfc-editor.org/rfc/rfc8628.html): the device displays a user
code and verification URI, the user approves it in a browser, and the device
polls for tokens. The OIDC wrapper requests `openid` and applies the same issuer,
audience, signature, lifetime, and subject checks to the returned ID Token.

The Resource Owner Password Credentials Grant, often called the Password
Grant, is not supported. The [current OAuth 2.0 Security Best Current Practice
(RFC 9700, section 2.4)](https://www.rfc-editor.org/rfc/rfc9700.html#section-2.4)
says it **MUST NOT** be used because it exposes the user's password to the
client and does not fit modern multi-step authentication. The [Client
Credentials Grant](https://www.rfc-editor.org/rfc/rfc6749.html#section-4.4) is
also intentionally out of scope: it authorizes a client acting on its own
behalf, with no end-user identity, and is suited to machine-to-machine or batch
work rather than this system's user login and session boundary. It is distinct
from the Device Authorization Grant.

JWT mode does more than verify a signature. It verifies the signature against
an allowlisted algorithm and discovered key, then checks `iss`, `aud`, `exp`,
`iat`, `sub`, the access-token type, maximum lifetime, and every required scope.
Configurations that need `jti` or revocation checks get those checks as well.
Issuer, audience, algorithms, maximum lifetime, admission, and revocation mode
have no permissive defaults; omitting one prevents startup. Every rejection
produces the same `401`, so the response does not expose which check failed.

### Development keeps the same authentication boundary

`pw dev` can start a small loopback IdP that speaks the same OIDC protocol as a
production OpenID Provider. It registers an ephemeral client and injects the
issuer, client ID, and client secret into the application. The application can
therefore exercise Authorization Code, PKCE, `state`, `nonce`, and ID Token
verification without adding a debug login or an authentication bypass to
production code. It also implements Device Authorization with public clients,
user codes, polling, and explicit browser approval. The development IdP itself
checks no password; the developer selects a fixture user from a list. It refuses
to run outside development, and the normal build also refuses to include it in
a release artifact.

JWT mode has a separate development relaxation for hand-written local tokens.
It disables signature, issuer, audience, token-type, time, and algorithm
allowlist checks as one unit. Four locks must all be open: the development build,
the development environment, the explicit setting, and a loopback request that
did not pass through a proxy. Identity-claim extraction, admission, revocation,
syntax, and token-size limits remain active. A response admitted this way carries
`X-Pw-Auth-Unverified: true`, while a production binary refuses to start if it
sees the relaxation setting instead of silently ignoring it.

- [Authentication](/guides/backend/authentication/)
- [Authentication design](/guides/backend/authentication-design/)
- [Sessions](/guides/backend/sessions/)
- [Development identity provider](/productivity/dev-identity-provider/)

## Error responses and rate limits

`pw.WriteProblem` negotiates RFC 9457 Problem Details or a safe HTML error page.
HTTP 429 follows RFC 6585 and can carry the standard `Retry-After` field.
`X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `X-RateLimit-Reset` are also
available for clients that use the established compatibility convention; they
are not IETF-standard fields.

- [Responses and problem details](/guides/frontend/responses/#errors)
- [Runtime error API](/reference/runtime/#errors)

## API lifecycle

Deprecation and shutdown are related, but they do not say the same thing.
RFC 9745 `Deprecation` says when a resource becomes discouraged while leaving
its behavior intact. RFC 8594 `Sunset` says when the resource is expected to
become unavailable. `pw.LifecycleHeaders` accepts both dates in one value,
validates their order, and serializes each in the format its RFC requires.

```go
lifecycle, err := pw.LifecycleHeaders(pw.Lifecycle{
	DeprecatedAt:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	SunsetAt:         time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
	DocumentationURL: "https://example.com/migrations/v2",
})
if err != nil {
	log.Fatal(err)
}
router.Handle("GET /v1/items", lifecycle(http.HandlerFunc(listV1Items)))
```

The response then carries a Structured Field Date for `Deprecation`, an
HTTP-date for `Sunset`, and a `Link` relation for the migration document. The
middleware only announces lifecycle state; it neither changes the response nor
turns the route off.

## OpenAPI

`pw generate` derives OpenAPI 3.1 operations from registered handlers, request
binding, response writers, streams, and problem constructors. The generated
document and optional Scalar or Swagger UI are served as operational endpoints,
behind the same path guard as application routes.

The document itself answers `Access-Control-Allow-Origin: *` whether or not a
cross-origin policy is configured. It describes a contract already chosen for
publication, and the tools that read it — a documentation UI hosted elsewhere, a
client generator, a linter in CI — have origins nobody can enumerate in advance.
A wildcard forbids credentials, so a document kept behind the path guard still
answers a cross-origin reader with the unauthenticated response.

- [API documentation](/productivity/api-documentation/)
- [Handlers and generated contracts](/guides/frontend/handlers/)
- [Operational endpoints](/guides/deployment/operational-endpoints/)

## API discovery

A client handed nothing but an origin still has to be told where the document
above lives. RFC 9727 removes that step. Turning on `server.api_catalog` answers
`/.well-known/api-catalog` with a catalog naming the OpenAPI document, the
reference UI, and the liveness probe — the three endpoints the configuration
already declares. Nothing is written for it, which is why one switch is the
whole of the adoption.

The catalog is an RFC 9264 link set, served as `application/linkset+json`
carrying the profile parameter that says this particular set of links is an API
catalog. A `HEAD` answers with the `api-catalog` link relation, which is what
the specification asks a supporting publisher for. Its links are relative unless
`server.api_catalog_origin` names the origin to build them from — relative
resolves against whatever URL the client fetched the catalog from, so the
endpoint needs no configuration to work, and RFC 9264's preference for
non-relative references is about a catalog somebody stores rather than follows.
The origin is never inferred from the request's `Host`: a guess taken from a
header the caller chose outlives the request and may name a host you do not own.
`pw doctor` notes the unset key outside development as PW0429.

Like the document it points at, the catalog answers
`Access-Control-Allow-Origin: *` whatever the cross-origin policy says — an
endpoint readable only from origins somebody already listed is not a discovery
endpoint. Unlike the probes, it stays inside the rate limit rather than joining
the exempt operational endpoints: a probe arrives on a schedule and a catalog is
fetched once, and RFC 9727 asks for the limit rather than an exception to it.

Catalogs nested inside other catalogs, the alternative formats offered through
content negotiation, and the `service-meta` link are all out of scope. One
binary publishes one API; a portfolio large enough to want grouping is
assembling its catalog above these binaries rather than inside one of them.

- [`[server]` configuration](/reference/configuration/#server)
- [Operational endpoints](/guides/deployment/operational-endpoints/)

## Caching and content negotiation

HTML is private and `no-store` unless its document shell declares a public
scope. Fingerprinted assets use validators and immutable caching; navigation
deltas and live delivery use `no-store`, and 429 responses are never stored.

### Representations follow the HTTP fields

The useful wire format depends on the caller, not just on the operation. JSON
is convenient from JavaScript, while an HTML form that works without
JavaScript—and a `curl` command someone can type without assembling a JSON
document—naturally use form encoding. Popcorn Web therefore does not require a
separate handler for each kind of client. The same request struct accepts
`application/json`, `application/x-www-form-urlencoded`, and
`multipart/form-data`; the request's `Content-Type` says how to decode it.

The response side follows the complementary rule. `Accept` chooses among the
representations a response supports: a typed stream, for example, can leave the
same handler as SSE, NDJSON, or a JSON array. Problem Details similarly
negotiates between a safe HTML page and JSON. Applications can add ordinary
media types such as `application/ld+json` without inventing a parallel RPC
transport. The framework reads `Content-Type`, `Accept`, and the related HTTP
fields at the boundary so application code can describe the value and let the
wire representation fit its surroundings.

- [Request body binding](/guides/frontend/handlers/#request-bodies)
- [JSON and other responses](/guides/frontend/responses/)
- [Negotiated streams](/guides/frontend/streams/)

### Images negotiate through `Accept`

One image URL does not imply that every client can decode the same bytes. With
image conversion enabled, the build converts PNG and JPEG sources referenced by
`img src` to WebP. Enabling `assets.images.avif = true` adds an AVIF
representation from the same source, leaving WebP and AVIF behind one URL. If a
conversion is larger than its source, the build declines it and retains the
original PNG or JPEG instead.

WebP is Baseline Widely Available and now makes a practical browser fallback.
AVIF support arrived later, so older browsers, WebViews, and non-browser clients
may still lack it. Popcorn Web does not guess from the User-Agent. It returns
AVIF when the request's `Accept` field permits `image/avif` and that
representation exists; otherwise it falls back to WebP. Only a URL with both
representations carries `Vary: Accept`, keeping shared caches from mixing the
two. If the build declined the WebP conversion, the retained PNG or JPEG is the
fallback instead.

### Static assets compress deeply at build time

With the corresponding asset transforms enabled, the build minifies CSS and
authored JavaScript under `public`. A TypeScript or TSX entry named by
`script src` goes through esbuild, which bundles its dependencies into an ES
module and emits a minified, content-hashed file. After those transformations,
the build precompresses the bytes that will actually ship—HTML, CSS, JavaScript,
JSON, SVG, and other compressible formats—at the maximum Brotli, zstd, and gzip
levels. The resulting `.br`, `.zstd`, and `.gz` sidecars spend CPU during the
build. Serving them spends none on compression.

For each request, `Accept-Encoding` selects the smallest available sidecar that
the client permits, with identity as the final fallback. Each representation
has its own ETag, and `Vary: Accept-Encoding` keeps caches from confusing them.

### Dynamic responses compress shallow and fast

A client is waiting while a handler produces HTML, JSON, navigation deltas, or
live output. With response compression enabled, eligible responses negotiate
zstd or gzip through `Accept-Encoding`, then run zstd at its fastest setting or
gzip at level 1. Brotli and maximum levels stay out of the request path because
saving a few more bytes is not worth delaying the response. Static compression
optimizes transfer size; dynamic compression protects throughput.

- [Rendering cache](/guides/frontend/rendering-cache/)
- [Static assets](/guides/frontend/static-assets/)
- [Compression](/guides/backend/compression/)
- [Responses](/guides/frontend/responses/)

## Trace context

W3C Trace Context is a Recommendation rather than an RFC, and the framework is
on both sides of it: `traceparent` and `tracestate` are read off every incoming
request and written onto every request made through an instrumented client, so
a trace continues across a service boundary instead of restarting at it. Both
HTTP backends read them through the same validator, which is what keeps the
rules below from being enforced on one and not the other.

What a bad header costs is the point. A `traceparent` that is malformed, that
uses the forbidden `ff` version, that spells its identifiers in uppercase, or
that arrives more than once leaves the request with no parent — it roots a new
trace and is still served normally. A `tracestate` that fails the grammar is
dropped on its own and the parent survives, which is what the specification
asks for: the trace still joins up and only the vendor data is lost.

- [Telemetry and cross-service traces](/guides/architecture/telemetry/#traces-that-cross-services)

## Operational HTTP

Health, readiness, OpenAPI, and API-documentation endpoints have distinct
availability and access rules. Request IDs, body limits, timeouts, recovery,
compression, redirects, and graceful shutdown complete the HTTP boundary around
application handlers.

- [Middlewares](/guides/backend/middlewares/)
- [Operational endpoints](/guides/deployment/operational-endpoints/)
- [Reverse proxies](/guides/deployment/reverse-proxy/)

## Why HTTPS termination stays out

A public web application needs HTTPS. Even so, Popcorn Web deliberately does
not load certificates and private keys or terminate inbound TLS inside the
application. TinyGo's `crypto/tls` support makes parity with a conventional Go
HTTPS server difficult, but the compiler is only part of the reason.

The missing piece is the inbound HTTPS server, not outbound HTTPS. Applications
can call OIDC providers and external APIs securely in either build. Host Go uses
the standard `net/http` and `crypto/tls` packages. TinyGo switches to
`tinygodriver/https`: Network.framework supplies TLS on macOS, Schannel supplies
it on Windows, and a vendored mbedTLS implementation supplies it on Linux.
Outbound traffic does not fall back to cleartext.

Terminating TLS in the application would also make certificate issuance,
renewal, revocation, private-key protection, and cipher-policy updates part of
the application's operating surface. The terminating process would then be the
first component to absorb TLS handshakes, connection floods, and other attack
traffic. A CDN or managed load balancer can manage the certificate lifecycle and
mitigate DDoS traffic at a much larger network edge. Popcorn Web has not
discarded HTTPS; it has moved termination to the layer better equipped to own it.

In production, terminate HTTPS at a CDN or load balancer and prevent direct
public access to the application's origin port. If traffic from the CDN to the
origin crosses an untrusted network, place a TLS proxy on the application host
or private network and keep HTTPS up to that proxy. Only the final, tightly
bounded hop to the application should use HTTP.

When local development also needs HTTPS, put nginx, Caddy, Traefik, or a similar
proxy in front of the application. Let that layer own certificates and TLS
configuration, and forward `Host` and `X-Forwarded-Proto` correctly. The
[reverse-proxy guide](/guides/deployment/reverse-proxy/) covers trusted-proxy
ranges, HSTS, and the public origins used for CSRF checks.

## Client hints, and why they stay out

`Accept-CH` looks like the right way to render a page the reader already wants.
The server advertises the hints it cares about, the browser sends
`Sec-CH-Prefers-Color-Scheme` on the next request, and a reader who prefers dark
gets a dark first paint with no script and no flash. Popcorn Web sends no
`Accept-CH` and reads no `Sec-CH-*` request header, because the mechanism has one
engine behind it.

| Header | Chromium | Firefox | Safari |
| --- | --- | --- | --- |
| `Sec-CH-Prefers-Color-Scheme` | 93 | — | — |
| `Sec-CH-Prefers-Reduced-Motion` | 108 | — | — |
| `Sec-CH-Viewport-Width` | 97 | — | — |
| `Critical-CH` | 91 | — | — |

Every one of these is still marked experimental, and `Critical-CH` — the header
that makes a browser retry the first navigation so the hints arrive in time — is
not on the standards track at all. An application built on them renders correctly
in Chrome and Edge and falls back to a guess everywhere else. A framework helper
would not close that gap; it would only make the narrow path look like the
supported one.

The alternatives are also better, which is what settles it. `prefers-color-scheme`
in CSS answers the operating-system preference before first paint, in every
browser, with no round trip and no cache cost. That leaves the hint improving one
case — an explicit override the reader chose on this site — and a cookie carries
that choice everywhere while the hint carries it in one engine. Layout that
depends on viewport width is worse still. The width changes when the reader
rotates the phone, with no new navigation to correct it, so a server-side
decision goes stale while it is still on screen; container queries and `srcset`
decide per element and stay correct.

Caching closes the argument. A response that varies on the reader's color scheme
has two representations, which is affordable. One that varies on viewport width
has a representation per distinct window size, which is not — a shared cache
would miss on nearly every request, and the page would lose more to that than the
hint ever returned.

None of this is enforced. A handler can read the header itself and set `Vary` to
match, and an application that has measured the flash and accepts the cache cost
is free to do exactly that. It is a choice the application owns, with reasoning
the framework declines to make on its behalf.

## Server-Timing trailers, and why they stay out

`pw dev` already measures the work behind a response. Tracing opens a span for
the render, one for each settled boundary, and one per statement, so the
breakdown of a slow page exists before anyone goes looking for it. Returning it
on the same response is the obvious next step, and HTTP has the mechanism for it:
`Trailer: Server-Timing` carries fields that are known only after the body, which
on a streamed page is most of what is worth knowing. DevTools would show them
beside the request. Popcorn Web sends no trailer, because on the connection
`pw dev` serves, nothing reads one.

| | Chromium | Firefox | Safari |
| --- | --- | --- | --- |
| `Server-Timing` as a response header | ✓ | ✓ | ✓ |
| `Server-Timing` as a trailer | — | HTTPS only | — |
| any trailer, read from `fetch()` | — | — | — |

Firefox is the only engine that has ever exposed a trailer in any form. It
restricted that support to HTTPS, and it advertises trailer support only over
HTTP/2, which it speaks only to `https://` origins. Chromium has declined
trailers repeatedly, on the grounds that too much of a browser interposes on
network requests for the change to stay contained. `pw dev` binds a cleartext
HTTP/1.1 port. Chrome therefore ignores the trailer, Firefox refuses it for not
being HTTPS, and the panel the developer opened it for stays empty.

The third row is why nothing routes around that. `fetch()` cannot read trailers
at all; DevTools can, and only for `Server-Timing`, as a special case wired into
the network panel. No script on the page and no pane in the dev console can pick
a trailer up and render it. One consumer remains, and two engines out of three
never populate it.

Forcing chunked encoding in development was the other half of the idea, and it
falls with the trailer it existed to carry. It would have cost something on its
own account. A development server that frames its responses differently from the
deployed one stops rehearsing it, and this framework has already paid for that
once: a boundary-marker bug that never appeared in development surfaced in
production as soon as a proxy, a TLS record, or a compressing encoder split the
bytes where development never had.

None of the timing is lost, which is what settles it. Those spans are in the
[development telemetry viewer](/productivity/dev-telemetry-viewer/) as a tree,
correlated with the log records and statements from the same request — including
everything that happened after the response committed. That is the part no
response header can carry, and the part the trailer was proposed for.

Nothing here is enforced either. A handler can set `Server-Timing` as an ordinary
response header and get a time-to-first-byte breakdown in every browser's
DevTools, and it can write a trailer beside it. What it cannot do is expect the
trailer to be read.
