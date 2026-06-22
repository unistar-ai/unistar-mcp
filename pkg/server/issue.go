package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const defaultIssueListLimit = 20

type issueAuthor struct {
	Login string `json:"login"`
}

type issueLabel struct {
	Name string `json:"name"`
}

type issueItem struct {
	Number    int          `json:"number"`
	Title     string       `json:"title"`
	Author    issueAuthor  `json:"author"`
	State     string       `json:"state"`
	Labels    []issueLabel `json:"labels"`
	UpdatedAt string       `json:"updatedAt"`
}

func (s *Server) issueTools() []toolEntry {
	listTool := mcp.NewTool("issue_list_open",
		mcp.WithDescription(
			"List open GitHub issues for a repository with compact title/label summary. "+
				"Use issue_get for full body on a specific issue."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("limit", mcp.Description("Max issues to return (default 20)")),
	)

	getTool := mcp.NewTool("issue_get",
		mcp.WithDescription(
			"Get a single issue's title, body, labels, and state. Body is capped for context."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("issue_number", mcp.Required(), mcp.Description("Issue number")),
	)

	labelTool := mcp.NewTool("issue_add_label",
		mcp.WithDescription("Add a label to an issue (mutating). Requires human approval in unistar-coworker."),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("issue_number", mcp.Required(), mcp.Description("Issue number")),
		mcp.WithString("label", mcp.Required(), mcp.Description("Label name to add")),
	)

	searchTool := mcp.NewTool("issue_search",
		mcp.WithDescription(
			"Search GitHub issues with compact results (issue-triage). "+
				"Use GitHub search syntax in query. Next: issue_get for details."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search terms (GitHub issue search syntax)")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 20, max 50)")),
	)

	return []toolEntry{
		{tool: listTool, handler: s.handleIssueListOpen},
		{tool: getTool, handler: s.handleIssueGet},
		{tool: labelTool, handler: s.handleIssueAddLabel},
		{tool: searchTool, handler: s.handleIssueSearch},
	}
}

func (s *Server) handleIssueListOpen(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := int(request.GetFloat("limit", defaultIssueListLimit))
	if limit <= 0 {
		limit = defaultIssueListLimit
	}

	res := runRetry(ctx, "", "gh", "issue", "list", "-R", repo, "--state", "open",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,author,state,labels,updatedAt")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list issues").Error()), nil
	}

	var issues []issueItem
	if err := json.Unmarshal([]byte(res.stdout), &issues); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse issue list: %s", err)), nil
	}

	if len(issues) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No open issues in %s.", repo)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d open issue(s) in %s:\n", len(issues), repo)
	for _, iss := range issues {
		labels := formatIssueLabels(iss.Labels)
		fmt.Fprintf(&b, "#%d  %s  @%s  labels:%s  updated:%s\n",
			iss.Number, iss.Title, iss.Author.Login, labels, shortDate(iss.UpdatedAt))
	}
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handleIssueGet(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	numFloat, err := request.RequireFloat("issue_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	num := int(numFloat)

	res := runRetry(ctx, "", "gh", "issue", "view", fmt.Sprintf("%d", num), "-R", repo,
		"--json", "number,title,author,state,labels,body,updatedAt")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to fetch issue").Error()), nil
	}

	var detail struct {
		Number    int          `json:"number"`
		Title     string       `json:"title"`
		Author    issueAuthor  `json:"author"`
		State     string       `json:"state"`
		Labels    []issueLabel `json:"labels"`
		Body      string       `json:"body"`
		UpdatedAt string       `json:"updatedAt"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &detail); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse issue: %s", err)), nil
	}

	body := strings.TrimSpace(detail.Body)
	const bodyCap = 4000
	if len(body) > bodyCap {
		body = body[:bodyCap] + "\n… (body truncated)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Issue #%d %s\n", detail.Number, detail.Title)
	fmt.Fprintf(&b, "Author: @%s   State: %s   Updated: %s\n",
		detail.Author.Login, strings.ToLower(detail.State), shortDate(detail.UpdatedAt))
	fmt.Fprintf(&b, "Labels: %s\n\n", formatIssueLabels(detail.Labels))
	b.WriteString(body)
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handleIssueAddLabel(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	numFloat, err := request.RequireFloat("issue_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	label, err := request.RequireString("label")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	res := run(ctx, "", "gh", "issue", "edit", fmt.Sprintf("%d", int(numFloat)), "-R", repo,
		"--add-label", label)
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to add label").Error()), nil
	}
	return mcp.NewToolResultText(formatToolOK(fmt.Sprintf("Added label %q to issue #%d in %s.",
		label, int(numFloat), repo))), nil
}

func (s *Server) handleIssueSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return mcp.NewToolResultError(formatToolError(ErrValidation, "query is empty",
			"pass GitHub issue search terms")), nil
	}
	limit := int(request.GetFloat("limit", defaultIssueListLimit))
	if limit <= 0 {
		limit = defaultIssueListLimit
	}
	if limit > 50 {
		limit = 50
	}

	res := runRetry(ctx, "", "gh", "search", "issues", query,
		"--repo", repo,
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,author,state,labels")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to search issues").Error()), nil
	}

	var issues []issueItem
	if err := json.Unmarshal([]byte(res.stdout), &issues); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse issue search: %s", err)), nil
	}
	if len(issues) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No issues matching %q in %s.", query, repo)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d issue(s) matching %q in %s:\n", len(issues), query, repo)
	for _, iss := range issues {
		labels := formatIssueLabels(iss.Labels)
		fmt.Fprintf(&b, "#%d  %s  @%s  %s  labels:%s\n",
			iss.Number, iss.Title, iss.Author.Login, strings.ToLower(iss.State), labels)
	}
	b.WriteString("Next: issue_get for full body on a specific number.")
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func formatIssueLabels(labels []issueLabel) string {
	if len(labels) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(labels))
	for _, l := range labels {
		if l.Name != "" {
			names = append(names, l.Name)
		}
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ",")
}

func shortDate(iso string) string {
	if len(iso) >= 10 {
		return iso[:10]
	}
	return iso
}
