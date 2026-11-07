# Issue #17 Implementation Plan: Quoted Delivery and Customer Acceptance

**Issue:** [#17 [R11]](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/17)  
**Milestone:** Week 2: Trustworthy commerce data  
**Branch:** `feat/17-quoted-delivery` targeting `develop`  
**Dependencies:** [#7 [R01]](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/7) (Closed), [#16 [R10]](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/16) (Closed)  

---

## 1. Outcome & Scope

Support Mombasa delivery and customer-arranged showroom pickup with versioned, expiring manager delivery quotes. Record recipient (`merchant` vs `courier`), amount in KES, and customer agreement; collect transport only when approved policy dictates merchant receives it.

### Core Acceptance Criteria
1. **No STK for an unaccepted total**:
   - For `delivery` orders, M-Pesa payment initiation or confirmation cannot proceed without explicit customer acceptance of the active delivery quote.
   - For `pickup` orders, transport charge is KES 0 and auto-accepted.
2. **Changed/expired quotes need new acceptance**:
   - If a manager revises a quote (amount or delivery arrangement), a new version is created (monotonically increasing integer), resetting acceptance status to `pending`.
   - Expired quotes (`expires_at <= now()`) cannot be accepted or paid; manager must issue an updated quote.
3. **Direct-courier fee is excluded from merchant collection**:
   - When `fee_recipient == 'courier'`, the customer pays the rider/courier directly upon arrival. The transport fee is recorded for courier handover instructions, but excluded from merchant collection (`payable_transport_kes = 0`). The M-Pesa prompt amount reflects items subtotal only.
   - When `fee_recipient == 'merchant'`, the transport fee is included in merchant collection, and total payable equals `items_subtotal_kes + amount_kes`.
4. **Payable item/transport snapshots reconcile**:
   - Order records and quotes explicitly snapshot `items_subtotal_kes`, `delivery_fee_kes`, `fee_recipient`, and `payable_total_kes`.
   - Reconciles `payable_total_kes == items_subtotal_kes + (delivery_fee_kes if fee_recipient == 'merchant' else 0)`.
   - Historical totals survive catalog price changes and subsequent delivery fee changes.

---

## 2. Architecture & Data Model

### Database Migration: `0004_delivery_quotes.sql`
Additive schema changes preserving legacy `"Order"` and `"MpesaTransaction"` table fingerprints:

```sql
CREATE TABLE public.delivery_quote (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id text NOT NULL REFERENCES public."Order"(id) ON DELETE CASCADE,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    amount_kes integer NOT NULL CHECK (amount_kes >= 0),
    fee_recipient text NOT NULL CHECK (fee_recipient IN ('merchant', 'courier')),
    arrangement text NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected', 'expired')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    accepted_at timestamptz,
    rejected_at timestamptz,
    created_by text NOT NULL,
    customer_notes text,
    UNIQUE (order_id, version)
);

CREATE INDEX delivery_quote_order_id_status_idx ON public.delivery_quote (order_id, status);
CREATE INDEX delivery_quote_status_expires_idx ON public.delivery_quote (status, expires_at);
```

### Role Grants & Readiness Attestation
- Add `GRANT SELECT, INSERT, UPDATE, DELETE ON public.delivery_quote TO bliss_runtime;` in `cmd/devstack/main.go`.
- Document privileges in `docs/sql-migrations.md`.
- Update `internal/migration/readiness.go` to verify `bliss_runtime` DML permissions on `delivery_quote`.
- Generate contract `internal/migration/schema-v4.json`.

---

## 3. Domain Service: `internal/delivery`

### Models & Interfaces
```go
package delivery

type QuoteStatus string

const (
    StatusPending  QuoteStatus = "pending"
    StatusAccepted QuoteStatus = "accepted"
    StatusRejected QuoteStatus = "rejected"
    StatusExpired  QuoteStatus = "expired"
)

type FeeRecipient string

const (
    RecipientMerchant FeeRecipient = "merchant"
    RecipientCourier  FeeRecipient = "courier"
)

type DeliveryQuote struct {
    ID            string       `json:"id"`
    OrderID       string       `json:"orderId"`
    Version       int          `json:"version"`
    AmountKes     int          `json:"amountKes"`
    FeeRecipient  FeeRecipient `json:"feeRecipient"`
    Arrangement   string       `json:"arrangement"`
    Status        QuoteStatus  `json:"status"`
    ExpiresAt     time.Time    `json:"expiresAt"`
    CreatedAt     time.Time    `json:"createdAt"`
    AcceptedAt    *time.Time   `json:"acceptedAt,omitempty"`
    RejectedAt    *time.Time   `json:"rejectedAt,omitempty"`
    CreatedBy     string       `json:"createdBy"`
    CustomerNotes string       `json:"customerNotes,omitempty"`
}

type Store interface {
    CreateQuote(ctx context.Context, orderID string, req CreateQuoteRequest) (*DeliveryQuote, error)
    GetActiveQuote(ctx context.Context, orderID string) (*DeliveryQuote, error)
    AcceptQuote(ctx context.Context, orderID string, version int) (*DeliveryQuote, error)
    RejectQuote(ctx context.Context, orderID string, version int, reason string) (*DeliveryQuote, error)
    ExpireQuotes(ctx context.Context, now time.Time) (int, error)
    CalculatePayable(itemsSubtotal int, quote *DeliveryQuote, deliveryOption string) (payableTotal int, transportFee int, err error)
}
```

### Business Rules
1. **Quote Creation**:
   - Verifies order exists and is in `PENDING` payment status.
   - If previous quote exists, increments `version` (e.g. 1 -> 2) and marks older versions superseded.
   - Sets default expiry (e.g. 15 minutes, matching stock reservation hold).
2. **Customer Acceptance**:
   - Must match latest active quote version.
   - Rejects if quote has already expired (`now.After(quote.ExpiresAt)`).
   - Atomic status transition (`WHERE order_id = $1 AND version = $2 AND status = 'pending'`).
3. **Payable Calculation**:
   - `pickup`: `payableTotal = itemsSubtotal`, `transportFee = 0`.
   - `delivery`: Requires `quote != nil && quote.Status == StatusAccepted && quote.ExpiresAt.After(now)`.
     - `RecipientMerchant`: `payableTotal = itemsSubtotal + quote.AmountKes`, `transportFee = quote.AmountKes`.
     - `RecipientCourier`: `payableTotal = itemsSubtotal`, `transportFee = 0` (courier fee excluded from merchant collection).
4. **Order Store Payment Guard**:
   - `orders.Store.ConfirmPayment` and future STK initiation check delivery acceptance and verify received amount matches `CalculatePayable`.

---

## 4. HTTP API Endpoints (`internal/httpapi/`)

- `POST /orders/{id}/delivery-quote` / `POST /api/orders/{id}/delivery-quote`: Manager creates/revises a delivery quote.
- `GET /orders/{id}/delivery-quote` / `GET /api/orders/{id}/delivery-quote`: Unauthenticated public endpoint returning sanitized non-PII quote details (`orderId`, `version`, `amountKes`, `feeRecipient`, `arrangement`, `status`, `expiresAt`).
- `POST /orders/{id}/delivery-quote/accept` / `POST /api/orders/{id}/delivery-quote/accept`: Customer accepts quote.
- `POST /orders/{id}/delivery-quote/reject` / `POST /api/orders/{id}/delivery-quote/reject`: Customer rejects quote.
- Sliding-window rate limiting applied via `ipLimiter` to prevent abuse.

---

## 5. Frontend & UI Flow

1. **Checkout Flow (`web/static/checkout.js`, `web/checkout.templ`)**:
   - When customer selects `delivery`:
     - Order is placed and 15-minute stock reservation is secured.
     - Checkout overlay polls `GET /api/orders/{id}/delivery-quote`.
     - If quote is pending, displays: "Manager is reviewing transport for your location...".
     - Once quote is ready, displays:
       - Transport arrangement (e.g. "Boda Boda to Nyali").
       - Fee in KES.
       - Payment instruction: "Included in M-Pesa Prompt" (merchant) vs "Pay Directly to Courier on Delivery" (courier).
     - Customer clicks "Accept Delivery Quote & Pay".
     - M-Pesa STK prompt is triggered for the reconciled total.
   - When customer selects `pickup`:
     - Displays KES 0 transport fee (Showroom Pickup).
     - Immediate STK prompt for items total.
2. **Dashboard Management (`web/static/dashboard.js`, `web/dashboard.templ`)**:
   - Fulfillment cards for pending delivery orders show "Create Transport Quote" button with input for amount, fee recipient (`merchant` / `courier`), arrangement description, and expiry.

---

## 6. Implementation Steps

1. **Step 1: Database Migration & Schema Contract**:
   - Create `internal/migration/sql/0004_delivery_quotes.sql`.
   - Update `internal/migration/migration.go` to register step 4.
   - Update `cmd/devstack/main.go` and `internal/migration/readiness.go` with runtime grants.
   - Generate `internal/migration/schema-v4.json`.
   - Update `docs/sql-migrations.md`.
2. **Step 2: Core Domain Logic (`internal/delivery/`)**:
   - Implement `types.go`, `store.go`, `store_test.go`.
   - Implement acceptance, rejection, version bump, expiration sweep, and payable reconciliation math.
3. **Step 3: Orders & HTTP API Integration**:
   - Wire `delivery.Store` into `orders.Store` and `httpapi.Server`.
   - Implement HTTP handlers and non-PII quote responses.
   - Add unit tests in `internal/httpapi/server_test.go`.
4. **Step 4: Frontend Overlay & Template Updates**:
   - Update `web/checkout.templ` and `web/static/checkout.js` to handle quote review and acceptance.
5. **Step 5: Verification Drill & Evidence**:
   - Run unit tests across all packages (`go test -count=1 ./...`).
   - Run integration test drill `scripts/test-integration.ps1`.
   - Run Docker core acceptance suite `scripts/Test-DockerCore.ps1`.
   - Document evidence in `docs/evidence/r11-quoted-delivery.md`.
