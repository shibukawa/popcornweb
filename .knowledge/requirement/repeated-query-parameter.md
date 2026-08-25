---
id: requirement:repeated-query-parameter
type: requirement
title: Repeated Query Parameter
---
A repeated query key binds as an array, on a page input and in api:request-binding alike, so a checkbox group or a multi-select filter can be read from the URL at all.

```yaml
source: reached from requirement:filter-form-state-round-trip, 2026-08-25
owner: system:tinybind, which owns both decoders
status: implemented 2026-08-26 in both rungs, plus the client surface; released as system:tinybind v0.5.26 and adopted here by the go.mod bump the same day
wire: rule:query-array-wire-format, which fixes the spelling this binds and the ones it refuses
today:
  page_input: requirement:discovered-page-routing declares inputs on the component and takes scalars only, since system:tinybind refuses a non-scalar declared parameter on the ground that a URL carries no object
  handler: api:request-binding plans a slice as a composite, and a composite tagged query, path, header, or cookie is a generation error naming payload and input as the only sources
  the_one_exception: a byte sequence, which binds from a value source as base64 because a blob has a spelling outside a document
  what_happens_instead_of_an_error: a scalar input reading tag=a&tag=b takes the first value through Values.Get and says nothing, so the URL is already reaching pages that cannot represent it
  so: neither rung reads a repeated key, which makes this a uniform gap rather than a disagreement between them
why_the_refusal_was_over_broad:
  premise: a URL carries no object, which holds for a struct and for a map
  not_true_of_a_repeated_key: it is what a URL carries natively, what url.Values already models as a slice, and what a checkbox group and a multi-select submit with no author intervention
  therefore: the array case is separable from the nesting case the refusal was written for
spelling:
  page: the array marker the template language already spells for a component parameter, beside the optional marker the scalar case uses
  handler: a slice of scalar behind an explicit query tag
  untagged_stays_body_bound: an input slice with no tag is the JSON case and does not change, which is what keeps this additive
absent_and_empty:
  problem: a blank control submits its key with an empty value, and rule:query-array-wire-format records that Go cannot tell tag= from a bare tag, so neither may become a one-element array holding the empty string
  consistent_with: the reasoning system:tinybind used to reject presence-by-Has for the scalar optional, where an empty value is the common output of a blank filter field
  shape: an absent key and a key carrying only empty values both produce an empty array
element_failure: an unparsable element takes the invalid query parameter error the scalar case already reports, before rendering
a_scalar_input_meeting_a_repeated_key:
  today: Values.Get returns the first and nothing is reported, which is the silent case above
  open: whether a declared scalar meeting a repeated key should stay first-wins or join the invalid query parameter error
  why_it_is_askable_here_and_not_elsewhere: rule:query-array-wire-format records that a stack without a declaration, Next.js among them, has to hand every reader a union and can raise nothing; a declared arity is what makes the mismatch detectable at all
  argument_for_failing: a page declaring one value and a URL carrying two disagree, and first-wins picks a winner the author never described
  argument_for_first_wins: a stale link or a doubled form field is an ordinary way to arrive at a repeated key, and a 400 on a link that used to work is a harsh answer to it
as_built:
  runtime: one accessor per transport returning every value for a key in URL order, skipping empty ones; the net/http side reads the wire-ordered pair spans api:request-binding already splits once per request, and the fasthttp side visits its own args
  page_input: the declared type carries the arity, so the array marker the template language already spells lowers to a slice and the decoder appends into it
  handler: a slice of scalar behind an explicit query tag; every other composite and every other value source keeps the refusal it had
  openapi: an array schema on the parameter, and nothing else, because form with explode true is already the default and already the repeated spelling
  client: api:client-update-api writes one pair per element for an array argument, so the programmatic path and the GET form path now produce the same query
  verified_end_to_end: a fixture page declaring a slice renders both values of tag=a&tag=b in URL order, drops an empty value, and binds nothing from a bracket-spelled key
unchanged:
  path_segments: a repeated path segment is not expressible, so this touches the query tail only
  openapi: the emitter writes no style or explode, and the OpenAPI default for a query parameter already serializes an array as a repeated key, so an array schema is the whole change
acceptance:
  - a page declaring a string array input receives both values of tag=a&tag=b, in the order the URL wrote them
  - a page declaring one for a URL carrying no such key receives an empty array
  - a page declaring one for tag= receives an empty array rather than one empty string
  - a handler struct binds the same URL through an explicit query tag
  - an int array reached by an unparsable element fails before rendering
  - an untagged slice in a handler struct still binds from the body
  - a bracket-spelled query reports the repeated spelling rather than binding or silently missing
```
