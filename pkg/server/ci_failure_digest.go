package server

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const failureDigestExcerptBudget = 1_024

func (s *Server) ciDigestTools() []toolEntry {
	tool := mcp.NewTool("ci_get_failure_digest",
		mcp.WithDescription(
			"Compact failure digest: job/step/test/sig/FP/policy verdict + ~1KB log excerpt. "+
				"Lighter than ci_get_failed_logs. Next: ci_get_failed_logs for full excerpts or ci_rerun_workflow."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("run_id", mcp.Required(), mcp.Description("Workflow run ID")),
		mcp.WithNumber("job_id", mcp.Description("Optional failed job ID from ci_get_run_summary")),
	)

	return []toolEntry{
		{tool: tool, handler: s.handleGetFailureDigest},
	}
}

func (s *Server) handleGetFailureDigest(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runIDFloat, err := request.RequireFloat("run_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runID := int64(runIDFloat)
	jobID := int64(request.GetFloat("job_id", 0))

	text, err := s.buildFailureDigestText(ctx, repo, runID, jobID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	text += "\nNext: ci_get_failed_logs for full excerpts; ci_rerun_workflow if flaky."
	return mcp.NewToolResultText(strings.TrimSpace(text)), nil
}

func mergeJobsForDistill(allJobs, failedJobs []runJob) []runJob {
	if len(failedJobs) > 0 {
		return failedJobs
	}
	_, _, _, fj := classifyRunJobs(allJobs)
	return fj
}
