# R11 Quoted Delivery and Customer Acceptance Evidence

Issue: [#17](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/17). Branch: `feat/17-quoted-delivery` targeting `develop`. Date: 7 November 2026.
Verification environment: Disposable local PostgreSQL 16 container, Docker Core Compose stack, Go unit & integration test suites, and R06 backup/restore/re-adoption drill. Legacy `"Order"` and `"MpesaTransaction"` table column schemas preserved with exact fingerprint reproducibility.

## Summary of Delivered Behavior

1. **Versioned Delivery Quotes (`public.delivery_quote`)**:
   - Migration `0004_delivery_quotes.sql` adds relational `public.delivery_quote` table with foreign key reference to `"Order"(id)` (cascading on delete).
   - Fields: `id` (UUID), `order_id` (text), `version` (int, > 0), `amount_kes` (int, >= 0), `fee_recipient` (`'merchant'` vs `'courier'`), `arrangement` (text), `status` (`'pending'`, `'accepted'`, `'rejected'`, `'expired'`), `expires_at` (timestamptz), `created_at` (timestamptz), `accepted_at` (timestamptz), `rejected_at` (timestamptz), `created_by` (text), `customer_notes` (text).
   - Compound unique constraint on `(order_id, version)` enforces strict version increments without version collision.
   - Indexes on `(order_id, status)` and `(status, expires_at)` support fast quote lookups and worker expiration sweeps.
   - Exact catalog contract verified and snapshot in `schema-v4.json`.

2. **Recipient Accounting & Excluded Courier Fees**:
   - When `fee_recipient == 'courier'`, transport fee is paid in cash or direct M-Pesa to the delivery rider upon arrival.
   - Direct courier transport fee is strictly excluded from merchant collection and M-Pesa STK push (`payable_merchant_kes = items_subtotal_kes`, `customer_direct_kes = amount_kes`).
   - When `fee_recipient == 'merchant'`, merchant collects the delivery fee upfront alongside perfume items (`payable_merchant_kes = items_subtotal_kes + amount_kes`, `customer_direct_kes = 0`).
   - Showroom pickup orders require zero delivery quotes (`payable_merchant_kes = items_subtotal_kes`, `customer_direct_kes = 0`).

3. **Quote Acceptance Gate Before Payment Confirmation**:
   - `orders.Store.ConfirmPayment` enforces that delivery orders cannot have payment confirmed or stock committed without an accepted, unexpired delivery quote (`ErrDeliveryQuoteUnaccepted`, `ErrQuoteExpired`).
   - Re-quoting an order increments version and resets quote status to `'pending'` (`changed/expired quotes need new acceptance`).
   - Showroom pickup orders proceed without requiring a delivery quote.

4. **Sanitized HTTP API Surfaces**:
   - `POST /orders/{id}/delivery-quote` (and `/api/orders/{id}/delivery-quote`): Manager submits versioned quote with recipient, amount, arrangement, and expiration.
   - `GET /orders/{id}/delivery-quote` (and `/api/orders/{id}/delivery-quote`): Customer inspects current active quote and reconciled payable breakdown (no internal manager actor PII disclosed).
   - `POST /orders/{id}/delivery-quote/accept` (and `/api/orders/{id}/delivery-quote/accept`): Customer accepts versioned quote. Idempotent acceptance succeeds; expired quotes fail with 409 Conflict.
   - `POST /orders/{id}/delivery-quote/reject` (and `/api/orders/{id}/delivery-quote/reject`): Customer rejects quote with explanation note.
   - `GET /orders/{id}` returns sanitized `PublicOrder` with attached `delivery_quote` and `payable_breakdown`.

5. **Automated Expiration Sweeps & Operational Parity**:
   - Background worker (`cmd/worker`) sweeps expired quotes alongside stock reservations every 30 seconds (`deliveryStore.ExpireQuotes`).
   - `cmd/devstack` grants `SELECT, INSERT, UPDATE, DELETE` on `delivery_quote` to `bliss_runtime`.
   - `migration.RuntimeReady` asserts contract presence and table grants across all active tables.
   - R06 backup/restore/re-adoption drill confirms 100% legacy data and history fingerprint match.
