# R06 / issue #12: local SQL baseline evidence

Date: 21 October 2026. Branch: `feat/12-sql-migration-baseline`. Scope: [issue #12](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/12), [ADR 004](../adr/004-sql-migration-baseline.md), [operator runbook](../sql-migrations.md).

## Implemented behavior

The pgx SQL runner supports explicit empty initialization and verified legacy adoption. It verifies catalog definitions and successful Prisma history before adoption, preserves historical SQL/history, records SHA-256 in `public.bliss_schema_migrations`, and serializes migration operations using both Go and Prisma advisory locks. All SQL and ledger changes share a transaction. Server statement/transaction deadlines bound abandoned work as well as client cancellation. There are no schema reset, force or destructive down commands.

The baseline's LF SHA-256 is `0bf265f9da0f9aaf07cb4a91e54efc03bad02aa33935806b1ba8d03d96226e49`. The archive equality and pinned hash are tested; legacy history permits only its LF/CRLF variants. The PostgreSQL 17 schema contract snapshot was derived from the committed baseline and standard Prisma metadata table definition, and is compared against freshly constructed fixtures in the integration suite.

## Verification and acceptance mapping

| Requirement | Local evidence |
| --- | --- |
| Empty database and empty legacy migration | Disposable integration tests initialize empty public schema, adopt an empty Prisma schema, require explicit path selection, and verify subsequent `up` is idempotent. |
| Populated legacy data/history preservation | Synthetic paid and pending orders plus a successful linked payment. Full JSON snapshots before/after adoption compare every field in both business tables and every Prisma history record, including IDs, totals, items, timestamps and provider mapping. |
| Mismatch rejection | Missing/changed columns, defaults, types, FK and unique index; extra tables/sequences; RLS; missing, unknown, incomplete, rolled-back, duplicate or altered-checksum Prisma history; altered Go checksums, versions, origins and ledger schema. Rejected legacy adoption creates no ledger. |
| Concurrent migration failure | Holding either advisory lock in another transaction makes adoption fail without data/history changes; adoption succeeds after release. |
| Failure/cancellation | A failing step after DDL leaves no partial table or ledger entry. A canceled sleeping step rolls back, and safe retries succeed after PostgreSQL releases its bounded transaction locks. A repeat run exposed asynchronous lock release; server-side deadlines and bounded retry assertions address it. |
| Compatible forward repair | Test-only ordered comment migrations apply once, preserve all legacy records/history, and reject subsequent edits to applied SQL. These are runner tests; no post-baseline business migration ships. |
| Backup and restore | Harness seeds populated legacy records, takes a PostgreSQL custom-format dump, adopts, removes the disposable schema, restores, compares a fingerprint over full records/history, and adopts again. Local drill passed with the fingerprint unchanged. |

Repeat from repository root:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File backend/scripts/test-integration.ps1
```

Environment: Windows amd64 Go 1.27.3 and Docker PostgreSQL 17.11, digest `sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24`. Harness uses a uniquely named disposable container, random loopback port and synthetic fixture credentials. The test URL guard accepts only loopback, test user/database and the explicit test query parameters. Container-local backups are removed with the container. No configured application database URL is used to select a test target.

Go formatting of changed files, vet, unit tests, module verification and API/worker/migrate build passed locally during implementation. Normal unit runs skip opt-in DB/recovery tests; the separate disposable harness result above is required evidence.

A clean Node 24 container ran `npm ci --ignore-scripts` and all seven legacy JavaScript tests successfully. Its actual `prisma migrate deploy` created the legacy schema in the disposable PostgreSQL database; native `go run ./cmd/migrate status`, `adopt-legacy`, and `up` then succeeded against that Prisma-created schema. This supplements the synthetic history fixture with actual Prisma metadata compatibility. Node image digest: `sha256:7b559cf7f77d837192392499a850abb1a3d1f49fbc2bd0692d54b212c9e38f6d`.

The clean npm install reported the existing 22 legacy dependency vulnerabilities (1 low, 7 moderate, 13 high, 1 critical). This slice changes no Node dependencies or lockfile; those findings remain unresolved legacy risks.

Final Linux Go 1.27.3 `go test -race ./...` passed against a fixed source snapshot, with `GO_TEST_DATABASE_URL` pointing to the disposable PostgreSQL container: database and migration integration tests ran rather than skipping. The earlier race attempt overlapped a source import edit and failed compilation; the fixed-snapshot rerun resolved that test setup failure. Native Go vet/unit/build/module checks, changed-file formatting, `git diff --check`, and links in the new documentation passed. Both the harness-owned containers and the additional Prisma compatibility container were removed after checks.

## Change and operational review

PR #43 [Codex finding on runtime grants](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/pull/43#discussion_r4341781521): the runbook now requires explicit post-init schema USAGE and business-table DML grants before starting the runtime. It also records the database CREATE prerequisite for the migration role's archived CREATE SCHEMA statement. A disposable regression test initializes under a separate nonsuperuser migration role with no runtime default privileges, reproduces denied runtime reads, applies the documented grants, and proves runtime INSERT/SELECT/UPDATE/DELETE for both business tables. Runtime CREATE/ALTER and ledger SELECT/UPDATE remain denied. The complete PostgreSQL integration suite and backup/restore/re-adoption harness passed again; migration vet, unit tests, formatting and diff checks passed. The immutable historical SQL remains unchanged; environment-specific role names and credentials are provisioned outside the baseline.

- Schema: existing business tables and Prisma history remain intact; adds only the Go migration ledger during initialization/adoption. Empty initialization creates the historical business schema without inventing Prisma history.
- Configuration: no new application variable; all migration commands require `GO_DATABASE_URL`. Existing `GO_IO_TIMEOUT` also bounds the migration attempt. PostgreSQL 17 is the tested migration target.
- API/payment behavior: no endpoint, pricing, payment writer, callback or settlement changes.
- Startup/ownership: Express `npm start` still generates Prisma client but no longer deploys Prisma migrations. Legacy-only setup now deploys explicitly. Adopted databases use the Go migration job exclusively; old automatic Prisma prestart images are unsuitable for rollback without bypassing that hook.
- Authorization: migration credentials need appropriate schema/DDL access. Runtime DML-only roles, stopped competing schema jobs and a maintenance window are deployment prerequisites. No authorization endpoint is introduced; catalog checks do not constitute an audit of all database privileges or administrator activity.
- Secrets/PII: process failures sanitize driver errors. Successful migration events include the validated command, state and aggregate row counts only. Fixtures are synthetic. No real customer records or credentials were inventoried or exported.
- Signals: structured `migration_status` and nonzero `process_failed` events identify command outcome. External alerts, production monitoring and deployment-job integration remain their roadmap slices.
- Recovery: preserve compatible schema and ledger for application rollback; use reviewed forward repair for committed changes. Restore backup to a separate validated target if necessary, reconciling post-backup writes. Detailed procedure is in the runbook.

## Limits and remaining delivery work

Evidence is local only, not sandbox-provider or production evidence. No real database inventory/adoption, live payment, public rollout, production RPO/RTO, or off-host backup/retention claim is made. Real-environment inventory and restore verification remain deployment prerequisites. Other PostgreSQL majors and databases with additional public schema objects require explicit compatibility work.

R05/#11 remained open in the tracker at implementation start; its review/closure is separate from the foundation code present here. R06 PR linking, external review, CI and issue closure have not been performed. New migrations must extend the ordered embedded list and schema contract with upgrade/failure tests. Actual engineering effort has not been measured; this evidence does not assert the provisional estimate was met.
