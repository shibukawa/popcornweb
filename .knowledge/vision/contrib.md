---
id: vision:contrib
type: vision
title: TinyGo Contrib Vision
---
Popcorn Web contrib provides focused TinyGo-compatible server packages where standard or common Go implementations are unavailable, incomplete, or too reflection-heavy.

```yaml
root: contrib/
packages:
  - requirement:contrib-auth-common
  - requirement:contrib-auth-state
  - requirement:contrib-auth-state-memory
  - requirement:contrib-auth-state-redis
  - requirement:contrib-auth-state-sqlite
  - requirement:contrib-auth-state-dynamo
  - requirement:contrib-passkey
  - requirement:contrib-otel
  - requirement:contrib-jwt
  - requirement:contrib-oauth
  - requirement:contrib-oauthprofile
  - requirement:contrib-oidc
  - requirement:contrib-html-template
upstreamed_packages:
  - package: requirement:contrib-cbor
    destination: system:tinygodriver encoding/cbor, from v1.2.6
host_only_packages:
  - package: requirement:contrib-devidp
    reason: development identity provider that never links into an application binary
    exemption: decision:devidp-scope-reduction waives the policy:contrib-compatibility TinyGo matrix
principles:
  - policy:contrib-compatibility
  - policy:outbound-transport-security
  - decision:contrib-delivery-order
  - decision:passkey-first-authentication
acceptance: requirement:contrib-acceptance
compatibility:
  - requirement:contrib-redis-valkey
  - requirement:dynamodb-store
external_networking: requirement:tinygodriver-adoption
external_runtime_compatibility: requirement:tinygodriver-adoption
scope_rule: implement explicit useful subsets; never claim full upstream compatibility without conformance evidence
```
