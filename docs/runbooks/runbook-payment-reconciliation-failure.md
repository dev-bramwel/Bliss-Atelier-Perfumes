# Runbook: Payment Reconciliation & Exception Triage

**Alerts:** `UnresolvedPaymentSpike`, `PaymentExceptionDetected`  
**Severity:** Critical (P1)  
**Responsible Owner:** On-Call SRE / Finance Ops / Store Manager  
**Grafana Dashboard:** [Bliss Atelier - Financial & Payment Operations](http://localhost:3000/d/bliss-financial-operations)

---

## 1. Problem Overview & Invariants

When M-Pesa STK push callbacks drop, NCBA IPN notifications time out, or customers pay an incorrect amount, payments transition into `unresolved` or raise actionable `payment_exceptions`:

- `AMOUNT_MISMATCH`: Customer paid different amount than expected order total.
- `UNMAPPED_TRANSACTION`: Bank/M-Pesa IPN arrived but cannot be correlated to any active checkout attempt.
- `DUPLICATE_RECEIPT`: Provider receipt number replayed on an already settled order.
- `STALE_ATTEMPT_UNRESOLVED`: Reconciler attempted maximum query retries without definitive status.

> [!CRITICAL]
> **Strict Operational Invariants:**
>
> 1. **Never initiate a second charge blindly**: Do not retry STK pushes without verifying existing attempt status.
> 2. **Never edit `Order.paymentStatus` directly via ad-hoc SQL**: All transitions require an active stock reservation or formal payment exception resolution drawer.
> 3. **Preserve audit trail**: Every exception resolution requires an immutable record in `public.payment_exception_resolution` and `public.admin_audit_log`.

---

## 2. Immediate Triage & Diagnosis

### Step 1: Check Active Exceptions via API

Run authenticated query to inspect open exceptions:

```bash
curl -s -X GET "http://localhost:8080/api/admin/payments/exceptions?status=open" \
  -H "Cookie: bliss_session=<manager_session_token>"
```

### Step 2: Query Unresolved Attempts Queue

```bash
curl -s -X GET "http://localhost:8080/api/admin/payments/unresolved" \
  -H "Cookie: bliss_session=<manager_session_token>"
```

### Step 3: Inspect Reconciler Structured Logs

Check API container logs for correlation IDs and provider transaction references:

```bash
docker compose logs api --tail=100 | grep -E "reconciler|ncba_ipn|mpesa_callback"
```

---

## 3. Remediation & Safe Actions

### Action A: Trigger Immediate Automated Query Reconciliation

If unresolved attempts are stuck due to transient provider timeouts, trigger a fresh sweep:

```bash
curl -s -X POST "http://localhost:8080/api/admin/payments/reconcile" \
  -H "Cookie: bliss_session=<manager_session_token>" \
  -H "X-CSRF-Token: <csrf_token>"
```

### Action B: Resolving Amount Mismatch (`AMOUNT_MISMATCH`)

1. In the Admin Dashboard (`/admin/payments`), open the **Payment Exceptions Drawer**.
2. Compare the `ReceivedAmountKes` against `ExpectedAmountKes`.
3. If customer underpaid:
   - Select resolution `applied_to_order` or `refund_recorded`.
   - Contact customer via WhatsApp (+254...) to collect remaining balance or issue store credit.
4. If customer overpaid:
   - Select `applied_to_order` and record notes for excess refund via M-Pesa reversal.

### Action C: Manual Settlement for Verified Physical / Bank Counter Payments

If a customer paid via offline direct Paybill or counter Till:

```bash
curl -s -X POST "http://localhost:8080/api/admin/payments/manual-settlement" \
  -H "Cookie: bliss_session=<manager_session_token>" \
  -H "X-CSRF-Token: <csrf_token>" \
  -H "Content-Type: application/json" \
  -d '{
    "order_id": "<order_id>",
    "amount_kes": 2500,
    "receipt_number": "<provider_receipt_id>",
    "evidence": "Customer SMS screenshot verified by counter staff",
    "notes": "Direct counter Paybill payment"
  }'
```

---

## 4. Verification & Recovery Confirmation

1. Verify `unresolved_payments_gauge` drops to 0 on Grafana dashboard.
2. Confirm the affected order has transitioned to `PAID` with stock reservation committed.
3. Verify the alert in Alertmanager transitions to `resolved`.
