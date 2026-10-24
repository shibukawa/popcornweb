# Build and run auction as a container image.
#
#   docker build -t auction .
#   docker run --rm -p 8080:8080 auction
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

# pw generates the code the framework reads, so its version has to match the
# framework this project depends on. go.mod is the one place that records it.
RUN GOBIN=/usr/local/bin go install \
      github.com/shibukawa/popcornweb/cmd/pw@$(go list -m -f '{{.Version}}' github.com/shibukawa/popcornweb)

# The standalone Tailwind executable, pinned to the version devbox.json holds.
# TARGETARCH is set by BuildKit; the release assets spell amd64 as x64.
ARG TARGETARCH
RUN case "${TARGETARCH:-amd64}" in \
      amd64) arch=x64 ;; \
      arm64) arch=arm64 ;; \
      *) echo "no Tailwind release for ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /usr/local/bin/tailwindcss \
      "https://github.com/tailwindlabs/tailwindcss/releases/download/v4.2.4/tailwindcss-linux-${arch}" \
    && chmod +x /usr/local/bin/tailwindcss

COPY . .

# A developer may add a workspace-local replacement while working on Popcorn
# Wave. Such a parent path is outside this Docker build context, so normalize
# the copied module file and build against the version pinned in go.mod.
RUN go mod edit -dropreplace=github.com/shibukawa/popcornweb \
    && go mod tidy

# CGO_ENABLED=0 is what makes the binary static enough for the runtime base
# below. pw build passes the environment through to the compiler.
RUN CGO_ENABLED=0 pw build

FROM gcr.io/distroless/static-debian13:nonroot
WORKDIR /app

# Project-local configuration resolves against the process working directory,
# so the WORKDIR above and this file belong together. Change one and the server
# starts on defaults instead of failing, which is the quieter mistake.
COPY --from=build /src/auction /app/auction
COPY --from=build /src/config.prod.toml /app/config.prod.toml

# An unset APP_ENV means dev, which would look for a file this image does not
# carry and fall back to development defaults.
ENV APP_ENV=prod
EXPOSE 8080

# The image ships no shell and no curl, so the binary is its own probe. It
# reads the same configuration this image already carries, so the port and the
# health path are never repeated here. Docker's --timeout is its patience with
# the whole command; the probe's own 3s default finishes inside it, so the
# verdict is always an exit code rather than a kill.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
  CMD ["/app/auction", "healthcheck"]

ENTRYPOINT ["/app/auction"]
