# Express/Prisma to Go migration plan

Status: planned rewrite, approved language direction on 8 October 2026. This document does not claim Go code exists. Issue BAP-022 owns the executable foundation and parity cutover.

## Compatibility target

| Existing surface | Go requirement |
| --- | --- |
| `GET /` | Preserve status JSON or document compatibility change |
| `POST /api/orders` | Preserve customer/items input and order/stkPush/warning envelope, success and saved-order warning statuses |
| `GET /api/orders/:id/payment-status` | Preserve paymentStatus/mpesaStatus/receiptNumber consumed by frontend; introduce scoped authorization with a coordinated frontend update |
| `POST /api/mpesa/callback` | Preserve provider response contract and signed callback routing; audit legacy token compatibility before removing it |
| JS server pricing | Port all 23 product IDs and money/quantity rules first, then move catalog authority to DB |
| PostgreSQL Prisma schema | Preserve order IDs, JSON items, timestamps, history and unique provider mapping while adding new schema |
| Frontend | Plain HTML/CSS/JS preserved; contract/security fixes are coordinated rather than replaced by a framework |

Contract tests capture existing successful behavior; known unsafe behavior is not a parity requirement. Reject malformed customer values safely; retain order after initiation failure; prevent paid-status downgrade; use HTTP deadlines and validate provider acceptance. Durable idempotency, inventory and reconciliation remain explicit dependent issues, not assumed benefits of changing language.

## Rewrite sequence

1. Record API fixtures and migration baseline using fake provider/disposable PostgreSQL. Inventory any existing data/schema version before migration work.
2. Add one Go module at `backend/`, `cmd/api`, `cmd/worker`, `cmd/migrate`, feature packages, DB/config/HTTP adapters and tests. During transition legacy source can remain at its current paths; don't move it until references/tooling are updated.
3. Implement pgx-backed repositories against legacy quoted tables, server-side pricing, orders and payment status. Add context cancellation, bounded pools and graceful shutdown.
4. Port provider transport, callback correlation and transaction processing with mocked acceptance/recovery scenarios. Trace/audit sensitive state without leaking secrets.
5. Baseline SQL runner recognizes verified Prisma history: do not rerun CREATE TABLE over existing data, silently mark unmatched schema as migrated, or have two active migration owners. Choose an explicit tested baseline/import procedure.
6. Run frontend/browser contracts against Go, upgrade fixture tests and restart/recovery scenarios. Add missing safety gates through linked issues.
7. Controlled single-writer cutover in development/staging then production only after launch prerequisites. Keep callback endpoint stable so in-flight accepted payments route to the active reconciler.
8. Remove Node backend source, Prisma runtime/config/package files only once Go parity/migrations pass. Preserve historical SQL migration evidence in the adopted migration baseline/archive. Update root instructions, CI and Docker to Go in the same cutover PR.

No dual live writes and no shadow STK initiation. Retain rollback-compatible schema and previous image through cutover. Existing frontend backup JSON isn't authoritative and must not be reimported as a new paid order.

## Acceptance and timing

Go build/vet/race tests, legacy contract compatibility, DB migration from empty and populated fixtures, fake-provider timeout/rejection/callback-before-mapping/duplicate cases, no paid regression, and documented startup/shutdown/cutover. Live settlement verification is a separate merchant gate.

Estimate the foundation from the first tested vertical slice. Oct 8–Dec 10 leaves about 126 hours at 14 hours/week before subtracting time already spent. The rewrite increases implementation effort; the previous 130-hour allocation must be reforecast rather than silently supplemented with extra hours. BAP-022 can be split into foundation, DB/orders, provider/callback, and cutover subissues with evidence per slice.
