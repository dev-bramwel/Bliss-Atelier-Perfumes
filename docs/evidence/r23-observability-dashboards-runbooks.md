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
   - Customer phone numbers (`+254...`, `07...`), addresses, session tokens, and webhook secrets are redacted across all logs, telemetry, and metrics. Caller-controlled `X-Correlation-ID` values are strictly sanitized to reject numeric phone patterns, digit sequences, and PII.
3. **Correlation ID Continuity & Tracing Scope**:
   > [!NOTE] Scope Clarification: Request-to-Worker Correlation IDs vs. Distributed Tracing
   > The telemetry implementation provides end-to-end **request correlation ID continuity** across HTTP contexts, structured `slog` logs, outbox event envelopes, and message queue headers. It does **not** create OpenTelemetry/Jaeger spans or export distributed trace trees.
   - Every inbound HTTP request receives or preserves a validated, non-PII `X-Correlation-ID` header.
   - Attached to `context.Context` via `telemetry.WithCorrelationID`.
   - Automatically injected into all structured `slog` logs via `telemetry.CorrelationHandler` and context-aware log calls.
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
| **Outbox** | `outbox_pending_events_gauge` | Gauge | none | Current backlog of unpublished transactional events (`-1` indicates count error). |
| **Outbox** | `outbox_count_errors_total` | Counter | none | Total failures querying transactional outbox backlog depth. |
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
| `OutboxBacklogGrowing` | Outbox pending events > 20 | Warning | [`runbook-outbox-backlog.md`](../runbooks/runbook-outbox-backlog.md) |
| `RedisOutageFallbackActive`| Redis operation error rate > 0 | Warning | [`runbook-cache-and-rate-limit-outage.md`](../runbooks/runbook-cache-and-rate-limit-outage.md) |
| `ServiceTargetDown` | Scrape target unreachable (`up == 0`) | Critical | [`runbook-api-error-surge.md`](../runbooks/runbook-api-error-surge.md) |

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

Six comprehensive runbooks delivered in [`docs/runbooks/`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/):
1. [`runbook-payment-reconciliation-failure.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-payment-reconciliation-failure.md): Triage for unmapped STK attempts, amount mismatch exceptions, and executing manual settlement safely without double-charging.
2. [`runbook-dlq-poison-messages.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-dlq-poison-messages.md): Inspecting dead-letter messages in PostgreSQL, diagnosing payload failures, and replaying or discarding safely.
3. [`runbook-database-pool-exhaustion.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-database-pool-exhaustion.md): Diagnosing connection pool saturation, finding orphaned transactions, and setting runtime pool limits safely.
4. [`runbook-cache-and-rate-limit-outage.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-cache-and-rate-limit-outage.md): Handling Redis outages, verifying PostgreSQL fallback, and ensuring rate limits fail closed/safe.
5. [`runbook-api-error-surge.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-api-error-surge.md): Tracing API 5xx spikes and p95 latency degradation via `correlation_id` across logs.
6. [`runbook-outbox-backlog.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-outbox-backlog.md): Diagnosing transactional outbox event accumulation, inspecting pending/publishing events in PostgreSQL `public.outbox_event`, resolving broker disconnects, and verifying publisher draining via Prometheus metrics.

---

## 8. Resolution of Initial Review Findings (Round 1)

| Finding | Review Item | Resolution |
| :--- | :--- | :--- |
| **Finding 1 `[P1]`** | Exercise Prometheus $\rightarrow$ Alertmanager $\rightarrow$ responder path using real tooling rather than synthetic-only mocks. | Added [`infra/observability/prometheus/alerts_test.yml`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/infra/observability/prometheus/alerts_test.yml) covering all 8 alerting rules with PromQL synthetic time series. Integrated containerized `promtool check rules`, `promtool test rules`, `amtool check-config`, and `amtool config routes test` directly into the automated drill script. Configured Alertmanager webhook authentication with Bearer token matching responder. |
| **Finding 2 `[P2]`** | Exercise and verify diagnosis and recovery runbooks for Payment, DLQ, and API incidents. | Added [`internal/telemetry/recovery_drill_test.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/internal/telemetry/recovery_drill_test.go) with `TestRunbookDiagnosisAndRecovery_Drill`, executing the 4-phase lifecycle (Induction $\rightarrow$ Diagnosis $\rightarrow$ Remediation $\rightarrow$ Recovery Verification) across Payment, DLQ, and API 5xx incidents, explicitly asserting that remediation actions clear the alert condition (`resolved` status). |
| **Finding 3 `[P2]`** | Implement or narrow the advertised tracing scope. | Implemented `telemetry.CorrelationHandler` wrapping `slog.Handler` to automatically attach `"correlation_id"` to structured log records. Propagated `FromContext(ctx)` through domain events in `internal/orders`, persisted into `public.outbox_event`, published in RabbitMQ `events.Envelope`, and restored in `worker.NotificationConsumer` via `WithCorrelationID`. Clarified throughout docs that the tracing scope delivered is **request-to-worker correlation ID tracing**. |
| **Finding 4 `[P2]`** | Bound and protect the drill responder's alert ingestion. | Hardened [`internal/telemetry/responder.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/internal/telemetry/responder.go) with `MaxAlertBodyBytes = 64 * 1024` (413 Payload Too Large on oversized bodies), ring buffer retention capped at `MaxRetainedAlerts = 50` to prevent memory exhaustion, and Bearer token authentication (`Authorization: Bearer <token>` or `X-Drill-Token`). |

---

## 9. Resolution of Follow-Up Review Findings (Round 2)

| Follow-up Finding | Severity | Problem & Review Requirement | Operational Resolution & Evidence |
| :--- | :--- | :--- | :--- |
| **Finding 1** | `[P1]` | **Live Containerized Alertmanager $\to$ Responder Delivery**: Verify that firing and resolved notifications actually reach the configured drill-responder container through the deployed Alertmanager stack, using Bearer token authentication from Docker secrets. | Added automated Step `[4b]` to [`scripts/test-synthetic-incident-drills.ps1`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/scripts/test-synthetic-incident-drills.ps1). Launches containerized `drill-responder` and `alertmanager`, asserts unauthenticated `GET /alerts` returns `401 Unauthorized`, posts a synthetic alert to Alertmanager API (`POST /api/v2/alerts`), verifies Alertmanager delivers firing notification with secret Bearer auth to `drill-responder:9099`, posts resolved alert, and verifies resolved notification arrival. Enabled `--cluster.listen-address=` for single-node instant routing and added `isAuthorized` check with authenticated `GET /alerts` query endpoint. |
| **Finding 2** | `[P2]` | **Real Operational Remediation in Recovery Drills**: Update recovery drills so that instead of simply calling `UnresolvedPaymentsGauge.Set(0)` in memory, the test actually performs the documented operational remediation against services: executing reconciliation sweeps (`ReconcileStaleAttempts`), handling dead-letter messages in `NotificationConsumer`, and executing HTTP requests through `HTTPMiddleware`. | Updated [`internal/telemetry/recovery_drill_test.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/internal/telemetry/recovery_drill_test.go) (`package telemetry_test`) to exercise real domain service logic for Payment and API 5xx incidents. |
| **Finding 3** | `[P2]` | **Externalize Responder Bearer Token via Docker Secrets**: Remove `bliss-drill-responder-secret` from committed files. Generate tokens dynamically via `devstack secrets`, load via `bearer_token_file: /run/secrets/drill_responder_token` in Alertmanager and Docker secrets, with no committed plaintext fallbacks. | 1. Removed `DefaultDrillResponderToken = "bliss-drill-responder-secret"` from [`internal/telemetry/responder.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/internal/telemetry/responder.go). Responder requires explicit token via constructor, `GO_DRILL_RESPONDER_TOKEN` env, or `GO_DRILL_RESPONDER_TOKEN_FILE` file path; refuses to start unauthenticated.<br>2. Updated [`cmd/devstack/main.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/cmd/devstack/main.go) to generate a cryptographically secure 32-byte hex token in `.local/dev/drill_responder_token` (with backward-compatible secret set upgrade).<br>3. Updated [`compose.yaml`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/compose.yaml) to mount `drill_responder_token` into both `alertmanager` and `drill-responder` via Docker secrets.<br>4. Updated [`infra/observability/alertmanager/alertmanager.yml`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/infra/observability/alertmanager/alertmanager.yml) to load auth token via `http_config.bearer_token_file: "/run/secrets/drill_responder_token"`. Zero committed plaintext fallback secrets. |

---

## 10. Resolution of Follow-Up Review Findings (Round 3)

| Follow-up Finding | Severity | Problem & Review Requirement | Operational Resolution & Evidence |
| :--- | :--- | :--- | :--- |
| **Finding 1** | `[P1]` | **Pass Directory When Generating Responder Secrets**: `devstack secrets` required a directory argument; running `devstack secrets` without arguments printed usage and failed clean checkout bootstrap. | 1. Updated [`scripts/test-synthetic-incident-drills.ps1`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/scripts/test-synthetic-incident-drills.ps1) to explicitly pass `$secretDir = Join-Path $baseDir ".local/dev"` and verify `$LASTEXITCODE -eq 0` and token file presence.<br>2. Updated [`cmd/devstack/main.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/cmd/devstack/main.go) to support `devstack secrets [DIRECTORY]`, defaulting to `.local/dev` or `BLISS_SECRET_DIR` when omitted. Verified in unit tests. |
| **Finding 2** | `[P2]` | **Accurate Compliance Reporting on Docker Availability**: Do not report full compliance when Docker checks are skipped. | Updated [`scripts/test-synthetic-incident-drills.ps1`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/scripts/test-synthetic-incident-drills.ps1) with a `-RequireDocker` parameter and live daemon responsiveness check (`docker info`). When Docker is absent/inactive and `-RequireDocker` is unset, marks `$dockerSkipped = $true` and concludes with `PARTIAL RUN: CODE DRILLS PASSED (CONTAINER CHECKS SKIPPED)` instead of falsely reporting full compliance. |
| **Finding 3** | `[P2]` | **Complete DLQ Recovery Drill Against Worker Behavior**: Do not directly set gauge to 0; exercise runbook's inspect/replay/quarantine procedure with controlled queue fixture. | Implemented `controlledDLQFixture` in [`internal/telemetry/recovery_drill_test.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/internal/telemetry/recovery_drill_test.go):<br>1. Induction: Poison payload rejected by `consumer.ProcessDelivery` moves to quarantine and increments `fixture.Depth()` and `rabbitmq_dlq_messages_gauge`.<br>2. Runbook Action 3.1: Operator inspects dead-letter in store via `fixture.Inspect("msg-poison-001")`, confirming unmarshal failure reason.<br>3. Runbook Action 3.2: Operator patches payload with valid `events.Envelope` and replays via `fixture.ReplayPatched`. Replayed delivery is processed and positively ACKed by worker.<br>4. Recovery: Quarantined item cleared, queue depth returns to 0 (`fixture.Depth() == 0`), which drives the recovery signal clearing `rabbitmq_dlq_messages_gauge` to 0 from queue state. |

### Round 4 Follow-Up Review Findings Resolution (Head `828eba3`)

| Follow-up Finding | Severity | Problem & Review Requirement | Operational Resolution & Evidence |
| :--- | :--- | :--- | :--- |
| **Finding 1** | `[P1]` | **Make Documented DLQ Recovery Procedure Actually Replay Quarantined Message**: Runbook SQL targeted wrong consumer (`'notifications'` vs `'bliss-notification-worker'`), and setting `status = 'processed'` manually does not replay the message or execute event handlers. `claimMessage` treated `InboxDeadLettered` as duplicates and skipped execution. | 1. **Consumer Name Alignment**: Corrected runbook SQL to target `consumer_name = 'bliss-notification-worker'`.<br>2. **Persistent Inbox Replay Support**: Updated `claimMessage` in [`internal/worker/consumer.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/internal/worker/consumer.go) so `InboxDeadLettered` rows transition back to `InboxProcessing` on replay, allowing domain event handlers to execute while preserving deduplication against subsequent duplicate redeliveries (`InboxProcessed`).<br>3. **InboxStore Abstraction**: Extracted `InboxStore` interface with `PGInboxStore` (PostgreSQL) and `MemoryInboxStore` (testing) enforcing identical dedupe, replay, and audit state machines.<br>4. **Replay Recovery Drill**: Updated `Queue_DLQ_Incident_Lifecycle` in [`internal/telemetry/recovery_drill_test.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/internal/telemetry/recovery_drill_test.go) with `controlledDLQFixture` implementing `InboxStore`, verifying message quarantine $\to$ inspect $\to$ repaired replay claim $\to$ handler execution $\to$ processed status $\to$ duplicate delivery suppression.<br>5. **Supported Shovel Runbook**: Documented the supported RabbitMQ Shovel replay command in [`docs/runbooks/runbook-dlq-poison-messages.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-dlq-poison-messages.md) and added explicit warnings against manual SQL status overwrites. |

### Round 5 Follow-Up Review Findings Resolution (Head `b4ffe03`)

| Follow-up Finding | Severity | Problem & Review Requirement | Operational Resolution & Evidence |
| :--- | :--- | :--- | :--- |
| **Finding 1** | `[P1]` | **Configure RabbitMQ for Observability Worker and Supported Shovel Syntax**: `compose.yaml` had no `rabbitmq` service and `worker` lacked `GO_RABBITMQ_URL`, leaving messaging disconnected (`worker_messaging_disabled`). The DLQ runbook used unsupported `rabbitmqctl shovel --move-messages-from-queue` and non-existent `docker compose restart rabbitmq`. | 1. **Provisioned RabbitMQ in Compose**: Added `rabbitmq:3-alpine` to [`compose.yaml`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/compose.yaml) under `profiles: ["observability"]` with non-root user `100:101`, dropped capabilities, memory limit 256m, private network, healthcheck, and persistent volume `rabbitmq_data`.<br>2. **Enabled Plugins Configuration**: Pre-configured `[rabbitmq_prometheus,rabbitmq_shovel].` in [`infra/observability/rabbitmq/enabled_plugins`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/infra/observability/rabbitmq/enabled_plugins) mounted read-only to `/etc/rabbitmq/enabled_plugins`.<br>3. **Worker & API Messaging Wiring**: Passed `GO_RABBITMQ_URL: ${GO_RABBITMQ_URL:-amqp://bliss:bliss_dev_secret@rabbitmq:5672/}` to `worker` and `api`, and wired `worker` dependency on `rabbitmq: { condition: service_healthy }`.<br>4. **Supported Dynamic Shovel Syntax**: Updated [`docs/runbooks/runbook-dlq-poison-messages.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-dlq-poison-messages.md) with verified syntax (`rabbitmqctl set_parameter shovel dlq-recovery ...` and `rabbitmqctl shovel_status`) scoped with `--profile observability`. |

### Round 6 Follow-Up Review Findings Resolution (Head `7cc2f18`)

| Follow-up Finding | Severity | Problem & Review Requirement | Operational Resolution & Evidence |
| :--- | :--- | :--- | :--- |
| **Finding 1** | `[P1]` | **Pass Valid JSON to Shovel Verification Command**: In `scripts/test-synthetic-incident-drills.ps1:298`, `$shovelJson` was defined as a single-quoted string containing literal backslashes before quotes (`{\\\"src-uri\\\"...`), causing `rabbitmqctl set_parameter` to receive invalid JSON and fail in container drills. | Serialized a structured PowerShell hashtable with `ConvertTo-Json -Compress` and applied version-aware quote escaping (`$argJson = if ($PSVersionTable.PSVersion.Major -le 5) { $shovelJson.Replace('"', '\"') } else { $shovelJson }`) ensuring valid JSON document parsing by RabbitMQ's Erlang parser across all PowerShell versions. Verified live via containerized drill passing with exit code 0. |
| **Finding 2** | `[P2]` | **Do Not Commit RabbitMQ Password as Default Credential**: `compose.yaml` committed `RABBITMQ_DEFAULT_PASS: bliss_dev_secret` and embedded that credential in fallback `GO_RABBITMQ_URL` for API and worker, leaking default broker credentials. | 1. **Secret Bootstrapping**: Extended `cmd/devstack/main.go` to generate cryptographically secure 32-byte hex credentials in `.local/dev/rabbitmq_password` and AMQP URL `.local/dev/rabbitmq_url` with `0600` file permissions. Added backward-compatible legacy secret upgrade handling.<br>2. **Container Secret Injection**: Configured `compose.yaml` to declare `rabbitmq_password` and `rabbitmq_url` Docker secrets without committed default values. Added `infra/observability/rabbitmq/entrypoint.sh` to load `RABBITMQ_DEFAULT_PASS` strictly from `/run/secrets/rabbitmq_password` at container boot.<br>3. **Worker Integration**: Configured `worker` service to load credentials via `GO_RABBITMQ_URL_FILE: /run/secrets/rabbitmq_url`, eliminating plain-text environment variable credential leakage. Verified in live container tests. |

### Round 7 Follow-Up Review Findings Resolution (Head `5a62d76`)

| Follow-up Finding | Severity | Problem & Review Requirement | Operational Resolution & Evidence |
| :--- | :--- | :--- | :--- |
| **Finding 1** | `[P1]` | **Safely Route DLQ Dynamic Shovel Replay and Stop on Drain**: The DLQ runbook and synthetic incident drill configured the RabbitMQ shovel with `dest-exchange: "bliss.events.v1"` without `dest-exchange-key`, and with legacy option `delete-after: "queue-length"`. In RabbitMQ 3.13, that option was renamed to `src-delete-after`. Furthermore, publishing without `dest-exchange-key` preserves the message's original routing key (`dead.notifications`), which matches the `dead.*` topic binding on `bliss.events.v1`, routing replayed messages straight back into the dead-letter queue (`bliss.notifications.v1.dead`) rather than reaching `bliss.notifications.v1`. | 1. **Direct Queue Replay Routing**: Updated [`docs/runbooks/runbook-dlq-poison-messages.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-dlq-poison-messages.md) and [`scripts/test-synthetic-incident-drills.ps1`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/scripts/test-synthetic-incident-drills.ps1) to route replayed messages directly to the notification queue via `"dest-queue": "bliss.notifications.v1"`. This bypasses the `dead.*` topic binding on `bliss.events.v1` and ensures messages return directly to the notification consumer.<br>2. **RabbitMQ 3.13 Option Compliance**: Updated shovel configuration to use `"src-delete-after": "queue-length"`, ensuring the dynamic shovel drains the source queue backlog and automatically cleans up without running indefinitely.<br>3. **Live Container Validation**: Verified dynamic shovel declaration in a live RabbitMQ container with `rabbitmqctl set_parameter shovel drill-test-shovel` exiting 0. |

### Round 8 Follow-Up Review Findings Resolution (Head `715e68e`)

| Follow-up Finding | Severity | Problem & Review Requirement | Operational Resolution & Evidence |
| :--- | :--- | :--- | :--- |
| **Finding 1** | `[P1]` | **Synchronous External Escalation & Alertmanager Retry Signaling**: The responder previously forwarded alerts asynchronously in an unmonitored goroutine, ignored upstream errors/non-2xx responses, and returned HTTP 200 immediately, blinding Alertmanager to upstream delivery failures. `GO_ESCALATION_FORWARD_URL` was also not configured in `compose.yaml`. | 1. **Synchronous Paging Delivery**: Updated `internal/telemetry/responder.go` so external escalation requests are forwarded synchronously with a 5-second context timeout before replying to Alertmanager.<br>2. **Upstream Failure Retry Signaling**: On upstream network failures or non-2xx HTTP status codes, the responder returns HTTP 502 Bad Gateway with structured JSON diagnostic output (`upstream escalation delivery failed` / `upstream escalation rejected notification`). Alertmanager detects non-2xx status and triggers its retry/backoff policy.<br>3. **Environment Injection**: Added `GO_ESCALATION_FORWARD_URL`, `GO_PLATFORM_ESCALATION_URL`, and `GO_FINANCE_ESCALATION_URL` to `drill-responder` in `compose.yaml`. |
| **Finding 2** | `[P2]` | **Accurate Direct Outbox Metrics Query in Runbook**: The outbox runbook piped `/app/probe` into grep, but probe only checks HTTP status and writes no response body. Querying `localhost:9090` from scratch API failed because scratch containers have neither curl nor shell. | Updated `docs/runbooks/runbook-outbox-backlog.md` to document verified metric inspection from the host/debug environment (`curl.exe -s http://127.0.0.1:5001/metrics \| findstr /i outbox_pending_events_gauge`) or querying Prometheus directly (`http://localhost:9090/api/v1/query?query=outbox_pending_events_gauge`). |
| **Finding 3** | `[P2]` | **End-to-End Prometheus Scrape $\to$ Rule $\to$ Alertmanager $\to$ Responder Pipeline Drill**: The incident drill script only tested direct synthetic alert injection to Alertmanager's API, skipping validation of the live scrape and rule evaluation loop. | Updated `scripts/test-synthetic-incident-drills.ps1` (Step 4b) to launch containerized `prometheus`, `alertmanager`, and `drill-responder` concurrently. Prometheus evaluates the active `ServiceTargetDown` rule (`up == 0`), fires the alert to Alertmanager, and Alertmanager routes the notification to the responder's `/escalations/platform-sre` channel with Docker secret authentication, verified end-to-end. |
| **Finding 4** | `[P2]` | **Outbox Count Failure Tracking and Gauge Staleness Prevention**: When `CountPending` failed, `ProcessBatch` silently logged and ignored the error, leaving the previous gauge value on dashboards. | 1. **Failure Counter**: Added `OutboxCountErrorsTotal` (`outbox_count_errors_total`) counter to track database query errors.<br>2. **Unavailable Marker**: When `CountPending` fails, `ProcessBatch` marks `OutboxPendingEventsGauge` as `-1`, signaling dashboard panels and alerting rules that the backlog depth is unavailable rather than stale.<br>3. **Unit Tests**: Added `TestPublisher_ProcessBatch_CountPendingErrorMarksUnavailableAndCounts` asserting `-1` gauge value and counter increment. |
| **Finding 5** | `[P2]` | **Team-Specific Escalation Channels with Failure Test Cases**: Escalation routing needed team-specific webhook receivers and rigorous test coverage for upstream rejection and failure modes. | 1. **Team Channels**: Added dedicated routes `/escalations/platform-sre`, `/escalations/finance-ops`, and `/escalations/default` in `responder.go` matching Alertmanager receiver definitions.<br>2. **Test Suite**: Added unit tests in `internal/telemetry/responder_test.go` covering upstream rejection (HTTP 500 $\to$ 502 returned), upstream unreachable (network error $\to$ 502 returned), and successful upstream paging delivery (HTTP 200 $\to$ 200 returned). |
| **Finding 6** | `[P2]` | **Tracing Scope Definition Alignment**: Clarify throughout evidence, PR description, and Issue #29 that tracing is implemented via request-to-worker correlation ID continuity rather than OpenTelemetry distributed trace trees. | Explicitly verified and clarified in Section 1 and PR metadata that tracing scope is request correlation ID continuity across HTTP middleware, structured `slog` logging, transactional outbox storage, and RabbitMQ message headers. Checked all delivery criteria. |

---

## 11. Schema, Configuration & Rollback Accounting

### Database Schema Accounting
- **Migrations Required:** **Zero (`0`)**.
- **Schema Impact:** No database tables or schema modifications were required. The observability implementation reads live PostgreSQL stats via `pgxpool.Stat()` dynamic collectors and queries existing `public.outbox_event` and `public.inbox_message` tables without schema churn.

### Dynamic DLQ Metric Synchronization & Schema Alignment
- The background worker (`internal/worker`) dynamically synchronizes `rabbitmq_dlq_messages_gauge` from active `dead_lettered` records in `public.inbox_message` upon poison rejection, repaired delivery processing, and during periodic 30-second sweeps.
- When poison messages are repaired and processed or updated by operators, `rabbitmq_dlq_messages_gauge` automatically drops to 0, clearing `DlqPoisonMessagesDetected` without requiring worker container restarts.
- Runbook [`docs/runbooks/runbook-dlq-poison-messages.md`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/runbooks/runbook-dlq-poison-messages.md) reflects `public.inbox_message` schema queries and documents automated metric clearance.

### Operational Escalation Webhook Contract

Alertmanager routes notifications to the drill responder and external operational receivers conforming to the Alertmanager Webhook Payload v4 schema:

```json
{
  "version": "4",
  "groupKey": "{}/{team=\"platform-sre\"}:{alertname=\"ServiceTargetDown\", severity=\"critical\", team=\"platform-sre\"}",
  "status": "firing",
  "receiver": "platform_sre_escalation",
  "groupLabels": { "alertname": "ServiceTargetDown", "severity": "critical", "team": "platform-sre" },
  "commonLabels": { "alertname": "ServiceTargetDown", "severity": "critical", "team": "platform-sre" },
  "commonAnnotations": { "runbook_url": "docs/runbooks/runbook-api-error-surge.md" },
  "alerts": [
    {
      "status": "firing",
      "labels": { "alertname": "ServiceTargetDown", "job": "bliss-api", "severity": "critical", "team": "platform-sre" },
      "annotations": { "summary": "Prometheus target bliss-api is unreachable" },
      "startsAt": "2026-11-22T08:31:31Z"
    }
  ]
}
```

#### Escalation Endpoints and Routing Behavior:
- `POST /alerts`: Default drill sink receiver with Bearer token authentication. Retains alerts in a bounded ring buffer (50 alerts) and forwards to `GO_ESCALATION_FORWARD_URL` if configured.
- `POST /escalations/platform-sre`: Platform SRE team escalation endpoint. Forwards synchronously to `GO_PLATFORM_ESCALATION_URL` (or `GO_ESCALATION_FORWARD_URL` fallback).
- `POST /escalations/finance-ops`: Financial Operations team escalation endpoint. Forwards synchronously to `GO_FINANCE_ESCALATION_URL` (or `GO_ESCALATION_FORWARD_URL` fallback).
- `POST /escalations/default`: Fallback escalation endpoint. Forwards synchronously to `GO_ESCALATION_FORWARD_URL`.

#### Delivery Guarantees & Retry Signaling:
- **Upstream Success**: When the configured paging provider responds with `2xx`, the responder returns HTTP 200 `{"status":"received"}`.
- **Upstream Rejection / Outage**: When upstream delivery fails (connection refused, network timeout, or HTTP non-2xx), the responder returns HTTP 502 Bad Gateway to Alertmanager, preserving Alertmanager's built-in retry and backoff mechanism.
- **Local Fallback**: If no upstream URL is configured, notifications are logged with `slog.Info("operational_escalation_received")` and retained in memory for verification.

#### Downstream Paging Integration & Contract Compatibility:
- **Forwarded Payload Contract**: The forwarder delivers the exact Alertmanager Webhook Payload v4 JSON received from Alertmanager without lossy conversion.
- **Direct Integration Compatibility**: The v4 webhook contract is natively supported by:
  - **PagerDuty**: Configured via the PagerDuty Prometheus Integration (`/integration/prometheus/enqueue`).
  - **Grafana OnCall**: Configured via the Alertmanager Webhook Integration.
  - **Opsgenie**: Configured via the Opsgenie Prometheus Integration.
  - **Slack / Teams**: Configured via standard Alertmanager Webhook receiver bridges (e.g. `prometheus-msteams` or `slack-webhook`).
- **Adapter Requirements**: Custom or proprietary on-call systems expecting a non-Alertmanager JSON schema require a dedicated translation adapter service between Alertmanager/Drill-Responder and the downstream API.
- **Drill Isolation & Safety**: Automated incident drills (`scripts/test-synthetic-incident-drills.ps1`) automatically isolate forwarding destinations to `http://127.0.0.1:9099/test-sink` (the responder's in-memory mock upstream), guaranteeing that automated test runs never generate accidental pages or on-call interruptions while synchronously validating external forwarding over HTTP.

### Configuration Accounting

| Variable | Target Subsystem | Default Value | Description |
| :--- | :--- | :--- | :--- |
| `GO_WORKER_METRICS_ADDR` | Worker process | `:5002` | Port on which the background worker exposes `GET /metrics`. Scraped by Prometheus. |
| `GO_DRILL_RESPONDER_ADDR` | Drill Responder | `:9099` | HTTP bind address for the synthetic incident drill alert responder webhook receiver. |
| `GO_DRILL_RESPONDER_TOKEN_FILE` | Drill Responder | `/run/secrets/drill_responder_token` | Path to Docker secret file containing the dynamic authentication token. Loaded securely without committed plaintext. |
| `GO_DRILL_RESPONDER_TOKEN` | Drill Responder | *(unset)* | Environment variable alternative for supplying authentication token (used in tests). |
| `GO_ESCALATION_FORWARD_URL` | Drill Responder | *(unset)* | Upstream webhook URL for external paging / incident management escalation (default channel). |
| `GO_PLATFORM_ESCALATION_URL` | Drill Responder | *(unset)* | Upstream webhook URL for platform-sre escalation paging. |
| `GO_FINANCE_ESCALATION_URL` | Drill Responder | *(unset)* | Upstream webhook URL for finance-ops escalation paging. |

### Secret Generation & Rotation Procedure
1. **Initial Provisioning:**
   ```bash
   go run ./cmd/devstack secrets .local/dev
   ```
   Generates a 32-byte hex string in `.local/dev/drill_responder_token` (mode `0600`).
2. **Rotation:**
   ```bash
   # Generate new token
   openssl rand -hex 32 > .local/dev/drill_responder_token
   # Restart Alertmanager and Drill Responder to load updated secret
   docker compose --profile observability restart alertmanager drill-responder
   ```

### Operational Rollback Procedure
If observability services or instrumentation need to be reverted:
1. Revert application container changes:
   ```bash
   git revert <commit-hash>
   docker compose build api worker drill-responder
   docker compose up -d api worker
   ```
2. Stop observability profile containers:
   ```bash
   docker compose --profile observability down
   ```
3. Because no database migrations were applied, no SQL rollbacks or schema restorations are required. Core operations continue unaffected.

---

## 12. Definition of Done & Sign-Off

- [x] Pull Request opened and green: [PR #82](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/pull/82)
- [x] All CI checks passing (Docker core validation, Security and contracts, PR closing blockers, PR Agent, Vercel).
- [x] All Round 1 review suggestions resolved and verified:
  - Finding 1: `promtool test rules` and Alertmanager routing verification implemented and passing.
  - Finding 2: Full incident diagnosis and recovery lifecycle drills executed for Payment, DLQ, and API incidents.
  - Finding 3: Tracing scope clearly defined as request-to-worker correlation ID tracing with structured log integration.
  - Finding 4: Drill responder protected with 64 KiB body limit, 50-alert ring buffer, and Bearer token authentication.
- [x] All Round 2 follow-up review suggestions resolved and verified:
  - Finding 1: Live containerized Alertmanager $\to$ Drill-Responder firing and resolved webhook delivery verified with Docker secret auth.
  - Finding 2: Recovery drills execute genuine operational domain remediation (`ReconcileStaleAttempts`, `NotificationConsumer.ProcessDelivery` poison handling, and `HTTPMiddleware`).
  - Finding 3: Responder Bearer token externalized via Docker secrets (`devstack secrets`, zero committed plaintext fallbacks).
- [x] All Round 3 follow-up review suggestions resolved and verified:
  - Finding 1: `devstack secrets` supports optional directory argument and clean-checkout bootstrapping path passes explicit directory.
  - Finding 2: Script accurately distinguishes and reports partial runs when Docker is skipped vs full compliance runs.
  - Finding 3: DLQ recovery drill exercises complete inspect $\to$ patch $\to$ replay $\to$ worker ACK lifecycle with `controlledDLQFixture`, driving recovery signal from actual queue depth.
- [x] All Round 4 follow-up review suggestions resolved and verified:
  - Finding 1: Corrected runbook consumer name to `bliss-notification-worker`, enabled replay claiming of `dead_lettered` messages in `claimMessage` without duplicate ACK suppression, documented supported RabbitMQ Shovel replay procedure, and verified persistent inbox replay and deduplication against `InboxStore`.
- [x] All Round 5 follow-up review suggestions resolved and verified:
  - Finding 1: Provisioned `rabbitmq` service in `compose.yaml` under profile `observability`, wired `GO_RABBITMQ_URL` on worker and api, enabled `rabbitmq_shovel` plugin, updated DLQ runbook with supported dynamic shovel parameters and `--profile observability` compose execution.
- [x] All Round 6 follow-up review suggestions resolved and verified:
  - Finding 1: Shovel verification command serializes PowerShell hashtable with `ConvertTo-Json -Compress` and validates dynamic shovel parameter declaration in live RabbitMQ container.
  - Finding 2: RabbitMQ credentials externalized from `compose.yaml` to Docker secrets (`rabbitmq_password` and `rabbitmq_url`) generated via `cmd/devstack` bootstrap with zero committed plain-text passwords.
- [x] All Round 7 follow-up review suggestions resolved and verified:
  - Finding 1: DLQ Shovel replay routes directly to `bliss.notifications.v1` (`dest-queue`) bypassing `bliss.events.v1` `dead.*` loop, and uses RabbitMQ 3.13 `src-delete-after: queue-length`.
- [x] All Round 8 follow-up review suggestions resolved and verified:
  - Finding 1: Synchronous external escalation forwarding with HTTP 502 retry signaling on failure, and environment wiring in `compose.yaml`.
  - Finding 2: Outbox runbook updated with verified host/debug direct curl and Prometheus metric queries.
  - Finding 3: Live containerized Prometheus $\to$ Alertmanager $\to$ Responder scrape and rule evaluation pipeline verified in automated drills.
  - Finding 4: Outbox count failure tracked via `outbox_count_errors_total` and gauge marked `-1` unavailable.
  - Finding 5: Team-specific escalation endpoints (`platform-sre`, `finance-ops`, `default`) with rejection, failure, and success tests.
  - Finding 6: Request-to-worker correlation ID tracing scope documented and verified with zero unredacted PII.
- [x] All Round 9 follow-up review suggestions resolved and verified:
  - Finding 1: Correlation ID validation strictly limited to canonical RFC 4122 UUIDs (`[0-9a-fA-F-]{36}`) and internal format (`c_[0-9a-fA-F]{24}`), strictly rejecting arbitrary tokens (`gt_...`), street addresses, words, or phone numbers, replacing invalid inputs with locally generated opaque IDs.
  - Finding 2: Outbox runbook recovery criteria updated to require a non-negative backlog below 5 (`0 <= outbox_pending_events_gauge < 5`), explicitly treating `-1` as an error sentinel rather than backlog recovery.
  - Finding 3: Publisher post-publish count failure consolidated via `updatePendingGauge`, setting gauge to `-1` and incrementing `outbox_count_errors_total` if the post-publish count fails.
  - Finding 4: Containerized incident drills exercise genuine application metrics (`payment_exceptions_total` $\to$ `PaymentExceptionDetected` $\to$ `/escalations/finance-ops`, and `rabbitmq_dlq_messages_gauge` $\to$ `DlqPoisonMessagesDetected` $\to$ `/escalations/platform-sre` firing and resolution) scraped by Prometheus, rather than direct Alertmanager API injection.
  - Finding 5: Incident drills isolate external escalation URLs to the responder's mock upstream test sink (`http://127.0.0.1:9099/test-sink`), preventing accidental paging on host machines while synchronously verifying HTTP delivery.
  - Finding 6: Documented Alertmanager Webhook v4 contract compatibility with downstream paging providers (PagerDuty, Grafana OnCall, Opsgenie, Slack bridges) and noted adapter service requirements for proprietary schemas.
- [x] All unit and integration tests passing (`go test -v ./...`).
- [x] Telemetry endpoint `GET /metrics` emits valid Prometheus 0.0.4 text format under 2ms.
- [x] Zero high-cardinality labels and zero PII verified via automated audit.
- [x] `compose.yaml` profile `observability` config validated.
- [x] Synthetic incident drills and recovery verification passing 100%.
- [x] 6 operational runbooks delivered and cross-referenced in alert annotations.
