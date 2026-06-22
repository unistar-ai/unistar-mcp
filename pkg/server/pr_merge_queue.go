package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) prMergeQueueTools() []toolEntry {
	readyTool := mcp.NewTool("pr_list_merge_ready",
		mcp.WithDescription(
			"Open PRs ready to merge: CI green, approved, mergeable, not draft. "+
				"For merge-health / release queue. Next: pr_post_comment or merge on GitHub."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("limit", mcp.Description("Max PRs to scan (default 30, max 50)")),
	)

	blockedTool := mcp.NewTool("pr_list_merge_blocked",
		mcp.WithDescription(
			"Open PRs with passing CI that still cannot merge (conflicts, review, draft). "+
				"For merge-health. Next: pr_get_merge_blockers or pr_get_review_state."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("limit", mcp.Description("Max PRs to scan (default 30, max 50)")),
	)

	return []toolEntry{
		{tool: readyTool, handler: s.handleListMergeReady},
		{tool: blockedTool, handler: s.handleListMergeBlocked},
	}
}

func (s *Server) handleListMergeReady(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := mergeQueueFetchLimit(request)
	prs, err := fetchOpenPRsForMergeQueue(ctx, repo, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var ready []pullRequest
	for _, pr := range prs {
		if isMergeReady(pr) {
			ready = append(ready, pr)
		}
	}

	var b strings.Builder
	if len(ready) == 0 {
		fmt.Fprintf(&b, "No merge-ready PRs in %s (scanned %d open).\n", repo, len(prs))
		b.WriteString("Next: pr_list_merge_blocked or pr_list_waiting_review.")
		return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
	}
	fmt.Fprintf(&b, "%d merge-ready PR(s) in %s:\n", len(ready), repo)
	for _, pr := range ready {
		fmt.Fprintf(&b, "#%d  %s  @%s  review:%s\n",
			pr.Number, pr.Title, pr.Author.Login, reviewState(pr.ReviewDecision))
	}
	b.WriteString("Next: merge on GitHub or notify via notify_post_slack.")
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handleListMergeBlocked(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := mergeQueueFetchLimit(request)
	prs, err := fetchOpenPRsForMergeQueue(ctx, repo, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var blocked []pullRequest
	for _, pr := range prs {
		if isCIGreen(pr) && !isMergeReady(pr) {
			blocked = append(blocked, pr)
		}
	}

	var b strings.Builder
	if len(blocked) == 0 {
		fmt.Fprintf(&b, "No CI-green-but-blocked PRs in %s (scanned %d open).\n", repo, len(prs))
		return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
	}
	fmt.Fprintf(&b, "%d PR(s) with green CI but not merge-ready in %s:\n", len(blocked), repo)
	for _, pr := range blocked {
		fmt.Fprintf(&b, "#%d  %s  @%s  blocker:%s\n",
			pr.Number, pr.Title, pr.Author.Login, mergeQueueBlocker(pr))
	}
	b.WriteString("Next: pr_get_merge_blockers on top rows.")
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func mergeQueueFetchLimit(request mcp.CallToolRequest) int {
	limit := int(request.GetFloat("limit", 30))
	if limit <= 0 {
		limit = 30
	}
	if limit > 50 {
		limit = 50
	}
	return limit
}

func fetchOpenPRsForMergeQueue(ctx context.Context, repo string, limit int) ([]pullRequest, error) {
	res := runRetry(ctx, "", "gh", "pr", "list", "-R", repo, "--state", "open",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,author,isDraft,mergeable,reviewDecision,statusCheckRollup")
	if res.err != nil {
		return nil, res.wrap("failed to list open PRs")
	}
	var prs []pullRequest
	if err := json.Unmarshal([]byte(res.stdout), &prs); err != nil {
		return nil, fmt.Errorf("failed to parse PR list: %w", err)
	}
	return prs, nil
}

func isCIGreen(pr pullRequest) bool {
	pass, fail, pending := tallyChecks(pr.StatusCheck)
	if fail > 0 || pending > 0 {
		return false
	}
	if pass == 0 && len(pr.StatusCheck) > 0 {
		return false
	}
	return true
}

func isMergeReady(pr pullRequest) bool {
	if pr.IsDraft {
		return false
	}
	if !isCIGreen(pr) {
		return false
	}
	if strings.ToUpper(strings.TrimSpace(pr.ReviewDecision)) != "APPROVED" {
		return false
	}
	return strings.ToUpper(strings.TrimSpace(pr.Mergeable)) == "MERGEABLE"
}

func mergeQueueBlocker(pr pullRequest) string {
	if pr.IsDraft {
		return "draft"
	}
	switch strings.ToUpper(strings.TrimSpace(pr.Mergeable)) {
	case "CONFLICTING":
		return "merge conflicts"
	case "UNKNOWN", "":
		return "mergeability unknown"
	}
	switch strings.ToUpper(strings.TrimSpace(pr.ReviewDecision)) {
	case "REVIEW_REQUIRED":
		return "review required"
	case "CHANGES_REQUESTED":
		return "changes requested"
	}
	_, fail, pending := tallyChecks(pr.StatusCheck)
	if fail > 0 {
		return "CI failing"
	}
	if pending > 0 {
		return "CI pending"
	}
	return "other blocker (branch protection?)"
}
