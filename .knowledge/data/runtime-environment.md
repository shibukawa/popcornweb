---
id: data:runtime-environment
type: data
title: Runtime Environment
---
A single process-level environment token selects deployment-specific configuration before any configbind target is loaded.

```yaml
selection:
  source: APP_ENV environment variable, then APP_ENV in ./.env and ./.env.local when the process did not set it, per policy:dotenv-resolution
  default: dev when neither sets it
  scope: process-level, resolved once before api:runtime-configuration ParseConfig
known_values:
  dev: local development
  stg: staging
  prod: production
extension:
  allowed: any additional token beyond the known values
  charset: lowercase letters, digits, hyphen, underscore
  rejected:
    - dot
    - path separator
    - uppercase
    - whitespace
validation:
  invalid_token: startup error before configuration load
  unknown_but_valid_token: accepted without warning
consumers:
  - policy:config-file-resolution local file naming
  - api:runtime-configuration exposed value
  - api:cli-dev default environment
rules:
  - the token is data, not a feature switch; framework behavior keys off explicit configuration fields
  - the resolved token is immutable for the process lifetime
  - the token is safe to log unredacted
```
