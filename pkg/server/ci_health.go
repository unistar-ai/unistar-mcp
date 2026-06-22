package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func listBranchRuns(ctx context.Context, repo, branch string, limit int) ([]branchRun, error) {
	if limit <= 0 {
		limit = 15
	}
	if limit > 50 {
		limit = 50
	}
	res := runRetry(ctx, "", "gh", "run", "list", "-R", repo,
		"--branch", branch, "--limit", fmt.Sprintf("%d", limit),
		"--json", "databaseId,workflowName,conclusion,status,headBranch,createdAt,updatedAt")
	if res.err != nil {
		return nil, res.wrap("failed to list workflow runs")
	}
	var runs []branchRun
	if err := json.Unmarshal([]byte(res.stdout), &runs); err != nil {
		return nil, fmt.Errorf("failed to parse run list: %w", err)
	}
	return runs, nil
}

func runConclusion(r branchRun) string {
	c := strings.ToLower(strings.TrimSpace(r.Conclusion))
	if c == "" {
		c = strings.ToLower(strings.TrimSpace(r.Status))
	}
	return c
}

func isFailedConclusion(c string) bool {
	switch c {
	case "failure", "timed_out", "cancelled", "action_required", "startup_failure", "stale":
		return true
	default:
		return false
	}
}

func buildBranchHealthText(repo, branch string, runs []branchRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Branch health: %s  %s\n", repo, branch)
	if len(runs) == 0 {
		b.WriteString("No workflow runs found.\n")
		b.WriteString("hint: confirm branch name or widen limit with ci_list_runs")
		return strings.TrimSpace(b.String())
	}

	completed := 0
	failed := 0
	streak := 0
	streakDone := false
	var lastFailID int64
	var lastFailWF string
	var slowestName string
	var slowestDurStr string
	var slowestDur time.Duration

	for _, r := range runs {
		c := runConclusion(r)
		if runStatusInProgress(c) {
			continue
		}
		completed++
		if isFailedConclusion(c) {
			failed++
			if !streakDone {
				streak++
			}
			if lastFailID == 0 {
				lastFailID = r.DatabaseID
				lastFailWF = r.WorkflowName
			}
		} else if !streakDone {
			streakDone = true
		}

		dur := runDuration(r.CreatedAt, r.UpdatedAt, c)
		if dur > slowestDur {
			slowestDur = dur
			slowestName = r.WorkflowName
			slowestDurStr = formatRunDuration(r.CreatedAt, r.UpdatedAt, c)
		}
	}

	failRate := "-"
	if completed > 0 {
		failRate = fmt.Sprintf("%d/%d (%.0f%%)", failed, completed, float64(failed)*100/float64(completed))
	}
	fmt.Fprintf(&b, "Recent runs: %d listed, %d completed, failures: %s\n", len(runs), completed, failRate)
	if streak > 0 {
		fmt.Fprintf(&b, "Failure streak (newest first): %d\n", streak)
	}
	if lastFailID != 0 {
		fmt.Fprintf(&b, "Last failure: run_id=%d workflow=%s\n", lastFailID, lastFailWF)
	}
	if slowestName != "" {
		fmt.Fprintf(&b, "Slowest recent run: %s (%s)\n", slowestName, slowestDurStr)
	}
	b.WriteString("Next: ci_get_run_summary on last failure; ci_list_runs for raw lines.")
	return strings.TrimSpace(b.String())
}

func runDuration(created, updated, conclusion string) time.Duration {
	if runStatusInProgress(conclusion) || created == "" || updated == "" {
		return 0
	}
	t0, err0 := time.Parse(time.RFC3339, created)
	t1, err1 := time.Parse(time.RFC3339, updated)
	if err0 != nil || err1 != nil {
		return 0
	}
	d := t1.Sub(t0)
	if d < 0 {
		return 0
	}
	return d
}

func (s *Server) ciHealthTools() []toolEntry {
	tool := mcp.NewTool("ci_branch_health",
		mcp.WithDescription(
			"Aggregate branch CI health: failure rate, failure streak, last failing run. "+
				"Compact alternative to reading many ci_list_runs lines. "+
				"Next: ci_get_run_summary → ci_failure_fingerprint → policy_classify_failure."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithString("branch", mcp.Description("Branch name (default: repository default branch)")),
		mcp.WithNumber("limit", mcp.Description("Runs to analyze (default 15, max 50)")),
	)

	return []toolEntry{
		{tool: tool, handler: s.handleBranchHealth},
	}
}

func (s *Server) handleBranchHealth(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	suffix := fmt.Sprintf("branch:%s:limit:%d", branch, limit)
	text, err := s.cachedString("ci_branch_health", repo, suffix, func() (string, error) {
		runs, err := listBranchRuns(ctx, repo, branch, limit)
		if err != nil {
			return "", err
		}
		return buildBranchHealthText(repo, branch, runs), nil
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(text), nil
}
