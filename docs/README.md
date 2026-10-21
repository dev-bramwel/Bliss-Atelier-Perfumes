# Development documentation

Planning baseline: 6 October 2026; updated with owner constraints. Target: 10 December 2026, zero infrastructure budget. On 16 October the owner removed the fixed hours cap; see the R01 decision record. Updated 8 October: Go backend and HTML/CSS/JS frontend confirmed. Status: language/scope choices accepted; implementation and delivery plan remain draft.

| Document | Purpose |
| --- | --- |
| [R01 decisions](release-scope-decisions.md) | Confirmed owner policies, current capacity decision and unresolved closure gates |
| [R04 inventory reconciliation](inventory-reconciliation.md) | Current stock workflow, proposed opening-count rules and remaining sign-off gates |
| [Research decision 005](adr/005-opening-stock-and-product-images.md) | Open comparison of Excel intake, PostgreSQL opening entry and product-image storage |
| [ADR 004](adr/004-inventory-authority.md) | Owner-approved replacement of MyAccurateBooks as stock authority at future cutover |
| [Scope](scope.md) | Product boundaries, open decisions, and completion criteria |
| [Current assessment](current-assessment.md) | Evidence from the code and improvement priorities |
| [Architecture](architecture.md) | Proposed components, data model, workflows, and API contracts |
| [Roadmap and backlog](roadmap.md) | Ordered milestones, issue IDs, dependencies, estimates, acceptance criteria |
| [Testing](testing.md) | Correctness, security, performance, and recovery evidence |
| [Operations](operations.md) | Metrics, deployment, CI/CD, incident response, and launch |
| [Development workflow](development-workflow.md) | Local checks, branches, PRs, review and completion rules |
| [Naming and code rules](naming-and-code-rules.md) | Go, frontend, SQL, API, messaging and configuration conventions |
| [CI/CD](ci-cd.md) | Check/build/release pipelines, artifacts and credentials |
| [Docker and environments](docker-and-local-environments.md) | Planned files, container/service contracts and verification |
| [Infrastructure](infrastructure.md) | Module ownership, topology and microservice extraction criteria |
| [R03 hosting feasibility](hosting-feasibility.md) | Host inventory, individual-service measurements, provider restrictions, staged plan and unresolved capacity gates |
| [R03 service evidence](evidence/r03-service-summary.md) | Seven individual service baselines, raw results, retry evidence and repeat commands |
| [ADR 003](adr/003-zero-budget-hosting.md) | Conditional hosting direction; public selection remains blocked pending evidence |
| [Deployment](deployment.md) | Environment requirements, release/rollback and evidence |
| [Security and data](security-and-data.md) | Access, secrets, payment trust and data lifecycle requirements |
| [API and event contracts](api-and-event-contracts.md) | Compatibility, errors, idempotency and message evolution |
| [Backend migration](backend-migration.md) | Compatibility, database baseline and Go cutover |
| [ADR 002](adr/002-go-backend-and-vanilla-frontend.md) | Accepted Go/vanilla direction and proposed technical defaults |
| [ADR 001](adr/001-runtime-and-messaging.md) | Proposed runtime and messaging boundaries |

These documents describe proposed work unless explicitly marked as current. BAP IDs remain backlog references; roadmap work is also tracked in GitHub issues, including R01 #7 and R03 #9. Estimates are engineering effort, not delivery commitments. Decisions awaiting the owner are listed in scope.md.

For each implementation issue: link its ID in the PR, update affected docs, record test evidence, and identify migration/rollback implications. A milestone closes only when its acceptance gates pass. Record consequential decisions as ADRs with context, alternatives, decision, consequences, and status.

## Decision and implementation status

Current runtime: Express/Prisma/PostgreSQL plus HTML/CSS/JS. Target runtime: Go modular monolith plus SQL/PostgreSQL; frontend remains HTML/CSS/JS. This documentation update does not add runnable Go, Docker, Kubernetes or CI configuration. Executable files belong to BAP-022/010/014/020 and must be verified when delivered.

Read in order: scope → architecture → ADR 002 → backend migration → development workflow → naming → Docker → CI/CD → deployment → testing/operations. Detailed pipeline/config/runbook ownership lives in the specialized docs; architecture summarizes boundaries.

## Next decision records

Record ADRs when decisions are made: SQL migration runner/Prisma baseline; hosting and supported runtime versions; admin identity/session strategy; inventory cutover and opening-stock intake; NCBA-authorized STK route and settlement identifiers; delivery fee acceptance/recipient; exchange price/stock policy; analytics retention. Consult R01 for approved business policy. ADR 003 records a conditional hosting direction; final public selection and its evidence remain open.
