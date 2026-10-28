# R02: NCBA collection route and onboarding

Issue: [#8](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/8). Updated 22 October 2026. Evidence: owner-supplied NCBA guides and bank instructions; local document review only.

The owner confirmed on 22 October that guides have been received and onboarding letters have **not been submitted**. NCBA has documented the integration route; account-specific API authorization, credentials, sandbox access and activation have not been established. Production payment capability remains blocked through onboarding and [R31 supervised settlement verification](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/37).

## Collection identifiers and implementation boundary

Use NCBA's Till STK API for the proposed bank collection route, subject to NCBA confirming the merchant mapping. The owner supplied paybill `880100` and a six-digit collection/account reference privately in the project conversation. Keep the actual collection reference in deployment configuration, as required by scope.md; it is not the internal order ID, an API username, or necessarily the bank-account number required on the signed request letter.

| Identifier | Meaning and handling |
| --- | --- |
| PayBillNo | `880100`, the NCBA collection paybill supplied by the owner. |
| AccountNo | Owner-supplied collection reference; ask NCBA to confirm it is the allocated NCBA Till/alias for this API. Preserve as a string, including leading zeros. |
| Internal order ID | Platform order identity; never substitute it for AccountNo. |
| Internal payment-attempt ID | Durable platform correlation/idempotency identity; created before provider IO. Multiple attempts belong to one order. |
| STK TransactionID / ReferenceID | Provider initiation identifiers, stored separately when returned; not assumed to be the M-Pesa receipt. |
| Notification TransID | M-Pesa reference/receipt; unique reconciliation identity scoped to provider/merchant. |
| Notification FTRef | Core banking reference present in the NCBA alias example; retain separately for reconciliation. |
| Notification BillRefNumber / Narrative | For the NCBA alias example, BillRefNumber is the merchant alias and Narrative is optional payer detail. Neither is documented as our internal order ID. |

The current Express adapter calls Safaricom Daraja, truncates an order ID into AccountReference, and expects Daraja callback fields. These NCBA APIs have different authentication, payloads and reconciliation identifiers. Changing the shortcode or Daraja credentials alone will not implement this route. This decision slice does not switch runtime payment writers or claim a working NCBA adapter. Adapter/callback work belongs to [R14/#20](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/20), durable attempts to [R13/#19](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/19), and reconciliation to [R17/#23](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/23).

## Provider documentation reviewed

The owner supplied and authorized publishing the original guides and blank request forms under [NCBA integration](NCBA%20integration/). Completed forms and credentials must remain private.

| Source | Relevant pages and documented contract |
| --- | --- |
| [NCBA TILL STK PUSH & DYNAMIC QR CODE API_2024.pdf](NCBA%20integration/NCBA%20TILL%20STK%20PUSH%20%26%20DYNAMIC%20QR%20CODE%20API_2024.pdf) | pp. 3–5: account/active alias prerequisites, token, initiation and STK query. QR is optional and outside this slice. |
| [NCBA Paybill-Level Push Notifications Service Guide.pdf](NCBA%20integration/NCBA%20Paybill-Level%20Push%20Notifications%20Service%20Guide.pdf) | pp. 3–6: delivery/authentication and hashing; pp. 11–13: the 880100 NCBA alias notification variant, JSON/XML requests and acknowledgments. |
| [C2B QUERY API SERVICE GUIDE NOV 2024.pdf](NCBA%20integration/C2B%20QUERY%20API%20SERVICE%20GUIDE%20NOV%202024.pdf) | pp. 3–5: signed request, independent token endpoint and receipt-based transaction query. |
| [STK AND API PUSH REQUEST LETTER.docx](NCBA%20integration/STK%20AND%20API%20PUSH%20REQUEST%20LETTER.docx) | Reviewed request template: company letterhead, customer/bank-account details, merchant identifiers, JSON selection, endpoint credentials, and authorized signatures. |
| [Query API Solution Request Letter.doc](NCBA%20integration/Query%20API%20Solution%20Request%20Letter.doc) | Supplied legacy Word template; its form content was not reliably extractable locally. Use the original editable form and NCBA's written instructions; verify its fields before signing. |
| Owner-pasted NCBA correspondence | Endpoint credentials on email and signed letter, branch submission, IPN secret maximum 50 characters, separate STK and C2B requests. Correspondence/submission dates not provided. |

## Endpoint and authentication contracts

The guides publish `https://c2bapis.ncbagroup.com`. They do not identify a sandbox URL or authorize testing on that host. Do not send test charges or credentials to it until NCBA confirms the environment and merchant entitlement.

| Operation | Method/path | Request and interpretation |
| --- | --- | --- |
| STK token | GET `/payments/api/v1/auth/token` | HTTP Basic username/password; response has access_token, token_type, expires_in and status. The guide example uses 18000 seconds; use the actual validated expiry. |
| STK initiate | POST `/payments/api/v1/stk-push/initiate` | Bearer token; TelephoneNo, Amount, PayBillNo, AccountNo, Network=`Safaricom`, TransactionType=`CustomerPayBillOnline`, all documented as strings. No callback URL or per-order reference field is documented. |
| STK status | POST `/payments/api/v1/stk-push/query` | Bearer token; TransactionID from initiation. The sample returns status SUCCESS/FAILED and description, without receipt, merchant or amount evidence. Obtain semantics before treating it as settled money. |
| C2B token | GET `/api/auth/generate-token` | Separate Basic authentication flow; responseCode `000` and token indicate the documented successful token response. Do not reuse the STK endpoint/response parser. |
| Receipt query | POST `/api/payments/query` | Bearer token; SHORTCODE, TYPE, MPESA_REFERENCE. Sample TYPE is CORPORATE, not a confirmed value for this merchant. Returns transactions with transID, businessShortCode, billRefNumber, transAmount, mobile, transTime, name and transType. Numeric status semantics are not specified. |
| Paybill IPN | POST to merchant's registered HTTPS endpoint | Request JSON as the proposed format; confirm bank configuration. The alias example includes TransType, TransID, FTRef, TransTime, TransAmount, BusinessShortCode, BillRefNumber, Narrative, Mobile, name, Username, Password and Hash. |

JSON IPN acknowledgments use string ResultCode `0` for accepted and `1` for failed, with ResultDesc. Define HTTP retry/acknowledgment semantics with NCBA before implementation. Authenticate and durably store a valid event before success acknowledgment; duplicate delivery must not repeat financial effects.

The guide's Java sample computes Base64 of the UTF-8 **lowercase hexadecimal SHA-256 text**, not Base64 of the raw digest. Its concatenation is secret + TransType + TransID + TransactionTime + TransAmount + CreditAccount + BillRefNumber + Mobile + Name + `1`. Preserve original field strings, especially decimal amounts, during signature verification. Confirm TransactionTime→TransTime, CreditAccount→BusinessShortCode, Name→name, null handling and the exact alias variant with NCBA using a known-answer fixture. The prose describes raw hashed bytes differently; a bank-confirmed vector must resolve that conflict. FTRef and Narrative are absent from that sample concatenation, so do not treat them as authenticated order correlation without confirmation. Never attempt to decode a hash back into its secret.

## Proposed payment and reconciliation flow

1. Platform creates an order with approved server-side totals and a durable payment attempt. The existing R01 requirement is payment before dispatch, with a 15-minute stock hold; its policies supersede legacy pay-on-delivery behavior.
2. Platform submits STK using the fixed configured merchant destination and payer phone. Persist TransactionID and ReferenceID on the attempt; initiation acceptance alone leaves payment pending. Unknown/timeout results remain unresolved and must not automatically create another charge.
3. NCBA sends the configured paybill notification. Verify credentials/hash, merchant destination, receipt, amount and currency assumptions. Store the event with receipt uniqueness before acknowledging it. A notification may precede the initiation response or be redelivered.
4. Correlate only through a bank-confirmed identifier mapping. Because the documented STK request has no per-order correlation field and its sample query lacks a receipt, phone+amount+time is insufficient to automatically select an order. Keep ambiguous and unmatched collections in an auditable reconciliation queue; do not mark an arbitrary order PAID.
5. Query STK for initiation status and C2B by M-Pesa receipt for transaction evidence. C2B is not documented as a listing/discovery API, so it cannot discover an unknown receipt after a lost IPN by itself. Confirm a statement/export or other recovery source with NCBA.
6. Apply verified payment once in a transaction. Duplicate receipts, wrong destination/amount, overpayments, conflicting evidence and late payments after stock release require reconciliation/owner review. A later failure cannot downgrade a verified payment. Dispatch requires payment verification and stock availability; refunds/corrections remain owner-authorized audit actions.
7. Reconcile receipt and FTRef against NCBA account/statement evidence. An HTTP 200, STK acceptance, SMS, or unverified client receipt alone does not prove settlement. Supervised live evidence must connect the intended collection reference to the merchant's credited account before launch.

## Onboarding owners and dates

| Work | Owner | Current status / next checkpoint |
| --- | --- | --- |
| Confirm merchant identity, legal bank-account number, collection alias and authorized signatories | Merchant | Collection details supplied; bank mapping/entitlement not confirmed. Review 23 Oct 2026. |
| Prepare HTTPS IPN endpoint and securely provision separate service credentials | Developer | Endpoint not implemented/deployed. Prepare technical contract and fake-provider checks before merchant submits working endpoint details. Review 23 Oct. |
| STK/IPN signed letter and requested email | Merchant; developer supplies reviewed technical fields | Not submitted, owner confirmed 22 Oct. Submission date unknown. |
| Editable C2B form and signed branch submission | Merchant | Not submitted. Verify original form and required identity fields; submission date unknown. |
| Resolve technical questions and sandbox/test entitlement | Developer drafts; merchant obtains NCBA response | Open; next review 23 Oct. Bank response/activation dates unknown, not promised. |
| Enable services and confirm limits/merchant scope | NCBA, coordinated by merchant | No activation confirmation. |
| Authorized sandbox checks, then supervised production collection/settlement | Developer + merchant | Blocked on entitlement/credentials and implementation; R31/#37 remains a launch gate. |

Use the [onboarding worksheet](templates/ncba-onboarding.example.md) in a private location. Keep completed credentials and signed forms out of Git, issue comments, logs, screenshots and browser code. The IPN secret must not exceed the bank's 50-character limit; generate high-entropy values privately. Bank-approved email/branch delivery is performed by the merchant, not automatically by this project.

## Questions NCBA must answer

- Is the supplied collection reference an NCBA Till alias valid as STK AccountNo, and what legal bank account should the letters identify? Are STK, alias-level IPN and C2B enabled for that exact merchant?
- The letters request a secret key while the token endpoints describe Basic username/password. Is the registered secret the Basic password, or is a separate password issued, for each service?
- For the shared 880100 paybill, what are the correct C2B SHORTCODE and TYPE, and how is access restricted to this merchant's collections?
- What response values mean STK accepted, pending, customer cancellation, timeout and final payment success? How do TransactionID/ReferenceID map to an IPN TransID/FTRef? Is there a supported per-order correlation/narration field not shown in this STK guide?
- Which alias notification variant is enabled, how are missing/hashed phone fields encoded, and what is the exact signature known-answer vector? Are FTRef/Narrative protected? What are retry schedule, duplicate handling, HTTP timeout and retention/replay rules?
- What are sandbox URLs, test credentials/accounts, permitted test amounts, production approval steps, TLS/IP restrictions, rate limits, token expiry/refresh rules, fees and activation dates?
- Does C2B confirm receipt only or bank credit/settlement too? Which statement/query/report covers missed notifications, reversals, settlement delays and reconciliation discrepancies?

## Verification, rollout and closure

This slice reviews documents and records requirements. No schema, runtime configuration or API behavior changes; no data migration is required. Rollback is reverting the documentation. No customer/credential payloads or raw provider errors should enter operational logs. Future implementation needs safe attempt/event identifiers and metrics for pending age, IPN authentication failures, duplicates, unmatched receipts and reconciliation lag, with restricted access to financial/PII records.

Local verification: source-page review, contract cross-check against the existing adapter, and Markdown/link checks. No bank endpoint called, credential generated, letter sent, charge initiated or settlement verified. Dates are checkpoints, not bank commitments.

Issue #8 cannot yet claim its full outcome: the merchant-specific mapping, credential semantics, sandbox requirements and authoritative correlation/settlement capabilities remain unanswered. The route and onboarding inventory are now documented, but close R02 only when NCBA's answers establish those requirements and the issue links reviewed delivery evidence. Activation and supervised settlement must remain explicit blockers in R31/#37 even after that discovery issue closes.
