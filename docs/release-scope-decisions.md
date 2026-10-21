# R01 release scope and operating decisions

Issue: [#7](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/7). Branch: `release-scope`.
Updated: 16 October 2026. Evidence: owner/developer decisions in the project conversation; documentation only, no runtime or production verification.
Status: owner-approved planning contract, with explicitly deferred decisions assigned below. Approval was given in the project conversation through 16 October 2026; this is a recorded conversational sign-off, not a separate executed signature document. This record supersedes conflicting earlier planning assumptions for the confirmed items below.

## Confirmed scope and capacity

The owner confirmed the existing release scope: guest checkout, Mombasa delivery/pickup, live M-Pesa STK, unused-perfume exchanges, administration and sales reports, Go backend with HTML/CSS/JavaScript frontend, and Redis, RabbitMQ, Kafka, Docker and Kubernetes deliverables. The owner confirmed authority to approve the completion contract in this conversation.

10 December 2026 remains the target; one developer and zero infrastructure budget remain the planning baseline. On 16 October the owner superseded the fixed 14-hours/week constraint: manage delivery by completed work rather than limiting work to allocated hours. This is not an assertion of unlimited availability or proof the date is feasible. Actual effort so far is no more than five hours total; weekly breakdown is unavailable. The issue's provisional 212–335-hour estimate remains unvalidated. Review completed acceptance criteria and remaining work weekly; record actual effort when available without using the former hours allocation as a hard cap. Reforecast after the first tested Go/database slice. Security, payment and recovery gates remain mandatory.

## Confirmed operating policies

- Payment on delivery is prohibited. Verify payment before dispatch; customer approves the transport quote and pays transport fees. Delivery covers all Mombasa areas. Delivery fees are added on top of the product order total and paid by the customer. Fee settlement/recipient and accounting treatment remain to be specified.
- Hold stock for 15 minutes during payment. An unpaid expired hold releases availability; late confirmed payment must be reconciled against available stock before fulfillment. Exact race/late-payment handling requires an implementation contract and tests.
- Only unused perfume qualifies for an exchange, performed at the shop without an exchange delivery budget. The owner clarified that the exchange window is the first two days after purchase; after that window closes, exchanges are prohibited. This supersedes the earlier interpretation of a two-day waiting period. Employees inspect bottles, which must be unopened and unsprayed. Replacement must be equal value with no extra charge, or higher value with the customer paying the difference; lower-value replacements are excluded. Employee inspection establishes eligibility under these rules.
- All perfumes currently cost KES 2,500. Add administration controls for a common bottle price and individual bottle prices. An individual bottle price overrides the common price. Preserve the price charged on each sale when catalog prices change.
- The business has an owner and two shop employees. The owner replenishes stock, manages social media and online sales, and forwards online sale details to employees. Employees operate the shop and record verified sales for admin review; each sees their own sales. Entry must be fast and accurate. Stock reduces immediately when an employee records a verified sale; admin reviews afterward. Record the sale, payment verification evidence, employee and review state; define measurable entry-time targets and correction rules before implementation. Admin review must not cause a second stock deduction. The owner assigns online orders to either employee for fulfillment. Admin manages stock; successful sales automatically update stock. Exact stock reservation/commit mechanics must prevent double deduction and overselling.
- Owner authority over pricing/stock is confirmed by context; employee pricing/stock mutation is not authorized. Employee access to forwarded online fulfillment details must be scoped separately from access to another person's sales reports.

- Reports use Africa/Nairobi. Users choose notification channels (WhatsApp and in-app) and eligible notification types. Admin options include sales, payment and stock alerts, with a basic statistical report covering the whole shop. Employee options include assigned-order and confirmed-sale alerts, scoped to their own sales and assigned orders. Preferences never expand role permissions. WhatsApp delivery feasibility and reliability handling remain technical dependencies.
- Customer feedback is private: a rating and optional comment after a verified purchase, reviewed and followed up by the owner. Match the purchase to the buyer’s phone number and verify control of that number with a one-time code before accepting feedback. Knowing a phone number alone grants no access. Rate-limit verification attempts, expire codes and never log codes or reveal another customer’s purchase.
- Duplicate payments, overpayments and payments for unavailable stock require owner review and an auditable correction/refund, separate from the no-routine-refunds policy. No automatic financial correction or refund disbursement is authorized by this record.

## Approved traffic and recovery targets

The owner approved 20 simultaneous browser sessions, 10 API requests/second steady for 30 minutes and a 30 requests/second burst for ten minutes, using a fake payment provider. Approved recovery objectives: RPO ≤15 minutes and RTO ≤2 hours. These are acceptance targets requiring environment-specific load tests and restore evidence, not measured capacity or achieved recovery claims. Detailed latency/error gates in testing.md remain provisional.

## Approved workflow contract

1. A guest starts checkout for an available KES 2,500 bottle: hold it for 15 minutes, obtain approval of the delivery quote, verify required payments, then permit dispatch. No pay-on-delivery option exists.
2. An unpaid checkout exceeds 15 minutes: release its stock hold. A later payment enters reconciliation rather than silently promising unavailable stock.
3. An employee records a verified shop sale for admin review: stock updates once; that employee can see their own sale. Admin review does not deduct stock again. Another employee cannot browse it through their personal sales report.
4. The owner changes common or individual prices: new sales use the applicable configured price; historical sales retain their original amounts.
5. Within two days from purchase, a customer brings an unopened, unsprayed bottle to the shop: following employee inspection, an equal-value replacement costs nothing extra; a higher-value replacement requires a verified top-up. An exchange requested after the two-day window is rejected.
6. The owner assigns an online order to an employee, who receives the fulfillment details needed for that order. Alerts follow each user’s channel/type preferences and role permissions; feedback requires a verified purchase and a one-time code for its associated phone number.

## Approved retention policy

Owner-approved defaults below are product policy; legal adequacy must be checked before production. Retention enforcement is implementation work, not delivered by this document.

| Record | Retention |
| --- | --- |
| Sales, payments, exchanges and financial audit records | 7 years, subject to applicable legal requirements |
| Operational customer phone/address | 90 days after completion; remove unless needed for an unresolved case |
| Private feedback | 12 months, then delete or anonymize |
| Operational logs | 30 days; redact phones and addresses |
| Security/admin audit logs | 12 months |
| Backups | Rolling 30 days; deleted data expires as backups age out |

Separate retained financial facts from operational contact/location fields; long-lived financial retention does not authorize retaining all customer contact data for seven years. Financial audit retention takes precedence for audit entries supporting financial records. Feedback eligibility must remain bounded by retained purchase/verification data; an aged-out phone association must not be reconstructed without an approved purpose. Restores must reapply deletion rules before reopening access; track unresolved-case exceptions and remove them when resolved. Analytics/event retention and consent/provider handling require follow-up before production.

## Deferred decisions and accountable review

The owner approved business/payment-provider ownership for the owner and technical verification ownership for the developer, with review due **23 October 2026**. Open decisions are permitted by R01 when assigned an owner and date. They remain gates for dependent implementation/launch and are not silently approved defaults.

| Decision / dependency | Owner | Review due | Required outcome |
| --- | --- | --- | --- |
| Delivery-fee collection, recipient, settlement and accounting | Owner | 23 Oct 2026 | Explicitly left unanswered at owner request; decide whether shop collects the full amount or provider collects fees. No collection mechanism is approved yet. |
| Pickup arrangements and quote/payment verification details | Owner | 23 Oct 2026 | Pickup location/process, fee verification and handover responsibility |
| Authorized NCBA/STK route and production onboarding | Owner | 23 Oct 2026 | Provider response and next action; no assumed approval |
| Existing inventory system and stock authority | Owner, developer verifies replacement | Replacement selected 21 Oct 2026; implementation follow-up remains | MyAccurateBooks replaced at future cutover by Bliss Atelier; [ADR 004](adr/004-inventory-authority.md). Owner enters the first opening-stock batch; actual balances/discrepancies remain pending. Intake and picture storage are [open research](adr/005-opening-stock-and-product-images.md). |
| Machine resources, hosting, HTTPS and durable storage | Developer | 23 Oct 2026 | Feasibility evidence under zero budget; identify live-launch blockers |
| WhatsApp and phone-code delivery | Developer | 23 Oct 2026 | Provider, cost/consent/privacy, retry/failure behavior; in-app channel does not waive required WhatsApp delivery |
| Sale-entry performance, corrections and stock/payment races | Developer; owner approves business corrections | 23 Oct 2026 | Measurable fast-entry criteria, auditable correction workflow and deterministic verification scenarios |
| Detailed latency/error gates and analytics/event retention | Developer proposes; owner approves policy | 23 Oct 2026 | Final gates and data lifecycle before dependent release verification |

## Completion and scope/date decision

The owner approved the operating policies and workflow choices above through iterative answers. Full scope and 10 December target remain, with the previous fixed hours cap removed; delivery feasibility remains unproven. Initial actual effort is ≤5 hours total as of 16 October; a weekly split was not supplied. At each weekly review record completed gates, total/weekly actual effort when available, remaining estimate, blockers and an explicit retain/change scope/date/capacity decision. Next review: 23 October. Reforecast after the first tested Go/database slice. This satisfies planning sign-off without claiming implementation or a feasible delivery commitment.

R01 closes the planning decision record only. Production STK, load tests, restore drills, notification delivery, data lifecycle enforcement and all implementation gates remain required in their delivery issues. The scope.md definition of complete still applies to the product; no payment, security or recovery gate is waived.

## Change and verification record

Documentation changes only. Schema/config/API changes and data migration: not applicable because no executable behavior changed. Rollback: revert the documentation change. No tests or recovery drills were run for these policy decisions. Authorization boundaries are described above; secret values and customer PII are neither required nor recorded. Notification permissions, retention and recovery/traffic targets are approved here; delivery feasibility and implementation signals remain assigned follow-ups. Local verification: documentation diff whitespace and relative-link checks, plus self-review of this record against the owner’s answers. No payment/security implementation is changed. No claim of production readiness or completed roadmap implementation is made.
