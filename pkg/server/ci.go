package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// workflowRun mirrors the fields we request from `gh run list --json`.
// Only fields the agent actually needs to act are requested/returned.
type workflowRun struct {
	DatabaseID   int64  `json:"databaseId"`
	WorkflowName string `json:"workflowName"`
	Conclusion   string `json:"conclusion"`
	Status       string `json:"status"`
}

type branchRun struct {
	DatabaseID   int64  `json:"databaseId"`
	WorkflowName string `json:"workflowName"`
	Conclusion   string `json:"conclusion"`
	Status       string `json:"status"`
	HeadBranch   string `json:"headBranch"`
}

func (s *Server) ciTools() []toolEntry {
	analyzeTool := mcp.NewTool("ci_analyze_pr_failures",
		mcp.WithDescription("List the failing CI workflow runs for a pull request, including their run IDs so they can be inspected (ci_get_failed_logs) or rerun (ci_rerun_workflow)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form, e.g. STARRY-S/unistar-mcp")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("The pull request number")),
	)

	logsTool := mcp.NewTool("ci_get_failed_logs",
		mcp.WithDescription("Fetch the failed-step logs of a CI workflow run so they can be analyzed to determine whether the failure is a real bug or a flaky test. Pass max_lines > 0 to page through long logs (use next_offset_lines from the response header)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("run_id", mcp.Required(), mcp.Description("The workflow run ID (from ci_analyze_pr_failures)")),
		mcp.WithNumber("offset_lines", mcp.Description("Line offset for pagination (default 0). Use next_offset_lines from a previous page.")),
		mcp.WithNumber("max_lines", mcp.Description("Lines per page (default 0 = single chunk capped at ~6KB). Set e.g. 80 to enable paging.")),
	)

	rerunTool := mcp.NewTool("ci_rerun_workflow",
		mcp.WithDescription("Rerun the failed jobs of a CI workflow run. Use this for flaky failures after inspecting the logs."),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("run_id", mcp.Required(), mcp.Description("The workflow run ID to rerun")),
	)

	listRunsTool := mcp.NewTool("ci_list_runs",
		mcp.WithDescription("List recent GitHub Actions workflow runs on a branch (default branch when branch is omitted). Used by main-guard and CI efficiency reports."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithString("branch", mcp.Description("Branch name (default: repository default branch)")),
		mcp.WithNumber("limit", mcp.Description("Max runs to return (default 15, max 50)")),
	)

	return []toolEntry{
		{tool: analyzeTool, handler: s.handleAnalyzeCI},
		{tool: logsTool, handler: s.handleGetFailedLogs},
		{tool: rerunTool, handler: s.handleRerunCI},
		{tool: listRunsTool, handler: s.handleListRuns},
	}
}

// prHeadSHA returns the head commit SHA of the given pull request.
func prHeadSHA(ctx context.Context, repo string, prNum int) (string, error) {
	res := runRetry(ctx, "", "gh", "pr", "view", fmt.Sprintf("%d", prNum),
		"-R", repo, "--json", "headRefOid", "-q", ".headRefOid")
	if res.err != nil {
		return "", res.wrap("failed to resolve PR head commit")
	}
	return strings.TrimSpace(res.stdout), nil
}

func defaultBranch(ctx context.Context, repo string) (string, error) {
	res := runRetry(ctx, "", "gh", "repo", "view", "-R", repo,
		"--json", "defaultBranchRef", "-q", ".defaultBranchRef.name")
	if res.err != nil {
		return "", res.wrap("failed to resolve default branch")
	}
	branch := strings.TrimSpace(res.stdout)
	if branch == "" {
		return "", fmt.Errorf("empty default branch for %s", repo)
	}
	return branch, nil
}

func (s *Server) handleAnalyzeCI(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)

	headSHA, err := prHeadSHA(ctx, repo, prNum)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Filter runs by commit server-side (--commit). Listing recent runs and
	// filtering locally misses everything in busy repositories, where other
	// activity pushes the PR's runs out of any recent-N window.
	res := runRetry(ctx, "", "gh", "run", "list", "-R", repo,
		"--commit", headSHA, "--limit", fmt.Sprintf("%d", ciRunListLimit),
		"--json", "databaseId,workflowName,conclusion")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list workflow runs").Error()), nil
	}

	var runs []workflowRun
	if err := json.Unmarshal([]byte(res.stdout), &runs); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse run list: %s", err)), nil
	}
	truncated := len(runs) == ciRunListLimit

	// action_required is included because pr_get_status counts it as failing:
	// a run held for approval blocks the PR just like a failed one, and the
	// conclusion column tells the model it needs approval, not log analysis.
	var failed []workflowRun
	for _, r := range runs {
		switch strings.ToLower(r.Conclusion) {
		case "failure", "timed_out", "startup_failure", "action_required":
			failed = append(failed, r)
		}
	}

	if len(failed) == 0 {
		// pr_get_status may still report failing checks here: its rollup also
		// covers external CI systems (commit statuses), which gh run list
		// cannot see. Say so instead of leaving the mismatch unexplained.
		return mcp.NewToolResultText(fmt.Sprintf(
			"No failing GitHub Actions runs for PR #%d @%s. "+
				"If pr_get_status reports failing checks, they come from an external CI system "+
				"not managed by GitHub Actions; inspect those on the PR page.",
			prNum, short(headSHA))), nil
	}

	sort.Slice(failed, func(i, j int) bool {
		return failed[i].WorkflowName < failed[j].WorkflowName
	})

	// Compact, one line per run: "<run_id>  <workflow>  <conclusion>".
	var b strings.Builder
	fmt.Fprintf(&b, "%d failing run(s) for PR #%d @%s:\n", len(failed), prNum, short(headSHA))
	if truncated {
		fmt.Fprintf(&b, "(only the most recent %d runs were inspected; there may be more)\n", ciRunListLimit)
	}
	for _, r := range failed {
		fmt.Fprintf(&b, "%d  %s  %s\n", r.DatabaseID, r.WorkflowName, strings.ToLower(r.Conclusion))
	}

	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handleGetFailedLogs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runIDFloat, err := request.RequireFloat("run_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runID := int64(runIDFloat)
	offsetLines := int(request.GetFloat("offset_lines", 0))
	maxLines := int(request.GetFloat("max_lines", 0))
	if offsetLines < 0 {
		offsetLines = 0
	}
	if maxLines < 0 {
		maxLines = 0
	}

	res := runRetry(ctx, "", "gh", "run", "view", fmt.Sprintf("%d", runID),
		"-R", repo, "--log-failed")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to fetch failed logs").Error()), nil
	}

	if strings.TrimSpace(res.stdout) == "" {
		return mcp.NewToolResultText(fmt.Sprintf(
			"Run %d has no failed-step logs (still running or cancelled).", runID)), nil
	}

	clean := cleanGHLog(res.stdout)

	var body string
	var mode string
	if extracted, n := extractErrors(clean); n > 0 {
		body = extracted
		mode = "error lines"
	} else {
		body = clean
		mode = "log tail"
	}

	if maxLines > 0 {
		page, total, next, hasMore := paginateLines(body, offsetLines, maxLines)
		if total == 0 {
			return mcp.NewToolResultText(fmt.Sprintf(
				"Run %d — empty %s (offset %d).", runID, mode, offsetLines)), nil
		}
		start := offsetLines + 1
		end := next
		if end > total {
			end = total
		}
		pageNum := offsetLines/maxLines + 1
		totalPages := (total + maxLines - 1) / maxLines
		return mcp.NewToolResultText(fmt.Sprintf(
			"Run %d — %s lines %d-%d of %d (page %d/%d, has_more: %t, next_offset_lines: %d)\n\n%s",
			runID, mode, start, end, total, pageNum, totalPages, hasMore, next, page)), nil
	}

	// Legacy single-chunk mode (~6KB cap).
	if mode == "error lines" {
		return mcp.NewToolResultText(fmt.Sprintf(
			"Run %d — %d error line(s):\n\n%s", runID, strings.Count(body, "\n")+1, tail(body, errBudget))), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf(
		"Run %d — no recognizable error lines, showing tail:\n\n%s", runID, tail(body, fallbackTail))), nil
}

func (s *Server) handleListRuns(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	branch := strings.TrimSpace(request.GetString("branch", ""))
	if branch == "" {
		branch, err = defaultBranch(ctx, repo)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	}
	limit := int(request.GetFloat("limit", 15))
	if limit <= 0 {
		limit = 15
	}
	if limit > 50 {
		limit = 50
	}

	res := runRetry(ctx, "", "gh", "run", "list", "-R", repo,
		"--branch", branch, "--limit", fmt.Sprintf("%d", limit),
		"--json", "databaseId,workflowName,conclusion,status,headBranch")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list workflow runs").Error()), nil
	}

	var runs []branchRun
	if err := json.Unmarshal([]byte(res.stdout), &runs); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse run list: %s", err)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "branch: %s\n%d run(s) for %s:\n", branch, len(runs), repo)
	for _, r := range runs {
		conclusion := strings.ToLower(strings.TrimSpace(r.Conclusion))
		if conclusion == "" {
			conclusion = strings.ToLower(strings.TrimSpace(r.Status))
		}
		fmt.Fprintf(&b, "%d  %s  %s\n", r.DatabaseID, r.WorkflowName, conclusion)
	}

	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handleRerunCI(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runIDFloat, err := request.RequireFloat("run_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runID := int64(runIDFloat)

	res := run(ctx, "", "gh", "run", "rerun", fmt.Sprintf("%d", runID), "-R", repo, "--failed")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to rerun workflow").Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Reran failed jobs in run %d.", runID)), nil
}

const (
	errBudget      = 6_000 // max bytes of extracted error lines returned
	fallbackTail   = 4_000 // max bytes returned when no error lines are recognized
	errContext     = 2     // lines of context kept around each matched error line
	ciRunListLimit = 100   // max workflow runs fetched per commit
)

// errLineRE matches lines that typically carry the actual failure signal.
var errLineRE = regexp.MustCompile(`(?i)(\berror\b|\bfailed\b|\bfailure\b|\bpanic\b|\bfatal\b|exception|traceback|assert|\bundefined\b|cannot |not found|exit code [1-9]|exit status [1-9]|✗|\bFAIL\b|\[error\])`)

// extractErrors returns the error lines of a cleaned log, each with a little
// surrounding context, and the number of matched lines. Gaps between kept
// regions are marked with a single "…" line; consecutive duplicate lines are
// collapsed. When nothing matches it returns ("", 0).
func extractErrors(clean string) (string, int) {
	lines := strings.Split(clean, "\n")
	keep := make([]bool, len(lines))
	matches := 0
	for i, ln := range lines {
		if errLineRE.MatchString(ln) {
			matches++
			lo, hi := i-errContext, i+errContext
			if lo < 0 {
				lo = 0
			}
			if hi >= len(lines) {
				hi = len(lines) - 1
			}
			for j := lo; j <= hi; j++ {
				keep[j] = true
			}
		}
	}
	if matches == 0 {
		return "", 0
	}

	var b strings.Builder
	gapOpen := false
	last := ""
	for i, ln := range lines {
		if !keep[i] {
			if gapOpen {
				b.WriteString("…\n")
				gapOpen = false
			}
			continue
		}
		gapOpen = true
		if ln == last {
			continue // collapse consecutive duplicates
		}
		b.WriteString(ln)
		b.WriteByte('\n')
		last = ln
	}
	return strings.TrimSpace(b.String()), matches
}

var (
	ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	// gh --log-failed prefixes every line with "<job>\t<step>\t<RFC3339 ts> ".
	logPrefixRE = regexp.MustCompile(`^[^\t]*\t[^\t]*\t\d{4}-\d{2}-\d{2}T[\d:.]+Z `)
)

// cleanGHLog strips ANSI escape codes and gh's per-line job/step/timestamp
// prefixes, and collapses runs of blank lines, to cut the payload sent back to
// the agent without losing the error content.
func cleanGHLog(s string) string {
	s = ansiRE.ReplaceAllString(s, "")

	var b strings.Builder
	blank := 0
	for _, line := range strings.Split(s, "\n") {
		line = logPrefixRE.ReplaceAllString(line, "")
		line = strings.TrimRight(line, "\r ")
		if line == "" {
			if blank > 0 {
				continue // collapse consecutive blank lines
			}
			blank++
		} else {
			blank = 0
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// tail returns at most n trailing bytes of s, prefixed with a notice when truncated.
// paginateLines returns a slice of lines [offset, offset+maxLines) from text.
func paginateLines(text string, offsetLines, maxLines int) (page string, totalLines, nextOffset int, hasMore bool) {
	if maxLines <= 0 {
		return text, 0, 0, false
	}
	lines := strings.Split(text, "\n")
	totalLines = len(lines)
	if offsetLines >= totalLines {
		return "", totalLines, totalLines, false
	}
	end := offsetLines + maxLines
	if end > totalLines {
		end = totalLines
	}
	page = strings.Join(lines[offsetLines:end], "\n")
	nextOffset = end
	hasMore = end < totalLines
	return page, totalLines, nextOffset, hasMore
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…[truncated]…\n" + s[len(s)-n:]
}
