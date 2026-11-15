# R18 Evidence: Owner/Manager Identity, Roles, and Audit

**Issue:** [#24 - [R18] Implement owner/manager identity, roles and audit](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/24)  
**Milestone:** Week 4: Shopper and operator journeys  
**Design Reference:** [ADR 006: Google Identity, Bliss-Managed Sessions, and Role-Based Access Control](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/docs/adr/006-google-identity-and-sessions.md)  
**Status:** Completed and verified locally against unit tests, disposable PostgreSQL/Redis integration tests, and Docker Core verification  

---

## 1. Problem and Outcome

Bliss Atelier Perfumes requires robust, zero-trust authentication and role-based access control for owners, store managers, counter employees, and online delivery shoppers while preserving guest access for physical pickups and store browsing.

### Key Operational Invariants Enforced:
1. **Google is the EXCLUSIVE Sign-In Method**:
   - Zero local email/password registration, no password storage, no password reset workflow.
   - **Zero password hashing dependencies**: `golang.org/x/crypto/argon2` or `bcrypt` are completely unnecessary and omitted.
   - Standard, reviewed Go libraries: `golang.org/x/oauth2` and `github.com/coreos/go-oidc/v3`.
2. **Server-Handled OIDC Authorization-Code Flow with PKCE**:
   - Initiated via `GET /api/auth/google`. Creates a short-lived, single-use, browser-bound `login_transaction` record storing random `state`, `nonce`, and PKCE verifier.
   - Callback handled at `GET /api/auth/google/callback`. Validates browser binding, state, and nonce, exchanges code via PKCE verifier, and cryptographically verifies Google ID token signature, issuer (`https://accounts.google.com`), and client audience.
3. **Upward Visibility and Downward Isolation in RBAC**:
   - Four distinct tiers: `owner` (rank 40) > `manager` (rank 30) > `employee` (rank 20) > `customer` (rank 10).
   - **Downward Isolation**:
     - `customer` is isolated strictly to self (`actorID == resourceOwnerID`). Access to `/api/admin/*` is rejected with `403 Forbidden`.
     - `employee` is laterally isolated to counter shifts and sales records recorded by themselves (`recorded_by == employee.id`) or orders assigned to them. Cannot access user management, pricing algorithms, or manager audit logs.
     - `manager` cannot assign or promote anyone to `owner`, cannot demote an `owner`, cannot modify base catalog pricing formulas, and cannot approve opening inventory stock counts.
   - **Upward Visibility**:
     - `owner` possesses full apex visibility over all subordinates, orders, sessions, audit trails, and financial reconciliations.
     - `manager` has operational oversight over `employee` and `customer` accounts, fulfillment, and exception reviews.
     - `employee` has visibility over customers for assigned orders.
4. **Authoritative Server-Side Sessions with Anti-CSRF**:
   - Cryptographically random 64-character tokens. Only SHA-256 hashes are stored in PostgreSQL (`public.user_session`) and cached in Redis.
   - Fail-closed: DB failure denies access (never silently authorizes cached roles). Redis outage falls back to PostgreSQL.
   - Staff lifetime: 30 minutes inactivity / 12 hours absolute maximum.
   - Customer lifetime: 7 days inactivity / 30 days absolute maximum.
   - Anti-CSRF: `X-CSRF-Token` header strictly required on all cookie-authenticated state-modifying requests (`POST`, `PUT`, `PATCH`, `DELETE`).
   - Immediate session revocation upon logout, user deactivation, or role change.
5. **Append-Only Administrative Audit Log (`public.admin_audit_log`)**:
   - All privileged operations recorded with actor user ID, role, action, target resource, IP address, and sanitized metadata.
   - Database runtime privilege constraint: `bliss_runtime` role has `SELECT, INSERT` only (no `UPDATE`, no `DELETE`).
6. **Strict One-Time Owner Bootstrap**:
   - Public registration default is strictly `customer`. Public registration can never grant staff roles.
   - Restricted operator action targeting a pre-existing Google account (`GO_BOOTSTRAP_OWNER_EMAIL`). Once an owner exists, bootstrap capability is permanently disabled.

---

## 2. Architecture & Invariants

```mermaid
sequenceDiagram
    autonumber
    actor User as Owner / Manager / Employee / Customer
    participant Browser as Browser Client
    participant API as Bliss Go API (/api/auth)
    participant Google as Google Identity (OIDC Provider)
    participant DB as PostgreSQL (Authoritative)
    participant Cache as Redis (Session Cache)

    Note over Browser,Google: 1. Authorization-Code Initiation with PKCE
    User->>Browser: Click "Sign in with Google"
    Browser->>API: GET /api/auth/google?return_url=/admin
    API->>API: Generate crypto state, nonce, PKCE verifier + challenge
    API->>DB: INSERT INTO login_transaction (state_hash, nonce, pkce_verifier, return_url, expires_at)
    API->>Browser: Set bliss_oauth_tx cookie & 302 Redirect to Google OIDC Auth URL

    Note over Browser,Google: 2. Google Authentication & Consent
    Browser->>Google: Authenticate & Consent (openid, email, profile)
    Google->>Browser: 302 Redirect to /api/auth/google/callback?code=AUTH_CODE&state=STATE

    Note over Browser,DB: 3. Server-Side Token Exchange & Verification
    Browser->>API: GET /api/auth/google/callback?code=AUTH_CODE&state=STATE (with bliss_oauth_tx cookie)
    API->>DB: Consume login_transaction (check state, binding, expiry; mark used)
    API->>Google: POST /token (exchange code + PKCE verifier)
    Google-->>API: Return id_token & access_token
    API->>API: Validate ID Token signature, issuer, audience, expiry & nonce via go-oidc

    Note over API,DB: 4. Account Resolution & Session Provisioning
    API->>DB: Upsert app_user by google_sub (default role = customer; reject if is_active=false)
    API->>API: Generate 64-char crypto session token + CSRF secret
    API->>DB: INSERT INTO user_session (token_hash, user_id, csrf_secret, expires_at, max_expires_at)
    API->>Cache: Cache session token_hash -> user_id, role, expires_at
    API->>Browser: Set bliss_session (HttpOnly; Secure; SameSite=Lax) & bliss_csrf cookies

    Note over Browser,DB: 5. Authenticated Protected Request
    Browser->>API: POST /api/admin/users/:id/role (Cookie: bliss_session, Header: X-CSRF-Token)
    API->>API: Validate CSRF token against session secret
    API->>Cache: Check session cache (fallback to DB on miss/down)
    API->>DB: Check Upward Visibility & Downward Isolation RBAC rules
    API->>DB: Update target role & revoke target's active sessions
    API->>DB: INSERT INTO admin_audit_log (actor, role, action, resource, ip, metadata)
    API-->>Browser: 200 OK Response
```

---

## 3. Database Schema Migration (`0009_admin_identity_sessions_audit.sql`)

Forward SQL migration registered at version 9:

| Table | Purpose | Runtime Grants (`bliss_runtime`) |
| --- | --- | --- |
| `public.app_user` | Application users indexed by unique `google_sub` | `SELECT, INSERT, UPDATE` |
| `public.login_transaction` | Single-use browser-bound PKCE state tracking | `SELECT, INSERT, UPDATE, DELETE` |
| `public.user_session` | Authoritative server sessions with SHA-256 hashed tokens | `SELECT, INSERT, UPDATE, DELETE` |
| `public.admin_audit_log` | Immutable administrative audit log | `SELECT, INSERT` (Append-only!) |

---

## 4. Verification Evidence

### 4.1 Automated Test Suites Passed
- **`internal/auth` Unit Tests**:
  - `TestRoleHierarchyAndIsolation`: Verified role ranks, Downward Isolation (manager cannot promote to owner or inspect owner logs), Upward Visibility (owner inspects all, manager inspects operational), and pricing/opening stock authority.
  - `TestCryptoUtilities`: Verified 64-character session token entropy, RFC 7636 PKCE S256 challenge, SHA-256 token hashing, and CSRF token HMAC signature + expiration.
  - `TestReturnURLValidation`: Verified open-redirect rejection (`//evil.com`, `https://attacker.com`) and safe relative URL retention.
  - `TestAuthServiceFullFlow`: Tested full OIDC login lifecycle with mock verifier, single-use replay rejection, session touch/sliding expiration, one-time owner bootstrap, and role modification with immediate target session revocation.
- **`internal/httpapi` Tests**:
  - `TestAuthRoutesEndToEnd`: Verified `GET /api/auth/google`, `GET /api/auth/google/callback`, `GET /api/auth/me`, `POST /api/auth/logout`, and CSRF enforcement.
  - `TestAdminRoutesAndRoleIsolation`: Verified customer rejection (403), owner listing all users, manager isolated from seeing owner in user list, manager forbidden from promoting to owner (403), and owner promoting employee to manager.
- **Integration Checks (`scripts/test-integration.ps1`)**:
  - Passed across all packages with disposable PostgreSQL 17.11 and Redis 7 containers.
  - Verified `0009_admin_identity_sessions_audit.sql` forward migration, `schema-v9.json` baseline contract, runtime append-only grants on `admin_audit_log`, and R06 backup/restore/re-adoption drill.
- **Docker Core Verification (`scripts/Test-DockerCore.ps1`)**:
  - Built production multi-stage container images (`bliss-backend`, `bliss-frontend`, `bliss-postgres`).
  - Passed all container checks: non-root execution, loopback binding, read-only rootfs, clean credential layers, and migration failure gating.

### 4.2 Live Instance & Production Google OIDC Verification
- **Automated Live Harness (`scripts/test-live-auth.ps1`)**:
  - Validated local live instance running full Docker Compose topology (`bliss-frontend-1` on port 8080, `bliss-api-1` on port 5001, `bliss-db-1` on PostgreSQL 17.11, `bliss-redis-1` on Redis 7).
  - Exercised full authentication lifecycle: PKCE token initiation, callback transaction binding, session creation, sliding expiration, role promotions, Downward Isolation, session revocation on deactivation, and self-logout.
- **Production Google OIDC Live Test**:
  - Connected live stack to Google Production OIDC Provider (`accounts.google.com`) using registered Web OAuth 2.0 Client credentials.
  - Authenticated genuine Google accounts:
    - `mutugibramwel@gmail.com` (Subject `109843963122575544752`) -> Successfully bootstrapped as initial **`owner`**.
    - `wangibrian66@gmail.com` (Subject `105826992383099195059`) -> Initialized as customer, elevated by Owner to **`manager`**.
    - `mynahnoreen@gmail.com` (Subject `116581322152212910191`) -> Initialized as customer, elevated by Owner to **`employee`**.
  - **Verified Security Invariants**:
    - **One-time Bootstrap**: After `mutugibramwel@gmail.com` became Owner, subsequent bootstrap attempts are permanently locked (`CountOwners > 0`).
    - **Privilege Separation**: Owner promoted accounts using `POST /api/admin/users/:id/role` with `X-CSRF-Token`.
    - **Session Invalidation**: Prior customer sessions were immediately revoked upon role change, preventing privilege fixation.
    - **Audit Log Integrity**: Every role transition and login was immutably recorded in PostgreSQL `admin_audit_log`.
