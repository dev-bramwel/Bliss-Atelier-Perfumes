# Go foundation and SQL baseline (R05 / #11, R06 / #12)

The active storefront backend remains Express/Prisma. Go exposes root status and foundation health only; order/payment routes are not implemented. See [migration boundaries](../docs/backend-migration.md) and [legacy observations](testdata/legacy/README.md).

## Run and check

Install Go **1.27.3**, matching `go.mod`. This is a supported patch release checked against [official downloads](https://go.dev/dl/?mode=json) and [release policy](https://go.dev/doc/devel/release) on 21 October 2026. Run from `backend/`:

```sh
go mod download
go mod verify
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/api ./cmd/worker ./cmd/migrate
go run ./cmd/api
```

`gofmt -l cmd internal` must print no files. Race checks require cgo and a supported C compiler. Where unavailable on Windows, use the Go Linux container:

```powershell
docker run --rm --mount "type=bind,source=$($PWD.Path),target=/app,readonly" --workdir /app golang:1.27.3 go test -race ./...
```

`go run ./cmd/worker` waits for termination without processing jobs. `go run ./cmd/migrate status` verifies supported schema/history and reports empty, legacy or current state with aggregate counts. Use `init` only for an empty public schema, `adopt-legacy` only for verified Prisma history, and `up` after initialization/adoption. The [SQL migration runbook](../docs/sql-migrations.md) documents locking, maintenance windows, ownership, backup and compatible rollback. `npm start` now generates the Prisma client without automatically deploying migrations.

## Configuration and operational behavior

The [Docker core stack](../docs/docker-core.md) adds non-root images and private local services. `GO_DATABASE_URL_FILE` reads a mounted DSN secret and is mutually exclusive with `GO_DATABASE_URL`. `GO_REQUIRE_SCHEMA=true` enables read-only catalog/ledger-attestation readiness and requires a configured DB plus the local bootstrap's restricted function. Both settings are opt-in; standalone foundation defaults are unchanged.

Export variables in the process environment; Go does not load `.env`. The independent names in [.env.go.example](.env.go.example) prevent accidentally reusing legacy production settings.

| Variable | Default / validation |
| --- | --- |
| `GO_HTTP_ADDR` | `127.0.0.1:5001`; IP or localhost plus port 1–65535 |
| `GO_DATABASE_URL` | Optional PostgreSQL URL for API/worker; required by every migrate command |
| `GO_IO_TIMEOUT` | `5s`; 1ms–1m for DB connection/readiness, a complete migration attempt and provider transport when wired |
| `GO_SHUTDOWN_TIMEOUT` | `10s`; 1ms–1m |
| `GO_DB_MAX_CONNECTIONS` | `4`; 1–32 |

Validation happens before IO. Missing optional database config enables a dependency-free foundation process. If a DB is configured, startup requires a successful bounded ping. `/health/live` means the HTTP process is alive. `/health/ready` checks the configured DB with a deadline; without DB config it means only the foundation is ready. Neither endpoint proves checkout functionality. Root JSON retains the legacy message. No CORS or public deployment is configured for this local foundation.

The HTTP server bounds headers, body size and read/write/idle waits. Provider JSON transport limits responses to 64 KiB, rejects non-2xx and redirects, and bounds total request time. It is not a production Daraja adapter; payload acceptance validation remains payment work. DB pools cap connections and lifetimes. Context cancellation stops worker waiting; SIGINT/SIGTERM disables readiness, drains HTTP within the configured deadline and closes the pool. Windows console termination uses Ctrl+C; abrupt process kills cannot drain.

JSON operational logs contain fixed event names and sanitized errors. They do not log request paths/queries, bodies, customer fields, DSNs or provider response errors. No request identifiers or business telemetry are promised by this slice.

## Disposable integration checks

From repository root on Windows:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File backend/scripts/test-integration.ps1
```

The process-local execution-policy flag permits this script without changing machine policy. It starts an isolated, digest-pinned PostgreSQL image on a random loopback port, waits for readiness, runs the tests and removes only its uniquely named container. The fixture password is public disposable test data. It never reads `DATABASE_URL` or `GO_DATABASE_URL`. `GO_TEST_DATABASE_URL` must target loopback, user `bliss_test` and database `bliss_foundation_test`; the harness supplies it. Foundation checks round-trip an opaque ID and prove query cancellation. Migration checks create/drop the disposable public schema, verify empty/populated upgrades and mismatch/concurrency failures, and rehearse backup/restore/re-adoption with synthetic records. Unconfigured normal unit runs explicitly skip DB tests.

Fake-provider tests are ordinary Go tests: acceptance, rejection, malformed/oversized JSON, timeout, cancellation and redirects. Existing JavaScript helper and fixture checks run with `npm test` after `npm ci` (no live payments). The [OpenAPI draft](api/openapi.json) records observed legacy DTOs/statuses and policy/security exclusions; it does not enable those routes in Go.

## Dependency review and rollback

The only direct runtime dependency is MIT-licensed [pgx/v5 v5.11.0](https://github.com/jackc/pgx), compatible with this toolchain. Its pgpassfile, pgservicefile and puddle dependencies use MIT; Go x/sync and x/text use BSD-style licenses. Checksums and the full graph are committed in `go.sum`/`go.mod`. Standard library handles routing, JSON, logs, signals and provider IO. The repository-owned transactional SQL runner uses pgx without new dependencies; see [ADR 004](../docs/adr/004-sql-migration-baseline.md). Redis/AMQP/Kafka clients remain deferred. Record vulnerability-scan results separately; dependency selection alone is not a security audit.

`x/text` is explicitly raised to v0.41.0 (and its required x/sync v0.22.0) to address the reachable [GO-2026-6629](https://pkg.go.dev/vuln/GO-2026-6629) and [GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970) findings from the initial pgx dependency graph.

No production schema, API traffic or payment writer changes were performed. R06 removes automatic Prisma deployment from legacy startup; use the [migration ownership and rollback procedure](../docs/sql-migrations.md) when adopting a database. Never run two live order/payment writers during later cutover. Historical KES 2,000/pay-on-delivery observations must not override approved R01 policy.

## Local verification, 21 October 2026

Windows amd64 Go 1.27.3: formatting, vet, unit tests, three-command build and module checksum verification passed. `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` reported no vulnerabilities after the transitive dependency update. Disposable PostgreSQL 17.11 integration passed on Docker, including cancellation; the container was removed afterward. Ordinary unit/race runs skip the opt-in DB test, so its separate integration result is required.

Final `go test -race ./...` passed in the Linux Go 1.27.3 container against the patched dependency graph, including readiness deadlines, normal shutdown and forced connection close at the drain deadline. Native Windows race execution was unavailable because no C compiler is installed.

Node 24 container: `npm ci --ignore-scripts` followed by `npm test` passed all seven helper/fixture tests. The install reported 22 existing legacy npm vulnerabilities (one critical); no Node runtime dependency changes are included here. OpenAPI 3.0.3 validation passed using `@apidevtools/swagger-parser@12.1.0` installed only inside a disposable Node container. Static response fixtures are source-derived; full route/browser parity is still a later migration gate.

Evidence is local, including disposable containers; no sandbox provider or production verification is claimed. Authorization review: Go exposes no order/customer/payment routes, and legacy authorization gaps are explicitly excluded from target parity. Operational signals cover process start/stop, config failure and live/ready health; business metrics remain later work. No production schema migration or rollout occurred. PR/reviewer sign-off and issue closure have not been performed; total engineering effort has not been measured.
