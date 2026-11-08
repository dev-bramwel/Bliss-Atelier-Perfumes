# Security and data handling

Status: target requirements, not a completed security audit. Applies to Go rewrite and retained frontend.

## Trust and access boundaries

Browser inputs, provider callbacks and broker messages are untrusted until validated. Never trust client totals, quantities, roles or payment status. Administrative use cases enforce owner/manager permissions server-side, including object ownership and audited mutations. Guest order references alone are not sufficient authorization; use scoped high-entropy tokens with expiry/rotation policy and store only token hashes where appropriate. Avoid tokens in URLs/logs where possible.

### Guest token lifecycle and customer recovery

1. **Token Generation**:
   - Each order creation generates a cryptographically secure, 256-bit random token prefixed with `gt_` and formatted as 64 lowercase hex characters (`gt_<64 hex chars>`).
   - The raw token is returned to the client in the order creation response payload once (`guest_token`). It is never stored in plaintext on disk or in the database.

2. **Secure Hash Persistence**:
   - Only the SHA-256 cryptographic hash of the token is stored in the relational table `public.guest_order_access` alongside `order_id`, `created_at`, `expires_at`, `revoked_at`, and `last_used_at`.
   - The table links to `"Order"(id)` with foreign key cascade on delete, leaving legacy `"Order"` and `"MpesaTransaction"` table schemas untouched.

3. **Expiration and Revocation Policy**:
   - Guest tokens have a default lifetime of **30 days** (`expires_at = created_at + 30 days`).
   - After expiration, or if explicitly revoked via `revoked_at`, guest requests return `403 Forbidden` (`access denied for this order`).
   - Every successful authorization updates `last_used_at` to provide auditability for customer access patterns.

4. **Customer Recovery Policy**:
   - If a customer clears browser storage or loses their guest credential, they can contact Bliss Atelier customer support via official WhatsApp (`+254 700 000 000`) or email.
   - Support operators verify ownership using out-of-band proofs:
     - M-Pesa transaction receipt number (`mpesaReceiptNumber`).
     - Customer phone number matched to order.
     - Approximate order timestamp and items ordered.
   - Once verified, support managers inspect the order using authenticated manager credentials (`Authorization: Bearer <manager_secret>`, `Authorization: Manager <manager_secret>`, `X-Manager-Secret`, and optional `X-Manager-Id`) and assist the customer directly with order fulfillment status or quote acceptance. Manager interfaces never disclose raw or hashed tokens. Unauthenticated requests supplying an unverified `X-Manager-Id` header without valid manager credentials are strictly rejected with HTTP 401 Unauthorized.

Choose admin authentication/session mechanism in an ADR before implementing it. For cookies: Secure, HttpOnly, appropriate SameSite, CSRF protection for mutations, logout/revocation and inactivity limits. Hash passwords with a reviewed modern password hashing library and parameters; no custom cryptography. Rate-limit login, order creation, payment prompts and lookup without allowing arbitrary client headers to bypass identity/IP controls. Only trust forwarded headers from known proxy hops.

## Payment controls

A signed callback URL proves possession of a capability, not provider authentication of the body. Validate shape and request/attempt mapping, expected amount/receipt and merchant destination; verify unresolved outcomes using authorized provider reconciliation. Never downgrade settled success or create another charge on ambiguous timeout. Callback/query strings and bodies can include sensitive identifiers: redact them in application, reverse proxy, tracing and ingress logs.

Separate routine no-refund merchandise policy from duplicate/incorrect-charge correction. Require authorized, auditable resolution; don't silently write a payment status to make reports balance. No real charges from CI/load tests. Provider credentials only in approved secret stores or untracked local environment files.

## Data lifecycle

Collect minimum contact/location needed for fulfillment. Replace full browser-local customer/order backups with a scoped reference when guest lookup is implemented. Never publish customer data to Kafka analytics by default. Audit actor/action/entity/time/reason without storing secrets. Apply the owner-approved defaults in [R01](release-scope-decisions.md#approved-retention-policy) for financial records, operational contact data, feedback, logs, audit and backups; finalize analytics/event retention before production; enforcement includes exports, backups and replayed events.

Public catalog is distinct from private financial/operational records. Transport fees, product sales, exchanges and exceptional corrections have explicit ledger/report definitions. Use UTC persistence; use the approved Africa/Nairobi reporting timezone. Parameterized SQL, output escaping and constrained uploads protect query/template/media boundaries. Inventory imports are validated and auditable; reject malformed mapping instead of corrupting stock.

## Release evidence

Authorization abuse tests; secret/dependency/image scans; redaction tests; session/CSRF checks; scoped order lookup; secure config validation; provider fake tests; incident/rotation runbook. Record findings and dispositions with owner and due date. A document or scanner pass alone is not a penetration test or legal compliance certification.
