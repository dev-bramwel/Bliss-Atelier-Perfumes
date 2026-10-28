# R04 opening-stock reconciliation

Updated: 28 October 2026. Status: authority and rules confirmed; launch cutover boundary established.
Issue: [#10](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/10). Authority decision: [ADR 004](adr/004-inventory-authority.md). Evidence: [R04 evidence](evidence/r04-inventory-reconciliation.md).

## Development vs. launch cutover boundary

On 28 October 2026, the owner confirmed the boundary between the development phase and production launch:

- **Development Phase**: Operates strictly against a mock / disposable development database. Tooling, batch validation, duplicate mapping rejection, ledger integrity, and fail-closed catalog availability are verified using synthetic test fixtures. No real inventory numbers or physical counts are entered during development.
- **Production Launch Phase**: A brand-new production database will be provisioned at launch. The live physical bottle count in the shop and the MyAccurateBooks discrepancy sign-off will take place at that cutover window, and the real opening batch import (`cmd/stock apply`) will be executed against that fresh database at launch.

## Confirmed facts

- Replace MyAccurateBooks with Bliss Atelier as stock authority at future cutover.
- Assume manual capture; export availability is unverified.
- Track whole 20 ml bottles by perfume; partial bottles are not sold. Employees count the bottles. No second storage location is recorded; verify this during the count.
- Owner approves using the checked physical count as opening stock and personally approving every discrepancy from MyAccurateBooks. Actual physical counts and discrepancy approvals are launch-stage cutover activities against the newly provisioned production database.
- Fully paid bottles promised to customers are successful sales, excluded from opening stock even if physically awaiting collection. Unpaid promised bottles remain stock; any active checkout hold still reduces availability under R01.
- Stock movements continue during counting and must be logged.
- Each perfume has its own stock quantity and one catalog entry, regardless of the number of bottles. For example, ten Hawas Elixir bottles produce one entry marked available. List currently available perfumes in normal browsing; unavailable/sold-out perfumes appear only when searched. Show exact available counts for 1–5 bottles and “Available” for 6 or more. This is display policy, not permission to hide quantities from authorized inventory management.
- Owner reports no opened, damaged or tester bottles.
- Owner approves logging every movement and reconciling counts to one common cutoff. Employees must not edit the movement log. Owner-approved correction mechanism: owner-only logged adjustments retaining the original entry; employees may record sales and counts but may not edit past movements.
- Requested verification: system compares counts against recorded opening stock plus replenishment minus sales, incorporating other approved movements. This can flag discrepancies; it cannot independently verify the first opening balance without a trusted baseline and complete movement records. Owner approval of physical opening stock remains required.
- Existing frontend and backend product literals say 100 ml; they conflict with the confirmed 20 ml size. Correct these during catalog implementation; do not use them as approved mapping evidence.
- Employees record sales, the owner replenishes stock, and current updates happen at business-day end.
- The target workflow uses immediate deductions for verified employee sales, as approved in R01.
- R09 addition: 24-hour reconciliation approval expiry. Fail-closed catalog availability: unconfirmed authority, unopened product, expired approval or database failure renders stock unavailable.

## Proposed reconciliation procedure for launch cutover

1. Verify the website product list against the shop and MyAccurateBooks, including sold-out products; use the confirmed 20 ml size. Assign stable SKUs and map each old-system product to exactly one target product (`p1` through `p23`). Do not match ambiguous names automatically.
2. Capture a dated MyAccurateBooks balance per perfume manually, or from a verified export if available. Have the capture checked against the source; exclude customer and payment details.
3. Count physical stock per SKU at a stated shop/location and timestamp. Separate sellable, quarantined and fully paid awaiting-collection bottles. Exclude paid awaiting-collection bottles from opening stock without deducting them a second time. Record active unpaid holds separately from total stock; available stock excludes holds and quarantine. Confirm opened/damaged stock treatment before approving balances.
4. Keep a movement log while counting; movements cannot pause. Record a unique reference, SKU, quantity, event time, type, actor and whether it happened before or after that SKU’s count. For a common cutoff after counting, opening stock equals counted eligible stock plus stock additions minus deductions occurring after that SKU’s count through the cutoff. Include paid sales when payment becomes verified, replenishment, quarantine transfers and approved corrections; unpaid promises alone do not deduct total stock. Reconcile MyAccurateBooks to the same cutoff, accounting for sales not yet entered in its end-of-day update. Have the log checked and avoid counting a movement both in the physical count and again as an adjustment.
5. Compare source quantities with physical quantities per SKU. Record every discrepancy, explanation, proposed adjustment and owner decision. Never silently overwrite unexplained differences.
6. Approve opening balances and any quarantined/committed quantities explicitly. Retain the dated count and sign-off as evidence. Do not invent quantities or interpret missing values as zero.
7. Revalidate movements between this count and the eventual runtime cutover into the fresh production database. Archive the old system's final snapshot and stop its stock writes when the replacement becomes authoritative.

Proposed worksheet fields: source product ID/name, target SKU, bottle size, location, source quantity, physical sellable quantity, quarantined quantity, committed quantity, discrepancy, reason, approved opening quantities, counter, checker, cutoff, owner approval and date.

## Mapping validation evidence

All 23 catalog products (`p1` to `p23`) are mapped to validated image assets in `frontend/img/`. Opening batches are validated by `backend/internal/catalog` and `backend/cmd/stock`:

- **Duplicate mapping rejection**: A batch with repeated product IDs (e.g. two lines for `p1`) is rejected with `invalid or duplicate product mapping`.
- **Missing products in opening batch**: Opening batch requires all 23 products; a partial batch is rejected with `counted import must map every catalog product exactly once`.
- **Unknown SKU rejection**: Unmapped product IDs (e.g. `unknown`) are rejected with `unknown product mapping`.
- **Negative counted quantity**: Count lines with quantity `< 0` are rejected with `negative counted stock`.
- **Missing / null quantity**: Lines with `null` or missing quantity are rejected with `product line requires an integer quantity and valid fields`.
- **Fractional / invalid quantity**: Non-integer numbers (e.g. `1.5`) or extra unknown JSON keys are rejected by strict unmarshaling.
- **Tampered batch replay**: Re-submitting an already used batch ID with changed content fails with `batch ID already used with different content`.
- **Second opening batch**: Attempting a second opening batch after stock is opened fails with `opening balance already recorded`.
- **Expected balance conflict**: Adjustments with stale `expectedBalance` fail with `balance changed; recount before retry`.

See [R04 evidence](evidence/r04-inventory-reconciliation.md) for full test scenario and drill outputs.

## Launch cutover inputs

- Count date, common cutoff and baseline owner sign-off on the physical count.
- Active holds and paid awaiting-collection quantities at cutover.
- Final discrepancy sign-off between MyAccurateBooks and the physical count.
- Fresh production PostgreSQL database provisioning.
- Execution of `stock approve-replacement` and `stock apply` with the signed-off production opening batch.

## Delivery evidence

Development evidence is recorded in [R04 reconciliation evidence](evidence/r04-inventory-reconciliation.md) and [R09 catalog evidence](evidence/r09-catalog-stock.md). Local tests (`go test ./...`), schema migrations (`0002_catalog.sql`), and operator CLI (`cmd/stock`) are verified on disposable development databases. PR [#41](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/pull/41) satisfies R04 development gates.
