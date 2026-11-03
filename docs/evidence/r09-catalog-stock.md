# R09 catalog and stock evidence

Issue: [#15](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/15). Branch: `feat/15-dtabase-catalogue`. Verification: 22-23 October 2026, disposable local Windows/Docker environments. No production database or payment provider was changed. See [plan](../issue-15-plan.md) and [operator workflow](../stock-workflow.md).

## Delivered behavior and acceptance

- Catalog migration preserves all 23 IDs/media mappings and supplies category/product slugs. Tests verify each referenced image exists. Effective prices come from DB common/individual configuration, defaulting to approved KES 2,500. The frontend no longer contains its own price catalog.
- Stock batches are validated and transactional. All-product opening counts and recounts, receipts, verified local sales and corrections have immutable event references, actor/evidence labels and ledger entries. Exact retries do not change stock; changed content under the same ID and a new opening ID after opening both fail.
- Tests cover malformed/missing/null/fractional quantities, duplicate/unknown mappings, atomic failure, expected-balance conflicts, receipt/recount, approval invalidation, sale retry, concurrent last-bottle sales and sum-of-ledger reconciliation.
- Owner confirmed database replacement and approved at-most-24-hour reconciliation approval in the project conversation. Initial/unopened/expired/zero stock is unavailable; DB failure returns HTTP 503 rather than fallback data. Browser loading/failure/staleness and unsafe media rejection are tested. No external integration is selected.
- Real opening balances are **not reconciled**: the owner will supply counts. [The template](../templates/opening-stock.json) intentionally contains null quantities that are rejected. Merchant discrepancy/count sign-off and the real opening import/report are outstanding. Keep #15 open until that evidence exists.

## Verification results

- `go test ./...`, `go vet ./...`, `go build ./cmd/...`: passed locally. Focused stock CLI validation tests also exercise bounded strict JSON input. No Go dependency changes.
- `powershell -NoProfile -ExecutionPolicy Bypass -File backend/scripts/test-integration.ps1`: passed against disposable pinned PostgreSQL 17.11. Includes empty/populated legacy migrations, schema/checksum/locking failure gates, stock fixtures and pre-upgrade backup/restore/re-adoption with full legacy data/history fingerprint preserved. Historical baseline SQL/checksum was not changed.
- Additional focused DB tests: catalog v1-to-v2 upgrade and idempotent re-upgrade, missing table rejection, product/media/price checks, stock workflow and trusted operator grants passed. Runtime cannot mutate stock/control or read approval history. Stock operator cannot edit prices or delete ledger entries. Approval rejects ledger mismatch.
- `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/Test-DockerCore.ps1`: passed in disposable Compose project `bliss-r07-check-4a5727da87`. Includes non-root/private services, credential layer checks, fixture provider behavior, persistence, deliberately failed migration startup, recovery, and proxied catalog with 23 KES 2,500 unavailable products. Subsequent CLI/input and frontend refresh refinements were checked separately with focused tests; the full Compose drill was not repeated for those refinements.
- Locked Node container checks: OpenAPI validation and legacy response fixtures passed; all 7 backend Node tests and the frontend test passed. Tailwind output was regenerated with the pinned toolchain and matched a second build (the first byte comparison detected a difference in the checked-out artifact).
- Frontend Node VM test: passed for DB prices, totals, staleness, failure clearing, HTML escaping and unsafe media rejection. JavaScript syntax checks passed. This is not full browser checkout E2E evidence.

## Schema, configuration, rollback and operational review

New SQL version 2 and schema manifest extend the existing checksum/lock runner. `status` now distinguishes pending versions; version-specific schema verification rejects missing or unexpected objects. Local schema attestation checks both migration versions. API gets read-only catalog grants. `stock` is a separate operator binary; it requires explicit `GO_DATABASE_URL`. No public stock mutation API, employee credential, provider secret or customer PII is introduced. Audit inputs must contain opaque staff/evidence references; see operator permissions and privacy rules in the runbook.

API change: public `GET /catalog` (`/api/catalog` through local proxy), no-store, sanitized 503 on failure. Existing order/payment contracts remain historical; live selling through Express is not compatible with this catalog. Online reservations/settlement and employee application delivery remain later roadmap slices. Operator recording of verified sales is available now; a durable offline queue, authentication UI and automated expiry alerts are not claimed.

Rollback requires a compatible app retaining version-2 readiness or forward repair; previous version-1 readiness cannot attest v2. Retain ledger/stock history. Backup/restore and post-backup event replay requirements are documented in the runbook. Do not drop stock data or rewrite checksums. Local fixtures establish safety properties, not production reconciliation, load capacity, RPO/RTO attainment or real payment verification.

Closure evidence still required: supplied opening counts, merchant count/discrepancy approval, real import/report matching the approved opening balance, and review/PR linkage. No PR, remote CI run or production rollout is claimed by this local evidence record.
