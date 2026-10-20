# R03: Zero-budget hosting and local capacity assessment

Issue: [#9](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/9). Branch: `feat/budget`.
Assessed: 20 October 2026 (Africa/Nairobi). Baseline commit: `5daff76`.
Status: feasibility assessment delivered for review. Local inventory, provider research and individual service probes are recorded below. On 20 October the owner directed that remaining integrated measurements and Oracle allocation verification be confirmed during subsequent implementation. These remain staging/launch gates, with ownership below; this assessment does not approve production hosting.

## Requirements and decision

Keep infrastructure spend at zero without depending on expiring promotional credits. Preserve the Go/vanilla frontend direction, PostgreSQL authority and required Redis, RabbitMQ, Kafka, Docker and local Kubernetes deliverables. A smaller public topology cannot silently remove required product behavior. Required production traffic targets are 20 browser sessions, 10 API requests/second for 30 minutes and a 30 requests/second burst for ten minutes using a fake provider. Recovery targets are RPO <=15 minutes and RTO <=2 hours; backups retain a rolling 30 days under R01.

No sustainable public topology has been validated. Continue local development and evaluate the existing Oracle account as a candidate; do not provision a production environment using trial credit. [ADR 003](adr/003-zero-budget-hosting.md) records this conditional decision. This is an evidence gap for the evaluated options, not proof that all free hosting is impossible.

The owner initially reported an Oracle trial account and subsequently confirmed it is on Always Free. This is owner-reported account status, not a console inspection. Home region, actual resource allocation, remaining quotas and resource-specific Always Free eligibility remain unknown. No account access, provisioning, billing changes or credentials were used for this assessment.

## Measured local inventory

Read-only PowerShell/CIM and CLI observations on the existing Windows host; this was an active desktop session, not an idle or controlled load benchmark. Values below exclude device identifiers, usernames, IP addresses and secrets.

| Item | Observed result | Implication |
| --- | --- | --- |
| Host | HP Laptop 15-da2xxx; Windows 11 Home Single Language, build 10.0.26300 | Windows/WSL2 development host |
| CPU | Intel Core i5-10210U, 4 cores / 8 logical processors | Inventory only; no application throughput result |
| Physical memory | 8,384,401,408 bytes (7.81 GiB usable physical total reported by CIM) | RAM headroom requires attention |
| Initial free RAM | 1,273,856 KiB (1,244 MiB) | Time-sensitive snapshot |
| Follow-up samples | Free RAM: 1,183 / 1,169 / 1,163 MiB; CPU load: 49 / 70 / 63 percent | Three sequential samples with two-second pauses; collection adds time; not a capacity benchmark |
| C: storage | 254,896,238,592 bytes total; 174,808,117,248 bytes free (162.8 GiB) | Snapshot; no storage throughput or backup-destination test |
| Virtualization | HypervisorPresent=True; VirtualizationFirmwareEnabled=False; WSL default version 2 | Conflicting indicators; do not infer BIOS changes are required from CIM alone |
| WSL | Initially stopped; follow-up: `docker-desktop`, version 2, running | WSL2 engine availability confirmed after owner started Docker |
| Docker | Follow-up: client/server 29.9.1, Linux x86_64, 8 logical CPUs, 4,003,131,392 bytes RAM (3.73 GiB) | Engine healthy; initially zero containers/images, not an application capacity result |
| Compose / Kubernetes client | Compose v5.6.0; kubectl v1.36.1 | Client presence does not establish a functioning cluster |
| Tool discovery | `go`, `node`, `kind`, `minikube` not found on PATH | Not proof of absence from other environments; Docker's bundled Go version is not a host Go toolchain |
| Existing product assets | 23 files, 1,384,560 bytes under `frontend/img` | Static committed assets fit a small release artifact; future uploads need durable storage |
| Repository | Public GitHub repository; no Go module, Dockerfiles, Compose, cluster manifests or CI workflow delivered | Runtime implementation remains in its backlog issues |

Commands used: `Get-CimInstance Win32_ComputerSystem`, `Win32_Processor`, `Win32_OperatingSystem`; `Get-Volume`; `Get-Command`; `wsl --status`; `wsl --list --verbose`; `docker version`; `docker info`; `docker ps`; `docker compose version`; `kubectl version --client --output=yaml`; `gh repo view --json visibility,defaultBranchRef`. Asset totals used `Get-ChildItem frontend/img -File | Measure-Object Length -Sum`.

Initial Docker API reads failed because `dockerDesktopLinuxEngine` was missing. WSL status also warned that WSL1 is unavailable; WSL2 is the intended backend. The owner subsequently started Docker. Follow-up server reads and an isolated container test succeeded; no BIOS or resource configuration changes were made by this assessment. Individual restart/data checks followed as recorded below; no application load or off-host restore drill ran.

### Docker follow-up verification

After the owner started the engine, `docker version`, `docker info` and `docker ps` succeeded. WSL reports the Docker distribution running. Windows free RAM was 1,090 MiB before the test and 1,030 MiB afterward; snapshots are not a measured allocation delta attributable solely to the container.

Pulled the official `alpine:3.22` image, resolved to `alpine@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8`. Ran a short-lived probe with no network, read-only root filesystem, all capabilities dropped, no-new-privileges, a 64 MiB memory limit and a 0.25 CPU limit. The probe exited successfully, reported `x86_64`, `memory.max=67108864` and `cpu.max=25000 100000`, confirming execution and applied cgroup limits. This is an engine smoke check, not a stress or capacity test.

The probe's `/proc/meminfo` reported 3,909,308 KiB total VM memory and 3,259,220 KiB available. These are VM-wide readings, not the container's 64 MiB allowance; do not add VM availability to Windows free RAM as independent physical capacity. The `--rm` probe left no container or volume. The downloaded image remains cached; `docker system df` reported 12.86 MB image storage, zero containers, zero volumes and zero build cache.

### Individual service measurements

At the owner's direction, measured PostgreSQL, Redis, RabbitMQ, Kafka, Prometheus, Grafana and OpenTelemetry Collector sequentially on 20 October. All seven completed their baseline/restart probes, with Collector limited to process-survival checks. See the [results table and raw evidence](evidence/r03-service-summary.md), [method](evidence/README.md) and [standalone runner](../scripts/Measure-ServiceBaseline.ps1).

Sampled idle memory maxima were approximately 31 MiB (PostgreSQL), 4.7 MiB (Redis), 146 MiB (RabbitMQ), 342 MiB (Kafka), 17.4 MiB (Prometheus), 81 MiB (Grafana) and 18.5 MiB (Collector). Five readings per service are not continuous peaks or minimum resource requirements. Measurements support individual-service development; they do not justify adding idle values to size the complete stack.

PostgreSQL retained 1,000 synthetic rows, Redis retained an AOF marker, and Kafka retained topic metadata across same-container restart. These are narrow restart checks, not off-host backup/restore or payment durability. RabbitMQ message durability and an active telemetry pipeline remain untested. The raw evidence preserves initial broker failures and passing retries; the runner now uses a loopback hostname for brokers and runs RabbitMQ readiness under the service user to avoid a cookie permission race. No OOM flag was observed in successful probes; startup peaks and host swap growth were not measured.

No public ports, external service connections or customer records were used. Containers and their newly created anonymous volumes were removed; downloaded images remain cached. The application's runnable Go/Compose configuration and Kubernetes are still undelivered, so application/integrated capacity cannot be verified by these probes.

## Resource envelope to verify

These are initial engineering allocations for a small synthetic-data experiment, not vendor minimums, delivered configuration or measured demand. Include process overhead and page cache when comparing the whole host. Pin versions and adjust allocations from measurements in BAP-010/011/013/019/020.

| Component | Initial RAM allowance (MiB) | Storage / CPU considerations |
| --- | ---: | --- |
| Frontend proxy, Go API and worker together | 512 | Bound concurrency and DB connections; build outside the runtime test |
| PostgreSQL | 512 | Persistent volume; cap connections; measure WAL and indexes |
| Redis | 256 | Set bounded memory/eviction; security state needs a separate failure policy |
| RabbitMQ | 512 | Persistent messages; bound queue backlog and disk usage |
| Kafka, one development broker/controller | 1,536 | Includes heap plus initial overhead allowance; cap retention; no HA claim |
| Collector, Prometheus and Grafana together | 1,024 | Cap metric cardinality and retention |
| Linux VM / local Kubernetes overhead | 1,536 | Measure separately from application services |
| Total experiment allowance | 5,888 | Excludes Windows, browser/editor, builds and safety headroom |

The current free-memory readings are below this proposed envelope. Docker exposes about 3,818 MiB total VM RAM; the proposed application/services allowance alone is 4,352 MiB before the 1,536 MiB VM/Kubernetes overhead row. The current VM cannot accommodate that entire proposed allocation simultaneously. These allowances are not measured minimum requirements, so this does not prove a tuned integrated stack cannot run on this hardware. Do not automatically enlarge the VM while host headroom is low. Use focused service profiles during development and measure actual demand before revising the envelope. The final integrated drill must include all required technologies. Do not count separately passing profiles as simultaneous full-stack evidence. Compose and Kubernetes exercises should run sequentially rather than duplicate every service on this host.

## Provider and cost evidence

Official sources were accessed on 20 October 2026. Recheck console entitlements and terms before selection; no provider account was inspected. Dollar values below are USD; no paid plan, domain purchase or currency conversion is approved.

| Candidate | Verified restrictions / price evidence | Assessment |
| --- | --- | --- |
| Render free web + free database | $0 subject to limits; web sleeps after 15 idle minutes and wakes in about a minute. Web filesystem is ephemeral; persistent disks require payment. Free PostgreSQL is 1 GB, expires after 30 days and has no backups. 750 web instance-hours/workspace/month. Excess bandwidth can cause charges with a payment method or suspension without one. [Free service rules](https://render.com/docs/free), [disks](https://render.com/docs/disks). | Reject as durable public payment deployment. A demo does not establish callback reliability or recovery. Account/card eligibility not tested. |
| Render free web + Neon free PostgreSQL | Neon: $0 without a card, 100 CU-hours/project/month, 1 GB database storage, 5 GB object storage and 5 GB public transfer. Compute sleeps after five idle minutes; restore history is up to six hours or 1 GB of changes. [Pricing](https://neon.com/pricing). | Candidate for intermittent synthetic demos only. Render sleep remains; no demonstrated continuous worker/broker placement or 30-day backup scheme. Neon storage is not a substitute for a tested off-host backup. At 0.25 CU continuously, 730 hours would consume 182.5 CU-hours, above the free allowance (calculation, not observed use). |
| Existing Oracle Always Free candidate | Always Free A1: 1,500 OCPU-hours and 9,000 GB-hours/month, equivalent to 2 OCPUs / 12 GB total. Combined boot/block storage: 200 GB; five volume backups. Always-Free-only object storage: 20 GB combined, 50,000 API requests/month. Home-region capacity can be unavailable; idle compute may be reclaimed. [Resource rules](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm). Signup needs a qualifying card, may place temporary authorization holds, and Free Tier has no SLA. [FAQ](https://www.oracle.com/cloud/free/faq/). | Best candidate to investigate, not an approved deployment. Owner confirms Always Free account status, but region, allocation, per-resource eligibility, architecture compatibility, HTTPS hostname, egress entitlement, independent backups and recovery are unverified. |
| Existing laptop + temporary tunnel | Existing hardware does not establish zero incremental electricity/connectivity cost. Quick Tunnels require no account/domain, generate temporary hostnames and have no uptime guarantee. [Cloudflare Quick Tunnels](https://developers.cloudflare.com/tunnel/get-started/quick-tunnels/). | Supervised sandbox callback experiment only. Laptop sleep, power, network failure, RAM pressure and hostname changes block a public production recommendation. No tunnel was opened. |

For an Oracle candidate, use the VM's supported CPU architecture and verify every pinned image. The candidate layout is a single VM with a same-origin HTTPS frontend/API, worker and private stateful services, with durable volumes and encrypted database backups copied outside that VM. A single VM is a failure domain, not HA. Full-product resource feasibility must be measured before choosing whether any component stays local only; local-only brokers cannot serve required public workflows.

### Registry, assets and cost controls

Recommend GHCR for immutable release images, conditional on repository-owner review and implementation in BAP-014. Container registry storage/bandwidth is currently free; public images support anonymous pulls. [GitHub Packages billing](https://docs.github.com/en/billing/concepts/product-billing/github-packages), [registry access](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry). No image was published. Image layers must contain no credentials, customer records or database backups.

The repository is public. Standard GitHub-hosted runner compute is free for public repositories; larger runners are charged. Artifact/cache storage has separate limits. Use standard runners and bounded retention; verify billing limits rather than treating all CI storage as free. [Actions billing](https://docs.github.com/en/billing/concepts/product-billing/github-actions).

Ship existing catalog images with the frontend release artifact. Future mutable product uploads need a selected durable volume/object store, access policy, backup and restore path. Never place uploads on an ephemeral web filesystem. A free platform HTTPS hostname can avoid a domain purchase for experiments; no stable production hostname/certificate path has been tested for the Oracle candidate.

No public deployment currently has a verified all-in $0 recurring cost. Include compute, database, volumes, backups, assets, egress, registry, CI retention, DNS/TLS and incremental local power/connectivity in the final bill of materials. Do not upgrade the trial to paid or provision paid resources to bypass capacity restrictions. Budget alerts alone do not establish a hard spending cap.

## Staged environment and operating plan

Accountable technical operator: project developer, as assigned in R01. The merchant owns account/budget and payment-provider decisions. These are role assignments from the approved record; a named responder, contact route and availability commitment remain to be recorded before public operation. Next review: 23 October 2026.

| Stage | Placement and entry gate | Data / public access | Operator and exit evidence |
| --- | --- | --- | --- |
| Local focused development | Laptop; Docker engine smoke passed; select supported Go toolchain and implement Compose in BAP-010 | Synthetic records, fake provider, private service ports and dedicated local volumes | Developer; measured core/infrastructure profiles and restart persistence |
| Local integrated / Kubernetes drill | Same laptop only if measured headroom permits, otherwise verified alternative machine | Synthetic records; isolated namespaces/volumes/secrets; no public database/broker/metrics ports | Developer; simultaneous required stack, resource peaks, rollout/pod-loss and recovery evidence |
| Supervised sandbox | Local temporary HTTPS tunnel or eligible Oracle sandbox after eligibility/security checks | Sandbox credentials only; expose callback/API intentionally, restrict admin; exclude indexing | Developer runs and records; merchant supplies approved sandbox access. Measure callback delivery after idle and restart; preserve durable correlation/reconciliation |
| Public staging | Oracle candidate only after sustainable quota, stable HTTPS and isolation are demonstrated | Separate DB, volumes and credentials from production; fake/sandbox provider | Developer; load, restore, alert delivery and rollback evidence |
| Customer production | Blocked until hosting/recovery/security and merchant live-STK gates pass | Production credentials; protected admin; minimum necessary customer data | Developer operates infrastructure; merchant authorizes live-payment verification and launch. Record named response coverage before opening traffic |

Operational signals required at staging: public synthetic reachability, callback failures/latency, unresolved payment age, DB storage/connections, queue age, Kafka lag, CPU/RAM/OOM, disk pressure, backup age and failed backup/restore. Route actionable alerts to the developer; define merchant escalation for payment discrepancies and downtime before launch. An installed dashboard without tested alert delivery is insufficient.

Backups must survive VM/laptop loss, be encrypted with separately recoverable keys, expire under the approved retention policy, and restore into an isolated environment. Determine base-backup/WAL or equivalent design from measured volume; five provider snapshots or a short managed history window do not establish 30-day retention and the RPO/RTO targets. Record last recoverable transaction time and total restore/reconciliation duration. Never test with live customer/payment data here.

## Remaining checks and closure evidence

| Gate | Current evidence / blocker | Responsible follow-up |
| --- | --- | --- |
| Feasibility/cost/resource evidence | Host inventory, engine smoke and seven individual service baseline/restart checks recorded; integrated/application capacity unmeasured | Developer: measure simultaneous profiles and application workloads when runnable implementation exists |
| Staged plan and accountable operator | Plan above; developer owns technical operations under R01 | Developer and merchant: record named responder/contact and confirm public operating coverage before launch |
| Explicit public-hosting blocker | No measured, post-trial sustainable public topology or recoverable deployment | Developer: inspect existing Oracle account entitlements; merchant supplies non-secret account facts |
| Delivery PR and validation | Documentation, standalone probe script and raw local evidence delivered for PR review; no application load test or off-host recovery drill yet | Developer: link the PR to #9; carry deferred checks into the implementation gates below |
| Schema/config/API and migration/rollback | No application schema/config/API or migration change. A standalone measurement script creates disposable local probe configuration | Rollback: revert assessment files; remove only owned test containers/volumes; downloaded images remain cached |
| Authorization, secrets/PII and signals | Host/provider research and isolated synthetic Docker probes; no real secrets or customer records accessed; required signals above | Developer: maintain redacted evidence; public exposure/credentials remain gated |

Next execution steps:

1. Owner now confirms Always Free account status. Record home region, resource shapes/OCPUs/RAM, boot/data volume totals, used/free quotas and exact Always Free labels from the console. Share only these non-secret facts. Account status alone does not prove eligible resource availability.
2. Engine availability is now verified: server reads, running WSL2 and bounded container execution passed. Retain the measured VM limit, establish sufficient desktop headroom and avoid configuration changes based solely on the earlier CIM virtualization flag. No automatic install, reboot or resource configuration change is part of this report.
3. Individual service idle baselines and narrow restart checks are complete. Once runnable implementation exists, measure core, broker/observability and simultaneous integrated profiles under representative workloads. Capture host and per-container CPU, memory/peak, disk growth, swap/OOM, restart persistence and workload duration. Use `docker stats --no-stream` plus repeated sampling and inspect restart/OOM state; use Kubernetes metrics only when a metrics server is installed. Proposed pass gate: no OOM/restart loop or sustained swap growth and enough host headroom to remain usable; agree numerical headroom before running the drill.
4. For any provisioned Oracle sandbox, record actual allocations and billing eligibility, verify TLS and no-charge callback reachability after idle/restart, bound all private ports, and measure the candidate stack. Keep recovery/load tests and merchant live-payment verification assigned to BAP-017/018/021; this assessment must identify their feasibility and dependencies without claiming those later tests passed.
5. Close #9 through the reviewed assessment PR with local evidence and the explicit public-hosting blocker recorded. The owner's 20 October direction defers remaining verification to implementation; it does not establish successful capacity tests or cloud eligibility. Update this report and ADR as evidence arrives, retaining the local/sandbox/production distinction.

Deferred verification ownership: actual Oracle quotas and eligible resource placement belong to the developer's hosting work before provisioning/staging; integrated resource measurements belong to BAP-010/011/013/019/020 as runnable services arrive; representative application load belongs to BAP-018; off-host recovery belongs to BAP-017; public HTTPS, callbacks and launch sign-off belong to BAP-021. Review open feasibility risks on 23 October 2026 under R01. None of these gates is satisfied merely by merging the assessment.

Existing implementation ownership remains BAP-010 (containers), BAP-011 (Redis/RabbitMQ), BAP-013 (signals), BAP-014 (CI/registry), BAP-017 (recovery), BAP-018 (load), BAP-019 (Kafka), BAP-020 (Kubernetes), and BAP-021 (launch). No required technology or payment/recovery gate is waived.

### Non-secret Oracle inspection worksheet

Use the signed-in console to inspect the following; this step needs no resource creation or paid-account upgrade. Do not paste credentials, account/tenancy identifiers, public IPs, billing-card details or connection strings.

| Field | Current evidence |
| --- | --- |
| Home region | Not supplied |
| Account type | Always Free, owner-confirmed in follow-up; console not inspected |
| Trial expiry | Previously reported trial; owner now reports Always Free |
| Existing VMs: shape, architecture, OCPUs, RAM, Always Free eligibility | Not inspected; no measurements supplied |
| Boot + data volume allocation and backup count | Not inspected |
| Object storage used and applicable post-trial entitlement | Not inspected |
| A1 quota: limit, used, available | Not inspected; advertised allowance is not actual capacity |
| Available Always Free capacity in home region | Not tested; avoid provisioning solely to fill this worksheet |
| Post-trial resource disposition | Unverified; classify every existing resource as eligible, paid/trial-only or unresolved |

### Documentation validation

On 20 October, documentation whitespace/link checks and PowerShell script parsing passed. Actual probe runs supplied the seven passing individual results and retained broker failures/retries. Self-review checked the report against #9 and R01: inventory and idle baselines are labeled separately from integrated capacity, trial funding is excluded from a sustainable cost claim, and open evidence gates remain visible. Application load and off-host recovery drills remain unperformed; missing assessment evidence remains visible above.
