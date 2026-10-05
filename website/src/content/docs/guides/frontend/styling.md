---
title: Styling
description: Component-scoped styles, and enabling Tailwind CSS — including after the fact.
sidebar:
  order: 5
---

Popcorn Web does not force one CSS workflow. Component-scoped styles require no
setup, while Tailwind CSS remains an opt-in build tool. The two approaches can
also share a component without competing for its class names.

## Component styles

A component contributes its own head content, and its class names are scoped
automatically:

```html
export component Card(label: string): html {
<head>
<style>
.box { color: red }
</style>
</head>
<div class="box"><span>{label}</span></div>
}
```

Classes declared in the block are renamed and the matching `class` attributes
rewritten. Classes **not** declared there pass through untouched, which is
exactly what lets Tailwind utilities sit next to scoped rules in the same
attribute. See [Templates](/guides/frontend/templates/).

## Tailwind CSS

Popcorn Web drives the **standalone** Tailwind binary. There is no
`package.json`, no `node_modules`, and no Node lockfile.

The shortest path is to enable it when creating the project:

```sh
pw init myapp --tailwind
```

### Enabling it later

Adding Tailwind later requires the same four pieces that the scaffold would have
created. Because the existing project may already contain custom files, add
them explicitly.

**1. Make the `tailwindcss` binary available.**

`pw dev` and `pw build` look for `tailwindcss` on `PATH` and fail with a clear
message if it is missing. Add it to `devbox.json`:

```json
{
  "$schema": "https://raw.githubusercontent.com/jetify-com/devbox/0.14.2/.schema/devbox.schema.json",
  "packages": ["go@latest", "git@latest", "valkey@latest", "tailwindcss_4@4.2.4"],
  "shell": {"init_hook": ["echo 'Popcorn Web development environment'"]}
}
```

Then re-enter the shell so the new package is on `PATH`:

```sh
devbox shell
```

The version pin keeps the CSS build reproducible; without it, the same source can
produce different output as the tool moves. If another tool manager provides
`tailwindcss` on `PATH`, Devbox is not required.

**2. Create the stylesheet entry point.**

```css
/* assets/app.css */
@import "tailwindcss";
@source "../handlers";
@source "../templates";
```

The import starts Tailwind, but it does not tell Tailwind where templates live.
The `@source` lines expose class names inside `.pw.html` files; without them, the
generated stylesheet is nearly empty. Add one source per template directory.
For the layout in [Project structure](/guides/architecture/project-structure/), that means
`@source "../webroot";`.

The `@import "tailwindcss"` line is checked before the build runs, so a
malformed entry point is reported rather than silently producing empty CSS.

**3. Turn it on in `popcornweb.toml`.**

```toml
[assets.tailwind]
enabled = true
input = "assets/app.css"
output = "public/generated/app.css"
minify = true
```

`input` and `output` are relative to the project root and must differ. When
`enabled` is true and the paths are omitted, these two values are the defaults.

**4. Link the output from the document shell.**

```html
package templates

external AssetURL(name: string): url

export component Document(children: html?): html {
<!doctype html>
<html lang="en"><head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>My App</title>
  <link rel="stylesheet" href={AssetURL("generated/app.css")}>
</head>
<body><slot /></body></html>
}
```

The argument is the path of `output` inside `public/`; `AssetURL` supplies
`server.public.mount` (default `/public`) and the revision segment that lets a
deployment cache the stylesheet rather than revalidating it on every page load.
`pw init --tailwind` writes this shell and the Go behind the declaration. See
[Static Assets](/guides/frontend/static-assets/) for how that directory is
embedded and served, and for what the revision buys.

Then start the dev server:

```sh
pw dev
```

### How it runs

| Command | Behaviour |
| --- | --- |
| `pw dev` | one unminified build, then a watcher; the input is re-watched if the watcher exits |
| `pw build` | one minified build, regardless of `minify` in the file |

Output is first written to a temporary file and then renamed into place, so the
server never observes a half-written stylesheet. `public/generated/app.css` is
build output. The scaffolded `.gitignore` already excludes compressed
`public/**/*.br`, `public/**/*.zstd` and `public/**/*.gz` sidecars, and
generated CSS usually belongs in it too.

### Plugins

Local plugins referenced from the entry point are resolved relative to it and
verified to exist before the build:

```css
@import "tailwindcss";
@plugin "./plugins/typography.mjs";
@source "../handlers";
```

Keep the modules in the project — `assets/plugins/*.mjs` is the scaffolded
convention — so the build stays reproducible without a package manager.
