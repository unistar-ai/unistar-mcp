package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const maxReviewComments = 20

type reviewRequest struct {
	Login string `json:"login"`
}

type latestReview struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	State string `json:"state"`
	Body  string `json:"body"`
}

type prReviewView struct {
	Number         int             `json:"number"`
	Title          string          `json:"title"`
	ReviewDecision string          `json:"reviewDecision"`
	ReviewRequests []reviewRequest `json:"reviewRequests"`
	LatestReviews  []latestReview  `json:"latestReviews"`
}

type inlineComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Body string `json:"body"`
}

func (s *Server) prReviewRiskTools() []toolEntry {
	reviewTool := mcp.NewTool("pr_get_review_state",
		mcp.WithDescription(
			"Compact PR review state: requested reviewers, latest reviews, inline comment snippets. "+
				"Call after pr_get_merge_blockers when review is blocking. Next: pr_post_comment (approval)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("Pull request number")),
	)

	riskTool := mcp.NewTool("pr_diff_risk_scan",
		mcp.WithDescription(
			"Heuristic risk scan on changed files: lockfiles, migrations, workflow edits, large diffs. "+
				"Call after pr_list_changed_files before pulling full pr_get_diff."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("Pull request number")),
	)

	return []toolEntry{
		{tool: reviewTool, handler: s.handlePRReviewState},
		{tool: riskTool, handler: s.handlePRDiffRiskScan},
	}
}

func (s *Server) handlePRReviewState(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)

	text, err := buildPRReviewStateText(ctx, repo, prNum)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(text), nil
}

func buildPRReviewStateText(ctx context.Context, repo string, prNum int) (string, error) {
	res := runRetry(ctx, "", "gh", "pr", "view", fmt.Sprintf("%d", prNum), "-R", repo,
		"--json", "number,title,reviewDecision,reviewRequests,latestReviews")
	if res.err != nil {
		return "", res.wrap("failed to fetch PR review state")
	}
	var pr prReviewView
	if err := json.Unmarshal([]byte(res.stdout), &pr); err != nil {
		return "", fmt.Errorf("failed to parse review state: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "PR #%d %s\n", pr.Number, pr.Title)
	fmt.Fprintf(&b, "Review decision: %s\n", reviewState(pr.ReviewDecision))

	if len(pr.ReviewRequests) > 0 {
		b.WriteString("Requested reviewers:")
		for _, rr := range pr.ReviewRequests {
			if rr.Login != "" {
				fmt.Fprintf(&b, " @%s", rr.Login)
			}
		}
		b.WriteByte('\n')
	}

	if len(pr.LatestReviews) > 0 {
		b.WriteString("Latest reviews:\n")
		for _, lr := range pr.LatestReviews {
			state := strings.ToUpper(strings.TrimSpace(lr.State))
			if state == "" {
				state = "COMMENTED"
			}
			snippet := clipForLog(strings.TrimSpace(lr.Body), 80)
			if snippet != "" {
				fmt.Fprintf(&b, "- @%s %s: %q\n", lr.Author.Login, state, snippet)
			} else {
				fmt.Fprintf(&b, "- @%s %s\n", lr.Author.Login, state)
			}
		}
	}

	cRes := runRetry(ctx, "", "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/comments", repo, prNum),
		"--paginate", "--jq", ".[] | {path, line, user: .user.login, body}")
	if cRes.err == nil && strings.TrimSpace(cRes.stdout) != "" {
		lines := strings.Split(strings.TrimSpace(cRes.stdout), "\n")
		if len(lines) > maxReviewComments {
			lines = lines[:maxReviewComments]
			fmt.Fprintf(&b, "Inline comments (first %d):\n", maxReviewComments)
		} else {
			b.WriteString("Inline comments:\n")
		}
		for _, line := range lines {
			if line == "" {
				continue
			}
			var c inlineComment
			if err := json.Unmarshal([]byte(line), &c); err != nil {
				continue
			}
			snippet := clipForLog(strings.TrimSpace(c.Body), 100)
			if c.Line > 0 {
				fmt.Fprintf(&b, "- %s:%d @%s: %q\n", c.Path, c.Line, c.User.Login, snippet)
			} else {
				fmt.Fprintf(&b, "- %s @%s: %q\n", c.Path, c.User.Login, snippet)
			}
		}
	}

	if len(pr.ReviewRequests) == 0 && len(pr.LatestReviews) == 0 {
		b.WriteString("No pending review requests or reviews recorded.")
	}
	b.WriteString("\nNext: pr_get_merge_blockers or pr_post_comment for follow-up.")
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) handlePRDiffRiskScan(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)

	files, err := fetchPRFileChanges(ctx, repo, prNum)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(formatDiffRiskScan(repo, prNum, files)), nil
}

func fetchPRFileChanges(ctx context.Context, repo string, prNum int) ([]prFileChange, error) {
	res := runRetry(ctx, "", "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/files", repo, prNum),
		"--paginate", "--jq", ".[] | {filename, additions, deletions, status}")
	if res.err != nil {
		return nil, res.wrap("failed to list changed files")
	}
	var out []prFileChange
	for _, line := range strings.Split(strings.TrimSpace(res.stdout), "\n") {
		if line == "" {
			continue
		}
		var f prFileChange
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

func formatDiffRiskScan(repo string, prNum int, files []prFileChange) string {
	if len(files) == 0 {
		return fmt.Sprintf("No changed files for %s#%d.", repo, prNum)
	}

	flags := map[string]bool{}
	var flaggedFiles []string
	totalAdd, totalDel := 0, 0

	for _, f := range files {
		totalAdd += f.Additions
		totalDel += f.Deletions
		base := strings.ToLower(filepath.Base(f.Filename))
		dir := strings.ToLower(filepath.Dir(f.Filename))

		if isLockfile(base) {
			flags["lockfile"] = true
			flaggedFiles = append(flaggedFiles, f.Filename)
		}
		if strings.Contains(dir, "migration") || strings.Contains(f.Filename, "/migrate/") {
			flags["migration"] = true
			flaggedFiles = append(flaggedFiles, f.Filename)
		}
		if strings.HasPrefix(f.Filename, ".github/workflows/") {
			flags["workflow_changed"] = true
			flaggedFiles = append(flaggedFiles, f.Filename)
		}
		if f.Additions+f.Deletions > 500 {
			flags["large_diff"] = true
		}
		if f.Status == "removed" && (strings.Contains(base, "_test.") || strings.Contains(dir, "/test")) {
			flags["tests_removed"] = true
			flaggedFiles = append(flaggedFiles, f.Filename)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Risk scan %s#%d (%d files, +%d/-%d):\n", repo, prNum, len(files), totalAdd, totalDel)
	if len(flags) == 0 {
		b.WriteString("RISK flags: (none significant)\n")
	} else {
		b.WriteString("RISK flags:")
		for name := range flags {
			fmt.Fprintf(&b, " %s", name)
		}
		b.WriteByte('\n')
	}
	if len(flaggedFiles) > 0 {
		b.WriteString("Notable files:\n")
		seen := map[string]bool{}
		n := 0
		for _, p := range flaggedFiles {
			if seen[p] {
				continue
			}
			seen[p] = true
			fmt.Fprintf(&b, "- %s\n", p)
			n++
			if n >= 12 {
				break
			}
		}
	}
	b.WriteString("Next: pr_get_diff for code review; pr_get_overview for CI context.")
	return strings.TrimSpace(b.String())
}

func isLockfile(base string) bool {
	switch base {
	case "go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "cargo.lock", "gemfile.lock", "poetry.lock":
		return true
	default:
		return strings.HasSuffix(base, ".lock")
	}
}
