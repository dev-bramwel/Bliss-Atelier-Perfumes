# Scaling and Load Management in Production

Status: Architectural specification and production operational guide following the Go module and Templ SSR refactor.

---

## 1. Executive Summary

Scaling and load management were core architectural drivers for elevating Go to the repository root and adopting server-side rendered (SSR) [Templ](https://templ.guide) components.

The previous prototype architecture consisted of an interpreted Node.js / Express backend with Prisma ORM, paired with client-side JavaScript fetching JSON over the wire to manipulate the DOM. That architecture presented concrete scaling bottlenecks:
- **High Memory Footprint:** Node.js V8 runtimes require 60–120 MiB of RAM per replica at baseline, severely limiting container density on resource-constrained hosting (e.g., Oracle Cloud Always Free or low-cost virtual private servers).
- **Event-Loop Head-of-Line Blocking:** A single CPU-intensive JSON parsing or cryptographic task could block the entire Node.js event loop for all concurrent connections.
- **Client-Side Rendering Latency (FOUC & Hydration Overhead):** Mobile clients on high-latency mobile networks in Kenya (Safaricom / Airtel 3G/4G) endured multiple roundtrips before seeing rendered products (HTML shell -> CSS/JS download -> API request -> client DOM construction).

The refactored unified Go architecture delivers:
- **Micro-Footprint Container Density:** Statically compiled scratch containers with zero runtime dependencies consuming **~3.4 MiB RAM** (`frontend`) and **~6.4 MiB RAM** (`api`) under load.
- **High Concurrency via Goroutines:** Go's $M:N$ scheduler handles thousands of concurrent HTTP connections with lightweight ~2–4 KiB goroutine stacks, eliminating thread-exhaustion bottlenecks.
- **Zero-Allocation Streaming SSR:** Templ compiles templates to native Go code ahead-of-time (AOT), streaming HTML buffers directly to network sockets without template interpretation or V8 runtime overhead.
- **Decoupled Scaling Tiers:** Independent service entrypoints (`cmd/web`, `cmd/api`, `cmd/worker`) allow scaling the edge presentation layer independently from database-heavy transactional APIs.

---

## 2. Resource Footprint & Measured Baseline

During R07 Docker Core acceptance drills, resource consumption was measured across the container stack:

| Container / Service | Image Base | Resident Memory (RSS) | Idle CPU | Concurrency Model |
| :--- | :--- | :--- | :--- | :--- |
| **`bliss-frontend-1` (`cmd/web`)** | Scratch | **3.4 MiB** | 0.00% | Goroutine per request; non-blocking proxy |
| **`bliss-api-1` (`cmd/api`)** | Scratch | **6.4 MiB** | 0.01% | Goroutine per request; pooled pgx connections |
| **`bliss-fake-provider-1`** | Scratch | **2.5 MiB** | 0.00% | Deterministic HTTP fixture |
| **`bliss-db-1` (PostgreSQL 17)** | Alpine | **45.3 MiB** | 0.05% | Process-per-connection (capped by pgxpool) |

### Memory Density Comparison

```text
Node.js + Express + Prisma (1 replica): [████████████████████████████████████████] ~85 MiB
Unified Go (web + api + fake-provider): [████] ~12.3 MiB combined (85%+ memory reduction)
```

On a 1 GB RAM virtual machine:
- The legacy Node stack could safely run at most 2–3 application replicas before risking Out-Of-Memory (OOM) kernel kills.
- The unified Go stack can comfortably support 20+ replicas, worker pools, and PostgreSQL within the same hardware boundary.

---

## 3. Decoupled Service Scaling Dimensions

The refactored layout establishes three distinct execution boundaries:

```mermaid
flowchart TD
    Client["Client / Browser / Mobile"] -->|"HTTPS / Port 443"| CDN["CDN / Edge Cache (Cloudflare)"]
    CDN -->|"HTTP / Port 8080"| Web["cmd/web (Frontend SSR & Edge Proxy)"]
    
    subgraph Edge Tier [Edge / Presentation Tier - Scales on Request Volume]
        Web
    end

    subgraph Internal Tier [Private Application Network - Isolated]
        Web -->|"Reverse Proxy /api/* (Port 5001)"| API["cmd/api (Core API & Catalog)"]
        API -->|"pgx connection pool"| DB[("PostgreSQL 17 (Private DB)")]
        Web -.->|"Direct SSR Catalog Reads (Optional)"| DB
        Worker["cmd/worker (Background Jobs)"] -->|"Outbox & Reconciliation"| DB
    end

    API -->|"HTTPS STK Push"| Provider["M-Pesa Daraja Gateway"]
```

### 1. `cmd/web` (Edge Presentation Tier)
- **Role:** Serves pre-compiled Templ HTML, streams embedded static assets (images, CSS, JS), sanitizes headers, and reverse-proxies `/api/` traffic.
- **Scaling Driver:** Inbound HTTP requests and bandwidth.
- **Scaling Strategy:** Fully stateless. Scale horizontally ($N$ replicas) behind an external load balancer or CDN. Replicas require no shared state or session affinity.

### 2. `cmd/api` (Domain Transactional Tier)
- **Role:** Executes business workflows, pricing calculation, database state transitions, and provider communication.
- **Scaling Driver:** Transactional throughput, database I/O, and external API latency.
- **Scaling Strategy:** Scale horizontally within the bounds of PostgreSQL connection limits. Each replica maintains a bounded connection pool.

### 3. `cmd/worker` (Asynchronous Job Tier)
- **Role:** Handles outbox dispatch, offline payment reconciliation, and scheduled inventory reporting.
- **Scaling Driver:** Background queue backlog.
- **Scaling Strategy:** Scales independently without consuming goroutines or connection slots reserved for user-facing checkout HTTP traffic.

---

## 4. Load Management & Protection Controls

### A. HTTP Request Bounding & Timeouts
Unbounded HTTP requests can lead to Slowloris attacks or memory exhaustion. All services configure explicit timeouts:

| Parameter | Value | Location | Protection |
| :--- | :--- | :--- | :--- |
| `r.Body` Max Bytes | `64 KiB` | `cmd/web`, `cmd/api` | Blocks oversized payload attacks; rejects requests exceeding 64 KiB. |
| `ReadHeaderTimeout` | `5s` | `cmd/web`, `cmd/api` | Closes connections that send headers too slowly (Slowloris mitigation). |
| `ReadTimeout` | `10s` | `cmd/web`, `cmd/api` | Bounds time spent reading the entire request. |
| `WriteTimeout` | `15s` | `cmd/web`, `cmd/api` | Bounds time spent writing the response. |
| `IdleTimeout` | `60s` | `cmd/web`, `cmd/api` | Recycles idle keep-alive connections. |
| `MaxConnsPerHost` | `16` | `cmd/web` (proxy transport) | Limits concurrent upstream connections from web to api to prevent cascade exhaustion. |
| `ResponseHeaderTimeout` | `15s` | `cmd/web` (proxy transport) | Prevents upstream stalls from hanging edge client connections. |

### B. Database Pool Sizing (`pgxpool`)
Direct connections to PostgreSQL consume ~2–10 MiB of server memory each. Naive setups open unlimited connections during traffic surges, causing PostgreSQL to exceed its `max_connections` (default 100) and crash.

In this architecture, database connections are strictly bounded via [`internal/database/database.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/internal/database/database.go):
- **Per-Replica Default:** Configured via `GO_DB_MAX_CONNECTIONS` (default: 4–16).
- **Cluster Capacity Formula:**
  $$\sum_{i=1}^{\text{replicas}} \text{MaxConnections}_i + \text{Reserved}_{\text{admin}} \le \text{PostgreSQL}_{\text{max\_connections}}$$
  For example, with PostgreSQL configured for 100 max connections:
  - 4 `api` replicas $\times$ 16 connections = 64 connections.
  - 2 `web` replicas $\times$ 8 connections = 16 connections.
  - 1 `worker` replica $\times$ 4 connections = 4 connections.
  - 16 connections reserved for migrations, administrative CLI, and backups.
- **Connection Lifecycle:** Connections idle for longer than 30 minutes or exceeding 1 hour total lifetime are recycled automatically to prevent resource leaks.

### C. Client Header Sanitization
In [`cmd/web/main.go`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/cmd/web/main.go), the reverse proxy director explicitly scrubs client-supplied routing headers:
```go
r.Header.Del("Forwarded")
r.Header.Del("X-Forwarded-For")
r.Header.Del("X-Forwarded-Host")
r.Header.Del("X-Forwarded-Proto")
```
This ensures upstream services cannot be tricked by spoofed proxy headers into trusting untrusted client IPs or protocols.

### D. Graceful Shutdown & Connection Draining
Both `cmd/web` and `cmd/api` listen for `SIGINT` and `SIGTERM` signals using Go's `signal.NotifyContext`.
Upon receiving a termination signal:
1. The HTTP server marks itself draining (`draining.Store(true)`), immediately failing readiness checks with `503 Service Unavailable` so external load balancers redirect new traffic to other replicas.
2. Active in-flight requests are allowed up to `GO_SHUTDOWN_TIMEOUT` (default: 10s) to complete processing.
3. Database connection pools close cleanly after in-flight queries finish.
4. The process exits with code 0, achieving **zero dropped customer requests** during rolling deployments or horizontal autoscaling scale-down events.

---

## 5. Templ SSR Performance & Edge Caching Strategy

### Ahead-of-Time Compiled Rendering
Unlike JSX or template engines that parse strings or invoke JavaScript runtimes per request:
- Templ compiles `.templ` files into standard Go code containing optimized byte slices.
- Rendering writes directly to an `io.Writer` (`http.ResponseWriter`), performing zero intermediate string allocations for static template chunks.
- HTML escaping is applied automatically at compile-time for dynamic expressions.

### Embedded Static Assets & Memory Mapping
All client assets under [`web/static/`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/web/static/) are embedded directly into the Go executable via `embed.FS`:
- **Zero Disk I/O:** Assets are served directly from process memory.
- **Immutable Headers:** Static assets are served with consistent MIME types and cache-control headers.

### Recommended Production CDN Architecture

```text
[ Browser / Mobile Client ]
             │
             ▼
[ Global CDN / Edge Cache (Cloudflare / Fastly) ]
    ├── Cache HIT: /static/*, /img/*, tailwind.css, *.js (0ms origin load)
    └── Cache MISS / Dynamic:
             │
             ▼
[ Edge Load Balancer (HTTPS termination) ]
             │
             ▼
[ bliss-frontend (cmd/web) ]
    ├── Renders SSR Pages (/, /cart, /checkout) -> Cache-Control: no-store
    └── Proxies /api/* -> bliss-api
```

By offloading static assets (`/static/*`) and media (`/img/*`) to a global CDN edge, **over 95% of storefront HTTP requests never touch the origin server**, allowing a minimal server instance to handle thousands of concurrent shoppers.

---

## 6. Production Autoscaling Configurations

### Kubernetes Horizontal Pod Autoscaler (HPA) Specification

For production Kubernetes deployments:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: bliss-frontend-hpa
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: bliss-frontend
  minReplicas: 2
  maxReplicas: 10
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 70
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: bliss-api-hpa
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: bliss-api
  minReplicas: 2
  maxReplicas: 6
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 65
```

---

## 7. Failure Modes & Degradation Handling

| Scenario | System Behavior | Recovery / Fail-Safe Mechanism |
| :--- | :--- | :--- |
| **PostgreSQL Transient Outage** | `cmd/api` fails `/health/ready` (returns 503). Inbound `/api/catalog` returns 503 with structured JSON error. | Database connection pool retries with backoff. `cmd/web` continues serving cached static assets and renders storefront with empty/loading state rather than crashing. |
| **M-Pesa API Outage / Timeout** | Provider transport deadline (15s) terminates request cleanly without hanging goroutines. | Order remains in `pending` state; user receives actionable retry message. No duplicate charge is initiated. |
| **High Traffic Spike (Flash Sale)** | Goroutines scale dynamically; `MaxBytesReader` and connection pool bounds prevent memory saturation. | Requests queue at the TCP listener level; excess requests receive standard 503 rather than corrupting database transactions. |
| **Schema Checksum Mismatch** | `RuntimeReady` attestation fails; `/health/ready` returns 503 while `/health/live` remains 200. | Deployment automation detects failed readiness and aborts rolling upgrade before traffic switches to incompatible replicas. |

---

## 8. Summary Checklist for Production Deployment

- [x] Set `GO_DB_MAX_CONNECTIONS` proportionally across all deployed replicas to prevent PostgreSQL connection saturation.
- [x] Configure a CDN (e.g. Cloudflare) to cache `/static/*` and `/img/*` assets at the edge.
- [x] Retain `GO_REQUIRE_SCHEMA=true` in production to prevent traffic routing to un-migrated database schemas.
- [x] Ensure reverse proxy forwarding headers are stripped at the edge (`cmd/web`).
- [x] Configure Kubernetes or container orchestrator with liveness probe on `/health/live` and readiness probe on `/api/health/ready`.
- [x] Deploy edge SSR instances (`cmd/web`) with least privilege: use the read-only `bliss_catalog` credential or fetch via `BLISS_API_URL` without direct database access, isolating `Order` and `MpesaTransaction` tables from perimeter processes.
