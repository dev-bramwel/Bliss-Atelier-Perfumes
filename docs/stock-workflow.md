# Database catalog and stock workflow (R09)

Issue: [#15](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/15). Implementation plan: [issue-15-plan.md](issue-15-plan.md).

## Approved authority and availability

The owner selected replacement by this database in the project conversation on 22 October 2026. Opening counts will be supplied. No external stock integration is selected. All verified shop sales must be recorded here; do not keep another writable stock authority after cutover. During an outage, record sales in a controlled queue, keep online availability blocked, replay each verified sale using its original unique reference, reconcile physical counts, then obtain owner approval before reopening. This queue is an operator procedure, not an implemented durable queue or employee application.

The owner approved a reconciliation approval lasting at most 24 hours. An unconfirmed authority, unopened product, zero stock, expired approval or database failure prevents available stock being advertised. Imports and corrections invalidate approval; a verified local sale deducts immediately and preserves the existing expiry. An owner must approve again after a stock import/correction or expiry, after confirming all local sales are entered. Approval history is retained. The browser refreshes every 30 seconds and rejects actions after a 60-second local freshness limit; checkout refreshes again before submission. This is a catalog display policy, not a checkout stock reservation guarantee.

## Schema and API

Migration `0002_catalog.sql` imports all 23 original product IDs (`p1` through `p23`), names, categories, slugs, descriptions and existing local media mappings. It creates catalog settings, products, stock control, batches, entries and approval history. Opening quantities are deliberately not seeded. Prices are integer KES, using the approved common KES 2,500 price unless a product has an individual override. Changing these values never rewrites historical order JSON/totals.

`GET /catalog` on Go, exposed as `GET /api/catalog` by the local web proxy, returns `{products: [...]}` with metadata, `priceKes` and `available`. Responses use `Cache-Control: no-store`. Database errors return sanitized HTTP 503 with no stale/hardcoded price fallback. Stock quantities, operator labels and evidence references are not public. There is no public stock-write HTTP endpoint.

The Go API runtime receives SELECT only on catalog products/settings/control. Migration ownership and stock operator credentials must not be mounted into the API. The existing Order/MpesaTransaction permissions remain for the foundation contract; this does not enable Go checkout.

## Operator setup

Use a private `GO_DATABASE_URL` environment variable for the operator role. Never paste credentials into command arguments, logs or committed files. The CLI uses the same bounded PostgreSQL connection helper and a 15-second operation deadline. Run commands from `backend/` (or build `go build -o stock ./cmd/stock`). A prepared operator role needs these grants, provisioned by a database administrator:

```sql
GRANT USAGE ON SCHEMA public TO bliss_stock_operator;
GRANT SELECT ON public.catalog_product, public.stock_control,
 public.stock_batch, public.stock_entry, public.stock_approval TO bliss_stock_operator;
GRANT UPDATE(stock,opened) ON public.catalog_product TO bliss_stock_operator;
GRANT UPDATE ON public.stock_control TO bliss_stock_operator;
GRANT INSERT ON public.stock_batch, public.stock_entry, public.stock_approval TO bliss_stock_operator;
```

Create the login and securely distribute its credential separately. This trusted owner/operator role can assert approval and record verified sales; `actor` is an audit label, not authentication. Employees must not receive this role. Employee-scoped login, payment-evidence access, sales review and an entry UI remain later roadmap work. Use opaque staff IDs and references to restricted evidence, not phone numbers, receipt payloads or customer names in input/audit fields. No runtime role may mutate stock or read approval history.

Owner-only pricing changes currently use reviewed SQL in a transaction, for example `UPDATE public.catalog_settings SET common_price_kes=2500 WHERE singleton;` or `UPDATE public.catalog_product SET price_override_kes=3000 WHERE id='p1';`. Set an override to NULL to restore common pricing. The employee/operator grants above do not permit pricing changes. The administration UI and pricing audit controls remain their own roadmap deliverables.

## Opening and ongoing operation

1. Complete `docs/templates/opening-stock.json` with all 23 signed-off physical quantities. Its null quantities are intentional placeholders and are rejected; zero must be explicit. Preserve IDs, use a stable unique batch ID, an opaque actor ID, and a reference to the approved count/discrepancy record. Stop other stock writers while counting and importing. The owner must sign off any discrepancy before approval.
2. Prepare a private approval JSON document: `{"actor":"owner-id","evidence":"authority-and-count-signoff-reference","until":"<RFC3339 timestamp within the next 24 hours>"}`. Include a timezone offset or UTC Z. Run `Get-Content approval.json -Raw | go run ./cmd/stock approve-replacement`. First approval selects replacement mode; unopened products remain unavailable.
3. Run `Get-Content opening-stock.json -Raw | go run ./cmd/stock apply`. Import must map every catalog product exactly once. It sets the opening balances atomically and invalidates approval. Run `go run ./cmd/stock report` and compare physical counts, `stock`, ledger totals and signed-off source. Then issue renewed owner approval to publish availability.
4. Record each verified shop sale as a `local_sale` batch. Example: `{"id":"sale-unique-reference","kind":"local_sale","actor":"operator-id","evidence":"verified-sale-reference","lines":[{"productId":"p1","quantity":1}]}`. Pipe the JSON to `stock apply`. Only record after payment verification. Admin review is not another stock event; it must not deduct stock again.
5. Replenishment uses `receipt` with positive units. Correction uses `adjustment` with a signed delta and `expectedBalance`. Full physical recount uses `reconcile`, includes all 23 products, counted quantities and each `expectedBalance`. Example correction line: `{"productId":"p1","quantity":-1,"expectedBalance":4}`. A concurrent change rejects the whole correction/recount: investigate and recount, then submit a new reference. Never edit prior ledger entries. Import/correction requires renewed approval.
6. Inspect `stock report`: it returns balances, `ledgerMatches`, authority and reconciliation expiry. A ledger mismatch exits nonzero and requires blocking availability and investigation. Approval also rejects a mismatch. Monitor expiry, failed imports and sanitized `catalog_unavailable` server events. No scheduled alerting is delivered here.

Use an immutable unique ID for every real event. An identical retry (including actor/evidence, independent of line ordering) returns `replayed:true` and adds no stock. Reusing an ID with changed content fails. Retrying opening under a new ID also fails. Unknown IDs, duplicate mappings, missing/null/fractional quantities, negative counts, invalid expected balances and overselling fail atomically. Batches serialize on the control row, so concurrent last-bottle sales cannot both succeed. On uncertain commit, retry the exact original ID and content. Do not invent a new ID to recover from a timeout.

## Migration, rollback and current limits

Back up first using the [SQL runbook](sql-migrations.md). Existing Go v1 databases: run `migrate status` (reports pending), then `migrate up`. Empty databases require explicit `init`; verified Prisma databases require `adopt-legacy`. Both apply v2. Historical migration 0001 and its checksum remain unchanged. Migration validates schema for the applied version, takes existing advisory locks, and commits new tables/seed/history atomically. The pinned PostgreSQL 17.11 schema contract is checked. Unknown/missing schema or checksum mismatch fails safely. Local devstack grants SELECT for catalog runtime and attests both migration checksums. No additional environment setting is introduced.

Rollback is application rollback with schema/ledger retained; do not drop stock tables after real events exist. The previous Go runtime's schema readiness contract expects v1 and will fail against v2. A rollback must therefore be a compatible application build retaining v2 readiness/attestation; disabling readiness is not an approved workaround. Forward repair is preferred. Restore a verified pre-upgrade backup only during a controlled outage with explicit acceptance/replay of post-backup events. Never rerun legacy CREATE TABLE or rewrite migration checksums.

Express still has the historical KES 2,000 order map and no stock reservation integration. The local Go proxy still returns 404 for order/payment routes. Do not route this catalog to legacy live checkout or enable production selling: atomic online reservations/payment settlement and authenticated employee sales remain later delivery slices. This work implements the catalog and trusted operator stock replacement workflow; it does not claim the full commerce cutover. No real opening import, physical discrepancy sign-off, production rollout, load target or recovery objective is claimed. Issue #15 cannot honestly close its opening-balance acceptance gate until the merchant supplies counts and the reconciliation is recorded.
