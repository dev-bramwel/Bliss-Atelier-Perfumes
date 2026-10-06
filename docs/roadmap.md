# Roadmap and issue backlog

Status: draft. All issues below are planned, not completed. IDs are local identifiers; transfer them to the selected tracker after review. Owner defaults to project developer, with merchant decisions owned by business owner. Dependencies are explicit issue IDs.

## Schedule model

Estimates include implementation, review, documentation and verification, but exclude provider approval wait times. Initial range: 65–100 focused engineering days for the baseline; Kafka/Kubernetes adds 10–20 days. At five focused days/week this is approximately 13–20 weeks plus the optional infrastructure work. At ten hours/week, assuming six focused hours/day, baseline is approximately 39–60 weeks. Add 20% contingency when making a calendar commitment. These are planning estimates, not a deadline; revise after the first milestone. No fixed dates until availability, scope and budget are confirmed.

| Milestone | Effort | Depends on | Exit gate |
| --- | --- | --- | --- |
| M0 Scope and design | 3–5 days | — | Scale, budget, workflows, ADRs and acceptance criteria approved |
| M1 Reproducible foundation | 5–8 days | M0 | Clean Docker setup, CI, DB integration tests, validation |
| M2 Reliable commerce/payments | 15–22 days | M1 | Inventory, idempotency, recovery and payment tests pass |
| M3 Admin and customer experience | 18–28 days | M2 | Admin access/audit, mobile checkout, fulfillment and analytics pass |
| M4 Operations and delivery | 10–15 days | M2; closes after M3 | Telemetry, staging, deployment/rollback, restore drill |
| M5 Capacity and launch | 14–22 days | M3, M4 | Load, security, SEO and merchant launch evidence |
| M6 Kafka/Kubernetes learning or production extension | 10–20 days | M4; production gate M5 | Replay, cluster recovery, documented costs and operational ownership |

M4 work may begin during M3; do not assume this reduces total solo effort. If all tools are mandatory at first release, M6 becomes a prerequisite to the launch portion of M5.

## Issue catalog

| ID | Milestone / priority | Work and acceptance evidence | Dependencies |
| --- | --- | --- | --- |
| BAP-001 | M0 / P0 | Approve scope, traffic model, budget, merchant requirements and completion contract; record decisions | — |
| BAP-002 | M0 / P1 | ADRs for frontend rendering, hosting, identity, messaging and data retention; OpenAPI contract draft; cost estimate | 001 |
| BAP-003 | M1 / P0 | Strict request/env schemas, bounded bodies, phone validation, safe errors, abuse limits, CORS/security headers; invalid inputs fail predictably | 002 |
| BAP-004 | M2 / P0 | DB catalog/order items, inventory reservations and multiple payment attempts; migration/backfill/seed; concurrent purchase cannot oversell | 003 |
| BAP-005 | M2 / P0 | Durable checkout/payment idempotency; retries and concurrent same-key calls create one logical order/attempt; conflicts return 409 | 004 |
| BAP-006 | M2 / P0 | Callback correlation/deduplication and guarded transitions; verify amount/receipt policy; duplicates and late failure never duplicate effects or downgrade PAID | 004, 005 |
| BAP-007 | M2 / P0 | Provider timeout/response validation, token reuse, stale-attempt reconciliation, safe retries and manual resolution; missing callback and ambiguous initiation tests | 006, 011 |
| BAP-008 | M3 / P0 | Admin authentication/MFA strategy, RBAC, scoped guest order tokens, CSRF/session protections, audit and PII minimization; ownership/role tests | 003, 004 |
| BAP-009 | M3 / P1 | Mobile storefront and persistent order/payment journey; resume/retry without duplicate charge, honest paid/pending/failed states; accessibility and browser E2E | 005, 007, 008 |
| BAP-010 | M1 / P0 | Dockerfiles and Compose for frontend/API/Postgres; lock runtime and dependencies, non-root images, volumes/health checks; clean-machine setup and CI tests | 002 |
| BAP-011 | M2 / P1 | Redis cache/limits plus RabbitMQ worker/outbox; acknowledgments, bounded retries, DLQ/redrive and broker outage tests; no lost committed job | 004, 010 |
| BAP-012 | M3 / P1 | Admin product/stock/order/delivery/fulfillment workflows and refund policy; audited changes, pagination and concurrent update tests | 007, 008 |
| BAP-013 | M4 / P1 | Metrics/logs/traces, dashboards, alert routes, health endpoints, graceful shutdown; synthetic outage reaches designated responder | 010, 011 |
| BAP-014 | M4 / P0 | CI/CD with immutable images, single migration job, staging smoke, controlled production release and rollback; secret scanning and least-privilege credentials | 010, 013 |
| BAP-015 | M3 / P1 | Sales/refund reports and privacy-aware funnel analytics; event deduplication, definition of revenue, timezone/filter tests, financial reconciliation | 006, 008, 012 |
| BAP-016 | M5 / P1 | Crawlable product/category pages, canonical URLs, sitemap/robots, accurate Product/Organization data, domain/HTTPS/Search Console; validation and index checks | 009, 014 |
| BAP-017 | M5 / P0 | Production DB HA/backup plan and restore drill, retention and secrets rotation runbooks; demonstrate approved RPO/RTO | 014 |
| BAP-018 | M5 / P0 | Load/soak/spike and failure tests, security review and accessibility audit; publish environment-specific capacity report | 009, 012, 013, 014 |
| BAP-019 | M6 / P2 | Kafka analytics stream, per-destination outbox, schema versioning, retention and replay; aggregates reproduce without duplicate revenue | 011, 015 |
| BAP-020 | M6 / P2 | Kubernetes staging manifests/Helm, ingress/TLS, probes/resources/HPA, secret handling, rollout/pod-loss drills; decide production promotion from evidence | 013, 014, 017 |
| BAP-021 | M5 / P0 | Merchant live payment verification, launch checklist, runbooks/admin guide, monitoring ownership and rollback rehearsal; release sign-off | 016, 017, 018 |

IDs in Dependencies omit the BAP prefix for readability. 019/020 remain requested capabilities with delivery placement pending the owner's learning decision, not silently dropped scope.

## Issue execution template

Each tracker issue should include: problem and user outcome; scope/exclusions; dependencies; proposed API/data/UI change; acceptance criteria; test scenarios; observability; security/privacy implications; migration and rollback; estimate; responsible owner; links to PR and evidence. Split issues larger than five focused days into reviewable child issues after design. Use labels for milestone, area, priority and status.

Workflow: Backlog → Ready (decisions/dependencies resolved) → In progress → Review → Verification → Done. Work in progress limit: one primary implementation issue per developer. Conduct weekly review of blocked decisions, estimates, incidents and scope. Do not call a ticket complete while its required evidence is missing.

## First execution sequence

Resolve BAP-001, finalize BAP-002, then implement BAP-010 and BAP-003. Establish CI/database integration evidence before changing payment persistence. Next implement BAP-004/005/006 together as small dependent changes, then durable workers/reconciliation. Cosmetic redesign must not delay fixing duplicate-payment risks.
