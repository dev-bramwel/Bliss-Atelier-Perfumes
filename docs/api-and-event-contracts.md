# API and event contracts

Status: contract rules for BAP-002/022. OpenAPI and event schema files will be added with executable implementation; this document is not their substitute.

## HTTP

Preserve legacy `/api/orders` payload/envelope during Go cutover; use explicit JSON tags and fixture tests. Document strict DTO types/limits, authentication, examples, pagination, HTTP status and errors. New incompatible shapes use a coordinated versioned route/consumer rollout; never silently rename `totalKes` while the frontend reads it.

Orders use server totals, snapshotted delivery quote and explicit payment state. `201` means order created, not paid; `202` can mean saved/processing/uncertain and must have an actionable response. Use `400` invalid input, `401/403` authentication/authorization, `404` scoped absence, `409` version/idempotency conflict, `429` abuse limit and safe retry guidance, and bounded dependency/server errors. Retain legacy warning handling until frontend migration is tested.

Proposed idempotency header: `Idempotency-Key`; persistence keyed by operation and caller scope, with canonical validated-request hash. Replay same body/result, reject changed body with conflict. Define TTL and expired-key behavior before implementation; an expired key must not blindly reinitiate a still-unresolved payment. Guest token and idempotency key serve different purposes.

Bound list size and specify stable sorting/cursors. Use optimistic version checks for mutable catalog/quotes/admin operations. Correlation ID belongs in safe headers/logs, not metric labels. Error details expose actionable user information without SQL, stack traces or provider credentials.

## Events and jobs

Envelope: `eventId`, `eventType`, `schemaVersion`, `occurredAt`, `aggregateId`, `aggregateVersion`, `correlationId`, `payload`. Times are UTC; amount/currency are explicit integers and strings. No contact/address data in analytics events by default. Order-ID partitioning preserves per-key Kafka ordering only within the broker guarantees; consumer logic handles stale versions and duplicates.

RabbitMQ job contracts include stable job/event ID, bounded attempt count, retry eligibility and dead-letter disposition. Kafka events describe committed facts; commands/jobs request work. Publish through transactional outbox with separate destination delivery state. Consumers acknowledge/commit offsets only after durable business effect and deduplication. Replays don't trigger customer charges or duplicate notifications.

Prefer additive optional schema evolution with documented defaults. Breaking changes get a new version and transition window; keep schema fixtures/consumer contract tests. Backfills/replays checkpoint progress and reconcile report totals. Define retention and poison-message behavior before deployment.

## Ownership

Business modules own their schemas and financial semantics. API and event contract changes include producer and all known consumer tests in the PR. Generated artifacts must be reproducible and drift-checked. Link migration/rollback implications and external-provider mapping to ADRs.
