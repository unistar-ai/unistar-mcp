package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const defaultBackportLabel = "needs-backport"

func (s *Server) prTier2Tools() []toolEntry {
	backportTool := mcp.NewTool("pr_list_backport_candidates",
		mcp.WithDescription(
			"Merged PRs with backport label (default needs-backport). For release-duty. "+
				"Next: pr_create_backport for each candidate."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithString("label", mcp.Description("Label filter (default needs-backport)")),
		mcp.WithString("since", mcp.Description("Merged since YYYY-MM-DD or days ago (default 14)")),
		mcp.WithNumber("limit", mcp.Description("Max PRs (default 30)")),
	)

	docsTool := mcp.NewTool("pr_is_docs_only",
		mcp.WithDescription(
			"Check whether a PR changes only docs/markdown paths (scheduler skip hint). "+
				"Next: skip deep CI triage if true; else pr_get_overview."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("Pull request number")),
	)

	return []toolEntry{
		{tool: backportTool, handler: s.handleListBackportCandidates},
		{tool: docsTool, handler: s.handlePRIsDocsOnly},
	}
}

func (s *Server) handleListBackportCandidates(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	label := strings.TrimSpace(request.GetString("label", ""))
	if label == "" {
		label = defaultBackportLabel
	}
	sinceRaw := request.GetString("since", "")
	limit := int(request.GetFloat("limit", 30))

	text, err := formatMergedPRList(ctx, repo, sinceRaw, label, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(text), nil
}

func (s *Server) handlePRIsDocsOnly(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)

	count, totalAdd, totalDel, docsOnly, err := prFilesSummary(ctx, repo, prNum)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var b strings.Builder
	if count == 0 {
		fmt.Fprintf(&b, "PR #%d in %s: no changed files detected.", prNum, repo)
		return mcp.NewToolResultText(b.String()), nil
	}
	fmt.Fprintf(&b, "PR #%d in %s: docs-only=%t\n", prNum, repo, docsOnly)
	fmt.Fprintf(&b, "Files: %d  +%d/-%d\n", count, totalAdd, totalDel)
	if docsOnly {
		b.WriteString("hint: safe to deprioritize CI triage for docs-only changes")
	} else {
		b.WriteString("Next: pr_get_overview or pr_diff_risk_scan")
	}
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func formatMergedPRList(ctx context.Context, repo, sinceRaw, label string, limit int) (string, error) {
	sinceDate, err := mergedSinceDate(sinceRaw)
	if err != nil {
		return "", err
	}
	if limit <= 0 {
		limit = 30
	}
	label = strings.TrimSpace(label)

	args := []string{"pr", "list", "-R", repo, "--state", "merged",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,author,mergedAt"}
	if label != "" {
		args = append(args, "--label", label)
	}
	search := fmt.Sprintf("merged:>=%s", sinceDate)
	args = append(args, "--search", search)

	res := runRetry(ctx, "", "gh", args...)
	if res.err != nil {
		return "", res.wrap("failed to list merged PRs")
	}

	var prs []prMergedRow
	if err := json.Unmarshal([]byte(res.stdout), &prs); err != nil {
		return "", fmt.Errorf("failed to parse merged PR list: %w", err)
	}
	if len(prs) == 0 {
		if label != "" {
			return fmt.Sprintf("No merged PRs in %s since %s with label %q.", repo, sinceDate, label), nil
		}
		return fmt.Sprintf("No merged PRs in %s since %s.", repo, sinceDate), nil
	}

	var b strings.Builder
	if label != "" {
		fmt.Fprintf(&b, "%d merged PR(s) in %s since %s (label %q):\n", len(prs), repo, sinceDate, label)
	} else {
		fmt.Fprintf(&b, "%d merged PR(s) in %s since %s:\n", len(prs), repo, sinceDate)
	}
	for _, pr := range prs {
		merged := pr.MergedAt
		if len(merged) >= 10 {
			merged = merged[:10]
		}
		fmt.Fprintf(&b, "#%d  %s  @%s  merged:%s\n",
			pr.Number, pr.Title, pr.Author.Login, merged)
	}
	if label == defaultBackportLabel {
		b.WriteString("Next: pr_create_backport for each row.")
	}
	return strings.TrimSpace(b.String()), nil
}
