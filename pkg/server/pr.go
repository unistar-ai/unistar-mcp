package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// defaultPRListLimit bounds pr_list_open output so the tool stays compact even
// on repositories with hundreds of open PRs.
const defaultPRListLimit = 20

// prAuthor is the nested author object returned by `gh pr` JSON.
type prAuthor struct {
	Login string `json:"login"`
}

// checkRollup is one entry of statusCheckRollup. It is a GraphQL union of
// CheckRun (status/conclusion) and StatusContext (state), so we read all the
// fields and normalize them in ciState.
type checkRollup struct {
	Typename   string `json:"__typename"`
	Status     string `json:"status"`     // CheckRun: QUEUED | IN_PROGRESS | COMPLETED
	Conclusion string `json:"conclusion"` // CheckRun: SUCCESS | FAILURE | ...
	State      string `json:"state"`      // StatusContext: SUCCESS | FAILURE | PENDING | ERROR
}

// pullRequest mirrors the fields we request from `gh pr list/view --json`.
type pullRequest struct {
	Number         int           `json:"number"`
	Title          string        `json:"title"`
	Author         prAuthor      `json:"author"`
	State          string        `json:"state"`
	IsDraft        bool          `json:"isDraft"`
	Mergeable      string        `json:"mergeable"`
	ReviewDecision string        `json:"reviewDecision"`
	StatusCheck    []checkRollup `json:"statusCheckRollup"`
}

func (s *Server) prTools() []toolEntry {
	listTool := mcp.NewTool("pr_list_open",
		mcp.WithDescription(
			"List open pull requests for a repository, most recent first, with a compact CI and "+
				"review summary per PR. Lists all authors by default; pass author=\"@me\" for your "+
				"own PRs or a GitHub login to filter by user. Output is bounded by limit, so this is "+
				"safe on large repositories."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form, e.g. STARRY-S/unistar-mcp")),
		mcp.WithString("author", mcp.Description("Filter by author: \"@me\" for your own PRs, or a GitHub login. Omit to list all authors.")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of PRs to return, newest first (default 20)")),
	)

	statusTool := mcp.NewTool("pr_get_status",
		mcp.WithDescription(
			"Get a compact mergeability snapshot for a single pull request: CI summary, "+
				"review decision, draft state, and whether it can be merged."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("The pull request number")),
	)

	changedFilesTool := mcp.NewTool("pr_list_changed_files",
		mcp.WithDescription(
			"List files changed in a pull request with additions/deletions counts. "+
				"Useful for docs-only filtering and large-PR warnings."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("The pull request number")),
	)

	staleTool := mcp.NewTool("pr_list_stale",
		mcp.WithDescription(
			"List open pull requests with no updates for at least N days (stale PR hygiene)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("days", mcp.Description("Minimum days since last update (default 7)")),
		mcp.WithNumber("limit", mcp.Description("Maximum stale PRs to return (default 20)")),
	)

	mergedTool := mcp.NewTool("pr_list_merged",
		mcp.WithDescription(
			"List recently merged pull requests since a date (release notes / regression link)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithString("since", mcp.Description("ISO date YYYY-MM-DD or days ago as number string (default 14)")),
		mcp.WithNumber("limit", mcp.Description("Maximum merged PRs to return (default 30)")),
	)

	diffTool := mcp.NewTool("pr_get_diff",
		mcp.WithDescription(
			"Fetch a capped unified diff for a pull request (light-review / breaking-sniff)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("The pull request number")),
		mcp.WithNumber("max_bytes", mcp.Description("Maximum diff bytes to return (default 32000)")),
	)

	commentTool := mcp.NewTool("pr_post_comment",
		mcp.WithDescription(
			"Post a comment on a pull request (mutating — requires human approval in coworker)."),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("The pull request number")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Comment body (markdown supported)")),
	)

	return []toolEntry{
		{tool: listTool, handler: s.handleListPRs},
		{tool: statusTool, handler: s.handlePRStatus},
		{tool: changedFilesTool, handler: s.handleListChangedFiles},
		{tool: staleTool, handler: s.handleListStalePRs},
		{tool: mergedTool, handler: s.handleListMergedPRs},
		{tool: diffTool, handler: s.handlePRDiff},
		{tool: commentTool, handler: s.handlePostPRComment},
	}
}

func (s *Server) handleListPRs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Default to all authors; the limit bounds the payload regardless of repo
	// size. author is a relevance filter, not a size control — pass "@me" or a
	// login to narrow it (gh resolves "@me" to the caller server-side).
	author := request.GetString("author", "")
	limit := int(request.GetFloat("limit", defaultPRListLimit))
	if limit <= 0 {
		limit = defaultPRListLimit
	}

	args := []string{"pr", "list", "-R", repo, "--state", "open",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,author,isDraft,reviewDecision,statusCheckRollup"}
	if author != "" {
		args = append(args, "--author", author)
	}

	res := runRetry(ctx, "", "gh", args...)
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list pull requests").Error()), nil
	}

	var prs []pullRequest
	if err := json.Unmarshal([]byte(res.stdout), &prs); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse PR list: %s", err)), nil
	}

	if len(prs) == 0 {
		if author != "" {
			return mcp.NewToolResultText(fmt.Sprintf("No open PRs by %s in %s.", author, repo)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("No open PRs in %s.", repo)), nil
	}

	// One compact line per PR: "#<n> <title> @<author> CI:<state> review:<decision>".
	var b strings.Builder
	fmt.Fprintf(&b, "%d open PR(s) in %s:\n", len(prs), repo)
	if len(prs) == limit {
		fmt.Fprintf(&b, "(list may be truncated at limit=%d; pass a larger limit to see more)\n", limit)
	}
	for _, pr := range prs {
		draft := ""
		if pr.IsDraft {
			draft = " [draft]"
		}
		fmt.Fprintf(&b, "#%d  %s  @%s  CI:%s  review:%s%s\n",
			pr.Number, pr.Title, pr.Author.Login,
			ciState(pr.StatusCheck), reviewState(pr.ReviewDecision), draft)
	}

	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handlePRStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		return mcp.NewToolResultError(res.wrap("failed to fetch PR status").Error()), nil
	}

	var pr pullRequest
	if err := json.Unmarshal([]byte(res.stdout), &pr); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse PR status: %s", err)), nil
	}

	pass, fail, pending := tallyChecks(pr.StatusCheck)

	var b strings.Builder
	fmt.Fprintf(&b, "PR #%d %s\n", pr.Number, pr.Title)
	fmt.Fprintf(&b, "Author: @%s   State: %s", pr.Author.Login, strings.ToLower(pr.State))
	if pr.IsDraft {
		b.WriteString(" (draft)")
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "CI: %d passing / %d failing / %d pending\n", pass, fail, pending)
	fmt.Fprintf(&b, "Review: %s\n", reviewState(pr.ReviewDecision))
	fmt.Fprintf(&b, "Mergeable: %s", mergeableState(pr.Mergeable, fail, pending))

	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

type prFileChange struct {
	Filename  string `json:"filename"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"`
}

func (s *Server) handleListChangedFiles(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)

	res := runRetry(ctx, "", "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/files", repo, prNum),
		"--paginate", "--jq", ".[] | {filename, additions, deletions, status}")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list changed files").Error()), nil
	}

	lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return mcp.NewToolResultText(fmt.Sprintf("No changed files for PR #%d in %s.", prNum, repo)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d changed file(s) in %s#%d:\n", len(lines), repo, prNum)
	totalAdd, totalDel := 0, 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		var f prFileChange
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}
		totalAdd += f.Additions
		totalDel += f.Deletions
		fmt.Fprintf(&b, "%s  +%d/-%d  (%s)\n", f.Filename, f.Additions, f.Deletions, f.Status)
	}
	fmt.Fprintf(&b, "totals: +%d/-%d", totalAdd, totalDel)
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

type prUpdatedRow struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	Author    prAuthor `json:"author"`
	IsDraft   bool     `json:"isDraft"`
	UpdatedAt string   `json:"updatedAt"`
}

func (s *Server) handleListStalePRs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	days := int(request.GetFloat("days", 7))
	if days <= 0 {
		days = 7
	}
	limit := int(request.GetFloat("limit", defaultPRListLimit))
	if limit <= 0 {
		limit = defaultPRListLimit
	}
	cutoff := time.Now().AddDate(0, 0, -days)

	res := runRetry(ctx, "", "gh", "pr", "list", "-R", repo, "--state", "open",
		"--limit", "100",
		"--json", "number,title,author,isDraft,updatedAt")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list pull requests").Error()), nil
	}

	var prs []prUpdatedRow
	if err := json.Unmarshal([]byte(res.stdout), &prs); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse PR list: %s", err)), nil
	}

	var stale []prUpdatedRow
	for _, pr := range prs {
		if pr.IsDraft {
			continue
		}
		updated, err := time.Parse(time.RFC3339, pr.UpdatedAt)
		if err != nil {
			continue
		}
		if updated.Before(cutoff) {
			stale = append(stale, pr)
		}
	}
	if len(stale) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No stale open PRs (>%dd without update) in %s.", days, repo)), nil
	}
	if len(stale) > limit {
		stale = stale[:limit]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d stale open PR(s) in %s (no update in %d+ days):\n", len(stale), repo, days)
	for _, pr := range stale {
		fmt.Fprintf(&b, "#%d  %s  @%s  updated:%s\n",
			pr.Number, pr.Title, pr.Author.Login, pr.UpdatedAt[:10])
	}
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

type prMergedRow struct {
	Number   int      `json:"number"`
	Title    string   `json:"title"`
	Author   prAuthor `json:"author"`
	MergedAt string   `json:"mergedAt"`
}

func mergedSinceDate(since string) (string, error) {
	if since == "" {
		return time.Now().AddDate(0, 0, -14).Format("2006-01-02"), nil
	}
	if len(since) == 10 && since[4] == '-' {
		return since, nil
	}
	var d int
	if _, err := fmt.Sscanf(since, "%d", &d); err != nil {
		return "", fmt.Errorf("since must be YYYY-MM-DD or days as integer, got %q", since)
	}
	if d <= 0 {
		d = 14
	}
	return time.Now().AddDate(0, 0, -d).Format("2006-01-02"), nil
}

func (s *Server) handleListMergedPRs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sinceRaw := request.GetString("since", "")
	sinceDate, err := mergedSinceDate(sinceRaw)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := int(request.GetFloat("limit", 30))
	if limit <= 0 {
		limit = 30
	}

	search := fmt.Sprintf("merged:>=%s", sinceDate)
	res := runRetry(ctx, "", "gh", "pr", "list", "-R", repo, "--state", "merged",
		"--limit", fmt.Sprintf("%d", limit),
		"--search", search,
		"--json", "number,title,author,mergedAt")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list merged PRs").Error()), nil
	}

	var prs []prMergedRow
	if err := json.Unmarshal([]byte(res.stdout), &prs); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse merged PR list: %s", err)), nil
	}
	if len(prs) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No merged PRs in %s since %s.", repo, sinceDate)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d merged PR(s) in %s since %s:\n", len(prs), repo, sinceDate)
	for _, pr := range prs {
		merged := pr.MergedAt
		if len(merged) >= 10 {
			merged = merged[:10]
		}
		fmt.Fprintf(&b, "#%d  %s  @%s  merged:%s\n",
			pr.Number, pr.Title, pr.Author.Login, merged)
	}
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handlePRDiff(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)
	maxBytes := int(request.GetFloat("max_bytes", 32000))
	if maxBytes <= 0 {
		maxBytes = 32000
	}

	res := runRetry(ctx, "", "gh", "pr", "diff", fmt.Sprintf("%d", prNum), "-R", repo)
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to fetch PR diff").Error()), nil
	}
	diff := res.stdout
	truncated := false
	if len(diff) > maxBytes {
		diff = diff[:maxBytes]
		truncated = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Diff for %s#%d (%d bytes", repo, prNum, len(diff))
	if truncated {
		b.WriteString(", truncated")
	}
	b.WriteString("):\n\n")
	b.WriteString(diff)
	if truncated {
		b.WriteString("\n\n[diff truncated at max_bytes]")
	}
	return mcp.NewToolResultText(b.String()), nil
}

func (s *Server) handlePostPRComment(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, err := request.RequireString("body")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)

	res := runRetry(ctx, "", "gh", "pr", "comment", fmt.Sprintf("%d", prNum), "-R", repo, "--body", body)
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to post PR comment").Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Comment posted on %s#%d.", repo, prNum)), nil
}

// tallyChecks counts passing, failing, and pending checks in a rollup.
func tallyChecks(checks []checkRollup) (pass, fail, pending int) {
	for _, c := range checks {
		var verdict string
		if c.Typename == "CheckRun" {
			if c.Status != "COMPLETED" {
				verdict = "PENDING"
			} else {
				verdict = c.Conclusion
			}
		} else {
			verdict = c.State
		}
		switch strings.ToUpper(verdict) {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
			pass++
		case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
			fail++
		default: // PENDING, QUEUED, IN_PROGRESS, EXPECTED, "" ...
			pending++
		}
	}
	return
}

// ciState returns a one-token CI summary for list output.
func ciState(checks []checkRollup) string {
	if len(checks) == 0 {
		return "none"
	}
	pass, fail, pending := tallyChecks(checks)
	switch {
	case fail > 0:
		return fmt.Sprintf("failing(%d)", fail)
	case pending > 0:
		return "pending"
	case pass > 0:
		return "passing"
	default:
		return "none"
	}
}

// reviewState normalizes gh's reviewDecision into a short token.
func reviewState(decision string) string {
	switch strings.ToUpper(decision) {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes-requested"
	case "REVIEW_REQUIRED":
		return "review-required"
	default:
		return "none"
	}
}

// mergeableState combines gh's mergeable flag with the CI tally into a verdict.
func mergeableState(mergeable string, fail, pending int) string {
	switch strings.ToUpper(mergeable) {
	case "CONFLICTING":
		return "no (merge conflicts)"
	case "UNKNOWN", "":
		return "unknown (still computing)"
	}
	// MERGEABLE per git, but CI may still block the merge.
	switch {
	case fail > 0:
		return "no (CI failing)"
	case pending > 0:
		return "not yet (CI pending)"
	default:
		return "yes"
	}
}
