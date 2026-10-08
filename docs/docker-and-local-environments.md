# Docker configuration and local environments

Status: specification for BAP-010/BAP-022. No Dockerfile or Compose file is supplied by this planning document. Validate implemented files against this contract before publishing runnable commands.

## Planned configuration files

| File | Responsibility |
| --- | --- |
| `backend/Dockerfile` | Go compilation/test stages and non-root runtime |
| `backend/.dockerignore` | Exclude env secrets, legacy node_modules, Git metadata, local artifacts |
| `frontend/Dockerfile` | Static frontend HTTP serving and same-origin API proxy |
| `infra/docker/frontend.conf` | Proxy `/api/` and serve HTML/assets; forwarded header policy |
| `compose.yaml` | Core frontend/API/Postgres development environment |
| `compose.infrastructure.yaml` | Redis/RabbitMQ/Kafka/worker profile or overlay |
| `compose.observability.yaml` | Metrics/dashboards/collector overlay |
| `.env.example` | Root Compose non-secret placeholders; never real credentials |

Final paths follow implementation decisions, with docs updated together. Browser uses public frontend origin; API connects to service DNS names internally. `localhost` inside a container is that container. Callback URLs must be publicly reachable HTTPS for sandbox/provider checks; a local service name is not a public callback endpoint.

## Go image contract

Multi-stage build: pinned supported Go builder, dependency manifests first for cache, source copy, verify/build with immutable dependencies, `CGO_ENABLED=0` only if all selected libraries support it, trimmed binary with build metadata. Runtime includes trusted CA certificates for HTTPS, a non-root UID and the necessary command binaries. Avoid shell/runtime tools unless needed for the chosen health check. `api`, `worker`, `migrate` may share one image with explicit command; never run migrations implicitly on API startup.

Exclude secrets from build context and layers. No latest tags; record reviewed image digest pins during implementation and automate updates. Choose architecture-compatible images for machine/CI. Read-only filesystem where possible, writable temporary mount only where needed. Bound resources and connections; no privileged mode or host Docker socket mounted into application containers.

## Compose service contract

Core services: frontend, API, Postgres and a one-shot migration service. Readiness depends on actual DB/schema availability; process startup ordering alone is insufficient. API liveness stays independent of external provider. Seed only synthetic/dev data through an explicit command.

Infrastructure: Redis cache/limits with documented eviction policy, RabbitMQ durable queues/confirmations/DLQ, Kafka with correct internal advertised listeners, topic retention and partitions, bounded worker concurrency. Separate cache and security/session policies as needed. A single broker node is a learning setup, not HA. Profiles/overlays allow limited hardware to run targeted tests, but the final integrated drill includes all required infrastructure.

Observability: telemetry collector, Prometheus/Grafana or equivalent, provisioned local dashboards. Restrict DB/broker/admin ports to loopback if host access is needed; otherwise keep internal. Development-only default credentials must never enter a public environment. Application connects with separate DB/queue credentials and least privilege where feasible.

Named volumes preserve local DB/broker data across restarts. `down --volumes` destroys it: document this clearly in the actual runbook and never use it as routine cleanup. Backups go outside the container and restore is tested into a separate volume/environment. Secret files stay untracked; redact config output before sharing because Compose renders environment values.

## Acceptance

Fresh checkout setup, offline-safe runtime behavior after image build, same-origin storefront checkout, DB persistence across restart, migration failure blocks unsafe startup, graceful shutdown, provider mock, redelivery/replay, and resource measurements. `docker compose config --quiet` checks structure; a build and exercised service stack are separate checks.

Source: [Docker Go guide](https://docs.docker.com/guides/golang/) and [multi-stage build guidance](https://docs.docker.com/build/building/multi-stage/) underpin the image design.
