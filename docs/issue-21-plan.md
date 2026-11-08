# Implementation Plan: [R15] Deliver Transactional Outbox and RabbitMQ Workers

**Issue:** [#21 - [R15] Deliver transactional outbox and RabbitMQ workers](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/21)  
**Milestone:** Week 3: Safe payments and background work  
**Priority:** P0 | Area: messaging  
**Branch:** `feat/21-transactional-outbox-rabbitmq-workers`  

---

## 1. Problem Statement & Scope

In an asynchronous e-commerce checkout flow, business state mutations (order creation, stock reservation, payment status transitions, and quote acceptance) must trigger background jobs and notifications (e.g. customer order confirmation, manager payment notification, delivery alerts, analytics ingestion).

If the API publishes directly to RabbitMQ during HTTP request handling, a network glitch or broker restart creates the **dual-write failure problem**:
1. Order commits in PostgreSQL, but RabbitMQ publish fails -> job is lost forever.
2. RabbitMQ publish succeeds, but database commit fails -> worker processes a non-existent phantom order.

### Scope & Invariants:
1. **Transactional Outbox (`public.outbox_event`)**:
   - Outbox records are inserted within the **exact same PostgreSQL transaction** (`pgx.Tx`) as the business mutation.
   - Database commit is the sole atomic source of truth.
   - Outbox publisher polls and dispatches pending events with publisher confirms.
2. **RabbitMQ Topology & Transport (`internal/messaging`)**:
   - Namespaced exchanges and queues per ADR 001 and project code rules:
     - Exchange: `bliss.events.v1` (topic exchange, durable)
     - Notifications Queue: `bliss.notifications.v1` (durable, bound with routing keys `order.*`, `payment.*`, `delivery.*`)
     - Retry Queue: `bliss.notifications.v1.retry` (durable, with dead-letter back to `bliss.events.v1` and TTL)
     - Dead Letter Queue: `bliss.notifications.v1.dead` (durable DLQ for poison or permanently unprocessable messages)
   - Connection resilience with auto-reconnection backoff, channel pool, and graceful context cancellation.
3. **Durable Consumer Deduplication / Inbox Pattern (`public.inbox_message`)**:
   - Workers record processed event IDs in an `inbox_message` table.
   - If a message is redelivered (e.g. after worker crash before ACK), the consumer checks the inbox, detects duplicate, and immediately ACKs without duplicate business side-effects (**exactly-once business effect**).
4. **Resilience & Fault Invariants**:
   - Crashes before/after commit/publish/ack lose no committed work.
   - Redelivery has one durable business effect.
   - Poison messages (malformed JSON / unhandled schema) reach DLQ without blocking consumer queue.
   - Broker outage cannot erase orders or trigger duplicate charges.

---

## 2. Architecture & Data Flow

```mermaid
sequenceDiagram
    autonumber
    actor Customer as Customer / Gateway
    participant API as API Server (HTTP)
    participant DB as PostgreSQL
    participant Publisher as Outbox Publisher Worker
    participant Rabbit as RabbitMQ Broker
    participant Consumer as Notification Worker

    Customer->>API: POST /api/orders (or payment callback)
    rect rgb(235, 245, 255)
    Note over API,DB: Single Atomic Transaction (pgx.Tx)
    API->>DB: 1. Insert Order & Hold Stock
    API->>DB: 2. Insert Outbox Event (status='pending')
    API->>DB: COMMIT
    end
    API-->>Customer: 201 Created

    loop Outbox Polling Worker
        Publisher->>DB: SELECT * FROM outbox_event WHERE status='pending' FOR UPDATE SKIP LOCKED
        Publisher->>Rabbit: Publish with Publisher Confirm
        Rabbit-->>Publisher: Confirm ACK
        Publisher->>DB: UPDATE outbox_event SET status='published', published_at=now()
    end

    Rabbit->>Consumer: Deliver message from bliss.notifications.v1
    rect rgb(255, 245, 235)
    Note over Consumer,DB: Deduplicated Consumer Execution
    Consumer->>DB: Check public.inbox_message
    alt Already Processed
        Consumer-->>Rabbit: ACK (Skip side-effects)
    else New Message
        Consumer->>Consumer: Execute notification / side-effect
        Consumer->>DB: Insert into public.inbox_message (processed)
        Consumer-->>Rabbit: ACK
    end
    end
```

---

## 3. Database Migration (`0007_transactional_outbox_inbox.sql`)

### Tables:
1. `public.outbox_event`:
   - `id text PRIMARY KEY` (Event UUID or prefixed `evt_...`)
   - `event_type text NOT NULL` (e.g. `order.created.v1`, `order.paid.v1`, `payment.attempt.v1`)
   - `aggregate_id text NOT NULL` (e.g. order ID)
   - `aggregate_version int NOT NULL DEFAULT 1`
   - `correlation_id text NOT NULL DEFAULT ''`
   - `schema_version int NOT NULL DEFAULT 1`
   - `payload jsonb NOT NULL`
   - `destination text NOT NULL DEFAULT 'rabbitmq'`
   - `status text NOT NULL DEFAULT 'pending'` (`pending`, `published`, `failed`)
   - `retry_count int NOT NULL DEFAULT 0`
   - `error_message text`
   - `published_at timestamptz`
   - `created_at timestamptz NOT NULL DEFAULT now()`
   - `updated_at timestamptz NOT NULL DEFAULT now()`
   - Index: `CREATE INDEX idx_outbox_pending ON public.outbox_event (destination, status, created_at) WHERE status = 'pending';`

2. `public.inbox_message`:
   - `id text NOT NULL` (Event / message ID)
   - `consumer_name text NOT NULL` (e.g. `notification_worker`)
   - `status text NOT NULL DEFAULT 'processed'` (`processing`, `processed`, `dead_lettered`)
   - `payload jsonb NOT NULL`
   - `error_message text`
   - `created_at timestamptz NOT NULL DEFAULT now()`
   - `processed_at timestamptz`
   - Composite PK / Unique: `PRIMARY KEY (id, consumer_name)`

3. Permissions:
   - `GRANT SELECT, INSERT, UPDATE, DELETE ON public.outbox_event TO bliss_runtime;`
   - `GRANT SELECT, INSERT, UPDATE, DELETE ON public.inbox_message TO bliss_runtime;`

---

## 4. Component Structure

1. **`internal/events`**:
   - `Event` struct defining canonical envelope matching `docs/api-and-event-contracts.md`.
   - Event types: `OrderCreatedEvent`, `OrderPaidEvent`, `PaymentAttemptEvent`.
   - Serialization and schema validation.
2. **`internal/messaging`**:
   - RabbitMQ connection and channel management (`amqp091-go`).
   - Topology setup: `bliss.events.v1` exchange, `bliss.notifications.v1` queue, DLQ, and retry bindings.
   - Publisher interface with publisher confirms.
   - Mock/Fake messaging client for in-memory unit testing without external RabbitMQ container.
3. **`internal/outbox`**:
   - `OutboxStore` for inserting events in business transactions and fetching pending events with row locks (`FOR UPDATE SKIP LOCKED`).
   - `Publisher`: background runner polling pending events and publishing to RabbitMQ with confirms.
4. **`internal/worker`**:
   - `NotificationConsumer`: consumes from `bliss.notifications.v1`.
   - Deduplicates using `inbox_message`.
   - Handles poison messages by routing to DLQ (`channel.Nack(false, false)`).
   - In-app / admin notification dispatcher.
5. **Configuration (`internal/config`)**:
   - `GO_RABBITMQ_URL` / `GO_RABBITMQ_URL_FILE` (default: `amqp://guest:guest@127.0.0.1:5672/`).
6. **Worker Process Integration (`internal/process`)**:
   - Wire outbox publisher and notification consumer into `case "worker":`.
   - Ensure graceful shutdown: finish in-flight message processing, close consumers, flush outbox batch.

---

## 5. Verification Plan

1. **Unit Tests**:
   - `TestOutbox_EnqueueAndPoll`: Outbox event creation within transaction, commit/rollback behavior.
   - `TestOutbox_PublisherConfirms`: Confirmed publish marks `published`; rejected/timed out publish increments `retry_count`.
   - `TestConsumer_Deduplication`: Replaying the same message ID multiple times invokes the business handler exactly once.
   - `TestConsumer_PoisonMessageToDLQ`: Malformed JSON or unprocessable event NACKs without requeue, landing in DLQ.
   - `TestBrokerOutage_Resilience`: When RabbitMQ is offline, order mutations commit in DB with pending outbox events; publisher resumes when broker recovers.
2. **Database Integration Test (`scripts/test-integration.ps1`)**:
   - Migration 7 applies cleanly on disposable PostgreSQL.
   - Tables `outbox_event` and `inbox_message` verified.
   - Backup/restore/re-adoption drill succeeds.
