# Legacy contract observations

Synthetic fixtures captured from `src/server.js`, `src/routes/orders.js`, `src/routes/mpesa.js` and the consumer `frontend/checkout.js`. `test/legacy-fixtures.test.js` executes legacy pricing against the checkout and order fixtures. Response envelopes are source-derived observations, not proof that Go order routes exist.

`created` is the HTTP 201 envelope; combine its order with `initiationFailure` for HTTP 202 after provider failure. Combine its order and STK result with `mappingFailure` for HTTP 202 after mapping failure. Neither 201 nor 202 means payment verified. Status fixtures retain nullable fields and opaque CUID strings. Callback success returns HTTP 200 with provider field casing.

The 23 catalog entries and IDs p1–p23 are mock stock, not the merchant's real products or inventory. They are retained here only to characterize the existing code. These fixtures neither seed the future catalog nor require real inventory to retain these mock IDs.

Required compatibility: camelCase names, opaque order IDs, integer server-authoritative amounts, saved-order warnings, nullability and timestamp representation. Mock product-ID coverage applies only to the legacy fixture suite.

Historical policy differences: legacy KES 2,000 prices and `on_delivery` acceptance/default are observations only. R01 requires KES 2,500 and prohibits payment on delivery. Coordinate catalog/checkout/frontend changes before cutover.

Excluded from parity: malformed customer acceptance, unscoped status lookup, unchecked provider acceptance, unbounded IO, sensitive logs, PAID downgrades, replayed charges and browser backup imports. Callback signatures correlate requests and do not establish verified settlement. No real customer data, credentials or receipts are included.
