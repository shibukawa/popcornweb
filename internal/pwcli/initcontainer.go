package pwcli

import (
	"strings"

	"github.com/shibukawa/popcornweb/internal/pwenv"
)

// The runtime stage of every recipe below. distroless/static carries CA
// certificates, time zone data, and no shell: a scratch base would fail the
// first outbound TLS handshake for want of a certificate pool, and anything
// with a package manager is a larger attack surface than a static Go binary
// needs. The nonroot tag runs as uid 65532.
const containerRuntimeBase = "gcr.io/distroless/static-debian13:nonroot"

// The builder image of Dockerfile.tinygo. It carries a host Go toolchain as
// well, which the generation phase needs whatever compiles the application.
const tinygoBuilderImage = "tinygo/tinygo:0.42.0"

// containerScaffoldFiles are written for every application, without a question,
// the way .gitignore and .editorconfig are. They change nothing about the
// application and cost one deletion to decline.
//
// The reason they are scaffolded rather than documented is that the Dockerfile
// a Go reader already knows how to write does not work here. A Popcorn Web
// build has a host phase before the compiler — generation, the stylesheet, the
// embedded asset tree — and every file it writes is excluded from version
// control, so COPY . . followed by go build fails on symbols whose sources the
// reader cannot find.
func containerScaffoldFiles(options initOptions) map[string]string {
	files := map[string]string{
		"Dockerfile":    dockerfileScaffold(options),
		".dockerignore": dockerignoreScaffold(options),
	}
	if options.TinyGo {
		files["Dockerfile.tinygo"] = tinygoDockerfileScaffold(options)
	}
	return files
}

// dockerfileScaffold is the host Go recipe, which is what docker build finds
// with no argument. A TinyGo project gets the second file as well; this one
// stays the default because it is the recipe every platform accepts and the one
// with no rule:tinygo-container-operations caveats attached.
func dockerfileScaffold(options initOptions) string {
	name := options.Name
	return `# Build and run ` + name + ` as a container image.
#
#   docker build -t ` + name + ` .
#   docker run --rm -p 8080:8080 ` + name + `
#
# A Popcorn Web build is not "go build". The generated Go, the stylesheet, and
# the embedded asset tree under dist/ are build outputs that version control
# does not carry, so the builder stage runs "pw build" — generate, CSS, assets,
# then compile — rather than invoking the compiler itself. A Dockerfile that
# skipped that step would fail on undefined symbols whose sources are here.

# The Debian release is pinned alongside the Go version: a bare golang tag
# rebases onto each new stable Debian the day it releases, and the runtime
# stage below names its Debian release explicitly, so an unpinned builder
# would let the two drift apart on someone else's schedule.
FROM golang:1.27-trixie AS build
WORKDIR /src

# The module files come first so the download layer survives a source edit.
COPY go.mod go.sum ./
RUN go mod download

` + containerPWInstall() + containerTailwindInstall(options) + `COPY . .

# CGO_ENABLED=0 is what makes the binary static enough for the runtime base
# below. pw build passes the environment through to the compiler.
RUN CGO_ENABLED=0 pw build

FROM ` + containerRuntimeBase + `
WORKDIR /app

# Project-local configuration resolves against the process working directory,
# so the WORKDIR above and this file belong together. Change one and the server
# starts on defaults instead of failing, which is the quieter mistake.
COPY --from=build /src/` + name + ` /app/` + name + `
COPY --from=build /src/` + pwenv.FileName(pwenv.Production) + ` /app/` + pwenv.FileName(pwenv.Production) + `
` + containerExternalAssetCopy() + `
# An unset APP_ENV means dev, which would look for a file this image does not
# carry and fall back to development defaults.
ENV APP_ENV=` + pwenv.Production + `
EXPOSE 8080

` + containerHealthcheck(name) + `ENTRYPOINT ["/app/` + name + `"]
`
}

// tinygoDockerfileScaffold is the same image built by the other toolchain. It
// is a second file rather than a build argument because the two differ in more
// than a base image: a TinyGo binary answers no SIGTERM, so an orchestrator
// stopping it always reaches the kill, and it cannot apply migrations in
// process. Those are deployment trades, and a file is where a reader meets one.
func tinygoDockerfileScaffold(options initOptions) string {
	name := options.Name
	return `# Build and run ` + name + ` with TinyGo, which produces a much smaller binary.
#
#   docker build -f Dockerfile.tinygo -t ` + name + ` .
#
# Two things behave differently in an image built this way, and neither is
# fixable in the application:
#
#   * TinyGo installs no signal handler, because its os/signal replaces the
#     default disposition and then delivers nothing. SIGTERM is ignored, so
#     "docker stop" and a pod deletion always end in SIGKILL and in-flight
#     requests are cut rather than drained. Stop sending traffic before the
#     stop signal — a preStop hook, or a load balancer that drains first.
#   * Migrations run as a child "pw" process under TinyGo, and this image
#     carries no pw. Apply them as a separate step, which is where they belong
#     in production anyway.
#
# TinyGo compiles for the machine running the build, so build on the target
# architecture or use "docker buildx --platform".

FROM ` + tinygoBuilderImage + ` AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

` + containerPWInstall() + containerTailwindInstall(options) + `COPY . .

# pw generate is pw build without the compiler: generation, CSS, assets, and the
# development-only import check. The line after it is the whole difference
# between this file and Dockerfile.
RUN pw generate

# -scheduler=threads is required for any engine that speaks a network protocol:
# under the cooperative scheduler a blocking socket call holds the whole
# runtime, so a driver's cancellation watcher never runs and a query outlives
# its deadline without reporting one. It is harmless otherwise, and postgres and
# mysql refuse to compile without it rather than failing quietly at run time.
RUN tinygo build -scheduler=threads -o /out/` + name + ` ./cmd/` + name + `

# If the binary turns out to be dynamically linked for your target, this is the
# line to change: gcr.io/distroless/base-debian13:nonroot carries a libc.
FROM ` + containerRuntimeBase + `
WORKDIR /app

COPY --from=build /out/` + name + ` /app/` + name + `
COPY --from=build /src/` + pwenv.FileName(pwenv.Production) + ` /app/` + pwenv.FileName(pwenv.Production) + `
` + containerExternalAssetCopy() + `
ENV APP_ENV=` + pwenv.Production + `
EXPOSE 8080

` + containerHealthcheck(name) + `ENTRYPOINT ["/app/` + name + `"]
`
}

// containerExternalAssetCopy carries the tree that is not in the binary.
//
// It resolves against the WORKDIR above, which is what makes the relative path
// the server looks for the same one this writes. The scaffolded sentinel is
// what keeps this line working in a project that has put nothing here yet: a
// COPY of a missing path fails the image build, and an empty directory is not
// something git carries on its own.
func containerExternalAssetCopy() string {
	return "COPY --from=build /src/" + externalPublicDir + " /app/" + externalPublicDir + "\n"
}

// containerPWInstall resolves pw from the framework version the project already
// depends on. pw generates the code that framework reads, so the two have to
// agree; taking the version from go.mod leaves no second place to update.
func containerPWInstall() string {
	return `# pw generates the code the framework reads, so its version has to match the
# framework this project depends on. go.mod is the one place that records it.
RUN GOBIN=/usr/local/bin go install \
      github.com/shibukawa/popcornweb/cmd/pw@$(go list -m -f '{{.Version}}' github.com/shibukawa/popcornweb)

`
}

// containerTailwindInstall installs the standalone CSS toolchain for a project
// that took Tailwind. devbox.json pins the version for a developer's machine
// and does not reach inside an image, so the pin is repeated here; the two
// going out of step is a difference between the CSS a developer sees and the
// CSS the deployment serves.
func containerTailwindInstall(options initOptions) string {
	if !options.Tailwind {
		return ""
	}
	return `# The standalone Tailwind executable, pinned to the version devbox.json holds.
# TARGETARCH is set by BuildKit; the release assets spell amd64 as x64.
ARG TARGETARCH
RUN case "${TARGETARCH:-amd64}" in \
      amd64) arch=x64 ;; \
      arm64) arch=arm64 ;; \
      *) echo "no Tailwind release for ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /usr/local/bin/tailwindcss \
      "https://github.com/tailwindlabs/tailwindcss/releases/download/v` + tailwindPinnedVersion() + `/tailwindcss-linux-${arch}" \
    && chmod +x /usr/local/bin/tailwindcss

`
}

// containerHealthcheck is the probe instruction both recipes carry. The image
// has no shell and no curl, so the binary probes itself: it reads the same
// configuration sources as the server, which is why no port or path is
// repeated here.
func containerHealthcheck(name string) string {
	return `# The image ships no shell and no curl, so the binary is its own probe. It
# reads the same configuration this image already carries, so the port and the
# health path are never repeated here. Docker's --timeout is its patience with
# the whole command; the probe's own 3s default finishes inside it, so the
# verdict is always an exit code rather than a kill.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
  CMD ["/app/` + name + `", "healthcheck"]

`
}

// tailwindPinnedVersion is the version half of the Devbox package identifier,
// which is the one place the pin is written.
func tailwindPinnedVersion() string {
	_, version, found := strings.Cut(tailwindDevboxPackage, "@")
	if !found {
		return "latest"
	}
	return version
}

// dockerignoreScaffold keeps the build context to sources. Everything excluded
// here is either rebuilt inside the image or belongs to this machine.
func dockerignoreScaffold(options initOptions) string {
	return `# The image rebuilds these. A host copy would be carried in and then
# overwritten, and a stale one that survived would be linked instead.
**/*_pw_gen.go
dist/

# This machine's, or this developer's. config.` + pwenv.Development + `.toml carries a local DSN and
# generated secrets, and an image has no use for either. The local dotenv files
# hold this machine's secrets; the deployment sets its own at run time.
` + pwenv.FileName(pwenv.Development) + `
` + pwenv.DotenvFileName(pwenv.Development) + `
` + pwenv.DotenvBase + pwenv.DotenvLocalSuffix + `
` + pwenv.DotenvBase + `.*` + pwenv.DotenvLocalSuffix + `
*.db
.devbox/

# Committed, unlike the rest of this group, but still development-only: it
# configures the services devbox starts beside pw dev, and the image runs none.
devbox.d/

.git/
.github/
.vscode/
.knowledge/

# The binary a local pw build left behind.
/` + options.Name + `
`
}

// productionConfigScaffold is the second environment file. A promoted artifact
// reads the file named for the environment it runs in, so a project carrying
// only the development one has nothing for anywhere it is deployed, and the
// first deployment invents the file under time pressure.
//
// Configuration resolution takes the first readable candidate and stops, so
// this file is a complete configuration rather than a set of overrides on the
// development one. What it cannot carry is a secret: an image layer is readable
// by anyone who can pull the image.
func productionConfigScaffold(options initOptions) string {
	config := `# Production runtime configuration, selected by APP_ENV=` + pwenv.Production + `.
#
# Resolution takes the first readable file and stops, so this is the whole
# configuration rather than a set of overrides on config.` + pwenv.Development + `.toml. To print
# every key this project defines, including the ones no scaffold can guess:
#
#   APP_ENV=` + pwenv.Production + ` ./` + options.Name + ` --generate-config=toml
#
# A value the deployment owns arrives as ${NAME}, expanded when the file loads;
# an undefined name is a load error rather than an empty value. No secret
# belongs in this file: it is copied into the image, and an image layer is
# readable by anyone who can pull it.
[server]
port = 8080
# The container probe reads these. An unset key makes pw healthcheck exit 1
# naming the key, rather than reporting anything about the process.
health = "/healthz"
readiness = "/readyz"
# openapi and api_doc are deliberately absent. An unset path serves nothing,
# which is how the document and its UI stay a development surface.

[observability]
minimum_level = "info"
service_name = "` + options.Name + `"
# One JSON object per line, which is what a log collector reads. The container
# writes to stdout and nowhere else.
stdout_format = "json"
`
	if options.Database {
		engine := engineFor(options.Engine)
		config += databaseRuntimeSection(pwenv.Production, engine.DSN(options.Name), engine)
	}
	return config + productionConfigGaps(options)
}

// productionConfigGaps names the sections config.dev.toml has that this file
// does not. They carry development endpoints, development credentials, and a
// generated secret, so copying them across would be worse than leaving them
// out — but leaving them out silently would produce a deployment that starts
// with a capability switched off rather than one that fails.
func productionConfigGaps(options initOptions) string {
	var missing []string
	if options.Dynamo {
		missing = append(missing, "middleware.dynamo")
	}
	if options.Firestore {
		missing = append(missing, "middleware.firestore")
	}
	if options.Session != "" {
		missing = append(missing, "session")
	}
	if options.Auth != "" && options.Auth != authNone {
		missing = append(missing, "auth")
	}
	if len(missing) == 0 {
		return ""
	}
	return `
# STILL TO WRITE: ` + strings.Join(missing, ", ") + `
#
# config.` + pwenv.Development + `.toml has these sections and this file does not, because every
# value in them is a development endpoint, a development credential, or a
# secret generated for this machine. Print the full scaffold with the command
# at the top of this file, then supply the deployment's values — the secrets
# through the environment, which the keys below accept:
#
#   SESSION_KEYRING_SECRET   signs and seals everything the browser carries
#   SESSION_COOKIE_SECRET    seals the cookie session backend
#   SESSION_REDIS_DSN        the Redis or Valkey session server
#
# The server will not start with a section missing that its code path needs,
# which is the intended failure: a deployment that half-starts is worse.
`
}
