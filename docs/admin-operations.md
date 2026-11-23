# R19 administrative operations

Issue #25 adds a session-backed workspace at `/admin`, `/dashboard` and
`/admin/orders`, `/admin/inventory`, `/admin/payments`, `/admin/users`,
`/admin/audit-logs`. These routes render the same Templ shell. Private records
are fetched after staff authentication; they are never embedded in the HTML.

## Sign-in and permissions

Use the configured Google sign-in flow with an active staff account. The client
gets the profile and a fresh CSRF token from `/api/auth/me`. The token stays in
memory and accompanies every mutation as `X-CSRF-Token`. Administrative APIs
require the `bliss_session` cookie; the historical shared manager secret is no
longer accepted for cache invalidation, quote issuance or `/admin/*` APIs.

Owners can inspect all orders, quote delivery, inspect and resolve payment
exceptions, record verified manual settlements, view payment audit records,
and assign orders to active employees. Employees can read inventory and
inspect/fulfill their assigned orders through `/operations/*` and `/orders/:id`
routes; administrative `/admin/*` APIs reject employees and customers, with the
scoped exception of customer status reactivation (`POST /admin/users/:id/status`),
which enforces downward role isolation. Guest order tokens retain their existing
scoped customer behavior.

Owners see all users and manage user roles; the UI offers employee and customer
as target roles. Audit queries allow owners to inspect operational records across
all actions. Subordinate roles (employees and customers) have zero role management
capability and cannot inspect administrative audit logs. Server authorization
remains authoritative when a control is hidden. Role changes/deactivation revoke
sessions. A subsequent 401 closes open drawers, clears the in-memory auth context
and displays the sign-in prompt.

## Account lifecycle and customer self-deactivation

Authenticated users can self-deactivate their accounts via `POST /api/auth/deactivate`
or through the double-confirmation modal on the storefront header.
Self-deactivation:

- Sets `is_active = false` on `app_user`.
- Atomically revokes all active database sessions across all devices.
- Evicts all cached sessions from Redis.
- Records an immutable audit log entry (`user.self_deactivate`) in `admin_audit_log`.
- Blocks subsequent Google OAuth sign-in attempts with HTTP 403 Forbidden and an
  atelier-styled "Account Deactivated" notice page.
- Prevents sole store owners from deactivating themselves (`ErrCannotDeactivateSoleOwner`,
  returning HTTP 409 Conflict).

Reactivation and downward status isolation:

- Store owners can activate or deactivate any account via `POST /admin/users/:id/status`.
- Employees can reactivate or deactivate customer accounts via `POST /admin/users/:id/status`.
  Downward isolation strictly prevents employees from modifying the active status of
  fellow employees or owners (returning HTTP 403 Forbidden).
- Once reactivated by staff, the user can immediately log in again via Google OAuth.

## Employee inventory editing permissions

Store Owners can delegate inventory management and stock editing privileges to trusted employees
on a per-employee basis:

- **Management Endpoint**: `POST /admin/users/:id/permissions` (or `/api/admin/users/:id/permissions`).
  - Restricted strictly to `RoleOwner`. Subordinate roles receive HTTP 403 Forbidden.
  - Payload: `{ "can_manage_inventory": boolean }`.
  - Can only be granted to accounts with `role == 'employee'`. Non-employee targets receive HTTP 400 Bad Request.
  - Updates persist durably in `public.app_user.can_manage_inventory`.
  - Atomic audit log entry with action `update_user_permissions` recorded in `admin_audit_log`.
  - Active employee sessions are evicted from Redis cache (not revoked in PostgreSQL), allowing updated
    permissions to take effect immediately on their next request without forcing an abrupt logout.
- **Authorization Enforcement**:
  - `POST /catalog/invalidate`, `GET /operations/inventory`, `POST /operations/inventory/restock`, and `POST /operations/inventory/adjust` require `auth.CanManageInventory(*user)`.
  - Employees with `can_manage_inventory = true` pass authorization and can view inventory, restock/adjust ledger quantities, and invalidate caches.
  - Employees without this permission receive HTTP 403 Forbidden on inventory mutation and query endpoints.
  - Offline batch operations via `cmd/stock apply` verify that the batch actor exists in `public.app_user`, is active, and is either an owner or an employee with `can_manage_inventory = true`. Approving replacement batches (`approve-replacement`) strictly requires an active owner actor. Both checks fail closed with an explicit authorization error if the operator database connection lacks SELECT on `public.app_user`.
- **Stock Restock, Count Adjustment & Dashboard UI**:
  - Store Owners see a "Can manage inventory" toggle and save button next to each employee in `/admin/users`.
  - When an employee without permission signs in, the "Catalog & inventory" navigation link, inventory panels, and restock buttons are hidden (`data-inventory hidden`), and direct URL access redirects to orders with an authority notice.
  - Permitted employees and store owners can click the "Add inventory" button on the Catalog & inventory panel to open the stock dialog (`#addInventoryDialog`).
  - Submitting the form calls `POST /api/operations/inventory/restock` (or `/api/operations/inventory/adjust`) with `{ batch_id, product_id, quantity, reference }`.
  - **Stock Additions and Adjustments**: Both positive receipts (`quantity > 0`, recorded as kind `receipt`) and count corrections / damage deductions (`quantity < 0`, recorded as kind `adjustment`) are supported. Count reductions verify that the resulting stock does not violate active checkout reservation holds (`ErrInsufficientUnreserved`).
  - **Stock Authority & Opening Balance**: Modifying stock requires `stock_control.authority == 'replacement'` and that the catalog product has already completed its opening physical count (`opened == true`). The runtime role modifies `catalog_product.stock` while keeping `opened` unchanged.
  - **Idempotent Retry & Mandatory Batch ID**: Submissions strictly require a caller-supplied stable `batch_id` (`ErrBatchIDRequired`). The dashboard assigns a stable client ID on dialog open. If a network retry re-sends the same `batch_id` with an identical digest, the endpoint succeeds idempotently without double-incrementing stock. Requests lacking a `batch_id` are rejected with HTTP 400 Bad Request to guarantee retry safety across all clients.
  - **Reconciliation & Invalidation**: Applying changes clears `stock_control.reconciled_until = NULL`, records receipt/adjustment ledger entries in `stock_batch` and `stock_entry`, logs an audit entry (`restock_inventory` or `adjust_inventory`), and asynchronously invalidates the storefront catalog cache.
  - **Error Sanitization**: Validation and business errors return standard 4xx responses (400 Bad Request, 404 Product Not Found, 409 Batch Conflict). Unexpected database or storage failures are logged server-side and returned as sanitized 500 Internal Server Error without leaking SQL or schema details.

## Screens and contracts

All URLs below have the `/api` prefix when accessed through the storefront.

| Screen         | Reads                                                                                        | Mutations                                                                                |
| -------------- | -------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Orders         | `GET /operations/orders`, `GET /orders/:id`                                                  | `POST /orders/:id/assign`, `POST /orders/:id/fulfill`, `POST /orders/:id/delivery-quote` |
| Inventory      | `GET /operations/inventory`                                                                  | `POST /catalog/invalidate`, `POST /operations/inventory/restock`, `POST /operations/inventory/adjust` |
| Payments       | `GET /admin/payments/exceptions`, `.../exceptions/:id`, `.../unresolved`, `.../audit-ledger` | `POST .../exceptions/:id/resolve`, `.../manual-settlement`, `.../reconcile`              |
| Users          | `GET /admin/users`                                                                           | `POST /admin/users/:id/role`, `.../:id/status`, `.../:id/permissions`                    |
| Security audit | `GET /admin/audit-logs`                                                                      | None                                                                                     |

Lists use `limit` and `offset`; the UI requests 25 records, the API defaults to
50 and caps requests at 100. Queries use stable ordering. The inventory response
includes `products`, `total`, `common_price_kes`, `approval_until`; each product
has `stock`, `held`, `available`, `opened`, `priceKes`. Unreserved units do not
override reconciliation approval or opening-count requirements for public sales.
Order lists include `orders`, `total`, `pending_dispatch`, assignment and latest
delivery quote. Employee counts and lists use the employee scope in SQL.
Stock/orders refresh every 15 seconds while the tab is visible and no mutation is
in flight. A manual refresh is available; this is polling rather than a push feed.

Assignment accepts `{ "user_id": "usr_..." }`. Fulfillment marks an already-paid,
unfulfilled order as dispatched/collected. It cannot take payment, adjust stock
again or fulfill an unpaid order. Assignment, fulfillment and their security audit
records commit atomically. Employee assignment is rechecked inside the fulfillment
transaction. Assignment and fulfillment serialize on the order row.

Quote issuance accepts `{ "fee_kes": 350, "notes": "Delivery to Nyali" }`,
derives the actor from the session, uses merchant collection, and expires after
15 minutes. The established detailed R11 payload remains supported. Database
quote status `pending` is displayed as **offered**, with a countdown. Pickup
orders do not expose quote controls. Acceptance remains a customer operation.

Exception resolution accepts `action`, `resolution_notes`, `evidence`; the old
`notes` field remains compatible. Allowed actions are `applied_to_order`,
`refund_recorded`, `dismissed_duplicate`, `voided`. A resolution records evidence
and an atomic administrative audit entry; it does not itself create a refund or
settle an order. Verify the actual financial operation before recording it.
Manual settlement accepts the issue's `order_id`, `amount_kes`, `receipt_number`,
`evidence`, `notes` fields, as well as the established camelCase payload. The
server binds `authorizedBy` to the session and retains the R17 stock, amount,
receipt and accepted-quote checks. The immutable payment ledger shows settlements
and corrections. No operator identity is taken from browser input.

The staged-sale metric counts existing device-local `shopSales` records. These
are drafts for the trusted stock CLI, not confirmed server sales or a new public
stock mutation interface. No write is sent to the previously missing
`/api/stock/apply` endpoint.

## Offline protection

`shared.js` and the layout own `#offlineOverlay`, its local looping empty-atomizer
WebM, and an animated SVG fallback. Reduced-motion users receive the static SVG.
The overlay is a modal dialog above open drawers. A browser disconnect, failed
fetch or gateway failure locks mutation buttons. Draft inputs stay in their
original DOM; sensitive payment/quote drafts are not persisted to browser storage.

A bounded heartbeat checks `/api/health/live` every three seconds after a failure,
and immediately on the browser's online event. A valid `{"status":"alive"}`
response restores controls and displays `Atelier Online ✓` before dismissing the
overlay. Recovery refreshes server reads without replacing draft forms. Mutations
are never replayed automatically. If a write's response is lost, inspect the
server record before explicitly retrying. Reloading or closing the tab discards
drafts. Logout removes local order/sale records and session storage and returns
to the collection.

## Upgrade and verification

Run the existing migration/deployment procedure before starting the updated API.
Migration `0010_order_assignment.sql` adds staff assignments and their foreign
keys/index; `schema-v10.json` and runtime readiness include it. Existing schema
snapshots and applied migrations remain unchanged. Runtime grants include the
assignment table; catalog-only credentials cannot read it. Roll back only to a
build compatible with schema v10, retaining assignment and audit history.

Verification commands:

```powershell
go test ./...
go vet ./...
node --test web/catalog.test.cjs
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-integration.ps1
```

The integration script includes `internal/operations` and requires Docker. Its
disposable database test checks inventory holds, employee-scoped lists, owner-only
assignment, unpaid/duplicate fulfillment rejection, paid stock and atomic audits.
The HTTP tests cover all four roles, cookie/CSRF requirements, revoked sessions,
employee order isolation, issue payloads and actor binding.

For browser verification, install the existing `tools/ci` dependencies and
Playwright Chromium, run the web server on `127.0.0.1:8086`, then:

```powershell
node web/admin.browser.cjs
```

`BLISS_WEB_URL` overrides the address; `PLAYWRIGHT_MODULE` can point to another
installed Playwright module. API fixtures test owner, employee and customer
journeys, pagination, offline and failed-write draft preservation,
CSRF headers, role filtering, session expiry and mobile layouts. They do not
replace the PostgreSQL or live provider integration suite. Screenshots are written
under `.local/r19-browser`.
