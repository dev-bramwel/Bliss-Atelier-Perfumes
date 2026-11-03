# ADR 004: Replace MyAccurateBooks as stock authority

Date: 21 October 2026; updated 28 October 2026. Issue: [R04 #10](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/10).
Status: replacement decision approved by owner; launch cutover lifecycle confirmed 28 October 2026. Evidence: [R04 evidence](../evidence/r04-inventory-reconciliation.md).

## Context

The owner identifies MyAccurateBooks as the current inventory system. Export support is unverified; plan on manual capture unless a usable export is subsequently demonstrated. This is a planning assumption, not a verified product limitation.

Stock is tracked by perfume, with one common bottle size. Employees record sales and the owner records replenishment. Current stock records are updated at the end of each business day. Partial bottles are not sold. The owner confirmed 20 ml bottles. The website should cover current stock; actual product mappings still require verification.

## Decision

Replace MyAccurateBooks: Bliss Atelier becomes the single writable stock authority at an explicitly approved future cutover. Until then, the current operational system remains authoritative. No ongoing integration or dual stock entry is planned.

Use PostgreSQL for durable stock records with fail-closed availability (24-hour owner approval expiry). The replacement must support the R01 policy: verified employee sales reduce stock immediately, admin review does not deduct it again, and owner replenishment is audited. This differs from the current end-of-day update process and requires operational preparation before cutover.

### Development vs. launch cutover lifecycle

On 28 October 2026, the owner confirmed the boundary between the development phase and launch cutover:

- **Development phase**: Operates against a mock / disposable development database. Opening batch tooling (`cmd/stock`), catalog availability, ledger reconciliation, and duplicate/malformed mapping rejection are verified with synthetic test data.
- **Production launch phase**: A clean, brand-new production database will be provisioned at launch. The shop physical bottle count and the MyAccurateBooks discrepancy sign-off will take place at that cutover window, and the real opening batch import will be executed against that fresh database at launch.

Capture and reconcile opening stock using the procedure in [R04 reconciliation](../inventory-reconciliation.md). Do not treat the hardcoded storefront catalog as evidence of physical quantities or complete merchant product mappings.

## Alternatives and consequences

Keeping MyAccurateBooks connected would require verified access/export capabilities, synchronization and failure handling. The owner chose replacement. Manual opening capture avoids depending on an unverified export, but requires a checked product mapping, physical counts and explicit discrepancy approval.

There must be only one writer at cutover. Employees will count while movements continue. The owner approved checked physical counts as opening stock and owner approval of every discrepancy. Fully paid customer promises are sales; unpaid promises remain stock, subject to active holds. The owner approved logging movements and normalizing counts to a common cutoff, with employee edits to that log prohibited. Each perfume has an independent stock quantity. No opened, damaged or tester bottles are reported. Automated comparison against opening stock, restocking and sales is implemented; the initial physical baseline requires owner sign-off at launch.

## Evidence and remaining gates

Source: owner decisions in the project conversation on 21 and 28 October 2026. No credentials, customer information, real balances or product exports were supplied.

- **Development gates (Closed)**: Authority selection, reconciliation rules, duplicate/malformed mapping rejection examples, and synthetic mock database batch drills are verified. See [R04 evidence](../evidence/r04-inventory-reconciliation.md).
- **Launch cutover gates (Operational Prerequisite)**: Physical shop bottle count, MyAccurateBooks discrepancy sign-off, and production opening batch import into the newly provisioned production database at launch cutover.
