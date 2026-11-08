# Issue #16 implementation plan: Stock reservations and order lifecycle

Branch: `feat/16-order-lifecycle` / `develop`. Source: https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/16

## 1. Requirements and Scope

Issue #16 ([R10]) establishes snapshotted order items, separated payment and fulfillment states, atomic inventory reservations with a 15-minute hold, expiry/cancellation safety, late-payment resolution, and ports `/api/orders` and `/api/orders/{id}/payment-status` into the Go HTTP service.

### Acceptance Criteria
1. **Concurrent Last-Item Purchases Cannot Oversell**:
   - Multiple shoppers attempting to buy the final unit concurrently are serialized via row-level locking (`SELECT ... FOR UPDATE` in sorted product order); exactly available quantity is reserved, subsequent requests fail with an actionable stock error (`409 Conflict` / `422 Unprocessable`).
2. **Retries/Expiry Cannot Double-Release**:
   - Stock release on expiration, payment failure, or cancellation is idempotent. Atomic status transition (`WHERE status = 'ACTIVE' RETURNING ...`) guarantees stock delta is credited back to `catalog_product.stock` exactly once.
3. **Historical Snapshots Survive Price Changes**:
   - Order items snapshot product identity, name, size, unit price, and subtotal at creation. Subsequent updates to `catalog_settings.common_price_kes` or `catalog_product.price_override_kes` do not alter existing orders.
4. **Late Paid Orders Require Explicit Resolution Rather Than Silent Fulfillment**:
   - If payment arrives after the 15-minute reservation expires and stock was released/re-allocated, the order transitions to `RESOLUTION_REQUIRED` / `LATE_PAID` rather than fulfilling silently without available inventory.

---

## 2. Architecture and Data Model

### Migration `0003_orders.sql`
- **Orders Table Enhancement (`Order`)**:
  - Add `fulfillmentStatus text NOT NULL DEFAULT 'UNFULFILLED' CHECK (fulfillmentStatus IN ('UNFULFILLED', 'FULFILLED', 'CANCELLED'))`.
  - Expand `paymentStatus` allowed states: `PENDING`, `PAID`, `FAILED`, `EXPIRED`, `RESOLUTION_REQUIRED`.
  - Add `expiresAt timestamptz`.
- **Order Items Table (`order_item`)**:
  - `id uuid PRIMARY KEY DEFAULT gen_random_uuid()`
  - `order_id text NOT NULL REFERENCES "Order"(id) ON DELETE CASCADE`
  - `product_id text NOT NULL REFERENCES catalog_product(id)`
  - `product_name text NOT NULL`
  - `product_size text NOT NULL`
  - `unit_price_kes integer NOT NULL CHECK (unit_price_kes > 0)`
  - `quantity integer NOT NULL CHECK (quantity > 0)`
  - `subtotal_kes integer NOT NULL CHECK (subtotal_kes > 0)`
- **Stock Reservation Table (`stock_reservation`)**:
  - `id uuid PRIMARY KEY DEFAULT gen_random_uuid()`
  - `order_id text NOT NULL REFERENCES "Order"(id) ON DELETE CASCADE UNIQUE`
  - `status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RELEASED', 'COMMITTED'))`
  - `expires_at timestamptz NOT NULL`
  - `created_at timestamptz NOT NULL DEFAULT now()`
  - `released_at timestamptz`
- **Stock Reservation Items Table (`stock_reservation_item`)**:
  - `reservation_id uuid NOT NULL REFERENCES stock_reservation(id) ON DELETE CASCADE`
  - `product_id text NOT NULL REFERENCES catalog_product(id)`
  - `quantity integer NOT NULL CHECK (quantity > 0)`
  - `PRIMARY KEY (reservation_id, product_id)`
- **Index & Constraints**:
  - `CREATE INDEX idx_stock_reservation_status_expires ON stock_reservation(status, expires_at);`
  - `CREATE INDEX idx_order_created_at ON "Order"("createdAt");`

---

## 3. Go Order Service (`internal/orders/`)

- **`Store` / `Service`**:
  - `CreateOrder(ctx, req)`:
    - Validates customer fields, delivery options (`delivery`, `pickup`), and item quantities (1–50 items, qty >= 1).
    - Checks catalog availability & active 24h approval (`catalog.Control`).
    - Begins transaction:
      - Acquires row locks on `catalog_product` in deterministic order (`ORDER BY id`).
      - Validates effective prices against catalog (`price_override_kes` or `common_price_kes`).
      - Verifies available stock >= requested quantity.
      - Decrements `catalog_product.stock = stock - qty`.
      - Inserts `Order` with `paymentStatus = 'PENDING'`, `fulfillmentStatus = 'UNFULFILLED'`, `expiresAt = now + 15m`.
      - Inserts snapshotted `order_item` records.
      - Inserts `stock_reservation` with status `ACTIVE`.
  - `ReleaseExpired(ctx, now)`:
    - Selects `ACTIVE` reservations where `expires_at <= now`.
    - Atomically updates status to `RELEASED`. If successful, increments `catalog_product.stock = stock + qty` and marks order `paymentStatus = 'EXPIRED'`, `fulfillmentStatus = 'CANCELLED'`.
    - Idempotent: concurrent or repeated runs do not double-increment stock.
  - `CancelOrder(ctx, orderID)`:
    - Atomically cancels unpaid order and releases reservation.
  - `HandlePaymentResult(ctx, orderID, paymentSuccess, receipt)`:
    - If payment succeeds before expiry: mark order `PAID`, commit reservation `COMMITTED`.
    - If payment succeeds after expiry (reservation already `RELEASED`): mark order `RESOLUTION_REQUIRED`. Does NOT silently fulfill without stock.
  - `GetOrder(ctx, orderID)` / `GetPaymentStatus(ctx, orderID)`.

---

## 4. HTTP API Endpoints (`internal/httpapi/`)

- `POST /orders`:
  - Request body validated against customer and items schema.
  - Calls `OrderService.CreateOrder`.
  - Returns HTTP 201 with `{ "order": ..., "stkPush": null }`.
  - Returns HTTP 409 / 422 if stock insufficient.
- `GET /orders/{id}/payment-status`:
  - Returns `{ "orderId": ..., "paymentStatus": ..., "fulfillmentStatus": ..., "receiptNumber": ..., "isExpired": ... }`.
- `GET /orders/{id}`:
  - Returns order and snapshotted items.

---

## 5. Verification Plan

1. **Unit & Package Tests**:
   - Input validation, item pricing calculation, JSON serialization.
2. **Concurrency Integration Tests**:
   - Multiple goroutines racing to purchase the last 1 or 2 bottles in PostgreSQL.
   - Assert zero negative stock and exactly N successful orders, remainder rejected with 409/422.
3. **Idempotent Expiry Tests**:
   - Repeatedly invoke `ReleaseExpired` on an expired order; verify stock balance is incremented exactly once.
4. **Historical Price Stability Tests**:
   - Create order at KES 2,500. Update catalog price to KES 3,000. Verify retrieved order total and item prices remain KES 2,500.
5. **Late Payment Resolution Tests**:
   - Expire order reservation, simulate late payment, verify order transitions to `RESOLUTION_REQUIRED` and fulfillment remains pending/manual review.
