---
id: decision:developer-loop-watch-scope
type: decision
title: Developer Loop Watch Scope
---
api:cli-dev watches the module rather than the decision:explicit-generation-sources lists, because a rebuild is triggered by any Go source, and the scope is trimmed with an exclude list instead of an include list.

```yaml
status: accepted
why_wider_than_generation:
  generation: reads only what it generates from, so an explicit list is exact
  developer_loop: restarts the application when any compiled input changes, including main, an ordinary package, and a file no purpose generates from
  consequence: an include-only watch scope would silently stop reloading on a change to a package nobody listed
form:
  key: dev.watch
  includes: extra relative paths and glob patterns added to the walk, for inputs the walk does not reach
  excludes: subtrees skipped during the walk, added to the built-in list
  built_in_excludes: .git, vendor, node_modules, .devbox, and the requirement:public-asset-delivery public tree
  defaults: both lists empty, because the module walk is already the right default
replaces:
  key: dev.extra_watch
  reason: an exclude list is meaningless without its include counterpart in the same block, and the pair reads as one scope
excludes_motivation:
  problem: a project that installs a large dependency tree makes the walk the slowest step of every loop iteration
  scope: exclusion is by directory subtree, since that is what makes a walk expensive
watched_by_default:
  - Go sources, for rebuild
  - .pw.html and .pw.sql, for regeneration
  - a new or removed concept:page-tree route, which needs nothing added: the walk compares files rather than subscribing to events, and a route always carries the page template that makes it one
  - popcornweb.toml, the policy:config-file-resolution project-local files, and the policy:dotenv-resolution files
  - the data:migration-source directory and the data:devidp-config file when enabled
rationale:
  - the wide default is what makes the loop trustworthy, and the narrow one is what makes it fast; only the second is safe to leave to the operator
  - a wrong exclude costs a missed reload the operator can see, while a missing include costs a reload that never happens for a reason nobody states
non_goals:
  - matching the generation scope
  - watching outside the project root
  - per-file rather than per-subtree exclusion
```
