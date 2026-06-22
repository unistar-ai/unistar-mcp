# UniStar MCP

A small [MCP](https://modelcontextprotocol.io/) server that turns day-to-day GitHub CI chores
into tools an AI agent can drive for you. It is a thin, shell-free wrapper around the
[GitHub CLI (`gh`)](https://cli.github.com/) and `git` (inputs are never interpolated into a
shell, so they can't be turned into command injection).

It is built for **small, locally-deployed models** with weak reasoning and short context —
every design choice below follows from that one constraint.

## Highlights

- **Context-frugal by design.** Tools never echo raw `gh`/`git` output. They request only the
  JSON fields they need, parse them, and return one short plain-text line per item. CI logs are
  *distilled* — ANSI codes and `gh` prefixes stripped, only error lines (+ a little context)
  kept, byte-capped to ~6 KB. The model gets signal, not a wall of noise.
- **Progressive tool disclosure (lazy loading).** An optional mode that exposes just three meta
  tools instead of every schema, so the `tools/list` payload stays constant as the toolset grows.
  See [Lazy loading](#lazy-loading-progressive-tool-disclosure).
- **Errors are instructions, not stack traces.** Missing binary, auth, 404, 403, rate-limit and
  5xx are each rewritten into a sentence telling the model what to do next. Transient GitHub 5xx
  are auto-retried with backoff; rate limits tell the model to wait rather than hammer.
- **Tool chaining is part of the contract.** Each tool's description names the tool to call next
  (`ci_analyze_pr_failures` → `ci_get_failed_logs` → `ci_rerun_workflow`), and a bundled
  Claude Code skill documents the full triage workflow.
- **Safe by construction.** No shell, so no command injection. Mutating tools (rerun, backport)
  are never retried, so they can't double-execute. Backport runs in a throwaway clone, so your
  own checkouts are never touched.
- **Two transports, one core.** stdio (logs to stderr, stdout stays a clean protocol stream) and
  Streamable HTTP (`/mcp`, with an instant Ctrl-C shutdown).

## Tools

All tools take `repo` as `owner/repo` unless noted. Errors use `ERROR: <code> | message | hint: …` so agents can parse them. Success responses may prefix `OK:` for mutating tools.

### Pull requests

| Tool | Description |
|------|-------------|
| `pr_list_open` | Open PRs with compact CI/review line each (`author`, `limit`). |
| `pr_list_stale` | Open PRs with no update for N days (`days`, `limit`). |
| `pr_list_merged` | Recently merged PRs since a date (`since`, `label`, `limit`). |
| `pr_list_waiting_review` | Open PRs with passing CI needing review (`limit`). |
| `pr_list_changed_files` | Changed files with +/- counts for one PR. |
| `pr_get_status` | Mergeability snapshot: CI tally, external checks, review, draft. |
| `pr_get_overview` | Single-call snapshot: status, files stats, failing run IDs. |
| `pr_get_ci_snapshot` | CI_KIND + failing runs + compact digest per run (default 2). |
| `pr_get_status_batch` | Batch CI/review lines for comma-separated PR numbers (GraphQL, max 15). |
| `pr_get_overview_batch` | Lightweight multi-PR overview (GraphQL, max 5; no failing run IDs). |
| `pr_get_merge_blockers` | Structured merge blockers list. |
| `pr_list_merge_ready` | Open PRs ready to merge (CI green, approved, mergeable). |
| `pr_list_merge_blocked` | CI green but still blocked (conflicts, review, etc.). |
| `pr_draft_ci_comment` | Draft CI failure comment from fingerprint + policy. |
| `pr_list_large` | Open PRs exceeding file/line thresholds (mega-PR hygiene). |
| `pr_get_review_routing` | CODEOWNERS-based review routing for changed files. |
| `pr_get_review_state` | Reviewers, latest reviews, inline comment snippets. |
| `pr_diff_risk_scan` | Heuristic risk flags (lockfile, migration, workflow edits). |
| `pr_get_diff` | Capped unified diff (`max_bytes`). |
| `pr_post_comment` | Post a PR comment (mutating). |
| `pr_create_backport` | Cherry-pick merged PR onto target branch and open backport PR (mutating). |
| `backport_get_conflict_files` | List conflict files in backport workspace (`workspace_path`). |
| `backport_suggest_resolution` | Conflict resolution hints from marker analysis (`workspace_path`, `max_files`). |
| `pr_list_backport_candidates` | Merged PRs with `needs-backport` label (release-duty). |
| `pr_is_docs_only` | Detect docs-only PR changes (scheduler hint). |

### CI (GitHub Actions)

| Tool | Description |
|------|-------------|
| `ci_analyze_pr_failures` | Failing workflow runs for a PR; separates `action_required` from real failures; surfaces external checks. |
| `ci_get_run_summary` | Compact run summary with failed jobs/steps before pulling logs. |
| `ci_get_failed_logs` | Distilled failed-step logs with synopsis (job/step/test/FP). Optional `job_id`, `focus`. |
| `ci_get_failure_digest` | Compact digest: verdict + FP + ~1KB excerpt. |
| `ci_list_runs` | Recent workflow runs on a branch (`branch`, `limit`). Lines include duration when completed. |
| `ci_branch_health` | Branch CI health rollup: failure rate, streak, last failure. |
| `ci_workflow_stats` | Per-workflow failure rate and duration stats on a branch. |
| `ci_failure_fingerprint` | Structured failure fingerprint (job, test, error sig) for flaky matching. |
| `policy_classify_failure` | Rule-based failure class (test/infra/auth/timeout). |
| `ci_compare_runs` | Compare two runs by fingerprint without full logs. |
| `ci_correlate_prs` | Recently merged PRs before a failing run (regression-link). |
| `ci_get_job_logs` | Distilled logs for one job (`job_id` from run summary). |
| `ci_list_workflows` | Workflow names and IDs for the repository. |
| `ci_list_external_checks` | External CI checks (Jenkins, etc.) on a PR — not GitHub Actions. |
| `ci_get_check_url` | External check names with details URLs (open in browser, not Actions logs). |
| `ci_rerun_workflow` | Rerun failed jobs of a run (mutating). |

### Repository, issues, security

| Tool | Description |
|------|-------------|
| `repo_get_info` | Default branch, visibility, language, license, topics, labels. |
| `issue_list_open` | Open issues with compact summary (`limit`). |
| `issue_get` | Issue title/body/labels. |
| `issue_add_label` | Add label to an issue (mutating). |
| `issue_search` | Search issues with GitHub query syntax (`query`, `limit`). |
| `alert_list_open` | Open Dependabot alerts (`limit`). |
| `alert_summarize_open` | Dependabot severity rollup (`limit`). |

### Release

| Tool | Description |
|------|-------------|
| `release_list_tags` | Recent git tags (newest first). |
| `release_notes_draft` | Release-notes bullets from PRs merged since a tag. |

### Notify

| Tool | Description |
|------|-------------|
| `notify_post_slack` | Post compact Slack message via incoming webhook (`text`, optional `webhook_url` or `SLACK_WEBHOOK_URL`). mutating |

### Events (HTTP mode)

| Tool | Description |
|------|-------------|
| `event_list_recent` | Recent GitHub webhook events (`repo`, `kind` prefix, `limit`). Shared across stdio + HTTP via `~/.cache/unistar-mcp/events.jsonl` (override with `UNISTAR_MCP_EVENT_FILE`; set `off` for memory-only). |

Configure GitHub to POST to `http://<host>:8080/hooks/github` when running `unistar-mcp http`. Optional env: `GITHUB_WEBHOOK_SECRET`.

### Lazy mode meta-tools (`--lazy`)

| Tool | Description |
|------|-------------|
| `tool_list` | Names + one-line summaries (with category tags). |
| `tool_describe` | Full schema for one tool. |
| `tool_call` | Execute any tool by name with JSON `args`. |

Typical CI flow: `pr_get_overview` → `ci_analyze_pr_failures` → `ci_get_run_summary` →
`ci_failure_fingerprint` → `policy_classify_failure` → `ci_get_failed_logs` → `ci_rerun_workflow`.
Branch health: `ci_branch_health` or `ci_list_runs` on main.
External CI (Jenkins, etc.) appears in status rollup — inspect the PR page; do not call
`ci_get_failed_logs` for those checks.

See [docs/TOOLS.md](docs/TOOLS.md) for the full parameter reference (SSOT).

### MCP Resources

Read-only PR overview snapshots via `resources/read` (when your MCP client supports Resources):

| URI template | Content |
|--------------|---------|
| `github://pull/{owner}/{repo}/{number}/overview` | Same text as `pr_get_overview` |
| `github://pull/{owner}/{repo}/{number}/blockers` | Same as `pr_get_merge_blockers` |
| `github://pull/{owner}/{repo}/{number}/ci` | Failing Actions runs (`CI_KIND` + run list) |
| `github://pull/{owner}/{repo}/{number}/ci-snapshot` | Same as `pr_get_ci_snapshot` |
| `github://pull/{owner}/{repo}/{number}/review` | Same as `pr_get_review_state` |
| `github://repo/{owner}/{repo}/branch/{branch}/ci-health` | Same as `ci_branch_health` |

Example: `github://pull/acme/widget/42/overview`

## Requirements

`gh` (all tools) and `git` (backport only) must be installed and `gh` authenticated — either
`gh auth login`, or set `GH_TOKEN` / `GITHUB_TOKEN` (preferred for servers/containers). The
token needs `repo` and `workflow` scopes. A startup preflight warns if anything is missing.

## Usage

**stdio (default)** — for local MCP clients. Logs go to stderr so stdout stays a clean protocol stream.

```sh
unistar-mcp
```

```json
{
  "mcpServers": {
    "unistar-mcp": {
      "command": "unistar-mcp",
      "env": { "GH_TOKEN": "ghp_xxxxxxxxxxxx" }
    }
  }
}
```

**Streamable HTTP** — for remote/shared use; MCP endpoint at `/mcp`, GitHub webhooks at `/hooks/github`.

```sh
unistar-mcp http --address :8080   # -a/--address, --debug
# optional: GITHUB_WEBHOOK_SECRET=... for signed webhook payloads
```

**Lazy loading** — `--lazy` exposes only three meta tools instead of every schema. See
[Lazy loading](#lazy-loading-progressive-tool-disclosure) for the design. For a demo the default
full mode is clearer; `--lazy` is the interesting one to *talk* about.

```sh
unistar-mcp --lazy
```

## Lazy loading: progressive tool disclosure

**The problem.** An MCP server advertises every tool's full JSON schema in `tools/list`, and that
list is sent to the model on every turn. With many tools, or verbose schemas, the toolset alone
can eat a large slice of the context window *before any work begins*. For a small local model with
a short context, that is the difference between working and not.

**The pattern.** Don't advertise N schemas — advertise a **gateway** of three meta tools, and let
the model pull detail on demand (a.k.a. *progressive disclosure*, the same idea behind Anthropic's
Tool Search Tool / deferred tools):

| Meta tool | Role |
|-----------|------|
| `tool_list` | Names + a one-line summary each — cheap to scan, this is all that's always loaded. |
| `tool_describe(name)` | The full description and parameter schema of **one** tool, fetched only when needed. |
| `tool_call(name, args)` | Execute a tool by name, passing its parameters as a JSON object. |

The model **discovers → inspects only what it needs → calls**. The `tools/list` payload is now
constant — three tools — no matter whether the server has six real tools or sixty.

**Tuned for weak models.** The indirection is made forgiving so a small model can't get stuck:

- `tool_call` **pre-checks required parameters** and, on a miss, returns *the tool's schema in the
  error message* — so a model that skips `tool_describe` and calls directly self-corrects in a
  single round trip.
- `args` must be a real JSON object; a stringified-JSON `args` is rejected with a worked example
  (small models routinely get nested-string escaping wrong).
- An unknown tool name comes back with the list of valid names, saving a `tool_list` round trip.

**One registry, two modes.** Every tool group returns `[]toolEntry{tool, handler}` into a single
internal registry (`registry.go`). In full mode the server registers them directly; in lazy mode
it registers only the three meta tools and dispatches through the registry. Adding a tool needs no
special handling — both modes pick it up automatically.

**Honest trade-offs** (good for Q&A):

- When the model genuinely needs a schema first, `describe`-then-`call` costs one extra round trip
  — mitigated by putting the schema in the error of a wrong `tool_call`.
- Tool annotations (read-only vs. mutating) are less visible to the host's permission UI behind a
  single `tool_call`; `tool_describe` surfaces them as text.
- For strong models or a handful of tools, full mode is simpler — which is why lazy loading is
  **opt-in** (`--lazy`), not the default.

## Install

```sh
# from source
go build -o unistar-mcp ./cmd
./unistar-mcp --help

# container
make image
docker run -e GH_TOKEN=ghp_xxx -p 8080:8080 unistar-mcp http --address :8080
```

## Use with Claude Code

This repo ships a ready-to-use Claude Code setup, so cloning it is enough to demo the server:

- **`.mcp.json`** registers the server with `go run ./cmd` — no absolute path or pre-build step,
  it works from a fresh clone (Go required). The first time you open the project, Claude Code
  asks you to approve the project MCP server; approve it and the tools appear (run `/mcp` to check).
- **`.claude/settings.json`** denies `gh pr` / `gh run` in Bash, so the agent reaches for these
  tools instead of shelling out — which is what you want when demonstrating the MCP.
- **`.claude/skills/pr-ci-triage`** documents the intended tool-chaining workflow so the agent
  uses the tools in the right order.

For faster startup (no recompile each launch), build once and point the command at the binary:

```jsonc
// .mcp.json
{ "mcpServers": { "unistar-mcp": { "command": "./unistar-mcp" } } }
```
then `go build -o unistar-mcp ./cmd`.

Personal config (`.claude/settings.local.json`) is gitignored and never shared.

## Use with Cursor

This repo ships a ready-to-use Cursor setup alongside the Claude Code config:

- **`.cursor/mcp.json`** registers the server with `go run ./cmd` — same zero-build
  workflow as `.mcp.json`. Enable it under **Settings → Tools & MCP** the first time
  you open the project.
- **`.cursor/skills/pr-ci-triage`** documents the intended tool-chaining workflow.
- **`.cursor/rules/`** provides always-on project context and file-scoped conventions
  for MCP tool development.
- **`AGENTS.md`** is the agent-facing development guide (Cursor and other clients);
  `CLAUDE.md` remains the Claude Code–specific equivalent.

For faster startup, build once and point the command at the binary (use
`${workspaceFolder}/unistar-mcp` in `.cursor/mcp.json`).

Unlike Claude Code, Cursor has no built-in Bash deny list — the rules and skill steer
the agent toward MCP tools instead of raw `gh`/`git` for PR/CI tasks.

## Notes

- **Backport** requires a *merged* PR and runs in a throwaway shallow clone, so your own
  checkouts are never touched. On a cherry-pick conflict it keeps that temporary workspace with
  the cherry-pick in progress on `backport-<pr>-to-<branch>` and returns step-by-step
  instructions to finish or abort.
- **Logs** from `ci_get_failed_logs` are distilled, not dumped: ANSI codes and `gh`'s line
  prefixes are stripped, then only the error lines (+ a little context) are extracted and capped
  to ~6 KB — keeping the signal, not the noise, to fit small-model context budgets.
- **Errors** (missing binary, auth, 404, 403) are classified into actionable messages.
- Authentication uses one ambient token — no per-request isolation, so HTTP mode is for trusted environments.

## License

[MIT](LICENSE) © 2026 Unistar contributors
