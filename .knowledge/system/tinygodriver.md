---
id: system:tinygodriver
type: system
title: tinygodriver
---
tinygodriver is the external owner of reusable TinyGo compatibility packages consumed by Popcorn Web.

```yaml
module: github.com/shibukawa/tinygodriver
source: https://github.com/shibukawa/tinygodriver
packages:
  netdev: github.com/shibukawa/tinygodriver/netdev
  https: github.com/shibukawa/tinygodriver/https
  httpmux: github.com/shibukawa/tinygodriver/httpmux
  httprevproxy: github.com/shibukawa/tinygodriver/httprevproxy
  httpserver: github.com/shibukawa/tinygodriver/httpserver
  websocket: github.com/shibukawa/tinygodriver/websocket
  fasthttp: github.com/shibukawa/tinygodriver/fasthttp
  fasthttpwebsocket: github.com/shibukawa/tinygodriver/fasthttpwebsocket
  zstd: github.com/shibukawa/tinygodriver/compress/zstd
  cbor: github.com/shibukawa/tinygodriver/encoding/cbor
  sqlite: github.com/shibukawa/tinygodriver/database/sql/sqlite
  postgresql: github.com/shibukawa/tinygodriver/database/pgx/stdlib, renamed from database/sql/pgxstdlib in v1.1.11, plus database/pgx/pgxpool for the native pool
  mysql: github.com/shibukawa/tinygodriver/database/sql/mysql
  sqlbatch: github.com/shibukawa/tinygodriver/database/sql/sqlbatch
  dynamodb: github.com/shibukawa/tinygodriver/nosql/dynamodb
  datastore: github.com/shibukawa/tinygodriver/nosql/datastore
  google: github.com/shibukawa/tinygodriver/cloud/google
roles:
  netdev: host TCP/IP Netdever registration for TinyGo
  https: net/http-compatible HTTPS client over the OS TLS stack, exposing the in-band upgrade seam the database drivers use
  httpmux: Go 1.22+ ServeMux-compatible routing for TinyGo
  httprevproxy: TinyGo-compatible net/http/httputil.ReverseProxy subset
  httpserver: serves net/http handlers on TinyGo when one of them takes over the connection, from v1.2.3; srv.Serve under host Go, and the reason decision:websocket-upgrade-capable-server is one line rather than a port
  websocket: gorilla/websocket v1.5.3 fork for the net/http transport, from v1.2.3
  fasthttp: the fasthttp fork api:pwfast-package serves on, so the request type is one type rather than two that agree
  fasthttpwebsocket: fasthttp/websocket v1.5.12 fork, the callback-shaped upgrade half; both websocket packages are reached through system:tinybind-websocket rather than directly
  zstd: bounded TinyGo encoder with optimized host fallback, streaming-capable from v1.0.4
  cbor: bounded reflection-free CBOR decoder and deterministic encoder, upstreamed from contrib/cbor in v1.2.6; what requirement:contrib-cbor is satisfied by
  sqlite: portable database/sql SQLite facade selecting a host or TinyGo backend
  postgresql: pgx stdlib driver, vendored with TLS rerouted for TinyGo, from v1.0.6
  mysql: MySQL and MariaDB driver forked from go-sql-driver for TinyGo, from v1.1.0
  sqlbatch: batched statement execution over a *sql.DB, one queue shape with a transport each driver package registers for itself; reached directly by a caller rather than wrapped, per rule:batch-engine-capability
  dynamodb: DynamoDB JSON-protocol client written to build under TinyGo, from v1.1.3; detailed in system:tinygodriver-dynamodb
  datastore: Firestore in Datastore mode over the Datastore v1 JSON API, from v1.1.4 and depended on from v1.1.9; detailed in system:tinygodriver-firestore
  google: Google Cloud credentials and bearer tokens, with the RSA signing split out so a token-only or metadata-only build links none of it; what the datastore client authenticates with
standard_go:
  netdev: no-op registration
  https: crypto/tls
  httpmux: alias of net/http.ServeMux
  zstd: optimized github.com/klauspost/compress backend
  sqlite: host-selected database/sql driver
  postgresql: upstream pgx stdlib, unmodified
  mysql: upstream github.com/go-sql-driver/mysql
tls_backends:
  linux: mbedTLS
  darwin: Secure Transport, with mbedTLS under -tags darwinstarttlswith13
  windows: Schannel
not_consumed:
  storage/s3: an S3 client for targets where aws-sdk-go-v2 does not build; requirement:object-storage wraps it as the s3 backend since 2026-09-03
requests_from_here:
  - docs/tinygodriver-s3-presign-request.md, filed 2026-09-03 and answered in v1.2.12: Presign on the storage/s3 client, and multipart upload with it; requirement:object-storage uses the first and leaves the second to an application that needs it
```
