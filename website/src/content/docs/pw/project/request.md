---
title: pw request
description: Send one request to the running application, with curl flags routed by its OpenAPI document.
sidebar:
  order: 8
---

```sh
pw request --list
pw request /api/users -d name=Alice -d age=3
pw request ShowUser -d id=7 -d verbose=true --format=json
```

`pw request` sends one request to this project's own application and prints
the response. The flags are curl's, so a line from your shell history works as
it did. What curl does not have is the document the application already
serves: `pw request` reads the OpenAPI document at `server.openapi`, finds the
operation your path names, and sends each `-d key=value` where that handler
reads it — a path segment, the query, a header, a cookie, or the body — with
values coerced to the schema's types. It was written with an agent in mind as
much as a person: `--list` names the operations, and `--format=json` prints one
object to parse.

## Finding the application

The origin is resolved in this order, and the report says which one won:

1. `--url=http://localhost:8080`, when you pass it. The host has to be loopback;
   `pw request` reaches this project's application and nothing else.
2. The address the running [`pw dev`](/pw/project/dev/) loop announced. The
   loop's console is asked, so the answer survives the port shift that moves a
   development run off a taken port.
3. `server.port` from the configuration of the environment, as a guess. A
   connection refused here says so and names `pw dev`.

`--env=<token>` selects which configuration file is read, and defaults the way
[`pw doctor`](/pw/project/doctor/) does: `APP_ENV`, then `dev`.

## Naming the operation

The target is a path, a method and a path, or an operationId from `--list`:

```sh
pw request /api/users/7                # GET, matched against /api/users/{id}
pw request POST /api/users -d name=x   # method as a word, or -X POST
pw request CreateUser -d name=x        # by id; every segment comes from -d
```

A literal path is matched against the templates the way `net/http` matches, so
`/api/users/7` selects `GET /api/users/{id}` and fills `id`. Without `-X`, the
method defaults as curl's does — `GET`, or `POST` once there is data — and if
nothing matches under that method but exactly one operation serves the path
under another, that operation is taken. A path two operations could answer is
refused until `-X` or an id settles it.

A path the catalog does not know still sends. The pairs then mean what they mean
to curl: a form-encoded `POST`, `-G` to move them to the query, `--json` to send
them as an object. The report says no operation matched and why, so the
fallback is never silent.

## Where each pair goes

With an operation matched, a `-d key=value` goes to the location the document
declares for that name. A body declared as JSON is assembled into one object,
with integers, numbers, and booleans parsed and a repeated key becoming an array:

```sh
pw request CreateUser -d name=Alice -d age=3 -d admin=true -d tags=a -d tags=b
```

```
pw request: POST /api/users via http://localhost:8080 (dev) as CreateUser
  name -> body
  age -> body
  admin -> body
  tags -> body
```

sends `{"name":"Alice","age":3,"admin":true,"tags":["a","b"]}`. A value the
schema cannot hold is sent as the string you gave and noted in the report,
because a `400` from the handler is a worse way to learn that `age=three` was
not an integer.

A pair the operation does not name is sent in the body position and noted;
`--strict` makes it a usage error instead, which is what a script wants. A
template segment you did not fill is always an error, since a request to
`/api/users/{id}` literally is never what was meant.

`-F field=value` and `-F file=@path` send multipart whatever the operation
declares, `-d @file` sends the file as the body under the declared media type,
and an explicit `-H 'Content-Type: …'` wins over the document, so a caller
testing the wrong content type can.

## Output

By default the body goes to standard output as curl prints it, and the routing
report above goes to standard error. `-i` prefixes the status and headers, `-o
file` writes the body to a file, and `-s` drops the report.

`--format=json` prints one object instead:

```json
{
  "status": 201,
  "headers": { "Content-Type": ["application/json"] },
  "body": { "id": 8, "name": "Alice" },
  "request": { "method": "POST", "url": "http://localhost:8080/api/users" },
  "operation": { "operationId": "CreateUser", "method": "POST", "path": "/api/users" },
  "routing": [ { "key": "name", "in": "body", "value": "Alice" } ],
  "target": "dev",
  "origin": "http://localhost:8080"
}
```

The body is parsed when the response says it is JSON and kept as a string
otherwise, so a reader handles one shape.

## Listing the operations

```sh
pw request --list
```

```
3 operations at http://localhost:8080

POST   /api/users  CreateUser
       name:string*  body
       age:integer  body
       body: application/json

GET    /api/users/{id}  ShowUser
       id:integer*  path
       verbose:boolean  query

GET    /account  Account  [protected]
```

`*` marks a required parameter. `[protected]` marks a path that
`auth.protection.include` guards in the environment read, evaluated with the
guard's own matcher — a projection of configuration, so a route guarded inside
its handler is not marked. `--format=json` prints the same catalog as an array.

## Credentials

There is no login ceremony a terminal can perform, so a guarded route is called
the way curl calls it: `-H 'Authorization: Bearer …'`, `-u user:password`, or a
cookie. `-c jar.txt` writes the cookies a response set in curl's cookie-file
format and `-b jar.txt` replays them, so a login response can feed the next
call. `-b 'name=value'` sends one cookie string directly.

Without any of these, a protected route answers `401`, which is the finding.

## Exit status

The codes are curl's, so a script written against curl keeps its branches.

| Exit | When |
| --- | --- |
| `0` | a response was received, whatever its status |
| `22` | `-f` was given and the status is `4xx` or `5xx` |
| `1` | no application found, a non-loopback `--url`, a connection failure, or a timeout |
| `2` | an unknown flag, an ambiguous target, an unfilled segment, `--strict` on an unknown pair, or `--list` with no document to list |

## What it does not do

It reaches this project's application on this machine and nothing else; a
`--url` on another host is refused before a connection is opened. It follows no
redirect unless `-L` is given, because a redirect to the login page is usually
the answer you were after. It keeps no session beyond a cookie-jar file, runs
no assertion on the response — that is a test, and
[`testutil`](/productivity/testing/) is where one lives — and it does not
invoke the application without a server; today the application has to be
running, which under `pw dev` it already is.

Page routes are out of its reach too: a `.pw.html` page is [discovered
routing](/guides/cross-layer/discovered-routing/) and stays out of the OpenAPI
document by design, so a page is called by path with curl semantics and
nothing is routed.
