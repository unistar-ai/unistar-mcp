# MCP Tools Reference (SSOT)

52 business tools + 5 lazy meta-tools (+ `resource_read` in coworker harness). All require `repo` (`owner/repo`) unless noted.

**Platform env:** `UNISTAR_MCP_EVENT_FILE` (default `~/.cache/unistar-mcp/events.jsonl`, `off` = memory-only); `UNISTAR_MCP_CACHE_TTL` (default 60s read cache for overview/branch-health/repo info, `off` to disable).

## Error contract

Failures: `ERROR: <code> | <message> | hint: <action>`

Codes: `AUTH`, `NOT_FOUND`, `FORBIDDEN`, `RATE_LIMIT`, `TRANSIENT`, `VALIDATION`, `EXTERNAL_CI`, `UNAVAILABLE`, `GENERIC`.

Success (some mutating tools): `OK: …`

Paged logs header: `PAGE: offset=N total_lines=M has_more=true next_offset_lines=K page=P/T`

## Pull requests

| Tool | Required | Optional |
|------|----------|----------|
| `pr_list_open` | `repo` | `author`, `limit` (default 20) |
| `pr_list_stale` | `repo` | `days` (default 7), `limit` |
| `pr_list_merged` | `repo` | `since` (YYYY-MM-DD or days), `label`, `limit` |
| `pr_list_waiting_review` | `repo` | `limit` |
| `pr_list_changed_files` | `repo`, `pr_number` | |
| `pr_get_status` | `repo`, `pr_number` | |
| `pr_get_overview` | `repo`, `pr_number` | |
| `pr_get_ci_snapshot` | `repo`, `pr_number` | `max_runs` (default 2, max 5), `include_external`. CI_KIND + digests. |
| `pr_get_status_batch` | `repo`, `pr_numbers` | Comma-separated PR numbers (max **15**). One GraphQL call. |
| `pr_get_overview_batch` | `repo`, `pr_numbers` | Lightweight overview batch (max **5**). CI/review/file stats only — no failing run IDs. |
| `pr_get_merge_blockers` | `repo`, `pr_number` | |
| `pr_list_merge_ready` | `repo` | `limit` (default 30). CI green + approved + mergeable. |
| `pr_list_merge_blocked` | `repo` | `limit`. CI green but not merge-ready. |
| `pr_draft_ci_comment` | `repo`, `pr_number`, `run_id` | Draft for `pr_post_comment` (read-only). |
| `pr_list_large` | `repo` | `min_files` (default 30), `min_lines` (default 1000), `limit`. Mega-PR filter. |
| `pr_get_review_routing` | `repo`, `pr_number` | CODEOWNERS → suggested reviewers. |
| `pr_get_review_state` | `repo`, `pr_number` | Reviewers, latest reviews, inline comments (cap) |
| `pr_diff_risk_scan` | `repo`, `pr_number` | Lockfile / migration / workflow heuristics |
| `pr_get_diff` | `repo`, `pr_number` | `max_bytes` (default 48000) |
| `pr_post_comment` | `repo`, `pr_number`, `body` | mutating |
| `pr_create_backport` | `repo`, `pr_number`, `target_branch` | mutating |
| `backport_get_conflict_files` | `workspace_path` | From `pr_create_backport` conflict error |
| `backport_suggest_resolution` | `workspace_path` | `max_files` (default 3). Conflict marker hints. |
| `pr_list_backport_candidates` | `repo` | `label` (default needs-backport), `since`, `limit` |
| `pr_is_docs_only` | `repo`, `pr_number` | Docs-only change detection for scheduler |

## CI

| Tool | Required | Optional |
|------|----------|----------|
| `ci_analyze_pr_failures` | `repo`, `pr_number` | `include_external` (default true). First line: `CI_KIND` (`actions_only` / `external_only` / `mixed` / …) |
| `ci_get_run_summary` | `repo`, `run_id` | |
| `ci_get_failed_logs` | `repo`, `run_id` | `job_id`, `focus` (`last`/`all`/`step:<name>`), `offset_lines`, `max_lines`. Synopsis + distilled errors. |
| `ci_get_failure_digest` | `repo`, `run_id` | `job_id`. Verdict + FP + ~1KB excerpt. |
| `ci_list_runs` | `repo` | `branch`, `limit` (default 15, max 50). Output: `run_id  workflow  conclusion  duration`. |
| `ci_branch_health` | `repo` | `branch`, `limit`. Failure rate, streak, last failure (aggregates ci_list_runs). |
| `ci_workflow_stats` | `repo` | `branch`, `limit` (default 30), `top` (default 10). Per-workflow fail rate / duration. |
| `ci_failure_fingerprint` | `repo`, `run_id` | Output: job, step, test, error signature, fingerprint (flaky ledger). |
| `policy_classify_failure` | `repo`, `run_id` | Rule-based verdict: test / infra / auth / timeout / external_ci |
| `ci_compare_runs` | `repo`, `run_id_a`, `run_id_b` | Same fingerprint / failed job diff without full logs. |
| `ci_correlate_prs` | `repo`, `run_id` | Merged PRs on run branch before failure (regression-link). |
| `ci_get_job_logs` | `repo`, `run_id`, `job_id` | Distilled single-job logs (`offset_lines`, `max_lines`). |
| `ci_list_workflows` | `repo` | `limit` (default 30, max 100). Workflow names/IDs. |
| `ci_list_external_checks` | `repo`, `pr_number` | External (non-Actions) status checks only. |
| `ci_get_check_url` | `repo`, `pr_number` | External check names with details URLs. |
| `ci_rerun_workflow` | `repo`, `run_id` | mutating |

## Repository / issues / security

| Tool | Required | Optional |
|------|----------|----------|
| `repo_get_info` | `repo` | `label_limit` |
| `issue_list_open` | `repo` | `limit` |
| `issue_get` | `repo`, `issue_number` | |
| `issue_add_label` | `repo`, `issue_number`, `label` | mutating |
| `issue_search` | `repo`, `query` | `limit` (default 20, max 50) |
| `alert_list_open` | `repo` | `limit` |
| `alert_summarize_open` | `repo` | `limit` (default 100). Severity rollup. |

## Release

| Tool | Required | Optional |
|------|----------|----------|
| `release_list_tags` | `repo` | `limit` (default 20, max 50) |
| `release_notes_draft` | `repo` | `since_tag` (default latest tag), `limit` (default 30) |

## Notify

| Tool | Required | Optional |
|------|----------|----------|
| `notify_post_slack` | `text` | `webhook_url` (else `SLACK_WEBHOOK_URL` env). mutating |

## Events (HTTP mode)

Ingest GitHub webhooks at **`POST /hooks/github`** when running `unistar-mcp http`. Set `GITHUB_WEBHOOK_SECRET` to match the GitHub webhook configuration.

| Tool | Required | Optional |
|------|----------|----------|
| `event_list_recent` | | `repo`, `kind` (prefix filter), `limit` (default 20, max 100). Failed `workflow_run` events may include async `fp:` fingerprint. Events persist to `UNISTAR_MCP_EVENT_FILE` (default `~/.cache/unistar-mcp/events.jsonl`) so stdio and HTTP processes share the same buffer. |

Stdio mode: buffer stays empty until events arrive via HTTP webhook ingest on the same process.

## Lazy meta-tools (`--lazy`)

| Tool | Required | Optional |
|------|----------|----------|
| `tool_search` | `query` | `limit` (default 5, max 15) |
| `tool_list_category` | `category` | CI, PR, Repo, Issue, Security, Release, … |
| `tool_list` | | full catalog (prefer search/category first) |
| `tool_describe` | `name` | optional — `tool_call` returns schema on missing args |
| `tool_call` | `name` | `args` (JSON object) |

## Chaining

```
pr_get_overview → pr_get_ci_snapshot | ci_analyze_pr_failures → ci_get_run_summary → ci_get_failure_digest → ci_get_failed_logs → policy_classify_failure → ci_rerun_workflow → ci_compare_runs
ci_list_workflows → ci_list_runs → ci_branch_health
release_list_tags → release_notes_draft
pr_list_changed_files → pr_diff_risk_scan → pr_get_diff
pr_get_merge_blockers → pr_get_review_state
```

External CI: `ci_list_external_checks` or `ci_get_check_url`; do not call `ci_get_failed_logs`.

## MCP Resources

| URI template | MIME | Content |
|--------------|------|---------|
| `github://pull/{owner}/{repo}/{number}/overview` | `text/plain` | Same as `pr_get_overview` |
| `github://pull/{owner}/{repo}/{number}/blockers` | `text/plain` | Same as `pr_get_merge_blockers` |
| `github://pull/{owner}/{repo}/{number}/ci` | `text/plain` | Failing Actions runs (`CI_KIND` + run list) |
| `github://pull/{owner}/{repo}/{number}/ci-snapshot` | `text/plain` | Same as `pr_get_ci_snapshot` |
| `github://pull/{owner}/{repo}/{number}/review` | `text/plain` | Same as `pr_get_review_state` |
| `github://repo/{owner}/{repo}/branch/{branch}/ci-health` | `text/plain` | Same as `ci_branch_health` |

Clients that support `resources/read` can fetch PR snapshots without a tool call.
