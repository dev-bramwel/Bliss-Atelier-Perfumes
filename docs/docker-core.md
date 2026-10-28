# Docker core stack (R07 / #13)

Implemented local frontend/API/PostgreSQL with a one-shot migration job and an isolated fake provider. See [verification evidence](evidence/r07-docker-core.md). Infrastructure/observability overlays and worker processing remain later slices.

## Fresh checkout

Requires Docker with Linux containers, Compose supporting successful one-shot dependencies and `up --wait` (tested 5.6.0), and Go 1.27.3. Initial image/module downloads need network access. From repository root in PowerShell:

```powershell
Push-Location backend
go run ./cmd/devstack secrets ../.local/dev
Pop-Location
docker compose config --quiet
docker compose build db api frontend
docker compose up -d --wait --wait-timeout 120
```

Open `http://127.0.0.1:8080/`, `/cart.html`, or `/checkout.html`. API base is empty; `/api/` proxies to internal Go. `/api/health/live` and `/api/health/ready` exercise this same-origin route. The Go foundation still has no order/payment routes: this serves the checkout interface, but order submission returns 404 until the order/payment migration slices. The fake provider is a transport fixture, not a checkout backend.

With GNU Make, use `make setup build up`; other targets are `down`, `logs`, `test`, and Windows `docker-test`. Direct Go/Compose commands work on Linux/macOS; the disposable acceptance script currently requires PowerShell. Optional non-secret overrides are in root `.env.example`: project name, loopback port and credential directory. A custom `BLISS_SECRET_DIR` must match the directory supplied to `devstack secrets`.

## Images and boundaries

| Service/artifact    | Contract                                                                                                                                                                                    |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Backend Dockerfile  | Digest-pinned Go 1.27.3 builder, module verification, static trimmed binaries; scratch runtime with CA bundle, UID/GID 10001.                                                               |
| Frontend Dockerfile | Same pinned builder, Go static server/proxy/probe; scratch runtime, CA bundle, local assets, UID/GID 10001.                                                                                 |
| PostgreSQL          | Derived from digest-pinned PostgreSQL 17 Alpine with unused `gosu` removed; native UID/GID 70, persistent named volume, no host DB port. Root startup is not supported by this local image. |
| Migration job       | Empty/current local database only; verifies/runs baseline, installs runtime grants and a restricted readiness function; exits before API starts. Legacy adoption is deliberately rejected.  |
| API                 | Runtime-role DSN, opt-in schema readiness, no host port, no live-provider egress.                                                                                                           |
| Fake provider       | Private, bounded deterministic fixture on 8090, no live credentials or external calls.                                                                                                      |
| Frontend            | Only published port: loopback 8080. Fixed upstream `api:5001`; client forwarding headers discarded; sanitized proxy failures.                                                               |

Application filesystems are read-only. All services are non-root, drop capabilities, use no-new-privileges, and bound CPU, RAM and processes. API/worker/migrate/probe/devstack binaries share an image; worker processing is not enabled. No Docker socket or home directory mounts. The database uses its normal writable runtime/data locations.

DB/API/migration/provider use an `internal: true` network only. Frontend also has an edge network for loopback publication. Application containers cannot reach live payment hosts. Tailwind CSS, scripts and images are local. Optional browser Google-font requests may fail offline; fallback fonts keep the page usable. Image pins live in Dockerfiles/Compose and require reviewed upgrades.

## Credentials and roles

`devstack secrets` generates random local passwords and separate migrator/runtime DSNs in ignored `.local/dev/`. It prints no values, leaves a complete existing set unchanged, and rejects a partial set. Keep credentials with their volume: generating new files does not rotate passwords in an existing PostgreSQL database. This initializer is exclusively for synthetic local work, not merchant databases.

Secrets are file-mounted per service: PostgreSQL receives role passwords, migration receives only its DSN, API receives only the runtime DSN; frontend/provider have none. Environment contains file paths, not DSN/password values. Build contexts allow required source/assets only and exclude env/key files; passwords are never build arguments. On Unix the secret directory is 0700 with 0444 leaf files so distinct non-root container UIDs can read the bind-mounted leaves while other host users cannot traverse the parent. On Windows protect the directory with user ACLs. Compose file secrets are not an encrypted production store.

PostgreSQL initialization creates `bliss_migrator`, owner of the local database, and `bliss_runtime`, with CONNECT, schema USAGE and business-table DML only. Runtime cannot alter schema or directly read/write the migration ledger. Local bootstrap adds `bliss_ops.schema_ready()`, a SECURITY DEFINER boolean function with a fixed pg_catalog search path and fully qualified ledger query. Only the runtime role gets EXECUTE/USAGE; PUBLIC access is revoked. It checks exact baseline version/checksum/origin. Go readiness also compares public catalog definitions with the committed contract in a read-only transaction.

The immutable baseline and business schema remain unchanged; local bootstrap adds only grants and the private operations schema/function. Real/legacy databases require the [SQL adoption runbook](sql-migrations.md), not this local bootstrap. Database-admin access inside the private container is for development drills, never granted to the API. Never publish completed credentials, rendered secret configuration, customer records or raw database/provider errors.

## Startup, persistence and rollback

Healthy DB gates migration; successful migration gates API; ready API gates frontend. Failed migration exits nonzero and blocks dependent startup. Schema drift after startup makes readiness 503 while liveness stays 200. Starting API separately cannot bypass that schema check.

Routine shutdown and recreation preserve the named database volume:

```powershell
docker compose down
docker compose up -d --wait
```

**`down --volumes` destroys database data**, so never use it for routine shutdown. The test harness uses it only with a generated disposable project. Do not delete matching credentials while keeping the volume. Use the [backup/recovery procedure](sql-migrations.md) before schema changes; R06's restore drill remains separately available.

Inspect a failed migration and correct the actual cause or apply a reviewed forward repair, then rerun `up --wait`. Never fix production by rewriting a checksum. The test deliberately changes/restores its known synthetic ledger hash only to prove failure/recovery gates. Processes handle SIGTERM; API has 15 seconds from Compose for its default 10-second drain. Roll back compatible application images/config while preserving schema, ledger, volume and matching credentials. Disabling schema readiness weakens the gate and needs operational review. No production rollout or payment traffic cutover occurs here.

## Provider fixture and repeat checks

The [Docker core CI workflow](../.github/workflows/docker-core.yaml) runs on PRs targeting `develop`/`main`, pushes to those branches, and manual dispatch. Its `Docker core validation` check builds and exercises this stack on Ubuntu, runs Go race/build checks and the migration/restore harness, and verifies committed frontend CSS matches a locked clean build. High/critical frontend dependency advisories fail the job. It uses pinned actions and read-only repository permissions, with no deployment secrets or registry publication. Required-check enforcement is a separate branch-protection setting.

Private `POST http://fake-provider:8090/payments` accepts synthetic `{"amount":2500}`. `X-Fixture-Mode` selects acceptance, `reject` (422), or `malformed` JSON. Responses/IDs are fixed synthetic values. This is neither a Daraja nor NCBA emulator and never marks actual orders paid.

```powershell
docker compose run --rm --no-deps --entrypoint /app/devstack api provider-check
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/Test-DockerCore.ps1
```

The acceptance script uses its own project, random loopback port, ignored credentials and database volume. It checks served checkout/assets/proxy; non-root identities; private ports; read-only app filesystems; CA bundle; generated credentials absent from final image layers; provider modes; persistence across recreation; readiness under schema mismatch; failed-migration startup blocking; and recovery. It removes only that generated project's containers/networks/volume. Ignored synthetic credential directories remain for inspection. Its layer scan is specific to generated credentials, not a universal secret scanner.

Tailwind build tooling is lockfile-pinned and dev-only. Regenerate the committed CSS with Node 24 in `frontend/`: `npm ci --ignore-scripts`, then `npm run build:css`. Review the output. Docker images contain no Node/npm and do not install frontend build dependencies. Dependency checks for this toolchain are separate from the unchanged legacy backend npm graph.

Sources: [Compose startup ordering](https://docs.docker.com/compose/how-tos/startup-order/), [file-mounted Compose secrets and limitations](https://docs.docker.com/compose/how-tos/use-secrets/), [official PostgreSQL initialization](https://hub.docker.com/_/postgres).
