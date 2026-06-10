# UniStar MCP

A small [MCP](https://modelcontextprotocol.io/) server that turns day-to-day GitHub CI chores
into tools an AI agent can drive for you. It is a thin, shell-free wrapper around the
[GitHub CLI (`gh`)](https://cli.github.com/) and `git` (inputs are never interpolated into a
shell, so they can't be turned into command injection).

## Tools

| Tool | Description |
|------|-------------|
| `ci_analyze_pr_failures` | List the failing CI workflow runs for a PR, with their `run_id`s. |
| `ci_get_failed_logs` | Fetch the cleaned failed-step logs of a run for analysis. |
| `ci_rerun_workflow` | Rerun the failed jobs of a run. |
| `pr_create_backport` | Cherry-pick a merged PR onto a target branch and open a backport PR. |

Typical flow: `ci_analyze_pr_failures` → `ci_get_failed_logs` (real bug vs. flaky?) →
`ci_rerun_workflow` if flaky. All CI tools take `repo` as `owner/repo`; `pr_create_backport`
also needs `pr_number`, `target_branch`, and `repo_dir` (a local clone for the cherry-pick).

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

## Install

```sh
go build -o unistar-mcp ./cmd     # from source
make image                        # container (installs gh + git)
docker run -e GH_TOKEN=ghp_xxx -p 8080:8080 unistar-mcp http --address :8080
```

## Notes

- **Backport** requires a *merged* PR; on a cherry-pick conflict the tool stops and leaves the
  cherry-pick in progress on `backport-<pr>-to-<branch>` with instructions to `--continue` or `--abort`.
- **Logs** from `ci_get_failed_logs` are distilled, not dumped: ANSI codes and `gh`'s line
  prefixes are stripped, then only the error lines (+ a little context) are extracted and capped
  to ~6 KB — keeping the signal, not the noise, to fit small-model context budgets.
- **Errors** (missing binary, auth, 404, 403) are classified into actionable messages.
- Authentication uses one ambient token — no per-request isolation, so HTTP mode is for trusted environments.

## License

[MIT](LICENSE) © 2026 STARRY-S
