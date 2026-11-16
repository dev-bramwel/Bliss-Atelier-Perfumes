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
3. **Background Worker (`internal/worker`)**: Consumes events, enforces PostgreSQL inbox deduplication, and routes poison messages to DLQ (`public.dead_letter_message`).

> [!CRITICAL]
> **Strict Operational Invariants:**
> 1. **Durable Poison Message Quarantine**: Corrupt, malformed, or unprocessable messages MUST NOT block the consumer queue or loop infinitely. They are quarantined to DLQ.
> 2. **Transactional Outbox Durability**: Outbox rows are never permanently deleted upon failure; retries increment `retry_count` up to `max_retries` (default 5).

---

## 2. Immediate Triage & Diagnosis

### Step 1: Inspect Quarantined Dead-Letter Messages
Connect to PostgreSQL and inspect `public.dead_letter_message`:
```bash
docker compose exec db psql -U bliss_admin -d bliss_dev -c \
  "SELECT id, message_id, reason, created_at, left(payload::text, 100) FROM public.dead_letter_message ORDER BY created_at DESC LIMIT 10;"
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
1. Review the quarantine `reason` in `public.dead_letter_message`:
   - If `validation failed`: A schema migration or field format was omitted by producer.
   - If `unmarshal failed`: A publisher serialized invalid JSON.
2. If the payload bug has been patched in code:
   - Replay the message by re-inserting into outbox or queue:
     ```sql
     -- Example: Requeueing dead letter after fix
     UPDATE public.dead_letter_message SET replayed_at = now() WHERE id = '<dead_letter_id>';
     ```
3. If the message was an irrecoverable test or malformed probe:
   - Document reason and leave archived in `public.dead_letter_message` for audit trail.

---

## 4. Verification & Recovery Confirmation

1. Ensure `rabbitmq_dlq_messages_gauge` returns to 0.
2. Confirm `outbox_pending_events_gauge` drops below 5.
3. Alertmanager resolves `DlqPoisonMessagesDetected`.
