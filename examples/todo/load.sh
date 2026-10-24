#!/usr/bin/env bash
# Starts PostgreSQL, seeds a fixed table, runs both services in turn, and puts
# k6 through each of them.
#
# The two services are measured alternately rather than one after the other, so
# that a machine that gets busier partway through moves both numbers instead of
# only the second one. Run from this directory.
set -euo pipefail

cd "$(dirname "$0")"
here=$(pwd)
bin=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null || true; rm -rf "$bin"' EXIT

# The application is still a normally compiled binary. It runs from a temporary
# directory only so the production-shaped benchmark configuration can be read as
# config.dev.toml: loopback HTTP is then allowed to override the Secure flag,
# while every other benchmark setting remains identical to config.bench.toml.
pwrun="$bin/pw-run"
mkdir -p "$pwrun"
cp popcornweb/config.bench.toml "$pwrun/config.dev.toml"

PASSES=${PASSES:-3}
VUS=${VUS:-20}
DURATION=${DURATION:-20s}
ROWS=${ROWS:-50}
SECRET=${SESSION_KEYRING_SECRET:-w7pGbK0UFoQhTImvxy9KptkfwkIki7dm94avz1Pi4UY=}
# The Debian-based image rather than the Alpine one: musl's libc is slower than
# glibc's, and a database benchmark should not be measuring the libc. The Debian
# release is in the tag so that a new stable cannot move the base under a
# comparison.
PG_IMAGE=${PG_IMAGE:-postgres:17-trixie}

echo "==> PostgreSQL ($PG_IMAGE)"
docker rm -f todo-pg >/dev/null 2>&1 || true
docker run -d --name todo-pg \
  -e POSTGRES_USER=todo -e POSTGRES_PASSWORD=todo -e POSTGRES_DB=todo \
  -p 5433:5432 "$PG_IMAGE" >/dev/null
# pg_isready can succeed during image initialization before POSTGRES_DB has
# created todo. Probe the database itself so the schema command cannot race it.
until docker exec todo-pg psql -qU todo -d todo -c 'SELECT 1' >/dev/null 2>&1; do sleep 1; done
docker exec -i todo-pg psql -qU todo -d todo < schema.sql >/dev/null

# A fixed row count, because the load is read-only and both services must answer
# the same query. Titles carry characters that have to be escaped, so neither
# renderer gets a free pass on inert text.
docker exec todo-pg psql -qU todo -d todo -c "TRUNCATE todos RESTART IDENTITY;" >/dev/null
docker exec todo-pg psql -qU todo -d todo -c \
  "INSERT INTO todos (title, done) SELECT 'Task ' || g || ' — review & ship <today>', (g % 3 = 0) FROM generate_series(1,${ROWS}) g;" >/dev/null
echo "    seeded ${ROWS} rows"

echo "==> building"
(cd stdhttp && go build -o "$bin/todo-std" .)
(cd popcornweb && go build -o "$bin/todo-pw" ./cmd/popcornweb)

echo "==> starting services"
"$bin/todo-std" >"$bin/std.log" 2>&1 &
(cd "$pwrun" && \
  APP_ENV=dev \
  SESSION_COOKIE_SECURE=false \
  SESSION_KEYRING_SECRET="$SECRET" \
  "$bin/todo-pw" >"$bin/pw.log" 2>&1) &

wait_ready() {
  for _ in $(seq 1 40); do
    [ "$(curl -s -o /dev/null -H 'Accept: text/html' -w '%{http_code}' "http://127.0.0.1:$1/")" = "200" ] && return 0
    sleep 1
  done
  echo "service on port $1 never became ready; see $bin" >&2
  return 1
}
wait_ready 8081
wait_ready 8082

# Fail before measuring if the loopback exception stopped working. curl and k6
# both implement the standard cookie rules, so a cookie that survives these two
# HTTP requests is one k6's per-VU jar can keep across its iterations too.
pw_cookie_jar="$bin/pw.cookies"
curl -sS -H 'Accept: text/html' -c "$pw_cookie_jar" -o /dev/null http://127.0.0.1:8082/
first_session=$(awk '$4 == "FALSE" && $6 == "pw_session" { print $7 }' "$pw_cookie_jar")
curl -sS -H 'Accept: text/html' -b "$pw_cookie_jar" -c "$pw_cookie_jar" -o /dev/null http://127.0.0.1:8082/
second_session=$(awk '$4 == "FALSE" && $6 == "pw_session" { print $7 }' "$pw_cookie_jar")
if [ -z "$first_session" ] || [ "$first_session" != "$second_session" ]; then
  echo "PopcornWeb session cookie did not persist over loopback HTTP" >&2
  exit 1
fi
echo "    PopcornWeb session persists across HTTP requests"

# Both must answer with the same row count, or the two are not rendering the
# same page and the throughput numbers describe different work.
std_rows=$(curl -s http://127.0.0.1:8081/api/todos | grep -o '"id"' | wc -l | tr -d ' ')
pw_rows=$(curl -s http://127.0.0.1:8082/api/todos  | grep -o '"id"' | wc -l | tr -d ' ')
if [ "$std_rows" != "$pw_rows" ] || [ "$std_rows" != "$ROWS" ]; then
  echo "row counts disagree: stdhttp=$std_rows popcornweb=$pw_rows expected=$ROWS" >&2
  exit 1
fi
echo "    both serving $ROWS rows"

one() {
  local name=$1 port=$2 out rps fail htm jsn
  out=$(k6 run --quiet -e BASE_URL="http://127.0.0.1:${port}" -e VUS="$VUS" -e DURATION="$DURATION" k6/load.js 2>&1)
  rps=$(printf '%s' "$out"  | grep -E '^[[:space:]]*http_reqs'       | grep -oE '[0-9.]+/s')
  fail=$(printf '%s' "$out" | grep -E '^[[:space:]]*http_req_failed' | grep -oE '[0-9.]+%' | head -1)
  htm=$(printf '%s' "$out"  | grep html_latency | grep -oE 'p\(95\)=[0-9.]+(ns|µs|ms|s)')
  jsn=$(printf '%s' "$out"  | grep json_latency | grep -oE 'p\(95\)=[0-9.]+(ns|µs|ms|s)')
  printf '%-14s %14s  fail=%-6s  html %-14s json %s\n' "$name" "$rps" "$fail" "$htm" "$jsn"
}

echo "==> load (${PASSES} passes, ${VUS} VUs, ${DURATION} each)"
echo "    load average now: $(uptime | sed 's/.*averages*: //')"
for pass in $(seq 1 "$PASSES"); do
  echo "-- pass $pass --"
  one "net/http+pgx" 8081; sleep 4
  one "PopcornWeb"  8082; sleep 4
done
echo "    load average now: $(uptime | sed 's/.*averages*: //')"
echo
echo "Numbers from a machine doing other work are not comparable between runs."
echo "Read the two services against each other within one pass, not across passes."
