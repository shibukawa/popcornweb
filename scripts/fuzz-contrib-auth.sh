#!/bin/sh

# Short host-Go fuzz smoke matrix for the authentication contrib set.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

export GOCACHE=${GOCACHE:-/tmp/petitweb-go-fuzz-cache}
FUZZTIME=${FUZZTIME:-2s}
go_bin=${GO:-go}
if ! command -v "$go_bin" >/dev/null 2>&1; then
	echo "missing Go executable: $go_bin" >&2
	exit 2
fi

while read -r package target; do
	[ -n "$package" ] || continue
	echo "-- $package $target ($FUZZTIME)"
	"$go_bin" test "$package" -run="^${target}$" -fuzz="^${target}$" -fuzztime="$FUZZTIME"
done <<'EOF'
./contrib/internal/authn FuzzValidateJSON
./contrib/internal/authn FuzzDecodeBase64URL
./contrib/internal/authn FuzzParseEndpoint
./contrib/jwt FuzzParse
./contrib/jwt FuzzParseJWKS
./contrib/oauth FuzzTokenResponse
./contrib/oauth FuzzScopeGrammar
./contrib/oauth FuzzDeviceAuthorizationResponse
./contrib/oauthprofile FuzzProfileResponse
./contrib/oidc FuzzIDTokenParsing
./contrib/oidc FuzzBearerTokenValidation
./contrib/passkey FuzzDecodeAuthenticationCredential
./contrib/passkey FuzzRPIDConfiguration
EOF
