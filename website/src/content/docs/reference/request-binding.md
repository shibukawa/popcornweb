---
title: Request Binding
description: Every struct tag pw.Parse reads, the field types it can fill, and the rules that decide which part of a request each field comes from.
sidebar:
  order: 1
---

`pw.Parse[T](r)` fills one struct from one request. Nothing about that is
decided at run time: `pw generate` reads the call site, learns `T`, and writes
the binding and validation code for it, which is what keeps the whole mechanism
available under TinyGo and what makes several rules below stricter than a
reflection-based binder would need.

This page is the complete tag and type surface. For the shape of a handler
around it — routing, the response side, the request-scoped accessors — see
[Handlers](/guides/frontend/handlers/).

## What generation needs from the call site

```go
input, err := pw.Parse[showUserInput](r)
```

The type argument has to be a concrete named type written at the call. A `Parse`
reached through a generic wrapper, or one whose type argument is a type
parameter, identifies no type statically and produces no binding. Generation
analyzes one package directory per run, so the call and the type it names should
live in a directory `generate.handlers` lists.

## Source tags

One tag per field names where the value comes from. A field with no tag is an
`input` field carrying its own name.

| Tag | Source | Notes |
| --- | --- | --- |
| *(no tag)* or `input:"name"` | query string, then the body | The default. Query wins when both carry the name |
| `query:"name"` | query string only | |
| `payload:"name"` | request body only | JSON, form, or multipart |
| `payload:"*"` | every body key no other field consumed | One per struct; see [the rest map](#the-rest-map) |
| `path:"id"` | a path wildcard | From a pattern such as `GET /users/{id}` |
| `header:"Authorization"` | one request header | |
| `cookie:"session"` | one cookie | |
| `method:"method"` | the HTTP method | The wire name is unused; the field receives `GET`, `POST`, and so on |

Without an explicit wire name the field name becomes lower camel case, so
`DisplayName` binds `displayName`.

`input` resolves per field kind rather than per request. A **scalar** `input`
field reads the query first and falls through to the body only when the query
does not carry the name; a **nested struct, slice, or map** always comes from
the body, because a query string has no shape to carry one. Reach for explicit
`query` and `payload` as soon as the ambiguity would be a bug rather than a
convenience — an endpoint that accepts a filter from either place has two ways
to be called and one of them is usually a mistake.

`path`, `header`, `cookie`, and `method` never consume a body key, which is what
lets a rest map be exhaustive about the body without seeing them.

A `json` tag does not decide any of this. `json:"-"` keeps a field out of the
JSON document on the response side, and it keeps `pw.WriteAPI` from writing it,
but the binder still fills that field from a request under its wire name — a
`Hidden` field tagged `json:"-"` and nothing else is still an `input` field, and
`?hidden=x` sets it. Nothing in a `Parse` struct is unbindable, so a value the
caller must not control does not belong in one. Bind what the request may carry,
and derive the rest in the handler.

## Field types

| Kind | Types |
| --- | --- |
| Scalars | `string`, `int`, `int64`, `bool`, `float64` |
| File | `httpbind.File` in a multipart body |
| Composite | named struct, nested anonymous struct, `[]scalar`, `[]struct`, `map[string]scalar`, `map[string]struct` |
| Rest | `map[string]any` or `map[string]json.RawMessage` behind `payload:"*"` |

Nesting is JSON-first. A JSON body maps structs to objects, slices to arrays,
and maps to objects with string keys, all to any depth. A
`application/x-www-form-urlencoded` or `multipart/form-data` body carries flat
keys, so declare a nested shape only where the client sends JSON.

Two composites are exceptions, because they have a spelling outside a document.
`[]byte` binds from any value source as base64. A `[]scalar` binds from a
**repeated query key** — `?tag=a&tag=b`, which is what a checkbox group submits
— behind an explicit `query` tag:

```go
Tags []string `query:"tag"`
```

The explicit tag is required: an untagged slice is the JSON case and still comes
from the body. An absent key and a key carrying only empty values both leave the
slice nil, and one unparsable element fails the request rather than binding a
partial list. `?tag[]=a` binds a key literally named `tag[]`, and `?tag=a,b` is
one element — neither bracket nor comma is read as an array. A repeated `path`,
`header`, or `cookie` value is still refused.

Pointer fields are not bound. Prefer value fields, and read
[presence](#presence-and-the-zero-value) for what that costs.

### Request bodies

Three media types fill the same struct, so an ordinary HTML form post and a JSON
API call share one handler and one model:

- `application/json`
- `application/x-www-form-urlencoded`
- `multipart/form-data`

A project that enables [`generate.api.cbor`](/guides/backend/cbor/) adds
`application/cbor` (and any `+cbor` suffix type) as a fourth: the body is one
CBOR map with text keys filling the same fields a JSON body fills, with
`server.cbor_max_body` bounding it the way the multipart limit below bounds
uploads. A `payload:"*"` rest map has no CBOR mapping, and generation reports
the combination as an error.

### Multipart files

```go
import httpbind "github.com/shibukawa/tinybind-go"

type uploadInput struct {
	Title string        `payload:"title" check:"required"`
	Image httpbind.File `payload:"image" check:"required"`
}
```

`File` exposes `Filename`, `ContentType`, `Size`, and `Content`. Only `required`
applies to it; every other `check` rule on a file field is a generation error.

Two limits stack, and the outer one is the framework's. `server.max_request_body`
bounds the whole request at 10 MiB by default, and the multipart body limit
below it defaults to 1 MiB:

```go
httpbind.SetMaxMultipartBodyBytes(8 << 20) // 8 MiB
```

Raising the multipart limit past `server.max_request_body` does nothing until
that key moves too. See
[Application Configuration Keys](/reference/configuration/#server).

### The rest map

`payload:"*"` collects the body keys no sibling field consumed:

```go
type eventInput struct {
	Type   string         `payload:"type"`
	Extras map[string]any `payload:"*"`
}
```

- The map key type has to be `string`. `map[string]any` keeps decoded JSON
  values; `map[string]json.RawMessage` keeps the raw bytes.
- Exactly one rest field per struct. A second is a generation error, and so is a
  rest field of a type that is not a map.
- Keys consumed by a sibling `payload` or body-reading `input` field are
  excluded. `path`, `header`, `cookie`, and `method` fields consume nothing, so
  a body key of the same name still lands here.
- A form or multipart body contributes its remaining non-file values as strings.
- A JSON body that is not an object is a bind error rather than an empty map.
- No remaining keys yields an empty map rather than a nil one.

## Validation

`check` holds the rules that can reject a value. `enum` and `default` are tags
of their own — writing either inside `check` is a generation error naming the
tag to use instead.

```go
type listInput struct {
	Keyword string `query:"keyword" check:"required,minlen=2,maxlen=64"`
	Page    int    `query:"page" check:"min=1" default:"1"`
	Sort    string `query:"sort" enum:"asc,desc" default:"asc"`
}
```

### `check` rules

| Rule | Form | Applies to |
| --- | --- | --- |
| `required` | bare | any field kind |
| `min`, `max` | `min=1` | `int`, `int64`, `float64` |
| `minlen`, `maxlen`, `len` | `minlen=3` | `string` |
| `pattern` | `pattern=^[A-Z]{3}$` | `string` |
| `email` | bare | `string` |
| `uuid` | bare | `string` |
| `date` | bare | `string`, `YYYY-MM-DD` |
| `time` | bare | `string`, `HH:MM:SS` |
| `datetime` | bare | `string`, RFC 3339 |

Commas separate rules, which is why `pattern` has to be the last token in the
tag: a comma inside a regular expression would otherwise split it. A `pattern`
written anywhere else fails generation with that message rather than silently
truncating the expression.

`min` and `max` are inclusive, and both are parsed as floating point before
being compared against the field's type, so `min=1` is legal on an `int`.

The format shortcuts skip an empty value unless the field is also `required`, so
an optional email field accepts absence and rejects `not-an-address`.

Only `required` applies to a file, a rest map, a nested struct, a slice, or a
map. Any other rule on one of those is a generation error saying so.

### Presence and the zero value

`required` means "non-empty" rather than "supplied", and what empty means
depends on the kind:

- a `string` must be non-empty;
- a slice must have a non-zero length;
- a `path` or `header` field that could not be extracted is a violation;
- an `int`, `int64`, `float64`, or `bool` cannot distinguish an omitted value
  from an explicit `0` or `false`, so `required` on one of those accepts the
  zero value.

That last row is a limit of value fields rather than a bug. A contract that
genuinely has to know which one happened needs a sentinel, and the ordering
below is what makes one work.

### `default`

```go
Page int `query:"page" check:"min=1" default:"1"`
```

- Scalars only. A `default` on a file, rest map, struct, slice, or map is a
  generation error.
- Parsed at generation time into a typed literal, so an unparsable value fails
  the build rather than a request.
- Applied **after** validation, and only when the value was absent. A value that
  was supplied and rejected keeps its rejection; a default is never a repair.
- Whitespace is part of the value — nothing is trimmed, because there is no
  separator to trim around.
- `default:""` is an explicit empty-string default and is honoured; a missing
  tag is not the same thing.

The ordering is what makes a sentinel work. With `check:"min=1" default:"-1"`
an absent value arrives as `-1`, while an explicitly supplied `-1` is rejected —
so the handler can tell the two apart on a non-pointer `int`.

### `enum`

```go
Sort string `query:"sort" enum:"asc,desc" default:"asc"`
```

- Scalars only, and the values have to parse as the field's type.
- Comma-separated, with the space around each value trimmed. A value containing
  a comma is not expressible; a set that needs one wants a validating type
  rather than a tag.
- An absent optional value skips the check. A present value outside the list
  becomes a field error reading `must be one of: asc, desc`.
- A field carrying only `enum` still generates validation and still documents a
  `400` response.
- The `default` need not appear in the list, which is what preserves the
  sentinel pattern above.

## What a failure returns

`pw.Parse` returns one error carrying every field it rejected. Hand it to
`pw.WriteProblem` and the response is an RFC 9457 problem document with
field-level detail:

```go
input, err := pw.Parse[createUserInput](r)
if err != nil {
	pw.WriteProblem(w, r, pw.BadRequest(err))
	return
}
```

Each field failure records the location the value would have come from —
`input`, `query`, `payload`, `path`, `header`, or `cookie` — so a client reading
the problem knows where to look. `pw.Validation(pw.Field(name, location, message))`
builds the same shape by hand for a rule the tags cannot express. See
[Responses](/guides/frontend/responses/).

## What reaches OpenAPI

The same analysis that writes the binding writes the operation, so the schema
cannot drift from the code. Every rule above has a keyword:

| Tag | OpenAPI |
| --- | --- |
| `check:"required"` | `required`, or a required parameter |
| `check:"min"`, `check:"max"` | `minimum`, `maximum` |
| `check:"minlen"`, `check:"maxlen"` | `minLength`, `maxLength` |
| `check:"len"` | `minLength` and `maxLength` together |
| `check:"pattern"` | `pattern` |
| `check:"email"`, `"uuid"`, `"date"`, `"time"`, `"datetime"` | `format` |
| `enum` | `enum` |
| `default` | `default` |

Descriptions come from Go doc comments rather than a second home: a doc comment
on the request struct becomes the schema description, and a doc or trailing line
comment on a field becomes the property and parameter description. A paragraph
opening `Deprecated:` sets `deprecated: true`. See
[API Documentation](/productivity/api-documentation/).

## Common generation errors

- `Parse` whose type argument is not a concrete named type at the call site
- `enum=` or `default=` written inside a `check` tag
- `pattern=` that is not the last rule in the tag
- `min`/`max` on a non-numeric field, or a length or format rule on a
  non-string field
- any rule but `required` on a file, rest map, struct, slice, or map field
- more than one `payload:"*"` field, or a rest field that is not a
  `map[string]…`
- a `default` or `enum` value that does not parse as the field's type
- a `json` tag option that is not `omitempty` or `omitzero`, which catches a
  misspelling that would otherwise encode a field you meant to drop
