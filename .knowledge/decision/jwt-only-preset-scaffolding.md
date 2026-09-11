---
id: decision:jwt-only-preset-scaffolding
type: decision
title: JWT-Only Is Scaffolded By a Preset, Not By a Question
---
requirement:jwt-only-api-authentication is offered as the api-server preset of requirement:init-presets, reversing the part of decision:jwt-only-mode-not-scaffolded that held no command may write it, while keeping the part that keeps it out of the authentication question.

```yaml
status: accepted
decided: user 2026-08-05
amends: decision:jwt-only-mode-not-scaffolded
what_that_decision_got_right:
  keeps_manual: the authentication row of decision:navigable-answer-hub does not offer jwt_only, so the Manual path cannot reach the mode; its values stay none, oidc, oidc-passkey, passkey, and oauth
  keeps_flag: --auth does not accept it, and an unrecognized value is rejected rather than passed through
  keeps_catalog: it stays out of the requirement:incremental-project-capabilities catalog, so api:cli-add never lists it and the auth capability still means the browser login
  keeps_doctor: api:cli-doctor never suggests it
  reason_all_four_survive: the argument that an offered option is a recommended one holds for a question inside a shared path; a project answering the authentication row is a browser application for which this mode is wrong
  where_the_line_falls: the mode is reachable by naming the project shape it belongs to, and not by answering a question every other project also answers
what_changed:
  claim: a scaffold cannot supply the three things the mode depends on, so a scaffolded project either refuses every request or admits everyone
  answer:
    issuer_and_audience: left empty, and startup fails naming the empty field, which is exactly what the external-provider answer of the OIDC question already does; the mode has no permissive default and this scaffold introduces none
    admission: policy:bearer-admission authenticated, whose own documented fit is an internal API whose issuer only mints tokens for people already entitled to it; with the issuer empty until an operator names one, admitting everyone that issuer verified is the operator's own boundary rather than a guess this scaffold made
    runnable: policy:dev-token-relaxation admits a hand-written token from loopback under pw dev, so a scaffolded project is developable against with no authorization server, which is what the original argument said was impossible
  standing: none of the reopen_when conditions of decision:jwt-only-mode-not-scaffolded were met; this reverses the conclusion on the dev-relaxation and empty-field grounds above rather than on one of them
why_a_preset_and_not_a_question:
  question: appears in the path of every project, including the browser applications the mode is wrong for
  preset: appears once, at the top, named for the project shape it is correct for, beside the shapes it is not
  effect: the reader who should not choose it never has to decline it, and the reader who should does not have to find it in a reference page
  this_is_the_original_argument: the earlier decision reasoned that offering is recommending; a preset list is a place where recommending one shape to one reader is the whole point
consequences:
  - the policy:dev-token-relaxation rule that api:cli-init never scaffolds auth.jwt.dev is replaced by requirement:api-server-scaffold, which writes it into config.dev.toml only
  - a project created from any other preset still cannot reach this mode through a command, and reaches it by writing the configuration
  - requirement:preset-customization-docs carries the mode where the tutorial does not, which the earlier decision already required
rejected_keeping_it_unscaffolded:
  form: leave the mode configuration-only and document it
  why_not: the hand-written block was already the thing most likely to drift, which is the argument requirement:incremental-project-capabilities makes for every other capability, and an API server is a common enough shape that it earns a name in the list
rejected_adding_it_to_the_auth_question:
  form: a fifth value in the authentication question and in --auth
  why_not: unchanged from decision:jwt-only-mode-not-scaffolded; it would put the mode in front of every browser application, and its second answer would scaffold a project shape the rest of that wizard path contradicts
```
