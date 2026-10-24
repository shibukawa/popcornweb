# todo — the same application twice

One todo list, written two ways against one PostgreSQL table, so that binary
size and throughput can be compared without arguing about whether the two
programs do the same thing.

| Directory | What it is |
| --- | --- |
| `stdhttp/` | `net/http`, `html/template`, `encoding/json`, `pgx` |
| `popcornweb/` | Popcorn Web: typed templates, typed SQL, generated binding |

Both serve the same five routes — an HTML list, a JSON list, and three form
posts — and `load.sh` refuses to measure them if their JSON bodies disagree on
the row count.

## Running one

```sh
docker run -d --name todo-pg \
  -e POSTGRES_USER=todo -e POSTGRES_PASSWORD=todo -e POSTGRES_DB=todo \
  -p 5433:5432 postgres:17-trixie
docker exec -i todo-pg psql -U todo -d todo < schema.sql
```

The image is the Debian one on purpose. Alpine's musl libc is slower than
glibc's, and a database benchmark should not be measuring the libc.

```sh
cd stdhttp && go run .              # http://127.0.0.1:8081
```

```sh
cd popcornweb && APP_ENV=dev go run ./cmd/popcornweb   # http://127.0.0.1:8082
```

## Comparing binary size

```sh
./sizes.sh
```

Six configurations per service. A build that fails is reported as `fails` with
its first error, because for this pair that is a result rather than a gap: `pgx`
does not compile under TinyGo at all, and neither service reaches `wasip1`.

## Comparing throughput

```sh
./load.sh
```

Needs [k6](https://k6.io). It seeds a fixed 50 rows, starts both services, and
alternates k6 between them so that a machine which gets busier partway through
moves both numbers rather than only the second.

Both are ordinary compiled binaries; the script does not run `pw dev`. For the
loopback HTTP measurement it copies `config.bench.toml` to a temporary
`config.dev.toml` and overrides only `SESSION_COOKIE_SECURE=false`. k6's
per-VU cookie jar can therefore keep one session across iterations, while the
logging, connection-pool, and middleware settings stay those of the benchmark.

`stdhttp/` can run on either database layer, which is how the framework's share
of the gap was separated from its database access:

```sh
DB_DRIVER=pgxpool DB_POOL=25 go run .   # pgx's own pool
DB_DRIVER=sqldb   DB_POOL=25 go run .   # database/sql + pgx/stdlib, as the framework does
```

Two runs of five passes each, all at 25 connections. The two drivers cannot be
read from their absolute rates, because they were measured in separate runs and
the machine is not the same machine twice; each run's framework service, which
does not change between them, is what normalises the pair:

| | `stdhttp` ÷ `popcornweb`, same run |
| --- | --- |
| `stdhttp` + pgxpool | 0.960 |
| `stdhttp` + `database/sql` + pgx/stdlib | 0.934 |

The database layer is worth 3 points, unchanged from when this control was first
run. A CPU profile showing `database/sql.withLock` at 15% of samples suggests it
should be worth far more, which is the point of running the control: a lock can
hold CPU share without being what limits throughput.

The larger lever is `session.backend` in `popcornweb/config.bench.toml`. Same
application code, ten passes each (two runs of five; nine for `dev-volatile`).
Each framework row is read against the baseline measured in the same two runs,
which is why the baseline appears twice:

| | requests/s | vs baseline | HTML p95 |
| --- | --- | --- | --- |
| `net/http` + pgx, with `cookie` | ~14,300 | — | 3.23–3.98 ms |
| `popcornweb`, `cookie` | ~14,750 | +3% | 2.58–3.55 ms |
| `net/http` + pgx, with `dev-volatile` | ~15,350 | — | 2.80–4.06 ms |
| `popcornweb`, `dev-volatile` | ~17,550 | +14% | 1.82–2.64 ms |

A cookie session sends 639 bytes of `Set-Cookie` per response against 299, and
the browser returns all of it on every request. Sealing and opening one measures
0.44 µs for a 256-byte record and 0.72 µs for a 1 KB one, so the cost is bytes
rather than crypto. `dev-volatile` is a development backend; `rdb`, `redis`, and
`dynamo` are its production shapes and each adds a storage round trip in
exchange for the bytes.

Per request the framework spends 149 µs of CPU against the baseline's 204. The
gap is wider than the throughput rows suggest because the profile runs with
mutex and block profiling on, which costs the framework more than the baseline —
so 149 is an over-estimate of its own cost and the real distance is larger.

### The order within a pass is worth about six points

`load.sh` measures the baseline first and the framework second in every pass.
Alternating passes was meant to keep a machine that gets busier partway through
from moving only the second number, and it does — but it leaves the order inside
a pass fixed, and on sustained load an M3 is measurably slower twenty seconds
later than it was at the start of the pass. Whichever service goes second pays
for that.

Running the same cookie configuration with the order swapped puts the framework
9% ahead of the baseline where the shipped order puts it 3% ahead. Neither is
the answer; the difference between them is the bias, and about 6% is the honest
midpoint. The ratio tables above keep the shipped order, which means every
framework row in them is the conservative one.

A session on a busier machine put the baseline at 11,400–11,700 rather than
14,300–15,400 and left both ratios recognisable, which is why these tables are
for the ratios rather than the absolute rates. A run whose passes disagree with
each other by more than a few points was measured against something else running
on the machine and should be thrown away rather than averaged. So should a single
pass in which one service alone loses a quarter of its usual rate while the other
holds: that is something else landing on the machine for twenty seconds. Two such
passes were dropped from the numbers above, one from the `dev-volatile` runs (the
framework at 11,600 against a usual 17,600) and one from the swapped-order runs
(the baseline at 10,500 against 15,100).

`k6/load.js` sends `Accept: text/html` on the page request because the framework
issues a CSRF token only for a request that looks like a document. A generator
that omits it is not measuring the page path, and the form render fails.

The load is read-only. Writes would grow the table during the run, so whichever
service went second would be answering a larger query.

## What the comparison is not

The two services are not feature-equivalent, and the size and throughput numbers
should be read knowing which way that cuts.

`popcornweb/` additionally has configuration binding per environment, an
OpenAPI document, `/healthz` and `/readyz`, structured errors, security headers,
request IDs, a session, and a CSRF token in every form. `stdhttp/` has one
middleware that sets a request ID header. So the framework carries more bytes
and does more per request, and both show up in the measurements.

Making them equal would mean writing all of that by hand in `stdhttp/`, which is
the comparison the framework exists to avoid — so the honest presentation is to
measure what each actually is and say what the difference includes.

`config.bench.toml` is the configuration the load test uses. It exists because
`config.dev.toml` logs at debug and prints every statement the database runs,
which costs several times the work being measured.
