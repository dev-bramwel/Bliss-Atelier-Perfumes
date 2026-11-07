# R09 catalog and stock evidence

Issue: [#15](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/15). Branch: `feat/15-dtabase-catalogue` / `develop`. Date: 28 October 2026 (updated 7 November 2026).
Verification environment: Disposable local Windows/Docker environments, synthetic test fixtures. No production database or payment provider was touched. See [plan](../issue-15-plan.md), [operator workflow](../stock-workflow.md), [R04 reconciliation](../inventory-reconciliation.md), and [ADR 004](../adr/004-inventory-authority.md).

## Development vs. launch cutover lifecycle boundary

On 28 October 2026, the owner confirmed the operational boundary between the development phase and production launch:

1. **Development Phase (Delivered & Verified)**:
   - Operates strictly against mock/disposable development databases using synthetic test fixtures.
   - Database catalog schema ([`0002_catalog.sql`](../../internal/migration/sql/0002_catalog.sql)), operator stock CLI ([`cmd/stock`](../../cmd/stock/main.go)), and domain logic ([`internal/catalog`](../../internal/catalog/catalog.go)) are implemented and verified.
   - All 23 products mapped with stable IDs, slugs, and KES 2,500 prices with individual overrides.
   - Batch validation, duplicate mapping rejection, idempotency, ledger balance reconciliation, and fail-closed catalog availability (24-hour approval expiry) are proven with automated unit and integration tests.
2. **Production Launch Phase (Operational Prerequisite)**:
   - A clean, brand-new production PostgreSQL database will be provisioned at launch.
   - The live physical shop bottle count, the MyAccurateBooks discrepancy reconciliation sign-off, and the production opening batch import (`cmd/stock apply` & `approve-replacement`) will take place during the launch cutover window against that fresh production database.

## Delivered behavior and acceptance

- **Catalog Database & Prices**: Migration `0002_catalog.sql` preserves all 23 IDs (`p1` through `p23`), category/product slugs, and verified local image mappings. Effective prices are read from the database, defaulting to common KES 2,500 with optional individual overrides. `GET /catalog` (proxied as `GET /api/catalog`) serves DB prices; the frontend no longer uses a hardcoded price list.
- **Stock Ledger & Batch Integrity**: Stock mutations balance against an immutable, audited batch (`stock_batch` storing actor, evidence, digest, and kind) with associated itemized ledger entries (`stock_entry`). No public unauthenticated stock write API is exposed.
- **Idempotency & Duplicate Rejection**: Exact retries return `replayed: true` without altering stock balances; conflicting retries with modified content fail with `batch ID already used with different content`. Subsequent opening batches after stock has opened fail with `opening balance already recorded`.
- **Fail-Closed Safe Availability Policy**: Catalog availability requires confirmed authority, opened products, positive stock, and an owner reconciliation approval valid within 24 hours. Initial, unopened, zero, expired, or failed database states immediately mark products unavailable (`available: false` / HTTP 503).
- **Transaction Rollback Safety**: All database transactions enforce rollback guarantees using bounded cleanup helpers (`defer rollback(tx)` or `defer tx.Rollback(ctx)`).

## Verification results

- `go test ./...`: All unit and package tests pass cleanly across `internal/catalog`, `internal/migration`, `internal/httpapi`, and `cmd/stock`.
- `backend/scripts/test-integration.ps1`: Integration drill passes against pinned PostgreSQL 17.11 container. Tests exercise catalog v1-to-v2 upgrade, idempotency, product/media checks, stock workflow, and operator role privileges.
- `scripts/Test-DockerCore.ps1`: Passed in disposable Compose project with non-root services, credential isolation, migration startup recovery, and proxied 23-product catalog.
- Synthetic batch validation drills pass:
  - Malformed/missing/null/fractional quantities rejected.
  - Duplicate product mappings rejected (`invalid or duplicate product mapping`).
  - Missing products in opening batch rejected (`counted import must map every catalog product exactly once`).
  - Stale expected balance conflicts fail safely (`balance changed; recount before retry`).
  - Concurrent last-bottle sales serialize without overselling.
  - Sum of ledger entries strictly matches `catalog_product.stock` (`ledgerMatches: true`).

## Pull Request and Code References

- **PR #48**: `feat(inventory): add database catalog and authoritative stock workflow`
- **PR #49**: `Feat/15 dtabase catalogue`
- **PR #51**: `Release: Go foundation, audited inventory, Docker core, and Templ SSR storefront`
- **PR #60**: `fix(pr-agent): allow equivalent rollback cleanup patterns and model stock audit at batch level`
- **PR #61**: `Release: PR-Agent invariant refinement for rollback helpers and batch audit schema`

## Closure Decision

All development acceptance criteria and delivery evidence for [R09 Issue #15](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/15) are satisfied. Real opening counts, discrepancy sign-off, and the production opening import are formally recognized as launch cutover activities against the newly provisioned production database.
