package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) ciCheckURLTools() []toolEntry {
	tool := mcp.NewTool("ci_get_check_url",
		mcp.WithDescription(
			"External CI check names with details URLs from PR status rollup. "+
				"Use when ci_list_external_checks shows failures — open URL instead of ci_get_failed_logs."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("Pull request number")),
	)

	return []toolEntry{
		{tool: tool, handler: s.handleGetCheckURL},
	}
}

func checkDetailsURL(c checkRollup) string {
	if u := strings.TrimSpace(c.DetailsURL); u != "" {
		return u
	}
	return strings.TrimSpace(c.TargetURL)
}

func (s *Server) handleGetCheckURL(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)

	rollup, err := prStatusRollup(ctx, repo, prNum)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var lines []string
	for _, c := range rollup {
		if c.Typename != "StatusContext" {
			continue
		}
		name := checkDisplayName(c)
		if name == "" {
			continue
		}
		url := checkDetailsURL(c)
		verdict := strings.ToLower(checkVerdict(c))
		if url != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s  %s", name, verdict, url))
		} else {
			lines = append(lines, fmt.Sprintf("- %s: %s  (no URL in API)", name, verdict))
		}
	}

	if len(lines) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf(
			"No external status checks with URLs on PR #%d in %s.\nUse ci_analyze_pr_failures for GitHub Actions.",
			prNum, repo)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d external check(s) with URLs on PR #%d:\n", len(lines), prNum)
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n\nDo not call ci_get_failed_logs for these checks.")
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}
