package server

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sirupsen/logrus"
)

// Options contains the configuration for the MCP server
type Options struct {
}

type Server struct {
	mcpServer *server.MCPServer
}

func New(opts Options) *Server {
	s := server.NewMCPServer(
		"workflow-helper-mcp",
		"0.0.1",
		server.WithLogging(),
	)

	srv := &Server{
		mcpServer: s,
	}

	srv.registerTools()

	return srv
}

func (s *Server) StartStdio() error {
	logrus.Info("Starting MCP Server over STDIO")
	return server.ServeStdio(s.mcpServer)
}

func (s *Server) registerTools() {
	// Analyze CI
	analyzeTool := mcp.NewTool("ci_analyze_pr_failures",
		mcp.WithDescription("Scan PR triggered CI Workflow, analyze error logs, and identify flaky tests."),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository name (e.g., owner/repo)")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("The pull request number")),
	)
	s.mcpServer.AddTool(analyzeTool, s.handleAnalyzeCI)

	// Rerun flaky tests CI
	rerunTool := mcp.NewTool("ci_rerun_workflow",
		mcp.WithDescription("Rerun a specific CI workflow run."),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository name")),
		mcp.WithNumber("run_id", mcp.Required(), mcp.Description("The workflow run ID to rerun")),
	)
	s.mcpServer.AddTool(rerunTool, s.handleRerunCI)

	// Backport PR
	backportTool := mcp.NewTool("ci_create_backport",
		mcp.WithDescription("Help create a Backport PR and assist with CherryPick Conflicts. If conflict happens, user can fix and rerun."),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository name")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("The pull request number to backport")),
		mcp.WithString("target_branch", mcp.Required(), mcp.Description("The target branch for the backport")),
	)
	s.mcpServer.AddTool(backportTool, s.handleBackport)
}

func (s *Server) handleAnalyzeCI(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)

	// Use gh cli to find failing checks
	cmdStr := fmt.Sprintf("gh pr checks %d -R %s --json name,state,bucket,url,link --jq '.[] | select(.state == \"FAILURE\")'", prNum, repo)
	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get pr checks: %s, output: %s", err, string(out))), nil
	}

	if len(out) == 0 {
		return mcp.NewToolResultText("No failing CI workflows found for this PR."), nil
	}

	resultText := fmt.Sprintf("Failing CI workflows found:\n%s\n\nPlease use `gh run view <run-id> --log-failed` to see details or I can summarize them.", string(out))

	return mcp.NewToolResultText(resultText), nil
}

func (s *Server) handleRerunCI(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runIDFloat, err := request.RequireFloat("run_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runID := int(runIDFloat)

	cmdStr := fmt.Sprintf("gh run rerun %d -R %s --failed", runID, repo)
	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to rerun workflow: %s, output: %s", err, string(out))), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully requested rerun for failed jobs in run %d:\n%s", runID, string(out))), nil
}

func (s *Server) handleBackport(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)
	targetBranch, err := request.RequireString("target_branch")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// This is a simplified backport script for demo purposes.
	// It assumes the git repository is cloned locally and you are in the correct directory,
	// OR it can use pure gh api if possible. But cherry-pick requires local clone.
	// Since this tool needs to be run in a local repo context, we document that.

	script := fmt.Sprintf(`
set -e
echo "Starting backport for PR %d to %s"
# 1. Get the commit hash of the merged PR
MERGE_COMMIT=$(gh pr view %d -R %s --json mergeCommit -q .mergeCommit.oid)
if [ -z "$MERGE_COMMIT" ] || [ "$MERGE_COMMIT" == "null" ]; then
    echo "Error: PR is not merged or merge commit not found."
    exit 1
fi

BRANCH_NAME="backport-%d-to-%s"

# 2. Fetch origin and checkout target branch
git fetch origin %s
git checkout -B $BRANCH_NAME origin/%s

# 3. Cherry-pick
git cherry-pick $MERGE_COMMIT || {
    echo "CONFLICT_DETECTED"
    echo "Cherry-pick conflict detected! Please resolve manually and then run: git cherry-pick --continue && gh pr create --base %s --head $BRANCH_NAME --title 'Backport PR %d to %s' --body 'Backporting #%d'"
    exit 1
}

# 4. Push and create PR
git push origin $BRANCH_NAME
gh pr create --base %s --head $BRANCH_NAME --title "Backport PR %d to %s" --body "Backporting #%d"
`, prNum, targetBranch, prNum, repo, prNum, targetBranch, targetBranch, targetBranch, targetBranch, prNum, targetBranch, prNum, targetBranch, prNum, targetBranch, prNum)

	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	out, err := cmd.CombinedOutput()
	outputStr := string(out)

	if err != nil {
		if strings.Contains(outputStr, "CONFLICT_DETECTED") {
			return mcp.NewToolResultError(fmt.Sprintf("Cherry-pick conflict detected. Please resolve manually:\n%s", outputStr)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("failed to perform backport: %s\nOutput:\n%s", err, outputStr)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Backport PR created successfully:\n%s", outputStr)), nil
}
