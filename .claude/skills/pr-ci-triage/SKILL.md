---
name: pr-ci-triage
description: Inspect and act on GitHub pull requests and their CI using the unistar-mcp tools. Use this whenever asked to check a PR's status, find out why CI is failing, decide whether a failure is flaky or a real bug, rerun failed CI jobs, investigate main branch regressions, or backport a merged PR. Always prefer these MCP tools over shelling out to gh or git directly.
---

# PR & CI triage with unistar-mcp

The `unistar-mcp` server wraps `gh`/`git` and returns compact, capped summaries
built for exactly this workflow. Prefer these tools over running `gh`/`git`
yourself: they request only the fields that matter, cap log output, and turn
common failures into actionable guidance.

Errors use `ERROR: <code> | message | hint: …` (codes like `NOT_FOUND`, `TRANSIENT`, `RATE_LIMIT`).

See [docs/TOOLS.md](../../docs/TOOLS.md) for all **51** business tools (+ 3 lazy meta-tools).

## Core tools (CI triage)

| Tool | Purpose | Required args |
|------|---------|---------------|
| `pr_get_overview` | Single-call snapshot: status, files, failing run IDs | `repo`, `pr_number` |
| `pr_get_status` | CI tally, external checks, review, mergeability | `repo`, `pr_number` |
| `pr_get_overview_batch` | Light multi-PR overview (max 5, GraphQL; no run IDs) | `repo`, `pr_numbers` |
| `pr_get_status_batch` | Batch CI/review lines (max 15, GraphQL) | `repo`, `pr_numbers` |
| `ci_analyze_pr_failures` | Failing Actions runs + external/pending checks | `repo`, `pr_number` |
| `ci_get_run_summary` | Run status, failed jobs/steps (`job_id` for per-job logs) | `repo`, `run_id` |
| `ci_get_failure_digest` | Verdict + FP + ~1KB excerpt (lightest) | `repo`, `run_id` |
| `ci_get_failed_logs` | Synopsis + distilled errors; optional `job_id`, `focus` | `repo`, `run_id` |
| `ci_get_job_logs` | Distilled logs for one job (when full run logs are huge) | `repo`, `run_id`, `job_id` |
| `ci_failure_fingerprint` | Job/step/test/error signature + flaky ledger hash | `repo`, `run_id` |
| `policy_classify_failure` | Rule verdict: test / infra / auth / timeout / external_ci | `repo`, `run_id` |
| `ci_compare_runs` | Same fingerprint across two runs (after rerun) | `repo`, `run_id_a`, `run_id_b` |
| `ci_list_runs` | Recent runs on a branch | `repo` |
| `ci_branch_health` | Failure rate, streak, last failure (aggregates runs) | `repo` |
| `ci_correlate_prs` | Merged PRs before a failing main run (regression-link) | `repo`, `run_id` |
| `ci_list_workflows` | Workflow names/IDs (when guessing workflow names) | `repo` |
| `ci_list_external_checks` | External CI only (Jenkins, etc.) | `repo`, `pr_number` |
| `ci_rerun_workflow` | Rerun failed jobs (mutating) | `repo`, `run_id` |
| `pr_create_backport` | Cherry-pick merged PR (mutating) | `repo`, `pr_number`, `target_branch` |
| `backport_get_conflict_files` | Conflict paths after backport failure | `workspace_path` |

`repo` is always `owner/repo`.

## Workflow: "why is this PR's CI failing?"

Chain the tools; do not jump straight to full logs:

1. **`pr_get_overview`** or **`pr_get_status`** — confirm failing/pending checks. Note **External checks** lines (Jenkins, etc.).
2. **`ci_analyze_pr_failures`** — failing GitHub Actions run IDs. Output separates:
   - real failures → continue triage
   - `action_required` → approval gate; **no logs**; tell user to approve on GitHub
   - external checks → PR checks tab; **do not** call `ci_get_failed_logs`
3. **`ci_get_run_summary`** (per run ID) — failed jobs/steps; note `job_id` values.
4. **`ci_get_failure_digest`** or **`ci_failure_fingerprint`** → **`policy_classify_failure`** — classify before heavy logs.
5. Logs (if needed):
   - **`ci_get_failed_logs`** — pass `job_id` on matrix workflows; `focus=step:<name>` or `focus=all`; page with `max_lines`
   - **`ci_get_job_logs`** — deeper single-job logs
6. Decision:
   - real bug → summarize; do not rerun
   - flaky / infra timeout → **`ci_rerun_workflow`** (mutating; explain why)
7. After rerun → **`ci_compare_runs`** (old vs new run ID) to see if fingerprint changed.

Optional: match fingerprint against coworker **`store_list_flaky`** (local ledger).

## Workflow: "main CI is red — what broke?"

1. **`ci_list_runs`** or **`ci_branch_health`** on `main` (or default branch).
2. Pick the failing **`run_id`**.
3. **`ci_correlate_prs`** — recently merged PRs before the failure.
4. Continue with run summary → digest → logs as above.

## External CI

If **`ci_analyze_pr_failures`** or **`pr_get_status`** lists external checks (Jenkins, Codecov, etc.):
- Do **not** call `ci_get_failed_logs` or `ci_get_job_logs`.
- Use **`ci_list_external_checks`** / **`ci_get_check_url`** and inspect the external system.

## Mutating tools

- **`ci_rerun_workflow`**, **`pr_create_backport`**, **`pr_post_comment`** require explicit user approval in coworker.
- Never rerun when the failure is a clear code/test bug.
