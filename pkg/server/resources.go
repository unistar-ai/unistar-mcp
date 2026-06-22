package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

var (
	prOverviewResourceRE = regexp.MustCompile(`^github://pull/([^/]+)/([^/]+)/(\d+)/overview$`)
	prBlockersResourceRE = regexp.MustCompile(`^github://pull/([^/]+)/([^/]+)/(\d+)/blockers$`)
	prCIResourceRE         = regexp.MustCompile(`^github://pull/([^/]+)/([^/]+)/(\d+)/ci$`)
	prCISnapshotResourceRE = regexp.MustCompile(`^github://pull/([^/]+)/([^/]+)/(\d+)/ci-snapshot$`)
	prReviewResourceRE   = regexp.MustCompile(`^github://pull/([^/]+)/([^/]+)/(\d+)/review$`)
	branchHealthResourceRE = regexp.MustCompile(`^github://repo/([^/]+)/([^/]+)/branch/([^/]+)/ci-health$`)
)

func (s *Server) registerResources() {
	s.mcpServer.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"github://pull/{owner}/{repo}/{number}/overview",
			"PR overview snapshot",
			mcp.WithTemplateDescription(
				"Compact PR overview (CI, review, files, failing runs). Same as pr_get_overview."),
			mcp.WithTemplateMIMEType("text/plain"),
		),
		s.handlePROverviewResource,
	)
	s.mcpServer.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"github://pull/{owner}/{repo}/{number}/blockers",
			"PR merge blockers snapshot",
			mcp.WithTemplateDescription(
				"Structured merge blockers. Same as pr_get_merge_blockers."),
			mcp.WithTemplateMIMEType("text/plain"),
		),
		s.handlePRBlockersResource,
	)
	s.mcpServer.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"github://pull/{owner}/{repo}/{number}/ci",
			"PR CI failure snapshot",
			mcp.WithTemplateDescription(
				"Failing GitHub Actions runs for a PR head commit. Same as ci_analyze_pr_failures output."),
			mcp.WithTemplateMIMEType("text/plain"),
		),
		s.handlePRCIResource,
	)
	s.mcpServer.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"github://pull/{owner}/{repo}/{number}/ci-snapshot",
			"PR CI snapshot with digests",
			mcp.WithTemplateDescription(
				"CI_KIND + failing runs + compact failure digest per run. Same as pr_get_ci_snapshot."),
			mcp.WithTemplateMIMEType("text/plain"),
		),
		s.handlePRCISnapshotResource,
	)
	s.mcpServer.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"github://pull/{owner}/{repo}/{number}/review",
			"PR review snapshot",
			mcp.WithTemplateDescription(
				"Review requests, latest reviews, inline comment snippets. Same as pr_get_review_state."),
			mcp.WithTemplateMIMEType("text/plain"),
		),
		s.handlePRReviewResource,
	)
	s.mcpServer.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"github://repo/{owner}/{repo}/branch/{branch}/ci-health",
			"Branch CI health snapshot",
			mcp.WithTemplateDescription(
				"Branch failure rate, streak, last failure. Same as ci_branch_health."),
			mcp.WithTemplateMIMEType("text/plain"),
		),
		s.handleBranchHealthResource,
	)
}

func (s *Server) handlePROverviewResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	m := prOverviewResourceRE.FindStringSubmatch(strings.TrimSpace(request.Params.URI))
	if m == nil {
		return nil, fmt.Errorf("invalid PR overview URI %q", request.Params.URI)
	}
	repo := m[1] + "/" + m[2]
	prNum, err := strconv.Atoi(m[3])
	if err != nil || prNum <= 0 {
		return nil, fmt.Errorf("invalid PR number in URI")
	}
	text, err := s.buildPROverviewText(ctx, repo, prNum)
	if err != nil {
		return nil, err
	}
	return textResourceContents(request.Params.URI, text), nil
}

func (s *Server) handlePRBlockersResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	m := prBlockersResourceRE.FindStringSubmatch(strings.TrimSpace(request.Params.URI))
	if m == nil {
		return nil, fmt.Errorf("invalid PR blockers URI %q", request.Params.URI)
	}
	repo := m[1] + "/" + m[2]
	prNum, err := strconv.Atoi(m[3])
	if err != nil || prNum <= 0 {
		return nil, fmt.Errorf("invalid PR number in URI")
	}
	text, err := buildPRMergeBlockersText(ctx, repo, prNum)
	if err != nil {
		return nil, err
	}
	return textResourceContents(request.Params.URI, text), nil
}

func (s *Server) handlePRCIResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	m := prCIResourceRE.FindStringSubmatch(strings.TrimSpace(request.Params.URI))
	if m == nil {
		return nil, fmt.Errorf("invalid PR CI URI %q", request.Params.URI)
	}
	repo := m[1] + "/" + m[2]
	prNum, err := strconv.Atoi(m[3])
	if err != nil || prNum <= 0 {
		return nil, fmt.Errorf("invalid PR number in URI")
	}
	state, err := s.loadPRFailureState(ctx, repo, prNum, true)
	if err != nil {
		return nil, err
	}
	text := formatAnalyzePRFailures(state, true)
	return textResourceContents(request.Params.URI, text), nil
}

func (s *Server) handlePRCISnapshotResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	m := prCISnapshotResourceRE.FindStringSubmatch(strings.TrimSpace(request.Params.URI))
	if m == nil {
		return nil, fmt.Errorf("invalid PR CI snapshot URI %q", request.Params.URI)
	}
	repo := m[1] + "/" + m[2]
	prNum, err := strconv.Atoi(m[3])
	if err != nil || prNum <= 0 {
		return nil, fmt.Errorf("invalid PR number in URI")
	}
	text, err := s.buildPRCISnapshotText(ctx, repo, prNum, defaultCISnapshotMaxRuns, true)
	if err != nil {
		return nil, err
	}
	return textResourceContents(request.Params.URI, text), nil
}

func (s *Server) handlePRReviewResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	m := prReviewResourceRE.FindStringSubmatch(strings.TrimSpace(request.Params.URI))
	if m == nil {
		return nil, fmt.Errorf("invalid PR review URI %q", request.Params.URI)
	}
	repo := m[1] + "/" + m[2]
	prNum, err := strconv.Atoi(m[3])
	if err != nil || prNum <= 0 {
		return nil, fmt.Errorf("invalid PR number in URI")
	}
	text, err := buildPRReviewStateText(ctx, repo, prNum)
	if err != nil {
		return nil, err
	}
	return textResourceContents(request.Params.URI, text), nil
}

func (s *Server) handleBranchHealthResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	m := branchHealthResourceRE.FindStringSubmatch(strings.TrimSpace(request.Params.URI))
	if m == nil {
		return nil, fmt.Errorf("invalid branch CI health URI %q", request.Params.URI)
	}
	repo := m[1] + "/" + m[2]
	branch := m[3]
	runs, err := listBranchRuns(ctx, repo, branch, 15)
	if err != nil {
		return nil, err
	}
	text := buildBranchHealthText(repo, branch, runs)
	return textResourceContents(request.Params.URI, text), nil
}

func textResourceContents(uri, text string) []mcp.ResourceContents {
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      uri,
			MIMEType: "text/plain",
			Text:     text,
		},
	}
}

func (s *Server) buildPROverviewText(ctx context.Context, repo string, prNum int) (string, error) {
	suffix := fmt.Sprintf("pr:%d", prNum)
	return s.cachedString("pr_get_overview", repo, suffix, func() (string, error) {
		res := runRetry(ctx, "", "gh", "pr", "view", fmt.Sprintf("%d", prNum), "-R", repo,
			"--json", "number,title,author,state,isDraft,mergeable,reviewDecision,statusCheckRollup")
		if res.err != nil {
			return "", res.wrap("failed to fetch PR overview")
		}

		var pr pullRequest
		if err := json.Unmarshal([]byte(res.stdout), &pr); err != nil {
			return "", fmt.Errorf("failed to parse PR overview: %w", err)
		}

		return formatPROverviewText(ctx, repo, prNum, pr)
	})
}

func buildPRCIText(ctx context.Context, repo string, prNum int) (string, error) {
	s := New(Options{})
	state, err := s.loadPRFailureState(ctx, repo, prNum, true)
	if err != nil {
		return "", err
	}
	return formatAnalyzePRFailures(state, true), nil
}
