# Issue #15 implementation plan

Branch: `feat/15-dtabase-catalogue` / `develop`. Source: https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/15

1. Preserve all 23 product IDs and media mappings in a transactional versioned SQL migration. Use the approved common KES 2,500 price with optional individual overrides.
2. Provide a DB-backed public catalog with fail-closed availability, no stale price fallback, and storefront loading/error states.
3. Provide an operator-only stock CLI: validated atomic batches, opening counts, ongoing shop-sale deduction, audited corrections, exact retry idempotency, concurrency protection, and reconciliation expiry. No unauthenticated stock write API.
4. Verify empty and legacy upgrades, repeat import, changed retry, invalid mappings, concurrent sale/oversell, stale/unavailable stock, and ledger totals. Record local evidence and migration/rollback instructions.

## Closure gates and lifecycle boundary

- **Development Phase (Closed)**:
  - R06 (#12) and R04 (#10) are closed.
  - Replacement database authority, schema migration `0002_catalog.sql`, operator tool `cmd/stock`, and fail-closed catalog availability (24-hour owner approval expiry) are implemented and verified via automated test suites.
  - Delivered in PR #48, PR #49, PR #51, PR #60, and PR #61.
- **Launch Cutover Phase (Operational Prerequisite)**:
  - Live shop physical bottle counting, MyAccurateBooks discrepancy sign-off, and production opening batch import are cutover procedures executed against the newly provisioned production database at launch cutover. See [stock workflow](stock-workflow.md), [R04 reconciliation](inventory-reconciliation.md), and [R09 evidence](evidence/r09-catalog-stock.md).
