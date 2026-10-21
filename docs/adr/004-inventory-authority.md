# ADR 004: Replace MyAccurateBooks as stock authority

Date: 21 October 2026. Issue: [R04 #10](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/10).
Status: replacement decision approved by owner in the project conversation; opening balances, reconciliation rules and cutover remain pending. Documentation only; no runtime changes or production verification.

## Context

The owner identifies MyAccurateBooks as the current inventory system. Export support is unverified; plan on manual capture unless a usable export is subsequently demonstrated. This is a planning assumption, not a verified product limitation.

Stock is tracked by perfume, with one common bottle size. Employees record sales and the owner records replenishment. Current stock records are updated at the end of each business day. Partial bottles are not sold. The owner confirmed 20 ml bottles. The website should cover current stock; actual product mappings still require verification.

## Decision

Replace MyAccurateBooks: Bliss Atelier becomes the single writable stock authority at an explicitly approved future cutover. Until then, the current operational system remains authoritative. No ongoing integration or dual stock entry is planned.

Use the already approved PostgreSQL target for durable stock records. The replacement must support the R01 policy: verified employee sales reduce stock immediately, admin review does not deduct it again, and owner replenishment is audited. This differs from the current end-of-day update process and requires operational preparation before cutover.

Capture and reconcile opening stock using the procedure in [R04 reconciliation](../inventory-reconciliation.md). Do not treat the hardcoded storefront catalog as evidence of physical quantities or complete merchant product mappings.

## Alternatives and consequences

Keeping MyAccurateBooks connected would require verified access/export capabilities, synchronization and failure handling. The owner chose replacement. Manual opening capture avoids depending on an unverified export, but requires a checked product mapping, physical counts and explicit discrepancy approval.

There must be only one writer at cutover. Employees will count while movements continue. The owner approved checked physical counts as opening stock and owner approval of every discrepancy. Fully paid customer promises are sales; unpaid promises remain stock, subject to active holds. The owner approved logging movements and normalizing counts to a common cutoff, with employee edits to that log prohibited. Each perfume has an independent stock quantity. No opened, damaged or tester bottles are reported. Automated comparison against opening stock, restocking and sales is requested; the initial physical baseline still requires owner sign-off. Actual balances, correction permissions and recovery procedures remain pending. Runtime stock persistence and synchronization/reservation implementation belong to dependent work, including R09 #15; they are not delivered here.

## Evidence and remaining gates

Source: owner answers in the project conversation on 21 October 2026. No credentials, customer information, real balances or product exports were supplied.

R04 remains open until mapping rejection examples, approved reconciliation rules, opening balances and discrepancy sign-off are recorded. No schema, API, configuration or database migration changed in this decision. Runtime rollback is not applicable to this documentation change; the future data cutover requires a separate verified procedure.
