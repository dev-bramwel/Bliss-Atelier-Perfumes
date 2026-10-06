# ADR 001: Application runtime and messaging

Status: proposed; owner decision required. Date: 6 October 2026.

## Context

The owner wants Redis, RabbitMQ, Kafka, Docker and Kubernetes, alongside a reliable store. The scale target and budget are not confirmed. Operating two brokers and a cluster adds deployment, backup, security and incident work.

## Proposed decision

Keep a modular Express application and PostgreSQL as the source of truth. Add Redis for cache/limits, RabbitMQ for durable work, and a transactional outbox. Use Docker throughout. If Kafka is a learning requirement, introduce it for replayable analytics after the checkout pipeline is reliable. If Kubernetes is a learning requirement, deploy a staging environment and pass recovery exercises before using it for production.

RabbitMQ jobs and Kafka events have separate contracts and consumers. No circular broker bridge; no shared assumption of exactly-once delivery. Deduplicate consumers with durable inbox/event IDs, and track outbox delivery independently per broker.

## Alternatives

1. PostgreSQL outbox plus one queue: smaller operational footprint; likely adequate for the provisional scale.
2. Redis-based jobs: fewer systems, but requires a separate durability/eviction policy from cache use.
3. All systems from day one: meets tool exposure early, but increases failure modes before business correctness is proven.
4. Microservices: defers until module ownership or measured independent scaling merits distribution.

## Consequences

This approach supports phased learning while keeping payment correctness independent of broker availability. Kafka/Kubernetes cannot be declared production-ready simply because containers start. If all tools are mandatory, budget and timeline must include HA, upgrades, storage, monitoring and disaster recovery for them.
