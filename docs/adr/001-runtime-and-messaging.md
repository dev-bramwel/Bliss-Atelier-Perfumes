# ADR 001: Application runtime and messaging

Status: messaging design proposed; infrastructure inclusion confirmed by owner. Runtime direction superseded by ADR 002 on 8 October 2026. Original date: 6 October 2026.

## Context

The owner wants Redis, RabbitMQ, Kafka, Docker and Kubernetes, alongside a reliable store. Budget is zero; 50,000 users is aspirational. The deadline is 10 December 2026 at 14 hours/week. Operating two brokers and a cluster adds deployment, backup, security and incident work.

## Proposed decision

Build a modular Go application and PostgreSQL as the source of truth. Add Redis for cache/limits, RabbitMQ for durable work, and a transactional outbox. Use Docker throughout. Kafka is required for learning; introduce it for replayable analytics after the checkout pipeline is reliable. Kubernetes is required for learning; deploy a local environment and pass recovery exercises before using it for production.

RabbitMQ jobs and Kafka events have separate contracts and consumers. No circular broker bridge; no shared assumption of exactly-once delivery. Deduplicate consumers with durable inbox/event IDs, and track outbox delivery independently per broker.

## Alternatives

1. PostgreSQL outbox plus one queue: smaller operational footprint; likely adequate for the provisional scale.
2. Redis-based jobs: fewer systems, but requires a separate durability/eviction policy from cache use.
3. All systems from day one: meets tool exposure early, but increases failure modes before business correctness is proven.
4. Microservices: defers until module ownership or measured independent scaling merits distribution.

## Consequences

This approach supports phased learning while keeping payment correctness independent of broker availability. Kafka/Kubernetes cannot be declared production-ready simply because containers start. All tools must be implemented and tested locally by the deadline. Customer production hosting is unresolved; HA cannot be claimed from a single local instance. Document upgrades, storage, monitoring and recovery even for the learning environment.

Runtime decision: [ADR 002](002-go-backend-and-vanilla-frontend.md). Messaging design remains applicable to the Go implementation.
