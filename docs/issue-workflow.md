# Issue Dependency & Blocker Workflow

This document outlines the policy and automated enforcement rules for issue dependencies in Bliss Atelier Perfumes.

---

## 1. Core Principle

**A dependent issue cannot be closed until all of its prerequisite blocking issues are closed.**

This prevents incomplete milestones from being prematurely marked as finished, ensures ordered execution of roadmap capabilities, and protects against merging PRs that claim to close dependent tasks whose foundational work is incomplete.

---

## 2. Dependency Tracking Mechanism

Dependencies are tracked in two complementary ways:

1. **GitHub Native Issue Dependencies (`blockedBy` / `blocking`)**:
   - Stored directly in GitHub's dependency graph.
   - Visible in the GitHub UI sidebar under **Relationships** ("Blocked by" and "Blocking") with live status badges.
   - Queryable via the GitHub CLI (`gh issue view <id> --json blockedBy`) and GitHub GraphQL API.

2. **Issue Body Metadata (Fallback & Human-Readable)**:
   - Written in the issue description, e.g.:
     ```markdown
     Dependencies: [R09](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/15)
     ```
     or
     ```markdown
     Blocked by #15
     ```

---

## 3. Automated Enforcement Workflows

The workflow at [`.github/workflows/enforce-issue-dependencies.yaml`](file:///C:/Users/mutug/Documents/Bliss-Atelier-Perfumes/.github/workflows/enforce-issue-dependencies.yaml) enforces dependency rules continuously:

### A. Automatic Reopening on Closure (`issues: [closed]`)
When any issue is closed (either manually via the GitHub UI or programmatically):
1. The action queries the closed issue's blockers via GitHub GraphQL.
2. If any blocking issue is still in state `OPEN`:
   - The issue is **immediately reopened**.
   - An explanatory warning comment is posted to the issue listing all unresolved blockers.
   - The workflow run is failed to provide an audit trail.

### B. Pull Request Auto-Closure Guard (`pull_request`)
When a pull request is opened or updated:
1. The action scans the PR description for closing keywords (`Closes #X`, `Fixes #X`, `Resolves #X`).
2. If any referenced issue has active open blockers, the PR check fails, preventing accidental closure upon PR merge.

---

## 4. How to Manage Issue Dependencies

### Adding a Dependency
- **Via GitHub Web UI:** On the issue page, locate the **Relationships** section in the right-hand sidebar, click **Add blocker**, and select or enter the issue number.
- **Via GitHub CLI:**
  ```bash
  gh issue edit <issue_number> --add-blocked-by <blocker_number>
  ```
- **Multiple Blockers:**
  ```bash
  gh issue edit 19 --add-blocked-by 16 --add-blocked-by 17 --add-blocked-by 18
  ```

### Removing a Dependency
- **Via GitHub Web UI:** Click the trash/remove icon next to the blocker under **Relationships**.
- **Via GitHub CLI:**
  ```bash
  gh issue edit <issue_number> --remove-blocked-by <blocker_number>
  ```

### Synchronizing the Backlog
To reconcile all issue dependencies defined in issue descriptions across the entire repository:
```bash
node scripts/sync-issue-dependencies.mjs
```
Use `--dry-run` to preview additions without writing:
```bash
node scripts/sync-issue-dependencies.mjs --dry-run
```
