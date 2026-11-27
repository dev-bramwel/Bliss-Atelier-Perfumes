# Future Scope: Analytics, AI Insights, and Privacy Evolution

**Status:** Proposal / Future Review  
**Date:** 2026-11-27  
**Related Implementation:** Reconciled Sales Reports & Funnel (`feat/28-reconciled-sales-reports`)

---

## 1. Current Data Foundation

The system currently separates **authoritative business records** from **privacy-aware storefront telemetry**:

1. **Authoritative Operational & Financial Tables** (used by `internal/reports/store.go`):
   - `public."Order"` — `"totalKes"`, `"paymentStatus"` (`'PAID'` / `'PENDING'`), `"updatedAt"` (bucketed in `Africa/Nairobi`).
   - `public.delivery_quote` — `amount_kes`, `fee_recipient` (`'merchant'` vs. `'courier'`), `status = 'accepted'`.
   - `public.exchange_request` — `price_difference_kes`, `status = 'completed'`, `completed_at`.
   - `public.payment_audit_ledger` — `action`, `amount_kes`, `created_at`.
   - `public.stock_entry`, `public.stock_batch`, and `public.catalog_product` — `product_id`, `delta`, `balance`, `kind`, `actor`, `evidence`, `created_at`, and `name`.

2. **Privacy-Aware Storefront Funnel** (`public.analytics_funnel_event`):
   - Collects `event_id`, pseudonymous tab-scoped `session_id` (stored in browser `sessionStorage` under `"bliss.funnel.session"` in `web/static/shared.js`), `event_type` (`catalog_view`, `cart_add`, `checkout_start`, `payment_initiated`), optional `product_id`, and `occurred_at`.
   - Deliberately excludes customer PII (`customerName`, `customerPhone`, `deliveryLocation`).
   - Automatically purged after **90 days** (`DefaultFunnelRetention = 90 * 24 * time.Hour`).

3. **Lightweight Privacy Notice** (`web/layout.templ`, `web/static/shared.js`, `web/static/style.css`):
   - Displays a non-blocking footer disclosure and an accessible `#privacyModal` dialog explaining session-only analytics (no advertising cookies), 90-day purge, and order/delivery data usage.

---

## 2. Future AI Analysis Ready Without Cookies

Most high-value AI analysis for the store does **not** require HTTP tracking cookies or cross-session user profiling, because the strongest signals already exist in the current schema:

- **Demand Forecasting & Restock Timing:**
  - Combine settled `Order` volume, `stock_entry` deltas, `stock_batch` restock cadence, and `catalog_product` availability to predict stockouts and optimal reorder quantities per fragrance.
- **Margin, Delivery & Exchange Analysis:**
  - Analyze `delivery_quote` (`merchant` vs. `courier` fee splits), `exchange_request` frequency/top-ups, and `payment_audit_ledger` corrections to identify delivery bottlenecks, high-exchange products, or payment friction.
- **Aggregate Storefront Conversion & Product Add-to-Cart Interest:**
  - Use `analytics_funnel_event` (`catalog_view`, `cart_add`, `checkout_start`, `payment_initiated`) and settled `PAID` orders as separate aggregate series (as exposed by `FunnelSummary`), plus `product_id` on `cart_add` events to gauge per-product cart interest and time-of-day drop-offs. *(Note: `checkout_start` and `payment_initiated` currently send an empty `product_id`, and `Order` does not store the funnel `session_id`, so per-product checkout-to-order attribution is not yet supported by the current schema.)*

---

## 3. Recommended Future Works

### 3.1 Funnel-to-Order & Product-Level Checkout Attribution
- **Gap:** `cart_add` records `product_id`, whereas `checkout_start` and `payment_initiated` send an empty `product_id`, `Order` does not store the funnel `session_id`, and `FunnelSummary` returns only aggregate event counts alongside settled order totals.
- **Future Work:** If product-level or intra-session checkout-to-order conversion joins are needed, extend checkout/payment funnel events to include item/product context, store a pseudonymous `session_id` (or checkout correlation ID) on `Order`—without attaching PII to funnel events—and add product-grouped reporting queries.

### 3.2 Pre-Purge Daily Funnel Rollups (for Long-Term AI Training)
- **Gap:** `DefaultFunnelRetention` purges raw `analytics_funnel_event` rows after 90 days, which limits year-over-year or multi-quarter AI trend modeling on funnel behavior.
- **Future Work:** Add a scheduled daily aggregation job (before the 90-day purge runs) that writes anonymized daily summaries (e.g., `analytics_funnel_daily` keyed by `Africa/Nairobi` local date, `event_type`, and `product_id`, with total counts and distinct session counts).
- **Benefit:** Preserves multi-year conversion history for AI forecasting while still discarding raw `session_id` values after 90 days.

### 3.3 Cross-Session / Returning-Visitor Attribution (Optional, Only If Needed)
- **Current Behavior:** `sessionStorage` resets when the browser tab closes, so multi-day browse-then-buy journeys are not linked together.
- **Future Work (only if product goals require it):**
  - If future AI features need multi-visit cohort analysis or personalized fragrance recommendations across visits, introduce either a first-party persistent identifier or tie recommendations to authenticated customer profiles.
  - Keep first-party storage distinct from third-party advertising trackers.

### 3.4 Privacy & Consent Governance for AI Expansion
- **Data Isolation:** Keep customer PII (`customerName`, `customerPhone`, `deliveryLocation`) excluded from AI analytics/training pipelines unless a feature explicitly requires it and has clear legal/operational justification.
- **Consent & Notice Updates:**
  - The current lightweight Privacy Notice is appropriate while using ephemeral `sessionStorage` and aggregated operational reporting.
  - If persistent cookies/identifiers or customer-level AI profiling/personalization are introduced later, update `#privacyModal` in `web/layout.templ` and add an explicit consent/opt-in control where required by applicable data-protection rules (e.g., Kenya Data Protection Act / GDPR).
