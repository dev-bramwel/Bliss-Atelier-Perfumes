# Runbook: Controlled Staging Promotion, Cutover & Rollback (R28)

## Scope
Covers promoting verified CI container images to staging/production (`compose.staging.yaml`), enforcing single-writer Go cutover, protecting secrets, and executing a compatible image rollback.

## 1. Promote a Passing Build Candidate (No Rebuild)
1. Identify a passing `Docker core validation` run on `develop` or `main` (`run_id` and 40-char `source_commit`).
2. Trigger `.github/workflows/release-promote.yaml` via `workflow_dispatch` with `run_id` and `source_commit`.
   - The workflow downloads `docker-build-<source_commit>-<run_id>-*`, verifies each `.tar` SHA-256 and loaded `imageId` against `build-manifest.json`, pushes digest-pinned images to GHCR (`ghcr.io/<owner>/bliss-{backend,frontend,postgres}`), and emits SLSA/OCI build provenance attestations.
   - Never promote an artifact from a failed CI run or an untrusted PR.

## 2. Isolated Secrets & Single-Writer Guard
- Store environment secrets outside the repository in `$BLISS_SECRET_DIR` (`0400` permissions) and GitHub `staging` / `production` environments — never reuse `.local/dev` secrets or PR workflow tokens.
- `compose.staging.yaml` excludes both `legacy/` (Express) and `fake-provider` so there are **no** concurrent legacy/Go database writers and **no** shadow STK initiation.
- Set `BLISS_LOCAL_DEV=0` in staging/production so `migrate` requires pre-provisioned least-privilege roles (`bliss_migrator`, `bliss_runtime`, `bliss_catalog`) under advisory lock `477418744910`.

## 3. Same-Origin HTTPS / Cloudflare Edge Routing
- `proxy` (`infra/docker/Caddyfile`) routes `/health/*` and `/api/*` (stripping `/api`) to `api:5001` and `/*` to `frontend:8080` on a single origin, preserving M-Pesa STK callbacks (`/api/payments/mpesa/callback`) and Google OAuth (`/api/auth/google/callback`).
- When fronted by Cloudflare Free Tier DNS/TLS or an always-on VM `cloudflared` tunnel, restrict `GO_TRUSTED_PROXIES` to the proxy/tunnel subnet.

## 4. Controlled Rollout & Compatible Image Rollback
1. **Promote (vN → vN+1):**
   ```bash
   export BLISS_BACKEND_IMAGE=ghcr.io/<owner>/bliss-backend@sha256:<vN+1>
   export BLISS_FRONTEND_IMAGE=ghcr.io/<owner>/bliss-frontend@sha256:<vN+1>
   docker compose -f compose.staging.yaml up -d --wait migrate api frontend proxy
   ```
   `api` drains in-flight requests (`stop_grace_period: 20s`) before stopping; `migrate` must exit `0` before `api` starts.
2. **Rollback (vN+1 → vN):**
   Re-pin `BLISS_BACKEND_IMAGE` and `BLISS_FRONTEND_IMAGE` to the previous verified `@sha256:<vN>` digest (do **not** run destructive down-migrations; forward-only SQL migrations remain compatible with vN):
   ```bash
   export BLISS_BACKEND_IMAGE=ghcr.io/<owner>/bliss-backend@sha256:<vN>
   export BLISS_FRONTEND_IMAGE=ghcr.io/<owner>/bliss-frontend@sha256:<vN>
   docker compose -f compose.staging.yaml up -d --wait api frontend proxy
   curl -fsS https://<domain>/api/health/ready
   ```
3. Verify `/api/health/ready` returns `200` and inspect `bliss_schema_migrations` + in-flight orders before reopening traffic.
