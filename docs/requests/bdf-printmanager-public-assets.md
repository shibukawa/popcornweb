# Change request: public assets of a project that is not at the module's root

**From:** bdf (`github.com/shibukawa/bdf`), the Print Manager (`cmd/printmanager`)
**Against:** `github.com/shibukawa/popcornweb` v0.5.11
**Date:** 2026-10-21
**Status:** open

## The situation

The BDF Print Manager is a Popcorn Web application that lives inside a larger Go module. Its source is in bdf's `cmd/printmanager`, beside the bdf command, and shares bdf's single `go.mod`. The bdf maintainer chose this layout deliberately.

pw wants `go.mod` beside `popcornweb.toml`. Placing `popcornweb.toml` in `cmd/printmanager` fails with "open …/cmd/printmanager/go.mod: no such file or directory". So `popcornweb.toml` sits at bdf's root, and `[generate]` names only `cmd/printmanager/handlers`. Code generation works this way.

The public assets do not. Everything about them is fixed to the project root, and bdf's root is package `bdf`:

| What | Where pw looks (v0.5.11) | Why that fails in bdf |
| --- | --- | --- |
| The embed scaffold | `<root>/public.go` containing `//go:embed all:dist/public` (`internal/pwcli/derivedassets.go`, `verifyPublicEmbed`, line 399) | `<root>` is package `bdf`. A `package publicassets` file there breaks the library, so it cannot be placed. |
| The authored tree | `<root>/public` (`derivedassets.go` line 223) | The application's files are in `cmd/printmanager/public`. |
| The derived tree | `<root>/dist/public` (`derivedPublicDir`, line 31) | It is never written, because the step stops at `verifyPublicEmbed`. |
| The development mount | `dist/public` of the working directory, and nothing else (`middlewares/publicassets.go`: `localPublicRoot` line 26, `resolvePublicAsset` line 495) | Under the `pwdev` tag a miss is a 404. The tree passed with `pw.WithPublicFS` is never consulted. |
| Lambda packaging | `<root>/public.go` and `<root>/config.prod.toml` (`internal/pwcli/deployment.go` line 138) | Same as above. The application is configured by environment variables only and has no `config.prod.toml`. |

### What the user saw

`pw dev` prints `public.go is required; run pw init to create the public embed scaffold` and then starts the application anyway. Every `/public/...` request, the stylesheet included, answers 404. The page renders unstyled, and nothing says why: the warning appears among the startup lines and does not mention that `/public` will be empty.

The same binary built with `go build` serves the files, because outside `pwdev` the mount falls back to the embedded tree. So the failure appears only during development, which is the mode where it is hardest to connect to its cause.

### What bdf does today (to be removed once this is answered)

- `pw generate --code-only`, which skips the asset step. The `*_pw_gen.go` files are committed, and CI checks them for drift.
- The pages' scripts and style are served by the application itself at `/static/`, from its own embed, with an ETag. `pw.WithPublicFS` still gets the same tree, because `server.public.enabled` refuses to start without one. This is `cmd/printmanager/handlers/static.go`.
- The style is built with the standalone `tailwindcss` from `go generate`, not from `[assets.tailwind]`.
- `pw build --target=lambda` is not used. `cmd/printmanager/deploy/lambda/Dockerfile` copies the Dockerfile pw would write (Lambda Web Adapter 1.0.1, distroless static).

## Summary of the asks

1. **Ask 1: configurable asset locations.** `popcornweb.toml` names the authored tree, the derived tree and the embed file, relative to the project root, with today's paths as defaults. Every pw command and the running application then use the configured paths.
2. **Ask 2: a development mount that does not go silently empty.** Under `pwdev`, the `/public` mount either falls back to the embedded tree on a miss, or refuses to start (or says so loudly) when the derived tree is missing.
3. **Ask 3, lower priority: packaging that follows Ask 1.** `pw build --target=…` uses the configured locations, and accepts a missing `config.prod.toml` when the application is configured by the environment alone.

Ask 1 is the one that matters. With it, bdf deletes `static.go` and the hand-written Dockerfile, and uses `pw dev` and `pw build` as any project does.

## Ask 1: configurable asset locations

### Shape

```toml
# popcornweb.toml at bdf's root
[assets]
public = "cmd/printmanager/public"           # authored tree (default "public")
derived = "cmd/printmanager/dist/public"     # derived tree (default "dist/public")
embed = "cmd/printmanager/public.go"         # the file verifyPublicEmbed checks (default "public.go")
```

- Paths are relative to the directory of `popcornweb.toml`. A path that leaves it, or goes through a symbolic link, is refused, as `safeLocalPath` refuses today.
- `verifyPublicEmbed` checks the configured file. The directive it looks for is the derived tree relative to that file's own directory: here `//go:embed all:dist/public` in `cmd/printmanager/public.go`. That is what lets the embed file live in package `main` of `cmd/printmanager` rather than at the root.
- `pw dev` and `pw generate` write the derived tree, the manifest and `public_manifest_pw_gen.go` beside the configured embed file, not at the root. `[assets.tailwind]`'s `input` and `output` already take paths, and would just be written relative to the same root.
- The running application needs the derived tree's location under `pwdev`. Today it is the constant `localPublicRoot`. One way: a setting `server.public.local_root` (default `dist/public`), which `pw dev` sets for the process it starts (for example, as `SERVER_PUBLIC_LOCAL_ROOT=cmd/printmanager/dist/public`). Another: `pw dev` starts the process in the project's working directory and passes the path relative to that. Either is fine for bdf. What it must not do is assume that the working directory holds `dist/public`.

### Why configuration rather than moving the project root

We considered asking for the opposite: let `popcornweb.toml` sit below `go.mod` (here, in `cmd/printmanager`), find `go.mod` by walking up, and resolve everything relative to the project directory. That would also solve this. But it changes how every generated import path is computed, and how the working directory, `config.{env}.toml` and `.env*` are found. It is the larger change.

Ask 1 touches only the asset step, the development mount and packaging. Projects that do not set `[assets]` see no difference.

### Done when

- A fixture module with Go code in package `fixture` at its root, and a Popcorn Web application in `cmd/app` with `[assets]` pointing there. In that fixture:
  - `pw generate` writes `cmd/app/dist/public` and the manifest without asking for `<root>/public.go`;
  - `pw dev` serves `/public/app.css` from it;
  - `pw build` embeds it;
  - nothing is written at the root except what `[generate]` names.
- Without `[assets]`, the existing fixtures produce byte-identical output.
- The documentation (`ja/guides/frontend/static-assets.md`, `ja/reference/build-configuration.md`) describes the keys.

## Ask 2: a development mount that does not go silently empty

Under `pwdev`, `resolvePublicAsset` reads only the derived tree, and a miss is a 404 (`if publicDevelopment || rejected { return publicAsset{}, false }`). The reason is in the comment on `readLocalPublicAsset`: the authored tree is an input, and a development loop that read it would 404 every moved reference.

That reason argues against reading the *authored* tree. It does not argue against reading the *embedded* one, which the application registered on purpose.

Either of these would have saved the debugging session:

- **(a) Fall back to the embedded tree** (the one passed with `pw.WithPublicFS`) when the derived tree has no file of that name. The served bytes may be one build stale, but that is no worse than a binary built without `pwdev`. If the staleness matters, log it once, as `reportPublicMismatch` does.
- **(b) Treat a missing derived tree as a failure of `pw dev`.** If the asset step fails, `pw dev` stops (or the dev console shows it as an error, not a startup line), and the application logs once at startup that `/public` has no tree and will answer 404.

We would take (a), and (b)'s log line as well.

### Done when

- Under `pwdev`, with no `dist/public`, a file present in the tree passed to `pw.WithPublicFS` is served. Or, under (b), `pw dev` exits non-zero with the `verifyPublicEmbed` message.
- A test covers the case.

## Ask 3 (lower priority): packaging that follows Ask 1

`pw build --target=lambda` (`internal/pwcli/deployment.go`) needs `<root>/public.go` through the asset step, and `<root>/config.prod.toml` (line 138: "deployment requires config.prod.toml").

- With Ask 1, the first goes away.
- For the second: the Print Manager is configured entirely by environment variables (`MANAGER_*`, plus the platform's `AWS_*`), and a Lambda function has no other source. Please accept a missing `config.prod.toml` when the project declares that, for example with `[deploy] config_file = false`, or simply when the file is absent, and write the Dockerfile without the `COPY` of it.

## Smaller notes from the same integration

None of these blocks anything; they are documentation gaps we hit while integrating:

- **`[]string` defaults, and CSRF's (a bug).** A `default` for a `[]string` reaches tinybind-go's `configbind` `Overlay.GetMulti` as a scalar, which splits it on commas (`configbind/overlay.go`, v0.5.33). So `default:"[]"` becomes the one-element list `["[]"]`; this is how we found it.
  - The same happens to `CSRFConfig.Include`'s `default:"[\"/**\"]"`, generated as `"security.csrf.include": "[\"/**\"]"` in `pwconfig/configbind_gen.go`. Parsed with no configuration, `pwconfig.Value[pwconfig.SecurityConfig]().CSRF.Include` is the one string `["/**"]`, not `/**`.
  - As a result, an application that turns CSRF on (`SECURITY_CSRF_ENABLED=true`) without setting `include` does not start: `popcornweb: security.csrf: pattern "[\"/**\"]" must start with a slash`. We reproduced this with the Print Manager on v0.5.11. It fails closed, which is the safe way to fail, but the documented default does not work.
  - Either `GetMulti` reads a `[`…`]` default as a list, or the generator rejects one and the default is written `"/**"`. In both cases, the rule belongs in `ja/reference/configuration-declaration.md`.
- **Startup before `pw.Run`.** An application that opens resources from its configuration before `pw.Run` (our store) must call `pw.ParseConfig()` and then `pwconfig.RunFrameworkAction()` first. Otherwise `--generate-config` and the health probe open the store. `ParseConfig` being idempotent makes this work, but the order is not documented, and `RunFrameworkAction` is exported only from `pwconfig`, not from `pw`.
- **Empty `generate.queries`.** It must be listed (`queries = []`) even when a project has no queries. The error says so clearly; this is only a note.
