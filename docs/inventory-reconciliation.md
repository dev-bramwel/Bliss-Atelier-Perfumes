# R04 opening-stock reconciliation

Updated: 21 October 2026. Status: working plan; no stock count or balance sign-off has occurred.
Issue: [#10](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/10). Authority decision: [ADR 004](adr/004-inventory-authority.md).

## Confirmed facts

- Replace MyAccurateBooks with Bliss Atelier as stock authority at future cutover.
- Assume manual capture; export availability is unverified.
- Track whole 20 ml bottles by perfume; partial bottles are not sold. Employees count the bottles. No second storage location is recorded; verify this during the count.
- Owner approves using the checked physical count as opening stock and personally approving every discrepancy from MyAccurateBooks. Actual counts and discrepancy approvals are still outstanding.
- Fully paid bottles promised to customers are successful sales, excluded from opening stock even if physically awaiting collection. Unpaid promised bottles remain stock; any active checkout hold still reduces availability under R01.
- Stock movements continue during counting and must be logged.
- Each perfume has its own stock quantity and one catalog entry, regardless of the number of bottles. For example, ten Hawas Elixir bottles produce one entry marked available. List currently available perfumes in normal browsing; unavailable/sold-out perfumes appear only when searched. Show exact available counts for 1–5 bottles and “Available” for 6 or more. This is display policy, not permission to hide quantities from authorized inventory management.
- Owner reports no opened, damaged or tester bottles.
- Owner approves logging every movement and reconciling counts to one common cutoff. Employees must not edit the movement log. Owner-approved correction mechanism: owner-only logged adjustments retaining the original entry; employees may record sales and counts but may not edit past movements.
- Requested verification: system compares counts against recorded opening stock plus replenishment minus sales, incorporating other approved movements. This can flag discrepancies; it cannot independently verify the first opening balance without a trusted baseline and complete movement records. Owner approval of physical opening stock remains required.
- Existing frontend and backend product literals say 100 ml; they conflict with the confirmed 20 ml size. Correct these during catalog implementation; do not use them as approved mapping evidence.
- Employees record sales, the owner replenishes stock, and current updates happen at business-day end.
- The target workflow uses immediate deductions for verified employee sales, as approved in R01.

## Proposed reconciliation procedure — pending owner review

1. Verify the website product list against the shop and MyAccurateBooks, including sold-out products; use the confirmed 20 ml size. Assign stable SKUs and map each old-system product to exactly one target product. Do not match ambiguous names automatically.
2. Capture a dated MyAccurateBooks balance per perfume manually, or from a verified export if available. Have the capture checked against the source; exclude customer and payment details.
3. Count physical stock per SKU at a stated shop/location and timestamp. Separate sellable, quarantined and fully paid awaiting-collection bottles. Exclude paid awaiting-collection bottles from opening stock without deducting them a second time. Record active unpaid holds separately from total stock; available stock excludes holds and quarantine. Confirm opened/damaged stock treatment before approving balances.
4. Keep a movement log while counting; movements cannot pause. Record a unique reference, SKU, quantity, event time, type, actor and whether it happened before or after that SKU’s count. For a common cutoff after counting, opening stock equals counted eligible stock plus stock additions minus deductions occurring after that SKU’s count through the cutoff. Include paid sales when payment becomes verified, replenishment, quarantine transfers and approved corrections; unpaid promises alone do not deduct total stock. Reconcile MyAccurateBooks to the same cutoff, accounting for sales not yet entered in its end-of-day update. Have the log checked and avoid counting a movement both in the physical count and again as an adjustment.
5. Compare source quantities with physical quantities per SKU. Record every discrepancy, explanation, proposed adjustment and owner decision. Never silently overwrite unexplained differences.
6. Approve opening balances and any quarantined/committed quantities explicitly. Retain the dated count and sign-off as evidence. Do not invent quantities or interpret missing values as zero.
7. Revalidate movements between this count and the eventual runtime cutover. A planning count alone does not establish live launch balances. Archive the old system's final snapshot and stop its stock writes when the replacement becomes authoritative.

Proposed worksheet fields: source product ID/name, target SKU, bottle size, location, source quantity, physical sellable quantity, quarantined quantity, committed quantity, discrepancy, reason, approved opening quantities, counter, checker, cutoff, owner approval and date. Define quantity semantics before filling the worksheet so categories cannot be double-counted.

## Mapping validation evidence to prepare

Include one valid mapping and rejection examples for duplicate source IDs, duplicate target mappings without an approved consolidation rule, missing/unknown SKUs, ambiguous perfume names, size mismatches, and missing, negative, fractional or nonnumeric quantities. Record expected and observed results; synthetic examples must be labeled synthetic and cannot substitute for actual opening balances.

## Open inputs

- Verified product list/source identifiers. Hawas Elixir was an example only; do not add it on that basis.
- Count date, common cutoff and first-baseline owner sign-off; later automated comparison requires complete movement records. No independent human checker has been designated.
- Active holds and paid awaiting-collection quantities.
- Opening-entry intake and product-image storage decision: see [research decision 005](adr/005-opening-stock-and-product-images.md).
- Actual opening balances and individual discrepancy sign-offs. The owner intends the first stock update to establish opening balances in the database; no quantities have yet been provided. This is a future acceptance gate, not evidence of completed reconciliation.

## Delivery evidence

Current evidence is owner conversation and local documentation only. No import, validator, stock persistence or production drill has run. No schema/config/API changes or database migration are delivered here. Before issue closure, attach mapping results, balance/discrepancy sign-off and the PR; review role authorization, sanitized data handling, unresolved blockers and the roadmap Definition of Done. Future cutover recovery must preserve post-cutover movements before changing authority; reopening both systems for independent writes is not a recovery procedure.
