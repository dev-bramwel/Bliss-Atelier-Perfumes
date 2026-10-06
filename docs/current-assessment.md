# Current implementation assessment

Reviewed baseline: commit `0b775ec`, 6 October 2026. Findings are a code review, not a penetration test or a production deployment audit.

## Keep and extend

- Separate frontend/backend; small Express routes and service helpers are understandable.
- Server catalog determines prices and bounds quantities rather than trusting client totals.
- PostgreSQL schema and initial Prisma migration exist; orders retain line-item snapshots.
- Failed STK initiation preserves the order; signed callback order IDs support mapping recovery.
- Callback updates payment and transaction in a database transaction.
- Frontend cart, filters, detail modal, and payment polling provide a usable foundation.
- Helper tests exist for pricing, references, and callback signatures. They do not exercise route/database/provider behavior.

## Findings and priorities

| Priority | Evidence | Consequence / required change | Issue |
| --- | --- | --- | --- |
| P0 | `routes/orders.js` always creates an order for each POST | Network retry can duplicate orders/payments; require durable idempotency | BAP-005 |
| P0 | `routes/mpesa.js` overwrites status on every callback | Duplicate/out-of-order failure can downgrade PAID; enforce transitions and deduplication | BAP-006 |
| P0 | Signature covers order ID, not callback body | A valid callback URL does not independently prove amount or provider payload authenticity; verify mapping and reconcile through provider | BAP-006 |
| P0 | No STK query or reconciliation worker | Missing callbacks leave payments pending indefinitely | BAP-007 |
| P0 | No rate limiting; truthiness validation of customer fields | Abuse can create orders and payment prompts; strict schemas, limits, and safe errors | BAP-003 |
| P1 | Payment status endpoint uses order ID without authorization | Add scoped guest lookup token or authenticated ownership checks | BAP-008 |
| P1 | Single `orderId @unique` payment transaction | Cannot represent multiple payment attempts safely | BAP-004 |
| P1 | Catalog duplicated in frontend and order service; no inventory | Drift and overselling risks; database catalog and atomic stock reservations | BAP-004 |
| P1 | Axios calls have no explicit timeout; response code not validated | Hung requests or invalid provider acceptance can appear successful | BAP-007 |
| P1 | Frontend polling errors/failure lead to generic order confirmation; cart cleared before payment settles | Separate saved-order and paid states, resume/retry payment, prevent duplicate actions | BAP-009 |
| P1 | `server.js` only has a static status response; no shutdown/telemetry | Deployments cannot check readiness or diagnose behavior | BAP-013 |
| P1 | Customer order backup stored in browser localStorage | Review retention/minimization; prefer scoped order reference over customer details | BAP-008 |
| P2 | Payment HTML still says coming soon; modal-only product views | Misleading UX and missing crawlable product URLs | BAP-009 / BAP-016 |
| P2 | `npm start` runs migrations per process | Concurrent replicas should not each own migration execution | BAP-014 |

No evidence of admin, CI workflows, Docker, Kubernetes, Redis, brokers, structured analytics, backups, or release runbooks in the checked-in baseline. Database connectivity and live payment settlement have not been verified by this assessment.
