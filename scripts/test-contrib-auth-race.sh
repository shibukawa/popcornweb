#!/bin/sh

# Race detector gate for the authentication contrib packages.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

export GOCACHE=${GOCACHE:-/tmp/petitweb-go-race-cache}
go_bin=${GO:-go}
if ! command -v "$go_bin" >/dev/null 2>&1; then
	echo "missing Go executable: $go_bin" >&2
	exit 2
fi

"$go_bin" test -race \
	./authstate \
	./authstate/memory \
	./contrib/internal/authn \
	./contrib/jwt \
	./contrib/oauth \
	./contrib/oauthprofile \
	./contrib/oidc \
	./contrib/passkey
