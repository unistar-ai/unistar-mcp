package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	conflictTool := mcp.NewTool("backport_get_conflict_files",
		mcp.WithDescription(
			"List unmerged (conflict) files in a backport workspace left by pr_create_backport. "+
				"Pass workspace_path from the backport error message. Read-only."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("workspace_path", mcp.Required(), mcp.Description("Temporary workspace path from pr_create_backport conflict error")),
	)

	suggestTool := mcp.NewTool("backport_suggest_resolution",
		mcp.WithDescription(
			"Conflict resolution hints from backport workspace markers (ours vs theirs line counts). "+
				"Next: resolve in workspace, git add -A && git cherry-pick --continue."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("workspace_path", mcp.Required(), mcp.Description("Temporary workspace path from pr_create_backport conflict error")),
		mcp.WithNumber("max_files", mcp.Description("Max conflict files to analyze (default 3, max 10)")),
	)

	return []toolEntry{
		{tool: backportTool, handler: s.handleBackport},
		{tool: conflictTool, handler: s.handleBackportConflictFiles},
		{tool: suggestTool, handler: s.handleBackportSuggestResolution},
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
	// Fetch the merge commit plus the title/body used to compose the backport
	// PR. An empty merge commit means the PR isn't merged.
	res := runRetry(ctx, "", "gh", "pr", "view", fmt.Sprintf("%d", prNum),
		"-R", repo, "--json", "mergeCommit,title,body")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to fetch PR details").Error()), nil
	}
	var info struct {
		MergeCommit struct {
			OID string `json:"oid"`
		} `json:"mergeCommit"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &info); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse PR details: %s", err)), nil
	}
	mergeCommit := info.MergeCommit.OID
	if mergeCommit == "" {
		return mcp.NewToolResultError(fmt.Sprintf(
			"PR #%d does not have a merge commit (is it merged?).", prNum)), nil
	}

	// Who triggered the backport, recorded in the PR body.
	who := ghCurrentUser(ctx)

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
				"  4. gh pr create -R %s --base %s --head %s --title \"[backport -> %s] %s\" --body \"Automated backport of #%d\"\n"+
				"  5. remove the workspace: rm -rf %s\n\n"+
				"To give up instead: rm -rf %s",
			short(mergeCommit), targetBranch, branchName, workDir, res.combined(),
			workDir, branchName, repo, targetBranch, branchName, targetBranch, info.Title, prNum,
			workDir, workDir)), nil
	}

	// Push the branch and open the PR.
	if res := run(ctx, workDir, "git", "push", "-u", "origin", branchName); res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to push backport branch").Error()), nil
	}

	title := fmt.Sprintf("[backport -> %s] %s", targetBranch, info.Title)
	body := backportBody(targetBranch, who, info.Body)
	res = run(ctx, workDir, "gh", "pr", "create", "-R", repo,
		"--base", targetBranch, "--head", branchName,
		"--title", title, "--body", body)
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap(
			"cherry-pick succeeded and branch pushed, but failed to create PR").Error()), nil
	}

	// gh pr create prints the new PR URL.
	return mcp.NewToolResultText(formatToolOK(fmt.Sprintf(
		"Backport PR opened: %s", strings.TrimSpace(res.stdout)))), nil
}

func (s *Server) handleBackportConflictFiles(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	workDir, err := request.RequireString("workspace_path")
	if err != nil {
		return mcp.NewToolResultError(formatToolError(ErrValidation, err.Error(),
			"pass workspace_path from pr_create_backport error")), nil
	}
	workDir = strings.TrimSpace(workDir)
	if !isBackportWorkspace(workDir) {
		return mcp.NewToolResultError(formatToolError(ErrValidation,
			"path is not a unistar backport workspace",
			"use the exact path from pr_create_backport conflict output")), nil
	}
	if _, statErr := os.Stat(workDir); statErr != nil {
		return mcp.NewToolResultError(formatToolError(ErrNotFound, statErr.Error(),
			"workspace may have been removed — rerun pr_create_backport")), nil
	}

	res := run(ctx, workDir, "git", "diff", "--name-only", "--diff-filter=U")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list conflict files").Error()), nil
	}
	files := strings.Fields(strings.TrimSpace(res.stdout))

	var b strings.Builder
	if len(files) == 0 {
		b.WriteString("No unmerged conflict files (cherry-pick may not be in conflict state).\n")
		b.WriteString("hint: run from the workspace left by pr_create_backport")
		return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
	}

	fmt.Fprintf(&b, "%d conflict file(s) in %s:\n", len(files), workDir)
	for _, f := range files {
		fmt.Fprintf(&b, "- %s\n", f)
	}

	if len(files) > 0 {
		first := files[0]
		diffRes := run(ctx, workDir, "git", "diff", "--", first)
		if diffRes.err == nil && strings.TrimSpace(diffRes.stdout) != "" {
			b.WriteString("\nConflict snippet (first file, capped):\n")
			b.WriteString(clipForLog(diffRes.stdout, 1500))
		}
	}
	b.WriteString("\nNext: resolve in workspace, git add -A && git cherry-pick --continue")
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handleBackportSuggestResolution(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	workDir, err := request.RequireString("workspace_path")
	if err != nil {
		return mcp.NewToolResultError(formatToolError(ErrValidation, err.Error(),
			"pass workspace_path from pr_create_backport error")), nil
	}
	workDir = strings.TrimSpace(workDir)
	if !isBackportWorkspace(workDir) {
		return mcp.NewToolResultError(formatToolError(ErrValidation,
			"path is not a unistar backport workspace",
			"use the exact path from pr_create_backport conflict output")), nil
	}
	if _, statErr := os.Stat(workDir); statErr != nil {
		return mcp.NewToolResultError(formatToolError(ErrNotFound, statErr.Error(),
			"workspace may have been removed — rerun pr_create_backport")), nil
	}

	maxFiles := int(request.GetFloat("max_files", 3))
	if maxFiles <= 0 {
		maxFiles = 3
	}
	if maxFiles > 10 {
		maxFiles = 10
	}

	res := run(ctx, workDir, "git", "diff", "--name-only", "--diff-filter=U")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list conflict files").Error()), nil
	}
	files := strings.Fields(strings.TrimSpace(res.stdout))
	if len(files) == 0 {
		return mcp.NewToolResultText(
			"No unmerged conflict files — cherry-pick may not be in conflict state.\nNext: backport_get_conflict_files to verify."), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Resolution hints for %d conflict file(s) in %s:\n", len(files), workDir)
	analyze := files
	if len(analyze) > maxFiles {
		analyze = analyze[:maxFiles]
	}
	for _, f := range analyze {
		content, readErr := os.ReadFile(filepath.Join(workDir, f))
		if readErr != nil {
			fmt.Fprintf(&b, "\n%s: (could not read file)\n", f)
			continue
		}
		ours, theirs, hint := analyzeConflictMarkers(string(content))
		fmt.Fprintf(&b, "\n%s:\n  ours:%d lines  theirs:%d lines\n  hint: %s\n", f, ours, theirs, hint)
	}
	if len(files) > len(analyze) {
		fmt.Fprintf(&b, "\n(%d more file(s) — raise max_files or use backport_get_conflict_files)\n", len(files)-len(analyze))
	}
	b.WriteString("\nNext: resolve markers, git add -A && git cherry-pick --continue")
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func analyzeConflictMarkers(content string) (ours, theirs int, hint string) {
	inOurs := false
	inTheirs := false
	for _, line := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "<<<<<<<") {
			inOurs = true
			inTheirs = false
			continue
		}
		if trim == "=======" {
			inOurs = false
			inTheirs = true
			continue
		}
		if strings.HasPrefix(trim, ">>>>>>>") {
			inTheirs = false
			continue
		}
		if inOurs {
			ours++
		} else if inTheirs {
			theirs++
		}
	}
	switch {
	case ours == 0 && theirs > 0:
		hint = "only target-branch content — consider accepting incoming (theirs)"
	case theirs == 0 && ours > 0:
		hint = "only cherry-pick content — consider keeping ours"
	case ours > 0 && theirs > 0:
		hint = "both sides edited — manual merge; compare semantics before choosing"
	default:
		hint = "markers present but no distinct hunks — inspect file manually"
	}
	return ours, theirs, hint
}

func isBackportWorkspace(path string) bool {
	base := filepath.Base(filepath.Clean(path))
	return strings.HasPrefix(base, "unistar-backport-")
}

// ghCurrentUser returns the login of the authenticated GitHub user, or
// "unknown" if it cannot be resolved — the backport should not fail just
// because the username lookup did.
func ghCurrentUser(ctx context.Context) string {
	res := runRetry(ctx, "", "gh", "api", "user", "-q", ".login")
	login := strings.TrimSpace(res.stdout)
	if res.err != nil || login == "" {
		return "unknown"
	}
	return login
}

// backportBody composes the backport PR description: a provenance line
// recording the target, who triggered it, and this server, followed by the
// original PR's description.
func backportBody(targetBranch, who, originalBody string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Automated backport to `%s`, triggered by @%s, using MCP `%s`\n\n", targetBranch, who, serverName)
	b.WriteString("## Original Description\n")
	b.WriteString(originalBody)
	return b.String()
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
