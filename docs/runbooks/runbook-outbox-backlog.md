# Runbook: Transactional Outbox Backlog Growing

**Alert:** `OutboxBacklogGrowing`  
**Severity:** Warning (P2) — Escalates to Critical if pending count > 100  
**Responsible Owner:** Platform SRE / Backend Worker Lead  
**Grafana Dashboard:** [Bliss Atelier - Messaging & Event SRE](http://localhost:3000/d/bliss-messaging-sre)  

---

## 1. Problem Overview & Invariants

The transactional outbox pattern guarantees reliable at-least-once message delivery from PostgreSQL (`public.outbox_event`) to the RabbitMQ event broker.

When `outbox_pending_events_gauge` exceeds 20 events for more than 30 seconds:
- Outbox event dispatcher latency is increasing or dispatcher processing has stalled.
- Downstream workers (payment notifications, stock reservation releases, audit logging) experience delivery delays.
- Eventual consistency latency between database transactions and message queues is compromised.

> [!CRITICAL]
> **Strict Operational Invariants:**
> 1. **Do not purge the outbox table**: Deleting pending events causes permanent loss of critical events (e.g. order confirmations, payment receipts).
> 2. **Maintain deterministic event ordering**: Events must be dispatched in ascending `created_at` order per stream/aggregate.
> 3. **Never bypass idempotency**: Downstream workers must maintain idempotent event consumption.

---

## 2. Immediate Triage & Diagnosis

### Step 1: Inspect Outbox Depth & Publisher Rates on Grafana
Check the **Messaging & Event SRE** dashboard:
- `outbox_pending_events_gauge`: Current pending event backlog.
- `rate(outbox_events_published_total[1m])`: Current publishing rate to RabbitMQ.
- `rabbitmq_dlq_messages_gauge`: Check if downstream poison messages are causing broker issues.

### Step 2: Query Pending Events in PostgreSQL
Inspect the outbox backlog directly in PostgreSQL:
```bash
docker compose exec db psql -U bliss_admin -d bliss_dev -c "
SELECT id, event_type, status, retry_count, error_message, now() - created_at AS age
FROM public.outbox_event
WHERE status IN ('pending', 'publishing')
ORDER BY created_at ASC
LIMIT 15;
"
```

### Step 3: Check Worker & Broker Logs
Inspect the outbox dispatcher worker container logs:
```bash
docker compose logs --tail=100 worker
```
Look for connection timeouts, channel errors, or RabbitMQ connection resets:
- `amqp091: connection reset by peer`
- `broker unavailable: connection refused`
- `database lock timeout on outbox_event`

---

## 3. Remediation Procedures

### Scenario A: RabbitMQ Broker Disconnection or Network Partition
1. Check RabbitMQ container status:
   ```bash
   docker compose ps rabbitmq
   docker compose logs --tail=50 rabbitmq
   ```
2. Verify broker AMQP port is reachable from worker:
   ```bash
   docker compose exec worker nc -zv rabbitmq 5672
   ```
3. If RabbitMQ is down or restarting, allow it to stabilize. The outbox dispatcher will automatically reconnect using exponential backoff.

### Scenario B: Dispatcher Worker Stalled
If PostgreSQL and RabbitMQ are healthy but `outbox_pending_events_gauge` is flat and not draining:
1. Restart the background worker container:
   ```bash
   docker compose restart worker
   ```
2. Monitor outbox gauge drainage:
   ```bash
   # Query Prometheus PromQL API on port 9090:
   curl -s "http://localhost:9090/api/v1/query?query=outbox_pending_events_gauge"

   # Or query API metrics directly from host (on port 5001):
   curl -s "http://127.0.0.1:5001/metrics" | grep outbox_pending_events_gauge
   ```
3. Confirm gauge returns to a valid non-negative count below 5 (`0 <= outbox_pending_events_gauge < 5`).
   > [!WARNING] Gauge Sentinel Accounting
   > A gauge reading of `-1` indicates that the outbox backlog count query failed (database error) and the metric is in an unavailable state. A reading of `-1` does **not** indicate recovery. True recovery requires an active, non-negative count below 5 with zero `outbox_count_errors_total` increments.

### Scenario C: Poison Outbox Record (Serialization or Schema Error)
If a specific event fails repeatedly with `retry_count >= 5`:
1. Inspect the failing payload:
   ```bash
   docker compose exec db psql -U bliss_admin -d bliss_dev -c "
   SELECT id, event_type, status, retry_count, error_message, payload
   FROM public.outbox_event
   WHERE status = 'failed' OR retry_count >= 5
   LIMIT 1;
   "
   ```
2. If corrupted or unprocessable, transition status to `'failed'` with quarantine reason (satisfying the `CHECK (status IN ('pending', 'publishing', 'published', 'failed'))` table constraint) so the publisher skips it:
   ```bash
   docker compose exec db psql -U bliss_admin -d bliss_dev -c "
   UPDATE public.outbox_event
   SET status = 'failed', error_message = 'operator quarantine: payload serialization failure', updated_at = now()
   WHERE id = '<CORRUPTED_ID>';
   "
   ```

---

## 4. Verification & Recovery Sign-Off

1. **Gauge Restored**: `outbox_pending_events_gauge` is non-negative and remains below 5 (`0 <= outbox_pending_events_gauge < 5`) for 5 consecutive minutes with zero `outbox_count_errors_total` increments.
2. **Alert Cleared**: Alertmanager confirms `OutboxBacklogGrowing` is in `resolved` state.
3. **Audit Logged**: Record any manual intervention in `docs/evidence/` or the team incident log.
