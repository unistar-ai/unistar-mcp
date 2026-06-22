package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

type gitTagRow struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

func (s *Server) releaseTools() []toolEntry {
	listTagsTool := mcp.NewTool("release_list_tags",
		mcp.WithDescription(
			"List recent git tags for a repository (newest first). "+
				"Next: release_notes_draft with since_tag set to the previous release tag."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("limit", mcp.Description("Max tags to return (default 20, max 50)")),
	)

	notesTool := mcp.NewTool("release_notes_draft",
		mcp.WithDescription(
			"Draft release-notes bullets from PRs merged since a tag (default: latest tag). "+
				"For release-duty / release-notes agents. Next: edit and publish or notify_post_slack."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithString("since_tag", mcp.Description("Tag name marking the previous release (default: newest tag)")),
		mcp.WithNumber("limit", mcp.Description("Max merged PRs to include (default 30, max 50)")),
	)

	return []toolEntry{
		{tool: listTagsTool, handler: s.handleListReleaseTags},
		{tool: notesTool, handler: s.handleReleaseNotesDraft},
	}
}

func (s *Server) handleListReleaseTags(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := int(request.GetFloat("limit", 20))
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	tags, err := listRepoTags(ctx, repo, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(tags) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No tags found for %s.", repo)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d tag(s) for %s (newest first):\n", len(tags), repo)
	for _, t := range tags {
		sha := strings.TrimSpace(t.Commit.SHA)
		if len(sha) > 7 {
			sha = sha[:7]
		}
		if sha != "" {
			fmt.Fprintf(&b, "%s  %s\n", t.Name, sha)
		} else {
			fmt.Fprintf(&b, "%s\n", t.Name)
		}
	}
	b.WriteString("Next: release_notes_draft with since_tag=<previous release>.")
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handleReleaseNotesDraft(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sinceTag := strings.TrimSpace(request.GetString("since_tag", ""))
	limit := int(request.GetFloat("limit", 30))
	if limit <= 0 {
		limit = 30
	}
	if limit > 50 {
		limit = 50
	}

	if sinceTag == "" {
		tags, err := listRepoTags(ctx, repo, 1)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(tags) == 0 {
			return mcp.NewToolResultError("no tags in repository; pass since_tag or create a tag first"), nil
		}
		sinceTag = tags[0].Name
	}

	sinceDate, err := tagCommitDate(ctx, repo, sinceTag)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	text, err := formatReleaseNotesBullets(ctx, repo, sinceTag, sinceDate, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(text), nil
}

func listRepoTags(ctx context.Context, repo string, limit int) ([]gitTagRow, error) {
	path := fmt.Sprintf("repos/%s/tags?per_page=%d", repo, limit)
	res := runRetry(ctx, "", "gh", "api", path)
	if res.err != nil {
		return nil, res.wrap("failed to list tags")
	}
	var tags []gitTagRow
	if err := json.Unmarshal([]byte(res.stdout), &tags); err != nil {
		return nil, fmt.Errorf("failed to parse tag list: %w", err)
	}
	return tags, nil
}

func tagCommitDate(ctx context.Context, repo, tag string) (string, error) {
	// Prefer GitHub Release metadata when the tag is published as a release.
	releasePath := fmt.Sprintf("repos/%s/releases/tags/%s", repo, tag)
	res := runRetry(ctx, "", "gh", "api", releasePath)
	if res.err == nil {
		var rel struct {
			PublishedAt string `json:"published_at"`
			CreatedAt   string `json:"created_at"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &rel); err == nil {
			ts := strings.TrimSpace(rel.PublishedAt)
			if ts == "" {
				ts = strings.TrimSpace(rel.CreatedAt)
			}
			if d := isoDatePrefix(ts); d != "" {
				return d, nil
			}
		}
	}

	refPath := fmt.Sprintf("repos/%s/git/ref/tags/%s", repo, tag)
	res = runRetry(ctx, "", "gh", "api", refPath)
	if res.err != nil {
		return "", res.wrap(fmt.Sprintf("failed to resolve tag %q", tag))
	}
	var ref struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &ref); err != nil {
		return "", fmt.Errorf("failed to parse tag ref: %w", err)
	}
	sha := strings.TrimSpace(ref.Object.SHA)
	if sha == "" {
		return "", fmt.Errorf("empty commit for tag %q", tag)
	}

	// Annotated tags point at a tag object; peel one level to the commit SHA.
	if strings.EqualFold(strings.TrimSpace(ref.Object.Type), "tag") {
		tagObjPath := fmt.Sprintf("repos/%s/git/tags/%s", repo, sha)
		tagRes := runRetry(ctx, "", "gh", "api", tagObjPath)
		if tagRes.err == nil {
			var tagObj struct {
				Object struct {
					SHA string `json:"sha"`
				} `json:"object"`
			}
			if err := json.Unmarshal([]byte(tagRes.stdout), &tagObj); err == nil {
				if inner := strings.TrimSpace(tagObj.Object.SHA); inner != "" {
					sha = inner
				}
			}
		}
	}

	commitPath := fmt.Sprintf("repos/%s/commits/%s", repo, sha)
	commitRes := runRetry(ctx, "", "gh", "api", commitPath, "-q", ".commit.committer.date")
	if commitRes.err != nil {
		return "", commitRes.wrap(fmt.Sprintf("failed to resolve commit date for tag %q", tag))
	}
	d := isoDatePrefix(strings.TrimSpace(commitRes.stdout))
	if d == "" {
		return "", fmt.Errorf("empty commit date for tag %q", tag)
	}
	return d, nil
}

func isoDatePrefix(ts string) string {
	if len(ts) >= 10 && ts[4] == '-' && ts[7] == '-' {
		return ts[:10]
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func formatReleaseNotesBullets(ctx context.Context, repo, sinceTag, sinceDate string, limit int) (string, error) {
	args := []string{"pr", "list", "-R", repo, "--state", "merged",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,author,mergedAt",
		"--search", fmt.Sprintf("merged:>=%s", sinceDate)}

	res := runRetry(ctx, "", "gh", args...)
	if res.err != nil {
		return "", res.wrap("failed to list merged PRs for release notes")
	}

	var prs []prMergedRow
	if err := json.Unmarshal([]byte(res.stdout), &prs); err != nil {
		return "", fmt.Errorf("failed to parse merged PR list: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Release notes draft for %s since tag %s (%s):\n", repo, sinceTag, sinceDate)
	if len(prs) == 0 {
		b.WriteString("(no merged PRs since tag date)\n")
	} else {
		for _, pr := range prs {
			fmt.Fprintf(&b, "- #%d %s (@%s)\n", pr.Number, pr.Title, pr.Author.Login)
		}
	}
	b.WriteString("Next: edit bullets and publish the release or notify_post_slack.")
	return strings.TrimSpace(b.String()), nil
}
