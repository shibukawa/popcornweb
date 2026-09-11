---
id: decision:jwt-only-mode-not-scaffolded
type: decision
title: JWT-Only Mode Is Configured, Never Scaffolded
---
requirement:jwt-only-api-authentication serves when a configuration names it, and no command offers it, suggests it, or writes it, because a scaffold cannot supply the three things the mode depends on.

```yaml
status: accepted, amended
amended_by:
  decision: decision:jwt-only-preset-scaffolding
  reverses: the init rule below; the api-server preset of requirement:init-presets scaffolds the mode, per requirement:api-server-scaffold
  survives: the authentication question and --auth still refuse jwt_only, the capability catalog still excludes it, and api:cli-doctor still never suggests it
  on_what_grounds: policy:dev-token-relaxation makes a scaffolded project developable with no authorization server, and the issuer and audience are left empty the way the external-provider OIDC answer already leaves its credentials, so nothing below is guessed
context:
  - every other mode of data:authentication-runtime-config can be scaffolded into a project that then runs, because api:cli-init can point it at requirement:contrib-devidp and let a developer log in
  - jwt_only depends on an authorization server the deployment already runs, an audience registered at that server for this API, and an admission rule expressing who inside that issuer may enter
  - none of the three has a defensible default, and a scaffold that guessed them would produce a project that either refuses every request or admits every holder of any token the issuer ever minted
  - the name reads like the easy answer for anyone writing an API, which is exactly the reader least likely to have the three answers yet
decision:
  binding: data:authentication-runtime-config accepts mode jwt_only and validates the whole auth.jwt prefix, so a hand-written configuration serves
  init: api:cli-init does not offer it in the authentication question and does not accept it as an --auth value; the enum stays none, oidc, oidc-passkey, passkey, and oauth, the last a browser login this question can express, per requirement:init-oauth-login-choice. The question half stands; the "no command writes it" half is reversed by the amendment above
  add: it is not a member of the requirement:incremental-project-capabilities catalog, so api:cli-add never lists it and its auth capability continues to mean the browser login
  doctor: api:cli-doctor validates a project already configured for it and never suggests it, per rule:configuration-advisories
  discovery: the mode is found by reading this catalog or the reference documentation, not by answering a wizard question
what_this_is_not:
  - not deferred work; the mode is meant to serve
  - not a private or unsupported API; policy:access-token-verification and policy:token-revocation are as binding here as anywhere
  - not a security measure; hiding a mode protects nobody, and the argument is entirely about who should reach for it
reason:
  - a capability offered in a wizard is a capability recommended, and this one is correct for a narrow deployment shape and wrong for the browser applications the wizard mostly serves
  - the requirement:incremental-project-capabilities promise is that a declined capability stays reachable later; a capability that was never a wizard answer is not a decision the project is stuck with, because the configuration is the whole installation
  - decision:authentication-bootstrap-strategy grounds every scaffolded mode in an account the ceremony creates, and this mode creates none, so there is nothing for the scaffold to write beyond a configuration block
consequence:
  - the auth capability catalog stays one entry meaning one thing, rather than an entry with a mode question whose second answer scaffolds nothing
  - a jwt_only project writes its own configuration section, its own resolver when it wants one, and runs the popcornweb_revoked_token migration by hand
  - documentation names the mode in the API-server reference and not in the tutorial, so requirement:tutorial-continuity is unaffected
reopen_when:
  - requirement:contrib-devidp can issue at+jwt access tokens, which would make a runnable scaffolded jwt_only project possible
  - a convention for the audience value exists that a scaffold could write without inventing a registration at somebody else's authorization server
  - enough deployments configure the mode by hand that the hand-written block is the thing drifting from what the framework expects, which is the argument requirement:incremental-project-capabilities makes for every other capability
```
