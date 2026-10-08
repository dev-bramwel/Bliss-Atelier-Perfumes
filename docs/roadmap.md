# Roadmap and issue backlog

Status: draft. All issues below are planned, not completed. IDs are local identifiers; transfer them to the selected tracker after review. Owner defaults to project developer, with merchant decisions owned by business owner. Dependencies are explicit issue IDs.

## Calendar and effort budget

Confirmed availability: 14 hours/week from 6 October to 10 December 2026, approximately 130 hours. The previous estimate (65–100 engineering days plus infrastructure) exceeds this budget and is superseded as a scheduling baseline; it remains evidence that the broad production/HA vision cannot be promised in this interval.

This is a candidate allocation, not an estimate proving feasibility. Weekly hours include learning, implementation, verification and documentation. No security/payment/recovery gate may be waived to make the date. Check actual throughput each week; if work exceeds the envelope, document the mismatch and revise date or scope with the owner.

| Window | Budget | Milestone and concrete outcome | Main issues |
| --- | --- | --- | --- |
| Oct 6–12 | 14h | M0 decisions and M1 baseline: merchant onboarding inquiry, narrow contracts, Docker/DB setup and initial CI | 001, 002, 010, 014 foundation |
| Oct 13–19 | 14h | M1/M2: strict validation, DB catalog/stock/order migration and route integration tests | 003, 004 |
| Oct 20–26 | 14h | M2: idempotent orders/attempts, guarded callbacks and scoped guest lookup | 005, 006, 008 guest access |
| Oct 27–Nov 2 | 14h | M2: Redis limits/cache, RabbitMQ outbox worker, reconciliation and recovery tests | 011, 007 |
| Nov 3–9 | 14h | M3: admin authentication/roles and minimal product/stock/order operations | 008, 012 |
| Nov 10–16 | 14h | M3: mobile checkout states, delivery/pickup, financial sales reports | 009, 015 |
| Nov 17–23 | 14h | M6: Kafka event consumer/replay; M4 metrics, logs and dashboards | 019, 013 |
| Nov 24–30 | 14h | M6: local Kubernetes deployment/recovery; CI release workflow, backups/restore | 020, 014, 017 |
| Dec 1–7 | 14h | M5: browser/security/load/recovery tests, crawlable product pages and SEO, merchant STK verification if enabled | 016, 018, 021 |
| Dec 8–10 | ~4h | Release evidence, documentation, sign-off or explicit unmet-gate report | 021 |

Total approximately 130h. There is little contingency; each row is a tightly constrained working budget. These windows replace the earlier sequential milestone assumptions. Authentication/guest access, CI and monitoring will be built incrementally rather than left entirely to their named week. Production HA, microservice extraction, advanced funnel analytics, automated refund disbursement, customer accounts, supplier payments and additional payment providers are outside the December baseline. Unused-perfume exchanges, auditable exceptional payment corrections and accurate sales reporting remain in scope.

## Decision checkpoints

- By Oct 12: owner confirms NCBA-supported STK route and begins authorized production STK onboarding; confirm machine resources and delivery rules. Document provider response instead of inventing approval lead time.
- By Oct 26: benchmark progress against the hour budget and verify whether a viable live payment/hosting path exists. Escalate deadline risk while continuing local implementation.
- By Nov 16: narrow admin/product flows must be testable. Measure remaining effort; agree changes rather than silently dropping required infrastructure.
- By Nov 30: infrastructure integrated and recoverable, public hosting path validated, production payment onboarding resolved or live-launch blocker recorded.
- Dec 10: publish actual test/deployment evidence and launch status. Local sandbox readiness and customer production readiness are reported separately.

## Issue catalog

| ID | Milestone / priority | Work and acceptance evidence | Dependencies |
| --- | --- | --- | --- |
| BAP-001 | M0 / P0 | Approve scope, traffic model, budget, merchant requirements and completion contract; record decisions | — |
| BAP-002 | M0 / P1 | ADRs for frontend rendering, hosting, identity, messaging and data retention; OpenAPI contract draft; cost estimate | 001 |
| BAP-003 | M1 / P0 | Strict request/env schemas, bounded bodies, phone validation, safe errors, abuse limits, CORS/security headers; invalid inputs fail predictably | 002 |
| BAP-004 | M2 / P0 | DB catalog/order items, existing-inventory authority/integration decision, inventory reservations and multiple payment attempts; migration/backfill/seed; concurrent purchase cannot oversell | 003 |
| BAP-005 | M2 / P0 | Durable checkout/payment idempotency; retries and concurrent same-key calls create one logical order/attempt; conflicts return 409 | 004 |
| BAP-006 | M2 / P0 | Callback correlation/deduplication and guarded transitions; verify amount/receipt policy; duplicates and late failure never duplicate effects or downgrade PAID | 004, 005 |
| BAP-007 | M2 / P0 | Provider timeout/response validation, token reuse, stale-attempt reconciliation, safe retries and manual resolution; missing callback and ambiguous initiation tests | 006, 011 |
| BAP-008 | M3 / P0 | Admin authentication/MFA strategy, RBAC, scoped guest order tokens, CSRF/session protections, audit and PII minimization; ownership/role tests | 003, 004 |
| BAP-009 | M3 / P1 | Mobile storefront and persistent order/payment journey; resume/retry without duplicate charge, honest paid/pending/failed states; accessibility and browser E2E | 005, 007, 008 |
| BAP-010 | M1 / P0 | Dockerfiles and Compose for frontend/API/Postgres; lock runtime and dependencies, non-root images, volumes/health checks; clean-machine setup and CI tests | 002 |
| BAP-011 | M2 / P1 | Redis cache/limits plus RabbitMQ worker/outbox; acknowledgments, bounded retries, DLQ/redrive and broker outage tests; no lost committed job | 004, 010 |
| BAP-012 | M3 / P1 | Admin product/stock/order/quoted-delivery/fulfillment and unused-exchange workflows; audited changes, pagination and concurrent update tests | 007, 008 |
| BAP-013 | M4 / P1 | Metrics/logs/traces, dashboards, alert routes, health endpoints, graceful shutdown; synthetic outage reaches designated responder | 010, 011 |
| BAP-014 | M4 / P0 | CI/CD with immutable images, single migration job, staging smoke, controlled production release and rollback; secret scanning and least-privilege credentials | 010, 013 |
| BAP-015 | M3 / P1 | Sales/exchange/payment-adjustment reports and privacy-aware funnel analytics; event deduplication, definition of revenue, timezone/filter tests, financial reconciliation | 006, 008, 012 |
| BAP-016 | M5 / P1 | Crawlable product/category pages, canonical URLs, sitemap/robots, accurate Product/Organization data, domain/HTTPS/Search Console; validation and index checks | 009, 014 |
| BAP-017 | M5 / P0 | DB backup/restore drill and documented HA gap, retention and secrets rotation runbooks; demonstrate approved RPO/RTO | 014 |
| BAP-018 | M5 / P0 | Load/soak/spike and failure tests, security review and accessibility audit; publish environment-specific capacity report | 009, 012, 013, 014 |
| BAP-019 | M6 / P2 | Kafka analytics stream, per-destination outbox, schema versioning, retention and replay; aggregates reproduce without duplicate revenue | 011, 015 |
| BAP-020 | M6 / P2 | Local Kubernetes manifests, ingress/TLS, probes/resources/HPA, secret handling, rollout/pod-loss drills; decide production promotion from evidence | 013, 014 foundation |
| BAP-021 | M5 / P0 | Merchant live payment verification, launch checklist, runbooks/admin guide, monitoring ownership and rollback rehearsal; release sign-off | 016, 017, 018 |

IDs in Dependencies omit the BAP prefix for readability. Kafka and Kubernetes (019/020) are required December deliverables. Their production promotion depends on verified hosting and operational feasibility; local integration/recovery evidence remains mandatory.

## Issue execution template

Each tracker issue should include: problem and user outcome; scope/exclusions; dependencies; proposed API/data/UI change; acceptance criteria; test scenarios; observability; security/privacy implications; migration and rollback; estimate; responsible owner; links to PR and evidence. Split issues larger than five focused days into reviewable child issues after design. Use labels for milestone, area, priority and status.

Workflow: Backlog → Ready (decisions/dependencies resolved) → In progress → Review → Verification → Done. Work in progress limit: one primary implementation issue per developer. Conduct weekly review of blocked decisions, estimates, incidents and scope. Do not call a ticket complete while its required evidence is missing.

## First execution sequence

Resolve BAP-001, finalize BAP-002, then implement BAP-010 and BAP-003. Establish CI/database integration evidence before changing payment persistence. Next implement BAP-004/005/006 together as small dependent changes, then durable workers/reconciliation. Cosmetic redesign must not delay fixing duplicate-payment risks.
