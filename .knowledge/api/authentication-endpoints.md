---
id: api:authentication-endpoints
type: api
title: Authentication Endpoints
---
The runtime mounts login, callback, and logout from data:authentication-runtime-config, so an application registers no authentication route and writes no protocol code.

```yaml
package: github.com/shibukawa/popcornweb/plugin/auth
registration:
  mechanism: importing the package installs session, authentication, and guard extensions through api:framework-extension
  precedent: decision:import-registered-session-plugins
  application_wiring: auth.SetAccountResolver, which the import comes with
  scaffold: api:cli-init writes the resolver, the framework migrations, and the configuration for an OIDC mode
surface:
  - auth.SetAccountResolver(resolver) links a verified identity to an application account, whichever provider flow verified it
  - auth.User(context) returns the stored account summary of the request
  - auth.Session(context) returns the validated session view
  - auth.MigrationSQL() and rdb.MigrationSQL(table) publish the framework tables
endpoints:
  login:
    path: auth.login_path, default /auth/login
    method: GET
    action: begin authorization through requirement:contrib-oidc, or through requirement:contrib-oauth under oauth_only, and store the transaction key, with the return path, in a short-lived cookie
  callback:
    path: auth.callback_path, default /auth/callback
    method: GET
    action: consume the transaction, verify the ID Token — or read the account through requirement:contrib-oauthprofile under oauth_only — and establish the session
  logout:
    path: auth.logout_path, default /auth/logout
    method: POST only
    reason: a logout reachable by link or prefetch is a denial-of-service surface
    action: delete the server-side session, then end the provider session before returning to the landing path
    scope: auth.oidc.logout_scope selects reconfirm or global, defaulting to reconfirm, per policy:provider-session-scope; the block below describes the removed bool it replaces
    provider_logout:
      default: enabled through auth.oidc.provider_logout
      reason: clearing only the local cookie leaves the provider signed in, so the next login silently returns the same user and the sign-out appears to do nothing
      request: RP-initiated logout with client_id and a post_logout_redirect_uri derived from this origin
      no_hint: the session payload holds no token body, so no id_token_hint is available or sent
      fallback: local logout when the provider advertises no end session endpoint, discovery fails, or the request cannot be built
      opt_out: auth.oidc.provider_logout false, for a provider shared with applications that must stay signed in
      resolved: policy:provider-session-scope replaces this bool with logout_scope, adds reconfirm as the default, and drops the false branch entirely; the no_hint cost above is one of its arguments
placement:
  inside: every framework middleware, so recovery, logging, and security headers apply
  resolve: a middleware installs the identity of an existing session on every request
session:
  form: opaque cookie token over a server-side record, per api:session-manager
  payload: the plugin/auth slot of api:session-registry, holding an account summary with no token body and no provider secret
  store: sessionstore/sqlite, verified at startup against rule:framework-owned-tables
  lifetime: auth.session.ttl absolute and auth.session.idle_timeout inactivity, per decision:session-lifetime-owned-by-auth
  rotation: login rotates the token, which revokes whatever the browser held before and preserves every other slot
  logout: destroys every slot, per flow:session-lifecycle
rules:
  - an unknown or expired session cookie yields an anonymous request rather than an error
  - a cross-origin logout is refused even though SameSite already blocks the cookie
  - a return path is accepted only as a rooted same-site path, so login cannot become an open redirect
  - provider errors are logged and returned as a generic unauthorized response
  - the account link is the issuer plus the claim auth.oidc.identity_claim names, never an email address
  - identity carries proof of authentication only; authorization stays with the application
modes:
  oidc_only: implemented; this concept is its whole endpoint surface
  oidc_passkey: these endpoints plus the login and enrollment endpoints of api:passkey-endpoints
  passkey_only: api:passkey-endpoints alone; login_path, callback_path, and the OIDC configuration are absent
  oauth_only: the same login_path and callback_path, over flow:oauth-provider-login; the callback reads an account from the provider instead of verifying an ID Token, and the logout is local because no end session endpoint exists
  jwt_only: no endpoint at all, per api:bearer-authentication; the mode installs a middleware and nothing this concept describes exists in it
  logout: shared by every browser mode, because a session is mode-neutral once created; jwt_only has none, because a credential this framework never issued is not one it can end
  selection: data:authentication-runtime-config mode_validation decides which endpoints mount and which fields are read
  status: every mode serves; api:cli-init still records passkey_only with auth.enabled false until its scaffold exists
guard:
  paths: auth.protection.include requires a session, everything else stays public
  unauthenticated: redirect through the login and return, or answer 401
security: policy:oauth-security and policy:oidc-security through requirement:contrib-oidc
transport_seam_2026_08_11:
  type: auth.Exchange, everything these endpoints need from the transport carrying one request
  precedent: session.Carrier, one layer up and for the same reason; a session touched three operations, and these touch a query, a form field, a body, a cookie, a redirect and a problem
  why_an_interface_rather_than_a_second_port: what would be duplicated is a login, and two implementations of when a transaction cookie is consumed, or of which failures answer 403 rather than 400, are two chances to leave a hole in one of them
  currency: net/http's cookie and header vocabulary, exactly as Carrier's is, because http.Cookie describes a cookie rather than implementing one
  halves:
    net_http: auth.HTTPExchange, and the extension registration is unchanged
    fasthttp: popcornweb/plugin/auth/authfast, which supplies the exchange, the frame, and the pwfast.GuardPolicy the chain installs
  entry_points:
    setup: auth.Setup for a runtime that owns startup; authfast.Setup wraps it
    installed: auth.Endpoints and authfast.Installed for a process whose startup already ran on the other runtime, so one runtime serves two listeners rather than two runtimes serving one application
  what_moved_out_of_the_endpoints:
    session: Manager gained AttachTo, RotateOn and DestroyOn, which take a carrier or the context the session was attached to; the response writer Rotate and Destroy took was never read
    jar: LoadFrom beside SaveTo and ClearFrom
    authentication: pwruntime.StoreAuthentication beside WithAuthentication, the write half for a transport that cannot derive
    guard: auth.Rules, the resolved protection policy as a value, because the frame that applies it is each transport's own
    assurance: the Requirement interface resolves against an Exchange; Dynamic keeps its net/http shape and DynamicOn is the neutral one, and a net/http-shaped requirement reports unresolved on another transport rather than admitting
  fixed_on_the_way:
    destroy_nil_deref: Destroy read current.carrier on the nil the state lookup had just reported, so a destroy with no session middleware panicked instead of returning
    redirect_body: the fasthttp Redirect wrote no fallback body, because that transport reports a default content type where net/http reports none; found by the cross-transport agreement test
  still_shared_with_pw: configuration binding has not moved to a shared package, so authfast links plugin/auth and therefore pw; requirement:alternate-http-backend-readiness calls that a legitimate intermediate and names the configuration layer as what closes it
  verification:
    authfaste2e: the OIDC round trip, the passkey ceremonies, the guard, logout, forget and presence, over fasthttp against a real provider and a real database
    authfastjwte2e: the bearer mode, in a second binary because a mode is a setting and configuration is parsed once per process
    agreement: both listeners are asked the same question and status, body and headers are compared
```
