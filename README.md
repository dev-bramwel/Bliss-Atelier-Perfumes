# Bliss Atelier Perfumes

Perfume storefront with an HTML/CSS/JavaScript frontend. The current backend is Express / Prisma / PostgreSQL with M-Pesa STK integration code. The approved target is a full Go backend rewrite, starting as a modular monolith.

## Project planning

Start with [the documentation index](docs/README.md). The production architecture and schedule are proposals, not implemented capabilities or measured capacity claims.

See [the Go migration plan](docs/backend-migration.md) and [development workflow](docs/development-workflow.md) for the target setup. Go, Docker and CI commands in the design docs are planned until their files are delivered.

The [Go foundation](backend/README.md) provides API/worker commands, health checks and local test harnesses. The [SQL runner](docs/sql-migrations.md) supports explicit empty initialization and verified Prisma adoption. Express remains the order/payment backend.

## Current local setup (legacy backend)

For the containerized Go foundation and static checkout, use the [Docker core setup](docs/docker-core.md). It provides same-origin routing, private PostgreSQL, migrations and a fake-provider fixture; order/payment submission remains on the legacy backend until later migration slices.

1. Install Node compatible with the locked backend dependencies and PostgreSQL.
2. In `backend/`, run `npm ci`.
3. Copy `backend/.env.example` to `backend/.env` and configure your database. Never commit secrets.
4. For a legacy-only database, explicitly run `npx prisma migrate deploy` before starting it. For Go-owned databases, follow the [SQL migration runbook](docs/sql-migrations.md) instead. From `backend/`, run `npm start`; its prestart generates the Prisma client but does not apply migrations. The default API port is 5000.
5. Serve `frontend/` with a static HTTP server. Set the `api-base-url` meta tag in `frontend/checkout.html` to your API origin, and set `FRONTEND_ORIGIN` to the frontend origin. An empty API base requires same-origin API routing, which the current Express app does not provide.
6. M-Pesa requires configured sandbox credentials and a publicly reachable callback URL. Do not use live payments for automated tests.

`npm run dev` enables server watch mode but does not apply migrations. `npm test` runs the backend helper tests. Docker, Kubernetes, administration, and telemetry are planned in the roadmap.
