# Deployment and rollback runbook

Status: target runbook template. As of 20 October 2026 the owner confirms the Oracle Cloud account is on Always Free; its region, resource allocation and resource-specific free-tier eligibility remain unverified. No public deployment, domain, live merchant API route or Kubernetes deployment has been verified. Commands/provider details will be added with tested infrastructure; this is not a runnable production deployment claim. See the [R03 feasibility report](hosting-feasibility.md) and [conditional hosting ADR](adr/003-zero-budget-hosting.md).

## Environment contract

Development uses synthetic data and fake provider/sandbox. Staging uses isolated DB/brokers/secrets and is excluded from indexing. Production has its own credentials, approved NCBA/STK route, durable data, public HTTPS callback, business policy and operations owner. Free-tier viability must be verified at selection time for persistent storage, sleep behavior, callbacks, broker resources, backups, terms and network access.

Required config categories: application mode/listen address, DB URL/pool limits, frontend origin, provider environment/credentials/callback, collection reference distinct from order ID, Redis/RabbitMQ/Kafka connections, telemetry endpoints and admin/session secrets. Define schema and startup validation with Go foundation. Never print full configuration or store real collection/account references in committed manifests.

## Release procedure

1. Select tested commit/digests from CI; record test evidence, migrations, rollback compatibility, owner and release window.
2. Verify approved provider route, HTTPS/callback and environment isolation. Confirm dashboards/alert delivery and recent recoverable backup; pause if a gate fails.
3. Apply one locked migration job with dedicated DB privileges. Check version/checksum/backfill status; APIs do not auto-migrate. Keep old and new application compatible with expand/contract migrations.
4. Deploy API/worker/frontend images. Readiness checks dependencies/schema; liveness checks process health without restarting healthy APIs during a provider outage.
5. Execute Initial Owner Identity Bootstrap (cold-start deployments only; see runbook below).
6. Run no-charge smoke: product HTML/API, valid validation errors, scoped lookup, admin authorization and fake-provider contract in staging. An authorized merchant conducts supervised live STK verification separately; confirm settlement, not just a prompt/callback.
7. Observe error/latency, DB pool, outbox/queue age and payment reconciliation. Record deployment outcome and image digests.

Local Kubernetes exercise follows this sequence with fake provider; it verifies implementation but not live availability. Publish the distinction in release evidence.

## Initial Owner Identity Bootstrap and Access Control Runbook

In accordance with [ADR 006](adr/006-google-identity-and-sessions.md), Bliss Atelier operates with **zero stored passwords** (no bcrypt/argon2 credentials). Google OpenID Connect (OIDC) is the authoritative identity provider, and all public registrations strictly default to the `customer` role to prevent unauthorized privilege escalation.

On fresh production deployments with an empty `app_user` table, the initial business Owner is established through a restricted, audited, one-time operator bootstrap procedure.

### 1. Production Prerequisites

1. **Google Cloud Console OAuth 2.0 Client**:
   - Authorized JavaScript origins: `https://<production-domain>`
   - Authorized redirect URIs: `https://<production-domain>/api/auth/google/callback`
2. **Production Secret Store / Environment Variables**:
   - `GO_GOOGLE_CLIENT_ID`: Production OAuth Client ID (`...apps.googleusercontent.com`).
   - `GO_GOOGLE_CLIENT_SECRET`: Production OAuth Client Secret (`GOCSPX-...`).
   - `GO_GOOGLE_REDIRECT_URL`: `https://<production-domain>/api/auth/google/callback`.
   - `GO_BOOTSTRAP_OWNER_EMAIL`: The designated human Owner's verified Google/Gmail address (`owner@domain.com`).
   - `GO_TRUSTED_PROXIES`: CIDR ranges of upstream edge reverse proxies / load balancers for client IP extraction.

### 2. Cold-Start Bootstrap Execution

1. **Deploy Production Stack**:
   - Deploy `api` and `frontend` with `GO_BOOTSTRAP_OWNER_EMAIL` set in production configuration.
   - At startup, the API verifies that `SELECT count(*) FROM public.app_user WHERE role = 'owner'` is `0`. The bootstrap gate is armed and waiting.
2. **Owner Initial Sign-In**:
   - The designated business owner navigates in their browser to:
     ```text
     https://<production-domain>/api/auth/google
     ```
   - The API server generates PKCE verification parameters and a cryptographic nonce, stores a short-lived `login_transaction`, and redirects to `https://accounts.google.com`.
   - The owner logs in using the exact Google account configured in `GO_BOOTSTRAP_OWNER_EMAIL`.
3. **Cryptographic Verification and Atomic Elevation**:
   - Google redirects to `https://<production-domain>/api/auth/google/callback`.
   - The API verifies Google's RSA token signature against Google JWKS (`https://www.googleapis.com/oauth2/v3/certs`) and validates that `profile.EmailVerified == true`.
   - The user is inserted into `public.app_user` with the default safe role (`customer`).
   - The system detects `CountOwners() == 0` and matches the verified email with `GO_BOOTSTRAP_OWNER_EMAIL`:
     - Updates user role to `owner`.
     - Revokes any pre-existing session tokens in PostgreSQL and evicts them from Redis to eliminate session fixation vulnerabilities.
     - Appends an immutable audit entry to `public.admin_audit_log` (`actor_role: "system_bootstrap"`, `action: "initial_owner_bootstrapped"`).
     - Generates a fresh authoritative server session with role `owner` and returns the `bliss_session` HttpOnly cookie.
4. **Verification**:
   - Owner visits `https://<production-domain>/api/auth/me` to confirm `"role": "owner"`.
   - Owner verifies user management at `/api/admin/users` and audit history at `/api/admin/audit-logs`.

### 3. Permanent Lockout and Day-2 Security Operations

1. **Permanent Lockout (`CountOwners > 0`)**:
   - The moment an Owner exists, the database check `CountOwners() > 0` permanently and irreversibly disables the bootstrap mechanism.
   - Even if the owner account is subsequently deactivated, `CountOwners()` counts all owner records, preventing bootstrap re-arming.
   - The operator can safely remove or unset `GO_BOOTSTRAP_OWNER_EMAIL` from production environment configurations.
2. **Staff Onboarding via Downward Isolation**:
   - Subsequent employees sign in via Google OIDC, creating default `customer` accounts.
   - The authenticated **Owner** promotes them to `employee` via `POST /api/admin/users/{id}/role` using their session cookie and `X-CSRF-Token`.
   - Downward Isolation strictly prevents employees and customers from accessing administrative endpoints or promoting/demoting accounts (returns `403 Forbidden`).
   - Audit logs capture every role modification, status toggle, and administrative action immutably.

## Rollback procedure

Freeze unsafe writes/worker actions if necessary. Identify previous tested compatible image, then revert API/worker/frontend together or according to validated version compatibility. Do not blindly reissue payments after a failed rollout. Reconcile accepted provider requests and outbox state before resuming retries.

An image rollback does not undo a migration. Prefer forward repair for additive schema; destructive changes require backup/restore decision with documented RPO. Restoring DB can lose payment records for funds actually collected: reconcile provider settlement before taking new actions. Never leave legacy Express and Go concurrently owning callbacks/orders during rollback.

Verify health, safe checkout, admin access and reconciliation, then record incident/root cause/follow-up issue. Include provider-specific commands and dashboard links when implementation exists.

## Required release artifacts

OpenAPI version, image digests, migration versions, compatibility notes, redacted test and capacity reports, backup/restore result, telemetry/alert drill, merchant payment verification, config inventory, support owner and remaining blockers. December 10 release status must reflect actual evidence rather than calendar alone.
