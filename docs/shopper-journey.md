# R20 shopper checkout and resume journey

Issue #26 adds `/order/:id` and a mobile checkout that retains an order until its payment is confirmed. Payment before dispatch remains the approved policy; pay on delivery is deliberately unavailable, as confirmed by the user.

The client reads `/api/auth/me`, keeps CSRF only in memory and uses authoritative session cookies for authenticated customers. Migration 0011 adds customer ownership to `guest_order_access`; browser-supplied user IDs cannot set ownership. Guests retain a separate token for each order in local storage and send `X-Guest-Token`. Tokens never appear in links. Losing site storage loses guest access; signing in does not claim older guest orders automatically.

A separate random 256-bit `X-Checkout-Token` binds guest creation retries to this browser across Wi-Fi/mobile IP changes. Only its hash is stored in the idempotency caller scope. It grants no access to order tracking; that still requires an order-specific guest token or customer session. Legacy clients without this header retain the existing IP-scoped creation behavior.

Checkout sends the established nested `customer`/`items` request; the API also accepts the issue's flat customer fields and `quantity`. Prices and holds come from PostgreSQL. A durable UUID and exact request body survive ambiguous creation responses and reloads. The explicit **Recover saved order request** action resends that request with its original key. It does not substitute edited cart contents. The saved pending payload includes the contact information needed for recovery; it is removed after confirmed creation or a definitive validation response.

Delivery orders do not create an STK attempt before quote acceptance. Pickup creation initiates the initial attempt using the actual `pay_now` method. Versioned quote accept/reject actions use server totals, including the distinction between merchant collection and fees paid directly to a courier. The guarded pickup endpoint changes the same order without renewing its hold; paid orders, expired holds and unresolved payment attempts cannot be changed.

Payment reads use a non-overlapping 2.5-second loop. PAID/FAILED stop polling, pending requests resume after verified reconnection, and ambiguous attempts prevent new prompts. Payment retries keep their UUID after transport errors and use a new UUID only after a definitive result. A successful payment removes the purchased cart quantities while preserving products added later. Reservation countdowns use server response time and never extend offline holds.

The shared modal preserves DOM drafts, locks writes, checks `/api/health/live` every three seconds and restores controls after confirmation. A local WebM has an SVG fallback and reduced-motion support. Restoring a connection refreshes reads; no order, quote or payment write is automatically replayed.

Catalog responses now include unreserved stock counts. Redis catalog cache v2 avoids serving an older projection without stock. Browsing uses In Stock, low-stock and Sold Out badges and bounds cart quantities against fresh catalog stock.

Run `node web/shopper.browser.cjs` against the compiled web process at `127.0.0.1:8086` (override with `BLISS_WEB_URL`). It runs Chromium, Firefox and WebKit by default. `BLISS_BROWSER_ENGINES` can select a subset. WebKit provides Safari-engine coverage; this is not a claim of testing the actual Safari application on Apple hardware. Browser fixtures verify client behavior; backend HTTP and disposable PostgreSQL tests verify ownership, CSRF, payment guarding and same-order pickup. Docker Core CI executes both suites and retains screenshots, logs and generated CSS.

Apply schema migrations before deploying the updated runtime. This change builds on R19 / PR #83 and schema v10; it adds schema v11 without rewriting historical migrations. Existing guest orders remain accessible through their tokens.
