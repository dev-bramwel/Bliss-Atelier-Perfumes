# Scope and completion contract

## Intent

Deliver a real, maintainable perfume commerce application with reliable payments, administration, analytics, a mobile-first storefront, repeatable deployment, monitoring, and a demonstrated capacity envelope. Learn the complete software delivery lifecycle.

## Confirmed constraints and scope

- Established operating business, managed remotely by the owner and locally by managers.
- Deadline: 10 December 2026; planning starts 6 October 2026. Developer availability: 14 hours/week, approximately 130 hours across the original interval; roughly 126 hours remain from Oct 8 before subtracting spent effort. Team size is not confirmed; planning assumes one developer assisted by coding tools.
- Current infrastructure budget: zero. No domain, hosting, server, cloud credits, or Daraja access. Existing collection method: NCBA bank paybill 880100 with a business collection reference supplied by the owner. Keep the actual collection reference in deployment configuration, not committed documentation.
- Go backend rewrite selected 8 October 2026; frontend stays HTML/CSS/JavaScript. Begin with a modular monolith, with evidence-based microservice extraction later.
- Guest checkout with delivery and pickup. M-Pesa STK must work at customer launch.
- Owner replenishes stock; local managers handle fulfillment, delivery and cancellations. Business policy: no routine refunds; exchanges only for unused perfume.
- Admin scope: products, stock, orders and sales reports. Supplier payments are excluded; implement only after a separately scoped request. Customer accounts and cards are outside this release baseline.
- Redis, RabbitMQ, Kafka, Docker and Kubernetes are required implementation/learning deliverables by the deadline. Learn monolith versus microservices through an ADR and real component boundaries; a microservice rewrite is not required.
- 50,000 users is a future scalability direction, not a December load acceptance target.

- Existing inventory-only system is in use; this project must establish trustworthy monetary records alongside a deliberate stock integration or migration plan.

## Remaining business and launch decisions

- NCBA documentation identifies 880100 as its generic bank paybill. Confirm whether the supplied reference is an NCBA Till short code or account reference and obtain the authorized STK integration path from NCBA; direct merchant Daraja access to that bank shortcode is not established.
- Mombasa is the initial delivery area. Pickup uses Pickup Mtaani with arrangements tailored to the customer; typical transport charge KES 300–400, not a fixed tariff. Confirm fee recipient, quote approval and payment timing, precise coverage, pickup details and reservation duration.
- Existing system manages inventory; money calculations have frequent errors. Identify system, export/API support, replacement versus integration, stock authority and reconciliation process. Avoid two independent writable stock systems.
- Define unused-exchange approval, inspection, stock treatment, transport payer and price differences. Clarify failed/duplicate payment exception handling separately from routine no-refund policy.
- Owner/manager permissions: proposed owner manages users/reports/catalog/stock; manager manages fulfillment/cancellation/exchange records. Financial report access needs explicit decision.
- Available development machine RAM/CPU and Docker support, relevant to running both brokers and local Kubernetes.
- Hosting feasibility, public HTTPS callback endpoint and durable storage for a real release under zero budget. Evaluate current free-service restrictions before selecting a provider; none is selected or guaranteed.
- Analytics retention, approved product/business/policy content and who owns launch operations.

Do not supply credentials in chat. Use local environment files and deployment secret storage.

## Release interpretation and feasibility

The requested December outcome is a thoroughly tested project with all infrastructure implemented, plus working live STK for customer launch. We retain those requirements. Approximately 130 hours cannot honestly inherit the previous multi-month feature/HA estimates; the roadmap now has a timeboxed candidate plan with explicit feasibility checkpoints. The Go rewrite adds effort requiring a forecast update. Keep implementation narrow, retain the vanilla frontend and prioritize payment correctness. Do not reduce release gates to fit the date.

A local integrated release can demonstrate all technologies at zero infrastructure spend, assuming suitable hardware. It cannot demonstrate public hosting availability or live payment onboarding. A customer launch also requires an authorized production payment route and sustainable public hosting. If these gates remain unmet, classify the December artifact as a tested local/sandbox release and explicitly report the unmet customer-launch requirements; do not treat that as fulfillment of the requested live launch.

## Capacity baseline for agreement

Proposed first benchmark: 20 simultaneous browser sessions, 10 API requests/second steady for 30 minutes and a 30 requests/second burst for ten minutes, with a fake provider. These are proposed measurement targets, not capacity claims or user-approved limits. Record hardware, data mix, response percentiles, errors and DB/queue behavior. Maintain pagination, indexed queries, bounded DB pools, stateless API scaling and durable workers for future growth. Revisit scale after real usage; do not buy infrastructure for the aspirational 50,000-user count.

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
