# Development documentation

Planning baseline: 6 October 2026; updated with owner constraints. Deadline: 10 December 2026, 14 hours/week, zero infrastructure budget. Updated 8 October: Go backend and HTML/CSS/JS frontend confirmed. Status: language/scope choices accepted; implementation and delivery plan remain draft.

| Document | Purpose |
| --- | --- |
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
| [Deployment](deployment.md) | Environment requirements, release/rollback and evidence |
| [Security and data](security-and-data.md) | Access, secrets, payment trust and data lifecycle requirements |
| [API and event contracts](api-and-event-contracts.md) | Compatibility, errors, idempotency and message evolution |
| [Backend migration](backend-migration.md) | Compatibility, database baseline and Go cutover |
| [ADR 002](adr/002-go-backend-and-vanilla-frontend.md) | Accepted Go/vanilla direction and proposed technical defaults |
| [ADR 001](adr/001-runtime-and-messaging.md) | Proposed runtime and messaging boundaries |

These documents describe proposed work unless explicitly marked as current. Local issue IDs are ready to transfer to a tracker; no remote issues or milestones have been created. Estimates are engineering effort, not delivery commitments. Decisions awaiting the owner are listed in scope.md.

For each implementation issue: link its ID in the PR, update affected docs, record test evidence, and identify migration/rollback implications. A milestone closes only when its acceptance gates pass. Record consequential decisions as ADRs with context, alternatives, decision, consequences, and status.

## Decision and implementation status

Current runtime: Express/Prisma/PostgreSQL plus HTML/CSS/JS. Target runtime: Go modular monolith plus SQL/PostgreSQL; frontend remains HTML/CSS/JS. This documentation update does not add runnable Go, Docker, Kubernetes or CI configuration. Executable files belong to BAP-022/010/014/020 and must be verified when delivered.

Read in order: scope → architecture → ADR 002 → backend migration → development workflow → naming → Docker → CI/CD → deployment → testing/operations. Detailed pipeline/config/runbook ownership lives in the specialized docs; architecture summarizes boundaries.

## Next decision records

Record ADRs when decisions are made: SQL migration runner/Prisma baseline; hosting and supported runtime versions; admin identity/session strategy; authoritative inventory integration; NCBA-authorized STK route and settlement identifiers; delivery fee acceptance/recipient; exchange price/stock policy; analytics retention. These remain open rather than assumed accepted defaults.
