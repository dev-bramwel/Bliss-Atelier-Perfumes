# R07 / issue #13: Docker core evidence

Date: 22 October 2026. Branch: `feat/13-docker-core`. Scope: [issue #13](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/13). All evidence is local, synthetic and disposable. See [setup/recovery](../docker-core.md).

## Acceptance mapping

| Acceptance | Evidence |
| --- | --- |
| Fresh checkout builds and serves checkout | Both root-context Dockerfiles build with pinned Go and static runtimes. A new Compose project with generated credentials/empty volume initializes, migrates and serves checkout HTML, JavaScript, styles and same-origin health routes with HTTP 200. Runtime Tailwind CDN is replaced by committed local CSS. |
| Data survives restart | Seed a synthetic order with KES 2500, bring the project down without deleting volumes, recreate it, rerun migration and verify the same ID/total. |
| Failed migration prevents unsafe readiness | Change the disposable ledger checksum: running API returns readiness 503 while liveness remains 200. After recreation, migration exits 1 and API never starts. Restore the known synthetic checksum and startup/readiness recover. Revoking runtime SELECT also disables readiness. |
| No secrets in layers; internal services private | Generated role passwords are checked against exported final image archives, including nested/compressed layers. Non-root service users, read-only application filesystems, no internal host port bindings and CA-store availability are inspected. Only frontend binds to loopback; database/API/provider/migration use the internal network. |

Repeat from repository root:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/Test-DockerCore.ps1
```

The script creates a unique project, random loopback port and separate named volume. It removes only those disposable resources, including its test volume. `.local/check-*` credential directories remain ignored for inspection. This script must not target an existing merchant database. Routine user shutdown is `docker compose down` without volume deletion.

## Additional verification

- Go 1.27.3: formatting, vet, normal unit tests, all command builds and module checksum verification passed during implementation. Linux race checks passed; ordinary unit/race runs skip opt-in DB tests. The separate real-Compose drill supplies the DB/readiness/deployment evidence.
- Tests prove file-DSN errors do not expose secrets/paths, conflicting URL/file settings fail, a schema-required process needs a DB, and setup does not rotate an existing credential set or silently replace partial credentials.
- Provider fixture acceptance, rejection and malformed JSON are checked through the existing bounded Go provider transport on the private network. No live provider traffic occurs.
- Image scanner negative tests include a deliberately leaked synthetic credential inside both plain and gzip-compressed nested layer archives, plus clean controls. The scanner fails on malformed/oversized/unsupported compressed archives rather than skipping them; it checks the generated credential set, not every possible secret type.
- Tailwind 4.3.0 and CLI are pinned with a frontend-only lockfile. After patching compatible transitive dependencies, clean frontend dependency audit reported zero vulnerabilities. Node 24 was used only in a disposable asset-build container; Node/npm are absent from runtime images. Initial vulnerable toolchain candidates were replaced before delivery.
- Compose schema checks, whitespace checks and local documentation links are verified separately from the exercised stack. HTTP checkout/asset smoke is not a full browser/order/payment E2E claim.

## Versions and limits

- Windows host; Docker Compose 5.6.0; Linux runtime containers.
- Go builder: `golang:1.27.3@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc`.
- PostgreSQL: `postgres:17.11-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24`. Native UID/GID is 70; an initial UID 999 assumption failed startup and was corrected using the pinned image's actual identity. The database binary reports 17.11.
- Asset builder: `node:24-bookworm-slim@sha256:55fb5f5a527ea2c5c4a95f963e48ef90aa38c7b346054fdc8ee9957a2b55d205`.
- CPU/RAM/process limits are development bounds, not capacity guarantees. The harness prints one idle resource sample; no load/soak, RPO/RTO, production hosting or high-availability claim is made.

Final Compose drill passed with the nested/compressed image scan enabled, including the runtime-permission failure check. One post-recovery idle sample reported frontend 2.898 MiB, API 4.297 MiB, database 28.2 MiB, and fake provider 2.551 MiB; these are a single local sample, not peak or integrated capacity measurements. `govulncheck@v1.9.0 ./...` reported no vulnerabilities. The disposable project/volume were removed after the successful drill; cached images and ignored synthetic credential directories remain.

## Schema/configuration and operational review

The historical business schema/migration remains unchanged. Local PostgreSQL initialization creates dedicated migrator/runtime roles. A successful one-shot bootstrap grants runtime business-table DML and installs a private `bliss_ops.schema_ready()` boolean function. It uses SECURITY DEFINER with a fixed search path, fully qualified SQL and revoked PUBLIC access; runtime gets EXECUTE/USAGE but no direct migration-ledger access. Readiness compares the public catalog, ledger attestation and required DML grants in a read-only transaction. This attestation is local deployment support, not a general production migration system.

New opt-in Go settings are `GO_DATABASE_URL_FILE` and `GO_REQUIRE_SCHEMA`; defaults preserve standalone foundation behavior. Compose sets non-loopback API binding internally; the host sees only the frontend's loopback port. No order/payment API, merchant credential, business policy or payment writer changes. Go order/payment endpoints remain later issues; existing legacy behavior is not replaced by the fixture.

Credentials are random file-mounted runtime inputs, excluded from Git/build contexts and printed output. Host secret directories need private ACLs; local Compose secrets are bind mounts, not production secret encryption. API receives only runtime DB credentials. Logs emit sanitized failures and process lifecycle events; health checks distinguish liveness/readiness. Reverse proxy strips untrusted forwarding headers and hides upstream error details. No customer/provider payload logging is introduced.

Rollback preserves the compatible schema, ledger, volume and matching credential set while restoring prior application images/config. Disabling schema readiness removes a safety gate. Backups/restore remain covered by the R06 runbook/harness; `down --volumes` is destructive and restricted to this test's disposable project. Legacy adoption requires its explicit reviewed procedure and is rejected by the local initializer.

External review, CI, PR linking and issue closure are not claimed. No images were pushed to a registry and no production changes were made. The R05 tracker dependency was previously still open despite merged foundation code; tracker closure/review is separate. Actual engineering effort has not been measured against the provisional 5–8-hour estimate.
