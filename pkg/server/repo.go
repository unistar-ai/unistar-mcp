package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const defaultRepoLabelLimit = 20

func (s *Server) repoTools() []toolEntry {
	infoTool := mcp.NewTool("repo_get_info",
		mcp.WithDescription(
			"Repository metadata: default branch, visibility, primary language, license, "+
				"topics, and label names. Use before ci_list_runs or when the user asks "+
				"which branch or labels apply to a repo."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("label_limit", mcp.Description("Max label names to include (default 20, max 50)")),
	)

	return []toolEntry{
		{tool: infoTool, handler: s.handleRepoGetInfo},
	}
}

type repoInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsPrivate   bool   `json:"isPrivate"`
	URL         string `json:"url"`
	Owner       struct {
		Login string `json:"login"`
	} `json:"owner"`
	DefaultBranchRef struct {
		Name string `json:"name"`
	} `json:"defaultBranchRef"`
	PrimaryLanguage *struct {
		Name string `json:"name"`
	} `json:"primaryLanguage"`
	LicenseInfo *struct {
		Name string `json:"name"`
	} `json:"licenseInfo"`
	RepositoryTopics []struct {
		Name string `json:"name"`
	} `json:"repositoryTopics"`
}

type repoLabel struct {
	Name string `json:"name"`
}

func (s *Server) handleRepoGetInfo(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	labelLimit := int(request.GetFloat("label_limit", defaultRepoLabelLimit))
	if labelLimit <= 0 {
		labelLimit = defaultRepoLabelLimit
	}
	if labelLimit > 50 {
		labelLimit = 50
	}

	suffix := fmt.Sprintf("labels:%d", labelLimit)
	text, err := s.cachedString("repo_get_info", repo, suffix, func() (string, error) {
		info, err := loadRepoInfo(ctx, repo)
		if err != nil {
			return "", err
		}
		labels, labelErr := loadRepoLabels(ctx, repo, labelLimit)
		return formatRepoInfo(repo, info, labels, labelErr), nil
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(text), nil
}

func loadRepoInfo(ctx context.Context, repo string) (*repoInfo, error) {
	res := runRetry(ctx, "", "gh", "repo", "view", repo,
		"--json", "name,description,isPrivate,url,owner,defaultBranchRef,primaryLanguage,licenseInfo,repositoryTopics")
	if res.err != nil {
		return nil, res.wrap("failed to fetch repository info")
	}
	var info repoInfo
	if err := json.Unmarshal([]byte(res.stdout), &info); err != nil {
		return nil, fmt.Errorf("failed to parse repository info: %w", err)
	}
	return &info, nil
}

func loadRepoLabels(ctx context.Context, repo string, limit int) ([]string, error) {
	res := runRetry(ctx, "", "gh", "label", "list", "-R", repo,
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "name")
	if res.err != nil {
		return nil, res.wrap("failed to list labels")
	}
	stdout := strings.TrimSpace(res.stdout)
	if stdout == "" || stdout == "[]" {
		return nil, nil
	}
	var rows []repoLabel
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		return nil, fmt.Errorf("failed to parse labels: %w", err)
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Name != "" {
			names = append(names, row.Name)
		}
	}
	return names, nil
}

func formatRepoInfo(repo string, info *repoInfo, labels []string, labelErr error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s/%s\n", info.Owner.Login, info.Name)
	if info.URL != "" {
		fmt.Fprintf(&b, "URL: %s\n", info.URL)
	}
	vis := "public"
	if info.IsPrivate {
		vis = "private"
	}
	fmt.Fprintf(&b, "Visibility: %s\n", vis)
	if branch := strings.TrimSpace(info.DefaultBranchRef.Name); branch != "" {
		fmt.Fprintf(&b, "Default branch: %s\n", branch)
	}
	if info.PrimaryLanguage != nil && info.PrimaryLanguage.Name != "" {
		fmt.Fprintf(&b, "Language: %s\n", info.PrimaryLanguage.Name)
	}
	if info.LicenseInfo != nil && info.LicenseInfo.Name != "" {
		fmt.Fprintf(&b, "License: %s\n", info.LicenseInfo.Name)
	}
	if topics := repoTopicNames(info); len(topics) > 0 {
		fmt.Fprintf(&b, "Topics: %s\n", strings.Join(topics, ", "))
	}
	switch {
	case labelErr != nil:
		fmt.Fprintf(&b, "Labels: (unavailable — %v)\n", labelErr)
	case len(labels) == 0:
		b.WriteString("Labels: (none)\n")
	default:
		fmt.Fprintf(&b, "Labels (%d): %s\n", len(labels), strings.Join(labels, ", "))
	}
	desc := strings.TrimSpace(info.Description)
	if desc != "" {
		if len(desc) > 500 {
			desc = desc[:497] + "…"
		}
		fmt.Fprintf(&b, "\n%s", desc)
	}
	return strings.TrimSpace(b.String())
}

func repoTopicNames(info *repoInfo) []string {
	if info == nil || len(info.RepositoryTopics) == 0 {
		return nil
	}
	out := make([]string, 0, len(info.RepositoryTopics))
	for _, t := range info.RepositoryTopics {
		if t.Name != "" {
			out = append(out, t.Name)
		}
	}
	return out
}
