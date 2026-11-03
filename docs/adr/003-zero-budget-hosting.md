# ADR 003: Conditional zero-budget hosting path

Date: 20 October 2026. Status: proposed; public topology selection blocked pending account and capacity evidence.
Issue: [#9](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/9).

## Context

The product requires durable data, public HTTPS callbacks, background processing and recovery within zero infrastructure budget. Redis, RabbitMQ, Kafka, Docker and local Kubernetes remain required deliverables. The owner now confirms the Oracle account is on Always Free; no allocation measurements or console verification were supplied. The laptop has 7.81 GiB physical RAM and roughly 1.0-1.2 GiB free across this session's readings. After the owner started Docker, engine reads and a bounded container smoke check passed; the VM exposes 3.73 GiB RAM. Neither inventory, an engine smoke check nor advertised cloud quotas demonstrate application runtime capacity.

The [R03 assessment](../hosting-feasibility.md) contains dated official provider sources, measured local inventory, cost/resource constraints, staged environments, operational ownership and unresolved checks. Its provider limits must be rechecked against the actual account before implementation.

Subsequent [individual service probes](../evidence/r03-service-summary.md) passed baseline/restart checks for PostgreSQL, Redis, RabbitMQ, Kafka, Prometheus, Grafana and Collector (process-only readiness for Collector). These idle measurements and narrow restart checks do not establish simultaneous stack capacity, application throughput or recovery targets.

## Proposed decision

Continue local implementation using focused profiles, with a mandatory later simultaneous integrated drill. Investigate the existing Oracle Always Free account for an eligible VM and durable storage, without treating trial credits as sustainable funding. Keep all public deployment choices conditional until CPU architecture, actual capacity, private networking, stable HTTPS, backup independence and operational ownership are verified.

If those gates pass, evaluate one VM with same-origin frontend/API, worker, private stateful services and separate recoverable backup storage. Measure required public workflows before assigning a component only to a local environment. Do not claim HA for one VM. Public Kubernetes promotion remains a separate evidence-based decision; required local Kubernetes implementation remains in scope.

Do not use an expiring free database as the durable production store or a temporary laptop tunnel as the customer deployment. Intermittent free web/database services may support synthetic demonstrations, with their sleep/quota limits stated. Recommend GHCR for release images and standard public-repository CI runners, with bounded artifact retention and verified billing controls, when BAP-014 implements them.

## Alternatives and consequences

Render-only free hosting fails the durable database requirement. Render with a separate free database improves database lifetime but leaves API sleep and worker/broker/recovery gaps. A laptop tunnel depends on desktop power, network and process lifetime. Paid hosting is outside the current budget. Oracle is an investigation candidate because an account exists, not a claim of available or sustainable capacity.

No sustainable public deployment has been established. Customer launch remains blocked until feasibility and the later payment/security/recovery gates pass. If the candidate fails, document the specific blocker and seek an explicit budget/capacity decision while continuing local/sandbox delivery. Do not reduce scope or describe a sandbox as a live release.

## Verification, ownership and rollback

The developer owns technical validation under R01; the merchant owns account/budget and payment-provider decisions. Review the open evidence on 23 October 2026. No production selection is accepted by this ADR yet.

Repository changes include assessment documentation, a standalone local probe script and its evidence. No application schema/config/API or migration change; rollback is to revert those files. Official images were downloaded for isolated, synthetic probes with no published ports; test containers and their newly created anonymous volumes are removed, leaving downloaded images cached. No cloud resource, image publication, public tunnel or billing change was made. Application runtime capacity, callback, restore and load drills remain unperformed; see the assessment's closure checklist.
