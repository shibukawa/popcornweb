---
id: rule:query-array-wire-format
type: rule
title: Query Array Wire Format
---
A repeated key is the array spelling this framework reads and writes, because it is what a browser submits and what net/url models; the bracket conventions of other stacks are literal key characters to Go and bind nothing.

```yaml
source: verified 2026-08-25 against go1.26 net/url, node URLSearchParams, python urllib, and ruby CGI; reached from requirement:repeated-query-parameter
normative_producer:
  who: the browser, which no decoder gets to negotiate with
  what: the urlencoded serializer writes one name=value pair per successful control in tree order, so a checkbox group or a multi-select named tag submits tag=a&tag=b, with no brackets and no separator
  consequence: a decoder that wants to read a filter form must read the repeated key; every other spelling is a convention some producer opted into
go_standard_library:
  parse: url.ParseQuery collects a repeated key into the slice at that key, in URL order
  read_one: Values.Get returns the first value, so a scalar input reading a repeated key silently takes the first rather than reporting anything
  presence: Values.Has separates absent from present-and-empty, which Get cannot
  encode: Values.Encode writes one pair per value and keeps value order within a key, but sorts the keys
  empty_value: tag= and a bare tag both parse to a one-element slice holding the empty string, and nothing downstream can tell them apart
  semicolon: rejected since go1.17; ParseQuery returns an error and discards the whole result, so a semicolon-separated query is not an alternate spelling anyone may rely on
bracket_conventions_are_foreign:
  spelling: tag[]=a&tag[]=b, and the indexed tag[0]=a&tag[1]=b
  origin: PHP and the Rack family parse brackets into an array or a nested object; it is a parser convention rather than part of the urlencoded serialization
  in_go: the brackets are ordinary key characters, so ParseQuery yields a key named literally tag[], Get("tag") returns the empty string, and Encode writes it back percent-encoded as tag%5B%5D
  same_in_the_browser: URLSearchParams agrees, and getAll("tag") over a bracket query is empty
  authoring_hazard: tag[] is not a legal parameter name in a page signature nor a plausible struct tag, so an author carrying a PHP idiom over gets a silent miss rather than a diagnostic
  therefore: brackets are not accepted, and a diagnostic naming the repeated spelling is worth more than tolerating them
comma_is_not_a_separator:
  in_go: tag=a,b parses to one value, the string a,b
  percent_encoding_changes_nothing: tag=a%2Cb parses to that same single value, because ParseQuery and URLSearchParams both unescape while parsing, so any splitter downstream of them sees a comma it cannot attribute
  therefore_the_joined_spelling_is_lossy: an element containing a comma round-trips only if the producer escapes it inside the element before joining, which means double-escaping; tag=a%252Cb decodes to the one value a%2Cb, leaving a second unescape for the splitter to perform
  described_by_openapi: style form with explode false, which the emitter does not write and this framework does not read
  who_takes_this_route: nuqs, the URL-state library for Next.js, whose parseAsArrayOf joins on a comma by default and whose documentation does not address an element containing one
  not_next_js_itself: the App Router hands a page searchParams in which a repeated key is already an array, so the framework is on the repeated spelling and the joined one is a library layered over it
  therefore_here: an element carrying a comma stays one element, and a project wanting the joined form parses that one string itself
arity_is_a_property_of_the_url_rather_than_of_a_declaration:
  elsewhere: Next.js types a search parameter as string, string array, or undefined, because nothing declares a key's arity and the URL may carry either; every read site narrows
  here: requirement:repeated-query-parameter puts the arity in the signature, so reading one value and reading many are different declarations and no read site narrows
  the_question_that_creates: what a scalar declared input does when the URL repeats its key, which Values.Get answers silently today
this_frameworks_client:
  get_form: requirement:query-navigation-interception builds the query as URLSearchParams over the form's FormData, which preserves a repeated name in DOM order, so the intercepted path emits exactly what the native submit emits
  key_order_differs_from_the_server: URLSearchParams keeps insertion order where Values.Encode sorts, so a URL the server composed and one the client composed from the same values can differ as strings while meaning the same thing
  programmatic_update: api:client-update-api appends one pair per element of an array argument, so it writes the same repeated key the form path does; it set each parameter through String(value) until 2026-08-26, which comma-joined an array into a value no form could have produced
openapi:
  default: a query parameter carrying no style or explode is form with explode true, which serializes an array as a repeated key
  consequence: the emitter needs an array schema and nothing else for the document to describe this spelling correctly
compared_with_other_standard_libraries:
  verified_here:
    python: urllib.parse.parse_qs collects a repeated key into a list, treats brackets as literal, and drops an empty value unless keep_blank_values is set
    ruby: CGI.parse collects a repeated key into an array and treats brackets as literal
    javascript: URLSearchParams.getAll returns every value where get returns the first, which is the Values pair again
  not_executed_here: PHP and Rack, whose bracket handling above is recorded from their documented behaviour rather than from a run
  reading: the repeated key is the one spelling every listed parser agrees on, which is why it is the one to bind
```
