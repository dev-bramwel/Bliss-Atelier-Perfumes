# Operations, delivery and launch

Status: proposed; nothing in this document implies deployed infrastructure. Budget is zero; no hosting provider or domain is selected. Local Docker/Kubernetes implementation is required; production availability and recovery targets need a feasible hosting plan.

## Service objectives

Future production proposed SLO: 99.9% monthly internal API availability, excluding clearly classified rejected user requests. Provider outages remain visible in a separate end-to-end checkout/payment completion measure; do not conceal them by exclusion. Initial RPO ≤15 minutes and RTO ≤2 hours, subject to database plan and budget. A restore drill must demonstrate both. Set latency gates from testing.md and revise from measured traffic.

## Monitoring

Use OpenTelemetry for request/worker traces, structured JSON logs with correlation IDs, Prometheus-compatible metrics and Grafana dashboards (or equivalent managed services). Redact customer phone/address, credentials, callback token/signature and authorization headers. No order/customer/request IDs as metric labels; keep them in access-controlled logs/traces. Sample routine traces but retain errors under a bounded policy.

Dashboards: HTTP throughput/error/duration by route template; CPU/memory/event-loop lag; DB pool usage/slow queries/connections/storage; Redis latency/hits/evictions; RabbitMQ backlog/oldest-job-age/retries/DLQ; Kafka lag if enabled; order creation/conversion; accepted/succeeded/failed/unknown payments and pending age; outbox age; inventory reservation expiry; backup age and synthetic availability.

Alerts: sustained API error/latency budget burn; failing readiness; DB pool/storage pressure; accepted payment lacking outcome beyond the reconciler window; growing outbox/queue age; dead letters; stale/failed backups. Define thresholds from staging baselines, responder, escalation and runbook URL. Page on urgent actionable failures; report business trends separately. Test delivery with a synthetic incident before launch.

Business analytics: settled product sales adjusted for exchanges and exceptional payment corrections; transport collections reported separately, order volume, average order value, popular products, stock movement and funnel conversion. Define time zone (merchant decision; planning context Africa/Mogadishu), deduplication and retention. Never count STK acceptance or order creation as paid revenue.

## Environments and Docker

Separate development, staging and production credentials/databases. Reproducible locked dependencies and pinned image/runtime versions; multi-stage non-root images; secrets injected at runtime, never baked into layers. Local Compose offers persistent development DB and optional messaging/observability profiles. Startup uses health/readiness, bounded connection pools and graceful HTTP/worker shutdown. Migration is a single deployment job rather than each replica's startup action.

## CI/CD workflow

1. PR checks: lint, tests, schema/migration validation, secret/dependency scan and build.
2. Merge: build immutable image once, record digest/SBOM, scan and publish using least-privilege or federated CI credentials.
3. Staging: run migration job, deploy that digest, execute smoke/payment-contract and migration compatibility checks.
4. Production: controlled promotion of tested digest; expand/contract migrations, readiness gate, gradual rollout, smoke and metrics observation. Keep last good digest available.
5. Rollback: restore previous compatible application image. Destructive DB changes require a reviewed recovery plan; reverting an image cannot reverse data deletion.

Workflow code belongs in `.github/workflows/` if GitHub remains the source host. Environment secrets and protected release settings are configured through the selected platform, with ownership documented. Automation must not share sandbox/production payment keys.

## Kubernetes: required local implementation

API/worker Deployments, Services, ingress/TLS, ConfigMaps, external secret integration, liveness/readiness, resource requests/limits, disruption budgets and autoscaling based on measured CPU/latency/queue pressure. Account for cluster-wide DB pool totals when scaling. Restrict network access and service accounts; test pod termination, rollout and worker redelivery. Prefer managed data services until the team can operate replicated stateful clusters. Document single-node limitations if a budget demo uses them; do not claim HA.

## Runbooks to deliver

- Payment pending/mismatch: inspect attempt and provider reference, query/reconcile, preserve evidence, never initiate a second charge blindly.
- DB incident/restore: assess, stop unsafe writes, restore into isolated environment, validate counts/receipts, reconcile post-restore provider payments, switch and record data loss.
- Broker outage/DLQ: preserve outbox, restore service, replay idempotently, inspect poison messages, track backlog to completion.
- Cache outage: use documented read fallback; protect database from request surge; do not bypass required security limits silently.
- Deployment failure: compare digest/health, roll back compatible image, monitor, escalate migration failures.
- Security incident: revoke/rotate credentials, preserve access-controlled evidence, contain and notify using approved business policy.

Each runbook needs responsible owner, diagnosis commands/dashboard links, safe actions, verification, escalation and post-incident review. Production backup includes object assets and infrastructure config alongside DB; restore broker data or replay outbox/events according to documented retention.

## Search and launch

Deliver stable crawlable product/category URLs, unique accurate titles/descriptions, canonical tags, internal links, optimized images, sitemap.xml and robots.txt. Validate accurate Product/Offer and Organization structured data; no invented ratings. Exclude admin/checkout/private order pages from indexing and protect staging. Configure public domain or suitable host subdomain, HTTPS, Search Console ownership, sitemap submission and URL inspection after deployment. Merchant Center is optional depending on eligibility and business choice.

Google decides indexing and ranking; the acceptance gate is technical readiness, submission and recorded inspection results, followed by index monitoring. See [Google ecommerce guidance](https://developers.google.com/search/docs/specialty/ecommerce) and [Product structured data](https://developers.google.com/search/docs/appearance/structured-data/product).

Launch sequence: signed release evidence → backups/rollback ready → merchant-controlled live payment check → limited customer beta → observe and reconcile → expand traffic. Validate contact/delivery/return/privacy content with the merchant. Record actual spend, capacity and outstanding issues at each expansion. Ongoing work includes dependency updates, monthly restore/reconciliation drills, access reviews and metric-based capacity planning.

## Delivery document ownership

The target runtime is Go, confirmed 8 October 2026. API/worker binaries use bounded resources, context cancellation, graceful shutdown and structured logs. Node/Prisma describe the historical runtime only. Detailed specifications live in [CI/CD](ci-cd.md), [Docker configuration](docker-and-local-environments.md), [infrastructure](infrastructure.md) and [deployment](deployment.md). Keep executed commands, chosen hosting versions and observed evidence in those implementation records rather than inventing a deployed environment here.
