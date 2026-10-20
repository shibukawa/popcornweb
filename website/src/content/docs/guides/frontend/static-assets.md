---
title: Static Assets
description: The public directory is what you write; a build turns it into the tree that ships, with a manifest that decides every cache header before a request arrives.
sidebar:
  order: 7
---

By default, a project uses the root `public/` directory as its static-file input. The
files are embedded in the binary, so the executable and its assets are deployed
together.

```
public/                     what you write
  favicon.ico
  generated/app.css
  images/logo.png
```

```
GET /public/images/logo.png
```

`pw init` creates both the directory and the `public.go` file that embeds it, so
a scaffolded project can serve assets immediately.

The binary actually embeds `dist/public`, not the source directory.
[`pw build`](/pw/project/build/) copies or transforms files from `public/` into
that build tree. With no transformations the copy is byte-for-byte identical;
when a transformation renames a file, the build tree holds the result and the
reference rewrite follows it. Serve assets elsewhere when a CDN or object store
needs to manage them independently from application releases.

## Applications below the module root

Keep a shared `go.mod` and an application in `cmd/app`, and select its asset
locations in the root `popcornweb.toml`:

```toml
[assets]
public = "cmd/app/public"
derived = "cmd/app/dist/public"
embed = "cmd/app/public.go"
```

`cmd/app/public.go` can use `package main`. Its directive is relative to that
file: `//go:embed all:dist/public`. The generated Go manifest uses the same
package. See [build configuration](/reference/build-configuration/#assets) for
defaults, auxiliary outputs, and path restrictions.

## What ships

```go
package publicassets

//go:embed all:dist/public
var embeddedPublic embed.FS

func init() {
	middlewares.RegisterPublicFS(PublicFS())
}
```

`public.go` is ordinary application code, not generated output. The `init`
registration is why nothing in your `main` mentions assets: the framework picks
the filesystem up during startup and mounts it. Serving with
`server.public.enabled` on and no registered filesystem is a startup error
rather than a directory that quietly 404s.

Beside it the build writes `public_manifest_pw_gen.go`, which is generated: one
entry per URL, naming every representation, its length, its validator, and its
cache policy. A response reads bytes and computes nothing. A URL the build did
not declare is a `404` whatever the tree happens to hold, which is what stops a
file that slipped in from being reachable without a build having decided it
should be.

`public` is the only static directory convention the framework has. Anything
else you want to serve — a user upload, a generated report — is an ordinary
route, and [object storage](/guides/storage/object-storage/) is usually the
better home for it.

## Two cache policies, decided at build time

A file that keeps the name you wrote gets `public, no-cache` and a strong
`ETag`. The browser revalidates and an unchanged asset costs a `304` with no
body. That is as much as a stable *name* can honestly promise, because the next
build may put different bytes behind it.

A file the build *produced* is named after the digest of its own bytes —
`logo.4f2a91c07b3e.webp` — and gets `public, max-age=31536000, immutable`.
Different bytes are a different URL, so the promise is true rather than hopeful.

Only produced files are named that way, and the reason is worth stating: hashing
a *name* works only where every reference to it is rewritten. The build rewrites
the `src` of an `img` and of a built `script`, and the `url()` of a stylesheet.
It does not rewrite a `link href`, so a stylesheet keeps the name you gave it.

## Naming an asset with `AssetURL`

Keeping the name and revalidating are separate problems, and the second one has
a way out. Every asset is also served under a **revision segment** — a digest of
that file's own bytes, sitting between the mount and the name:

```
/public/app.css                        public, no-cache
/public/9f4c1e2a7b60d381/app.css       public, max-age=31536000, immutable
```

Same bytes, same `ETag`, two URLs. The second one can promise never to change
because a file whose contents changed is answered at a different address — and
the old address `404`s rather than quietly serving something else, which is what
makes a browser safe to hold it forever.

Which URL a page loads is decided by how the template names it. A literal path
has no revision and revalidates. `AssetURL` asks the build:

```html
package templates

external AssetURL(name: string): url

export component Document(children: html?): html {
  <html><head>
    <link rel="stylesheet" href={AssetURL("app.css")}>
  </head><body><slot /></body></html>
}
```

```go
func AssetURL(name string) *url.URL { return &url.URL{Path: pw.PublicAssetURL(name)} }
```

`pw init` scaffolds both halves, so a new project is already cacheable.

The argument is the path inside the served tree — `"app.css"`, not
`"/public/app.css"` — because the mount is runtime configuration and a template
that spells it out is a second place to change when it moves. The full URL is
accepted too, so migrating an existing template is a mechanical edit.

Two things get no revision, and neither needs one. A produced file already
carries its digest in its name, so a segment would say the same thing twice. And
a file in the [external tree](#when-a-file-should-not-be-in-the-binary) ships as
its own artifact, whose bytes the build never read.

In the development loop there is no manifest, so `AssetURL` returns the plain
URL and every edit is visible on the next load. Nothing to switch off.

Use it for the assets nothing else renames: a stylesheet, a plain script. Leave
an `img src` and a TypeScript `script src` as literals — the build already
rewrites those into names that carry their own digest, and hiding them behind a
function is how they stop being converted.

## Conversion

Nothing converts until you ask. [`pw add images`](/pw/project/add/) installs the
encoders and the switch together, which is deliberate: a switch without the
tools converts nothing and says so, and neither one alone is useful.

```toml
[assets.images]
enabled = true
quality = 75
avif = false

[assets.css]
minify = true

[assets.scripts]
enabled = true
```

What each one does, and what follows the file:

The extension selects the pipeline, and it is not the only proof of what the
file holds: the build checks the bytes against it first and refuses a file that
disagrees, before any encoder sees it. See [A file has to be what it says it
is](#a-file-has-to-be-what-it-says-it-is).

| Source | Becomes | The reference |
| --- | --- | --- |
| `img src` whose URL has a `.png`, `.jpg`, or `.jpeg` extension | WebP, lossless from a PNG and lossy from a JPEG; with `avif = true`, an AVIF representation as well | rewritten to the hashed URL; `Accept` chooses between AVIF and WebP when both exist |
| `script src` naming a `.ts` or `.tsx` | a bundled ES module, with a source map in a debug build | rewritten to the hashed name |
| a `.css` file | minified, with its `url()` references pointed at whatever they became | unchanged — the stylesheet keeps its own URL |
| a `.js` file | minified, not bundled, so a module stays a module | unchanged |
| anything else | copied | unchanged |

A conversion that loses is declined. An encode that came out larger than its
source leaves the reference on the original and says why in the build output,
because a derived file nobody benefits from is weight with extra steps.

The authored source is dropped from the shipped tree once every reference the
build can see has been rewritten. When one it cannot rewrite remains — a path in
a `meta` tag, a URL a script builds — the source is kept and the build says so.
That way a page never loses an image to a conversion it did not know about.

With the script build on, TypeScript is an input rather than a file the tree
owes anyone, so a module an entry imported is not served either. No browser runs
it. The same retention rule applies: a `.ts` some Go code still names stays, and
the build says why.

The source map is the one output that depends on how the build was invoked.
[`pw dev`](/pw/project/dev/) always writes it, and
[`pw build --debug`](/pw/project/build/#debug-artifacts) keeps it in the artifact;
a plain `pw build` writes neither the map nor the `sourceMappingURL` comment
naming it. The map embeds the authored TypeScript, so shipping one serves your
own sources to anyone who asks for them, and staging and production have no use
for that.

### A built script needs `type="module"`

The build emits a module, and a module under a classic `<script src>` is a
syntax error at load time: the page renders and silently loses its script. The
build refuses that rather than shipping it, and names the template and line.

```html
<script type="module" src="/public/js/app.ts"></script>
```

### AVIF is chosen per request

With `avif = true` an image carries a second representation behind the same URL,
and `Accept` decides which one a request gets. `Vary` names `Accept` only for
the URLs that actually have two, so a cache stores one variant for everything
else.

The build never writes a `<picture>` element for you. Wrapping an `img` changes
the element tree, which can break a CSS combinator or a script that walks the
DOM, and neither of those is visible from here. Writing one yourself works fine
— its `img src` converts like any other, and its `source` elements are left
alone.

## When a file should not be in the binary

`public/` is compiled into the executable. That is the right default — one
artifact, nothing to deploy alongside it, no path to get wrong — and it stops
being right somewhere around the first video. A 200 MB binary is slow to build,
slow to push, and on a function host it may simply be refused.

So there is a second authored tree:

```
public/            → transformed, embedded in the binary
public-external/   → untouched, shipped beside it
```

Both answer at the same mount. `public-external/promo.mp4` is served at
`/public/promo.mp4`, and nothing on the page says which tree answered.

Put a file there when it is large and already compressed — video, audio,
archives, a big PDF. Leave everything else in `public/`: a stylesheet or an
icon out here would lose its conversion and its precompressed siblings for no
gain, and cost you a file to deploy. `pw doctor` reports a media file over 4 MiB
in the embedded tree
([PW0132](/appendix/diagnostics/#pw0132-a-large-media-file-is-compiled-into-the-binary))
rather than moving it, because where a file lives should not change when it
grows.

### What it costs and what it buys

The build does not copy these files. Nothing transforms them, so a staging copy
would be identical to the source — and these are the files a project least wants
copied on every build. It reads their leading bytes to verify them and their
extension to set a media type, and that is all.

They are served with `http.ServeContent`, so they get `Accept-Ranges`, `206`
responses, and `If-Range` handling that the embedded tree does not have. That is
the real reason the split is worth it: a `<video>` element that cannot seek
downloads the whole file to play from the middle.

What you give up is the strong `ETag`. The tree is deployed as its own artifact,
so a validator computed at build time could outlive the bytes it describes;
these URLs revalidate on size and modification time instead. You cannot have
independent deployment and an immutable URL at once, and this mechanism is that
trade.

### Deployment

Every artifact `pw` produces carries the directory, and always at the root the
server resolves against:

- the scaffolded `Dockerfile` copies it into the image, which is why `pw init`
  writes `public-external/.keep` — a `COPY` of a path that does not exist fails
  the image build
- `pw build --target` places it beside `config.prod.toml` in the deployment
  stage, for Lambda, Azure Functions, Cloud Run functions, and Vercel alike.
  Both are resolved against the working directory the function runs with, so
  they belong in the same place

The one deployment that needs a step from you is a bare binary: ship the
directory next to it and start the process with a working directory where
`public-external/` resolves.

A target with no filesystem cannot carry it at all. That is Cloudflare Workers,
which is [not a supported target](/guides/deployment/serverless/) in any case;
every target that is ships as a container or a bundle.

This is also the only place the tree is copied. `pw build` never copies it —
once per deployment artifact, rather than once per compile.

### If both trees hold the same URL

The external one wins, and the build warns:

```
asset: warning: public-external/app.css (shadows public/app.css)
```

It is a warning rather than an error because the precedence is defined — but the
embedded file is still sitting there looking like the one being served, which is
exactly the confusion worth one line of build output.

## A file has to be what it says it is

Everything above decides from the extension. `.png` selects the WebP
conversion, the manifest takes its media type from the same place, and the
response sends that media type. A file whose bytes disagree with its name is
therefore labelled by the name, and every response asserts a type the bytes
never had. The case worth naming is a `.png` that is really an SVG with a
`<script>` in it: served as `image/png`, and a different thing entirely
wherever the extension gets trusted again.

`pw build` refuses it:

```
public assets: logo.png: the extension declares png, and the bytes carry no png signature; rename the file to the type it actually is, or list the path in assets.verify.allow
```

The check reads the first 64 bytes of each authored file — bytes the build
already holds to digest them, so it costs nothing measurable. A format that has
a signature has to carry it. A format that has none, which is CSS, JavaScript,
JSON, and SVG, has to *not* carry someone else's; that second half is what
catches a `.css` holding a ZIP, and without it the extensions a browser treats
as executable would be exactly the ones exempt. An extension the table has never
heard of is left alone rather than guessed at, so an unusual file does not fail
a build over a name nobody taught it.

Only the authored tree is checked. What the build produced is labelled by the
build, and that distinction earns its keep: an AVIF representation deliberately
lives under a `.webp` URL, so a check reading URLs would refuse the build's own
output.

Nothing here parses a text format. A `.svg` holding broken XML ships, because a
parser per format is a large surface for the narrow gain of catching a file that
is malformed rather than mislabelled.

### SVG is the one image that executes

An SVG is XML, and `image/svg+xml` served from your own origin runs its scripts
on direct navigation. An `<img>` never did, so the exposure is someone opening
the asset URL, or an `<object>` pointing at it.

Two things address it, and the load-bearing one is the header. Every
`image/svg+xml` response carries `Content-Security-Policy: sandbox`, which puts
the document in a unique origin with no scripting — so an SVG that does execute
cannot reach your application, whether or not any build ever read it. It is
added beside the application's own policy rather than replacing it, and a
browser enforces both, so it can only tighten what you declared.

The build also scans authored SVGs for `<script`, an `on…=` handler, and
`javascript:`, and refuses what it finds. That scan is literal by design: it
parses nothing, so a handler hidden behind SMIL, entity encoding, or a namespace
prefix goes unnoticed. That is a missing warning rather than a missing defence,
which is exactly why the header is the part to rely on.

Turn the sandbox off only for an SVG that is *meant* to be interactive through
`<object>` or a link. `server.public.svg_sandbox = false` is the switch, and it
applies to every SVG the endpoint serves. To keep one deliberate file without
giving up the header, `assets.verify.allow` is the narrower instrument — it
exempts that path from both build checks and changes no response.

## Precompression

The build writes a `.br`, a `.zstd` and a `.gz` sibling next to every
compressible file after the conversions, so what is compressed is what actually
ships. Serving then costs no CPU at all: the encoded bytes already exist.

| Compressed | Left alone |
| --- | --- |
| `.html`, `.css`, `.js`, `.mjs`, `.json`, `.map`, `.txt`, `.xml`, `.svg`, `.webmanifest`, and any other `text/*` | images other than SVG, audio, video, fonts, archives, WebAssembly — anything already compressed |

All three run at their maximum level, which is affordable here for the reason it
is not on [a rendered response](/guides/backend/compression/): the cost lands on
the build rather than on a request. Brotli exists only here, and only because of
that — at maximum it comes out roughly fifteen percent smaller than zstd and
seventeen percent smaller than gzip, a margin that appears at levels far too slow
to encode while a client waits.

A coding whose output is not smaller than its source is skipped rather than
written, so a short file may have fewer than three siblings, or none. That is
ordinary: negotiation falls through to the next coding, and the identity bytes
always answer.

A sidecar path is never a URL. It is a representation of the asset beside it, and
a request for one is a `404`. Each form carries its own validator, so a cache that
stored one cannot serve it to a client that asked for another.

## What a request gets

| Behaviour | Detail |
| --- | --- |
| Methods | `GET` and `HEAD`; anything else is `405` with `Allow` |
| Mount without the trailing slash | `308` redirect to the mount, query string preserved |
| Encoding | the first of `br`, `zstd`, `gzip` that `Accept-Encoding` allows with a non-zero q-value *and* has a sidecar; identity otherwise. The order is the build's, smallest first, not the client's q-values |
| Media type | the preferred representation the request accepts; the fallback otherwise |
| `Vary` | `Accept-Encoding`, plus `Accept` where a URL has more than one media type |
| `ETag` | strong, from the build, per representation |
| `If-None-Match` | `304` on a match |
| Revision segment | stripped and checked against the manifest; a match answers `immutable`, a segment this build does not serve is `404` |
| Everything refused | `406` |

A directory resolves to its `index.html` when there is one, and to a `404`
otherwise. There are no directory listings.

## Path handling

The rules here are all refusals, and they are worth knowing because they are
absolute:

- dot-prefixed segments are rejected, so `.env` and `.git/config` under `public/`
  are not reachable
- traversal, backslashes, and NUL are rejected after one round of percent-decoding
- symbolic links are refused — the local root, anything below it, and any
  non-regular file. The build refuses to walk one too, rather than embedding
  whatever it points at
- a `.br`, `.zstd` or `.gz` suffix in the request path is rejected outright

## During development

`pw dev` passes the configured derived-tree path to the application. An ordinary
file miss falls back to the FS passed with `pw.WithPublicFS`, or the registered
embedded FS. A missing derived tree produces one startup warning that embedded
bytes may be stale. If neither tree exists, the diagnostic explicitly says
public assets will return 404. Symlink and unsafe-path rejections do not fall
through. Failure of the initial asset build makes `pw dev` report the error and exit.

[`pw dev`](/pw/project/dev/) runs the same conversions and serves `dist/public`
from disk. Edit an asset and reload: the tree is rebuilt, and no Go rebuild
happens for a file the binary does not compile. There is no manifest, so
`AssetURL` names the plain URL and an edit is visible on the next load — which
is the one place where revalidating on every request is the behaviour you
want.

It has to run the same conversions rather than skip them, because a rewritten
reference is compiled into generated code — a development build that skipped
them would serve pages naming files it never produced. What keeps that
affordable is the conversion cache under `dist/`: an unchanged asset costs a
digest instead of an encode. The first build after a clone is the one that pays.

`public-external/` is read straight from the project root, and it is consulted
*before* the built tree — the same precedence production uses. That ordering
matters more than it looks: if the loop checked it second, a file you had
deliberately shadowed would render one way here and the other way after a
deploy.

Outside that loop, `server.public.read_local = true` gives the same disk-first
behaviour from an ordinary build, for the deployment that overlays a directory
onto the embedded tree.

## Configuration

| Key | Default | Meaning |
| --- | --- | --- |
| `server.public.enabled` | `true` | serve the assets at all |
| `server.public.mount` | `"/public"` | where they are mounted |
| `server.public.local_root` | `"dist/public"` | Built tree on disk, relative to the working directory |
| `server.public.read_local` | `false` | prefer the built tree on disk over the embedded one |
| `server.public.svg_sandbox` | `true` | add `Content-Security-Policy: sandbox` to every `image/svg+xml` response |

The mount must be an absolute, canonical, non-root path with no wildcards, and
an application route that collides with it fails startup rather than shadowing
it. Turning `enabled` off registers no route but changes nothing about the
binary — the assets are still embedded, just not reachable.

The build-time keys are in
[Build Tool Configuration](/reference/build-configuration/), and `dist/` is
build output: the scaffolded `.gitignore` excludes everything under it except
the sentinel that keeps the embed compiling before the first build.

The framework's own browser scripts do not live here. They are served from a
fixed `/_pw/` prefix ahead of application routing, which is what keeps them
available no matter how this endpoint is configured. See
[Progressive rendering](/guides/cross-layer/async-rendering/).

## Not the same as response compression

The sidecars above are static files, compressed once at build time. Compressing
a response the application just rendered is a separate switch with separate
trade-offs — see [Response Compression](/guides/backend/compression/). That
middleware never recompresses what this handler served.

The two also offer different codings, and for the same reason the levels differ:
a rendered response is encoded while a client waits, so it offers only `zstd` and
`gzip` and runs them shallow. Brotli and the maximum levels stay here, where
nobody is waiting on them.
