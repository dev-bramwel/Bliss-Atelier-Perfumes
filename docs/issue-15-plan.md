# Issue #15 implementation plan

Branch: `feat/15-dtabase-catalogue`. Source: https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/15

1. Preserve all 23 product IDs and media mappings in a transactional versioned SQL migration. Use the approved common KES 2,500 price with optional individual overrides.
2. Provide a DB-backed public catalog with fail-closed availability, no stale price fallback, and storefront loading/error states.
3. Provide an operator-only stock CLI: validated atomic batches, opening counts, ongoing shop-sale deduction, audited corrections, exact retry idempotency, concurrency protection, and reconciliation expiry. No unauthenticated stock write API.
4. Verify empty and legacy upgrades, repeat import, changed retry, invalid mappings, concurrent sale/oversell, stale/unavailable stock, and ledger totals. Record local evidence and migration/rollback instructions.

## Closure gates

R06 (#12) is closed. R04 (#10) still needs real opening counts and merchant discrepancy sign-off. Database replacement and the availability policy are confirmed below. Schema starts with unconfirmed authority and unavailable stock until the operator explicitly records approval and opening counts. Authenticated employee sales and checkout reservations remain later roadmap slices; the operator workflow states this limit.

## Confirmed decisions

Owner selected database replacement and approved a reconciliation approval lasting at most 24 hours in this conversation on 22 October 2026. Opening counts will be supplied. External integration is not selected. The remaining R04/R09 gate is the real count import and signed-off reconciliation, not authority selection. See [stock workflow](stock-workflow.md).
