# R19 — Administrative screens and operational access

Issue: #25. Verification date: 16 November 2026.

## Implemented

- Templ workspace and route aliases for orders, inventory, payments, users and
  audit logs, with profile/role header and CSRF-protected logout.
- Bounded database pagination, inventory/checkout-hold reads and a scoped order
  queue. Migration v10 persists owner-controlled employee assignments.
- Full authorized staff order inspection and paid-only, single-transition
  fulfillment with atomic administrative auditing.
- Delivery quote form, offered/accepted/rejected/expired display and expiry
  countdown, with the issue's `fee_kes`/`notes` request supported.
- Exception inspection/resolution, verified manual settlement, reconciliation and
  payment audit views. Actor identity comes from the session. Resolution and its
  administrative audit commit together.
- Manager filtering before database pagination for users/audit logs; owner
  elevation is absent from UI options. Employees have no admin navigation or
  access to `/admin/*` APIs and only inspect/fulfill assigned orders.
- Shared offline modal with local empty-atomizer WebM/SVG, three-second bounded
  heartbeat, disabled mutation controls, DOM draft retention, and no automatic
  mutation replay. Role-selection drafts also survive reconnection.

## Verification

- `go test -p 1 ./...`: passed. Database/broker tests retain their normal
  environment-based skips when their disposable services are not configured.
- `go vet -p 1 ./...`: passed.
- `go build -p 1 -o .local/r19/web-final.exe ./cmd/web`: passed.
- `node --test web/catalog.test.cjs`: all three tests passed.
- `node web/admin.browser.cjs`: owner, manager, employee and customer browser
  journeys passed against the compiled web process with fixture API responses.
  Checks include CSRF headers, role visibility, pagination, quote/exception/manual
  settlement forms, actual browser offline events, aborted writes, retained
  drafts, session expiry, script errors and 390px layouts.
- All ten SQL migrations executed in a disposable embedded PostgreSQL engine
  (PGlite/PostgreSQL 18). The v10 schema fingerprint matched after excluding the
  separately exposed PostgreSQL 18 NOT NULL constraints. This is supplemental
  SQL/schema verification, not the pinned PostgreSQL 17 integration run.
- `git diff --check`: passed.

The Docker daemon was unavailable on this machine. The Docker Core test,
disposable PostgreSQL 17/Redis/RabbitMQ integration suite and migration/restore
drill were **not run** for this change. `scripts/test-integration.ps1` now includes
the new `internal/operations` database test. Run that suite before deployment;
the embedded schema check and browser fixtures do not establish live bank
settlement, production Google authentication, or deployment readiness.

See [administrative operations](../admin-operations.md) for permissions, payloads,
upgrade instructions and operator behavior.
