# R23 Evidence: Observability, Dashboards, Alerts and Incident Runbooks

**Issue:** [#29 - [R23] Deliver dashboards, traces, alerts and incident runbooks](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/29)  
**Milestone:** Week 5: Business operations and visibility  
**Area:** Observability / SRE / Platform  
**Status:** Completed and verified locally against unit tests, synthetic incident drills, and static PII audits.  

---

## 1. Problem and Outcome

Bliss Atelier Perfumes requires robust, zero-leak operational visibility and incident response capabilities across the entire e-commerce and administrative stack. Previously, operational signals were limited to standard container survival checks without continuous telemetry, structured alerting, or actionable operational runbooks.

### Key Operational Invariants Enforced:
1. **Strict Bounded-Cardinality Metrics**:
   - Zero high-cardinality values permitted in metric labels. Dynamic identifiers (order IDs `ord_...`, user IDs `usr_...`, customer IP addresses, guest tokens `gt_...`) are strictly prohibited in metric labels.
   - Route paths are normalized to bounded templates (e.g. `/api/orders/{id}/payment-status`, `/api/admin/users/{id}/role`). Any unmapped path collapses into `/unmapped`.
2. **Total PII & Secret Redaction**:
   - Customer phone numbers (`+254...`, `07...`), addresses, session tokens, and webhook secrets are redacted across all logs, traces, and metrics.
3. **Correlation ID Continuity**:
   - Every inbound HTTP request receives or preserves an `X-Correlation-ID` header. This identifier is attached to `context.Context`, injected into structured `slog` attributes, and propagated into outbox event envelopes and message queue headers.
4. **Lightweight, Zero-Dependency Prometheus Exposition (`GET /metrics`)**:
   - Standard Prometheus 0.0.4 text exposition format served on `GET /metrics` with zero external Go module overhead.
   - Dynamic collectors sample PostgreSQL connection pool stats (`pgxpool.Stat()`) and outbox queue depths on-demand during scrapes without background polling waste.
5. **Actionable Alerting Rules Linked to Runbooks**:
   - Every alert definition in `infra/observability/prometheus/alerts.yml` includes an explicit, validated `runbook_url` pointing to an operational procedure in `docs/runbooks/`.
6. **Isolated Docker Compose Profile (`observability`)**:
   - Core development (`docker compose up -d`) remains lean and lightweight, while `docker compose --profile observability up -d` launches Prometheus, Alertmanager, and Grafana on demand.

---

## 2. Architecture & Telemetry Data Flow

```mermaid
flowchart TD
    subgraph Client Traffic
        Web["Frontend / Shoppers / Staff"] -->|"HTTP Request (X-Correlation-ID)"| API["Go HTTP API (cmd/api)"]
    end

    subgraph Instrumentation Layer
        API -->|"HTTP Middleware"| Tel["internal/telemetry"]
        API -->|"Pool Stats Hook"| PGPool["PostgreSQL pgxpool"]
        API -->|"Cache / Rate Limit Events"| Redis["Redis 7"]
        API -->|"Payment Attempts & Exceptions"| Reconciler["Payment Reconciler"]
        API -->|"Outbox Pending Depth"| OutboxStore["Transactional Outbox"]
        Worker["RabbitMQ Worker"] -->|"DLQ / Message Acks"| Tel
        PGPool -.->|"On Scrape"| Tel
        Redis -.-> Tel
        Reconciler -.-> Tel
        OutboxStore -.-> Tel
    end

    subgraph Monitoring Stack (Profile: observability)
        Prom["Prometheus (prom/prometheus:v2.53.0)"] -->|"Scrapes GET /metrics (every 5s)"| API
        Prom -->|"Evaluates alerts.yml"| Alertmanager["Alertmanager (prom/alertmanager:v0.27.0)"]
        Prom -->|"Metrics Datasource"| Grafana["Grafana (grafana/grafana:11.1.0)"]
    end

    subgraph Incident Response & Recovery
        Alertmanager -->|"Dispatches Webhook Alert"| ResponderSink["Designated Responder (Synthetic Drill Sink)"]
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

Automated verification was executed via [`scripts/test-synthetic-incident-drills.ps1`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/scripts/test-synthetic-incident-drills.ps1) and `internal/telemetry/drill_test.go`:

```
=================================================================
  BLISS ATELIER: OBSERVABILITY & INCIDENT DRILL VERIFICATION    
=================================================================

[1/5] Verifying Incident Runbooks in docs/runbooks/...
  ✓ Found runbook: docs/runbooks/runbook-payment-reconciliation-failure.md (3875 bytes)
  ✓ Found runbook: docs/runbooks/runbook-dlq-poison-messages.md (3266 bytes)
  ✓ Found runbook: docs/runbooks/runbook-database-pool-exhaustion.md (2845 bytes)
  ✓ Found runbook: docs/runbooks/runbook-cache-and-rate-limit-outage.md (2544 bytes)
  ✓ Found runbook: docs/runbooks/runbook-api-error-surge.md (2805 bytes)

[2/5] Verifying Prometheus Alerting Rules & Runbook Linkage...
  ✓ Configured Alerts (8): ApiHigh5xxRate, ApiHighLatency, DbPoolSaturation, UnresolvedPaymentSpike, PaymentExceptionDetected, DlqPoisonMessagesDetected, OutboxBacklogGrowing, RedisOutageFallbackActive
  ✓ All alerting rules have verified runbook_url destinations

[3/5] Verifying Provisioned Grafana Dashboards...
  ✓ Dashboard: 'Bliss Atelier - System & API Overview' (6 panels)
  ✓ Dashboard: 'Bliss Atelier - Financial & Payment Operations' (4 panels)
  ✓ Dashboard: 'Bliss Atelier - Messaging, Outbox & SRE' (5 panels)

[4/5] Executing Go Synthetic Incident Drill Suite...
    === RUN   TestSyntheticIncidentDrill_ReachesDesignatedResponder
    --- PASS: TestSyntheticIncidentDrill_ReachesDesignatedResponder (0.01s)
    PASS
    ok  	github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/telemetry	2.022s
  ✓ Synthetic incident drill executed successfully and reached designated responder

[5/5] Auditing Telemetry for PII Redaction & Label Cardinality...
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

## 8. Definition of Done & Sign-Off

- [x] All unit and integration tests passing (`go test -v ./...`).
- [x] Telemetry endpoint `GET /metrics` emits valid Prometheus 0.0.4 text format under 2ms.
- [x] Zero high-cardinality labels and zero PII verified via automated audit.
- [x] `compose.yaml` profile `observability` config validated.
- [x] Synthetic incident drills verified reaching designated responder webhook.
- [x] 5 operational runbooks delivered and cross-referenced in alert annotations.
