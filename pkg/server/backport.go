package server

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) backportTools() []toolEntry {
	backportTool := mcp.NewTool("pr_create_backport",
		mcp.WithDescription(
			"Backport a merged pull request to a target branch: clone the repository into a "+
				"temporary workspace, cherry-pick the PR's merge commit onto the target branch, "+
				"push a backport branch and open the backport PR. The workspace is removed when "+
				"done; on a cherry-pick conflict it is kept so the conflict can be resolved "+
				"manually, and the result explains how."),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("The merged pull request number to backport")),
		mcp.WithString("target_branch", mcp.Required(), mcp.Description("The branch to backport onto, e.g. release/1.2")),
	)

	return []toolEntry{
		{tool: backportTool, handler: s.handleBackport},
	}
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
	// Resolve the merge commit of the PR. Empty/null means it isn't merged.
	res := runRetry(ctx, "", "gh", "pr", "view", fmt.Sprintf("%d", prNum),
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

	// All git operations run in a throwaway workspace so the user's own clones
	// are never touched. The workspace is removed on every path except a
	// conflict, where the in-progress cherry-pick is the value being returned.
	workDir, err := os.MkdirTemp("", "unistar-backport-")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create temporary workspace: %s", err)), nil
	}
	keepWorkDir := false
	defer func() {
		if !keepWorkDir {
			os.RemoveAll(workDir)
		}
	}()

	// Shallow-clone only the target branch; the rest of the history is not
	// needed for a cherry-pick.
	if res := run(ctx, "", "gh", "repo", "clone", repo, workDir, "--",
		"--depth", "1", "--branch", targetBranch); res.err != nil {
		return mcp.NewToolResultError(res.wrap(fmt.Sprintf(
			"failed to clone %s at branch %q (does the branch exist?)", repo, targetBranch)).Error()), nil
	}

	if res := run(ctx, workDir, "git", "checkout", "-B", branchName); res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to create backport branch").Error()), nil
	}

	// Fetch the merge commit with depth 2 so its parents exist locally: the
	// cherry-pick needs the parent tree to compute the diff.
	if res := run(ctx, workDir, "git", "fetch", "--depth", "2", "origin", mergeCommit); res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to fetch the PR merge commit").Error()), nil
	}

	// Cherry-pick the merge commit. -m 1 is only valid on merge commits,
	// so check the parent count before adding it.
	cpArgs := []string{"cherry-pick", "-x"}
	if isMergeCommit(ctx, workDir, mergeCommit) {
		cpArgs = append(cpArgs, "-m", "1")
	}
	cpArgs = append(cpArgs, mergeCommit)

	if res := run(ctx, workDir, "git", cpArgs...); res.err != nil {
		// Conflict (or other failure): keep the workspace with the
		// cherry-pick in progress so it can be resolved manually.
		keepWorkDir = true
		return mcp.NewToolResultError(fmt.Sprintf(
			"Cherry-pick of %s onto %s failed (likely a conflict). "+
				"The cherry-pick is left in progress on branch %q in the temporary workspace %s.\n\n%s\n\n"+
				"To finish manually:\n"+
				"  1. cd %s\n"+
				"  2. resolve conflicts, then: git add -A && git cherry-pick --continue\n"+
				"  3. git push -u origin %s\n"+
				"  4. gh pr create -R %s --base %s --head %s --title \"Backport #%d to %s\" --body \"Backport of #%d\"\n"+
				"  5. remove the workspace: rm -rf %s\n\n"+
				"To give up instead: rm -rf %s",
			short(mergeCommit), targetBranch, branchName, workDir, res.combined(),
			workDir, branchName, repo, targetBranch, branchName, prNum, targetBranch, prNum,
			workDir, workDir)), nil
	}

	// Push the branch and open the PR.
	if res := run(ctx, workDir, "git", "push", "-u", "origin", branchName); res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to push backport branch").Error()), nil
	}

	title := fmt.Sprintf("Backport #%d to %s", prNum, targetBranch)
	body := fmt.Sprintf("Backport of #%d to `%s`.", prNum, targetBranch)
	res = run(ctx, workDir, "gh", "pr", "create", "-R", repo,
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
