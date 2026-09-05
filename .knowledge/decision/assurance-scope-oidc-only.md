---
id: decision:assurance-scope-oidc-only
type: decision
title: Assurance Scope Under External-Identity-Only Login
---
Assurance covers freshness and strength only; contact-channel verification stays out of scope while every account is created by an external identity or a passkey the framework did not collect itself.

```yaml
status: accepted
context:
  - every mode of data:authentication-runtime-config authenticates through an identity provider or a passkey
  - the framework registers no account from a self-asserted email address or password
  - data:external-identity already refuses to identify an account by a mutable email claim
excluded:
  - framework-owned email or phone one-time-code verification
  - an unverified account state gating features until a contact channel is proved
  - a verification banner, a resend throttle, or a grace period as framework surface
reason:
  - a contact channel the framework never collected cannot become a framework-enforced gate
  - policy:oidc-admission already decides who may enter, which is the gate a deployment actually configures
  - policy:account-linking already forbids linking accounts because email strings match, which is where an unverified address would do its damage
  - policy:account-recovery already demands an explicit recovery authority and already forbids possession of an email address alone
application_owned:
  - an application collecting its own contact address verifies it and gates its own features
  - the framework offers no opinion on that gate and no storage for it
reopen_when:
  - a registration policy admits an account from a self-asserted identifier the framework stores
  - a framework flow needs an out-of-band channel, such as notifying an assurance change or a recovery path the deployment cannot supply
  - a passkey_only deployment asks the framework for a recovery channel instead of the administrator bootstrap it has today
consequence:
  - concept:assurance-axes carries two axes, and a reachability axis is deliberately absent
  - the freshness axis needs a provider that reports auth_time, so a mode without one — flow:oauth-provider-login — answers a confirmed or zero-window requirement 503 and logs the mode, rather than redirecting into a re-proof that cannot converge; a window measured from the login stays answerable
  - requirement:session-assurance-levels lists contact verification as a non-goal rather than as deferred work
```
