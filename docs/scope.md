# Scope and completion contract

Current R01 decisions: see [release scope decision record](release-scope-decisions.md). Confirmed decisions there supersede conflicting proposals below; unresolved items remain proposals.

## Intent

Deliver a real, maintainable perfume commerce application with reliable payments, administration, analytics, a mobile-first storefront, repeatable deployment, monitoring, and a demonstrated capacity envelope. Learn the complete software delivery lifecycle.

## Confirmed constraints and scope

- Established operating business, managed remotely by the owner and locally by managers.
- Deadline: 10 December 2026; planning starts 6 October 2026. Capacity decision updated 16 October: the owner removed the fixed 14-hours/week cap and prioritizes completed work; actual effort to date is ≤5 hours total. The original 130-hour allocation is historical, not a current limit or feasibility commitment. Planning assumes one developer assisted by coding tools.
- Current infrastructure budget: zero. No domain, hosting, server, cloud credits, or Daraja access. Existing collection method: NCBA bank paybill 880100 with a business collection reference supplied by the owner. Keep the actual collection reference in deployment configuration, not committed documentation.
- Go backend rewrite selected 8 October 2026; frontend stays HTML/CSS/JavaScript. Begin with a modular monolith, with evidence-based microservice extraction later.
- Customers must sign in with Google only when they choose delivery. Browsing, cart use, pickup and other checkout without delivery remain available as guest flows; customer sign-in may be offered elsewhere but is not required. Google is also the only selected sign-in method for admins and employees; Bliss email/password login is not in scope. Bliss manages its own server-side sessions after Google authenticates identity. See [ADR 005](adr/005-google-identity-and-sessions.md). M-Pesa STK must work at customer launch.
- Owner replenishes stock; local managers handle fulfillment, delivery and cancellations. Business policy: no routine refunds; exchanges only for unused perfume.
- Admin scope: products, stock, orders and sales reports. Supplier payments are excluded; implement only after a separately scoped request. Optional Google customer accounts were added by the owner on 23 October 2026; cards remain outside this release baseline.
- Redis, RabbitMQ, Kafka, Docker and Kubernetes are required implementation/learning deliverables by the deadline. Learn monolith versus microservices through an ADR and real component boundaries; a microservice rewrite is not required.
- 50,000 users is a future scalability direction, not a December load acceptance target.

- Existing inventory-only system is in use; this project must establish trustworthy monetary records alongside a deliberate stock integration or migration plan.

## Operating policies and deferred decisions

The [R01 completion contract](release-scope-decisions.md) records the owner-approved policies: no pay-on-delivery; all-Mombasa delivery with customer-paid transport added to the order; a 15-minute stock hold; unopened/unsprayed in-shop exchanges within two days, equal value or higher value with top-up; common pricing with individual-price overrides; verified employee sales immediately updating stock followed by admin review; owner-assigned online orders; private phone-verified buyer feedback; Africa/Nairobi reports; role-scoped WhatsApp/in-app notification preferences; and approved retention defaults.

Existing pickup context: Pickup Mtaani, tailored arrangements and a typical KES 300–400 transport estimate, not a fixed tariff. Confirm the operational details at the review. NCBA’s authorized STK route for the existing bank paybill/reference remains unconfirmed; do not assume direct Daraja access. Existing inventory money-calculation errors motivate reconciliation and a single writable stock authority.

Delivery-fee collection is explicitly undecided until the 23 October 2026 review. Other provider, inventory, pickup, hosting and technical decisions have named owners and the same review date in the contract. These remain dependent implementation/launch gates. Never include credentials or customer PII in decision records.

## Release interpretation and feasibility

The requested December outcome is a thoroughly tested project with all infrastructure implemented, plus working live STK for customer launch. We retain those requirements. The historical 130-hour allocation could not support the previous multi-month feature/HA estimates; removing the fixed cap does not establish available capacity. The roadmap now has a timeboxed candidate plan with explicit feasibility checkpoints. The Go rewrite adds effort requiring a forecast update. Keep implementation narrow, retain the vanilla frontend and prioritize payment correctness. Do not reduce release gates to fit the date.

A local integrated release can demonstrate all technologies at zero infrastructure spend, assuming suitable hardware. It cannot demonstrate public hosting availability or live payment onboarding. A customer launch also requires an authorized production payment route and sustainable public hosting. If these gates remain unmet, classify the December artifact as a tested local/sandbox release and explicitly report the unmet customer-launch requirements; do not treat that as fulfillment of the requested live launch.

## Approved capacity benchmark

Owner-approved first benchmark: 20 simultaneous browser sessions, 10 API requests/second steady for 30 minutes and a 30 requests/second burst for ten minutes, with a fake provider. These measurement targets were approved by the owner in R01; they are not measured capacity claims. Record hardware, data mix, response percentiles, errors and DB/queue behavior. Maintain pagination, indexed queries, bounded DB pools, stateless API scaling and durable workers for future growth. Revisit scale after real usage; do not buy infrastructure for the aspirational 50,000-user count.

## Definition of complete

- Approved product workflows work on mobile and desktop, including payment failure and recovery.
- Prices, stock, delivery totals, access controls, audit trails, and payment transitions are enforced server-side.
- Production payment onboarding and supervised live verification completed by the merchant; no claim of payment completion from sandbox tests alone.
- Docker-based clean checkout setup; reproducible CI builds; tested staging and production deployment/rollback; local Kubernetes deployment and recovery exercise, with production deployment conditional on hosting feasibility.
- Measured capacity report meets the approved traffic target and SLOs.
- Dashboards, actionable alerts, incident ownership, backups, and successful restore drill.
- Admin and analytics reports reconcile with authoritative paid orders, exchange adjustments and exceptional payment corrections.
- Public host/subdomain or domain, HTTPS, sitemap, canonical product pages, metadata, structured data, and Search Console verified. Indexing status checked after launch; indexing or ranking cannot be guaranteed.
- No unresolved release-blocking correctness/security findings; runbooks and user/admin documentation delivered.
