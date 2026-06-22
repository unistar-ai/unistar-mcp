package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// runFailureAnalysis holds structured failure metadata for fingerprinting and compare.
type runFailureAnalysis struct {
	RunID       int64
	Workflow    string
	Job         string
	Step        string
	TestName    string
	ErrorSig    string
	Fingerprint string
}

// computeFailureFingerprint matches unistar-coworker store::compute_fingerprint:
// sha256("{repo}|{workflow}|{job}|{test_name or error_sig}").
func computeFailureFingerprint(repo, workflow, job, testName, errorSig string) string {
	fallback := testName
	if fallback == "" {
		fallback = errorSig
	}
	payload := fmt.Sprintf("%s|%s|%s|%s", repo, workflow, job, fallback)
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", sum)
}

func extractTestNameFromLogs(logs string) string {
	for _, line := range strings.Split(logs, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)
		if strings.Contains(t, "::") && (strings.Contains(t, "FAILED") || strings.Contains(low, "failed")) {
			return truncateRunes(t, 120)
		}
		if strings.HasPrefix(t, "FAIL ") || strings.HasPrefix(t, "--- FAIL:") {
			return truncateRunes(t, 120)
		}
		if strings.HasPrefix(t, "✕ ") || strings.HasPrefix(t, "× ") {
			return truncateRunes(t, 120)
		}
		if strings.Contains(low, " ... failed") && strings.HasPrefix(low, "test ") {
			return truncateRunes(t, 120)
		}
		if strings.Contains(t, ".test.") && (strings.Contains(low, " fail") || strings.HasPrefix(t, "FAIL ")) {
			return truncateRunes(t, 120)
		}
		if strings.Contains(low, "tests failed") || strings.Contains(low, "test suite failed") {
			return truncateRunes(t, 120)
		}
		if strings.HasPrefix(low, "failures:") {
			return truncateRunes(t, 120)
		}
	}
	return ""
}

func extractErrorSignature(logs string) string {
	clean := cleanGHLog(logs)
	body, _ := extractErrors(clean)
	if strings.TrimSpace(body) == "" {
		body = tail(clean, 500)
	}
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || t == "…" {
			continue
		}
		low := strings.ToLower(t)
		if strings.Contains(low, "error") || strings.Contains(low, "fail") ||
			strings.Contains(low, "panic") || strings.Contains(low, "fatal") {
			return truncateRunes(t, 200)
		}
	}
	return truncateRunes(strings.TrimSpace(body), 200)
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func analyzeRunFailure(ctx context.Context, repo string, runID int64) (runFailureAnalysis, error) {
	run, err := loadRunSummary(ctx, repo, runID)
	if err != nil {
		return runFailureAnalysis{}, err
	}

	rawLogs, failedJobs, err := fetchFailedRunLogs(ctx, repo, runID)
	if err != nil {
		return runFailureAnalysis{}, err
	}

	job := ""
	if len(failedJobs) > 0 {
		job = strings.TrimSpace(failedJobs[0].Name)
	} else {
		_, _, _, fj := classifyRunJobs(run.Jobs)
		if len(fj) > 0 {
			job = strings.TrimSpace(fj[0].Name)
		}
	}

	step := ""
	if steps := failedStepNames(run.Jobs); len(steps) > 0 {
		step = steps[0]
	}

	testName := extractTestNameFromLogs(rawLogs)
	errorSig := extractErrorSignature(rawLogs)
	if errorSig == "" && strings.TrimSpace(rawLogs) != "" {
		errorSig = truncateRunes(strings.TrimSpace(rawLogs), 200)
	}

	fp := computeFailureFingerprint(repo, run.WorkflowName, job, testName, errorSig)
	return runFailureAnalysis{
		RunID:       runID,
		Workflow:    run.WorkflowName,
		Job:         job,
		Step:        step,
		TestName:    testName,
		ErrorSig:    errorSig,
		Fingerprint: fp,
	}, nil
}

func formatFailureAnalysis(a runFailureAnalysis) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run %d  %s\n", a.RunID, a.Workflow)
	if a.Job != "" {
		fmt.Fprintf(&b, "Job: %s\n", a.Job)
	}
	if a.Step != "" {
		fmt.Fprintf(&b, "Step: %s\n", a.Step)
	}
	if a.TestName != "" {
		fmt.Fprintf(&b, "Test: %s\n", a.TestName)
	}
	if a.ErrorSig != "" {
		fmt.Fprintf(&b, "Error signature: %s\n", a.ErrorSig)
	}
	fmt.Fprintf(&b, "Fingerprint: %s\n", a.Fingerprint)
	b.WriteString("Next: policy_classify_failure; then ci_compare_runs or ci_get_failed_logs.")
	return strings.TrimSpace(b.String())
}

func (s *Server) fingerprintTools() []toolEntry {
	fingerprintTool := mcp.NewTool("ci_failure_fingerprint",
		mcp.WithDescription(
			"Structured failure fingerprint for a workflow run (job, step, test, error signature). "+
				"Aligns with unistar-coworker flaky ledger. Call after ci_analyze_pr_failures; "+
				"Next: policy_classify_failure (lighter than full ci_get_failed_logs)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("run_id", mcp.Required(), mcp.Description("Workflow run ID")),
	)

	compareTool := mcp.NewTool("ci_compare_runs",
		mcp.WithDescription(
			"Compare two workflow runs: same fingerprint, failed jobs, and whether failure looks recurring. "+
				"Use after a rerun to see if the failure changed. Does not return full logs."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("run_id_a", mcp.Required(), mcp.Description("First run ID (often older/base)")),
		mcp.WithNumber("run_id_b", mcp.Required(), mcp.Description("Second run ID (often newer/compare)")),
	)

	externalTool := mcp.NewTool("ci_list_external_checks",
		mcp.WithDescription(
			"List external (non-GitHub Actions) status checks on a PR — Jenkins, Codecov, etc. "+
				"When checks appear here, do not call ci_get_failed_logs; inspect the PR checks tab or the external CI system."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("Pull request number")),
	)

	return []toolEntry{
		{tool: fingerprintTool, handler: s.handleFailureFingerprint},
		{tool: compareTool, handler: s.handleCompareRuns},
		{tool: externalTool, handler: s.handleListExternalChecks},
	}
}

func (s *Server) handleFailureFingerprint(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runIDFloat, err := request.RequireFloat("run_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runID := int64(runIDFloat)

	analysis, err := analyzeRunFailure(ctx, repo, runID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(formatFailureAnalysis(analysis)), nil
}

func (s *Server) handleCompareRuns(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	aFloat, err := request.RequireFloat("run_id_a")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	bFloat, err := request.RequireFloat("run_id_b")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runA := int64(aFloat)
	runB := int64(bFloat)

	analysisA, err := analyzeRunFailure(ctx, repo, runA)
	if err != nil {
		return mcp.NewToolResultError(formatToolError(ErrNotFound,
			fmt.Sprintf("run_id_a %d: %s", runA, err.Error()),
			"Confirm run IDs from ci_analyze_pr_failures or ci_list_runs")), nil
	}
	analysisB, err := analyzeRunFailure(ctx, repo, runB)
	if err != nil {
		return mcp.NewToolResultError(formatToolError(ErrNotFound,
			fmt.Sprintf("run_id_b %d: %s", runB, err.Error()),
			"Confirm run IDs from ci_analyze_pr_failures or ci_list_runs")), nil
	}

	sameFP := analysisA.Fingerprint == analysisB.Fingerprint && analysisA.Fingerprint != ""
	sameWorkflow := analysisA.Workflow == analysisB.Workflow

	var b strings.Builder
	fmt.Fprintf(&b, "Compare runs in %s\n\n", repo)
	fmt.Fprintf(&b, "Run A: %d  %s\n", analysisA.RunID, analysisA.Workflow)
	fmt.Fprintf(&b, "  Fingerprint: %s\n", analysisA.Fingerprint)
	if analysisA.Job != "" {
		fmt.Fprintf(&b, "  Job: %s\n", analysisA.Job)
	}
	fmt.Fprintf(&b, "Run B: %d  %s\n", analysisB.RunID, analysisB.Workflow)
	fmt.Fprintf(&b, "  Fingerprint: %s\n", analysisB.Fingerprint)
	if analysisB.Job != "" {
		fmt.Fprintf(&b, "  Job: %s\n", analysisB.Job)
	}

	b.WriteString("\n")
	if sameFP {
		b.WriteString("Same fingerprint: yes — likely the same failure (possibly flaky).\n")
	} else {
		b.WriteString("Same fingerprint: no — failures differ.\n")
	}
	if sameWorkflow {
		b.WriteString("Same workflow: yes\n")
	} else {
		fmt.Fprintf(&b, "Same workflow: no (%s vs %s)\n", analysisA.Workflow, analysisB.Workflow)
	}

	if analysisA.Job != "" && analysisB.Job != "" {
		switch {
		case analysisA.Job == analysisB.Job:
			fmt.Fprintf(&b, "Failed job: both %s\n", analysisA.Job)
		default:
			fmt.Fprintf(&b, "Failed job: A=%s  B=%s\n", analysisA.Job, analysisB.Job)
		}
	}

	if analysisA.ErrorSig != "" && analysisB.ErrorSig != "" && analysisA.ErrorSig != analysisB.ErrorSig {
		b.WriteString("\nError signature changed — inspect ci_get_failed_logs if unsure.\n")
	} else if sameFP {
		b.WriteString("\nNext: ci_rerun_workflow if flaky; otherwise fix the recurring failure.\n")
	}

	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handleListExternalChecks(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)

	rollup, err := prStatusRollup(ctx, repo, prNum)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var lines []string
	for _, c := range rollup {
		if c.Typename != "StatusContext" {
			continue
		}
		name := checkDisplayName(c)
		if name == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", name, strings.ToLower(checkVerdict(c))))
	}

	if len(lines) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf(
			"No external status checks on PR #%d in %s.\nGitHub Actions checks are not listed here — use ci_analyze_pr_failures.",
			prNum, repo)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d external check(s) on PR #%d in %s:\n", len(lines), prNum, repo)
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n\nThese are not GitHub Actions — do not call ci_get_failed_logs.")
	b.WriteString("\nInspect the PR checks tab or the external CI system for logs and rerun.")
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}
