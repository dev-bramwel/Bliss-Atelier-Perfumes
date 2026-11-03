# R03 individual service results

Measured: 20 October 2026. Evidence: local, isolated, sequential probes on Docker Desktop/WSL2 (8 logical CPUs, about 3.73 GiB VM RAM). Each service had one CPU and a RAM limit; no container swap or published ports.

| Service    | Ready (seconds) | Sampled idle RAM (MiB, min-max) | Highest sampled CPU | RAM limit | Restart | Evidence                           |
| ---------- | --------------: | ------------------------------: | ------------------: | --------- | ------- | ---------------------------------- |
| postgres   |            5.59 |                     30.95-31.01 |               0.46% | 512m      | passed  | [raw](r03-service-baselines.json)  |
| redis      |            0.95 |                     4.551-4.672 |               1.31% | 256m      | passed  | [raw](r03-service-baselines.json)  |
| rabbitmq   |           16.23 |                     127.1-145.9 |               1.08% | 512m      | passed  | [raw](r03-rabbitmq-corrected.json) |
| kafka      |              32 |                     341.1-341.9 |               8.77% | 1536m     | passed  | [raw](r03-service-retries.json)    |
| prometheus |            0.98 |                     17.34-17.36 |               0.11% | 512m      | passed  | [raw](r03-service-baselines.json)  |
| grafana    |           11.09 |                     81.02-81.02 |               0.72% | 384m      | passed  | [raw](r03-service-baselines.json)  |
| collector  |            5.77 |                      17.75-18.5 |               3.57% | 128m      | passed  | [raw](r03-service-baselines.json)  |

Five samples per service, after readiness and before the tiny data check; these are observed idle values, not true peaks or minimum RAM requirements. Startup timing excludes image downloads. Collector readiness is process survival only; no end-to-end telemetry pipeline was tested. See the [method and repeat commands](README.md) and [probe script](../../scripts/Measure-ServiceBaseline.ps1).

PostgreSQL retained 1,000 synthetic rows and Redis retained an AOF-backed marker across same-container restart. Kafka retained topic metadata; no Kafka message or RabbitMQ message durability was tested. Prometheus, Grafana, RabbitMQ and Collector passed restart/readiness without persisted-data assertions. Successful probes recorded no OOM-kill flag before or after restart and no automatic restart before the intentional restart. No host/VM swap-growth test was performed.

The [initial pass](r03-service-baselines.json) preserves failed RabbitMQ and Kafka checks. A [broker retry](r03-service-retries.json) added explicit localhost hostnames: Kafka passed, while RabbitMQ reported an Erlang cookie-file permission error and no OOM kill. The [corrected RabbitMQ pass](r03-rabbitmq-corrected.json) ran readiness as the rabbitmq user rather than root and passed. This is consistent with a readiness-command cookie ownership race; no separate root-cause reproduction was performed. The initial Kafka failure has no retained diagnostic log; the successful hostname-adjusted retry does not prove its original cause.

The current script includes both probe fixes. The initial-pass process exited nonzero for its two failed probes; the broker retry exited nonzero for RabbitMQ; the corrected RabbitMQ run exited zero. Failed results remain visible alongside passing retries.

All test containers and newly created anonymous volumes were removed. Downloaded images remain cached. Existing unrelated images/build cache were left alone. No cloud resources, application configuration, public endpoints or billing settings were changed.

These results support individual-service development on this machine. They do not validate simultaneous operation, startup peaks, sustained workloads, the Go application, Kubernetes, public hosting, payment callbacks, alert delivery or the approved traffic/recovery targets. Do not size production from the sum of idle memory samples.
