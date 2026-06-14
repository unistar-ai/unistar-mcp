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

[MIT](LICENSE) © 2026 STARRY-S
