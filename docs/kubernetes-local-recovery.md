# Local Kubernetes Deployment & Recovery Runbook (R26)

**Issue:** `#32` (`[R26] Deliver local Kubernetes and recovery exercises`)  
**Manifests:** `infra/k8s/base/` (Kustomize)  
**Scripts:** `scripts/k8s-local-secrets.ps1`, `scripts/k8s-drill.ps1`  
**Environment Scope:** **Local** (Docker Desktop Kubernetes, single-node)

---

## 1. Architecture & Data-Service Placement

- **Stateless Application Workloads:**
  - `frontend` (`bliss-frontend:local`, `:8080`), `api` (`bliss-backend:local`, `:5001`), `worker` (`bliss-backend:local`, `:5002`), and `fake-provider` (`bliss-backend:local`, `:8090`).
  - Schema migrations run as a dedicated pre-rollout `batch/v1` `Job` (`migrate`) using `GO_DATABASE_URL_FILE=/run/secrets/migrator_url`.
- **Stateful Backing Services (Single-Node Local Placement):**
  - `db` (`bliss-postgres:local` `StatefulSet`, `replicas: 1`, `1Gi` `ReadWriteOnce` PVC `pgdata-db-0`)
  - `rabbitmq` (`rabbitmq:3-alpine` `StatefulSet`, `replicas: 1`, `512Mi` `ReadWriteOnce` PVC `rabbitmq-data-rabbitmq-0`)
  - `redpanda` (`redpandadata/redpanda:v24.2.11` `StatefulSet`, `replicas: 1`, `1Gi` `ReadWriteOnce` PVC `redpanda-data-redpanda-0`)
  - `redis` (`redis:7-alpine` `Deployment`, `replicas: 1`, ephemeral in-memory `64mb` `volatile-lru`)

---

## 2. Security, Authorization & Secret/PII Handling

- **Workload Hardening:** `api`, `worker`, `frontend`, `migrate`, and `fake-provider` run as non-root `10001:10001` with `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, and `capabilities.drop: ["ALL"]`.
- **Identity & Network Isolation:**
  - Dedicated `ServiceAccount` per workload with `automountServiceAccountToken: false`.
  - `NetworkPolicy` (`default-deny-ingress` + explicit allow rules) restricts `db`, `redis`, `rabbitmq`, `redpanda`, and `fake-provider` ingress to only the pods that require them (`frontend` cannot reach `redis`, `rabbitmq`, `redpanda`, or `fake-provider`).
- **Secrets & TLS:**
  - No secrets or private keys are committed to Git. `scripts/k8s-local-secrets.ps1` generates ephemeral local passwords, DSNs, and a self-signed TLS certificate (`bliss.local` $\to$ `bliss-tls`) into namespace `bliss-local`, preserving existing secrets across re-runs.
  - Ingress (`bliss-ingress`) configures TLS termination for `bliss.local` (`nginx` class, `/api` $\to$ `api:5001`, `/` $\to$ `frontend:8080`).
- **PII:** Local exercises use synthetic data and `fake-provider` (`cmd/fakeprovider`); no real customer records or live NCBA/M-Pesa credentials are used.

---

## 3. Single-Node Limitations (**No Local-HA Claim**)

- **No High Availability:** Docker Desktop Kubernetes runs on a single node (`desktop-control-plane`) with single-replica `StatefulSet`s (`replicas: 1`) and `ReadWriteOnce` local PVCs. Node loss or host disk failure takes down the database and brokers.
- **HPA Prerequisite (`metrics-server`):** `api-hpa` and `frontend-hpa` (`autoscaling/v2`, `minReplicas: 1`, `maxReplicas: 3`, `70%` CPU target) are deployed and validated, but Docker Desktop Kubernetes does not bundle `metrics-server` by default (`kubectl top pods` reports `error: Metrics API not available`, and HPA targets show `<unknown>/70%`). Installing `metrics-server` is required before CPU-based autoscaling can trigger.
- **Ingress Controller:** `bliss-ingress` targets `ingressClassName: nginx`; an `ingress-nginx` controller must be installed on the cluster to serve external HTTPS traffic.
- **Resource Envelope:** Tuned for a ~3.73 GiB Docker Desktop VM; run Docker Compose and Kubernetes sequentially rather than simultaneously.

---

## 4. Rollback & Recovery Notes

- **Manifest / Image Rollback:** Use `kubectl rollout undo deployment/api -n bliss-local` (and `frontend` / `worker`), noting that schema migrations are forward-only (`migrate` `Job`).
- **Outbox & Worker Recovery:** During `worker` pod loss or graceful shutdown (`terminationGracePeriodSeconds: 25`), uncommitted RabbitMQ deliveries requeue and uncommitted PostgreSQL `outbox_event` rows remain `pending` / `publishing` (1-minute lease) until the replacement `worker` reconnects.

---

## 5. Local Drill Evidence (2026-11-28, Docker Desktop Kubernetes)

Executed `./scripts/k8s-drill.ps1` against `docker-desktop` (`bliss-local` namespace):
- **Integrated Stack:** All pods (`db-0`, `redis`, `rabbitmq-0`, `redpanda-0`, `fake-provider`, `api`, `worker`, `frontend`) reached `1/1 Running`, and `job/migrate` reached `Completed`.
- **Rolling Restart:** `kubectl rollout restart deployment/api deployment/frontend -n bliss-local` completed cleanly via `kubectl rollout status`.
- **Pod Loss, In-Flight Worker Redelivery & Duplicate Suppression:** Held an uncommitted PostgreSQL row lock on `public.inbox_message` for synthetic `order.created.v1` event `evt_k8s_drill_5c09b4da6801`, confirmed RabbitMQ held `bliss.notifications.v1` with `messages_unacknowledged = 1` before worker termination, and force-deleted `worker-5f6d745f48-klc6q` (plus `api-7bfd485bf6-9bszn`). After releasing the barrier, replacement pods (`api-7bfd485bf6-ck2b2`, `worker-5f6d745f48-f5q62`) came up `1/1 Running`, reconnected to PostgreSQL, Redis, RabbitMQ (`messaging_connected topology=bliss.events.v1`), and Redpanda/Kafka (`kafka_analytics_connected`), processed the broker-requeued delivery (`notification_order_created`, `published|processed`, `messages_unacknowledged = 0`), and on re-queued redelivery logged `consumer_duplicate_delivery_ignored` while preserving `inbox_message` count `= 1`.
- **HPA Check:** `api-hpa` and `frontend-hpa` present (`min=1`, `max=3`); `kubectl top pods` confirmed `Metrics API not available` (recorded `metrics-server` prerequisite).
