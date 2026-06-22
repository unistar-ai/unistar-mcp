package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const defaultAlertLimit = 20

type dependabotAlertRow struct {
	Number int `json:"number"`
	State  string `json:"state"`
	SecurityAdvisory struct {
		Severity string `json:"severity"`
		Summary  string `json:"summary"`
	} `json:"security_advisory"`
}

func (s *Server) securityTools() []toolEntry {
	alertTool := mcp.NewTool("alert_list_open",
		mcp.WithDescription(
			"List open Dependabot security alerts for a repository (severity + summary). "+
				"Read-only; used by security-digest workflow."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("limit", mcp.Description("Max alerts (default 20)")),
	)

	summaryTool := mcp.NewTool("alert_summarize_open",
		mcp.WithDescription(
			"Roll up open Dependabot alerts by severity (counts + top summaries). "+
				"Next: alert_list_open for full list or issue triage."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("limit", mcp.Description("Max alerts to scan (default 100)")),
	)

	return []toolEntry{
		{tool: alertTool, handler: s.handleAlertListOpen},
		{tool: summaryTool, handler: s.handleAlertSummarizeOpen},
	}
}

func (s *Server) handleAlertListOpen(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := int(request.GetFloat("limit", defaultAlertLimit))
	if limit <= 0 {
		limit = defaultAlertLimit
	}

	alerts, err := fetchOpenDependabotAlerts(ctx, repo, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(alerts) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No open Dependabot alerts in %s.", repo)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d open Dependabot alert(s) in %s:\n", len(alerts), repo)
	for _, a := range alerts {
		sev := strings.ToUpper(a.SecurityAdvisory.Severity)
		summary := a.SecurityAdvisory.Summary
		if len(summary) > 120 {
			summary = summary[:120] + "…"
		}
		fmt.Fprintf(&b, "#%d  %s  %s\n", a.Number, sev, summary)
	}
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handleAlertSummarizeOpen(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := int(request.GetFloat("limit", 100))
	if limit <= 0 {
		limit = 100
	}

	alerts, err := fetchOpenDependabotAlerts(ctx, repo, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(alerts) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No open Dependabot alerts in %s.", repo)), nil
	}

	text := formatAlertSeveritySummary(repo, alerts)
	return mcp.NewToolResultText(text), nil
}

func fetchOpenDependabotAlerts(ctx context.Context, repo string, limit int) ([]dependabotAlertRow, error) {
	res := runRetry(ctx, "", "gh", "api",
		fmt.Sprintf("repos/%s/dependabot/alerts", repo),
		"-f", "state=open",
		"--paginate",
		"--jq", fmt.Sprintf(".[] | {number, state, security_advisory: {severity, summary}} | .[0:%d]", limit))
	if res.err != nil {
		return nil, res.wrap("failed to list dependabot alerts (requires repo admin or security permission)")
	}

	raw := strings.TrimSpace(res.stdout)
	if raw == "" || raw == "[]" {
		return nil, nil
	}

	var alerts []dependabotAlertRow
	if err := json.Unmarshal([]byte(raw), &alerts); err != nil {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var row dependabotAlertRow
			if json.Unmarshal([]byte(line), &row) == nil {
				alerts = append(alerts, row)
			}
		}
	}
	return alerts, nil
}

func formatAlertSeveritySummary(repo string, alerts []dependabotAlertRow) string {
	order := []string{"critical", "high", "medium", "low", "unknown"}
	counts := map[string]int{}
	bySev := map[string][]dependabotAlertRow{}
	for _, a := range alerts {
		sev := strings.ToLower(strings.TrimSpace(a.SecurityAdvisory.Severity))
		if sev == "" {
			sev = "unknown"
		}
		counts[sev]++
		bySev[sev] = append(bySev[sev], a)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Dependabot alert summary for %s (%d open):\n", repo, len(alerts))
	for _, sev := range order {
		n := counts[sev]
		if n == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s: %d\n", strings.ToUpper(sev), n)
		top := bySev[sev]
		if len(top) > 3 {
			top = top[:3]
		}
		for _, a := range top {
			summary := a.SecurityAdvisory.Summary
			if len(summary) > 80 {
				summary = summary[:80] + "…"
			}
			fmt.Fprintf(&b, "  #%d  %s\n", a.Number, summary)
		}
	}
	b.WriteString("Next: alert_list_open for full list.")
	return strings.TrimSpace(b.String())
}
