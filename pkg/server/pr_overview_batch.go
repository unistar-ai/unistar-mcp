package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const maxBatchPROverview = 5

type pullRequestOverviewBatch struct {
	pullRequest
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
	ChangedFiles int `json:"changedFiles"`
}

func buildPROverviewBatchQuery(owner, name string, numbers []int) string {
	var b strings.Builder
	b.WriteString("query { repository(owner: ")
	b.WriteString(strconv.Quote(owner))
	b.WriteString(", name: ")
	b.WriteString(strconv.Quote(name))
	b.WriteString(") {")
	for _, n := range numbers {
		fmt.Fprintf(&b, " pr%d: pullRequest(number: %d) {", n, n)
		b.WriteString(`
			number title isDraft reviewDecision mergeable state
			additions deletions changedFiles
			author { login }
			statusCheckRollup {
				__typename
				... on CheckRun { name status conclusion }
				... on StatusContext { context state }
			}
		}`)
		b.WriteByte('}')
	}
	b.WriteString(" } }")
	return b.String()
}

func fetchPROverviewBatch(ctx context.Context, repo string, numbers []int) (map[int]pullRequestOverviewBatch, []int, error) {
	owner, name, err := splitOwnerRepo(repo)
	if err != nil {
		return nil, nil, err
	}
	query := buildPROverviewBatchQuery(owner, name, numbers)
	res := runRetry(ctx, "", "gh", "api", "graphql", "-f", "query="+query)
	if res.err != nil {
		return nil, nil, res.wrap("GraphQL batch PR overview failed")
	}

	var envelope struct {
		Data struct {
			Repository json.RawMessage `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &envelope); err != nil {
		return nil, nil, fmt.Errorf("failed to parse GraphQL response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return nil, nil, fmt.Errorf("GraphQL error: %s", envelope.Errors[0].Message)
	}

	var repoFields map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data.Repository, &repoFields); err != nil {
		return nil, nil, fmt.Errorf("failed to parse repository batch: %w", err)
	}

	found := make(map[int]pullRequestOverviewBatch)
	var missing []int
	for _, n := range numbers {
		key := fmt.Sprintf("pr%d", n)
		raw, ok := repoFields[key]
		if !ok || len(raw) == 0 || string(raw) == "null" {
			missing = append(missing, n)
			continue
		}
		var pr pullRequestOverviewBatch
		if err := json.Unmarshal(raw, &pr); err != nil {
			missing = append(missing, n)
			continue
		}
		if pr.Number == 0 {
			pr.Number = n
		}
		found[n] = pr
	}
	return found, missing, nil
}

func formatPROverviewBatchLine(pr pullRequestOverviewBatch) string {
	pass, fail, pending := tallyChecks(pr.StatusCheck)
	draft := ""
	if pr.IsDraft {
		draft = " draft"
	}
	return fmt.Sprintf("#%d  %s  @%s  CI:%d/%d/%d  review:%s  files:%d +%d/-%d%s",
		pr.Number, pr.Title, pr.Author.Login,
		pass, fail, pending, reviewState(pr.ReviewDecision),
		pr.ChangedFiles, pr.Additions, pr.Deletions, draft)
}

func (s *Server) handlePROverviewBatch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rawNums, err := request.RequireString("pr_numbers")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	numbers, err := parsePRNumberListMax(rawNums, maxBatchPROverview)
	if err != nil {
		return mcp.NewToolResultError(formatToolError(ErrValidation, err.Error(),
			"Pass pr_numbers as comma-separated integers, e.g. \"42,43\" (max 5).")), nil
	}

	cacheSuffix := "batch:" + rawNums
	text, err := s.cachedString("pr_get_overview_batch", repo, cacheSuffix, func() (string, error) {
		return buildPROverviewBatchText(ctx, repo, numbers)
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(text), nil
}

func buildPROverviewBatchText(ctx context.Context, repo string, numbers []int) (string, error) {
	found, missing, err := fetchPROverviewBatch(ctx, repo, numbers)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Overview batch for %s (%d requested, %d found):\n", repo, len(numbers), len(found))
	if len(numbers) == maxBatchPROverview {
		fmt.Fprintf(&b, "(capped at %d PRs per call — no failing run IDs in batch)\n", maxBatchPROverview)
	}
	for _, n := range numbers {
		pr, ok := found[n]
		if !ok {
			continue
		}
		b.WriteString(formatPROverviewBatchLine(pr))
		b.WriteByte('\n')
	}
	if len(missing) > 0 {
		parts := make([]string, len(missing))
		for i, n := range missing {
			parts[i] = fmt.Sprintf("#%d", n)
		}
		fmt.Fprintf(&b, "Missing: %s\n", strings.Join(parts, ", "))
	}
	if len(found) == 0 {
		b.WriteString("No PR overview returned.")
	} else {
		b.WriteString("Next: pr_get_overview or ci_analyze_pr_failures on PRs with failing CI.")
	}
	return strings.TrimSpace(b.String()), nil
}
