# R08: required PR checks and build evidence

Issue [#14](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/14). This extends R07 CI; release promotion, registry publishing and production credentials remain R28/#34.

## Check coverage

| Required check | Gates |
| --- | --- |
| Docker core validation | Go formatting/module verification/vet/race/build; CSS reproducibility and dependency audit; real PostgreSQL migration/upgrade/restore fixtures; non-root/private Compose stack; actual Chromium cart/checkout rendering and same-origin health; persistence, migration failure/recovery, fixture provider, image credential scans, HIGH/CRITICAL image scans and SBOMs. |
| Security and contracts | Redacted Gitleaks history scan, Go vulnerability checking, OpenAPI structural validation and response-fixture schema checks, CI-tool dependency audit and existing legacy JavaScript tests. Negative controls must reject a synthetic key and an invalid contract. |

Both workflows run for PRs to develop/main, pushes to those branches, and manual dispatch, with no path filters. SHA-pinned actions, read-only contents permission, disabled persisted checkout credentials, standard hosted runners and no environment/deployment secrets apply to untrusted PRs too. Fork PRs may require GitHub's contributor approval; no pull_request_target or privileged self-hosted runner executes PR code. Scanners are version-pinned and their release archives are verified against committed SHA-256 values before execution. Scanner database download/tool failures fail the job, not silently skip a gate.

The current contracts are explicitly the foundation plus historical Express compatibility. Go still does not implement order/payment endpoints. The browser smoke seeds a synthetic cart, blocks external requests, checks mobile checkout rendering and same-origin foundation health, and never initiates payment. Extend its scenarios when order/payment implementation lands; it does not establish settlement or full product E2E/accessibility acceptance.

## Evidence and immutable artifacts

Each Docker run retains a seven-day `docker-build-<checked-out commit>` artifact containing exported backend/frontend/PostgreSQL images, `build-manifest.json` (source commit, image content IDs, archive SHA-256), acceptance JSON, synthetic browser report/screenshot, image vulnerability reports and CycloneDX SBOMs. Uploaded images are build candidates; only a successful entire job is a passing candidate. A failed run's diagnostic artifact is never eligible for promotion. The manifest is emitted only after the acceptance drill passes. Repository artifact storage/billing limits still apply; this retention policy is not a capacity/cost guarantee.

Verify downloaded archives against the manifest before `docker load`; inspect the loaded image IDs against the manifest too. Build once and reuse the exact archive/content identity in later controlled promotion. Commit-addressed artifact names are immutable within a run; this is not a claim that rebuilding on another date produces byte-identical archives. Signed release provenance/registry publication and staging/production promotion remain R28. Never deploy an untrusted PR artifact.

The security artifact contains redacted secret findings, Go vulnerability output, contract/test logs and synthetic negative-control results. The allowlisted upload directories exclude `.local/check-*` and all generated credential files. No Compose environment dump, real customer data, merchant secrets, signed bank forms, raw provider callback or production database is collected.

Image/Go/CI-tool/frontend scans block the supported Go container stack. The retained legacy Express backend's known npm vulnerabilities, recorded in R05/R06 evidence, are not thereby cleared or waived for production. Existing legacy helper tests run, but full legacy dependency remediation remains an explicit launch concern; this workflow does not advertise a clean legacy dependency audit. No blanket vulnerability ignore list is added.

## Enforcement and regression proof

Repository rulesets should require both stable contexts above, with current-base enforcement, on develop and main/default branch. Preserve existing review/force-push/deletion rules. Check branch patterns carefully: a literal quote inside a ref pattern does not match the actual branch. Required-check configuration and hosted run results are recorded in the R08 evidence when verified.

The security job exercises a synthetic key-like value entirely in temporary storage and requires the scanner's detection exit code. The contract job replaces a temporary schema with an invalid document and requires nonzero exit. These controls never commit secrets or weaken the real checks. A separate reviewed negative CI run establishes actual red/green status behavior. Do not merge intentional broken fixtures.

## Local repetition and rollback

Linux scanner scripts are `scripts/ci/install-scanners.sh`, `security.sh` and `scan-images.sh`. Go checks remain documented in backend/README. Run `scripts/Test-DockerCore.ps1 -EvidenceDirectory .local/ci-build` with PowerShell and Docker for browser/build artifacts; this opt-in mode downloads the digest-pinned Playwright image and installs locked tools from tools/ci. It is slower than the default R07 drill. Use a fresh isolated project, never a real database.

No business schema or API change is made. Compose builds a derived PostgreSQL image, retaining the pinned 17.11 base and native non-root identity, while removing unused gosu: its obsolete embedded Go runtime caused the initial high/critical scan failures. Direct UID 70 startup is verified; running this image as root is unsupported. The test harness adds optional evidence output; production rollout is not involved. Rollback restores previous workflow/scripts/tool lockfiles, while explicitly deciding how required check names are updated so branches do not wait forever for removed jobs. Do not disable required checks merely to bypass a real failure. New HIGH/CRITICAL image findings require remediation or an explicit, reviewed reachability/severity assessment; scanner outages are failures. Actual engineering effort is not yet measured against the provisional estimate.
