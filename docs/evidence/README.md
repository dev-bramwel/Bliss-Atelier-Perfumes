# R03 local service baselines

The [probe runner](../../scripts/Measure-ServiceBaseline.ps1) starts one service at a time with one CPU, a service-specific RAM limit, a combined memory-plus-swap limit equal to that RAM limit (container swap disabled), no external networking and no published ports. It uses synthetic data and removes only its own labeled containers and their newly created anonymous volumes. Downloaded images remain cached. It does not change Docker Desktop's resource allocation or touch other containers/images.

Run from the repository root in Windows PowerShell with Docker Desktop running:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/Measure-ServiceBaseline.ps1
```

To run selected services without overwriting the full report, invoke the script from PowerShell with a distinct output file:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "& './scripts/Measure-ServiceBaseline.ps1' -Service @('postgres','redis') -OutputPath 'docs/evidence/r03-core-repeat.json'"
```

The execution-policy flag applies only to that PowerShell process; it does not change the machine policy. It is needed on this host, where direct script invocation is disabled. The default report is [r03-service-baselines.json](r03-service-baselines.json). Each successful pull resolves the input tag to a digest; the container runs that digest. These versions are experiment inputs, not an approved production dependency set. Image pull time is recorded separately from startup readiness time. A run updates the output file after each completed service and returns a nonzero exit code if any probe fails. Use a separate output path when preserving prior evidence.

Each service gets five `docker stats --no-stream` readings, separated by two-second pauses, after initial readiness and before the tiny data check. Docker command execution adds to the interval; the samples cover idle behavior, not startup peaks, a soak or application load. CPU percentages are Docker's reported percentages; 100 percent represents approximately one logical CPU. Memory is Docker's reported working-set-style usage and does not represent total VM/host memory demand. Block I/O counters are cumulative since container start, not throughput measurements.

Readiness uses PostgreSQL `pg_isready`, Redis `PING`, RabbitMQ `check_running`, Kafka topic listing, Prometheus `/-/ready`, and Grafana `/api/health`. Collector readiness is only process survival; there is no configured end-to-end telemetry exercise. After sampling, the runner adds 1,000 PostgreSQL rows, one Redis AOF-backed marker, or Kafka topic metadata, then restarts and verifies them. Other services get readiness checks after restart without a persisted-data assertion. `postRestartReadySeconds` starts after `docker restart` completes, so it is not total downtime/RTO.

The synthetic passwords are deliberately non-secret, local-only probe values. They must not be reused in a public or shared deployment. No real credentials, customer records, account identifiers or public IPs belong in this evidence directory.

Limitations: same-container restart is not container recreation, volume loss, off-host restore or recovery of payments. Kafka message delivery, RabbitMQ durable message recovery, Redis production failure policy, security hardening, broker replication, retention growth, alert delivery and application traffic remain untested. The Go API/worker/frontend and Kubernetes still need their own executable implementation and measurement. Adding idle measurements cannot establish that all services fit simultaneously or that the approved traffic/RPO/RTO gates pass.

Reference documentation for the probe commands/images: [PostgreSQL image](https://hub.docker.com/_/postgres), [Redis image](https://hub.docker.com/_/redis), [RabbitMQ monitoring](https://www.rabbitmq.com/docs/monitoring), [Kafka Docker](https://kafka.apache.org/40/getting-started/docker/), [Prometheus Docker](https://prometheus.io/docs/prometheus/latest/installation/), [Grafana Docker](https://grafana.com/docs/grafana/latest/setup-grafana/installation/docker/), [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/install/).
