package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const maxBatchPRStatus = 15

var prBatchNumberRE = regexp.MustCompile(`^\d+$`)

func (s *Server) prBatchTools() []toolEntry {
	batchTool := mcp.NewTool("pr_get_status_batch",
		mcp.WithDescription(
			"Fetch compact CI/review status for multiple PRs in one GraphQL round-trip. "+
				"Same line format as pr_list_open. Use when you already have PR numbers "+
				"(e.g. after pr_list_waiting_review) instead of N pr_get_status calls."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithString("pr_numbers", mcp.Required(),
			mcp.Description("Comma-separated PR numbers (max 15), e.g. \"42,43,99\"")),
	)

	overviewBatchTool := mcp.NewTool("pr_get_overview_batch",
		mcp.WithDescription(
			"Lightweight multi-PR overview in one GraphQL call (max 5): CI counts, review, file stats. "+
				"No failing run IDs — use pr_get_overview or ci_analyze_pr_failures for triage depth."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithString("pr_numbers", mcp.Required(),
			mcp.Description("Comma-separated PR numbers (max 5), e.g. \"42,43\"")),
	)

	return []toolEntry{
		{tool: batchTool, handler: s.handlePRStatusBatch},
		{tool: overviewBatchTool, handler: s.handlePROverviewBatch},
	}
}

func splitOwnerRepo(repo string) (owner, name string, err error) {
	parts := strings.Split(strings.TrimSpace(repo), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repo %q (want owner/name)", repo)
	}
	return parts[0], parts[1], nil
}

func parsePRNumberList(raw string) ([]int, error) {
	return parsePRNumberListMax(raw, maxBatchPRStatus)
}

func parsePRNumberListMax(raw string, max int) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("pr_numbers is empty")
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	})
	out := make([]int, 0, len(parts))
	seen := make(map[int]struct{})
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = strings.TrimPrefix(p, "#")
		if !prBatchNumberRE.MatchString(p) {
			return nil, fmt.Errorf("invalid PR number %q", p)
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid PR number %q", p)
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid PR numbers in %q", raw)
	}
	sort.Ints(out)
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out, nil
}

func formatPRListLine(pr pullRequest) string {
	draft := ""
	if pr.IsDraft {
		draft = " [draft]"
	}
	return fmt.Sprintf("#%d  %s  @%s  CI:%s  review:%s%s",
		pr.Number, pr.Title, pr.Author.Login,
		ciState(pr.StatusCheck), reviewState(pr.ReviewDecision), draft)
}

func buildPRStatusBatchQuery(owner, name string, numbers []int) string {
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

func fetchPRStatusBatch(ctx context.Context, repo string, numbers []int) (map[int]pullRequest, []int, error) {
	owner, name, err := splitOwnerRepo(repo)
	if err != nil {
		return nil, nil, err
	}
	query := buildPRStatusBatchQuery(owner, name, numbers)
	res := runRetry(ctx, "", "gh", "api", "graphql", "-f", "query="+query)
	if res.err != nil {
		return nil, nil, res.wrap("GraphQL batch PR status failed")
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

	found := make(map[int]pullRequest)
	var missing []int
	for _, n := range numbers {
		key := fmt.Sprintf("pr%d", n)
		raw, ok := repoFields[key]
		if !ok || len(raw) == 0 || string(raw) == "null" {
			missing = append(missing, n)
			continue
		}
		var pr pullRequest
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

func (s *Server) handlePRStatusBatch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rawNums, err := request.RequireString("pr_numbers")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	numbers, err := parsePRNumberList(rawNums)
	if err != nil {
		return mcp.NewToolResultError(formatToolError(ErrValidation, err.Error(),
			"Pass pr_numbers as comma-separated integers, e.g. \"42,43\" (max 15).")), nil
	}

	found, missing, err := fetchPRStatusBatch(ctx, repo, numbers)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Status batch for %s (%d requested, %d found):\n", repo, len(numbers), len(found))
	if len(numbers) == maxBatchPRStatus {
		b.WriteString(fmt.Sprintf("(capped at %d PRs per call)\n", maxBatchPRStatus))
	}
	for _, n := range numbers {
		pr, ok := found[n]
		if !ok {
			continue
		}
		b.WriteString(formatPRListLine(pr))
		b.WriteByte('\n')
	}
	if len(missing) > 0 {
		parts := make([]string, len(missing))
		for i, n := range missing {
			parts[i] = fmt.Sprintf("#%d", n)
		}
		fmt.Fprintf(&b, "Missing: %s (not found or not accessible)\n", strings.Join(parts, ", "))
	}
	if len(found) == 0 {
		b.WriteString("No PR status returned — verify numbers are open/accessible PRs in this repo.")
	}

	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}
