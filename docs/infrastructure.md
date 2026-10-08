# Infrastructure and evolution to microservices

Status: target design. Modular Go monolith is the initial application architecture. Hosting, capacity and production topology are not yet established.

## Initial deployment units

One Go module and one release train. API, worker and migration commands share business packages and versioned artifacts. Separately running workers improve scheduling/isolation without making them independently owned microservices. PostgreSQL is authoritative; Redis handles cache/limits; RabbitMQ handles durable jobs; Kafka handles replayable analytics. Their roles do not overlap by accident.

Prefer same-origin frontend/API HTTP routing; public TLS endpoint exposes storefront/API/provider callback. Database, Redis, brokers and metrics stay private. Admin authentication and metrics access remain protected. Kubernetes local namespaces separate environments but are not sufficient tenant/security isolation by themselves.

## Module ownership

| Module | Owns | Boundary |
| --- | --- | --- |
| catalog | Products/prices/images | Product lookup service |
| inventory | Stock movements/reservations/exchange quarantine | Reserve/release/inspect use cases |
| orders | Checkout snapshots/delivery quotes/order lifecycle | Coordinates checkout transaction |
| payments | Attempts/provider correlation/reconciliation | Typed provider port and durable events |
| admin | Identity/roles/audit access | Authorizes use cases, no direct stock bypass |
| analytics | Read models and report definitions | Consumes settled events; cannot mutate payments |

Modules depend on explicit Go service contracts. Avoid cyclic imports and cross-module repository access. A checkout coordinator may intentionally perform a local DB transaction across order/reservation modules; document that boundary so extraction does not accidentally remove atomicity. No internal HTTP between modules solely to mimic microservices.

## Resource and failure model

Bound DB pools across all replicas/workers to fit database max connections. Backpressure jobs and provider calls; Redis/Kafka outage must not erase persisted payments. Outbox carries durable work, with independent destination acknowledgment and retry state. Rate-limit/session failure policy is security-aware; public cache reads may fall back with protection against DB overload.

Local Kubernetes includes Deployment/Service, ingress, probes, resources, NetworkPolicy if supported by selected CNI, controlled secret injection, rollout and termination tests. Autoscaling requires metrics and enough node capacity. A local single-node cluster cannot establish production HA. Managed stateful services remain a future option constrained by budget; do not promise zero-cost durable public infrastructure.

## Extraction criteria

Extract a module only after recording evidence of at least one concrete benefit: measured independent resource bottleneck that monolith scaling cannot economically resolve, genuinely separate team/release ownership, or a needed security/failure-isolation boundary. Also require stable API/events, operational capacity, cost plan and automated failure/contract testing. User count alone and tool familiarity are insufficient criteria.

Analytics or notifications are lower-coupling candidates. Keep order/inventory/payment correctness together until compensating workflows are designed. Extraction ADR must state new source of truth and ownership, contract/versioning, loss/duplicate/reorder handling, data migration, tracing, operational owner, measured success criterion and rollback.

## Extraction sequence

1. Isolate module internals and establish contract/invariant tests in monolith.
2. Create independently owned schema/store and backfill with checkpointed CDC/outbox events. Reconcile counts and domain totals; no naive dual writes.
3. Run read-only shadow consumers, compare results and measure lag; never shadow-initiate payments.
4. Cut over one responsibility at a time with a single writer; route consumers through explicit contract.
5. Prove failure/retry/compensation and rollback. Remove legacy coupling only after stability window.

Distributed changes across services use explicit saga/compensation where appropriate, with no claim that a broker gives database-wide exactly-once semantics. Kafka does not replace the financial ledger.
