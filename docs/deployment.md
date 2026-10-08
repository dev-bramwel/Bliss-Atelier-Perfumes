# Deployment and rollback runbook

Status: target runbook template. No hosting account, public server, domain, live merchant API route or Kubernetes deployment currently exists. Commands/provider details will be added with tested infrastructure; this is not a runnable production deployment claim.

## Environment contract

Development uses synthetic data and fake provider/sandbox. Staging uses isolated DB/brokers/secrets and is excluded from indexing. Production has its own credentials, approved NCBA/STK route, durable data, public HTTPS callback, business policy and operations owner. Free-tier viability must be verified at selection time for persistent storage, sleep behavior, callbacks, broker resources, backups, terms and network access.

Required config categories: application mode/listen address, DB URL/pool limits, frontend origin, provider environment/credentials/callback, collection reference distinct from order ID, Redis/RabbitMQ/Kafka connections, telemetry endpoints and admin/session secrets. Define schema and startup validation with Go foundation. Never print full configuration or store real collection/account references in committed manifests.

## Release procedure

1. Select tested commit/digests from CI; record test evidence, migrations, rollback compatibility, owner and release window.
2. Verify approved provider route, HTTPS/callback and environment isolation. Confirm dashboards/alert delivery and recent recoverable backup; pause if a gate fails.
3. Apply one locked migration job with dedicated DB privileges. Check version/checksum/backfill status; APIs do not auto-migrate. Keep old and new application compatible with expand/contract migrations.
4. Deploy API/worker/frontend images. Readiness checks dependencies/schema; liveness checks process health without restarting healthy APIs during a provider outage.
5. Run no-charge smoke: product HTML/API, valid validation errors, scoped lookup, admin authorization and fake-provider contract in staging. An authorized merchant conducts supervised live STK verification separately; confirm settlement, not just a prompt/callback.
6. Observe error/latency, DB pool, outbox/queue age and payment reconciliation. Record deployment outcome and image digests.

Local Kubernetes exercise follows this sequence with fake provider; it verifies implementation but not live availability. Publish the distinction in release evidence.

## Rollback procedure

Freeze unsafe writes/worker actions if necessary. Identify previous tested compatible image, then revert API/worker/frontend together or according to validated version compatibility. Do not blindly reissue payments after a failed rollout. Reconcile accepted provider requests and outbox state before resuming retries.

An image rollback does not undo a migration. Prefer forward repair for additive schema; destructive changes require backup/restore decision with documented RPO. Restoring DB can lose payment records for funds actually collected: reconcile provider settlement before taking new actions. Never leave legacy Express and Go concurrently owning callbacks/orders during rollback.

Verify health, safe checkout, admin access and reconciliation, then record incident/root cause/follow-up issue. Include provider-specific commands and dashboard links when implementation exists.

## Required release artifacts

OpenAPI version, image digests, migration versions, compatibility notes, redacted test and capacity reports, backup/restore result, telemetry/alert drill, merchant payment verification, config inventory, support owner and remaining blockers. December 10 release status must reflect actual evidence rather than calendar alone.
