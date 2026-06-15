package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) prChatTools() []toolEntry {
	overviewTool := mcp.NewTool("pr_get_overview",
		mcp.WithDescription(
			"Single-call PR snapshot: status, CI/review summary, changed-files stats, and failing "+
				"GitHub Actions run IDs. Prefer this for adhoc “what's going on with #N?” questions."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("Pull request number")),
	)

	blockersTool := mcp.NewTool("pr_get_merge_blockers",
		mcp.WithDescription(
			"Structured merge blockers for one PR: conflicts, failing/pending checks, review state, draft."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("Pull request number")),
	)

	waitingTool := mcp.NewTool("pr_list_waiting_review",
		mcp.WithDescription(
			"List open PRs with passing CI that still need review (not draft). "+
				"Same compact line format as pr_list_open."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("limit", mcp.Description("Maximum PRs to return (default 20)")),
	)

	return []toolEntry{
		{tool: overviewTool, handler: s.handlePROverview},
		{tool: blockersTool, handler: s.handlePRMergeBlockers},
		{tool: waitingTool, handler: s.handleListWaitingReview},
	}
}

func (s *Server) handlePROverview(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)

	res := runRetry(ctx, "", "gh", "pr", "view", fmt.Sprintf("%d", prNum), "-R", repo,
		"--json", "number,title,author,state,isDraft,mergeable,reviewDecision,statusCheckRollup")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to fetch PR overview").Error()), nil
	}

	var pr pullRequest
	if err := json.Unmarshal([]byte(res.stdout), &pr); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse PR overview: %s", err)), nil
	}

	pass, fail, pending := tallyChecks(pr.StatusCheck)
	fileCount, totalAdd, totalDel, docsOnly, fileErr := prFilesSummary(ctx, repo, prNum)

	var b strings.Builder
	fmt.Fprintf(&b, "PR #%d %s\n", pr.Number, pr.Title)
	fmt.Fprintf(&b, "Author: @%s   State: %s", pr.Author.Login, strings.ToLower(pr.State))
	if pr.IsDraft {
		b.WriteString(" (draft)")
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "CI: %d passing / %d failing / %d pending\n", pass, fail, pending)
	fmt.Fprintf(&b, "Review: %s\n", reviewState(pr.ReviewDecision))
	fmt.Fprintf(&b, "Mergeable: %s\n", mergeableState(pr.Mergeable, fail, pending))
	if fileErr != nil {
		fmt.Fprintf(&b, "Files: (unavailable — %v)\n", fileErr)
	} else {
		fmt.Fprintf(&b, "Files: %d changed  +%d/-%d", fileCount, totalAdd, totalDel)
		if docsOnly && fileCount > 0 {
			b.WriteString("  (docs-only)")
		}
		b.WriteByte('\n')
	}

	headSHA, failed, _, runErr := failingRunsForPR(ctx, repo, prNum)
	if runErr != nil {
		fmt.Fprintf(&b, "\nFailing CI runs: (could not list — %v)", runErr)
	} else if len(failed) == 0 {
		fmt.Fprintf(&b, "\nFailing CI runs: none on GitHub Actions @%s", short(headSHA))
		if fail > 0 {
			b.WriteString(" (external CI may still be failing)")
		}
	} else {
		fmt.Fprintf(&b, "\n%d failing run(s) for PR #%d @%s:\n", len(failed), prNum, short(headSHA))
		for _, r := range failed {
			fmt.Fprintf(&b, "%d  %s  %s\n", r.DatabaseID, r.WorkflowName, strings.ToLower(r.Conclusion))
		}
	}

	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handlePRMergeBlockers(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)

	res := runRetry(ctx, "", "gh", "pr", "view", fmt.Sprintf("%d", prNum), "-R", repo,
		"--json", "number,title,author,isDraft,mergeable,reviewDecision,statusCheckRollup")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to fetch PR blockers").Error()), nil
	}

	var pr pullRequest
	if err := json.Unmarshal([]byte(res.stdout), &pr); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse PR blockers: %s", err)), nil
	}

	pass, fail, pending := tallyChecks(pr.StatusCheck)
	blockers := mergeBlockers(&pr, fail, pending)

	var b strings.Builder
	fmt.Fprintf(&b, "PR #%d %s\n", pr.Number, pr.Title)
	fmt.Fprintf(&b, "Author: @%s", pr.Author.Login)
	if pr.IsDraft {
		b.WriteString("  (draft)")
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "CI: %d passing / %d failing / %d pending\n", pass, fail, pending)
	fmt.Fprintf(&b, "Review: %s\n", reviewState(pr.ReviewDecision))
	fmt.Fprintf(&b, "Mergeable: %s\n", mergeableState(pr.Mergeable, fail, pending))
	if len(blockers) == 0 {
		b.WriteString("\nBlockers: (none)")
	} else {
		b.WriteString("\nBlockers:\n")
		for _, bl := range blockers {
			fmt.Fprintf(&b, "- %s\n", bl)
		}
	}

	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handleListWaitingReview(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := int(request.GetFloat("limit", defaultPRListLimit))
	if limit <= 0 {
		limit = defaultPRListLimit
	}

	// Fetch extra rows then filter — gh has no native review+CI filter.
	fetchLimit := limit * 5
	if fetchLimit < 50 {
		fetchLimit = 50
	}
	if fetchLimit > 100 {
		fetchLimit = 100
	}

	res := runRetry(ctx, "", "gh", "pr", "list", "-R", repo, "--state", "open",
		"--limit", fmt.Sprintf("%d", fetchLimit),
		"--json", "number,title,author,isDraft,reviewDecision,statusCheckRollup")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list pull requests").Error()), nil
	}

	var prs []pullRequest
	if err := json.Unmarshal([]byte(res.stdout), &prs); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse PR list: %s", err)), nil
	}

	var waiting []pullRequest
	for _, pr := range prs {
		if pr.IsDraft {
			continue
		}
		if strings.ToUpper(pr.ReviewDecision) != "REVIEW_REQUIRED" {
			continue
		}
		pass, fail, pending := tallyChecks(pr.StatusCheck)
		if fail > 0 || pending > 0 {
			continue
		}
		if pass == 0 && len(pr.StatusCheck) > 0 {
			continue
		}
		waiting = append(waiting, pr)
		if len(waiting) >= limit {
			break
		}
	}

	if len(waiting) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No PRs waiting for review in %s.", repo)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d PR(s) waiting for review in %s:\n", len(waiting), repo)
	for _, pr := range waiting {
		fmt.Fprintf(&b, "#%d  %s  @%s  CI:%s  review:%s\n",
			pr.Number, pr.Title, pr.Author.Login,
			ciState(pr.StatusCheck), reviewState(pr.ReviewDecision))
	}
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func mergeBlockers(pr *pullRequest, fail, pending int) []string {
	var blockers []string
	if pr.IsDraft {
		blockers = append(blockers, "draft PR")
	}
	switch strings.ToUpper(pr.Mergeable) {
	case "CONFLICTING":
		blockers = append(blockers, "merge conflicts")
	}
	for _, c := range pr.StatusCheck {
		name := checkDisplayName(c)
		if name == "" {
			name = "check"
		}
		verdict := checkVerdict(c)
		switch strings.ToUpper(verdict) {
		case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "STARTUP_FAILURE", "ACTION_REQUIRED":
			blockers = append(blockers, fmt.Sprintf("CI failing: %s (%s)", name, strings.ToLower(verdict)))
		case "PENDING", "QUEUED", "IN_PROGRESS", "EXPECTED", "":
			if verdict != "SUCCESS" && verdict != "NEUTRAL" && verdict != "SKIPPED" {
				blockers = append(blockers, fmt.Sprintf("CI pending: %s", name))
			}
		}
	}
	if fail > 0 && !hasPrefixBlocker(blockers, "CI failing") {
		blockers = append(blockers, fmt.Sprintf("CI failing (%d check(s))", fail))
	}
	if pending > 0 && !hasPrefixBlocker(blockers, "CI pending") {
		blockers = append(blockers, fmt.Sprintf("CI pending (%d check(s))", pending))
	}
	switch strings.ToUpper(pr.ReviewDecision) {
	case "REVIEW_REQUIRED":
		blockers = append(blockers, "review required")
	case "CHANGES_REQUESTED":
		blockers = append(blockers, "changes requested")
	}
	return blockers
}

func hasPrefixBlocker(blockers []string, prefix string) bool {
	for _, b := range blockers {
		if strings.HasPrefix(b, prefix) {
			return true
		}
	}
	return false
}

func checkDisplayName(c checkRollup) string {
	if c.Name != "" {
		return c.Name
	}
	return c.Context
}

func checkVerdict(c checkRollup) string {
	if c.Typename == "CheckRun" {
		if c.Status != "COMPLETED" {
			return "PENDING"
		}
		return c.Conclusion
	}
	return c.State
}

type prFileRow struct {
	Filename  string `json:"filename"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

func prFilesSummary(ctx context.Context, repo string, prNum int) (count, totalAdd, totalDel int, docsOnly bool, err error) {
	res := runRetry(ctx, "", "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/files", repo, prNum),
		"--paginate", "--jq", ".[] | {filename, additions, deletions}")
	if res.err != nil {
		return 0, 0, 0, false, res.wrap("failed to list changed files")
	}
	lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return 0, 0, 0, false, nil
	}
	docsOnly = true
	for _, line := range lines {
		if line == "" {
			continue
		}
		var f prFileRow
		if json.Unmarshal([]byte(line), &f) != nil {
			continue
		}
		count++
		totalAdd += f.Additions
		totalDel += f.Deletions
		if !isDocsPath(f.Filename) {
			docsOnly = false
		}
	}
	return count, totalAdd, totalDel, docsOnly, nil
}

func isDocsPath(path string) bool {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".rst") {
		return true
	}
	return strings.HasPrefix(lower, "docs/") ||
		strings.HasPrefix(lower, "doc/") ||
		strings.Contains(lower, "/docs/") ||
		lower == "readme.md" ||
		lower == "changelog.md"
}
