---
title: Security
description: What the framework defends by default, what it hands you, and where a request is checked before your handler runs.
sidebar:
  order: 5
---

Much of the framework's security is already decided before you write a handler.
Some defenses are always on, others need configuration, and a few remain the
application's responsibility. That distinction matters: an absent defense is
far easier to address when you know it is absent.

The request path below shows where each check runs. It also makes the boundary
clear: Popcorn Web can reject an unsafe request, but it cannot decide who
should be allowed to perform an application-specific action.

## What a request passes through

A request reaches your handler through a chain, and the order is not arbitrary.
Each stage depends on the one above it having already run.

| Stage | What it decides |
| --- | --- |
| Operational endpoints | Health, readiness, and framework assets answer above everything |
| Public assets | A static file is served without touching a route |
| Body limit | An oversized body is refused before it is read |
| Security headers | The response policy the browser will apply |
| Recovery | A panic becomes a response instead of a dropped connection |
| **Session** | The cookie becomes a validated record, or the request is anonymous |
| **Authentication** | The login endpoints answer their own paths |
| **CSRF** | An unsafe request proves it came from your page |
| **Guard** | An unauthenticated request to a protected path is redirected |
| Your handler | — |

The three bold stages are where identity turns into authority. Everything above
them is about the shape of the request; everything from `Session` down is about
who is making it.

CSRF sits below authentication for a reason that is easy to miss. The login
endpoints answer above it, so a login POST and an OIDC callback never need a
token — they never reach the check at all. That is not an exclusion you
configure; it falls out of where the stage is.

## What is on by default

Five things protect a project that configured nothing.

**Response headers.** `X-Content-Type-Options`, `X-Frame-Options`, a referrer
policy, and a narrow `Content-Security-Policy` go out on every response. The
policy restricts scripts, plugins, the document base, and framing, and leaves
images, fonts, styles, and connections alone so an ordinary page keeps working.
See [Security Headers](/guides/frontend/security-headers/) for what it says and
when to write your own.

**Escaping.** A template inserts values through typed holes, and each one is
escaped for the position it lands in. Producing raw markup takes an explicit
intrinsic, so it is visible in review rather than implicit in a helper.

**URL schemes.** Escaping is the wrong tool for a URL, because the danger is
which scheme the browser executes rather than which characters it reads —
`javascript:alert(1)` survives every escaper unchanged. So a URL-bearing
attribute is checked against a scheme allowlist and renders `#tb-blocked-url`
when it fails. See [URL
attributes](/guides/frontend/templates/#attributes).

**Opaque session tokens.** The browser holds 256 random bits. The store is keyed
by a SHA-256 hash of that value, so a leaked database dump cannot be replayed as
a cookie.

**Static assets that are what they claim.** `pw build` refuses a file in
`public/` whose bytes contradict its extension — a `.png` holding a scripted
SVG never reaches the binary. Every `image/svg+xml` response also goes out
sandboxed, because SVG is the one served image that executes and it would
otherwise execute in your origin. See [Static
assets](/guides/frontend/static-assets/#a-file-has-to-be-what-it-says-it-is).

## What waits for a line of configuration

**CSRF** is off until you name the paths it covers. That is deliberate: a check
installed over nothing reads as protection that is not there, and `csrf.include
= []` would be a worse lie than `csrf.enabled = false`.

A browser login names one on the way in, so `pw init` writes the shape and turns
it on — the scaffolded sign-out control is a form, and a generated form is
refused rather than rendered without a token:

```toml
[security]
csrf.enabled = true
csrf.include = ["/**"]
csrf.exclude = []
```

Without a login there is no session to bind a token to, and `pw init` writes no
`[security]` section at all.

**Path protection** is the same shape. `protection.include = []` means every
path is public, and a project opts routes in rather than out.

**HSTS** is off until a deployment is certain about its certificate, because
turning it on is hard to undo in a browser that already cached it.

## How CSRF works here

A CSRF token proves a request came from a page your server rendered. The
mechanism is a secret the server knows and the attacker's page cannot read.

### The secret

One secret per session, minted when the session is created and stored in the
session record. Rotation is automatic: logging in replaces the session, and the
secret goes with it, so a token minted before a login is refused after one.

The secret never reaches a handler. It is deliberately absent from the session
view your code reads, and it never enters template scope.

### The three channels

The token reaches the browser three ways, and all three verify against the one
secret.

| Channel | Written by | Read by |
| --- | --- | --- |
| Hidden form field | The template compiler, into every unsafe form | A plain form submission |
| `pw_csrf` cookie | The session manager, beside the session cookie | The browser runtime |
| `X-CSRF-Token` header | The runtime, from the cookie | The server |

The hidden field is the part you never write. Any `<form method="post">` — or
put, patch, delete — gets one as its first child at generation time. A GET form
does not, because its fields become the query string and a token in a URL
reaches history, logs, and referrers.

Two forms of the same page are refused at generation rather than at runtime: a
form posting cross-origin, which would hand your session's secret to a third
party, and a form whose method is computed, which cannot be classified as safe
or unsafe.

### Why the cookie exists

The header could have come from the page. It does not, and the reason is
rotation.

A token embedded in the rendered HTML is fixed at render. If the session rotates
while the page is open — a login in another tab, a privilege change — the page
holds a token the server no longer accepts, and the next action fails with
nothing on screen explaining why. Reading the cookie at the moment a request is
issued closes that: the rotation wrote a new cookie, and the runtime picks it up
on its next request.

This is the shape Django, Laravel, and Spring's SPA configuration all use. It
costs one cookie that JavaScript can read, which is the one documented exception
to the rule that framework cookies are `HttpOnly`. A runtime that cannot read
the token cannot send it.

### Why the bytes differ every time

Each emission carries a fresh random pad, so two renders of one page produce
different token bytes even though both unmask to the same secret.

The reason is compression. Responses are compressed, and a secret that appears
unchanged in a compressed body alongside attacker-influenced text — an echoed
search query, a redisplayed form value — can be extracted a byte at a time by
watching response sizes. A pad that changes per response leaves nothing to
accumulate.

Verification rebuilds the expected value from the pad the request carried, then
compares in constant time. Rails and Django mask for the same reason.

### Anonymous visitors

A token needs a secret, and a public page with its own unsafe form — a contact
form, a search POST — has no login to take one from.

It needs no separate mechanism. The secret is a
[registered session slot](/guides/storage/session-storage/#the-csrf-secret)
declared `session.Private`, and a private slot rides a sealed cookie while the
session is anonymous. So an anonymous visitor gets a secret and the store gets
no row: a crawler that loads the page costs a cookie, not a record.

Once the visitor signs in, the login rotation moves the same slot onto the
configured backend and mints a fresh secret with it. There is no anonymous
secret left in play, because there was never a second one — one slot serves both
populations, and nothing here is configured on its own.

For a while the two were separate: a record field for a signed-in visitor and a
signed cookie for an anonymous one, under its own key and its own opt-in. That
split existed to keep anonymous traffic from deciding how many rows the store
holds. Placing a private slot in a cookie until the login answered the same
question, so the second mechanism went away.

### What a refusal looks like

A refused request gets 403 through the same error path as any other — your HTML
error page in a browser, a problem document for an API client — and your handler
is never called.

The response never says which check failed. The reason reaches the log, because
naming it in the response tells a caller which half to work on.

## What you still own

The framework does not decide these.

**Authorization.** A validated session says who is asking, not what they may
do. Path protection covers whole routes; anything finer is yours to write — in
the handler, or in the page itself with
[`{check …}`](/reference/template-syntax/#check--refusing-a-render), which calls
a Go function for its error alone and lets a refusal choose the response before
a byte is written.

**Which paths are unsafe.** `csrf.include` is yours to narrow. So is
`csrf.exclude`, and a webhook belongs there: it has no session and carries its
own authentication.

**Registered component inputs.** Publishing a component as redrawable makes its
parameters attacker-controlled. A component that formats values handed to it is
safe; one that loads a record by identifier must check ownership itself, exactly
as a handler would — which is what `{check …}` is for. Registration is the
review point.

**Secrets in configuration.** Values marked secret are redacted from logs and
the startup summary, and `pw doctor` reports a literal secret in a committed
file. It cannot tell you the value was already leaked.

## What development is allowed to do

A number of settings mean something different depending on `APP_ENV`. None of
them is read from a request, and none is inferred from a hostname — the
environment is a token the deployment sets, and the framework treats an
unrecognized one as a deployment rather than as development.

The three tiers below are worth telling apart, because they fail at different
times and one of them never fails at all.

### Refused outside development

These stop the process from starting. Each one turns a defense off rather than
weakening it, which is why none of them is left to a warning.

`session.cookie.secure = false` lets the session cookie travel over plain http.
`pw init` writes it into the development configuration on purpose, and outside
`dev` startup fails naming the setting. The exception is `same_site = none`
without `secure`, which fails everywhere, because no browser accepts that
combination as a cross-site cookie in the first place.

`auth.jwt.dev.trust_unverified_tokens` admits a hand-written bearer token with
no signature, so a developer with `curl` needs no authorization server. It has
four locks and needs all of them: the `pwdev` build mode, an `APP_ENV` that is
not `stg` or `prod`, the setting itself, and a request from a loopback address
with no opt-out. A binary built without the tag fails at startup when it merely
sees the setting, rather than ignoring it. What it does not relax is as
important: admission, revocation, and the parser bounds all keep running, so a
developer exercises the real rules rather than a bypass of them.

The [development identity provider](/productivity/dev-identity-provider/)
authenticates nobody — signing in means picking a user from a list — so it
refuses to start under `prod` and binds to loopback with no opt-out. `pw build`
also fails when the application being built imports it, so it cannot reach a
release artifact by accident. [Authentication](/guides/backend/authentication/#development)
covers how `pw dev` wires it up.

### Allowed outside development, with a warning

These start, warn at startup, and are reported by `pw doctor`. They cost
visibility, durability, or defense in depth rather than switching authentication
off, so the judgement stays with the deployment.

| Setting | What it costs outside `dev` |
| --- | --- |
| `security.csrf.enabled = false` | Unsafe requests are not checked for origin or token |
| `security.headers.enabled = false`, `hsts.enabled = false` | The browser applies a weaker response policy |
| `observability.query.enabled` | SQL text reaches diagnostic records |
| `observability.query.bind_values` | Row values reach them too — the only path by which application data enters a framework SQL record |
| `observability.minimum_level = trace` or `debug` | Verbose logs in a deployed process |
| A `sqlite` in-memory DSN | Schema and every row are lost at restart |
| `server.public.read_local = true` | Assets are served from disk rather than the built tree |
| `session.backend = cookie` with `auth.enabled` | A logout cannot end a session already issued — see below |

`csrf.enabled` is the one to look at first. The framework default is off, so a
project that scaffolded without a login and never turned it on is in this row
without having chosen to be.

### Different with no setting at all

Error pages carry the whole problem in `dev` — message, cause, the detail the
`Problem` was built with — and only a status and a title anywhere else. There is
no configuration for this, and the template is the same in both: what changes is
what it is handed. In development the reader is the person who caused the
failure and is about to fix it. Everywhere else the same page is public.

One relaxation is deliberately *not* environment-gated:
`auth.oidc.allow_loopback_http` and its JWT counterpart permit an `http` issuer,
and they are bounded by the host actually being loopback rather than by
`APP_ENV`. That is a stronger bound than the environment token, which the
deployment sets and can get wrong.

## Known limits

Four defenses here are bounded rather than absolute. Each bound is a
consequence of something real — a browser keeps what it was given, a runtime
cannot cancel a socket read — and none of them is a setting you can turn up
until the problem goes away. They are listed because a limit you know about is
one you can design around.

### The cookie session backend cannot end a session

`session.backend = cookie` seals the session record into the browser instead of
storing it. That makes it the cheapest backend to run and the only one that
needs no infrastructure, and it costs revocation: a record already written to a
browser cannot be taken back, so logging out expires the client's copy while a
copy taken beforehand keeps working until its sealed expiry. Account suspension
has the same shape.

Pairing it with a login is the right trade in development, where a login that
needs no database is the whole point and the exposure needs a browser someone
else is holding. So `dev` says nothing. Every other environment gets a warning
at startup, and `pw doctor` reports `PW0506` against the configuration:

```
the login session is stored where it cannot be revoked
```

Neither one stops the process. The judgement stays with the deployment, which
is the only party that knows whether it can live without ending a session on
demand — but if you are reading that warning in a staging or production log, the
answer is almost certainly no. Move to `rdb`, `redis`, `dynamo`, or `firestore`: logout and
suspension are the two acts that make an incident survivable, and both need a
record the server can delete.

The cookie backend stays a good choice for a cart, a locale, or a wizard's
progress with no login in front of it.

### Suspending an account takes up to 30 seconds

A suspended account is refused at login. It is also refused on requests that
already have a session, because the account behind a live session is re-read as
requests come in — but no more often than every 30 seconds per account, since
reading it on every request would put a database round trip in front of every
authenticated page.

That interval is the honest answer to how quickly a suspension reaches a session
someone is already holding. Call `auth.ForgetAccount(accountID)` from whatever
performs the suspension and the next request re-reads immediately; that is
process-local, so a deployment running several instances still waits out the
interval on the others.

When the account store cannot be reached at all, the request is refused with 503
rather than being admitted or signed out. The credential was never judged, a
retry may succeed, and admitting on an outage would make every suspension
conditional on the account database being up.

### A TinyGo build enforces its own request timeouts

TinyGo's `net/http` dials and then reads with no deadline — its own source says
so, in a `TINYGO: TODO handle timeouts` comment — so a `context.WithTimeout`
around an OIDC discovery, a token exchange, or a JWKS fetch would bound nothing,
and a slow identity provider would hold the request handler until the peer
closed the connection.

The OIDC and JWT clients wrap their transport on TinyGo builds so the deadline
returns to the caller regardless. What the wrapper cannot do is cancel the round
trip underneath it, because that runtime offers nowhere to cancel it: a hung
provider costs a goroutine and a connection until the socket itself fails,
rather than a stalled handler. Requests keep being served, and a provider that
hangs indefinitely under sustained traffic is still worth an alert.

None of this reproduces on a host Go build, including under
`force_tinygo_logic`, which selects the TinyGo code paths while still linking
the host `net/http`. Timeout behavior is one of the few things that has to be
tested on the real toolchain.

### Cached JWKS keys expire even during an outage

Verification keys are fetched from the issuer and cached. When a refresh fails,
the cached keys stay in use — an unreachable issuer does not make its keys
wrong. That holds for an hour past the cache TTL, and then verification starts
failing instead.

The reason is that a key withdrawn from the published document is withdrawn
whether or not this process can read the document that says so. An unbounded
stale window would make a compromised key usable for exactly as long as the
outage lasted, which is the window an attacker would choose. An hour is the
default; `MaxStaleAge` moves it, up to 24 hours, and there is deliberately no
setting for *forever*.

## Checking what is on

`pw doctor` reads the merged configuration for the environment you name and
reports what is off:

```bash
pw doctor --env prod
```

It flags a disabled CSRF check, a session cookie without `secure`, weakened
response headers, and query diagnostics left on outside development. Running it
against production configuration from a developer machine is the point — the
findings are about the file, not the running process.
