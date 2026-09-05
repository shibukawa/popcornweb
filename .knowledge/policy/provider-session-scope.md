---
id: policy:provider-session-scope
type: policy
title: Provider Session Scope of Logout
---
Ending the local session and ending the provider session are separate acts, and only the second one signs the user out of every other application sharing that provider.

```yaml
question: what a logout should do to the session the identity provider holds, which is not the session this application owns
naming:
  local_vocabulary: reconfirm and global, and the rejected local below, are names for deployment behavior in this catalog, and no specification defines them
  standardized: only the prompt and max_age parameters of an authorization request, and the RP-initiated logout request
  absent_from_every_specification: logging out of one relying party at the provider while leaving the provider session alive for the others; the provider session is one shared session and nothing can scope its end to one client
  consequence: reconfirm exists because that request cannot be made, so it asks the provider for nothing and changes the next authorization request instead
modes:
  reconfirm:
    action: revoke the framework session, send the provider nothing at all, and mark the next authorization to carry prompt
    request_at_logout: none; this mode is the absence of a logout request plus one parameter on the next ordinary authorization request
    prompt: select_account and login, per the prompt_values of policy:reauthentication
    provider_session: untouched, so every other relying party is unaffected
    next_login: the provider shows who it knows and still demands proof, which is the behavior local logout fails to produce
    fits: the ordinary case for an application sharing a consumer or organizational provider
    intent_storage: a short-lived cookie set by logout and consumed by the login endpoint
    intent_integrity: none required, because prompt can only add interaction; a forged intent costs the user a redundant confirmation and grants nothing
  global:
    availability: the OIDC modes only; flow:oauth-provider-login has no end session endpoint to send the request to
    action: RP-initiated logout against the discovered end_session_endpoint of requirement:contrib-oidc
    effect: ends the provider session, and the provider notifies every other relying party through its registered logout mechanisms
    fits: a deployment that defines sign-out as leaving everything, and every deployment under policy:shared-device-mode, which fixes this mode
    confirmation: the provider decides whether to ask the end user, and must ask when no id_token_hint was sent
shared_primitive:
  fact: reconfirm logout and the step-up of flow:step-up-reauthentication both want an authorization request the provider may not satisfy silently
  difference: step-up keeps the local session and resumes an operation, while reconfirm logout destroys it and lands on the login page
  implementation: one reconfirmation intent carried into the next requirement:contrib-oidc authorization request, not two mechanisms
selection:
  field: auth.oidc.logout_scope, one of reconfirm or global
  default: reconfirm
  shape: a configbind enum tag, which validates the chosen value whatever source set it and which requires the default to be a listed value
rejected_mode:
  name: local, meaning revoke the framework session and leave the provider session alone
  behavior: the next login is silent, so the same user returns without interaction and the sign-out appears not to have worked
  fits: nothing; it is the failure mode that made ending the provider session look like the only alternative
  status: not offered, because reconfirm supplies everything local was reached for and nothing was reached for local itself
removed_setting:
  was: auth.oidc.provider_logout, a bool defaulting to true, read at plugin/auth/endpoints.go
  equivalence: true was global, and false was the rejected local mode
  cost_one: signing out of one application signed the user out of every application sharing the provider, which is rarely what the user asked for
  cost_two: api:authentication-endpoints sends no id_token_hint because the session payload holds no token body, so the provider is required to show a confirmation screen on every logout
  original_reason: local logout left the provider signed in and made the next login silent, which is true and which reconfirm fixes without ending the provider session
removal:
  status: implemented
  compatibility: none is owed before 1.0, so provider_logout is deleted rather than mapped
  hazard: configbind treats a key owned by no definition as an ordinary unknown overlay key and ignores it, so a deleted field goes silently dead rather than failing
  aggravation: api:cli-init wrote provider_logout into every scaffolded configuration, so the stray key is present in every existing project rather than rare
  consequence_if_ignored: a deployment that wrote provider_logout true would run reconfirm while its configuration still reads as global, which is the silently-ignored-security-setting case the data:authentication-runtime-config mode_validation principle exists to prevent
  mechanism:
    approach: the field survives with its default inverted to false, which makes presence detectable without a raw-key lookup the plugin does not have
    untouched_project: binds false and starts
    stale_true: refused at startup with logout_scope and the value to use named
    stale_false: accepted, because false meant the rejected local scope whose nearest surviving behavior is the new default
    rejected_alternative: a pointer field, which configbind has no kind for, and a raw overlay lookup, which would need new framework surface for one transitional check
  lifetime: a transitional check, removable once no configuration in the wild carries the key
  scaffold: api:cli-init writes logout_scope and no longer writes provider_logout
per_request:
  field: auth.oidc.allow_global_logout_request, bool, default false
  purpose: one deployment offering both a sign-out button and a sign-out-everywhere button, which is a user choice rather than a deployment choice
  direction: a request may only escalate toward global and may never downgrade
  reason: escalation costs the user extra sign-outs and nothing else, while a forced downgrade would leave the provider session alive after the user asked to leave it
  transport: a field on the existing same-origin POST logout, so it inherits the method and origin checks api:authentication-endpoints already applies
rules:
  - a logout revokes the local session first and unconditionally, whatever the selected mode does afterward
  - a provider that advertises no end_session_endpoint degrades global to reconfirm, never to a silent logout that leaves the next login unchallenged
  - a typed logout_scope or allow_global_logout_request is refused under every mode that reaches no provider session — passkey_only, which has none, and oauth_only, whose provider offers no way to end the one it holds — per the mode_validation principle of data:authentication-runtime-config
  - the default is not refused there, because every mode binds it; only a value somebody typed is, which is what makes the refusal legible
  - the reconfirmation intent survives only until the next completed authorization, and never becomes a permanent setting
  - a mode is a deployment choice, and an application handler never selects one
  - audit the selected mode with the logout event, without tokens or cookie values
standards:
  authorization_request: https://openid.net/specs/openid-connect-core-1_0.html
  authorization_request_note: section 3.1.2.1 defines prompt and max_age, which are the whole of reconfirm mode
  rp_initiated_logout: https://openid.net/specs/openid-connect-rpinitiated-1_0.html
  rp_initiated_logout_note: the whole of global mode
unusable_neighbors:
  session_management: check_session_iframe reads provider cookies from a cross-site iframe, which third-party cookie blocking prevents, so the specification no longer works in practice
  front_channel_logout: the same cross-site iframe pattern, and equally unusable
  back_channel_logout: server to server, so it still works and remains the way a provider tells this application to end a session
  consequence: the working toolbox is the authorization-request parameters, RP-initiated logout, and back-channel logout, and nothing else
```
