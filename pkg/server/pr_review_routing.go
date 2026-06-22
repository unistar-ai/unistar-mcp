package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

type codeownersRule struct {
	pattern string
	owners  []string
}

type ownerCountRow struct {
	owner string
	count int
}

func (s *Server) prReviewRoutingTools() []toolEntry {
	tool := mcp.NewTool("pr_get_review_routing",
		mcp.WithDescription(
			"Suggest CODEOWNERS reviewers for a PR from changed file paths. "+
				"Next: pr_get_review_state or @mention in pr_post_comment draft."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("Pull request number")),
	)

	return []toolEntry{
		{tool: tool, handler: s.handlePRReviewRouting},
	}
}

func (s *Server) handlePRReviewRouting(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)

	files, err := listPRChangedPaths(ctx, repo, prNum)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rules, err := loadCODEOWNERS(ctx, repo)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	ownerHits := map[string]map[string]struct{}{}
	for _, f := range files {
		for _, owner := range matchCODEOWNERS(rules, f) {
			if ownerHits[owner] == nil {
				ownerHits[owner] = make(map[string]struct{})
			}
			ownerHits[owner][f] = struct{}{}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Review routing for PR #%d in %s (%d changed files):\n", prNum, repo, len(files))
	if len(rules) == 0 {
		b.WriteString("No CODEOWNERS file found in repo root or .github/.\n")
		return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
	}
	if len(ownerHits) == 0 {
		b.WriteString("No CODEOWNERS patterns matched changed files.\n")
		b.WriteString("Next: pr_get_review_state for requested reviewers.")
		return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
	}

	var rows []ownerCountRow
	for owner, matched := range ownerHits {
		rows = append(rows, ownerCountRow{owner: owner, count: len(matched)})
	}
	sortOwnerRows(rows)

	for _, r := range rows {
		fmt.Fprintf(&b, "%s  (%d file(s))\n", r.owner, r.count)
	}
	b.WriteString("Next: pr_get_review_state; mention owners in review request.")
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func sortOwnerRows(rows []ownerCountRow) {
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[j].count > rows[i].count || (rows[j].count == rows[i].count && rows[j].owner < rows[i].owner) {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
}

func listPRChangedPaths(ctx context.Context, repo string, prNum int) ([]string, error) {
	res := runRetry(ctx, "", "gh", "pr", "view", fmt.Sprintf("%d", prNum), "-R", repo,
		"--json", "files")
	if res.err != nil {
		return nil, res.wrap(fmt.Sprintf("failed to load PR #%d files", prNum))
	}
	var payload struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &payload); err != nil {
		return nil, fmt.Errorf("parse PR files: %w", err)
	}
	out := make([]string, 0, len(payload.Files))
	for _, f := range payload.Files {
		if p := strings.TrimSpace(f.Path); p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

func loadCODEOWNERS(ctx context.Context, repo string) ([]codeownersRule, error) {
	for _, loc := range []string{".github/CODEOWNERS", "CODEOWNERS"} {
		rules, err := fetchCODEOWNERSAt(ctx, repo, loc)
		if err != nil {
			return nil, err
		}
		if len(rules) > 0 {
			return rules, nil
		}
	}
	return nil, nil
}

func fetchCODEOWNERSAt(ctx context.Context, repo, filePath string) ([]codeownersRule, error) {
	res := runRetry(ctx, "", "gh", "api", fmt.Sprintf("repos/%s/contents/%s", repo, filePath))
	if res.err != nil {
		if res.exitCode == 404 {
			return nil, nil
		}
		return nil, res.wrap("fetch CODEOWNERS")
	}
	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &payload); err != nil {
		return nil, fmt.Errorf("parse CODEOWNERS metadata: %w", err)
	}
	if payload.Encoding != "base64" {
		return nil, fmt.Errorf("unexpected CODEOWNERS encoding: %s", payload.Encoding)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(payload.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("decode CODEOWNERS: %w", err)
	}
	return parseCODEOWNERS(string(raw)), nil
}

func parseCODEOWNERS(text string) []codeownersRule {
	var rules []codeownersRule
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		rules = append(rules, codeownersRule{pattern: parts[0], owners: parts[1:]})
	}
	return rules
}

func matchCODEOWNERS(rules []codeownersRule, filePath string) []string {
	filePath = strings.TrimPrefix(filePath, "./")
	var owners []string
	seen := map[string]struct{}{}
	for _, rule := range rules {
		if !codeownersPatternMatch(rule.pattern, filePath) {
			continue
		}
		for _, o := range rule.owners {
			if _, ok := seen[o]; ok {
				continue
			}
			seen[o] = struct{}{}
			owners = append(owners, o)
		}
	}
	return owners
}

func codeownersPatternMatch(pattern, filePath string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "*" || pattern == "**" || pattern == "/**" {
		return true
	}

	filePath = strings.TrimPrefix(filePath, "./")
	filePath = strings.TrimPrefix(filePath, "/")

	if strings.Contains(pattern, "**") {
		prefix := strings.TrimSuffix(pattern, "**")
		prefix = strings.TrimSuffix(prefix, "/")
		if prefix == "" || prefix == "/" {
			return true
		}
		prefix = strings.TrimPrefix(prefix, "/")
		return strings.HasPrefix(filePath, prefix+"/") || filePath == prefix
	}

	if strings.HasPrefix(pattern, "/") {
		pattern = strings.TrimPrefix(pattern, "/")
	} else if !strings.Contains(pattern, "/") {
		base := path.Base(filePath)
		matched, _ := path.Match(pattern, base)
		return matched
	}

	if strings.HasSuffix(pattern, "/*") {
		dir := strings.TrimSuffix(pattern, "/*")
		dir = strings.TrimPrefix(dir, "/")
		return filePath == dir || strings.HasPrefix(filePath, dir+"/")
	}
	if strings.HasSuffix(pattern, "*") && !strings.Contains(strings.TrimSuffix(pattern, "*"), "/") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(filePath, suffix)
	}
	matched, _ := path.Match(pattern, filePath)
	return matched
}
