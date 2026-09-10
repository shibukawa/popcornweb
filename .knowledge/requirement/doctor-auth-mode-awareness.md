---
id: requirement:doctor-auth-mode-awareness
type: requirement
title: Doctor Reads the Provider Section the Auth Mode Selects
---
The api:cli-doctor identity provider checks read auth.mode and inspect only the provider section that mode uses, so an oauth_only project is judged on auth.oauth.* and told to set AUTH_OAUTH_* rather than on an oidc section it is forbidden to fill.

```yaml
status: implemented 2026-09-10; reported by an application the same day
priority: must
defect:
  where: checkIdentityProvider in internal/pwcli/doctorchecks.go
  behavior: after auth.enabled it reads auth.oidc.issuer, auth.oidc.redirect_url, auth.oidc.allow_loopback_http, and the three oidc provider keys, for every mode
  observed_on_oauth_only:
    PW0437: fires because auth.oidc.redirect_url is empty, while auth.oauth.redirect_url is set
    PW0434: fires and names AUTH_OIDC_ISSUER, AUTH_OIDC_CLIENT_ID, and AUTH_OIDC_CLIENT_SECRET, which the mode refuses per data:authentication-runtime-config mode_validation
  why_wrong: data:authentication-runtime-config rules say validate and read only the section the selected mode uses; the runtime does, and doctor did not follow it, so the two disagree on a configuration the application accepts
  also_affected:
    passkey_only: has no provider at all, so every provider advisory is a false positive
    jwt_only: has no provider ceremony either; decision:jwt-only-mode-not-scaffolded already says doctor validates it and never suggests a browser mode
mode_dispatch:
  read: auth.mode, defaulting to oidc_only when absent, because the runtime default is oidc_only
  oidc_only_and_oidc_passkey: the oidc section, unchanged from today
  oauth_only:
    section: auth.oauth
    redirect: auth.oauth.redirect_url is what PW0437 and the redirect-target-disagreement advisory read; auth.oauth.allow_loopback_http is the loopback exception
    issuer_checks: none; the mode has no issuer field, so development-issuer and insecure-issuer advisories do not apply, and the loopback pairing advisory reads oauth.allow_loopback_http
    provider_values: PW0434 checks auth.oauth.client_id and auth.oauth.client_secret and names AUTH_OAUTH_CLIENT_ID and AUTH_OAUTH_CLIENT_SECRET, the env names plugin/auth binds
    provider_name: auth.oauth.provider has no environment variable and is a startup validation failure when empty, so doctor leaves it to startup rather than inventing a variable name
  passkey_only: skip every provider advisory; the dev.idp advisory still applies because it reads data:project-config rather than the auth section
  jwt_only: skip every provider advisory; requirement:jwt-only-api-authentication owns whatever bearer advisories exist
  unknown_mode: skip the provider advisories, because the enum refuses the value at startup and six advisories about a section nobody selected would bury that one failure
message_shape:
  codes: keep PW0434 and PW0437; the identifier names the situation, and decision:shared-check-catalog makes the message carry the field in force
  titles: PW0432, PW0433, and PW0437 say the provider and the login redirect URL rather than the OIDC issuer and the OIDC redirect URL, so the generated diagnostics page reads correctly for both modes; their anchors change with them
  PW0434: names only the variables of the section in force, so the fix line a reader copies is the one the runtime reads
  PW0437: names auth.oauth.redirect_url or auth.oidc.redirect_url according to the mode
  docs: the diagnostics appendix entries for PW0434 and PW0437 name both spellings, one per mode
shared_predicate:
  rule: the predicates deciding which section a mode uses live once, in plugin/auth, as the exported ModeUsesOIDC, ModeUsesOAuth, ModeUsesPasskey, and ModeUsesJWT, and doctor asks them rather than restating the enum
  reason: the config struct already carries dependon mode lists for oidc, oauth, and jwt; a second list in doctor is the drift this defect is
catalog:
  rule:configuration-advisories: the identity_provider block gains the dispatch above; every trigger that says auth oidc.* reads as the section in force
  api:cli-doctor: unchanged; the check groups already point at the rule
acceptance:
  - an oauth_only prod configuration with oauth.redirect_url absolute and client values empty reports PW0434 naming AUTH_OAUTH_CLIENT_ID and AUTH_OAUTH_CLIENT_SECRET and nothing about oidc
  - an oauth_only prod configuration with oauth.redirect_url empty reports PW0437 naming auth.oauth.redirect_url
  - an oauth_only configuration never reports PW0435 or PW0436 for oidc fields
  - a passkey_only and a jwt_only configuration report no PW0434 and no PW0437
  - an oidc_only configuration reports exactly what it reports today; the existing doctor tests stay green unchanged
  - the doctor test file gains one case per mode above
non_goals:
  - a new diagnostic code for the oauth mode
  - fetching the provider definition or any network call; policy:oauth-security stays a runtime concern
  - changing what the runtime accepts
```
