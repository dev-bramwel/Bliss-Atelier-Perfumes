# R28 Evidence: Staging, Controlled Promotion and Rollback

Issue: [#34](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/34) (`BAP-014`, `BAP-022d`).  
Environment: **Local disposable Docker rehearsal** (public cloud host remains blocked; see Open Blockers below).

## Deliverables Implemented
1. **Controlled GHCR Promotion & Provenance:** [`.github/workflows/release-promote.yaml`](../../.github/workflows/release-promote.yaml) verifies `build-manifest.json` SHA-256 and `imageId` before `docker load`, pushes digest-pinned images to GHCR, attaches build provenance attestations (`actions/attest-build-provenance`), and retains SBOMs under the `staging` GitHub Environment.
2. **Same-Origin Reverse Proxy & Staging Topology:** [`infra/docker/Caddyfile`](../../infra/docker/Caddyfile) and [`compose.staging.yaml`](../../compose.staging.yaml) run digest-pinned `db`, `migrate` (advisory-locked), `redis`, `api`, `frontend`, and `proxy` with isolated secrets (`BLISS_SECRET_DIR`), `stop_grace_period: 20s`, **zero** `legacy/` Express writers, and **zero** `fake-provider` shadow STK.
3. **Deployment & Rollback Runbook:** [`docs/runbooks/runbook-deployment-rollback.md`](../runbooks/runbook-deployment-rollback.md).

## Verification Results (Local Rehearsal)
Executed `powershell -ExecutionPolicy Bypass -File ./scripts/Test-StagingPromotionAndRollback.ps1`:
- **Manifest integrity:** Exported `bliss-backend`, `bliss-frontend`, and `bliss-postgres` archives and verified SHA-256 + `imageId` against the manifest.
- **Same-origin proxy & readiness (v1):** `db`, `migrate`, `redis`, `api`, `frontend`, and `proxy` (`caddy:2.8.4-alpine`) all reached `Healthy`; `GET /api/health/ready` and `GET /` returned `200` through the Caddy proxy.
- **Controlled promotion (v1 → v2) & in-flight order preservation:** Built `v1` from prior supported revision `ba02b70` (via `git worktree`) and `v2` from the candidate `HEAD` source tree (distinct `v1` and `v2` `api`/`frontend` image IDs). Created an unpaid `PENDING` order on `v1`, rolled out `v2` (`migrate`, `api`, `frontend`, `proxy`), verified `/api/health/ready` returned `200`, and confirmed `GET /api/orders/<id>/payment-status` remained `PENDING` on `v2`.
- **Compatible image rollback (v2 → v1):** Rolled `api` and `frontend` back from `v2` to `v1` (`ba02b70`) against the database, verified `/api/health/ready` returned `200`, and confirmed the in-flight order remained intact. Note: `RuntimeReady()` enforces exact schema parity against `schema-v13.json`, so compatible image rollback applies across releases sharing a compatible schema contract; forward-only SQL schema upgrades are verified separately by `internal/migration/migration_test.go`.

## Open Blockers & Legacy Retention
- **Public-host blocker retained:** No public Oracle Always Free VM or live domain is provisioned yet (`docs/adr/003-zero-budget-hosting.md`), and Cloudflare Workers Free Tier cannot host the Go + PostgreSQL + Redis + RabbitMQ stack directly (though Cloudflare DNS/TLS/Tunnel can front a future origin VM). Live public HTTPS staging and live M-Pesa callback delivery remain blocked until a VM/domain is provisioned.
- **Legacy runtime retained:** `legacy/` is excluded from `compose.staging.yaml` (zero concurrent writers) and retained in the repository per Issue #34 (`Legacy runtime removed only after evidence`) until public staging/acceptance evidence is signed off.
