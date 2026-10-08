# Development workflow

Status: development policy for the planned Go rewrite. Updated 8 October 2026. Commands marked target become runnable when BAP-022 adds the Go module/tooling. Current runtime remains Express/Prisma; see the root README.

## Working cycle

1. Select one Ready issue from the roadmap. Resolve its contract, business decisions and dependencies; split work exceeding one week's 14-hour budget.
2. Branch from current main: `feat/BAP-022-go-foundation`, `fix/BAP-006-callback-transition`, or `docs/BAP-002-deployment`.
3. Write acceptance scenarios first, then implement a reviewable vertical slice. Every payment change needs deterministic fake-provider evidence and persistent database assertions.
4. Run relevant local checks; update API/schema/docs with the implementation. Record failed checks and unresolved blockers rather than declaring success.
5. Open a PR with problem/outcome, linked issue, test evidence, schema/config changes, rollout/rollback and risks. Use a draft while incomplete. Review code and generated SQL, even when using coding assistants.
6. Merge only after required CI and review. In a solo project, conduct an explicit self-review; use a second reviewer for payment/security work when available. Never claim a review occurred if it did not.
7. Deploy to staging, verify, then promote using the deployment runbook. Close the issue only with acceptance evidence attached.

Use small commits: `feat(payments): guard paid transitions (BAP-006)`. Main stays releasable. Avoid long-lived rewrite branches: a temporarily parallel Go implementation is described in backend-migration.md. Never run legacy and Go payment writers concurrently against real orders.

## Target local setup and commands

Install the selected Go toolchain, Docker with Compose, Git, and optional Node for browser test tooling. Node is not a backend production dependency after cutover. `go.mod`, `go.sum`, container image references and CI toolchain must agree. The observed host Go 1.22.2 is not the selected release toolchain.

From `backend/` after Go foundation exists:

```sh
go mod download
go mod verify
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/api ./cmd/worker ./cmd/migrate
```

Integration checks require a disposable DB and explicit test configuration; they must never silently target production. Define root Makefile targets during BAP-022: `make check`, `make test-integration`, `make dev-up`, `make migrate`, `make dev-down`. These targets do not exist yet. Document actual ports and commands beside the delivered Compose files.

Retain HTML/CSS/JS frontend. Prefer ES modules and small domain-specific files as needed, semantic HTML, progressive enhancement and keyboard access. Use `textContent` for untrusted data; avoid interpolating customer/admin data into `innerHTML`. Use a compiled/self-hosted CSS strategy for release, preserving plain CSS output; framework runtime is unnecessary. Crawlable product HTML can be generated or served with Go templates.

## Definition of Ready / Done

Ready: bounded user outcome, dependencies satisfied, decisions recorded, API/data contract drafted, test scenarios and estimate present.

Done: reviewed implementation; relevant tests pass; migration and rollback rehearsed where applicable; docs and telemetry updated; issue acceptance evidence linked; no release blocker hidden. Prototype, mock, local integration and production-ready are distinct statuses.

## Weekly planning

Track the 14-hour allocation across learning, coding, tests and review. Friday review: actual versus planned hours, provider/hosting blockers, unfinished acceptance gates and next week's slice. The Go rewrite adds scope and must be estimated before retaining previous calendar promises. Do not trade payment correctness for infrastructure screenshots.
