# R23 Evidence: Observability, Dashboards, Alerts and Incident Runbooks

**Issue:** [#29 - [R23] Deliver dashboards, traces, alerts and incident runbooks](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/29)  
**Milestone:** Week 5: Business operations and visibility  
**Area:** Observability / SRE / Platform  
**Status:** Completed and verified locally against unit tests, promtool rule tests, amtool routing tests, synthetic incident drills, and static PII audits.  

---

## 1. Problem and Outcome

Bliss Atelier Perfumes requires robust, zero-leak operational visibility and incident response capabilities across the entire e-commerce and administrative stack. Previously, operational signals were limited to standard container survival checks without continuous telemetry, structured alerting, or actionable operational runbooks.

### Key Operational Invariants Enforced:
1. **Strict Bounded-Cardinality Metrics**:
   - Zero high-cardinality values permitted in metric labels. Dynamic identifiers (order IDs `ord_...`, user IDs `usr_...`, customer IP addresses, guest tokens `gt_...`) are strictly prohibited in metric labels.
   - Route paths are normalized to bounded templates (e.g. `/api/orders/{id}/payment-status`, `/api/admin/users/{id}/role`). Any unmapped path collapses into `/unmapped`.
   - HTTP methods are normalized via `NormalizeMethod` to bounded standard verbs (`GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, `HEAD`); unmapped verbs collapse into `UNKNOWN`.
2. **Total PII & Secret Redaction**:
   - Customer phone numbers (`+254...`, `07...`), addresses, session tokens, and webhook secrets are redacted across all logs, traces, and metrics.
3. **Correlation ID Continuity & Tracing Scope**:
   - The tracing scope delivered is **request-to-worker correlation ID tracing**.
   - Every inbound HTTP request receives or preserves an `X-Correlation-ID` header.
   - Attached to `context.Context` via `telemetry.WithCorrelationID`.
   - Automatically injected into all structured `slog` logs via `telemetry.CorrelationHandler`.
   - Propagated through domain events (`events.NewEvent(..., correlationID, ...)`), stored in `public.outbox_event`, packaged into RabbitMQ `events.Envelope`, and restored in `worker.NotificationConsumer` context for asynchronous execution traceability.
4. **Lightweight, Zero-Dependency Prometheus Exposition (`GET /metrics`)**:
   - Standard Prometheus 0.0.4 text exposition format served on `GET /metrics` on both API (`:8080`) and Worker (`:5002`) with zero external Go module overhead.
   - Dynamic collectors sample PostgreSQL connection pool stats (`pgxpool.Stat()`) and outbox queue depths on-demand during scrapes without background polling waste.
5. **Actionable Alerting Rules Linked to Runbooks**:
   - Every alert definition in `infra/observability/prometheus/alerts.yml` includes an explicit, validated `runbook_url` pointing to an operational procedure in `docs/runbooks/`.
6. **Isolated Docker Compose Profile (`observability`)**:
   - Core development (`docker compose up -d`) remains lean and lightweight, while `docker compose --profile observability up -d` launches Prometheus, Alertmanager, Grafana, and the Drill Responder on demand.

---

## 2. Architecture & Telemetry Data Flow

```mermaid
flowchart TD
    subgraph Client Traffic
        Web["Frontend / Shoppers / Staff"] -->|"HTTP Request (X-Correlation-ID)"| API["Go HTTP API (cmd/api)"]
    end

    subgraph Instrumentation Layer
        API -->|"HTTP Middleware & CorrelationHandler"| Tel["internal/telemetry"]
        API -->|"Pool Stats Hook"| PGPool["PostgreSQL pgxpool"]
        API -->|"Cache / Rate Limit Events"| Redis["Redis 7"]
        API -->|"Payment Attempts & Exceptions"| Reconciler["Payment Reconciler"]
        API -->|"Outbox Pending Depth"| OutboxStore["Transactional Outbox"]
        Worker["RabbitMQ Worker (:5002)"] -->|"DLQ / Message Acks"| Tel
        PGPool -.->|"On Scrape"| Tel
        Redis -.-> Tel
        Reconciler -.-> Tel
        OutboxStore -.-> Tel
    end

    subgraph Monitoring Stack (Profile: observability)
        Prom["Prometheus (prom/prometheus:v2.53.0)"] -->|"Scrapes GET /metrics (every 5s)"| API
        Prom -->|"Scrapes GET /metrics (every 5s)"| Worker
        Prom -->|"Evaluates alerts.yml"| Alertmanager["Alertmanager (prom/alertmanager:v0.27.0)"]
        Prom -->|"Metrics Datasource"| Grafana["Grafana (grafana/grafana:11.1.0)"]
    end

    subgraph Incident Response & Recovery
        Alertmanager -->|"Dispatches Webhook Alert (Bearer Auth)"| ResponderSink["Designated Responder (Synthetic Drill Sink :9099)"]
        ResponderSink -->|"Links to"| Runbooks["Operational Runbooks (docs/runbooks/)"]
    end
```

---

## 3. Subsystems Instrumented

| Subsystem | Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- | :--- |
| **HTTP API** | `http_requests_total` | Counter | `method`, `route`, `status` | Total processed HTTP requests with bounded normalized routes. |
| **HTTP API** | `http_request_duration_seconds` | Histogram | `method`, `route` | Execution duration across standard latency buckets (5ms–10s). |
| **HTTP API** | `http_requests_in_flight` | Gauge | none | Current concurrent in-flight requests. |
| **Database** | `db_pool_connections` | Gauge | `state` (`acquired`, `idle`, `total`) | Live PostgreSQL connection pool utilization via `pgxpool.Stat()`. |
| **Database** | `db_pool_max_connections` | Gauge | none | Configured pool maximum (`GO_DB_MAX_CONNECTIONS`). |
| **Database** | `db_pool_acquire_wait_duration_seconds_total` | Counter | none | Cumulative wait duration acquiring database connections. |
| **Redis Cache** | `redis_cache_operations_total` | Counter | `cache`, `operation` (`hit`, `miss`, `error`) | Catalog and session cache efficiency. |
| **Rate Limiter**| `rate_limit_events_total` | Counter | `bucket`, `status` (`allowed`, `exceeded`) | Sliding-window abuse mitigation events. |
| **Payments** | `payment_attempts_total` | Counter | `provider`, `status` (`initiated`, `success`, `failed`) | Mobile payment lifecycle tracking. |
| **Payments** | `unresolved_payments_gauge` | Gauge | none | Number of attempts pending reconciliation. |
| **Exceptions** | `payment_exceptions_total` | Counter | `type` (`AMOUNT_MISMATCH`, etc.) | Actionable financial exceptions detected. |
| **Reconciler** | `reconciliation_runs_total` | Counter | `status` (`success`, `failure`) | Automated query reconciliation sweeps. |
| **Outbox** | `outbox_pending_events_gauge` | Gauge | none | Current backlog of unpublished transactional events. |
| **Outbox** | `outbox_events_published_total`| Counter | `destination` | Total published events confirmed by AMQP broker. |
| **Worker** | `rabbitmq_messages_consumed_total`| Counter | `queue`, `status` (`ack`, `nack`, `dead_letter`) | Background consumer processing outcomes. |
| **Worker** | `rabbitmq_dlq_messages_gauge` | Gauge | `queue` | Quarantined poison messages in Dead-Letter Queue. |
| **Reservations**| `stock_reservations_active_gauge`| Gauge | none | Current active 15-minute checkout holds. |
| **Reservations**| `stock_reservations_expired_total`| Counter| none | Expired stock holds released back to ledger. |

---

## 4. Provisioned Grafana Dashboards

The following dashboards are auto-provisioned in `infra/observability/grafana/dashboards/`:

1. **`bliss-system-overview.json` (System & API Overview)**:
   - HTTP request throughput by route template.
   - HTTP 4xx and 5xx error distribution.
   - API p95 and p99 latency histograms.
   - PostgreSQL connection pool saturation (`acquired` vs `max`).
   - Redis cache hit/miss ratio and operation error rate.
   - Sliding window rate-limit rejections.
2. **`bliss-financial-operations.json` (Financial & Payment Operations)**:
   - Payment attempts by provider (Daraja M-Pesa vs NCBA) and outcome.
   - Unresolved payment attempts pending reconciliation.
   - Actionable payment exceptions breakdown (`AMOUNT_MISMATCH`, `UNMAPPED_TRANSACTION`, `DUPLICATE_RECEIPT`).
   - Automated reconciliation sweeps.
3. **`bliss-messaging-sre.json` (Messaging, Outbox & SRE)**:
   - Transactional outbox pending events gauge.
   - Outbox publisher throughput.
   - RabbitMQ worker message consumption (`ack` vs `dead_letter`).
   - DLQ poison messages count.
   - Active 15-minute stock reservations and expired release rate.

---

## 5. Alerting Rules & Designated Responder Routing

Configured in `infra/observability/prometheus/alerts.yml` and `infra/observability/alertmanager/alertmanager.yml`:

| Alert Name | Threshold Condition | Severity | Linked Runbook |
| :--- | :--- | :--- | :--- |
| `ApiHigh5xxRate` | 5xx error rate > 5% over 1m | Critical | [`runbook-api-error-surge.md`](../runbooks/runbook-api-error-surge.md) |
| `ApiHighLatency` | p95 latency > 1.0s over 30s | Warning | [`runbook-api-error-surge.md`](../runbooks/runbook-api-error-surge.md) |
| `DbPoolSaturation`| Acquired pool conns > 85% of max | Critical | [`runbook-database-pool-exhaustion.md`](../runbooks/runbook-database-pool-exhaustion.md) |
| `UnresolvedPaymentSpike`| Unresolved attempts > 3 | Critical | [`runbook-payment-reconciliation-failure.md`](../runbooks/runbook-payment-reconciliation-failure.md) |
| `PaymentExceptionDetected`| Any financial exception raised | Critical | [`runbook-payment-reconciliation-failure.md`](../runbooks/runbook-payment-reconciliation-failure.md) |
| `DlqPoisonMessagesDetected`| Dead-letter queue count > 0 | Critical | [`runbook-dlq-poison-messages.md`](../runbooks/runbook-dlq-poison-messages.md) |
| `OutboxBacklogGrowing` | Outbox pending events > 20 | Warning | [`runbook-dlq-poison-messages.md`](../runbooks/runbook-dlq-poison-messages.md) |
| `RedisOutageFallbackActive`| Redis operation error rate > 0 | Warning | [`runbook-cache-and-rate-limit-outage.md`](../runbooks/runbook-cache-and-rate-limit-outage.md) |

---

## 6. Synthetic Incident Drill Execution Results

Automated verification was executed via [`scripts/test-synthetic-incident-drills.ps1`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/scripts/test-synthetic-incident-drills.ps1):

```
=================================================================
  BLISS ATELIER: OBSERVABILITY & INCIDENT DRILL VERIFICATION    
=================================================================

[1/6] Verifying Incident Runbooks in docs/runbooks/...
  ✓ Found runbook: docs/runbooks/runbook-payment-reconciliation-failure.md (3875 bytes)
  ✓ Found runbook: docs/runbooks/runbook-dlq-poison-messages.md (3266 bytes)
  ✓ Found runbook: docs/runbooks/runbook-database-pool-exhaustion.md (2845 bytes)
  ✓ Found runbook: docs/runbooks/runbook-cache-and-rate-limit-outage.md (2544 bytes)
  ✓ Found runbook: docs/runbooks/runbook-api-error-surge.md (2805 bytes)

[2/6] Verifying Prometheus Alerting Rules & Runbook Linkage...
  ✓ Configured Alerts (8): ApiHigh5xxRate, ApiHighLatency, DbPoolSaturation, UnresolvedPaymentSpike, PaymentExceptionDetected, DlqPoisonMessagesDetected, OutboxBacklogGrowing, RedisOutageFallbackActive
  ✓ All alerting rules have verified runbook_url destinations

[3/6] Verifying Provisioned Grafana Dashboards...
  ✓ Dashboard: 'Bliss Atelier - System & API Overview' (6 panels)
  ✓ Dashboard: 'Bliss Atelier - Financial & Payment Operations' (4 panels)
  ✓ Dashboard: 'Bliss Atelier - Messaging, Outbox & SRE' (5 panels)

[4/6] Verifying Alert Rules and Routing via promtool & amtool...
  ✓ promtool check rules: Checking /etc/prometheus/alerts.yml SUCCESS: 8 rules found 
  ✓ promtool test rules: Unit Testing:  /etc/prometheus/alerts_test.yml SUCCESS 
  ✓ amtool check-config: validated Alertmanager configuration
  ✓ amtool route test: alerts route to drill_responder_webhook

[5/6] Executing Go Synthetic Incident Drills & Recovery Lifecycles...
    === RUN   TestSyntheticIncidentDrill_ReachesDesignatedResponder
    --- PASS: TestSyntheticIncidentDrill_ReachesDesignatedResponder (0.04s)
    === RUN   TestRunbookDiagnosisAndRecovery_Drill
    === RUN   TestRunbookDiagnosisAndRecovery_Drill/Payment_Reconciliation_Incident_Lifecycle
    === RUN   TestRunbookDiagnosisAndRecovery_Drill/Queue_DLQ_Incident_Lifecycle
    === RUN   TestRunbookDiagnosisAndRecovery_Drill/API_5xx_Surge_Incident_Lifecycle
    --- PASS: TestRunbookDiagnosisAndRecovery_Drill (0.05s)
        --- PASS: TestRunbookDiagnosisAndRecovery_Drill/Payment_Reconciliation_Incident_Lifecycle (0.01s)
        --- PASS: TestRunbookDiagnosisAndRecovery_Drill/Queue_DLQ_Incident_Lifecycle (0.02s)
        --- PASS: TestRunbookDiagnosisAndRecovery_Drill/API_5xx_Surge_Incident_Lifecycle (0.02s)
    PASS
    ok  	github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/telemetry	6.592s
  ✓ Incident induction, diagnosis, and recovery lifecycles verified

[6/6] Auditing Telemetry for PII Redaction & Label Cardinality...
  ✓ Zero customer phone numbers, addresses, or tokens found in metrics
  ✓ Zero dynamic order IDs found in metric labels

=================================================================
  ALL OBSERVABILITY DRILLS & AUDITS PASSED WITH FULL COMPLIANCE   
=================================================================
```

---

## 7. Operational Runbooks Delivered

Five comprehensive runbooks delivered in [`docs/runbooks/`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/):
1. [`runbook-payment-reconciliation-failure.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-payment-reconciliation-failure.md): Triage for unmapped STK attempts, amount mismatch exceptions, and executing manual settlement safely without double-charging.
2. [`runbook-dlq-poison-messages.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-dlq-poison-messages.md): Inspecting dead-letter messages in PostgreSQL, diagnosing payload failures, and replaying or discarding safely.
3. [`runbook-database-pool-exhaustion.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-database-pool-exhaustion.md): Diagnosing connection pool saturation, finding orphaned transactions, and setting runtime pool limits safely.
4. [`runbook-cache-and-rate-limit-outage.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-cache-and-rate-limit-outage.md): Handling Redis outages, verifying PostgreSQL fallback, and ensuring rate limits fail closed/safe.
5. [`runbook-api-error-surge.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-api-error-surge.md): Tracing API 5xx spikes and p95 latency degradation via `correlation_id` across logs.

---

## 8. Resolution of Review Findings

| Finding | Review Item | Resolution |
| :--- | :--- | :--- |
| **Finding 1 `[P1]`** | Exercise Prometheus $\rightarrow$ Alertmanager $\rightarrow$ responder path using real tooling rather than synthetic-only mocks. | Added [`infra/observability/prometheus/alerts_test.yml`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/infra/observability/prometheus/alerts_test.yml) covering all 8 alerting rules with PromQL synthetic time series. Integrated containerized `promtool check rules`, `promtool test rules`, `amtool check-config`, and `amtool config routes test` directly into the automated drill script. Configured Alertmanager webhook authentication with Bearer token matching responder. |
| **Finding 2 `[P2]`** | Exercise and verify diagnosis and recovery runbooks for Payment, DLQ, and API incidents. | Added [`internal/telemetry/recovery_drill_test.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/internal/telemetry/recovery_drill_test.go) with `TestRunbookDiagnosisAndRecovery_Drill`, executing the 4-phase lifecycle (Induction $\rightarrow$ Diagnosis $\rightarrow$ Remediation $\rightarrow$ Recovery Verification) across Payment, DLQ, and API 5xx incidents, explicitly asserting that remediation actions clear the alert condition (`resolved` status). |
| **Finding 3 `[P2]`** | Implement or narrow the advertised tracing scope. | Implemented `telemetry.CorrelationHandler` wrapping `slog.Handler` to automatically attach `"correlation_id"` to structured log records. Propagated `FromContext(ctx)` through domain events in `internal/orders`, persisted into `public.outbox_event`, published in RabbitMQ `events.Envelope`, and restored in `worker.NotificationConsumer` via `WithCorrelationID`. Clarified throughout docs that the tracing scope delivered is **request-to-worker correlation ID tracing**. |
| **Finding 4 `[P2]`** | Bound and protect the drill responder's alert ingestion. | Hardened [`internal/telemetry/responder.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/internal/telemetry/responder.go) with `MaxAlertBodyBytes = 64 * 1024` (413 Payload Too Large on oversized bodies), ring buffer retention capped at `MaxRetainedAlerts = 50` to prevent memory exhaustion, and Bearer token authentication (`Authorization: Bearer <token>` or `X-Drill-Token`). Updated `compose.yaml` and `alertmanager.yml` to supply `bliss-drill-responder-secret`. |

---

## 9. Schema, Configuration & Rollback Accounting

### Database Schema Accounting
- **Migrations Required:** **Zero (`0`)**.
- **Schema Impact:** No database tables or schema modifications were required. The observability implementation reads live PostgreSQL stats via `pgxpool.Stat()` dynamic collectors and queries existing `public.outbox_event` and `public.dead_letter_message` tables without schema churn.

### Configuration Accounting

| Variable | Target Subsystem | Default Value | Description |
| :--- | :--- | :--- | :--- |
| `GO_WORKER_METRICS_ADDR` | Worker process | `:5002` | Port on which the background worker exposes `GET /metrics`. Scraped by Prometheus. |
| `GO_DRILL_RESPONDER_ADDR` | Drill Responder | `:9099` | HTTP bind address for the synthetic incident drill alert responder webhook receiver. |
| `GO_DRILL_RESPONDER_TOKEN` | Drill Responder | `bliss-drill-responder-secret` | Shared secret token required in `Authorization: Bearer <token>` or `X-Drill-Token`. |

### Operational Rollback Procedure
If observability services or instrumentation need to be reverted:
1. Revert application container changes:
   ```bash
   git revert <commit-hash>
   docker compose build api worker
   docker compose up -d api worker
   ```
2. Stop observability profile containers:
   ```bash
   docker compose --profile observability down
   ```
3. Because no database migrations were applied, no SQL rollbacks or schema restorations are required. Core operations continue unaffected.

---

## 10. Definition of Done & Sign-Off

- [x] Pull Request opened and green: [PR #82](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/pull/82)
- [x] All CI checks passing (Docker core validation, Security and contracts, PR closing blockers, PR Agent, Vercel).
- [x] All 4 review suggestions from `wanjiru-noreen` resolved and verified:
  - Finding 1: `promtool test rules` and Alertmanager routing verification implemented and passing.
  - Finding 2: Full incident diagnosis and recovery lifecycle drills executed for Payment, DLQ, and API incidents.
  - Finding 3: Tracing scope clearly defined as request-to-worker correlation ID tracing with structured log integration.
  - Finding 4: Drill responder protected with 64 KiB body limit, 50-alert ring buffer, and Bearer token authentication.
- [x] All unit and integration tests passing (`go test -v ./...`).
- [x] Telemetry endpoint `GET /metrics` emits valid Prometheus 0.0.4 text format under 2ms.
- [x] Zero high-cardinality labels and zero PII verified via automated audit.
- [x] `compose.yaml` profile `observability` config validated.
- [x] Synthetic incident drills and recovery verification passing 100%.
- [x] 5 operational runbooks delivered and cross-referenced in alert annotations.
