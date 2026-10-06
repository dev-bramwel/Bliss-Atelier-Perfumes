# Test strategy and release gates

Status: proposed. Existing tests cover selected pure pricing and M-Pesa helpers only. Preserve them; add behavior tests against real infrastructure for transactions and delivery semantics.

## Layers and scenarios

| Layer | Required evidence |
| --- | --- |
| Unit | Money calculations, delivery fees, validation, payment state machine, stock transitions, analytics definitions |
| API integration | Real disposable PostgreSQL with migrations; input failures, auth/RBAC, scoped order lookup, idempotency conflict and simultaneous requests |
| Payment contract | Fake Daraja server; token expiry, rejection, malformed response, timeout before/after acceptance, callback before mapping save, duplicates, late failure, missing callback and query reconciliation |
| Data concurrency | Concurrent last-item purchase, reservation expiry versus successful payment, concurrent payment retries, unique receipt, crash before/after commit |
| Messaging | Real Redis/RabbitMQ; outbox publish/ack crash windows, redelivery, poison event, DLQ/redrive, cache failure; Kafka replay if enabled |
| Browser E2E | Search/filter/detail/cart, guest checkout, delivery/pickup, pay later/pay now, network retry, failure, timeout, resumed order; admin role and fulfillment flows |
| Accessibility | Keyboard, labels/errors, focus trap/restore, screen reader payment states, contrast, touch targets and 320px layout; automate and manually verify |
| Security | Ownership bypass, privilege escalation, CSRF/session behavior, rate-limit abuse, unsafe rendering, secret/PII leakage, dependency/container findings |
| Recovery | DB restore, app/worker restart, broker outage, pod loss, migration failure, release rollback and payment reconciliation after downtime |
| SEO | Server HTML contains product content and links; correct canonical/status/robots/sitemap/schema; staging blocked from indexing |

No automated live charges. Sandbox smoke tests supplement a deterministic fake provider; authorized supervised merchant checks establish live integration evidence.

## CI requirements

PR: clean dependency install, formatting/lint, unit/API tests, schema generation/migration check, deterministic browser smoke and artifact capture. Nightly/staging: full browser matrix, broker failure, security and representative load. Release: all blocking gates plus restore/rollback and sandbox evidence. Add meaningful branch coverage targets for payment/inventory invariants rather than treating a global percentage as proof. Tests must assert user/business behavior and persisted effects.

## Performance protocol

Confirm the traffic model in scope.md first. Seed representative catalog, orders/payment attempts and analytics history (provisional 50,000 customers and 500,000 orders); anonymized synthetic data only. Run from a separate load generator and record its saturation as well as application resources.

Use k6 or equivalent: warm-up, 30-minute steady run at approved peak, ten-minute 3x burst, two-hour soak, then failure/recovery tests. Cover catalog, order lookup/polling, checkout writes, callback and admin reads. Provisional API mix: 70% reads, 20% status polls, 5% order writes, 3% simulated callbacks, 2% admin; adjust from expected business traffic. Also benchmark static/CDN delivery separately. Distinguish attempted requests, achieved throughput, concurrent sessions and completed orders.

Stub Daraja in load tests and verify rate-limit/backpressure behavior; never load-test Safaricom without provider authorization. Test cold/warm cache and broker/cache outage. Record provider latency separately from internal latency.

Provisional gates: read p95 under 300ms, internal order persistence p95 under 700ms, unexpected 5xx under 0.5%, no duplicate charges or oversells, bounded queue age and DB connections, no resource exhaustion. External initiation latency gets its own budget after measurement. Mobile performance target: LCP ≤2.5s, INP ≤200ms, CLS ≤0.1 under agreed conditions; synthetic tests are initial evidence, real-user percentiles validate after launch.

Capacity report includes commit, date, environment/cost, requests and data mix, durations, percentiles, errors, DB/queue/cache metrics, bottlenecks, approved capacity and limitations. A passing run does not certify arbitrary 50,000-user traffic.

## Release blockers

Any reproducible duplicate payment/order from a same-key retry, unauthorized administrative mutation, paid-status regression, overselling, lost committed work, untested restore, exposed secrets, missing actionable monitoring or unmet approved load target blocks release. Lower-severity exceptions require explicit owner acceptance and a dated follow-up issue.
