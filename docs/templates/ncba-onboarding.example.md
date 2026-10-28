# NCBA onboarding worksheet — blank template

Copy to a private, access-controlled location. Do not commit completed forms, usernames/passwords, secrets, legal bank account details or signed letters. This is a preparation worksheet, not a substitute for NCBA's original forms or an executed authorization.

## Merchant identity

- Legal customer/business name: [merchant completes]
- Authorized signatories and branch: [merchant completes]
- Legal bank account number: [merchant confirms with NCBA; do not assume the collection alias]
- Paybill: 880100
- NCBA Till/collection alias: [private deployment value; bank confirms STK AccountNo mapping]
- Services requested: STK Push, NCBA alias-level IPN, C2B receipt query
- Bank contact and correspondence date: [merchant completes]

## IPN configuration

- Preferred payload: JSON, subject to bank confirmation
- Public HTTPS URL: [developer provides only after endpoint is deployed and verified]
- Username: [provision privately]
- Password: [provision privately]
- Secret key: [provision privately; maximum 50 characters per bank instructions]
- Confirmed hash variant / known-answer vector: [obtain from NCBA]
- Retry, acknowledgment and missed-notification recovery agreement: [obtain from NCBA]

## STK request

- Username: [provision privately]
- Secret key: [provision privately]
- Basic-auth password/secret mapping: [NCBA confirms; do not assume]
- Confirmed AccountNo / collection alias: [NCBA confirms privately]
- Initiation-to-receipt correlation: [NCBA confirms]
- Sandbox URL, merchant test scope and activation permission: [NCBA confirms]

## C2B request — use the original editable bank form

- First and last name: [merchant completes privately]
- Username: [provision privately]
- Secret key: [provision privately]
- Basic-auth password/secret mapping: [NCBA confirms]
- SHORTCODE and TYPE for this merchant: [NCBA confirms]
- Collection-reference access scope: [NCBA confirms]
- Settlement/reversal and statement/export evidence: [NCBA confirms]

## Submission and activation record

- Developer technical verification completed: [date / evidence]
- Merchant signature date: [date]
- STK/IPN email and signed branch-letter submission: [date / restricted bank acknowledgment]
- C2B editable form and signed branch submission: [date / restricted bank acknowledgment]
- NCBA sandbox authorization: [date / scope]
- NCBA production activation: [date / scope]
- Supervised payment and merchant-credit/settlement verification: [date / restricted evidence, issue #37]

No credentials or completed letters are sent automatically. Merchant owns submission and confirms bank receipt. Unknown dates stay unknown; review open items on 23 October 2026.
