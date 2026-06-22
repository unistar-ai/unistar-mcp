package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultCISnapshotMaxRuns = 2
	maxCISnapshotRuns        = 5
)

func (s *Server) prCISnapshotTools() []toolEntry {
	tool := mcp.NewTool("pr_get_ci_snapshot",
		mcp.WithDescription(
			"One-call PR CI snapshot: CI_KIND + failing run list + compact failure digest per run (~1KB each). "+
				"Lighter than chaining ci_analyze_pr_failures + ci_get_failure_digest. "+
				"Next: ci_get_failed_logs for full excerpts; ci_rerun_workflow if flaky."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("Pull request number")),
		mcp.WithNumber("max_runs", mcp.Description("Max failing runs to include digests for (default 2, max 5)")),
		mcp.WithBoolean("include_external", mcp.Description("Include external/pending checks in analyze header (default true)")),
	)

	return []toolEntry{
		{tool: tool, handler: s.handlePRCISnapshot},
	}
}

func (s *Server) handlePRCISnapshot(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)
	maxRuns := int(request.GetFloat("max_runs", defaultCISnapshotMaxRuns))
	includeExternal := parseIncludeExternalArg(request.GetArguments()["include_external"], true)

	text, err := s.buildPRCISnapshotText(ctx, repo, prNum, maxRuns, includeExternal)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(text), nil
}

func parseIncludeExternalArg(raw any, defaultVal bool) bool {
	if raw == nil {
		return defaultVal
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return !strings.EqualFold(v, "false") && v != "0"
	default:
		return defaultVal
	}
}

type prFailureState struct {
	prNum           int
	headSHA         string
	realFailed      []workflowRun
	waitingApproval []workflowRun
	rollup          []checkRollup
	truncated       bool
}

func (s *Server) loadPRFailureState(ctx context.Context, repo string, prNum int, includeExternal bool) (*prFailureState, error) {
	headSHA, failed, truncated, err := failingRunsForPR(ctx, repo, prNum)
	if err != nil {
		return nil, err
	}
	state := &prFailureState{
		prNum:     prNum,
		headSHA:   headSHA,
		truncated: truncated,
	}
	for _, r := range failed {
		conc := strings.ToLower(strings.TrimSpace(r.Conclusion))
		if conc == "action_required" {
			state.waitingApproval = append(state.waitingApproval, r)
		} else {
			state.realFailed = append(state.realFailed, r)
		}
	}
	if includeExternal {
		state.rollup, _ = prStatusRollup(ctx, repo, prNum)
	}
	sort.Slice(state.realFailed, func(i, j int) bool {
		return state.realFailed[i].WorkflowName < state.realFailed[j].WorkflowName
	})
	sort.Slice(state.waitingApproval, func(i, j int) bool {
		return state.waitingApproval[i].WorkflowName < state.waitingApproval[j].WorkflowName
	})
	return state, nil
}

func formatAnalyzePRFailures(state *prFailureState, includeExternal bool) string {
	if len(state.realFailed) == 0 && len(state.waitingApproval) == 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "No failing GitHub Actions runs for PR #%d @%s.\n", state.prNum, short(state.headSHA))
		if ext := formatExternalCheckSummary(state.rollup); ext != "" {
			b.WriteString("\n")
			b.WriteString(ext)
			b.WriteString("Do not call ci_get_failed_logs for external checks — inspect the PR checks tab.\n")
		} else if pending := pendingCheckSummary(state.rollup); pending != "" {
			b.WriteString("\n")
			b.WriteString(pending)
		} else {
			b.WriteString("If pr_get_status reports failing checks, they may come from an external CI system; inspect the PR page.\n")
		}
		kind := computeCIKind(0, 0, state.rollup)
		return prependCIKind(strings.TrimSpace(b.String()), kind)
	}

	var b strings.Builder
	if len(state.realFailed) > 0 {
		fmt.Fprintf(&b, "%d failing run(s) for PR #%d @%s:\n", len(state.realFailed), state.prNum, short(state.headSHA))
		if state.truncated {
			fmt.Fprintf(&b, "(only the most recent %d runs were inspected; there may be more)\n", ciRunListLimit)
		}
		for _, r := range state.realFailed {
			label := strings.ToLower(strings.TrimSpace(r.Conclusion))
			if label == "" {
				label = strings.ToLower(strings.TrimSpace(r.Status))
			}
			fmt.Fprintf(&b, "%d  %s  %s\n", r.DatabaseID, r.WorkflowName, label)
		}
	}
	if len(state.waitingApproval) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d run(s) waiting for approval (action_required — not a code failure; do not call ci_get_failed_logs):\n", len(state.waitingApproval))
		for _, r := range state.waitingApproval {
			fmt.Fprintf(&b, "%d  %s  action_required\n", r.DatabaseID, r.WorkflowName)
		}
	}
	if includeExternal {
		if ext := formatExternalCheckSummary(state.rollup); ext != "" {
			b.WriteString("\n")
			b.WriteString(ext)
		}
	}
	kind := computeCIKind(len(state.realFailed), len(state.waitingApproval), state.rollup)
	return prependCIKind(strings.TrimSpace(b.String()), kind)
}

func (s *Server) buildPRCISnapshotText(ctx context.Context, repo string, prNum, maxRuns int, includeExternal bool) (string, error) {
	state, err := s.loadPRFailureState(ctx, repo, prNum, includeExternal)
	if err != nil {
		return "", err
	}
	if maxRuns <= 0 {
		maxRuns = defaultCISnapshotMaxRuns
	}
	if maxRuns > maxCISnapshotRuns {
		maxRuns = maxCISnapshotRuns
	}

	var b strings.Builder
	b.WriteString(formatAnalyzePRFailures(state, includeExternal))
	if len(state.realFailed) == 0 {
		return strings.TrimSpace(b.String()), nil
	}

	n := len(state.realFailed)
	if n > maxRuns {
		n = maxRuns
	}
	for i := 0; i < n; i++ {
		r := state.realFailed[i]
		fmt.Fprintf(&b, "\n\n--- run %d (%s) ---\n", r.DatabaseID, r.WorkflowName)
		digest, err := s.buildFailureDigestText(ctx, repo, r.DatabaseID, 0)
		if err != nil {
			fmt.Fprintf(&b, "(digest unavailable: %v)\n", err)
			continue
		}
		b.WriteString(digest)
	}
	if len(state.realFailed) > n {
		fmt.Fprintf(&b, "\n\n(%d more failing run(s) — call ci_get_failure_digest per run_id)\n", len(state.realFailed)-n)
	}
	b.WriteString("\nNext: ci_get_failed_logs for full excerpts; ci_rerun_workflow if flaky.")
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) buildFailureDigestText(ctx context.Context, repo string, runID, jobID int64) (string, error) {
	run, err := loadRunSummary(ctx, repo, runID)
	if err != nil {
		return "", err
	}

	logText, failedJobs, err := fetchFailedLogs(ctx, repo, runID, jobID)
	if err != nil {
		return "", err
	}

	opts := distillOptions{focus: "last", jobs: mergeJobsForDistill(run.Jobs, failedJobs)}
	body, _ := distillFailedLogText(logText, opts)
	synopsis := formatFailureLogSynopsis(repo, run, runID, failedJobs, body)

	analysis := runFailureAnalysis{
		RunID:    runID,
		Workflow: run.WorkflowName,
	}
	if len(failedJobs) > 0 {
		analysis.Job = failedJobs[0].Name
	}
	if steps := failedStepNamesForJobs(failedJobs); len(steps) > 0 {
		analysis.Step = steps[0]
	}
	analysis.TestName = extractTestNameFromLogs(body)
	analysis.ErrorSig = extractErrorSignature(body)
	if analysis.ErrorSig == "" && strings.TrimSpace(body) != "" {
		analysis.ErrorSig = truncateRunes(strings.TrimSpace(body), 200)
	}
	analysis.Fingerprint = computeFailureFingerprint(repo, analysis.Workflow, analysis.Job, analysis.TestName, analysis.ErrorSig)

	verdict, ruleID := classifyFailure(analysis)

	var b strings.Builder
	b.WriteString(synopsis)
	if hint := s.formatFlakyFingerprintHint(repo, analysis.Fingerprint); hint != "" {
		b.WriteString("\n")
		b.WriteString(hint)
	}
	fmt.Fprintf(&b, "\nVerdict: %s (%s)\n", verdict, ruleID)
	if excerpt := strings.TrimSpace(body); excerpt != "" {
		b.WriteString("\nExcerpt:\n")
		b.WriteString(tail(excerpt, failureDigestExcerptBudget))
	} else if len(failedJobs) > 0 {
		b.WriteString("\n(no log excerpt yet — job may still be running)\n")
	}
	return strings.TrimSpace(b.String()), nil
}
