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

	return []toolEntry{
		{tool: alertTool, handler: s.handleAlertListOpen},
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

	res := runRetry(ctx, "", "gh", "api",
		fmt.Sprintf("repos/%s/dependabot/alerts", repo),
		"-f", "state=open",
		"--paginate",
		"--jq", fmt.Sprintf(".[] | {number, state, security_advisory: {severity, summary}} | .[0:%d]", limit))
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to list dependabot alerts (requires repo admin or security permission)").Error()), nil
	}

	raw := strings.TrimSpace(res.stdout)
	if raw == "" || raw == "[]" {
		return mcp.NewToolResultText(fmt.Sprintf("No open Dependabot alerts in %s.", repo)), nil
	}

	var alerts []dependabotAlertRow
	if err := json.Unmarshal([]byte(raw), &alerts); err != nil {
		// gh --jq may emit NDJSON when paginating; try line-by-line
		var b strings.Builder
		fmt.Fprintf(&b, "Open Dependabot alerts in %s:\n", repo)
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var row dependabotAlertRow
			if json.Unmarshal([]byte(line), &row) == nil {
				fmt.Fprintf(&b, "#%d  %s  %s\n", row.Number,
					strings.ToUpper(row.SecurityAdvisory.Severity), row.SecurityAdvisory.Summary)
			}
		}
		out := strings.TrimSpace(b.String())
		if out == fmt.Sprintf("Open Dependabot alerts in %s:", repo) {
			return mcp.NewToolResultText(fmt.Sprintf("No open Dependabot alerts in %s.", repo)), nil
		}
		return mcp.NewToolResultText(out), nil
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
