# Runbook: API Error Surge & Latency Degradation

**Alerts:** `ApiHigh5xxRate`, `ApiHighLatency`  
**Severity:** Critical (P1)  
**Responsible Owner:** On-Call SRE / Backend Engineer  
**Grafana Dashboard:** [Bliss Atelier - System & API Overview](http://localhost:3000/d/bliss-system-overview)  

---

## 1. Problem Overview & Invariants

An error surge or latency degradation indicates unhandled server crashes, upstream timeout, or resource exhaustion:
- `ApiHigh5xxRate`: Over 5% of all inbound API requests are returning HTTP 500, 502, 503, or 504.
- `ApiHighLatency`: The 95th percentile request execution latency has exceeded 1.0 second.

> [!CRITICAL]
> **Strict Operational Invariants:**
> 1. **Structured Traceability**: Every error log emitted contains a `correlation_id` matching the response header `X-Correlation-ID`.
> 2. **Never expose internal stack traces or database errors to clients**: The API returns clean JSON error objects with non-leaking error messages.

---

## 2. Immediate Triage & Diagnosis

### Step 1: Identify Offending Route Templates on Grafana
Check which route template is driving the error spike:
- Route label on `http_requests_total{status=~"5.."}` (e.g. `/api/orders`, `/api/catalog`, `/api/orders/{id}/delivery-quote`).

### Step 2: Extract Correlation IDs from Recent 5xx Logs
```bash
docker compose logs api --tail=100 | grep -E '"level":"ERROR"'
```
Copy a sample `correlation_id` (e.g. `c_7f3a9b1c...`).

### Step 3: Trace End-to-End Context for that Correlation ID
```bash
docker compose logs api | grep "c_7f3a9b1c"
```
Review the exact failure stage (DB query timeout, JSON unmarshal error, upstream provider failure).

---

## 3. Remediation & Safe Actions

### Scenario A: Database Outage or Lock Contention
If logs indicate `dial tcp: connection refused` or query timeouts:
1. Check PostgreSQL health:
   ```bash
   docker compose ps db
   ```
2. Refer to [PostgreSQL Connection Pool Saturation Runbook](runbook-database-pool-exhaustion.md).

### Scenario B: Payment Provider Gateway Outage
If `POST /api/orders` or `.../retry-payment` is failing due to Daraja/NCBA provider timeouts:
1. Verify if the provider is returning HTTP 504 or connection reset.
2. Orders will safely remain in `PENDING` with active 15-minute stock reservations, allowing safe retry when provider recovers.

### Scenario C: Bad Application Deployment Rollback
If errors began immediately following a container update:
1. Revert to the last known good image digest:
   ```bash
   git checkout HEAD~1
   docker compose build api frontend
   docker compose up -d api frontend
   ```

---

## 4. Verification & Recovery Confirmation

1. Inbound 5xx rate drops below 1% on Grafana.
2. p95 latency returns under 200ms.
3. Alertmanager resolves `ApiHigh5xxRate` and `ApiHighLatency`.
