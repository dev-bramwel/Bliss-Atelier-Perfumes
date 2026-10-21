# CI/CD design

Status: the [Docker core CI workflow](../.github/workflows/docker-core.yaml) implements Go formatting/module/vet/race/build checks, frontend CSS reproducibility/dependency checks, disposable SQL migration/restore checks and Docker core acceptance tests. The remaining release, security, browser and infrastructure jobs below remain target specifications. Owner has selected Go backend and vanilla frontend. The source repository is public on GitHub. The [R03 assessment](hosting-feasibility.md) recommends GHCR and standard public-repository runners subject to billing/access verification during implementation; no registry publication or sustainable public hosting has been established.

## PR pipeline

The [R08 implementation](pr-checks.md) extends Docker core CI with security/contract checks, a real mobile browser smoke, redacted retained evidence, SBOMs and commit-addressed image exports. Release publishing and full product scenarios below remain future gates.

Trigger on pull requests and main changes; minimal `contents: read` permissions. Pin third-party actions to reviewed commit SHAs, and choose the same supported Go version used in go.mod and Docker. Cancel superseded PR checks; do not cancel a production migration or deployment midway. Never execute untrusted PR code with deployment secrets or privileged self-hosted runners.

Required jobs:

1. Go formatting check (fail on gofmt diff), `go mod verify`, `go vet ./...`, `go test -race ./...`, build all commands.
2. Disposable PostgreSQL migration tests: empty DB and upgrade fixture from legacy Prisma schema. Route/payment integration against fake provider; capture JUnit/coverage if tool choice warrants it.
3. Frontend HTML/JS checks, browser smoke and accessibility automation using pinned development dependencies. Node is allowed for tooling only.
4. OpenAPI/event schema checks and reproducible generated-file drift check if generators are used.
5. Secret, Go vulnerability and dependency/image scans. Critical findings need remediation or documented severity assessment; never blanket-ignore scanner output.
6. Build Go runtime and frontend images without pushing on untrusted PRs. Full broker tests can be a separate job/profile; core payment gates remain required.

Nightly/main validation adds Kafka/RabbitMQ delivery/replay, full browser scenarios, load/soak and recovery exercises. Record which checks actually ran; no skipped infrastructure job counts as verified infrastructure.

## Release pipeline

Build once from reviewed commit; attach commit, image digest, build provenance and SBOM. Publish immutable tags/digests to selected registry. Staging consumes those digests, not a rebuild. Use narrowly scoped registry credentials or OIDC where supported. Build caches must not contain secrets.

Deploy sequence: validate configuration → backup checkpoint → single locked migration job → deploy API/worker/frontend → readiness/smoke/payment contract tests → observe dashboards → controlled production promotion. Use protected environment/release control for live payments and migrations; configuration belongs to the platform, not source files. Public hosting and live credentials are external prerequisites, not pipeline substitutes.

For local Kubernetes learning, CI can validate/render manifests and deploy an ephemeral cluster when runner capacity supports it. Otherwise run and record the same acceptance drill locally; do not mark cluster validation passed from syntax checks alone.

## Artifact and failure policy

Retain redacted test/browser/coverage reports and release manifest with commit, digests, migration version and environment. PR build failure blocks merge. Staging failure blocks promotion. Production failure invokes deployment.md and operations.md. Do not automatically re-run payment initiation because a workflow failed.

Implementation issues: BAP-014 and BAP-022. Track exact commands, pinned tool versions, repository protections, credentials owner and artifact retention when delivered.

Source: [GitHub Actions secure use](https://docs.github.com/en/actions/reference/security/secure-use) informs action pinning, secret handling and workflow permissions.
