# Security and data handling

Status: target requirements, not a completed security audit. Applies to Go rewrite and retained frontend.

## Trust and access boundaries

Browser inputs, provider callbacks and broker messages are untrusted until validated. Never trust client totals, quantities, roles or payment status. Administrative use cases enforce owner/manager permissions server-side, including object ownership and audited mutations. Guest order references alone are not sufficient authorization; use scoped high-entropy tokens with expiry/rotation policy and store only token hashes where appropriate. Avoid tokens in URLs/logs where possible.

Choose admin authentication/session mechanism in an ADR before implementing it. For cookies: Secure, HttpOnly, appropriate SameSite, CSRF protection for mutations, logout/revocation and inactivity limits. Hash passwords with a reviewed modern password hashing library and parameters; no custom cryptography. Rate-limit login, order creation, payment prompts and lookup without allowing arbitrary client headers to bypass identity/IP controls. Only trust forwarded headers from known proxy hops.

## Payment controls

A signed callback URL proves possession of a capability, not provider authentication of the body. Validate shape and request/attempt mapping, expected amount/receipt and merchant destination; verify unresolved outcomes using authorized provider reconciliation. Never downgrade settled success or create another charge on ambiguous timeout. Callback/query strings and bodies can include sensitive identifiers: redact them in application, reverse proxy, tracing and ingress logs.

Separate routine no-refund merchandise policy from duplicate/incorrect-charge correction. Require authorized, auditable resolution; don't silently write a payment status to make reports balance. No real charges from CI/load tests. Provider credentials only in approved secret stores or untracked local environment files.

## Data lifecycle

Collect minimum contact/location needed for fulfillment. Replace full browser-local customer/order backups with a scoped reference when guest lookup is implemented. Never publish customer data to Kafka analytics by default. Audit actor/action/entity/time/reason without storing secrets. Define retention durations with business owner for orders, contact data, logs, audit and events before production; enforcement includes exports, backups and replayed events.

Public catalog is distinct from private financial/operational records. Transport fees, product sales, exchanges and exceptional corrections have explicit ledger/report definitions. Use UTC persistence; confirm merchant reporting timezone. Parameterized SQL, output escaping and constrained uploads protect query/template/media boundaries. Inventory imports are validated and auditable; reject malformed mapping instead of corrupting stock.

## Release evidence

Authorization abuse tests; secret/dependency/image scans; redaction tests; session/CSRF checks; scoped order lookup; secure config validation; provider fake tests; incident/rotation runbook. Record findings and dispositions with owner and due date. A document or scanner pass alone is not a penetration test or legal compliance certification.
