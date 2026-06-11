# UniStar MCP

A small [MCP](https://modelcontextprotocol.io/) server that turns day-to-day GitHub CI chores
into tools an AI agent can drive for you. It is a thin, shell-free wrapper around the
[GitHub CLI (`gh`)](https://cli.github.com/) and `git` (inputs are never interpolated into a
shell, so they can't be turned into command injection).

## Tools

| Tool | Description |
|------|-------------|
| `pr_list_open` | List open PRs (all authors by default, newest first; `author="@me"` for yours), one compact CI/review line each. |
| `pr_get_status` | Compact mergeability snapshot for one PR: CI tally, review decision, draft state, mergeable. |
| `ci_analyze_pr_failures` | List the failing CI workflow runs for a PR, with their `run_id`s. |
| `ci_get_failed_logs` | Fetch the cleaned failed-step logs of a run for analysis. |
| `ci_rerun_workflow` | Rerun the failed jobs of a run. |
| `pr_create_backport` | Cherry-pick a merged PR onto a target branch and open a backport PR. |

Typical flow: `pr_get_status` to triage a PR, then `ci_analyze_pr_failures` → `ci_get_failed_logs`
(real bug vs. flaky?) → `ci_rerun_workflow` if flaky. All tools take `repo` as `owner/repo`;
`pr_create_backport` also needs `pr_number` and `target_branch` — it clones into a temporary
workspace, so no local checkout is required.

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

**Streamable HTTP** — for remote/shared use; MCP endpoint at `/mcp`.

```sh
unistar-mcp http --address :8080   # -a/--address, --debug
```

**Lazy loading** — `--lazy` advertises only three meta tools (`tool_list`, `tool_describe`,
`tool_call`) instead of every schema, keeping the `tools/list` payload constant as tools grow.
Useful for small-context models; for a demo the default full mode is clearer.

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

[MIT](LICENSE) © 2026 STARRY-S
