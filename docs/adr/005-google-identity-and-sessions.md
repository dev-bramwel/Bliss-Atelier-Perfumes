# ADR 005: Google identity and Bliss-managed sessions

Date: 23 October 2026.
Status: Google-only login and optional customer accounts accepted by the owner; session and bootstrap designs documented as proposals, not implemented settings or runtime behavior.
Delivery owner: R18 / [issue #24](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/24), coordinated with guest access in R12 / #18 and customer checkout in R20 / #26.

## Context and accepted product decision

The owner selected Google as the only sign-in method for admins, employees and customers, while preserving guest checkout. Customers are required to sign in only when they choose delivery. Browsing, cart use and checkout without delivery (including pickup) remain available without signing in; customers may still choose to sign in elsewhere. There will be no Bliss email-and-password registration or login, local password storage, or password-reset flow. Google authenticates the person; Bliss issues and manages its own application sessions so ordinary requests do not require contacting Google. This adds customer accounts to the release scope and supersedes the previous customer-account exclusion. It does not add Google API integrations or new staff permissions.

First successful Google sign-in creates a local customer account; subsequent sign-ins find the same identity. Staff authenticate through Google too, but their local admin/employee role must be provisioned through a restricted operator/owner workflow. Public registration cannot select a staff role. The first registrant must never automatically become the owner. Bootstrap the initial owner through a restricted, one-time, audited operator action after independently confirming the intended person's Google identity. The action must target that already-created local account, be unavailable through public registration/login, and be disabled after a single owner is provisioned. Recovery must use a separately controlled operator process, never an automatic first-user or email-based fallback. A display name, client role field, email suffix or unverified email claim never grants staff privileges.

After bootstrap, the owner can promote an existing account to employee through an authorized admin workflow. The account must already exist from Google sign-in; an email search is only a way to find/select that account, not proof of identity or authorization. Record the actor, target, role change, time and reason in the access audit. Revoke the target's existing sessions when a privileged role changes.

Email may be stored as profile/contact information, but it is not a Bliss credential or the identity key. Identify Google accounts by the verified issuer and subject (`sub`); do not automatically link identities solely because email addresses match.

Browsing, cart use and checkout without delivery (including pickup) remain available to guests. Selecting delivery requires Google sign-in before the customer can continue with that delivery checkout. An account may simplify future order access, but account history and guest-order attachment require explicit ownership checks. Matching an order email or phone to a Google profile alone must not grant access. Google authentication does not verify the purchase phone number, replace the feedback one-time-code requirement, or verify an M-Pesa payment.

## Proposed login flow

Use Google OpenID Connect with a server-handled authorization-code flow and a maintained, reviewed Go OIDC/OAuth library. Request only `openid email profile`. No Gmail, Drive or Contacts permission; no offline access or retained Google refresh token is needed for basic sign-in.

1. The browser starts login through the same-origin Go API. The server creates a short-lived, single-use login transaction bound to that browser, with random state, nonce and PKCE verifier. Only allowlisted local return destinations are accepted.
2. Google authenticates the person and returns a code to an exactly registered callback URI. The server checks state/browser binding/expiry, exchanges the code, and validates the ID token signature, allowed issuer, client audience, expiry and nonce using the reviewed library. Validate authorized-party claims when applicable. Do not accept a browser-supplied decoded profile as proof.
3. Match the verified Google issuer and subject (`sub`) to a local identity, or atomically create a customer user and identity. Enforce uniqueness so parallel callbacks cannot create duplicate users. Email is profile/contact data, not the identity key. Do not automatically link accounts by email.
4. Reject disabled accounts, consume the login transaction and issue a new local session. Rotate any previous anonymous/login session identifier to prevent session fixation. Clear transient codes/tokens from the redirect destination; never log them.
5. Read current local permissions on protected requests. Google establishes identity; Bliss establishes authorization. Failed or cancelled login leaves the person signed out; guest browsing, cart use and non-delivery checkout remain usable. A customer cannot proceed with delivery checkout until signed in.

These are implementation requirements, not existing endpoints or a verified Google integration. The exact library and route contract must be selected and tested during R18.

## Proposed session and credential storage

Use PostgreSQL as the initial authoritative session store, consistent with the existing source-of-truth architecture. Redis remains available for rate limiting and may later cache security state only with a defined revocation/failure policy; it is not required merely to introduce login.

On successful Google login, issue a new cryptographically random opaque Bliss session token. Send it only in a `Secure`, `HttpOnly` cookie; browser JavaScript must not be able to read it. Persist only a cryptographic hash of the token in PostgreSQL, associated with the user, creation/last-activity times, expiry and revocation state. Validate the session, current account status and current local permissions on protected requests. Never put application session tokens or passwords in `localStorage`.

| Stored item | Intended location and handling |
| --- | --- |
| Local user | PostgreSQL: internal ID, active/disabled state and server-controlled role |
| Google identity | PostgreSQL: unique issuer + subject, user ID, minimum needed profile/contact data |
| Passwords | Bliss has no email/password login and never receives or stores Google passwords |
| Session | Browser receives a random opaque token; PostgreSQL stores its hash, user ID, creation/last-activity/absolute-expiry timestamps and revocation state |
| Login transaction | Short-lived server-side state; keep PKCE material protected and delete/expire after use; bind the browser cookie and nonce to the transaction |
| Google OAuth client secret | Server-only injected secret or private local secret file; never Git, frontend JavaScript, image layers, analytics or logs |
| Google access/ID tokens | Validate/use during login only; do not retain them as the application's session or put them in browser localStorage |

Production session cookie: `Secure`, `HttpOnly`, `SameSite=Lax`, host-only, `Path=/`, with a `__Host-` prefix where applicable. Protect state-changing operations with CSRF tokens and origin checks; logout is a mutation, not a GET. Keep tokens out of URLs and localStorage. Any local HTTP exception must be explicit, loopback-only and rejected in production configuration.

### Proposed initial expiry limits

These values are owner-proposed starting limits, not final operational settings and not implemented behavior. A session expires when either its inactivity limit or maximum lifetime is reached. Activity may refresh the inactivity deadline, but must never extend a session past its absolute maximum lifetime.

| Account type | Inactivity limit | Maximum lifetime |
| --- | --- | --- |
| Admins and employees | 30 minutes | 12 hours |
| Signed-in customers | 7 days | 30 days |
| Guests | No authenticated session | Scoped order access is managed separately |

Implement expiry, logout, a user-accessible action to revoke all of that user's sessions across devices, and immediate revocation on account disable or privileged-role change. Check active user state and current role on protected requests. Require recent authentication for sensitive admin actions. Database/session-store failure denies protected access; it must not silently authorize a cached role. Expired-session cleanup is required, but must not be the mechanism that enforces expiry: requests must reject expired sessions even if cleanup has not run. Simultaneous-device limits and renewal cadence remain implementation decisions.

Signing out of Bliss revokes the current Bliss session and clears its cookie; it does not sign the browser out of Google. Signing out of Google is not itself proof that any Bliss session has been revoked. Google-account compromise/recovery and provider security-event integration need an explicit operational policy. Do not claim that a basic Google ID token proves MFA occurred; the staff MFA/step-up and recovery strategy remains an R18 acceptance decision. Session timeout values, MFA/step-up requirements and recovery procedures require review before production activation.

## Delivery plan and verification gates

1. Confirm the OAuth project owner, local/staging/production origins, exact redirect URIs, consent branding, supported audience/test-user settings and secret injection. Record identifiers only where appropriate; do not request secrets in chat or commit them.
2. Add a forward SQL migration for users, Google identities, sessions, login transactions and access audit. Coordinate version registration/manifests with the unmerged inventory work in PR #48; do not rewrite its migration or allocate a conflicting version. Give runtime only required identity/session privileges and retain restricted bootstrap administration.
3. Implement provider verification behind an interface, login/callback, current-user lookup, protected logout/revocation and role middleware in Go. Add sign-in/account controls to the vanilla frontend; require customer authentication when delivery is selected while keeping guest browsing, cart use and non-delivery checkout functional.
4. Prove forged/expired/wrong-audience tokens, state/nonce/PKCE failures, replay, duplicate identity races, open redirects, session fixation, CSRF, role escalation, disabled users, logout, expiry and dependency failure are rejected. Test customer isolation, employee own-sales/assigned-order access and owner-only role/stock/price controls directly against the API.
5. Run isolated fake-provider tests without personal Google accounts; then perform an explicitly configured real Google test with dedicated test accounts. Fake-provider success alone does not prove Google setup or production readiness.

Existing order lookup authorization and browser-local order backups still need their planned R12/R20 changes. Google identity does not by itself repair those legacy paths. Optional accounts expand scope and require re-estimation; no prior time estimate, deadline feasibility or completed login capability is implied by this ADR.

## Alternatives and consequences

Local email/password login would add password storage/reset responsibilities and is out of scope. A managed identity platform is possible but is not selected merely because Google is the provider. Long-lived browser JWTs would complicate immediate disable/revocation and are not the proposed application session mechanism.

Google-only account login depends on Google availability and excludes creating an account without a Google identity. Guest checkout reduces that restriction for customers. Staff still require a documented recovery/availability procedure. No fallback staff bypass is authorized. New profile/session records require privacy, retention, account-deletion and audit rules before release; financial retention does not justify keeping every profile field indefinitely.

## Sources and current evidence

Reviewed 23 October 2026: [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect), [Google web-server OAuth flow](https://developers.google.com/identity/protocols/oauth2/web-server), and [Google ID-token claims](https://developers.google.com/identity/openid-connect/reference).

This change records decisions and an implementation contract only. No login code, live OAuth client, schema/config/API change, credential access, account provisioning or production test is delivered here. Rollback is reverting these documentation changes; provider/session migrations require their own reviewed rollback and evidence when implemented.
