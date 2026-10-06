# Bliss Atelier Perfumes

Perfume storefront with a vanilla JavaScript frontend and an Express / Prisma / PostgreSQL backend integrating M-Pesa STK push.

## Project planning

Start with [the documentation index](docs/README.md). The production architecture and schedule are proposals, not implemented capabilities or measured capacity claims.

## Current local setup

1. Install Node compatible with the locked backend dependencies and PostgreSQL.
2. In `backend/`, run `npm ci`.
3. Copy `backend/.env.example` to `backend/.env` and configure your database. Never commit secrets.
4. From `backend/`, run `npm start`. Its prestart generates Prisma and applies committed migrations; the default API port is 5000.
5. Serve `frontend/` with a static HTTP server. Set the `api-base-url` meta tag in `frontend/checkout.html` to your API origin, and set `FRONTEND_ORIGIN` to the frontend origin. An empty API base requires same-origin API routing, which the current Express app does not provide.
6. M-Pesa requires configured sandbox credentials and a publicly reachable callback URL. Do not use live payments for automated tests.

`npm run dev` enables server watch mode but does not apply migrations. `npm test` runs the backend helper tests. Docker, Kubernetes, administration, and telemetry are planned in the roadmap.
