# R12 HTTP Validation and Scoped Guest Access Evidence

Issue: [#18](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/18). Branch: `feat/18-http-validation-guest-access` targeting `develop`. Date: 8 November 2026.
Verification environment: Disposable local PostgreSQL 16 container, Docker Core Compose stack, Go unit & integration test suites, and R06 backup/restore/re-adoption drill. Legacy `"Order"` and `"MpesaTransaction"` table column schemas preserved with exact fingerprint reproducibility.

## Summary of Delivered Behavior

1. **Strict Request Body & Input Validation**:
   - 64KB `http.MaxBytesReader` enforcement: Requests with body exceeding 64KB are rejected immediately with safe `413 Payload Too Large` (`{"error": "request body too large; maximum 64KB allowed"}`).
   - Malformed JSON payloads return safe `400 Bad Request` (`{"error": "invalid json payload"}`) without leaking parser internals or stack traces.
   - Field size and sanitization bounds in `internal/orders/validate.go`:
     - Customer Name: 1–100 characters, printable characters only, control characters (`\x00-\x1f`, `\x7f`) and XSS/HTML tags (`<script>`, `javascript:`) rejected.
     - Location: 1–200 characters, printable characters only, control characters stripped/rejected.
     - Customer Notes: maximum 500 runes truncated cleanly on valid UTF-8 boundary, control characters stripped.
     - Phone: Strict Kenyan E.164 and mobile validation (`+254...`, `07...`, `01...`, `254...`, 9–15 digits). Letters, symbols, and SQL/XSS tokens are strictly rejected with `ErrInvalidCustomer`.
     - Order Items: 1 to 50 unique items, 1 to 100 units per line item, and aggregate order quantity capped at 500 units (`ErrInvalidQuantity`).

2. **Scoped Guest Authorization (`public.guest_order_access`)**:
   - Migration `0005_guest_access.sql` creates dedicated relational table `public.guest_order_access`:
     - `order_id` (text, foreign key referencing `"Order"(id)` with `ON DELETE CASCADE`), `token_hash` (text, SHA-256 hash), `created_at` (timestamptz), `expires_at` (timestamptz, default 30 days), `revoked_at` (timestamptz), `last_used_at` (timestamptz).
     - Compound primary key on `(order_id, token_hash)`.
     - Indexes on `token_hash` and `(order_id, expires_at)`.
     - Legacy `"Order"` and `"MpesaTransaction"` tables remain completely untouched, maintaining immutable R06 schema checksums.
   - Token Generation & Storage (`internal/orders/token.go`):
     - High-entropy cryptographic random: 256 bits of entropy from `crypto/rand`, formatted as `gt_<64 hex chars>` (total length 67 chars).
     - Raw token is returned only once in `handleCreateOrder` response (`POST /api/orders` returns `guest_token: gt_...`). Plaintext token is never stored in the database.
     - SHA-256 hash is computed and stored in `public.guest_order_access`.
   - Access Scoping & Verification (`internal/httpapi/security.go`):
     - `GET /orders/{id}` & `GET /api/orders/{id}`: Requires valid matching `X-Guest-Token` (or `Authorization: Bearer gt_...`) or authenticated manager credentials (`Authorization: Bearer <manager_secret>`, `Authorization: Manager <manager_secret>`, `X-Manager-Secret`, and optional `X-Manager-Id`). Unauthenticated requests or requests with unverified `X-Manager-Id` headers return `401 Unauthorized`; mismatched or expired guest tokens return `403 Forbidden`.
     - `GET /orders/{id}/payment-status` & `GET /api/orders/{id}/payment-status`: Requires valid guest token or authenticated manager credentials.
     - `GET /orders/{id}/delivery-quote` & `GET /api/orders/{id}/delivery-quote`: Requires valid guest token or authenticated manager credentials.
     - `POST /orders/{id}/delivery-quote`: Requires authenticated manager credentials (`Authorization: Bearer <manager_secret>` or `X-Manager-Secret`). Forged `X-Manager-Id` headers without valid credentials return `401 Unauthorized`.
     - `POST /orders/{id}/delivery-quote/accept`: Scoped strictly to the order's guest token or authenticated manager credentials. Another guest cannot accept the quote (returns `403 Forbidden`).
     - `POST /orders/{id}/delivery-quote/reject`: Scoped strictly to the order's guest token or authenticated manager credentials. Another guest cannot reject the quote (returns `403 Forbidden`).
   - Frontend Checkout Integration (`web/static/checkout.js`):
     - Automatically stores `guest_token: data.guest_token` in local order object in `localStorage`.
     - Supplies `X-Guest-Token: guestToken` header during automated payment status polling (`pollPaymentStatus`).

3. **Trusted-Proxy & CORS Architecture**:
   - `TrustedProxyChecker` and `SafeClientIP` (`internal/httpapi/security.go`):
     - Evaluates incoming `r.RemoteAddr` against trusted proxy CIDRs (defaulting to loopback `127.0.0.1/32`, `::1/128`, configurable via `GO_TRUSTED_PROXIES`).
     - Untrusted callers have client-supplied `X-Forwarded-For` strictly ignored, preventing IP spoofing.
     - Trusted proxies parse `X-Forwarded-For` right-to-left to safely extract the first untrusted upstream IP.
   - CORS Middleware:
     - Handles `OPTIONS` preflight with `204 No Content`.
     - Configurable via `GO_ALLOWED_ORIGINS` with origin validation and `Vary: Origin`.
     - Allows headers: `Content-Type, Authorization, X-Guest-Token, X-Manager-Id, Idempotency-Key`.
   - Standard Security Headers:
     - `X-Content-Type-Options: nosniff`
     - `X-Frame-Options: DENY`
     - `Referrer-Policy: strict-origin-when-cross-origin`
     - `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`
     - `Cache-Control: no-store`

4. **Token & PII Redaction (`internal/httpapi/redact.go`)**:
   - `RedactString`: Masks any pattern matching `gt_[0-9a-fA-F]{64}` to `gt_[REDACTED]`; masks phone numbers matching `(\+?254|0)[17]\d{8}` to `+254***REDACTED***`.
   - `RedactingReplaceAttr`: Structured slog attribute replacer masking keys `token`, `guest_token`, `raw_token`, `authorization`, `x-guest-token`, `x-manager-id`, `secret`, `password`, `credentials`, `auth` to `[REDACTED]`, and phone keys to `+254***REDACTED***`.
   - Documented token expiry and customer recovery policies in `docs/security-and-data.md`.

5. **Schema Contract & Grant Governance**:
   - `internal/migration/schema-v5.json`: Contract snapshot including `guest_order_access`.
   - `internal/migration/readiness.go`: Asserts presence and `SELECT, INSERT, UPDATE, DELETE` DML grants on `guest_order_access` for `bliss_runtime`.
   - `cmd/devstack/main.go`: Grants `SELECT, INSERT, UPDATE, DELETE` on `guest_order_access` to `bliss_runtime`.
   - R06 backup/restore/re-adoption drill confirms 100% legacy data and history fingerprint match.
