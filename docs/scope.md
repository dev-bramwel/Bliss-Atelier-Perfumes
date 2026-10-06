# Scope and completion contract

## Intent

Deliver a real, maintainable perfume commerce application with reliable payments, administration, analytics, a mobile-first storefront, repeatable deployment, monitoring, and a demonstrated capacity envelope. Learn the complete software delivery lifecycle.

## Decisions awaiting the owner

- Does 50,000 mean registered users, daily active users, or concurrent users? Which peak geography and traffic pattern?
- Real Kenyan merchant or demonstration first? Available Daraja sandbox/production access, shortcode type, and merchant onboarding status?
- Weekly engineering hours, desired launch date, monthly infrastructure budget, hosting provider, domain, and ownership of accounts?
- Are Kafka, RabbitMQ, and Kubernetes learning requirements regardless of operational need?
- Guest checkout only, customer accounts, or both? Admin roles and who fulfills orders?
- Stock source, delivery zones/fees, pickup locations, cancellation/refund rules, notification channels, and whether cards are required?
- Analytics consent/retention expectations and business approval of accurate product descriptions, shipping, return, and privacy pages?

Do not supply credentials in chat. Use environment files locally and a deployment secret store.

## Proposed product baseline

Guest checkout plus secure order lookup; admin authentication and roles; database catalog, stock, images, delivery fees, fulfillment states; M-Pesa with retries and reconciliation; merchant-managed refund workflow; transactional notifications; sales and funnel analytics; mobile accessibility and crawlable product pages. Customer accounts and additional payment providers require a scope decision.

## Capacity assumption for planning only

Provisional scenario: 50,000 registered customers, 5,000 daily active visitors, 500 simultaneously browsing, 50 API requests/second steady and 150 requests/second for a ten-minute burst. These values are assumptions, not derivations from the user count. CDN serves most static traffic. Load evidence must state endpoint mix, data size, hardware, replicas, DB pool limits, cache state, and external-provider simulation. Revise before committing infrastructure. 50,000 concurrent users requires a separate sizing and cost exercise.

## Definition of complete

- Approved product workflows work on mobile and desktop, including payment failure and recovery.
- Prices, stock, delivery totals, access controls, audit trails, and payment transitions are enforced server-side.
- Production payment onboarding and supervised live verification completed by the merchant; no claim of payment completion from sandbox tests alone.
- Docker-based clean checkout setup; reproducible CI builds; tested staging and production deployment/rollback; Kubernetes exercise if retained in scope.
- Measured capacity report meets the approved traffic target and SLOs.
- Dashboards, actionable alerts, incident ownership, backups, and successful restore drill.
- Admin and analytics reports reconcile with authoritative paid orders and refunds.
- Domain, HTTPS, sitemap, canonical product pages, metadata, structured data, and Search Console verified. Indexing status checked after launch; indexing or ranking cannot be guaranteed.
- No unresolved release-blocking correctness/security findings; runbooks and user/admin documentation delivered.
