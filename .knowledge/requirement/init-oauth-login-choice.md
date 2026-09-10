---
id: requirement:init-oauth-login-choice
type: requirement
title: OAuth Login in the pw init Authentication Question
---
The api:cli-init authentication question and --auth offer oauth, writing the oauth_only mode of data:authentication-runtime-config with a data:oauth-provider-profile name, so the [auth.oauth] section is scaffolded rather than copied by hand.

```yaml
status: implemented 2026-09-10; reported by an application the same day
priority: should
defect:
  where: the auth constants and the --auth switch in internal/pwcli/init.go, and the authentication row of decision:navigable-answer-hub
  behavior: the enum is none, oidc, oidc-passkey, and passkey; oauth_only is a shipped runtime mode per flow:oauth-provider-login with no scaffold, so every oauth project hand-writes [auth.oauth] and the account resolver
  cost: the hand-written block is the one requirement:incremental-project-capabilities says drifts; the reported doctor defect in requirement:doctor-auth-mode-awareness is what such a project meets first
why_a_question_value:
  fits: oauth_only is a browser login with a session, a callback, and a sign-out control, the same shape as the three offered values
  contrast: decision:jwt-only-preset-scaffolding kept jwt_only out because it is a different project shape; that argument does not reach a mode every browser application could choose
  not_a_preset: a preset answers every question, and provider choice is one row, so a website-x preset would multiply presets per provider
question:
  value: oauth
  flag: --auth=oauth, and the rejection message lists five values
  writes:
    mode: auth.mode oauth_only
    section: auth.oauth.provider, auth.oauth.client_id and client_secret empty, auth.oauth.redirect_url the development origin plus auth.callback_path
    provider: the built-in definitions of decision:oauth-provider-definitions-are-built-in, which today is x alone, so no provider sub-question until a second definition exists; the value is written explicitly because the field has no default
    loopback: auth.oauth.allow_loopback_http true, in config.dev.toml, because the development redirect is loopback http
    prod: config.prod.toml carries no auth section, as for every login; its STILL TO WRITE gap names auth, and once the operator writes the section rule:configuration-advisories PW0437 and PW0434 report what is still empty
  refuses:
    emulator: the emulator question is not asked and a stray --devidp is dropped, the same way passkey already drops it, because requirement:contrib-devidp is an OpenID Provider and this mode has no issuer
    passkey_combination: none exists for oauth in the runtime enum, so the question offers oauth alone
  requires: a store for the login records, the same rule as every other login value
  dotenv: .env.example names AUTH_OAUTH_CLIENT_ID and AUTH_OAUTH_CLIENT_SECRET, per requirement:dotenv-files
  page: the sign-in and sign-out controls the OIDC scaffold writes, because api:authentication-endpoints serves the same paths
  resolver: the account resolver source the OIDC scaffold writes, keyed on the profile identity claim, because policy:oauth-admission authenticated needs it
  next_steps: a line naming the provider developer console where the callback URL is registered, because the redirect must match exactly
catalog:
  api:cli-init: the authentication question and usage string gain the value; usesOIDC stays false for it, and a usesOAuth predicate gates the section and the refusals
  api:cli-add: the auth capability asks which protocol the provider speaks, OIDC or OAuth, before the emulator question, which only the OIDC answer reaches; passkey stays a pw init answer because its relying-party registration is bound to the deployment origin
  decision:navigable-answer-hub: the authentication row gains the value
  requirement:preset-customization-docs: the tutorial page for a login mentions the value beside oidc
acceptance:
  - pw init --auth=oauth --yes writes a project whose config.dev.toml has auth.mode oauth_only, auth.oauth.provider x, and auth.oauth.allow_loopback_http true
  - the same project's config.prod.toml names auth among the sections still to write and carries no oidc or oauth key
  - pw init --auth=oauth --devidp writes no devidp.toml and no dev.idp section
  - pw doctor --env=prod on the scaffold, once the operator has written the oauth section with empty client values and no redirect, reports PW0434 naming AUTH_OAUTH_CLIENT_ID and AUTH_OAUTH_CLIENT_SECRET and PW0437 naming auth.oauth.redirect_url, and nothing about oidc
  - the scaffold generates and builds, and its starter page shows the sign-in control
  - pw add auth offers oauth in the same terms
non_goals:
  - a second provider definition; the question grows a provider sub-question when decision:oauth-provider-definitions-are-built-in gains one
  - a development emulator for OAuth providers
  - combining oauth with passkey, which the runtime enum does not offer
```
