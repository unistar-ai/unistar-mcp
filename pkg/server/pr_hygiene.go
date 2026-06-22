package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

type prSizeRow struct {
	Number       int      `json:"number"`
	Title        string   `json:"title"`
	Author       prAuthor `json:"author"`
	Additions    int      `json:"additions"`
	Deletions    int      `json:"deletions"`
	ChangedFiles int      `json:"changedFiles"`
}

func (s *Server) prHygieneTools() []toolEntry {
	tool := mcp.NewTool("pr_list_large",
		mcp.WithDescription(
			"List open PRs exceeding file or line thresholds (mega-PR hygiene). "+
				"Next: pr_diff_risk_scan or pr_get_overview on top rows."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("min_files", mcp.Description("Minimum changed files (default 30)")),
		mcp.WithNumber("min_lines", mcp.Description("Minimum additions+deletions (default 1000)")),
		mcp.WithNumber("limit", mcp.Description("Max open PRs to scan (default 40, max 60)")),
	)

	return []toolEntry{
		{tool: tool, handler: s.handleListLargePRs},
	}
}

func (s *Server) handleListLargePRs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	minFiles := int(request.GetFloat("min_files", 30))
	minLines := int(request.GetFloat("min_lines", 1000))
	if minFiles <= 0 {
		minFiles = 30
	}
	if minLines <= 0 {
		minLines = 1000
	}
	scanLimit := int(request.GetFloat("limit", 40))
	if scanLimit <= 0 {
		scanLimit = 40
	}
	if scanLimit > 60 {
		scanLimit = 60
	}

	res := runRetry(ctx, "", "gh", "pr", "list", "-R", repo, "--state", "open",
		"--limit", fmt.Sprintf("%d", scanLimit),
		"--json", "number,title,author,additions,deletions,changedFiles")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list open PRs").Error()), nil
	}

	var rows []prSizeRow
	if err := json.Unmarshal([]byte(res.stdout), &rows); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse PR list: %s", err)), nil
	}

	var large []prSizeRow
	for _, pr := range rows {
		lines := pr.Additions + pr.Deletions
		if pr.ChangedFiles >= minFiles || lines >= minLines {
			large = append(large, pr)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Large PR scan in %s (scanned %d open, thresholds: %d files or %d lines):\n",
		repo, len(rows), minFiles, minLines)
	if len(large) == 0 {
		b.WriteString("(none above thresholds)\n")
	} else {
		for _, pr := range large {
			fmt.Fprintf(&b, "#%d  %s  @%s  files:%d  +%d/-%d\n",
				pr.Number, pr.Title, pr.Author.Login, pr.ChangedFiles, pr.Additions, pr.Deletions)
		}
	}
	b.WriteString("Next: pr_diff_risk_scan on top rows.")
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}
