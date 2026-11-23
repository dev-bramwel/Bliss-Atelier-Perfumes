# Runbook: Redis Cache Outage & Rate Limiter Fallback

**Alert:** `RedisOutageFallbackActive`  
**Severity:** Warning (P2)  
**Responsible Owner:** SRE / Security Engineer  
**Grafana Dashboard:** [Bliss Atelier - System & API Overview](http://localhost:3000/d/bliss-system-overview)

---

## 1. Problem Overview & Invariants

Bliss Atelier uses Redis 7 for:

1. **Catalog Read Cache**: Accelerates `/api/catalog` lookups.
2. **Session Role & Token Cache**: Caches verified Google OIDC sessions.
3. **Abuse & Rate Limiting**: Sliding window token bucket for API endpoints.

> [!CRITICAL]
> **Strict Operational Invariants:**
>
> 1. **Graceful Read Fallback**: Redis outages MUST NOT crash the storefront. Catalog queries automatically fall back to PostgreSQL.
> 2. **Security Fail-Safe Rate Limiting**: Attackers MUST NOT bypass rate limits by inducing a Redis outage. On Redis failure, the rate limiter falls back to an in-memory sliding window enforcing the exact same thresholds.

---

## 2. Immediate Triage & Diagnosis

### Step 1: Check Redis Health & Status

```bash
docker compose exec redis redis-cli ping
```

Expected output: `PONG`. If connection refused or timeout occurs, Redis is down.

### Step 2: Inspect Redis Memory & Eviction Status

```bash
docker compose exec redis redis-cli info memory
```

Check `used_memory_human` and `maxmemory_human` (64mb configured).

### Step 3: Check API Structured Logs

```bash
docker compose logs api --tail=50 | grep -E "rate_limiter_redis_fallback|catalog_cache"
```

---

## 3. Remediation & Safe Actions

### Scenario A: Redis Container Stopped or Crashed

Restart the Redis service:

```bash
docker compose restart redis
```

### Scenario B: Redis Memory Exhaustion (OOM)

If Redis maxmemory (64MB) is full and cannot evict keys under `volatile-lru`:

1. Flush expired keys or flush the cache (safe for read cache, sessions will re-authenticate against PostgreSQL):
   ```bash
   docker compose exec redis redis-cli flushdb
   ```
2. Verify Redis memory usage drops.

### Scenario C: Repopulating Catalog Cache

Once Redis is healthy, trigger an intentional cache warm/invalidation:

```bash
curl -s -X POST "http://localhost:8080/api/catalog/invalidate" \
  -H "Cookie: bliss_session=<manager_session>" \
  -H "X-CSRF-Token: <csrf_token>"
```

---

## 4. Verification & Recovery Confirmation

1. `redis_cache_operations_total{operation="hit"}` resumes climbing on Grafana.
2. `redis_cache_operations_total{operation="error"}` rate drops to 0.
3. Alertmanager resolves `RedisOutageFallbackActive`.
