# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## What this project is

unistar-mcp is an MCP (Model Context Protocol) server, written in Go, that helps manage
GitHub pull requests by wrapping the `gh` and `git` CLIs. Its capabilities:

- List one author's open PRs (defaults to my own via gh's `@me`; pass `author` for a
  specific user — always filtered by author because large repositories have far too
  many open PRs to list them all) and show a compact per-PR status: CI state, review
  decision, mergeability.
- Analyze CI failures for a PR: find the failing workflow runs, fetch only the
  failed-step logs, and extract the error lines so the model can judge whether a
  failure is a real bug or a flaky test.
- Rerun the failed jobs of a workflow run (for flaky failures).
- Create backport PRs: cherry-pick a merged PR onto `release/x.y`, `next/a.b.c`,
  or any custom branch, push the branch, and open the PR.
- On cherry-pick conflicts: simple conflicts should get a suggested fix the model can
  apply; severe conflicts are handed back to a human. Today the tool keeps the
  temporary workspace with the cherry-pick in progress and returns step-by-step
  manual instructions — the auto-fix path is a planned improvement, not yet
  implemented.

## Design principles (read before changing any tool)

The server targets small, locally deployed LLMs with weak reasoning and short
effective context. Every design decision follows from that:

- **Save context.** Never return raw `gh`/`git` output, especially long JSON with
  redundant fields. Request only the JSON fields you need (`--json a,b,c`), parse them,
  and return short plain-text summaries — one line per item where possible. See
  `ciState`/`tallyChecks` in pr.go and `extractErrors`/`cleanGHLog`/`tail` in ci.go
  for the existing patterns; reuse them.
- **Keep tools simple.** Few required parameters, flat inputs, one job per tool.
  Tool descriptions should tell the model when to use the tool and which tool to call
  next (e.g. ci_analyze_pr_failures returns run IDs for ci_get_failed_logs /
  ci_rerun_workflow).
- **Errors must be actionable.** Tool errors go through `runResult.wrap()` (exec.go),
  which rewrites common failures (missing binary, auth, 404, permission) into guidance
  the model can act on. Don't return raw stderr.
- **Hard caps on output size.** Log output is error-extracted and byte-capped
  (`errBudget`, `fallbackTail` in ci.go). Any new tool that can produce large output
  needs a similar cap.

## Architecture

- `cmd/main.go` — entry point, calls `pkg/commands.Execute`.
- `pkg/commands/` — cobra CLI. Root command runs the server in stdio mode
  (logs go to stderr; stdout carries the protocol). `http` subcommand runs
  Streamable HTTP on `:8080` at `/mcp`. `version` prints the version.
- `pkg/server/` — the MCP server itself:
  - `server.go` — server setup, startup preflight (warns if `gh`/`git`/auth are
    missing), and `registerTools()`. New tool groups get registered here.
  - `registry.go` — `toolEntry` pairs a tool definition with its handler. All tool
    groups return `[]toolEntry` (see `prTools`/`ciTools`/`backportTools`); the server
    either registers them directly (full mode) or dispatches through the lazy meta
    tools. New tools must go through this registry, never `AddTool` directly.
  - `lazy.go` — optional lazy-loading mode (`--lazy` flag). Instead of advertising
    every tool schema in `tools/list`, the server exposes only three meta tools:
    `tool_list` (names + one-line summaries), `tool_describe` (full schema of one
    tool), and `tool_call` (execute by name with an `args` object). `tool_call`
    pre-checks required parameters and puts the tool's schema in the error message
    so a wrong call self-corrects in one round trip; stringified-JSON `args` are
    rejected with guidance. Keeps the `tools/list` payload constant as tools grow.
  - `exec.go` — `run()`/`runEnv()` execute external commands without a shell
    (no injection from model-provided input) and `wrap()` classifies failures.
    All external command execution must go through these. Read-only commands
    use `runRetry()`, which retries transient GitHub 5xx/network errors with
    backoff; mutating commands must use `run()` so they never execute twice.
  - `pr.go` — `pr_list_open`, `pr_get_status`.
  - `ci.go` — `ci_analyze_pr_failures`, `ci_get_failed_logs`, `ci_rerun_workflow`,
    plus the log-cleaning/error-extraction helpers.
  - `backport.go` — `pr_create_backport`. Works in a throwaway workspace: shallow
    clone (`--depth=1 --branch <target>`) into a temp dir, fetch the merge commit at
    depth 2, cherry-pick, push, open the PR. The workspace is always removed except
    on a cherry-pick conflict, where it is kept (with instructions) for manual
    resolution.
- `pkg/utils/`, `pkg/signal/` — logging setup and signal-aware context.

Runtime dependencies: `gh` (authenticated via `gh auth login` or `GH_TOKEN`) and `git`
on PATH. There are no GitHub API client libraries — everything shells out to `gh`.

## Common commands

```sh
go build ./...        # quick compile check
make test             # unit tests (go test -cover ./...)
make verify           # module verification
make build            # release-style build via goreleaser (snapshot)
go run ./cmd          # run the server in stdio mode
go run ./cmd http     # run in Streamable HTTP mode (-a to change address)
go run ./cmd --lazy   # lazy-loading mode: expose only the tool_list/tool_describe/tool_call meta tools
```

## Conventions

- Comments are plain English prose. No numbered lists (`1.`, `2.`) in comments, and
  avoid filler phrasing — state the constraint or the why, nothing else.
- Tool results are human-readable plain text built with `strings.Builder`, not JSON.
- Read-only tools must set `mcp.WithReadOnlyHintAnnotation(true)`; mutating tools set
  the destructive/idempotent/open-world hints (see ci_rerun_workflow and
  pr_create_backport for examples).
- Tool parameter validation follows the existing pattern: `request.RequireString` /
  `RequireFloat`, returning `mcp.NewToolResultError(err.Error()), nil` on failure
  (tool errors are results, not Go errors).
