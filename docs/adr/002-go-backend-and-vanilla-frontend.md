# ADR 002: Go backend and vanilla frontend

Date: 8 October 2026. Status: accepted language/frontend choice by owner; implementation details below are proposed engineering defaults.

## Context and decision

Rewrite the full backend in Go. Preserve frontend delivery in HTML, CSS and JavaScript. Start with a modular monolith; extract microservices only using criteria in infrastructure.md. PostgreSQL, Redis, RabbitMQ, Kafka, Docker and Kubernetes remain in scope. Choosing Go does not resolve merchant onboarding, hosting, idempotency or settlement verification.

## Proposed implementation defaults

- One Go module under backend with cmd/api, cmd/worker, cmd/migrate and feature code under internal.
- Standard library net/http routing, JSON, structured slog logging, context and testing; avoid a framework unless a concrete requirement warrants it.
- pgx/v5 and pgxpool for PostgreSQL, explicit parameterized SQL and service-owned transactions. SQL query generation is optional later; Prisma is retired at cutover.
- Versioned SQL migrations with a selected locking/checksum-capable runner. Selection and Prisma baseline procedure are recorded before coding migration tooling.
- Standard HTTP client behind a typed PaymentProvider interface for Daraja/authorized NCBA route. Provider-specific payloads do not become domain models.
- Selected maintained Redis, AMQP and Kafka clients pinned in go.mod/go.sum; choose exact clients/versions at foundation implementation, checking maintenance and compatibility.
- Plain static HTML/JS/CSS; Go templates or generated product HTML for crawling, without a browser framework.

Pin a supported Go release and patches in toolchain/CI/Docker after checking official support. The current installed 1.22.2 is an environment observation, not the target baseline.

## Alternatives and consequences

Keeping Express is cheaper short term; Go matches the owner's backend learning goal and offers a coherent toolchain and explicit concurrency control. A framework may reduce boilerplate but isn't required for this small API. Microservices would add distributed transactions and operational overhead before the local domain is reliable.

Costs: rewrite/testing/migration work within a tight deadline, explicit SQL and DTO mapping, Go learning, and managing compatibility with existing data. Preserve proven business behavior through contracts, rather than translate every line blindly. See backend-migration.md for single-writer cutover and rollback.

Sources: [Go server module layout](https://go.dev/doc/modules/layout), [net/http](https://pkg.go.dev/net/http), [pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool).
