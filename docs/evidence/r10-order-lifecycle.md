# R10 Stock Reservations and Order Lifecycle Evidence

Issue: [#16](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/16). Branch: `feat/16-order-lifecycle` targeting `develop`. Date: 7 November 2026.
Verification environment: Disposable local PostgreSQL 16 container, Docker Core Compose stack, Go unit & integration test suites, and R06 backup/restore/re-adoption drill. Legacy `"Order"` and `"MpesaTransaction"` table column schemas preserved with exact fingerprint reproducibility.

## Summary of Delivered Behavior

1. **Atomic Inventory Reservations (15-Minute Hold Window)**:
   - Order placement executes inside a single database transaction (`pgx.Tx`).
   - Catalog products are locked using deterministic alphabetical `ORDER BY id FOR UPDATE` ordering to serialize inventory deduction and eliminate deadlocks during concurrent checkouts.
   - Stock is verified against available units (`stock >= requested_qty`) and immediately decremented in `catalog_product`.
   - A linked `stock_reservation` record is created with `status = 'active'` and `expires_at = now() + 15 minutes`, along with `stock_reservation_item` records capturing exact reserved quantities.

2. **Snapshotted Order Items (Immutable Price History)**:
   - Order items are stored relationally in `order_item` (with backward-compatible JSON mirrored to legacy `"Order".items`).
   - Historical item records freeze `product_name`, `product_size`, `unit_price_kes`, `quantity`, and `subtotal_kes`.
   - Subsequent price alterations or catalog setting adjustments have zero effect on historical orders or items.

3. **Separated Payment and Fulfillment States**:
   - Payment states: `PENDING`, `PAID`, `FAILED`, `EXPIRED`, `RESOLUTION_REQUIRED`.
   - Fulfillment states: `unfulfilled`, `fulfilled`, `cancelled` (tracked in relational `order_fulfillment`).
   - Cash-on-delivery is strictly prohibited (`ErrPayOnDeliveryProhib`), complying with approved operating rule 1 (payment before dispatch).

4. **Idempotent Expiry Release & Cancellation (Zero Double-Restoration)**:
   - `ReleaseExpired` identifies active reservations where `expires_at <= now()`.
   - Transitions reservation `status` from `'active'` to `'released'` using an atomic conditional update (`WHERE status = 'active' RETURNING id`).
   - If 0 rows return, the reservation was already processed, ensuring stock is never restored twice even under repeated sequential or concurrent invocations.
   - `CancelOrder` manually cancels pending orders, safely releasing held stock and transitioning payment status to `FAILED` and fulfillment status to `cancelled`.

5. **Concurrency Protection Against Overselling**:
   - Concurrency tests simulate 10 parallel goroutines competing for the final 1 unit of stock.
   - Alphabetical row locking and inventory constraints guarantee that exactly 1 checkout succeeds, 9 fail with `ErrInsufficientStock`, and stock reaches 0 without going negative.

6. **Late-Payment Explicit Resolution Requirement**:
   - If payment confirmation arrives after a reservation has expired and released stock, `ConfirmPayment` detects that the reservation is no longer active.
   - The order is transitioned to `paymentStatus = 'RESOLUTION_REQUIRED'` and `fulfillmentStatus = 'cancelled'`.
   - Payment confirmation returns `ErrLatePaymentResolution` and records an M-Pesa transaction with status `'LATE_PAID'`.
   - Unreserved stock is never fulfilled silently.

7. **HTTP Service Routes**:
   - `POST /orders` and `POST /api/orders`: Validates customer info and items, reserves stock, creates order, and returns HTTP 201 Created with `{ "order": ..., "stkPush": null }`. Returns 400 for validation errors, 409 for insufficient stock / unopened items, and 503 if catalog stock control is unconfirmed.
   - `GET /orders/{id}/payment-status` and `GET /api/orders/{id}/payment-status`: Public polling endpoint for checkout overlay returning payment status, fulfillment status, receipt number, and hold expiration. Returns 404 if not found.
   - `GET /orders/{id}` and `GET /api/orders/{id}`: Returns order details with snapshotted items. Returns 404 if not found.
   - `GET /orders` and `GET /api/orders`: Returns recent orders for shop dashboard and fulfillment queue synchronization.

---

## Schema Changes

Implemented in migration [`0003_orders.sql`](../../internal/migration/sql/0003_orders.sql) and validated under contract schema [`schema-v3.json`](../../internal/migration/schema-v3.json):

```sql
CREATE TABLE public.order_item (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id text NOT NULL REFERENCES public."Order"(id) ON DELETE CASCADE,
    product_id text NOT NULL REFERENCES public.catalog_product(id),
    product_name text NOT NULL,
    product_size text NOT NULL,
    unit_price_kes integer NOT NULL CHECK (unit_price_kes > 0),
    quantity integer NOT NULL CHECK (quantity > 0),
    subtotal_kes integer NOT NULL CHECK (subtotal_kes > 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE public.stock_reservation (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id text NOT NULL UNIQUE REFERENCES public."Order"(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'released', 'committed')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    released_at timestamptz
);

CREATE TABLE public.stock_reservation_item (
    reservation_id uuid NOT NULL REFERENCES public.stock_reservation(id) ON DELETE CASCADE,
    product_id text NOT NULL REFERENCES public.catalog_product(id),
    quantity integer NOT NULL CHECK (quantity > 0),
    PRIMARY KEY (reservation_id, product_id)
);

CREATE TABLE public.order_fulfillment (
    order_id text PRIMARY KEY REFERENCES public."Order"(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'unfulfilled' CHECK (status IN ('unfulfilled', 'fulfilled', 'cancelled')),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX stock_reservation_status_expires_idx ON public.stock_reservation (status, expires_at);
CREATE INDEX order_item_order_id_idx ON public.order_item (order_id);
```

Legacy `"Order"`, `"MpesaTransaction"`, and `_prisma_migrations` schemas remain untouched to guarantee backward compatibility with prior fixtures and preserve database history fingerprints.

---

## Verification Evidence

1. **Domain & Concurrency Tests** ([`internal/orders/store_test.go`](../../internal/orders/store_test.go)):
   - `TestCreateOrderAndPriceSnapshot`: Proves atomic order creation, 15-minute hold reservation, stock reduction, and historical item price preservation across subsequent common price hikes.
   - `TestConcurrentLastItemCannotOversell`: 10 parallel goroutines compete for the last item; exactly 1 succeeds, 9 fail with `ErrInsufficientStock`, stock remains 0.
   - `TestIdempotentExpiryRelease`: Proves exact-once restoration of stock upon hold expiry; repeated or concurrent runs do not double-restore inventory.
   - `TestCancelOrder`: Proves safe release of stock upon order cancellation and prevention of double-cancellation.
   - `TestLatePaymentExplicitResolution`: Proves that late payment after hold release transitions the order to `RESOLUTION_REQUIRED` and rejects silent fulfillment.
   - `TestPromptPaymentConfirmation`: Proves timely payment confirmation marks order `PAID`, commits reservation, and records `MpesaTransaction` idempotently.
   - `TestListRecent`: Proves query of recent orders for dashboard queue sync.

2. **HTTP API Route Tests** ([`internal/httpapi/server_test.go`](../../internal/httpapi/server_test.go)):
   - `TestOrderRoutes`: Exercises `POST /orders`, `POST /api/orders`, `GET /orders/{id}/payment-status`, `GET /api/orders/{id}/payment-status`, `GET /orders/{id}`, `GET /api/orders/{id}`, `GET /orders`, and `GET /api/orders`.
   - `TestHealth`: Proves foundation route contracts and readiness gates are preserved.

3. **Database Integration & R06 Drill** (`scripts/test-integration.ps1`):
   - All tests in `internal/database`, `internal/migration`, `internal/catalog`, and `internal/orders` passed sequentially against disposable PostgreSQL.
   - Seeded recovery fixture, dump, schema drop, restore, and re-adoption passed with identical sha256 JSON fingerprint across `"Order"`, `"MpesaTransaction"`, and `_prisma_migrations`.

4. **Docker Core Validation** (`scripts/Test-DockerCore.ps1`):
   - Verified non-root containers, internal network isolation, static assets, same-origin health, and runtime privilege grants for `bliss_runtime`.

---

## Rollback Plan

If rollback of migration 3 is required:
1. Drop additive tables:
   ```sql
   DROP TABLE public.order_fulfillment, public.stock_reservation_item, public.stock_reservation, public.order_item CASCADE;
   ```
2. Remove migration version 3 entry from ledger:
   ```sql
   DELETE FROM public.bliss_schema_migrations WHERE version = 3;
   ```
3. Revert code to pre-R10 commit. Legacy tables `"Order"` and `"MpesaTransaction"` continue functioning without data loss.
