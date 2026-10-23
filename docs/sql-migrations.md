# SQL migrations and Prisma adoption (R06 / #12)

Implemented locally on 21 October 2026. See [ADR 004](adr/004-sql-migration-baseline.md) and [verification evidence](evidence/r06-sql-baseline.md). Express remains the order/payment application. This slice changes schema ownership only; it does not introduce Go checkout routes or alter business/payment policy.

## Commands and supported states

Run from `backend/`, with `GO_DATABASE_URL` explicitly exported. Go does not load `.env`. Use a direct PostgreSQL connection, not transaction-pooling PgBouncer. Supply a dedicated migration role; runtime roles should have DML only and no schema ownership/DDL. Never put credentials in committed command examples or paste raw PostgreSQL errors into public evidence.

For `init`, provision the migration role with CONNECT and CREATE on the target database plus USAGE and CREATE on `public`. The archived baseline contains `CREATE SCHEMA IF NOT EXISTS`, which requires database CREATE even when the schema already exists. These are migration-role privileges, not runtime-role privileges. For legacy adoption, arrange the appropriate ownership/role membership for locking and managing the existing tables and Prisma history.

```powershell
go run ./cmd/migrate status
# Choose exactly one initial path, based on the inspected database:
go run ./cmd/migrate init
# OR, for a verified existing Prisma database:
go run ./cmd/migrate adopt-legacy
# Subsequent deployments:
go run ./cmd/migrate up
```

| Command | Preconditions and behavior |
| --- | --- |
| `status` | Reports `empty`, `legacy`, or `current`, plus aggregate order/payment counts. Rejects schema/history mismatches. No schema/history changes; releases locks by rollback. |
| `init` | Empty `public` schema only. Creates the legacy baseline, catalog/stock schema and Go checksum ledger atomically. Does not fabricate Prisma history. |
| `adopt-legacy` | Exact supported business schema and one verified successful `20261006120000_init` history record. Records baseline adoption without executing historical table creation. |
| `up` | Requires an existing, verified Go ledger. Validates applied SQL checksums before running pending embedded transactional steps. Currently there are no post-baseline steps. |

All commands acquire Go lock `477418744910` and Prisma lock `72707369`. Lock contention fails immediately. Existing tables are locked with `SHARE ROW EXCLUSIVE NOWAIT` to prevent data/schema changes during verification and inventory. Even `status` briefly excludes writes; perform inventory/adoption during a maintenance window with writers stopped. `GO_IO_TIMEOUT` bounds the whole migration attempt (default 5s, maximum 1m), with PostgreSQL 17 statement/transaction timeouts bounding server work too. Increase it within its supported range for the controlled operation. A canceled client's locks can remain briefly while the server finishes rollback; a busy retry must stop safely and be retried after release. Large future changes need a separately designed timeout/backfill strategy.

The supported scope is PostgreSQL 17's dedicated `public` schema. Additional public tables, routines, sequences, custom types, triggers, policies, altered constraints/indexes or incompatible column definitions cause rejection. Other schemas are outside this ownership contract. Catalog output differences on another PostgreSQL major require compatibility tests before adoption. Existing non-default installations must be reviewed explicitly; do not weaken checks to make them pass.

## Preflight and real-data inventory

1. Identify environment, database/server version, database owner, app and migration roles, schema tools, active writers and pending deployments. Confirm R05/#11 review/evidence status separately; its tracker issue was still open when this slice began.
2. Stop application writers and all Prisma/other schema jobs. Back up before adopting; prevent deployment automation from restarting a competing migration job.
3. With the intended target configured, run `status`. Record state and aggregate counts only. Inspect schema-only exports and `_prisma_migrations` in a restricted session; confirm the single committed migration, completed timestamp, applied step count, no rollback/failure and approved SHA-256. The runner checks LF and CRLF variants of the exact archived SQL, accommodating Git line endings without trusting arbitrary SQL.
4. Inventory database size, row counts, timestamp ranges, orphan/duplicate mappings and any unexpected tables in a restricted session. Verify backup access/retention and the restore target. Do not export customer names, phones, addresses, item payloads or payment/provider identifiers into issue evidence. No real database was accessed by this implementation; its inventory remains an environment-specific prerequisite.
5. Run the chosen explicit path, then `status` and `up`. Compare pre/post aggregate counts and restricted data/history fingerprints. Smoke-test the active application before restarting traffic.

Missing/failed/unknown Prisma history, `db push` databases without history, partial schemas and drift all fail closed. Preserve evidence and investigate against a restored clone; never force a ledger row or overwrite a checksum. Historical SQL remains in `backend/prisma/migrations/`; its embedded copy is checked against that archive by tests.

## Migration ownership and legacy startup

After `init`, before starting Express or another runtime, the table owner or database administrator must grant the runtime role schema usage and DML on the two business tables. A dedicated migration role owns newly initialized tables; PostgreSQL does not automatically grant a separate runtime role access. In a restricted `psql` session, set `runtime_role` to the existing application role (example name below), then run:

```sql
\set runtime_role bliss_runtime
GRANT USAGE ON SCHEMA public TO :"runtime_role";
GRANT SELECT, INSERT, UPDATE, DELETE
  ON TABLE public."Order", public."MpesaTransaction" TO :"runtime_role";
```

Provision runtime login/CONNECT separately using the environment's secret-management process. The runtime must not own these tables, inherit the migration role, or have superuser/DDL privileges. Ensure neither it nor `PUBLIC` can CREATE in the application schema; for a dedicated application schema, an administrator can run `REVOKE CREATE ON SCHEMA public FROM PUBLIC` and revoke any direct runtime CREATE grant. Do not grant access to `bliss_schema_migrations` or use grants/default privileges covering every public table. This baseline uses client-generated text IDs and needs no sequence grants.

For `adopt-legacy`, existing ownership and grants are preserved: verify the same business-table DML access for the active runtime and apply missing grants as the appropriate owner. If the runtime currently owns legacy tables, arrange an explicit ownership/role-hardening procedure before claiming DML-only isolation. Future migrations that create business tables must include a reviewed grant step. Smoke-test order/payment reads and writes using the actual runtime credentials after provisioning, and confirm schema changes and ledger access remain denied before restarting traffic.

`npm start` generates the Prisma client and starts Express; it no longer deploys migrations. For an explicitly legacy-only database before adoption, run `npx prisma migrate deploy` manually using its legacy `DATABASE_URL`, then inspect it. Never run that command on an adopted or Go-initialized database. Go-initialized databases intentionally have no fabricated `_prisma_migrations` history.

After adoption, all schema deployment runs through the single Go migration job. Prisma remains a client for the unchanged quoted tables until the application cutover. Do not use `prisma db push`, reset, dev or resolve against adopted databases. Keep runtime credentials unable to execute DDL. Advisory locks coordinate cooperating tools, not arbitrary administrator SQL. Separate application traffic cutover and callback/payment single-writer gates remain in [backend migration](backend-migration.md).

## Backup, compatible rollback and forward repair

Before a real change, use approved secret injection (for example a restricted `PGPASSFILE` and PostgreSQL service configuration), then take a custom-format `pg_dump` of the full database to protected storage. Record tool/server versions, backup timestamp, access controls and checksum. A schema-only export is useful for comparison but is not a data backup. Restore into a separate, empty, isolated database with `pg_restore --exit-on-error`; verify schema, counts and restricted record/history fingerprints, then run `status` there. Do not assume a backup is usable without a restore test.

The R06 baseline leaves all business columns and Prisma history intact. To roll back application behavior, stop the new migration job and run the compatible Express application with the updated startup script (`node src/server.js` also bypasses the old npm prestart hook). Keep the Go ledger in place. Older Go foundation binaries cannot verify ownership; do not use their connectivity-only `status` as evidence. Never restart an old image that automatically deploys Prisma migrations against the adopted database.

There is no destructive `down`, `reset` or `force` command. A failed SQL step rolls back its DDL and ledger entry together; correct an unapplied step and retry only after inspection. A commit connection failure can have an uncertain outcome: inspect `status` after reconnecting before retrying. An already committed migration is immutable; use a reviewed new compatible forward migration for repair, preserving evidence. New schema steps require ordered embedded registration, updated version-specific schema expectations and upgrade/failure tests; R06 introduced baseline version 1. R09 now registers version 2 for catalog/stock; see the [stock workflow](stock-workflow.md) for the current schema, runtime grants and compatible rollback requirements.

If application rollback is insufficient, restore the verified pre-change backup to a separate target, validate it, and coordinate switching the single application writer. Writes after the backup require reconciliation and may be lost on restore; stop writers and preserve the failed database before making that decision. Do not blindly restore over a live payment database. R06's disposable restore is a local mechanism drill, not an approved production RPO/RTO, retention plan or live recovery claim.

## Repeat local verification

From repository root, with Docker Desktop and the pinned Go toolchain:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File backend/scripts/test-integration.ps1
```

The harness creates a uniquely named digest-pinned PostgreSQL container on a random loopback port, runs guarded integration tests, backs up synthetic legacy records/history inside that container, adopts, restores, verifies a full-record fingerprint, and adopts again. Cleanup removes only its own container and its local backup. It never reads `DATABASE_URL` or `GO_DATABASE_URL`; it restores process-local test variables afterward. Normal unit runs skip database/recovery tests unless explicitly configured by this harness.
