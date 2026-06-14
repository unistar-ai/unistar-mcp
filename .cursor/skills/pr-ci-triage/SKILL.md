---
name: pr-ci-triage
description: Inspect and act on GitHub pull requests and their CI using the unistar-mcp tools. Use this whenever asked to check a PR's status, find out why CI is failing, decide whether a failure is flaky or a real bug, rerun failed CI jobs, or backport a merged PR. Always prefer these MCP tools over shelling out to gh or git directly.
---

# PR & CI triage with unistar-mcp

The `unistar-mcp` server wraps `gh`/`git` and returns compact, capped summaries
built for exactly this workflow. Prefer these tools over running `gh`/`git`
yourself: they request only the fields that matter, cap log output, and turn
common failures into actionable guidance.

## The tools

| Tool | Purpose | Required args |
|------|---------|---------------|
| `pr_list_open` | Open PRs with a one-line CI/review summary each | `repo` |
| `pr_get_status` | A single PR's CI tally, review decision, mergeability | `repo`, `pr_number` |
| `ci_analyze_pr_failures` | Failing CI runs for a PR, with run IDs | `repo`, `pr_number` |
| `ci_get_failed_logs` | Extracted error lines from a failed run | `repo`, `run_id` |
| `ci_rerun_workflow` | Rerun the failed jobs of a run (mutating) | `repo`, `run_id` |
| `pr_create_backport` | Cherry-pick a merged PR onto a branch, open the backport PR (mutating) | `repo`, `pr_number`, `target_branch` |

`repo` is always `owner/repo` (e.g. `Kong/kong-ee`). `pr_list_open` lists all
authors by default, newest first, bounded by `limit` (default 20); pass
`author="@me"` for your own PRs or a GitHub login to filter by user.

## Workflow: "why is this PR's CI failing?"

Chain the tools, do not jump straight to logs:

1. **`pr_get_status`** — confirm it is actually failing and see the pass/fail/pending tally. If nothing is failing, stop here.
2. **`ci_analyze_pr_failures`** — get the failing run IDs and their conclusions. This is the entry point that hands you the IDs the next two tools need.
3. **`ci_get_failed_logs`** (per failing run ID) — read the extracted error lines and judge **flaky vs real**:
   - real bug → summarize the failing step and the error; do not rerun.
   - flaky (network blip, timeout, known-flaky test) → go to step 4.
4. **`ci_rerun_workflow`** — only for flaky failures, and only after looking at the logs. This is mutating; say what you are rerunning and why.

Two things the model gets wrong without help:

- A run with conclusion **`action_required`** is waiting for approval, not a code
  failure — it has no failure logs to analyze. Tell the user it needs approval.
- `ci_analyze_pr_failures` only sees GitHub Actions. If `pr_get_status` reports
  failing checks but analyze finds none, the failures come from an **external CI**
  (commit statuses) — point the user at the PR page rather than insisting nothing failed.

## Workflow: backport a merged PR

1. Make sure the PR is **merged** (an unmerged PR has no merge commit to cherry-pick).
2. **`pr_create_backport`** with the `target_branch` (e.g. `release/3.4`, `next/1.2.0`, or any branch). It clones into a throwaway workspace, cherry-picks, pushes, and opens the PR — you do not need a local clone.
3. On a **cherry-pick conflict** the tool returns step-by-step manual instructions and keeps the temporary workspace. Relay those instructions; do not try to resolve the conflict by shelling out to git.

## If the server is running in `--lazy` mode

Only three meta tools are visible: `tool_list`, `tool_describe`, `tool_call`.
Map the workflow onto them: `tool_list` to see names, `tool_describe` for a
tool's schema, then `tool_call` with `{ "name": "...", "args": { ... } }` where
`args` is a real JSON object (not a string). For a demo, full mode is clearer.
