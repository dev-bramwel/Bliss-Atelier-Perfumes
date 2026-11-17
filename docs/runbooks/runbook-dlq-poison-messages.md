# Runbook: Dead-Letter Queue (DLQ) & Outbox Backlog Triage

**Alerts:** `DlqPoisonMessagesDetected`, `OutboxBacklogGrowing`  
**Severity:** Critical (P1)  
**Responsible Owner:** Backend Engineer / SRE Platform  
**Grafana Dashboard:** [Bliss Atelier - Messaging, Outbox & SRE](http://localhost:3000/d/bliss-messaging-sre)  

---

## 1. Problem Overview & Invariants

The Bliss messaging pipeline consists of:
1. **Transactional Outbox (`public.outbox_event`)**: Stores events atomically within domain transactions.
2. **Background Dispatcher (`internal/outbox`)**: Polls outbox and publishes with AMQP publisher confirms.
3. **Background Worker (`internal/worker`)**: Consumes events, enforces PostgreSQL inbox deduplication, and routes poison messages to DLQ (`public.inbox_message` with `status = 'dead_lettered'`).

> [!CRITICAL]
> **Strict Operational Invariants:**
> 1. **Durable Poison Message Quarantine**: Corrupt, malformed, or unprocessable messages MUST NOT block the consumer queue or loop infinitely. They are quarantined to DLQ.
> 2. **Transactional Outbox Durability**: Outbox rows are never permanently deleted upon failure; retries increment `retry_count` up to `max_retries` (default 5).

---

## 2. Immediate Triage & Diagnosis

### Step 1: Inspect Quarantined Dead-Letter Messages
Connect to PostgreSQL and inspect `public.inbox_message`:
```bash
docker compose exec db psql -U bliss_admin -d bliss_dev -c \
  "SELECT id, consumer_name, status, error_message, created_at, left(payload::text, 100) FROM public.inbox_message WHERE status = 'dead_lettered' AND consumer_name = 'bliss-notification-worker' ORDER BY created_at DESC LIMIT 10;"
```

### Step 2: Check Outbox Backlog & Failures
```bash
docker compose exec db psql -U bliss_admin -d bliss_dev -c \
  "SELECT status, count(*), max(retry_count) FROM public.outbox_event GROUP BY status;"
```

### Step 3: Check Worker Container Logs
```bash
docker compose logs worker --tail=100 | grep -E "consumer_poison|outbox"
```

---

## 3. Remediation & Safe Actions

### Scenario A: Outbox Backlog Growing (`OutboxBacklogGrowing`)
1. Check if the RabbitMQ broker is unreachable or outbox worker is stopped:
   ```bash
   docker compose ps
   ```
2. If RabbitMQ is down, restart the broker:
   ```bash
   docker compose restart rabbitmq
   ```
3. Once broker connectivity is restored, the outbox dispatcher automatically drains pending rows with publisher confirms.

### Scenario B: Poison Payload Quarantined in DLQ (`DlqPoisonMessagesDetected`)
1. Review the quarantine `error_message` in `public.inbox_message`:
   - If `validation failed`: A schema migration or field format was omitted by producer.
   - If `unmarshal failed`: A publisher serialized invalid JSON.
2. Resolve the underlying root cause:
   - If code / schema bug: deploy the application fix or execute the missing database migration.
   - If corrupt payload: patch data at the source.
3. Replay the quarantined message:
   - **Supported Shovel Replay (Recommended):** Use RabbitMQ Shovel to move the message from the dead-letter queue back to the main exchange for reprocessing:
     ```bash
     # Move quarantined dead letters from DLQ back to notifications exchange
     docker compose exec rabbitmq rabbitmqctl shovel --move-messages-from-queue=bliss.notifications.v1.dead --to-exchange=bliss.events.v1
     ```
   - **Replay Execution & Invariant Preservation:**
     When the replayed message arrives, the worker's `claimMessage` detects that the message was quarantined (`status = 'dead_lettered'`). It atomically transitions the inbox state back to `processing`, executes the event handler, and upon handler success updates the inbox to `processed`.
   - **Warning on Manual SQL Overwrites:**
     > [!WARNING]
     > DO NOT manually execute `UPDATE public.inbox_message SET status = 'processed'`! Manually changing status to `processed` bypasses domain event execution, leaving side effects unhandled while falsely marking the message completed.
4. If the message was an irrecoverable test or malformed probe:
   - Document reason and leave archived in `public.inbox_message` for audit trail.

---

## 4. Verification & Recovery Confirmation

1. Ensure `rabbitmq_dlq_messages_gauge` returns to 0. (The worker automatically synchronizes `rabbitmq_dlq_messages_gauge` from active `dead_lettered` records in `public.inbox_message` on message processing and every periodic sweep).
2. Confirm `outbox_pending_events_gauge` drops below 5.
3. Alertmanager resolves `DlqPoisonMessagesDetected`.
