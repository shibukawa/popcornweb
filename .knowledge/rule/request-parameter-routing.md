---
id: rule:request-parameter-routing
type: rule
title: Request Parameter Routing
---
A -d pair given to api:cli-request goes where the matched data:operation-catalog entry reads it from, and goes where curl would send it when no entry matched, so a curl line pasted in still works and a named parameter needs no knowledge of the handler.

```yaml
proposed_by: requirement:cli-request
inputs:
  pair: -d key=value, a name and a string
  raw: -d with no equals sign, an @file, or a leading { or [, taken as a whole body
  form: -F key=value or key=@file, always a multipart field
with_operation:
  path: a pair naming a {segment} of the template fills it; a missing required segment is a usage error, because a request to /api/users/{id} literally is never what was meant
  query: a pair naming a query parameter is appended to the query string, repeated when given twice, per requirement:repeated-query-parameter
  header: a pair naming a header parameter becomes a header
  cookie: a pair naming a cookie parameter joins the Cookie header
  body:
    json: the remaining pairs become one object under the declared media type when it is application/json, each value coerced by the schema type: integers and numbers parsed, booleans parsed, strings kept, arrays split on repeated pairs; a value that fails coercion is sent as a string and reported
    form: the remaining pairs are urlencoded when the media type is a form
    multipart: -F fields are sent as multipart whatever the operation declares, and a -d pair beside -F is a usage error, as in curl
    raw: a raw body is sent as given with the declared media type unless -H names one
  unknown_pair: a pair naming nothing in the entry is sent in the body position and reported; --strict turns it into a usage error, which is what an agent wants
  explicit_header_wins: a -H Content-Type overrides the declared media type, so a caller testing a wrong content type can
without_operation:
  semantics: exactly curl; -d pairs are urlencoded into a form body and the method becomes POST, -G moves them to the query, --json sends them as a JSON object
  reported: the report says no operation matched and why, so the fallback is never silent
  reasons: catalog unavailable, path not in the catalog, --no-catalog given
why_the_split:
  - curl flags keep curl meaning, so a line copied from a shell history or a tutorial sends the same bytes
  - the catalog adds knowledge, and knowledge should only ever move a value to where the handler reads it; it never changes what a flag means when the knowledge is absent
  - the report makes the routing visible, because a value that went to the query when the caller meant the body is a wrong test that looks like a failing handler
rejected:
  positional_pairs: httpie-style key=value words without -d, which read well but make a bare word ambiguous with the path and the operationId
  location_prefixes: spellings like path:id=7 or query:verbose=true; the catalog already knows the location, and a caller who has to say it gains nothing over curl
```
