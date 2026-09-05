---
title: Authentication design
description: What the five modes actually separate, how to choose one, and the assurance a session carries once the login is over.
sidebar:
  order: 0
---

`auth.mode` has five values. Four of them describe a person signing in with a
browser, and they do not merely select OpenID Connect, passkeys, or a
combination of the two. In `oidc_passkey` mode, for example, people normally
sign in with a passkey; the OIDC provider returns only for recovery.

Choose among those four by looking at account creation and recovery authority,
not only at the daily sign-in screen. The fifth, `jwt_only`, is not on that
axis at all — it serves an API where nobody signs in.

## What the modes separate

A passkey cannot create an account. The reason is plain: there is nothing for the first credential to attach to. A public key arrives, and unless something else can say whose it is, the service has nothing to do with it.

Every mode therefore has to answer one question before anybody signs in at all: what brought this account into existence? The modes are five answers.

| `auth.mode` | Where an account comes from | Daily sign-in | Recovery authority |
| --- | --- | --- | --- |
| `oidc_only` | The provider | The provider | The provider |
| `oidc_passkey` | The provider | A passkey | The provider |
| `passkey_only` | A login ID and one-time secret an administrator issues | A passkey | An administrator, or another passkey |
| `oauth_only` | A provider that issues no ID Token, such as X | That provider | That provider |
| `jwt_only` | The authorization server that mints the tokens | None — a bearer token on every request | Not this application's, and not a question it can be asked |

That last column carries the most operational weight, and the last row is where it stops applying.

### `oidc_only`

Everything leans on the provider. Accounts are created there, daily sign-ins happen there, and the person who forgot their password is rescued there. You hold a session and nothing else.

**What you get.** No credential to store, so nothing to leak and no leak to defend against. When the provider account stops — a departure, a cancelled contract — sign-in here stops with it. In B2B that alone can decide the design.

**What you take on.** The provider's outage is your outage, and its policy becomes your policy. Anyone without an account there cannot get in by any route.

### `oidc_passkey`

The first visit goes through the provider; every later one uses a passkey. The provider leaves the daily path and waits behind it as the way back in.

**What you get.** Sign-in gets faster, because the round trip to the provider drops out of the ordinary path along with its latency and its availability. Someone who loses a passkey still has the recovery route `oidc_only` would have given them.

**What you take on.** Two authentication methods, and the obligation to keep reasoning about both. Which one outranks the other, who may remove one, what happens to a person who loses both. Startup refuses until `auth.recovery.policy` is set explicitly, so that question cannot be quietly postponed.

### `passkey_only`

No provider exists. An administrator creates the account and hands the person a login ID and a one-time secret. They redeem it and register a passkey of their own.

**What you get.** No external dependency and no shared secret in storage. It works on a closed network, and it works for an organization that has no identity provider at all.

**What you take on.** Recovery, entirely. Knowing an email address is not grounds for recovery — anyone can know one, which is why it is forbidden by default. What remains is another enrolled passkey, an administrator reissuing a credential, or a verified mechanism the application provides itself.

### `oauth_only`

The provider authenticates people and never issues an ID Token. X works this way, and so does most of what the industry calls social login. Read that row of the table again and it looks identical to `oidc_only` — same account origin, same daily sign-in, same recovery authority — because on those three axes it is. What separates the two is not who holds the authority but how much of it this application can act on.

**What you get.** The shortest possible distance to a working sign-in, for the audience that already has an account with the provider and would rather not make another. The application writes no protocol code, and the session it ends up with is the ordinary one: the guard, the account resolver, and admission behave exactly as they do after an ID Token.

**What you take on.** Three capabilities that plain OAuth cannot express. Sign-out reaches your session and not the provider's, so a shared browser keeps the visitor one click from the previous visitor's account — which is why `auth.shared_device` is refused here rather than half-honored. Step-up re-authentication cannot run at all: proving an identity twice means sending `max_age` and reading a verified `auth_time` back, and neither exists, so `auth.Confirmed` answers `503` instead of looping. And the profile the login rests on is unsigned; the trust is that the token came from an exchange this deployment started and the answer arrived from the provider over TLS.

That is the ordinary basis for social login. It is weaker than an ID Token, and the three refusals above are how the framework declines to pretend otherwise. [Signing in with X](/guides/backend/authentication/#signing-in-with-x) has the configuration.

### `jwt_only`

Nobody signs in. Every request carries an access token in `Authorization: Bearer …`, the application verifies it, and that is the whole ceremony. There is no login endpoint, no callback, no session record, and no cookie — the caller was already holding a credential an authorization server issued, and this application is the resource server that checks it.

That is why the three questions above go quiet here. The account was created wherever the authorization server says; the daily sign-in belongs to whatever obtained the token; and recovery is a conversation the caller has with the authorization server, not with you.

**What you get.** No credential storage, no session storage, and no state to carry between requests. A deployment that keeps nothing can scale to nothing and back.

**What you take on.** A token stays valid until it expires, so ending access early needs a revocation strategy you choose deliberately — `revocation.mode` has no permissive default, and startup refuses until you name one. See [Revoking a bearer token](/guides/backend/authentication/#revoking-a-bearer-token).

A browser flow and a bearer API are different trust models, so this mode does not combine them. An application that needs both serves the API from a separate deployment. `pw init --auth` does not offer `jwt_only` for that reason; the API-server preset scaffolds it instead. [Authentication](/guides/backend/authentication/#jwt-only-api-servers) has the configuration.

## Choosing one

The mode alone does not settle it. Admission — who may enter — settles the rest.

One question comes before all of them: is there a browser? If callers are other services, scripts, or a mobile client holding a token, take `jwt_only` and the cases below do not apply — there is no account creation to place and no recovery route to design, only an admission rule and a revocation strategy. Everything else here assumes a person at a browser.

### Consumer products

People arrive already holding a Google or Apple account. Use `oidc_only` or `oidc_passkey` with admission `authenticated` and `auto_provision = true`, so a first sign-in creates the account.

One consequence deserves attention. If a user loses that Google account, they lose yours at the same moment. Choosing `oidc_passkey` and getting a passkey enrolled early puts a second door in the wall.

### Business customers

The buying organization's identity provider is the authority. Pair `oidc_only` with admission `registered` and record who may enter **before** anyone signs in. Because `auth.oidc.identity_claim` can name a stable directory-issued identifier such as an employee number, you can register a person who has never logged in — which a subject claim would never let you do.

Offboarding happens in their provider. The account stops there, and sign-in here stops with it. Not maintaining a list of former employees is the practical value of this arrangement.

### Shared terminals

A reception desk, a line-side station, a till. The browser does not correspond to one person.

Set `auth.shared_device = true`. It couples three settings, and **any one of them alone accomplishes nothing**. Clear the local memory while the provider session stays alive and the next visitor still reads the previous person's name — out of the provider's own account picker, because the provider is what supplies the name.

The common end of a session on a shared terminal is not a sign-out but abandonment, so enable `auth.assurance.presence` alongside it. More on that below.

### Consumer products whose users arrive from X

The audience already has an X account and no reason to create another. Use `oauth_only` with `provider = "x"`, admission `authenticated`, and `auto_provision = true`.

Link accounts on the X user id, which `identity_claim = "sub"` already selects. The handle is the readable field and the wrong one to link on: X lets a handle be renamed, and lets a released one be claimed by somebody else, so an account linked to `@example` eventually belongs to whoever holds `@example` next.

Do not reach for this mode on a shared terminal, and do not reach for it if any operation in the application needs a fresh confirmation before it runs. Both want capabilities the protocol does not have, and startup refuses the first while the guard answers `503` on the second.

### Closed internal systems

No identity provider, or none you may reach. Use `passkey_only` with admission `registered` and `auth.registration.policy = "administrator"`. The bootstrap credential an administrator issues is itself the account-opening ceremony.

### Keyboardless and input-constrained devices

A TinyGo device with no keyboard cannot follow the browser login path: it cannot
safely collect a password, complete passkey UI, or receive an authorization-code
redirect. This is a different axis from `auth.mode`. The mode chooses how a
human enters the web application; an input-constrained device uses the [RFC 8628
Device Authorization Grant](/appendix/web-standards/#authentication) as an OIDC
public client.

The device asks the provider for an authorization, displays the short user code
and verification URI (or a QR code for `VerificationURIComplete`), and polls
while the person approves on a separate phone or computer:

```go
device, err := oidc.NewDeviceClient(provider, oidc.DeviceConfig{
    ClientID: "display-controller",
}, oidc.DeviceOptions{})
if err != nil {
    return err
}

authorization, err := device.Begin(ctx, oidc.DeviceBeginOptions{
    Scopes: []string{"display.read"},
})
if err != nil {
    return err
}
show(authorization.VerificationURI, authorization.UserCode)

tokens, identity, err := device.Poll(ctx, authorization)
```

Register it as a public client and do not embed a client secret in firmware. The
library follows the provider's polling interval and `slow_down` responses; use a
cancelable context so leaving the screen, restarting provisioning, or shutting
down the device stops the poll. `DeviceCode` is deliberately not exposed: never
log or display that bearer credential.

Approval returns tokens to the device, not a browser session and not a passkey.
Decide separately which API accepts the access token, which scopes the device
needs, how refresh or reprovisioning works, and where tokens can be stored on
that hardware. Prefer short-lived, least-privilege tokens when secure persistent
storage is unavailable. The [development identity
provider](/productivity/dev-identity-provider/) implements the same public-client,
user-code, approval, and polling path for local tests.

## After the login

Everything so far concerned signing in. Once that is done, the simple design gives a session two states: valid or not.

That design has a problem. A stolen session cookie reaches settlement, credential changes, and export **at full authority**. The harder you make the moment of login with passkeys and MFA, the more attackers stop attacking authentication and start stealing the cookie behind it. Since 2025 that has been the dominant route into accounts.

Shortening the expiry as a remedy makes ordinary users sign in repeatedly. The dial that keeps logins rare and the dial that bounds a theft are the same dial. That is the limit of the two-state model.

### States and transitions

A session has four states, and they do not sit in a line. A login enters
`active`, a heavy operation raises it to `confirmed`, and time lowers it again.
The path is a loop.

![A state diagram: sign-in moves anonymous to active, Ensure raises active to confirmed, and time returns it](../../../../assets/diagrams/assurance-states.svg)

`identified` is dashed for a reason. Without a configured hint that state does
not exist, and a session drops from `active` straight to `anonymous`. Keeping a
memory of the last visitor is something a deployment turns on deliberately, and
a shared-terminal deployment is forbidden from turning it on at all.

**A handler branches on two of these.**

- Authenticated or not — written in `auth.protection.include`, with no handler code at all
- Recently proved or not — declared per operation, in the handler that performs it

`identified` is a login-screen appearance, not something a handler tests. `anonymous` is a login problem, and path protection answers it first.

### Freshness is the only axis

You could add a second axis beside "how recently": how strongly. We do not.

The framework cannot rank the methods it mounts. `oidc_only` and `passkey_only` each produce a single method, so an ordering would have nothing to order. `oidc_passkey` produces two, and neither is stronger in general — a provider backed by a hardware key beats a local passkey, and a passkey requiring user verification beats a provider that waves through a ninety-day SSO session. An ordering is a claim a deployment makes about its provider, not a property of the label.

So a handler writes one predicate and one parameter.

```go
app.HandleFunc("GET /admin", auth.Ensure(adminPage, auth.Policy("admin")))
app.HandleFunc("POST /api/admin/drop", auth.EnsureAPI(drop, auth.Policy("danger")))
```

```toml
[[auth.assurance.policy]]
name = "admin"
max_age = "15m"

[[auth.assurance.policy]]
name = "danger"
max_age = "0"
confirm = true    # confirm now, for this operation
```

Windows are named rather than written inline so that one handler serves a consumer deployment with a generous window and an internal one with a tight window.

### A login is not a confirmation

Every window so far measures minutes since the last proof. That gives a freshly signed-in session a free pass through all of them: someone who signed in to read a dashboard walks on to the transfer screen without ever being asked about it.

Signing in and confirming an operation are different acts. The login happened for its own reasons, and it says nothing about the person **now** asking to move money. The step-up happened because this operation demanded it. Measured by freshness alone, the two become the same thing.

So a requirement comes in two kinds.

```go
auth.MaxAge(15 * time.Minute)     // any recent proof, the login included
auth.Confirmed(5 * time.Minute)   // only a re-proof this guard asked for
```

```toml
[[auth.assurance.policy]]
name = "transfer"
max_age = "5m"
confirm = true    # a login never fills this
```

Entering an administration area wants `MaxAge`: the target is a session left open all afternoon, not the person who signed in a minute ago. Transfers, tenant deletion, and customer-list exports want `Confirmed`.

`Confirmed(0)` means confirm for this attempt. Elapsed time can never satisfy a zero window — the trip to the provider and back always costs more than zero seconds, so a timestamp comparison would refuse again immediately after a successful re-proof and never converge. Only the admission a completed step-up leaves behind satisfies it. A positive window lets one confirmation cover several operations over the next few minutes.

### Where freshness is measured from

Not from when the answer arrived.

A provider that receives `max_age` may satisfy it out of its own single sign-on session. A token arriving now does not mean an authentication happening now. The `auth_time` claim is the real moment of proof, and freshness is measured from there.

A missing `auth_time` after `max_age` was sent is therefore a failed re-proof. OpenID Connect requires the claim in exactly that case, so its absence means the provider did not answer the question it was asked.

`prompt=login` cannot be checked at all. The specification makes it a SHOULD, and no claim reports whether it was honored. Prompt improves the odds of an interaction; it proves nothing. **Security decisions rest on `max_age` and `auth_time`.**

## Sequences

### An ordinary sign-in

![A sequence across the browser, Popcorn Web, and the provider: redirects out, a code back, and an exchange](../../../../assets/diagrams/login-sequence.svg)

That final rotation revokes whatever session the browser already held. Nothing from before the login survives it, which is what closes fixation.

### A step-up

![A sequence where a stale auth_time forces re-authentication and four checks precede the rotation](../../../../assets/diagrams/step-up-sequence.svg)

Drop step 1 and the step-up becomes an **account swap**: the previous account's sensitive operation is already staged, and somebody else's successful login completes it. Issuer, identity claim, and value are compared against the session in hand, and a mismatch writes nothing.

Step 3 is there so that a person who lost access in the meantime cannot re-prove their way back.

Resuming a POST has a limit. The return from the callback is a redirect, which is a GET, so the wrapper can only replay a safe method. Give the read route the working window and the write route the boundary, and make the write window slightly the more generous of the two — otherwise a long form filled after a fresh read loses its input on submit.

### Signing out

What a sign-out does to the provider session has three possible answers. One of them is not offered.

![A sequence contrasting reconfirm, which sends the provider nothing, with global, which calls end_session_endpoint](../../../../assets/diagrams/logout-scopes.svg)

`reconfirm` is a name we gave a behavior, not a standard. **No request goes to the provider at a reconfirm sign-out.** One parameter is added to the next ordinary authorization request, and that is the whole of it.

No specification offers a way to be forgotten by a provider for one relying party alone. The provider session is a single session shared across all of them, and RP-Initiated Logout ends all or nothing. Reconfirm sidesteps that by asking the provider for nothing.

Choose `global` for shared terminals, kiosks, and any deployment whose definition of signing out is leaving everything. To offer both to a user, set `auth.oidc.allow_global_logout_request`. A request may **escalate only**, never downgrade.

### Sessions nobody is sitting at

An idle timeout measures time since the last **request**. It does not measure time since a person did anything.

That gap causes real trouble in both directions. A page holding a live connection reconnects on its own, so an unattended browser stays signed in indefinitely. A person reading one page for forty minutes issues no request and is signed out mid-task.

Enable `auth.assurance.presence` and the browser reports **one bit per tick**: whether any input happened since the last one. No coordinate, no keystroke, no timing pattern leaves the browser.

The trust here runs one way.

- A report of absence is **acted on**. A false positive costs one extra sign-in.
- A claim of presence is **bounded by what the server already enforces**. A script can send it, so it never moves the absolute expiry.
- A beacon that stops arriving **is itself a report of absence**. A client cannot assert not-being-there.

Nothing reports a machine waking from sleep. A timer that should fire on a fixed interval observing a much larger gap is how it is inferred, and that counts as absence rather than presence.

## Remembering the last visitor

After a session ends, the browser can keep a note of who signed in last. By default it keeps none.

Keeping one is worth something because it holds **what no protocol can supply**. A provider can name which of its own accounts a returning visitor holds, through `prompt=select_account`. It knows nothing about the other providers a deployment offers. Whether a multi-issuer login screen shows its picker again is decided by local memory or by nothing. A passkey-only deployment has nobody to ask at all.

The note rides in a sealed cookie, unreadable by the browser, which is what lets it hold a login identifier. What needs protecting is not the contents but **what the login screen renders** — that is what the next person at a shared terminal reads, so put it through `auth.MaskIdentifier`.

An issuer cannot be masked. The screen either offers the "Continue with Microsoft" button or it does not; there is no partial form. Whether an issuer may be remembered is therefore the enabled flag and the lifetime, not a rendering choice. Setting `ttl = "0"` is a valid answer, and it sends the browser from `active` straight to `anonymous`.

## Where to go next

Configuration keys, endpoint behavior, the passkey ceremonies, and session backend selection are in [Authentication](/guides/backend/authentication/). For where a session is stored, see [Session storage](/guides/storage/session-storage/).
