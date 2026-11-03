# Bliss Atelier Perfumes

Perfume storefront powered by a unified Go application root featuring Server-Side Rendering (SSR) with [Templ](https://templ.guide) and Tailwind CSS, backed by PostgreSQL. The legacy Express/Prisma prototype is isolated in `legacy/` for fixture and contract verification while full business capabilities are migrated.

## Architecture Overview

The repository is organized as an idiomatic full-stack Go modular monolith:

- **Repository Root**: Standard Go module (`github.com/dev-bramwel/Bliss-Atelier-Perfumes`) with top-level `Dockerfile`, `compose.yaml`, and `Makefile`.
- **`cmd/`**: Entrypoints for each application service:
  - `cmd/web`: Edge frontend server providing Templ SSR pages and reverse-proxying `/api/` requests to the API service.
  - `cmd/api`: Core HTTP API serving catalog, health, and foundation endpoints.
  - `cmd/migrate`: Transactional SQL schema migration runner.
  - `cmd/devstack`: Local development bootstrap, secret generation, and image layer auditing.
  - `cmd/fakeprovider`: Deterministic local payment fixture for M-Pesa STK simulations.
  - `cmd/stock`: Inventory batch control and opening stock management CLI.
  - `cmd/probe`: HTTP health probe for container healthchecks.
  - `cmd/worker`: Background task worker foundation.
- **`internal/`**: Private application domain packages (`catalog`, `config`, `database`, `httpapi`, `migration`, `process`, `provider`).
- **`web/`**: Encapsulates all presentation logic:
  - `.templ` components for type-safe server-side rendering (`index`, `cart`, `checkout`, `layout`).
  - `static/`: Bundled static assets (compiled `tailwind.css`, `style.css`, client JavaScript, product imagery).
  - Tailwind CSS build configuration and client component test suites.
- **`legacy/`**: Preserved Express/Prisma prototype and historical test fixtures.

## Quick Start (Docker Core)

The containerized stack provides same-origin routing, private PostgreSQL with initialization migrations, and an isolated fake payment provider.

### Prerequisites

- Docker and Docker Compose
- Go 1.27.3 (for local execution/testing)
- Node.js 24 (optional, for Tailwind CSS rebuilding)

### 1. Initialize Secrets & Build Stack

```sh
# Generate local development credentials in .local/dev/
make setup

# Build Docker images (db, api, frontend)
make build

# Launch services in detached mode with healthchecks
make up
```

Once healthy, access the application:
- **Storefront**: `http://127.0.0.1:8080/`
- **Cart**: `http://127.0.0.1:8080/cart`
- **Checkout**: `http://127.0.0.1:8080/checkout`
- **Health**: `http://127.0.0.1:8080/api/health/ready`

### 2. Teardown & Inspection

```sh
# Inspect container logs
make logs

# Stop and remove containers and networks
make down
```

## Development & Testing

```sh
# Run unit and race tests
go test ./...

# Run code formatting
make format

# Run Docker Core end-to-end acceptance drill (PowerShell)
make docker-test

# Recompile Templ templates (if modified)
cd web && templ generate

# Rebuild Tailwind CSS (if modified)
cd web && npm run build:css
```

See the [documentation index](docs/README.md) for architectural decision records (ADRs), SQL migrations, and the migration roadmap.
