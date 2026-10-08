# Naming and code rules

Status: conventions for new Go and frontend work. Preserve existing external contracts until a versioned migration.

| Area | Rule | Example |
| --- | --- | --- |
| Go packages | Short lowercase domain names, no underscores; avoid generic utils/common packages | `orders`, `payments`, `inventory` |
| Go files | Lowercase snake_case; tests beside source | `payment_attempt.go`, `payment_attempt_test.go` |
| Go identifiers | Exported PascalCase, local camelCase; consistent initialisms | `OrderID`, `HTTPClient`, `totalKES` |
| Interfaces | Small, named by behavior, owned by consumer | `PaymentProvider`, `OrderRepository` |
| JS | camelCase functions/variables, PascalCase classes, UPPER_SNAKE_CASE constants | `renderCart`, `API_BASE` |
| HTML/CSS | kebab-case new IDs/classes/data attributes; preserve old selectors during migration | `payment-status`, `data-order-id` |
| SQL | snake_case new tables/columns/indexes | `payment_attempts`, `checkout_request_id` |
| HTTP routes | Lowercase plural nouns, kebab-case actions; compatibility exceptions documented | `/api/orders/{id}/payment-status` |
| JSON | camelCase to match existing frontend; explicit Go JSON tags | `paymentStatus`, `totalKes` |
| Environment | UPPER_SNAKE_CASE with ownership prefixes | `MPESA_CALLBACK_URL`, `REDIS_URL` |
| Events | Versioned dotted business names | `payment.succeeded.v1` |
| RabbitMQ | Namespaced exchanges/queues; version when incompatible | `bliss.notifications.v1`, `.retry`, `.dead` |
| Kafka | Namespaced domain topic with schema version | `bliss.payments.v1` |
| Migrations | Monotonic timestamp and purpose; runner handles locking/checksums | `20261008120000_create_payment_attempts.sql` |
| Kubernetes | DNS-safe kebab-case, stable app/component labels | `bliss-api`, `app.kubernetes.io/component: api` |
| Docs/ADRs | kebab-case files; numbered immutable ADR identity | `003-go-backend-and-vanilla-frontend.md` |

Do not rename historical Prisma tables or migrations just to conform. Legacy quoted names remain until an explicit data-preserving migration. Existing IDs are opaque strings; don't force UUID validation on legacy CUIDs. New order/attempt IDs can use UUIDs with preserved string JSON representation after choosing a generator.

## Go rules

Use gofmt and go vet. Pass `context.Context` first for IO, propagate cancellation/deadlines, close bodies/rows and handle errors. Services own transaction boundaries; handlers validate transport input and translate typed errors. Wrap errors with context using `%w`; never log provider secrets or raw sensitive bodies. Constructors return errors for invalid configuration; `main` owns process exit. Avoid panics for expected failures, global mutable state, goroutine-per-job without bounds, and interfaces added without a consumer need.

Use integer amounts with explicit currency and units; never float arithmetic for totals. Store timestamps in UTC and convert only at presentation/report boundaries. Database constraints enforce invariants alongside application checks. Parameterize SQL. Every shared cache/token refresher/worker path must be race-safe and bounded.

JSON is not a database model: separate request/response DTOs from persisted structures where fields/access differ. Provider payload names follow provider specifications and stay inside adapters. Unknown fields can be tolerated in provider callbacks but validate required identifiers/types and processing semantics.

## Frontend rules

Plain HTML, CSS and JavaScript remain the delivery stack. Semantic forms and accessible status messages; no secret keys in browser assets. Centralize API calls/error parsing without putting business pricing or payment state authority in the browser. Labels must distinguish order saved from payment verified. Avoid currency/stock duplication once catalog API exists. No fake review/availability data for SEO.

## Comments and generated files

Explain invariant or tradeoff, not the line of code. Exported Go APIs have useful comments. Commit reproducible generated contracts/query code if selected, and CI checks for drift. Generated files identify generator/version; edit their source, not output.
