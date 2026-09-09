---
id: system:go-envparse
type: system
title: go-envparse
---
github.com/hashicorp/go-envparse is the dotenv parser behind policy:dotenv-resolution, chosen over godotenv and gotenv because it builds and runs under TinyGo without regexp and costs the requirement:cloudflare-workers-build-target bundle 22 KB rather than 450 KB.

```yaml
module: github.com/hashicorp/go-envparse v0.1.0
license: MPL-2.0, a file-level copyleft that does not reach the program importing it
imports: bufio, bytes, fmt, unicode/utf8, and unicode/utf16, nothing else
grammar:
  - NAME=value with an optional export prefix and # comments
  - unquoted, single-quoted, and double-quoted text mixed within one value; double quotes take JSON escapes
  - a later assignment of the same name wins
  - no ${NAME} expansion, no shell quoting beyond the above, no YAML forms
  - a parse error carries the line number, which the read reports beside the file name
verified_2026_09_08:
  toolchain: tinygo 0.42.0 with go 1.27
  builds: native darwin, -target wasm, and -target wasip1, each run with identical output
  wasm_size: 744 KB, against 722 KB for a hand-written parser and 1166 KB for godotenv, which pulls regexp and os/exec
rejected:
  godotenv: the de facto library, but regexp and os/exec, and ${VAR} expansion the policy excludes
  gotenv: regexp and x/text for the same grammar
  hand_written: 22 KB smaller and no license question, but one more parser to keep correct
home: system:tinybind 0.5.31 reads LoadOptions.EnvFiles with this parser; the framework parses the same files once more, only for the token, the APP_ENV warning, and the environment its own array variables decode from
```
