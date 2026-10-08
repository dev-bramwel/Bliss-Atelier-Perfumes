# Development documentation

Planning baseline: 6 October 2026; updated with owner constraints. Deadline: 10 December 2026, 14 hours/week, zero infrastructure budget. Status: scope constraints confirmed; architecture and timeboxed delivery plan remain draft.

| Document | Purpose |
| --- | --- |
| [Scope](scope.md) | Product boundaries, open decisions, and completion criteria |
| [Current assessment](current-assessment.md) | Evidence from the code and improvement priorities |
| [Architecture](architecture.md) | Proposed components, data model, workflows, and API contracts |
| [Roadmap and backlog](roadmap.md) | Ordered milestones, issue IDs, dependencies, estimates, acceptance criteria |
| [Testing](testing.md) | Correctness, security, performance, and recovery evidence |
| [Operations](operations.md) | Metrics, deployment, CI/CD, incident response, and launch |
| [ADR 001](adr/001-runtime-and-messaging.md) | Proposed runtime and messaging boundaries |

These documents describe proposed work unless explicitly marked as current. Local issue IDs are ready to transfer to a tracker; no remote issues or milestones have been created. Estimates are engineering effort, not delivery commitments. Decisions awaiting the owner are listed in scope.md.

For each implementation issue: link its ID in the PR, update affected docs, record test evidence, and identify migration/rollback implications. A milestone closes only when its acceptance gates pass. Record consequential decisions as ADRs with context, alternatives, decision, consequences, and status.
