# Runbook: PostgreSQL Connection Pool Saturation

**Alert:** `DbPoolSaturation`  
**Severity:** Critical (P1)  
**Responsible Owner:** SRE / Database Administrator  
**Grafana Dashboard:** [Bliss Atelier - System & API Overview](http://localhost:3000/d/bliss-system-overview)

---

## 1. Problem Overview & Invariants

The Go API utilizes `jackc/pgx/v5/pgxpool` with a bounded connection limit (`GO_DB_MAX_CONNECTIONS`, default 16). When acquired connections exceed 85% of capacity:

- Inbound HTTP requests queue up waiting for an available connection.
- Requests eventually fail with `503 Service Unavailable` or `504 Gateway Timeout` once context deadlines expire.

> [!CRITICAL]
> **Strict Operational Invariants:**
>
> 1. **Do not inflate connection limits arbitrarily**: Increasing pool limits beyond database memory capacity will cause PostgreSQL OOM crashes.
> 2. **Ensure bounded query contexts**: Every query and transaction must respect request deadlines (`cfg.IOTimeout`, default 15s).

---

## 2. Immediate Triage & Diagnosis

### Step 1: Inspect Pool Metrics on Prometheus/Grafana

Check the current `db_pool_connections{state="acquired"}` vs `db_pool_max_connections`.

### Step 2: Query Active Connections & Queries in PostgreSQL

Execute diagnostic query against PostgreSQL:

```bash
docker compose exec db psql -U bliss_admin -d bliss_dev -c "
SELECT pid, usename, client_addr, state, now() - query_start AS duration, query
FROM pg_stat_activity
WHERE state != 'idle'
ORDER BY duration DESC
LIMIT 10;
"
```

### Step 3: Check for "idle in transaction" Leaks

```bash
docker compose exec db psql -U bliss_admin -d bliss_dev -c "
SELECT pid, usename, state, now() - state_change AS idle_duration, query
FROM pg_stat_activity
WHERE state = 'idle in transaction'
ORDER BY idle_duration DESC;
"
```

---

## 3. Remediation & Safe Actions

### Scenario A: Terminating an Orphaned or Blocking Backend Connection

If a runaway query or hung transaction is holding connections open:

```bash
docker compose exec db psql -U bliss_admin -d bliss_dev -c "
SELECT pg_terminate_backend(<pid_from_step_2>);
"
```

### Scenario B: Restarting Stalled Application Replica

If connection leaks persist in a specific container replica:

```bash
docker compose restart api
```

### Scenario C: Adjusting Pool Boundaries for Planned High Traffic

If legit customer traffic is saturating the pool:

1. Review host available RAM (`free -m` or Docker stats).
2. Update `GO_DB_MAX_CONNECTIONS` in `compose.yaml` (e.g. from 16 to 24).
3. Re-deploy container without downtime:
   ```bash
   docker compose up -d api
   ```

---

## 4. Verification & Recovery Confirmation

1. Acquired pool connections drop below 50% on Grafana.
2. API liveness probe `GET /api/health/ready` returns HTTP 200 `{"status":"ready"}`.
3. Alertmanager resolves `DbPoolSaturation`.
