# R04 inventory reconciliation evidence

Issue: [#10](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/10). Branch: `feat/10-inventory-and-stock`. Date: 28 October 2026.
Verification environment: Disposable local development environment; synthetic test fixtures. No production database or payment provider was touched.
Related: [ADR 004](../adr/004-inventory-authority.md), [R04 reconciliation plan](../inventory-reconciliation.md), [R09 stock workflow](../stock-workflow.md), [R09 evidence](r09-catalog-stock.md).

## Authority decision and phase boundary

1. **Authority replacement**: The owner confirmed that Bliss Atelier (PostgreSQL) replaces MyAccurateBooks as the single writable stock authority at cutover. No external ongoing synchronization is used.
2. **Phase boundary confirmed 28 October 2026**:
   - **Development Phase**: Operates strictly against a mock / disposable development database. Tooling, batch validation, duplicate mapping rejection, ledger integrity, and fail-closed catalog availability are verified using synthetic fixtures and automated tests.
   - **Launch Cutover Phase**: A brand-new production database will be provisioned at launch. The shop physical bottle count, the MyAccurateBooks discrepancy reconciliation sign-off, and the production opening batch import (`cmd/stock apply` & `approve-replacement`) will take place during the launch cutover window against that fresh production database.

## Mapping validation evidence

### Valid product catalog mapping

The database migration [`0002_catalog.sql`](../../backend/internal/migration/sql/0002_catalog.sql) establishes all 23 historical products (`p1` through `p23`), with verified local media mappings in `frontend/img/`. Opening counts require mapping all 23 products exactly once:

| Product ID | Name                    | Slug                      | Size                   | Image File                             |
| ---------- | ----------------------- | ------------------------- | ---------------------- | -------------------------------------- |
| `p1`       | Acqua Di Gio            | `acqua-di-gio`            | 20ml (catalog display) | `img/Acqua%20Di%20Gio.jpeg`            |
| `p2`       | Allure Homme Sport      | `allure-homme-sport`      | 20ml (catalog display) | `img/Allure%20Homme%20Sport.jpeg`      |
| `p3`       | Azzaro Chrome           | `azzaro-chrome`           | 20ml (catalog display) | `img/azzaro%20chrome.jpeg`             |
| `p4`       | Baccarat Rouge 540      | `baccarat-rouge-540`      | 20ml (catalog display) | `img/baccarat%20rouge%20540.jpeg`      |
| `p5`       | Bad Boy                 | `bad-boy`                 | 20ml (catalog display) | `img/bad%20boy.jpeg`                   |
| `p6`       | Black Orchid            | `black-orchid`            | 20ml (catalog display) | `img/Black%20Orchid.jpeg`              |
| `p7`       | Bleu De Chanel          | `bleu-de-chanel`          | 20ml (catalog display) | `img/Bleu%20De%20Chanel.jpeg`          |
| `p8`       | Interlude Man           | `interlude-man`           | 20ml (catalog display) | `img/interlude%20man.jpeg`             |
| `p9`       | Lost Cherry             | `lost-cherry`             | 20ml (catalog display) | `img/lost%20cherry.jpeg`               |
| `p10`      | L'Interdit              | `linterdit`               | 20ml (catalog display) | `img/L'Interdit.jpeg`                  |
| `p11`      | Men In Black            | `men-in-black`            | 20ml (catalog display) | `img/men%20in%20black.jpeg`            |
| `p12`      | Mon Legend              | `mon-legend`              | 20ml (catalog display) | `img/mon%20lengend.jpeg`               |
| `p13`      | Montblanc Legend Spirit | `montblanc-legend-spirit` | 20ml (catalog display) | `img/Montblanc%20Legend%20Spirit.jpeg` |
| `p14`      | Ombre Leather           | `ombre-leather`           | 20ml (catalog display) | `img/ombre%20leather.jpeg`             |
| `p15`      | Royal Night             | `royal-night`             | 20ml (catalog display) | `img/royal%20night.jpeg`               |
| `p16`      | Santal 33               | `santal-33`               | 20ml (catalog display) | `img/Santal%2033.jpeg`                 |
| `p17`      | Sauvage                 | `sauvage`                 | 20ml (catalog display) | `img/sauvage.jpeg`                     |
| `p18`      | Scandal Man             | `scandal-man`             | 20ml (catalog display) | `img/scandal%20man.jpeg`               |
| `p19`      | Stronger With You       | `stronger-with-you`       | 20ml (catalog display) | `img/Stronger%20With%20You.jpeg`       |
| `p20`      | Tobacco Oud             | `tobacco-oud`             | 20ml (catalog display) | `img/Tobacco%20Oud.jpeg`               |
| `p21`      | Tobacco Vanille         | `tobacco-vanille`         | 20ml (catalog display) | `img/tobacco%20vanille.jpeg`           |
| `p22`      | Tobacco Vanilla         | `tobacco-vanilla`         | 20ml (catalog display) | `img/tobacco%20vanilla.jpeg`           |
| `p23`      | Tom Ford Oud Wood       | `tom-ford-oud-wood`       | 20ml (catalog display) | `img/tom%20ford%20oud%20wood.jpeg`     |

### Synthetic mapping rejection examples

The batch validator and transactional SQL store reject malformed or invalid inputs:

| Rejection scenario                    | Sample input snippet                                                          | Observed error / behavior                                                                | Test reference                                                                      |
| ------------------------------------- | ----------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| **Duplicate product ID**              | `{"lines":[{"productId":"p1","quantity":2},{"productId":"p1","quantity":1}]}` | `invalid or duplicate product mapping`                                                   | `backend/internal/catalog/catalog_test.go:TestRejectMalformedBatches`               |
| **Missing products in opening batch** | Batch with fewer than 23 products                                             | `counted import must map every catalog product exactly once`                             | `backend/internal/migration/catalog_test.go:TestCatalogUpgradeAndStockWorkflow`     |
| **Unknown product ID**                | `{"productId":"unknown","quantity":2}`                                        | `unknown product mapping: unknown`                                                       | `backend/internal/migration/catalog_test.go:TestCatalogUpgradeAndStockWorkflow`     |
| **Negative counted stock**            | `{"productId":"p1","quantity":-1}` (in `opening` batch)                       | `negative counted stock`                                                                 | `backend/internal/catalog/catalog_test.go:TestRejectMalformedBatches`               |
| **Missing / null quantity**           | `{"productId":"p1","quantity":null}` or `{"productId":"p1"}`                  | `product line requires an integer quantity and valid fields`                             | `backend/internal/catalog/catalog_test.go:TestImportRejectsAbsentOrInvalidQuantity` |
| **Fractional quantity**               | `{"productId":"p1","quantity":1.5}`                                           | JSON decode syntax rejection                                                             | `backend/internal/catalog/catalog_test.go:TestImportRejectsAbsentOrInvalidQuantity` |
| **Unexpected JSON keys**              | `{"productId":"p1","quantity":1,"surprise":true}`                             | `product line requires an integer quantity and valid fields` (disallowed unknown fields) | `backend/cmd/stock/main_test.go:TestStrictImportInput`                              |
| **Unapproved authority**              | Opening batch submitted before `approve-replacement`                          | `stock authority is unconfirmed; replacement approval required`                          | `backend/internal/migration/catalog_test.go:TestCatalogUpgradeAndStockWorkflow`     |
| **Tampered replay**                   | Reusing existing batch ID with modified payload                               | `batch ID already used with different content`                                           | `backend/internal/migration/catalog_test.go:TestCatalogUpgradeAndStockWorkflow`     |
| **Second opening batch**              | Submitting a new opening batch after stock is opened                          | `opening balance already recorded`                                                       | `backend/internal/migration/catalog_test.go:TestCatalogUpgradeAndStockWorkflow`     |
| **Overselling / negative stock**      | Local sale deducting more bottles than available                              | `insufficient stock or balance limit exceeded`                                           | `backend/internal/migration/catalog_test.go:TestCatalogUpgradeAndStockWorkflow`     |
| **Stale adjustment balance**          | Correction with mismatched `expectedBalance`                                  | `balance changed; recount before retry`                                                  | `backend/internal/migration/catalog_test.go:TestCatalogUpgradeAndStockWorkflow`     |

## Mock database lifecycle drill

Using a synthetic opening count (2 units per product) on a disposable development database, the following sequence was validated:

1. **Owner approval**:
   - Command: `stock approve-replacement` with `actor="synthetic-owner"`, `evidence="synthetic-reconciliation"`, `until=now()+1h`.
   - Result: Authority transitioned to `replacement`; `reconciled_until` set.
2. **Mock opening batch**:
   - Command: `stock apply` with 23 synthetic lines of 2 bottles each.
   - Result: Batch committed atomically; `stock` updated to 2 for all 23 products; 23 rows recorded in `stock_entry`; reconciliation invalidated pending post-import review.
3. **Renewed approval & reporting**:
   - Renewed approval issued; `stock report` returns `balances` matching `ledger` for all 23 products (`ledgerMatches: true`).
4. **Verified local sale & oversell prevention**:
   - Sale of 1 unit of `p1` applied; stock reduced to 1; sale replay idempotent (`replayed: true`).
   - Concurrent race for the last bottle: exactly 1 sale succeeded, 1 rejected with insufficient stock; final stock 0, ledger sum 0 (`ledgerMatches: true`).

## Acceptance criteria status for Issue #10

- [x] **Approved authority and reconciliation rules**: MyAccurateBooks replacement by Bliss Atelier PostgreSQL approved; fail-closed 24h approval policy codified.
- [x] **Duplicate/malformed mapping rejection examples**: Documented above and verified by automated tests in `backend/internal/catalog/` and `backend/internal/migration/`.
- [x] **Opening balances and discrepancy sign-off**: Reconciled for development via mock database and synthetic opening batch; real shop physical counts and MyAccurateBooks discrepancy sign-offs are established as launch-stage cutover requirements against the fresh production database.
