# Kafka Analytics Pipeline: Retention, Replay, Broker Recovery & Storage Limits

**Issue:** `#31` (`[R25] Implement analytics events, retention and replay`)  
**Packages:** `internal/events`, `internal/outbox`, `internal/analytics`, `internal/process`  
**Migration:** `internal/migration/sql/0016_kafka_analytics_read_models.sql`

---

## 1. Architecture & Isolation Guarantees

1. **Per-Destination Transactional Outbox (`public.outbox_event`):**
   - Business transactions (`CreateOrder`, `ConfirmPayment`, `ManualSettleOrder`, `RecordPaymentAudit`, `exchanges.Complete`) atomically insert analytics events with `destination = 'kafka'` alongside existing RabbitMQ notification events (`destination = 'rabbitmq'`).
   - Separate `outbox.BackgroundPublisher` goroutines poll `destination = 'rabbitmq'` and `destination = 'kafka'` independently.
   - **Kafka Outage Isolation:** If Kafka is unreachable, `destination = 'kafka'` rows remain `pending` / `failed` in PostgreSQL with bounded retries (`outbox.DefaultMaxRetries`), while order creation, M-Pesa payment confirmation, stock reservation/deduction, and RabbitMQ notifications continue unaffected.

2. **Versioned, No-PII Analytics Events:**
   - Topic: `bliss.analytics.events.v1` (configurable via `GO_KAFKA_TOPIC`).
   - Partition key: `aggregateId` (`orderId`), preserving per-order ordering within a partition.
   - Event types (`schemaVersion = 1`, UTC timestamps, integer KES amounts, explicit `"currency": "KES"`):
     - `analytics.order.created.v1`
     - `analytics.order.updated.v1`
     - `analytics.order.paid.v1` (carries separate `transportMerchantKes` and `transportCourierKes`)
     - `analytics.exchange.completed.v1`
     - `analytics.payment.correction.v1`
   - Customer PII (`customerName`, `customerPhone`, `deliveryLocation`, `receiptNumber`) is excluded from all `analytics.*` payloads.
   - **Transport Security & Authentication:** Remote Kafka deployments support TLS (`GO_KAFKA_TLS=true`) and SASL (`GO_KAFKA_SASL_MECHANISM=plain|scram-sha-256|scram-sha-512`, `GO_KAFKA_SASL_USERNAME`, `GO_KAFKA_SASL_PASSWORD` / `GO_KAFKA_SASL_PASSWORD_FILE`) across producer, admin, consumer, and replay clients.

3. **Read Models Cannot Initiate Payments:**
   - `internal/analytics` has zero dependency on `internal/provider` or payment initiation flows.
   - `analytics.Store.SalesSummary` is owner-only (`auth.RoleOwner`) and executes inside a `pgx.ReadOnly` + `pgx.RepeatableRead` transaction.

---

## 2. Deduplication, Ordering & Checkpointed Replay

- **Durable Deduplication (`public.analytics_consumer_dedup`):**
  - Primary key `(event_id, consumer_group)` guarantees at-least-once broker redeliveries or consumer crashes between DB commit and Kafka offset commit cannot double-apply aggregates.
- **Reordered / Stale Events (`public.analytics_order_fact`):**
  - `aggregate_version` guards prevent stale events (e.g., `analytics.order.created.v1` at version `1` arriving after `analytics.order.paid.v1` at version `2`) from overwriting settled `PAID` totals back to `PENDING`.
- **Checkpointed Offsets (`public.analytics_consumer_checkpoint`):**
  - `(consumer_group, topic, partition)` stores `last_offset` in the **same PostgreSQL transaction** as the projection update.
- **Full Replay / Rebuild (`Consumer.ReplayAll` & `Store.BackfillToOutbox`):**
  - On startup, `runKafkaAnalyticsWorker` runs `Store.BackfillToOutbox`, which idempotently enqueues deterministic `destination = 'kafka'` outbox events for any pre-release `Order`, `exchange_request`, and `payment_audit_ledger` rows so existing history is streamed into Kafka.
  - `Consumer.ReplayAll` preflights Kafka partition offsets (failing immediately on any broker or partition-offset error) and then streams retained Kafka batches in bounded memory inside a single PostgreSQL transaction (resetting projections, seeding any pre-retention baseline from `seedBaselineTx`, and applying each batch atomically). If Kafka fails mid-replay, the transaction rolls back and existing projections remain intact.

---

## 3. Retention Policy

- **Kafka Topic Retention:** `EnsureTopic` configures `cleanup.policy=delete` and `retention.ms=7776000000` (**90 days**, configurable via `GO_KAFKA_RETENTION_MS`, minimum `60000` ms).
- **Alignment with R22:** Matches `reports.DefaultFunnelRetention` (90 days).
- **Important Replay Boundary:** `Consumer.ReplayAll` can only rebuild events still retained in the Kafka log (within `retention.ms`). Events older than `retention.ms` are expired by the broker and will not be present in a clean replay from Kafka alone.

---

## 4. Broker Recovery & Storage Limitations

### Local / Single-Broker Compose (`redpanda`)
- `compose.yaml` runs a single-node `redpanda` service (`profiles: ["observability"]`) backed by the `redpanda_data` Docker volume, capped at `mem_limit: 512m` and `1.0` CPU.
- **Broker Restart / Crash Recovery:**
  1. While Redpanda is down, `outbox_event` rows for `destination = 'kafka'` accumulate safely in PostgreSQL (`status = 'pending'` or `'failed'`).
  2. When Redpanda returns healthy (`rpk cluster health`), `runKafkaAnalyticsWorker` reconnects with exponential backoff (2s up to 30s), ensures topic retention config, and drains pending outbox rows.
  3. If `outbox_event` rows hit `retry_count = 5` (`status = 'failed'`) during an extended outage, reset them to `status = 'pending', retry_count = 0` after broker recovery so the publisher drains the backlog.
- **Storage & Volume Loss Limitations:**
  - Single-node `redpanda` has `replication_factor = 1` (no multi-broker redundancy). Loss of the `redpanda_data` volume loses un-replicated topic segments.
  - Because `redpanda_data` is finite and `retention.ms` is 90 days, monitor disk utilization on `/var/lib/redpanda/data` and Prometheus `outbox_pending_events_gauge` to detect stalled consumers or disk pressure before segments fill the volume.
  - Production multi-AZ deployments should use `replication_factor >= 3`, `min.insync.replicas >= 2`, and dedicated persistent volumes sized for 90 days of peak order/exchange volume.
