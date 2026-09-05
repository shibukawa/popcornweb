#!/bin/sh

# Reproducible host/TinyGo smoke matrix for the authentication contrib set.
# Set TINYGO to a pinned executable or invoke this script inside Dockerfile.dev.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

packages='
./authstate
./authstate/memory
./authstate/redis
./contrib/internal/authn
./contrib/jwt
./contrib/oauth
./contrib/oauthprofile
./contrib/oidc
./contrib/passkey'

export GOCACHE=${GOCACHE:-/tmp/petitweb-go-build-cache}

echo '== host Go =='
go_bin=${GO:-go}
if ! command -v "$go_bin" >/dev/null 2>&1; then
	echo "missing Go executable: $go_bin" >&2
	exit 2
fi
"$go_bin" test $packages

tinygo_bin=${TINYGO:-tinygo}
if ! command -v "$tinygo_bin" >/dev/null 2>&1; then
	echo "missing TinyGo executable: $tinygo_bin" >&2
	exit 2
fi

echo "== $($tinygo_bin version) =="
for package in $packages; do
	echo "-- $package"
	"$tinygo_bin" test "$package"
done
