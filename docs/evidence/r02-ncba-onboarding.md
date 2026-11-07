# R02 NCBA integration and onboarding evidence

Issue: [#8](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/8). Branch: `feat/payments` / `develop`. Date: 22 October 2026 (updated 7 November 2026).
Verification environment: Local documentation, vendor PDF contract review, and request letter extraction. No production database, live bank endpoints, or payment channels were touched. See [NCBA payments documentation](../ncba-payments.md), [onboarding worksheet template](../templates/ncba-onboarding.example.md), and [vendor integration assets](../NCBA%20integration/).

## Development vs. launch cutover lifecycle boundary

Following the approved project lifecycle, the boundary between the technical development phase and operational live cutover is:

1. **Development Phase (Delivered & Verified in R02)**:
   - Evaluated vendor guides (`NCBA TILL STK PUSH & DYNAMIC QR CODE API_2024.pdf`, `NCBA Paybill-Level Push Notifications Service Guide.pdf`, and `C2B QUERY API SERVICE GUIDE NOV 2024.pdf`).
   - Defined endpoint and authentication contracts (HTTP Basic auth for tokens, Bearer tokens for STK/C2B query, JSON IPN callback).
   - Designed collection identifier model and decoupled internal order ID and durable payment attempt IDs from provider AccountNo (Paybill `880100` with NCBA Till alias).
   - Documented the JSON IPN signature hash algorithm (lowercase hex SHA-256 Base64), acknowledgment semantics, and field concatenation ambiguities.
   - Designed 7-step safe payment and reconciliation sequence (payment before dispatch, 15-minute stock hold, non-downgradable PAID states, durable attempts, and receipt uniqueness).
   - Compiled an inventory of open bank questions and provided the blank merchant onboarding worksheet (`docs/templates/ncba-onboarding.example.md`).
   - Prepared integration contract boundaries for the bounded adapter implementation in [R14 Issue #20](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/20).

2. **Launch Cutover / Supervised Settlement Phase (Operational Prerequisite in R31)**:
   - External onboarding letters (`STK AND API PUSH REQUEST LETTER.docx` and `Query API Solution Request Letter.doc`) signed by authorized business signatories and submitted to NCBA branch by the merchant.
   - NCBA provisions production / sandbox merchant credentials, resolves alias-to-account mapping, and clarifies token/signature vectors.
   - Production route activation and live financial settlement into merchant bank account will be verified under supervision in [R31 Issue #37](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/37) prior to operational launch. Live payments remain hard-blocked until that verification.

## Delivered behavior and acceptance

- **Provider Authorization and Identifiers Without Secrets**:
  - `PayBillNo`: `880100` (NCBA bank collection paybill).
  - `AccountNo`: Merchant collection reference / NCBA Till alias kept strictly in private deployment configuration, decoupled from internal order identifiers.
  - Internal order and payment-attempt IDs are durable platform identifiers created prior to provider I/O.
  - Zero bank credentials, secrets, or customer PII are committed to Git.
- **Onboarding Ownership and External Checkpoints**:
  - Onboarding responsibilities clearly partitioned between Merchant (signatories, bank account, letter submission) and Developer (HTTPS callback endpoint, credentials provisioning, adapter implementation).
  - Checkpoint and review dates recorded without fabricating external bank approval lead times.
- **Production Route Gated**:
  - Confirmed as a mandatory launch gate in [R31 Issue #37](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/37). Production route activation is prohibited until supervised settlement verification succeeds.

## Verification results

- Verified vendor documentation and extracted editable request letter forms under `docs/NCBA integration/`.
- Markdown link consistency and documentation structure verified locally.
- Git diff checks and cleanliness verified (`git diff --check`).
- CI validation passed (`Docker core validation`, `Security and contracts`, `Enforce Issue Dependencies`, `PR Agent`).

## Pull Request and Documentation References

- **PR #44**: `docs(payments): establish NCBA integration and onboarding requirements (#8)`
- [NCBA payments documentation](../ncba-payments.md)
- [NCBA onboarding worksheet template](../templates/ncba-onboarding.example.md)
- [Vendor integration assets](../NCBA%20integration/)
- [Issue #8](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/8)
- Downstream implementation: [Issue #20 (R14)](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/20)
- Downstream settlement gate: [Issue #37 (R31)](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/37)

## Closure Decision

All development acceptance criteria and delivery evidence for [R02 Issue #8](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/8) are satisfied. The engineering integration contract, identifier decoupling, onboarding templates, and bank question inventory are complete. External merchant onboarding submission and supervised settlement verification remain strictly tracked as prerequisites under [R14 Issue #20](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/20) and [R31 Issue #37](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/37).
