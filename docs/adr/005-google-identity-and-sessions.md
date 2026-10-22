# ADR 005: Google identity for staff and optional customer accounts

Date: 23 October 2026.
Status: owner-selected identity provider and customer scope; engineering design proposed, not implemented.
Delivery owner: R18 / [issue #24](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/24), coordinated with guest access in R12 / #18 and customer checkout in R20 / #26.

## Context and accepted product decision

The owner requested Google account creation and login for admins, employees and customers in the project conversation. The owner explicitly chose optional Google sign-in while preserving guest checkout. This adds customer accounts to the release scope and supersedes the previous customer-account exclusion. It does not add customer passwords, Google API integrations or new staff permissions.

First successful Google sign-in creates a local customer account; subsequent sign-ins find the same identity. Staff authenticate through Google too, but their local admin/employee role must be provisioned through a restricted operator/owner workflow. Public registration cannot select a staff role. The first registrant must never automatically become the owner. Before activation, define a one-time audited bootstrap using an independently confirmed Google identity. A display name, client role field, email suffix or unverified email claim never grants staff privileges.

Browsing, cart use and checkout remain available to guests. An account may simplify future order access, but account history and guest-order attachment require explicit ownership checks. Matching an order email or phone to a Google profile alone must not grant access. Google authentication does not verify the purchase phone number, replace the feedback one-time-code requirement, or verify an M-Pesa payment.

## Proposed login flow

Use Google OpenID Connect with a server-handled authorization-code flow and a maintained, reviewed Go OIDC/OAuth library. Request only `openid email profile`. No Gmail, Drive or Contacts permission; no offline access or retained Google refresh token is needed for basic sign-in.

1. The browser starts login through the same-origin Go API. The server creates a short-lived, single-use login transaction bound to that browser, with random state, nonce and PKCE verifier. Only allowlisted local return destinations are accepted.
2. Google authenticates the person and returns a code to an exactly registered callback URI. The server checks state/browser binding/expiry, exchanges the code, and validates the ID token signature, allowed issuer, client audience, expiry and nonce using the reviewed library. Validate authorized-party claims when applicable. Do not accept a browser-supplied decoded profile as proof.
3. Match the verified Google issuer and subject (`sub`) to a local identity, or atomically create a customer user and identity. Enforce uniqueness so parallel callbacks cannot create duplicate users. Email is profile/contact data, not the identity key. Do not automatically link accounts by email.
4. Reject disabled accounts, consume the login transaction and issue a new local session. Rotate any previous anonymous/login session identifier to prevent session fixation. Clear transient codes/tokens from the redirect destination; never log them.
5. Read current local permissions on protected requests. Google establishes identity; Bliss establishes authorization. Failed or cancelled login leaves the person signed out and guest checkout usable.

These are implementation requirements, not existing endpoints or a verified Google integration. The exact library and route contract must be selected and tested during R18.

## Proposed session and credential storage

Use PostgreSQL as the initial authoritative session store, consistent with the existing source-of-truth architecture. Redis remains available for rate limiting and may later cache security state only with a defined revocation/failure policy; it is not required merely to introduce login.

| Stored item | Intended location and handling |
| --- | --- |
| Local user | PostgreSQL: internal ID, active/disabled state and server-controlled role |
| Google identity | PostgreSQL: unique issuer + subject, user ID, minimum needed profile/contact data |
| Google password | Never received or stored by Bliss; entered only with Google |
| Session | Browser receives a random opaque token; PostgreSQL stores its hash, user ID, creation/last-activity/absolute-expiry timestamps and revocation state |
| Login transaction | Short-lived server-side state; keep PKCE material protected and delete/expire after use; bind the browser cookie and nonce to the transaction |
| Google OAuth client secret | Server-only injected secret or private local secret file; never Git, frontend JavaScript, image layers, analytics or logs |
| Google access/ID tokens | Validate/use during login only; do not retain them as the application's session or put them in browser localStorage |

Production session cookie: `Secure`, `HttpOnly`, `SameSite=Lax`, host-only, `Path=/`, with a `__Host-` prefix where applicable. Protect state-changing operations with CSRF tokens and origin checks; logout is a mutation, not a GET. Keep tokens out of URLs and localStorage. Any local HTTP exception must be explicit, loopback-only and rejected in production configuration.

Implement idle and absolute expiry, logout, logout-all and immediate revocation on account disable or privileged-role change. Check active user state and current role on protected requests. Require fresh authentication for sensitive owner actions. Database/session-store failure denies protected access; it must not silently authorize a cached role. Staff and customer timeout values, simultaneous-device limits, renewal cadence and cleanup intervals remain engineering decisions to document before runtime implementation is complete.

Signing out of Bliss revokes its session; it does not sign the browser out of Google. Signing out of Google is not itself proof that every Bliss session has been revoked. Google-account compromise/recovery and provider security-event integration need an explicit operational policy. Do not claim that a basic Google ID token proves MFA occurred; the staff MFA/step-up and recovery strategy remains an R18 acceptance decision.

## Delivery plan and verification gates

1. Confirm the OAuth project owner, local/staging/production origins, exact redirect URIs, consent branding, supported audience/test-user settings and secret injection. Record identifiers only where appropriate; do not request secrets in chat or commit them.
2. Add a forward SQL migration for users, Google identities, sessions, login transactions and access audit. Coordinate version registration/manifests with the unmerged inventory work in PR #48; do not rewrite its migration or allocate a conflicting version. Give runtime only required identity/session privileges and retain restricted bootstrap administration.
3. Implement provider verification behind an interface, login/callback, current-user lookup, protected logout/revocation and role middleware in Go. Add optional sign-in/account controls to the vanilla frontend while keeping guest checkout functional.
4. Prove forged/expired/wrong-audience tokens, state/nonce/PKCE failures, replay, duplicate identity races, open redirects, session fixation, CSRF, role escalation, disabled users, logout, expiry and dependency failure are rejected. Test customer isolation, employee own-sales/assigned-order access and owner-only role/stock/price controls directly against the API.
5. Run isolated fake-provider tests without personal Google accounts; then perform an explicitly configured real Google test with dedicated test accounts. Fake-provider success alone does not prove Google setup or production readiness.

Existing order lookup authorization and browser-local order backups still need their planned R12/R20 changes. Google identity does not by itself repair those legacy paths. Optional accounts expand scope and require re-estimation; no prior time estimate, deadline feasibility or completed login capability is implied by this ADR.

## Alternatives and consequences

Local passwords would add password storage/reset responsibilities and are not selected for the requested initial login path. A managed identity platform is possible but is not selected merely because Google is the provider. Long-lived browser JWTs would complicate immediate disable/revocation and are not the proposed application session mechanism.

Google-only account login depends on Google availability and excludes creating an account without a Google identity. Guest checkout reduces that restriction for customers. Staff still require a documented recovery/availability procedure. No fallback staff bypass is authorized. New profile/session records require privacy, retention, account-deletion and audit rules before release; financial retention does not justify keeping every profile field indefinitely.

## Sources and current evidence

Reviewed 23 October 2026: [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect), [Google web-server OAuth flow](https://developers.google.com/identity/protocols/oauth2/web-server), and [Google ID-token claims](https://developers.google.com/identity/openid-connect/reference).

This change records decisions and an implementation contract only. No login code, live OAuth client, schema/config/API change, credential access, account provisioning or production test is delivered here. Rollback is reverting these documentation changes; provider/session migrations require their own reviewed rollback and evidence when implemented.
