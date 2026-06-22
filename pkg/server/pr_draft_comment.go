package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) prDraftCommentTools() []toolEntry {
	tool := mcp.NewTool("pr_draft_ci_comment",
		mcp.WithDescription(
			"Draft a short PR comment from CI failure fingerprint + policy verdict. "+
				"Edit then post via pr_post_comment (mutating, needs approval)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("Pull request number")),
		mcp.WithNumber("run_id", mcp.Required(), mcp.Description("Failing workflow run ID")),
	)

	return []toolEntry{
		{tool: tool, handler: s.handleDraftCIComment},
	}
}

func (s *Server) handleDraftCIComment(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runIDFloat, err := request.RequireFloat("run_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)
	runID := int64(runIDFloat)

	analysis, err := analyzeRunFailure(ctx, repo, runID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	verdict, ruleID := classifyFailure(analysis)
	text := formatDraftCIComment(repo, prNum, analysis, verdict, ruleID)
	return mcp.NewToolResultText(text), nil
}

func formatDraftCIComment(repo string, prNum int, a runFailureAnalysis, verdict failureVerdict, ruleID string) string {
	var b strings.Builder
	b.WriteString("DRAFT COMMENT (edit before pr_post_comment):\n\n")
	fmt.Fprintf(&b, "### CI failure on run %d\n", a.RunID)
	fmt.Fprintf(&b, "Repo: %s  PR: #%d\n", repo, prNum)
	fmt.Fprintf(&b, "Workflow: **%s**", a.Workflow)
	if a.Job != "" {
		fmt.Fprintf(&b, "  Job: **%s**", a.Job)
	}
	b.WriteByte('\n')
	if a.Step != "" {
		fmt.Fprintf(&b, "Failed step: %s\n", a.Step)
	}
	if a.TestName != "" {
		fmt.Fprintf(&b, "Test: `%s`\n", a.TestName)
	}
	if a.ErrorSig != "" {
		fmt.Fprintf(&b, "Error: %s\n", a.ErrorSig)
	}
	fmt.Fprintf(&b, "Policy: **%s** (rule: %s)\n", verdict, ruleID)
	fmt.Fprintf(&b, "Fingerprint: `%s`\n", a.Fingerprint)

	b.WriteString("\n")
	switch verdict {
	case verdictTimeout, verdictInfra:
		b.WriteString("Looks like a transient infra/timeout failure — consider rerunning CI if this fingerprint is new.")
	case verdictAuth:
		b.WriteString("Auth/permission failure — please check secrets or token scopes before rerunning.")
	case verdictTest:
		b.WriteString("Test failure — please investigate the failing test before merge.")
	default:
		b.WriteString("Please investigate the linked workflow run logs.")
	}

	b.WriteString("\n\nNext: pr_post_comment with edited body (approval required).")
	return strings.TrimSpace(b.String())
}
