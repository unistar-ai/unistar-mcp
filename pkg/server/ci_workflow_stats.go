package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

type workflowStats struct {
	runs             int
	failures         int
	durationSum      time.Duration
	durationSamples  int
	maxDuration      time.Duration
}

func (s *Server) ciWorkflowStatsTools() []toolEntry {
	tool := mcp.NewTool("ci_workflow_stats",
		mcp.WithDescription(
			"Per-workflow CI stats: runs, failure rate, avg/max duration on a branch. "+
				"For ci-efficiency and main-guard. Next: ci_branch_health or ci_get_run_summary on worst workflow."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithString("branch", mcp.Description("Branch (default: default branch)")),
		mcp.WithNumber("limit", mcp.Description("Runs to sample (default 30, max 50)")),
		mcp.WithNumber("top", mcp.Description("Max workflows listed (default 10, max 20)")),
	)

	return []toolEntry{
		{tool: tool, handler: s.handleCIWorkflowStats},
	}
}

func (s *Server) handleCIWorkflowStats(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	branch := strings.TrimSpace(request.GetString("branch", ""))
	if branch == "" {
		branch, err = defaultBranch(ctx, repo)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	}
	limit := int(request.GetFloat("limit", 30))
	top := int(request.GetFloat("top", 10))
	if top <= 0 {
		top = 10
	}
	if top > 20 {
		top = 20
	}

	suffix := fmt.Sprintf("branch:%s:limit:%d:top:%d", branch, limit, top)
	text, err := s.cachedString("ci_workflow_stats", repo, suffix, func() (string, error) {
		runs, err := listBranchRuns(ctx, repo, branch, limit)
		if err != nil {
			return "", err
		}
		return buildWorkflowStatsText(repo, branch, runs, top), nil
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(text), nil
}

func aggregateWorkflowStats(runs []branchRun) map[string]workflowStats {
	out := make(map[string]workflowStats)
	for _, r := range runs {
		c := runConclusion(r)
		if runStatusInProgress(c) {
			continue
		}
		name := strings.TrimSpace(r.WorkflowName)
		if name == "" {
			name = "(unknown)"
		}
		st := out[name]
		st.runs++
		if isFailedConclusion(c) {
			st.failures++
		}
		d := runDuration(r.CreatedAt, r.UpdatedAt, c)
		if d > 0 {
			st.durationSum += d
			st.durationSamples++
			if d > st.maxDuration {
				st.maxDuration = d
			}
		}
		out[name] = st
	}
	return out
}

type workflowStatsRow struct {
	name  string
	stats workflowStats
}

func buildWorkflowStatsText(repo, branch string, runs []branchRun, top int) string {
	byWF := aggregateWorkflowStats(runs)
	var b strings.Builder
	fmt.Fprintf(&b, "Workflow stats: %s  branch %s  (%d runs sampled)\n", repo, branch, len(runs))
	if len(byWF) == 0 {
		b.WriteString("No completed workflow runs in sample.\n")
		b.WriteString("Next: ci_list_runs or widen limit.")
		return strings.TrimSpace(b.String())
	}

	rows := make([]workflowStatsRow, 0, len(byWF))
	for name, st := range byWF {
		rows = append(rows, workflowStatsRow{name: name, stats: st})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].stats.failures != rows[j].stats.failures {
			return rows[i].stats.failures > rows[j].stats.failures
		}
		return rows[i].stats.runs > rows[j].stats.runs
	})
	if len(rows) > top {
		rows = rows[:top]
	}

	b.WriteString("workflow  runs  failures  fail_rate  avg_dur  max_dur\n")
	for _, row := range rows {
		st := row.stats
		rate := "-"
		if st.runs > 0 {
			rate = fmt.Sprintf("%.0f%%", float64(st.failures)*100/float64(st.runs))
		}
		avg := "-"
		if st.durationSamples > 0 {
			avg = formatDurationCompact(st.durationSum / time.Duration(st.durationSamples))
		}
		max := "-"
		if st.maxDuration > 0 {
			max = formatDurationCompact(st.maxDuration)
		}
		fmt.Fprintf(&b, "%s  %d  %d  %s  %s  %s\n",
			row.name, st.runs, st.failures, rate, avg, max)
	}
	b.WriteString("Next: ci_branch_health for streak; ci_get_run_summary on failing workflow.")
	return strings.TrimSpace(b.String())
}

func formatDurationCompact(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
