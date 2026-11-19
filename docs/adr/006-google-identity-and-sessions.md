# ADR 006: Google Identity, Bliss-Managed Sessions, and Role-Based Access Control

Date: 15 November 2026.
Status: Accepted.
Delivery owner: R18 / [issue #24](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/24), coordinated with guest access in R12 / #18 and customer checkout in R20 / #26.

## Context and Accepted Product Decisions

The shop owner confirmed Google OpenID Connect as the **exclusive sign-in method** across all tiers: store owners, operational managers, retail employees, and shoppers. Guest checkout is preserved for browsing, cart management, and pickup. Selecting **delivery checkout** requires Google sign-in before finalizing the order.

There is zero local email/password authentication, zero local password storage, no password reset workflow, and **zero password hashing dependencies** (`golang.org/x/crypto/argon2` or `bcrypt` are completely unnecessary and omitted). Google authenticates identity; Bliss issues and manages authoritative server-side application sessions so standard API traffic never contacts Google on every request.

### Role Separation: Upward Visibility and Downward Isolation

Bliss defines four principal roles: `owner`, `manager`, `employee`, and `customer`. Access control enforces strict **Upward Visibility and Downward Isolation**:

1. **`owner` (Executive Apex)**:
   - **Upward Visibility**: Complete visibility over all operational branches, subordinate staff (`manager`, `employee`), shoppers (`customer`), audit logs, manual settlements, catalog pricing, and inventory batch approvals.
   - **Exclusive Authority**: Exclusive authority to assign, promote, or demote roles (including managers and employees), approve inventory opening balances, alter base catalog pricing algorithms, and inspect full administrative audit records.
2. **`manager` (Operational Command)**:
   - **Downward Isolation from Owner**: Zero role-management authority (cannot assign, promote, or demote any role; role changes are owner-exclusive), cannot approve inventory opening balances, cannot modify base catalog pricing formulas, and cannot inspect owner-confidential audit events.
   - **Upward Visibility over Subordinates**: Full operational oversight over orders, delivery fulfillments, customer exception reviews, manual payment settlements with evidence, and employee operational performance. Can activate/deactivate subordinate employee accounts and manage retail counter staff assignments.
3. **`employee` (Retail Counter Operator)**:
   - **Downward Isolation from Manager & Owner**: Cannot access user administration (`/api/admin/users/*`), cannot mutate catalog pricing, cannot approve stock batches, cannot perform unassisted payment reconciliations, and cannot inspect management audit logs.
   - **Lateral Isolation from Peers**: Isolated to physical sales and counter shifts recorded by themselves (`recorded_by == employee.id`) or orders specifically assigned to them for fulfillment. Cannot view or alter peer employees' sales transactions.
   - **Scoped Customer Visibility**: Can inspect customer delivery and contact details solely for orders assigned to them for packing and dispatch.
4. **`customer` (Public Shopper)**:
   - **Downward Isolation from Staff & Lateral Isolation from Peers**: Strictly isolated to their own authenticated customer account (`user_id == current_user.id`). Any attempt to access `/api/admin/*` or another customer's orders yields `403 Forbidden`.
   - **Self-Visibility**: Visibility into own profile and personal order history.

### Strict One-Time Owner Bootstrap
Public sign-in always creates a standard `customer` account. Public registration can never grant staff or administrative roles. Initial owner provisioning occurs through a restricted, one-time, audited operator configuration targeting a pre-existing account created via Google sign-in (`GO_BOOTSTRAP_OWNER_EMAIL`). Once an owner exists in the database, bootstrap capability is permanently disabled.

### Server-Side Sessions & Anti-CSRF
Sessions are backed by high-entropy 64-character cryptographic tokens. Only SHA-256 hashes are stored in PostgreSQL (`public.user_session`) and cached in Redis.
- **Inactivity Lifetimes**: Staff (30 minutes) / Customer (7 days).
- **Absolute Maximum Lifetimes**: Staff (12 hours) / Customer (30 days).
- **Fail-Closed Guarantee**: Database/session-store failures deny access; cached roles are never silently authorized if authoritative verification fails. Redis outage gracefully falls back to PostgreSQL.
- **Immediate Revocation**: Explicit logout, account deactivation (`is_active = false`), or role changes immediately invalidate all active sessions.
- **Anti-CSRF**: All cookie-authenticated state-modifying requests (`POST`, `PUT`, `PATCH`, `DELETE`) require a valid `X-CSRF-Token` header cryptographically bound to the session secret.

### Append-Only Administrative Audit Log
All privileged mutations (role changes, user deactivations, stock approvals, manual payment settlements) are recorded in `public.admin_audit_log`. The `bliss_runtime` database role has strict `SELECT, INSERT` privileges only; `UPDATE` and `DELETE` permissions are withheld to preserve audit immutability.
