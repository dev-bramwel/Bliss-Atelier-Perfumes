# Research decision 005: Opening-stock intake and product images

Date: 21 October 2026. Status: open research decision requested by owner; recommendation below is not approved implementation. Related: R04 #10 and ADR 004.

## Question

Should the owner prepare opening stock in Excel or enter it directly into PostgreSQL, and where should perfume pictures be stored? The owner intends the first stock update to establish opening balances in the database. No opening balances have been entered or approved yet.

## Options

| Option | Role and tradeoff |
| --- | --- |
| Excel intake sheet | Useful for collecting/checking SKU, name, 20 ml size, count, cutoff and image reference before import. Requires validation, duplicate rejection and a single approved version. Must not become a second live stock authority. |
| Owner entry through the application into PostgreSQL | Recommended durable authority, consistent with ADR 004. Requires an authorized opening-entry workflow, validation, audit and transactional import/commit. Reuse the planned application database; a separate inventory database would require additional transaction and recovery design. |
| Images in PostgreSQL binary columns | Technically possible with bytea. Includes image bytes in database storage/backup planning; evaluate measured image volume and restore needs before selection. |
| Image files in durable media/object storage, references in PostgreSQL | Recommended starting direction. Store a stable media key and metadata against the product; ensure uploads, backups, restoration and deletion work together. Local development may use a persistent media directory. Production storage remains a hosting decision; ephemeral container files are insufficient. |

Excel and PostgreSQL are complementary if Excel is only intake. Pictures need not live in the same storage as stock quantities. Existing frontend/img assets are candidate source files, not evidence of a production upload or media backup workflow.

## Preliminary recommendation

Use the application PostgreSQL database for products, opening balances and stock movements. Let the owner either enter the initial batch through an authorized form or import a checked Excel/CSV intake. Commit the approved batch once, with a batch identifier, cutoff, actor, validation results and replay protection; preserve opening entries and correct them through owner-only adjustments.

Keep image files in durable media storage and save their references in the product record. If a spreadsheet is used, include an image filename/key column rather than depending on embedded pictures as the import contract. This is an engineering recommendation, not a measured storage comparison.

## Required research and acceptance

- Confirm whether “dedicated PostgreSQL database” means the application's existing database or an independently operated inventory database.
- Compare owner-entry and spreadsheet-import effort using representative sanitized products; demonstrate rejection of duplicate/malformed mappings before writes.
- Measure expected photo count, sizes and formats; compare durable storage with database binary storage against the approved hosting/budget and backup targets. No provider has been selected by this document.
- Define upload authorization, file type/size validation, safe generated keys, image processing and access rules. Product images must contain no customer/credential data.
- Demonstrate product-to-photo mapping, missing-image handling, replacement, and restore of both database records and image files without broken references.
- Record owner selection, local/sandbox test results, migration/rollback approach and outstanding production gates before accepting this decision.

## Initial official sources

Checked 21 October 2026. PostgreSQL documents bytea for binary strings: [binary data types](https://www.postgresql.org/docs/17/datatype-binary.html). This establishes capability, not that database image storage is best for this deployment.

Microsoft documents Excel images from source locations using [IMAGE](https://support.microsoft.com/en-US/Excel/image-function), and [pictures placed in cells](https://support.microsoft.com/en-gb/excel/insert-picture-in-cell-in-excel). Displaying an image in Excel does not establish a validated media import contract for this application.
