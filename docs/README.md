# Development documentation

Planning baseline: 6 October 2026; updated with owner constraints. Target: 10 December 2026, zero infrastructure budget. On 16 October the owner removed the fixed hours cap; see the R01 decision record. Updated 8 October: Go backend and HTML/CSS/JS frontend confirmed. Status: language/scope choices accepted; implementation and delivery plan remain draft.

| Document | Purpose |
| --- | --- |
| [R01 decisions](release-scope-decisions.md) | Confirmed owner policies, current capacity decision and unresolved closure gates |
| [Scope](scope.md) | Product boundaries, open decisions, and completion criteria |
| [Current assessment](current-assessment.md) | Evidence from the code and improvement priorities |
| [Architecture](architecture.md) | Proposed components, data model, workflows, and API contracts |
| [Roadmap and backlog](roadmap.md) | Ordered milestones, issue IDs, dependencies, estimates, acceptance criteria |
| [Testing](testing.md) | Correctness, security, performance, and recovery evidence |
| [Operations](operations.md) | Metrics, deployment, CI/CD, incident response, and launch |
| [Development workflow](development-workflow.md) | Local checks, branches, PRs, review and completion rules |
| [Naming and code rules](naming-and-code-rules.md) | Go, frontend, SQL, API, messaging and configuration conventions |
| [CI/CD](ci-cd.md) | Check/build/release pipelines, artifacts and credentials |
| [Required PR checks](pr-checks.md) | R08 security/contracts/browser gates and commit-addressed build evidence |
| [Docker and environments](docker-and-local-environments.md) | Planned files, container/service contracts and verification |
| [Docker core setup](docker-core.md) | Implemented R07 setup, local credentials, readiness and safe shutdown |
| [R07 Docker evidence](evidence/r07-docker-core.md) | Local container, persistence, failure and image checks |
| [Infrastructure](infrastructure.md) | Module ownership, topology and microservice extraction criteria |
| [R03 hosting feasibility](hosting-feasibility.md) | Host inventory, individual-service measurements, provider restrictions, staged plan and unresolved capacity gates |
| [R03 service evidence](evidence/r03-service-summary.md) | Seven individual service baselines, raw results, retry evidence and repeat commands |
| [ADR 003](adr/003-zero-budget-hosting.md) | Conditional hosting direction; public selection remains blocked pending evidence |
| [Deployment](deployment.md) | Environment requirements, release/rollback and evidence |
| [ADR 005](adr/005-google-identity-and-sessions.md) | Google-only sign-in, Bliss-managed sessions, proposed expiry limits and restricted owner bootstrap |
| [Security and data](security-and-data.md) | Access, secrets, payment trust and data lifecycle requirements |
| [API and event contracts](api-and-event-contracts.md) | Compatibility, errors, idempotency and message evolution |
| [Backend migration](backend-migration.md) | Compatibility, database baseline and Go cutover |
| [SQL migration runbook](sql-migrations.md) | Implemented R06 commands, verified Prisma adoption, ownership and recovery |
| [R06 SQL evidence](evidence/r06-sql-baseline.md) | Local upgrade, mismatch, concurrency and backup/restore verification |
| [ADR 004](adr/004-sql-migration-baseline.md) | Selected transactional pgx SQL runner and baseline policy |
| [Go foundation](../backend/README.md) | Implemented command/config/health surface, isolated harnesses and current limits |
| [ADR 002](adr/002-go-backend-and-vanilla-frontend.md) | Accepted Go/vanilla direction and proposed technical defaults |
| [ADR 001](adr/001-runtime-and-messaging.md) | Proposed runtime and messaging boundaries |

These documents describe proposed work unless explicitly marked as current. BAP IDs remain backlog references; roadmap work is also tracked in GitHub issues, including R01 #7 and R03 #9. Estimates are engineering effort, not delivery commitments. Decisions awaiting the owner are listed in scope.md.

For each implementation issue: link its ID in the PR, update affected docs, record test evidence, and identify migration/rollback implications. A milestone closes only when its acceptance gates pass. Record consequential decisions as ADRs with context, alternatives, decision, consequences, and status.

## Decision and implementation status

Current storefront runtime: Express/Prisma/PostgreSQL plus HTML/CSS/JS. A separate Go foundation and Docker core stack are available locally; target runtime remains Go modular monolith plus SQL/PostgreSQL with HTML/CSS/JS frontend. Full order/payment migration, Kubernetes and CI configuration remain planned under their roadmap slices.

Read in order: scope → architecture → ADR 002 → backend migration → development workflow → naming → Docker → CI/CD → deployment → testing/operations. Detailed pipeline/config/runbook ownership lives in the specialized docs; architecture summarizes boundaries.

## Next decision records

ADR 004 records the SQL migration runner/Prisma baseline. Record further ADRs when decisions are made: hosting and supported runtime versions; Google identity/session implementation following ADR 005; authoritative inventory integration; NCBA-authorized STK route and settlement identifiers; delivery fee acceptance/recipient; exchange price/stock policy; analytics retention. Consult R01 for approved business policy. ADR 003 records a conditional hosting direction; final public selection and its evidence remain open.
