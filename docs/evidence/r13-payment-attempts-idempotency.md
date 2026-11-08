# R13 Evidence: Persist Payment Attempts and Durable Idempotency

**Issue:** [#19 - [R13] Persist payment attempts and durable idempotency](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/19)  
**Milestone:** Week 3: Safe payments and background work  
**Status:** Completed and verified locally against disposable PostgreSQL  

---

## 1. Problem and Outcome

During mobile shopping and network interruptions, customer retries, double-clicks, and connection resets can cause duplicate order creation, duplicate inventory deduction, or duplicate payment charges.

R13 delivers:
1. **Durable Idempotency Engine (`public.idempotency_key`)**:
   - Stores caller/operation scoped keys (`key`, `caller_scope`, `operation`).
   - Tracks canonical SHA-256 request payload digests.
   - Caches completed HTTP response status codes and serialized response bodies.
   - Identical replayed requests return the cached response with header `X-Idempotent-Replay: true`.
   - Tampered payloads with a recycled key return `HTTP 409 Conflict`.
   - Concurrent in-flight requests coordinate safely via `in_progress` status, preventing duplicate records.
2. **Multiple Payment Attempts Ledger (`public.payment_attempt`)**:
   - Decouples single monolithic payment statuses into discrete chronological attempts tied to an `Order`.
   - Tracks attempt number, amount in KES, recipient phone, provider (`mpesa_stk`), provider transaction/reference IDs, error messages, and lifecycle states (`created`, `initiating`, `accepted`, `succeeded`, `failed`, `cancelled`, `unknown`).
3. **Explicit Payment Retry Safety**:
   - Introduces `POST /api/orders/{id}/retry-payment` protected by guest tokens or manager credentials.
   - Enforces the **unresolved attempt rule**: if an active attempt is `initiating` or `accepted` within the provider timeout window (2 minutes), retry requests are rejected with `HTTP 409 Conflict`, preventing duplicate prompts.
   - If previous attempts are `failed`, `cancelled`, or `expired`, creates attempt #2 without recreating the order or duplicate stock holds.
   - Rejects retries if the order is already `PAID` or if the stock reservation has expired.

---

## 2. Schema Changes & Contract Evolution

Added migration `internal/migration/sql/0006_payment_attempts_idempotency.sql` establishing:
- `public.idempotency_key` with composite primary key `(key, caller_scope, operation)` and `expires_at` index.
- `public.payment_attempt` with foreign key to `Order(id) ON DELETE CASCADE` and unique index `(order_id, attempt_number)`.
- Updated migration ledger and readiness check to enforce `schema-v6.json`.
- Zero regression on R06 legacy tables; full backup/restore and schema fingerprint preserved.

---

## 3. Verification & Test Evidence

### A. Unit Tests
Executed across all packages (`go test -count=1 ./...`):
- `TestIdempotencyLifecycle`: verified initial in-progress lock, concurrent rejection (`ErrIdempotencyInProgress`), completion, replayed response with matching hash, conflict rejection (`ErrIdempotencyConflict`) on payload mutation, and failed key retry.
- `TestPaymentAttemptLifecycleAndExplicitRetry`: verified attempt #1 creation during order placement, in-flight unresolved attempt blocking (`ErrUnresolvedPaymentAttempt`), attempt #2 creation after attempt #1 failure, and rejection on already `PAID` order.
- `TestIdempotentOrderCreation`: verified HTTP 201 Created on initial submission, cached replay with `X-Idempotent-Replay: true` on identical resubmission, and HTTP 409 Conflict on payload alteration.
- `TestRetryPaymentEndpoint`: verified HTTP 401 on missing credentials, HTTP 201 on authorized retry, HTTP 409 when attempt in progress, and HTTP 409 on paid order.

### B. Integration Drills
Executed `scripts/test-integration.ps1`:
- Disposable PostgreSQL database container spun up.
- Migrations 1 through 6 applied.
- R06 backup/restore/re-adoption drill passed; full data/history fingerprint preserved.
