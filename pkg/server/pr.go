package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

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
			"List one author's open pull requests for a repository with a compact CI and review summary. "+
				"By default lists your own PRs; pass author to see another user's."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form, e.g. STARRY-S/unistar-mcp")),
		mcp.WithString("author", mcp.Description("The PR author as a GitHub login. Omit for your own PRs.")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of PRs to return (default 30)")),
	)

	statusTool := mcp.NewTool("pr_get_status",
		mcp.WithDescription(
			"Get a compact mergeability snapshot for a single pull request: CI summary, "+
				"review decision, draft state, and whether it can be merged."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("The pull request number")),
	)

	return []toolEntry{
		{tool: listTool, handler: s.handleListPRs},
		{tool: statusTool, handler: s.handlePRStatus},
	}
}

func (s *Server) handleListPRs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// gh resolves "@me" to the authenticated user server-side, so listing the
	// caller's own PRs needs no explicit login lookup.
	author := request.GetString("author", "@me")
	limit := int(request.GetFloat("limit", 30))
	if limit <= 0 {
		limit = 30
	}

	args := []string{"pr", "list", "-R", repo, "--state", "open",
		"--author", author,
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,author,isDraft,reviewDecision,statusCheckRollup"}

	res := runRetry(ctx, "", "gh", args...)
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list pull requests").Error()), nil
	}

	var prs []pullRequest
	if err := json.Unmarshal([]byte(res.stdout), &prs); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse PR list: %s", err)), nil
	}

	if len(prs) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No open PRs by %s in %s.", author, repo)), nil
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
