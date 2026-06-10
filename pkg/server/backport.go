package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) registerBackportTools() {
	backportTool := mcp.NewTool("pr_create_backport",
		mcp.WithDescription(
			"Backport a merged pull request to a target branch by cherry-picking its merge commit "+
				"into a new branch and opening a backport PR. Requires a local clone of the repository "+
				"(pass its path via repo_dir). If the cherry-pick conflicts, the working tree is left "+
				"mid-cherry-pick so the conflict can be resolved manually."),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("The merged pull request number to backport")),
		mcp.WithString("target_branch", mcp.Required(), mcp.Description("The branch to backport onto, e.g. release/1.2")),
		mcp.WithString("repo_dir", mcp.Required(), mcp.Description("Absolute path to a local clone of the repository where git operations run")),
	)
	s.mcpServer.AddTool(backportTool, s.handleBackport)
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
	repoDir, err := request.RequireString("repo_dir")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// 1. Resolve the merge commit of the PR. Empty/null means it isn't merged.
	res := run(ctx, repoDir, "gh", "pr", "view", fmt.Sprintf("%d", prNum),
		"-R", repo, "--json", "mergeCommit", "-q", ".mergeCommit.oid")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to resolve merge commit").Error()), nil
	}
	mergeCommit := strings.TrimSpace(res.stdout)
	if mergeCommit == "" || mergeCommit == "null" {
		return mcp.NewToolResultError(fmt.Sprintf(
			"PR #%d does not have a merge commit (is it merged?).", prNum)), nil
	}

	branchName := fmt.Sprintf("backport-%d-to-%s", prNum, sanitizeRef(targetBranch))

	// 2. Fetch the target branch and create the backport branch from it.
	if res := run(ctx, repoDir, "git", "fetch", "origin", targetBranch); res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to fetch target branch").Error()), nil
	}
	// Also make sure we have the merge commit available locally.
	_ = run(ctx, repoDir, "git", "fetch", "origin", mergeCommit)

	if res := run(ctx, repoDir, "git", "checkout", "-B", branchName,
		"origin/"+targetBranch); res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to create backport branch").Error()), nil
	}

	// 3. Cherry-pick the merge commit. -m 1 handles merge commits; for a normal
	// squash/rebase commit git ignores it harmlessly? No — -m only valid on merges.
	// Detect parent count to decide.
	cpArgs := []string{"cherry-pick", "-x"}
	if isMergeCommit(ctx, repoDir, mergeCommit) {
		cpArgs = append(cpArgs, "-m", "1")
	}
	cpArgs = append(cpArgs, mergeCommit)

	if res := run(ctx, repoDir, "git", cpArgs...); res.err != nil {
		// Conflict (or other failure): leave the cherry-pick in progress on
		// branch %s so it can be resolved manually, then continued.
		return mcp.NewToolResultError(fmt.Sprintf(
			"Cherry-pick of %s onto %s failed (likely a conflict). "+
				"The cherry-pick is left in progress on branch %q in %s.\n\n%s\n\n"+
				"To finish manually:\n"+
				"  1. resolve conflicts, then: git add -A && git cherry-pick --continue\n"+
				"  2. git push -u origin %s\n"+
				"  3. gh pr create -R %s --base %s --head %s --title \"Backport #%d to %s\" --body \"Backport of #%d\"\n\n"+
				"To give up instead: git cherry-pick --abort",
			short(mergeCommit), targetBranch, branchName, repoDir, res.combined(),
			branchName, repo, targetBranch, branchName, prNum, targetBranch, prNum)), nil
	}

	// 4. Push the branch and open the PR.
	if res := run(ctx, repoDir, "git", "push", "-u", "origin", branchName); res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to push backport branch").Error()), nil
	}

	title := fmt.Sprintf("Backport #%d to %s", prNum, targetBranch)
	body := fmt.Sprintf("Backport of #%d to `%s`.", prNum, targetBranch)
	res = run(ctx, repoDir, "gh", "pr", "create", "-R", repo,
		"--base", targetBranch, "--head", branchName,
		"--title", title, "--body", body)
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap(
			"cherry-pick succeeded and branch pushed, but failed to create PR").Error()), nil
	}

	// gh pr create prints the new PR URL.
	return mcp.NewToolResultText(fmt.Sprintf(
		"Backport PR opened: %s", strings.TrimSpace(res.stdout))), nil
}

// isMergeCommit reports whether the commit has more than one parent.
func isMergeCommit(ctx context.Context, dir, commit string) bool {
	res := run(ctx, dir, "git", "rev-list", "--parents", "-n", "1", commit)
	// Output: "<sha> <parent1> <parent2?> ..."; >2 fields means a merge commit.
	return len(strings.Fields(strings.TrimSpace(res.stdout))) > 2
}

// sanitizeRef makes a branch name safe to embed in another ref.
func sanitizeRef(s string) string {
	return strings.NewReplacer("/", "-", " ", "-").Replace(s)
}
