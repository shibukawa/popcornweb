---
id: rule:configuration-advisories
type: rule
title: Configuration Advisories
---
The checks data:diagnostic-report renders as findings: a wiring gap the process would reject, and a merged value that is allowed but inadvisable for the diagnosed data:runtime-environment.

```yaml
severity:
  error: the process would refuse to start, or a policy forbids the value in the diagnosed environment
  warning: the process starts, and the value is inadvisable for the diagnosed environment
  note: the value is intentional and worth stating, such as an unused linked plugin
  not_a_finding: an arrangement working as designed, whose only remedy would be "no action"; it is left out rather than reported, because a report full of them is one a reader skims
environment_model:
  dev_only: the advisory fires for every token other than dev, because data:runtime-environment treats an unknown token as a deployment
  prod_only: reserved for a policy that names prod, as policy:devidp-safety does
  dev: the advisory fires only for dev, to state an arrangement that exists only there
  every: the advisory is environment-independent
  rule: no advisory hard-codes a list of deployment names; it tests whether the diagnosed token is dev
  source: the token comes from the api:cli-doctor --env option, so one run can evaluate the same file set as dev and as prod
wiring:
  missing-session-backend-plugin:
    trigger: data:session-runtime-config backend names a backend no linked plugin registered
    severity: error
    remedy: the blank import of that plugin, per decision:import-registered-session-plugins
  missing-sql-driver:
    trigger: a data:database-connection-set DSN scheme that no linked database/sql driver answers
    severity: error
    remedy: the system:tinygodriver package for that scheme, named per requirement:contrib-sqlite and its siblings
  unclaimed-config-key:
    trigger: a configured section whose owning plugin the application does not link
    unit: the top-level section, because that is what a plugin registers a binding for
    configured: at least one key of the section came from a place other than its default, since a linked binding contributes defaults to every project
    unattributable: a section no known binding declares is a limit rather than a finding, because a plugin outside the framework may own it
    severity: error
    remedy: the plugin import for the section
    reason: data:runtime-config-registry rejects configuration for an unimported plugin, and the value of the advisory is naming which import is missing
  missing-netdev-registration:
    trigger: data:project-config project.toolchain is tinygo and the tinygohelper.go blank import of rule:tinygo-runtime-compatibility is absent
    severity: error
    remedy: the netdev blank import, because the binary otherwise builds and exits with "Netdev not set"
  devidp-linked-into-application:
    trigger: application source imports requirement:contrib-devidp
    severity: error
    reference: policy:devidp-safety, which api:cli-build already enforces
  unused-linked-plugin:
    trigger: a linked plugin no configuration selects
    severity: note
    reason: it costs binary size and configuration surface without serving a request
dependency:
  disabled-dependency:
    trigger: an enabled feature whose required middleware or binding is disabled, per data:middleware-runtime-config
    severity: error
  auth-without-session:
    trigger: data:authentication-runtime-config enabled while data:session-runtime-config is not
    severity: error
  session-rdb-without-middleware:
    trigger: session rdb source middleware while middleware.rdb.enabled is false
    severity: error
  framework-write-on-readonly-group:
    trigger: a resolved write, session, or migration pointer selecting a readonly group, or an ambiguous one, per policy:connection-group-selection
    severity: error
  deferred-selection:
    trigger: a value data:session-runtime-config or data:authentication-runtime-config lists as deferred, such as the redis backend or passkey_only mode
    severity: error
    remedy: the implemented value, named
environment:
  query-diagnostics-outside-dev:
    trigger: data:query-diagnostics-config enabled resolves on
    scope: dev_only
    severity: warning
    message: names the environment, bind values, and slow threshold, matching the policy:query-log-safety startup warning
  bind-values-outside-dev:
    trigger: bind_values resolves on
    scope: dev_only
    severity: warning
    reason: bind values are the only path by which application row data enters a framework SQL record
  insecure-session-cookie:
    trigger: session cookie.secure false, or same_site none without secure
    scope: dev_only for the first, every for the second
    severity: error
    phase: doctor and startup, so a deployment refuses to serve one session over plain http rather than being advised about it
    dev_silence: api:cli-init writes the false into the development file on purpose, and an advisory whose remedy is the arrangement working teaches a reader to skim
  csrf-disabled:
    trigger: security csrf.enabled false, which is its default
    scope: dev_only
    severity: warning
    remedy: the include and exclude patterns that turn it on, per policy:csrf-protection
  response-headers-weakened:
    trigger: security headers.enabled false, or hsts.enabled false
    scope: dev_only
    severity: warning
    reference: policy:security-response-headers
  memory-database-outside-dev:
    trigger: a sqlite in-memory DSN in any group
    scope: dev_only
    severity: warning
    reason: the schema and every row are lost at restart
  local-public-read-outside-dev:
    trigger: server public.read_local true
    scope: dev_only
    severity: warning
    reference: decision:development-public-assets, which forces the flag only in the development build mode
  verbose-log-level-outside-dev:
    trigger: observability minimum_level trace or debug
    scope: dev_only
    severity: warning
  plaintext-logs-outside-dev:
    trigger: observability stdout_format plaintext
    scope: dev_only
    severity: note
  boot-log-tree-outside-dev:
    trigger: observability boot_log resolves tree
    scope: dev_only
    severity: note
    reason: policy:startup-summary auto already resolves record off a terminal, so an explicit tree is the only case
  telemetry-export-disabled-outside-dev:
    trigger: observability otel.enabled false
    scope: dev_only
    severity: note
    reference: requirement:modern-observability
secret_material:
  basis:
    classification: the configbind metadata already marks a field secret, because policy:log-emission and policy:startup-summary redact by that mark; these advisories read the same mark instead of a list of names
    fields_today: DSN credentials, auth oidc.client_secret, observability otel.headers, and the data:session-runtime-config session keyring
    cookies:
      was: the framework held no cookie signing or encryption key, because a login session was a server-side data:session-record and the CSRF secret is per-session crypto/rand
      now: decision:slot-declared-placement makes the keyring first-class, since session.ReadOnly signs and session.Private seals, so the framework holds a long-lived key that a file can pin
      covered: automatically, because the field carries the secret mark and this advisory reads the mark rather than a list of names
    intent: keying the check to the classification means a future signing key needs no new advisory
    cookies: no cookie key is configured while every CSRF secret lives in a server-side data:session-record; decision:anonymous-csrf-secret-storage needs one, and it is the policy:cookie-value-protection keyring rather than a key of its own, so it is covered the moment that keyring's metadata marks it secret
    intent: keying the check to the classification means a cookie keyring needs no new advisory
    credential_test: classification is by field name, so it marks every DSN; a value holding no user info and no credential parameter, such as a sqlite path, discloses nothing and is skipped, because a finding a reader learns to ignore costs the ones that matter
  where_a_secret_is_kept:
    members: literal-secret-in-config-file, secret-file-not-ignored, secret-file-permissions
    scope: dev_only
    reason: a development machine keeps the password of the database running beside it in config.dev.toml on purpose, and that file is written to be shared with the people who run it; what a secret is, rather than where it sits, stays a finding in every token
  literal-secret-in-config-file:
    trigger: a secret-classified field whose place is the TOML file as a literal, rather than an environment variable, a CLI argument, or a ${NAME} expansion
    scope: dev_only
    severity: error
    evidence: the key, the file, and whether that file is tracked in version control when the project is a git work tree, since tracking is what turns a fixed value into a disclosure
    remedy: the ${NAME} expansion of data:database-connection-set for an array element, or the generated environment variable name for a scalar key
  scaffolded-or-placeholder-secret:
    trigger: a secret-classified literal still equal to what requirement:built-in-config-generation or api:cli-init wrote, or to a known placeholder word
    scope: every
    severity: error outside dev, warning in dev
    reason: a scaffold value is published in the framework source, so it is a known credential rather than a weak one
    bound: shape only; doctor judges no value's strength, and an undeterminable secret yields no finding at all
  secret-shared-between-environments:
    trigger: one literal secret value appearing in the files of more than one diagnosed token
    scope: every, evaluated only when api:cli-doctor diagnoses more than one token
    severity: error when any of those tokens is not dev
    reason: promoting a value across environments erases the boundary the separate files exist to draw
    visibility: only a reader of every environment file sees this, which is why it is a doctor check and not a startup one
  secret-file-not-ignored:
    trigger: a file this analysis found carrying a secret-classified literal that the project's git work tree tracks or does not ignore
    scope: dev_only
    severity: error
    bound: the file set comes from what was actually read, which is the selected TOML and the policy:dotenv-resolution files of the diagnosed token; no advisory assumes a file neither policy defines
  env-template-holds-secret:
    id: PW0438
    trigger: .env.example assigns a non-empty value to a variable whose name carries a secret token, and the value carries a credential by the same test the TOML checks use
    scope: every
    severity: error
    reason: the template is the one dotenv file .gitignore lets through, so a value there is a committed credential whatever the environment, per requirement:dotenv-files
  secret-file-permissions:
    trigger: a file carrying a secret-classified literal that is readable beyond its owner
    scope: dev_only
    severity: warning
    reason: it is the cheapest half of the same disclosure question, and it is a check only a reader of the filesystem can make
  required-secret-not-set:
    trigger: a secret-classified field with no default, no file value, and no value in this host's environment, for a non-dev token
    scope: dev_only
    severity: error on a deployment host, note otherwise
    host_dependence: the severity turns on whether this host is the one holding the deployment's environment, per decision:host-side-diagnostic-analysis
    use: this is what makes pw doctor --env prod a pre-deploy CI gate rather than an advisory run
  deferred:
    key_rotation:
      intent: a check that the configuration can hold a verifying key alongside a signing key, so a rotation does not require downtime
      blocked_on: no framework binding expresses a key set today, and the schema decision comes before the check
      note: doctor cannot check a shape the configuration cannot express, which is why this is listed rather than written
proxy:
  basis:
    normal: decision:local-tls-proxy-boundary terminates TLS upstream, so a deployment reaching this framework over plain http is the designed arrangement and never a finding on its own
    what_doctor_sees: the configuration; whether a proxy actually sits in front is a runtime observation, which is why one member of this group is a startup check reading the first request instead
    subject: requirement:proxied-request-identity, whose every failure mode is a value nobody set
    determinacy: decision:ingress-tls-termination makes the boundary a declared fact, so these checks test a configured value rather than guessing which shape they are looking at; before that decision lands, every severity here is one step weaker than written, because a directly served deployment could not be told apart from a proxied one
    no_suppression: this group carries no per-advisory off switch, per the no_advisory_off_switch clause of decision:ingress-tls-termination; a declared boundary is what silences these, because it stays true when the deployment changes and a mute does not
  trusted-proxies-unset:
    trigger: server.trusted_proxies empty while security headers.enabled and hsts.enabled
    scope: dev_only
    severity: warning
    reason: decision:forwarded-header-trust treats an ungated header as absent, so HSTS is silently never emitted behind a TLS-terminating proxy, and the missing header is the one nobody notices
    remedy: the proxy network in server.trusted_proxies
    bound: doctor cannot tell a proxied deployment from a directly served one, so this fires on configuration alone and is a warning rather than an error
  forwarded-header-from-untrusted-peer:
    trigger: a request carrying X-Forwarded-Proto or X-Forwarded-For from a peer outside server.trusted_proxies
    phase: startup, because no configuration shows it and the first request does
    scope: dev_only
    severity: warning
    reason: the deployment is behind a proxy its configuration does not declare, so every value of requirement:proxied-request-identity is degraded at once
    once: reported on the first such request and not again, since a per-request advisory is a log flood rather than a finding
    remedy: the observed peer address, named, so the remedy is a value to paste rather than a network to work out
  csrf-trusted-origins-unset:
    trigger: security csrf.enabled with an empty csrf.trusted_origins and no declared server.tls
    scope: dev_only
    severity: error
    reason: policy:csrf-protection matches against a declared origin, and a deployment terminating no TLS of its own reconstructs http while the browser reports https, so every unsafe request is refused
    remedy: the deployment's own https origin
    not_owed_by_auth: api:authentication-endpoints derives its origins from the passkey allowlist and the OIDC redirect URL, so one deployment configures this and needs nothing there; the advisory says so, because the asymmetry reads as an oversight otherwise
    why_error_rather_than_warning: the trigger names the declared boundary of decision:ingress-tls-termination, so this is not a guess about deployment shape; it is a configuration whose every unsafe request is already refused
    before_that_decision: warning, because without a declared boundary a directly served deployment cannot be told from a proxied one
  rate-limit-no-process-ceiling:
    trigger: data:rate-limit-runtime-config enabled with process zero
    scope: dev_only
    severity: warning
    reason: the per-address bucket cannot see a distributed flood, since such a flood keeps every source under it by construction, so a zero ceiling leaves that case to the edge alone
    remedy: a ceiling sized against what this deployment can serve, which is why it defaults to zero rather than to a guess
    not_an_error: a deployment whose edge already sheds volumetric load has made a defensible choice, and doctor cannot see that rule
  rate-limit-store-in-process:
    trigger: requirement:rate-limit-enforcement enabled with the in-process counter store
    scope: dev_only
    severity: warning
    reason: N replicas each enforce the configured limit, so the effective limit is N times what the deployment declared; the configuration is correct on one replica and wrong on the shape decision:local-tls-proxy-boundary assumes
    remedy: a shared counter backend, requirement:contrib-redis-valkey being the first
  live-bound-unenforceable-behind-proxy:
    trigger: html.live_max_responses positive while server.trusted_proxies is empty
    scope: dev_only
    severity: warning
    reason: the client_key of policy:live-subscription-bounds falls back to the remote address, which behind a proxy is one bucket per proxy node, so the bound refuses ordinary visitors instead of bounding one
    remedy: the proxy network in server.trusted_proxies, which is the same remedy as trusted-proxies-unset and is reported once
identity_provider:
  basis:
    dev: requirement:contrib-devidp is a legitimate dev issuer that api:cli-dev injects, so an empty auth oidc section in config.dev.toml is correct rather than missing
    deployed: policy:oidc-security requires an HTTPS issuer and exact issuer equality, so a deployed token needs a real provider declared somewhere
    static_bound: doctor checks the shape of the issuer and the agreement of the local paths; it fetches no discovery document, because that is the network call the deferred probe of decision:host-side-diagnostic-analysis owns
    section_in_force: every trigger below that names an oidc field reads the provider section auth.mode selects, per requirement:doctor-auth-mode-awareness; oauth_only reads auth.oauth and its AUTH_OAUTH_* names, and passkey_only and jwt_only skip the provider advisories
  devidp-enabled-outside-dev:
    trigger: data:project-config dev.idp.enabled with a non-dev token
    scope: dev_only
    severity: error in prod per policy:devidp-safety, warning otherwise
  development-issuer-outside-dev:
    trigger: auth oidc.issuer whose host is loopback, or which matches the data:devidp-config issuer of this project
    scope: dev_only
    severity: error
    reason: policy:devidp-safety states the development provider authenticates nobody
  insecure-issuer-outside-dev:
    trigger: auth oidc.issuer with an http scheme, or oidc.allow_loopback_http true
    scope: dev_only
    severity: error
    reference: policy:oidc-security, which permits http only for explicit loopback development
  loopback-development-pairing-outside-dev:
    trigger: oidc.allow_loopback_http true together with session cookie.secure false
    scope: dev_only
    severity: error
    reference: the development-only pairing data:authentication-runtime-config documents
  redirect-target-disagreement:
    trigger: auth oidc.redirect_url whose path is not callback_path, or whose host is loopback for a non-dev token
    scope: every for the path, dev_only for the host
    severity: error
    reason: the provider would redirect to a URL the application does not serve, and a loopback redirect that works locally hides it
  provider-not-declared:
    trigger: auth enabled for a non-dev token while issuer, client_id, or client_secret is determinable from no layer this host can read
    scope: dev_only
    severity: note
    message: names the data:authentication-runtime-config environment variables the deployment must set, so a reader confirms them instead of assuming them
    reason: an absent value here is either platform injection or a real gap, and doctor cannot tell which; naming what must be set is the useful half
rules:
  - an advisory that restates a startup validation failure carries that failure's message, so doctor and api:application-lifecycle never disagree
  - an advisory evaluates a merged value and its place, never a value it assumed
  - a value marked as a limit by decision:host-side-diagnostic-analysis suppresses its advisories and is listed as a suppression, because a false positive costs more than a missing line
  - a wiring advisory fires only on a registration the import graph resolves; an unresolvable one is a limit, not a gap
  - every advisory carries a remedy that names an import path, a key, or an api:cli-add capability
  - a secret-classified value is compared but never rendered, so a cross-environment match names the keys and the files and no value
  - a secret advisory reports the place of a value, not the value, which is why it works the same for a credential doctor must not print
  - severity is a function of the diagnosed token and the advisory scope, and nothing else
  - an advisory is skipped, not passed, when the feature it examines is off
extension:
  framework: this catalog
  plugin: a plugin registering a binding registers the advisories for its own keys, because only it knows which of its values are development-only
  application: no application-defined advisories in the first release
```
