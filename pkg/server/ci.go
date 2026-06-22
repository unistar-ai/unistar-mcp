package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// workflowRun mirrors the fields we request from `gh run list --json`.
// Only fields the agent actually needs to act are requested/returned.
type workflowRun struct {
	DatabaseID   int64  `json:"databaseId"`
	WorkflowName string `json:"workflowName"`
	Conclusion   string `json:"conclusion"`
	Status       string `json:"status"`
}

type branchRun struct {
	DatabaseID   int64  `json:"databaseId"`
	WorkflowName string `json:"workflowName"`
	Conclusion   string `json:"conclusion"`
	Status       string `json:"status"`
	HeadBranch   string `json:"headBranch"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

func (s *Server) ciTools() []toolEntry {
	analyzeTool := mcp.NewTool("ci_analyze_pr_failures",
		mcp.WithDescription("List failing GitHub Actions workflow runs for a PR (run IDs for ci_get_run_summary / ci_get_failed_logs). "+
			"First line is CI_KIND (actions_only / external_only / mixed / pending / approval / clean) for routing. "+
			"Separates real failures from action_required (approval gates). "+
			"When no Actions failures exist, reports external/pending checks from the PR rollup."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form, e.g. acme/widget")),
		mcp.WithNumber("pr_number", mcp.Required(), mcp.Description("The pull request number")),
		mcp.WithBoolean("include_external", mcp.Description("Include external CI checks from statusCheckRollup (default true)")),
	)

	logsTool := mcp.NewTool("ci_get_failed_logs",
		mcp.WithDescription(
			"Distilled failed-step logs for a workflow run: synopsis (job/step/test/FP) plus error excerpts. "+
				"Pass job_id from ci_get_run_summary to target one failed job. "+
				"Next: policy_classify_failure; max_lines=80 to page."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("run_id", mcp.Required(), mcp.Description("The workflow run ID (from ci_analyze_pr_failures)")),
		mcp.WithNumber("job_id", mcp.Description("Optional failed job ID from ci_get_run_summary (recommended for matrix workflows)")),
		mcp.WithString("focus", mcp.Description("Error extract focus: last (default), all clusters, or step:<name> from ci_get_run_summary")),
		mcp.WithNumber("offset_lines", mcp.Description("Line offset for pagination (default 0). Use next_offset_lines from a previous page.")),
		mcp.WithNumber("max_lines", mcp.Description("Lines per page (default 0 = single chunk capped at ~6KB). Set e.g. 80 to enable paging.")),
	)

	rerunTool := mcp.NewTool("ci_rerun_workflow",
		mcp.WithDescription("Rerun the failed jobs of a CI workflow run. Use this for flaky failures after inspecting the logs."),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("run_id", mcp.Required(), mcp.Description("The workflow run ID to rerun")),
	)

	listRunsTool := mcp.NewTool("ci_list_runs",
		mcp.WithDescription("List recent GitHub Actions workflow runs on a branch (default branch when branch is omitted). Each line includes duration when completed. Used by main-guard and CI efficiency reports."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithString("branch", mcp.Description("Branch name (default: repository default branch)")),
		mcp.WithNumber("limit", mcp.Description("Max runs to return (default 15, max 50)")),
	)

	runSummaryTool := mcp.NewTool("ci_get_run_summary",
		mcp.WithDescription(
			"Compact workflow run summary: status, conclusion, duration, and failed job names. "+
				"Use before ci_get_failed_logs to decide whether full logs are needed."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("The repository in owner/repo form")),
		mcp.WithNumber("run_id", mcp.Required(), mcp.Description("Workflow run ID")),
	)

	return []toolEntry{
		{tool: analyzeTool, handler: s.handleAnalyzeCI},
		{tool: logsTool, handler: s.handleGetFailedLogs},
		{tool: rerunTool, handler: s.handleRerunCI},
		{tool: listRunsTool, handler: s.handleListRuns},
		{tool: runSummaryTool, handler: s.handleGetRunSummary},
	}
}

// prHeadSHA returns the head commit SHA of the given pull request.
func prHeadSHA(ctx context.Context, repo string, prNum int) (string, error) {
	res := runRetry(ctx, "", "gh", "pr", "view", fmt.Sprintf("%d", prNum),
		"-R", repo, "--json", "headRefOid", "-q", ".headRefOid")
	if res.err != nil {
		return "", res.wrap("failed to resolve PR head commit")
	}
	return strings.TrimSpace(res.stdout), nil
}

func defaultBranch(ctx context.Context, repo string) (string, error) {
	res := runRetry(ctx, "", "gh", "repo", "view", repo,
		"--json", "defaultBranchRef", "-q", ".defaultBranchRef.name")
	if res.err != nil {
		return "", res.wrap("failed to resolve default branch")
	}
	branch := strings.TrimSpace(res.stdout)
	if branch == "" {
		return "", fmt.Errorf("empty default branch for %s", repo)
	}
	return branch, nil
}

func (s *Server) handleAnalyzeCI(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNumFloat, err := request.RequireFloat("pr_number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prNum := int(prNumFloat)
	includeExternal := parseIncludeExternalArg(request.GetArguments()["include_external"], true)

	state, err := s.loadPRFailureState(ctx, repo, prNum, includeExternal)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(formatAnalyzePRFailures(state, includeExternal)), nil
}

func prStatusRollup(ctx context.Context, repo string, prNum int) ([]checkRollup, error) {
	res := runRetry(ctx, "", "gh", "pr", "view", fmt.Sprintf("%d", prNum), "-R", repo,
		"--json", "statusCheckRollup")
	if res.err != nil {
		return nil, res.wrap("failed to fetch PR checks")
	}
	var pr struct {
		StatusCheck []checkRollup `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &pr); err != nil {
		return nil, fmt.Errorf("failed to parse PR checks: %w", err)
	}
	return pr.StatusCheck, nil
}

func pendingCheckSummary(checks []checkRollup) string {
	var lines []string
	for _, c := range checks {
		v := strings.ToUpper(checkVerdict(c))
		if v == "SUCCESS" || v == "NEUTRAL" || v == "SKIPPED" {
			continue
		}
		if v == "FAILURE" || v == "ERROR" || v == "TIMED_OUT" || v == "CANCELLED" {
			continue
		}
		name := checkDisplayName(c)
		if name == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("  - %s: %s", name, strings.ToLower(v)))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Pending checks:\n" + strings.Join(lines, "\n") + "\n"
}

func isCheckFailing(c checkRollup) bool {
	v := strings.ToUpper(checkVerdict(c))
	switch v {
	case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED":
		return true
	default:
		return false
	}
}

func externalChecksFailing(checks []checkRollup) bool {
	for _, c := range checks {
		if c.Typename != "StatusContext" {
			continue
		}
		if isCheckFailing(c) {
			return true
		}
	}
	return false
}

// computeCIKind classifies PR CI for routing (Actions vs external vs mixed).
func computeCIKind(realFailed, waitingApproval int, rollup []checkRollup) string {
	hasActions := realFailed > 0
	hasExternal := externalChecksFailing(rollup)
	hasApproval := waitingApproval > 0
	hasPending := pendingCheckSummary(rollup) != ""

	switch {
	case hasActions && hasExternal:
		return "mixed"
	case hasActions:
		return "actions_only"
	case hasExternal:
		return "external_only"
	case hasApproval:
		return "approval"
	case hasPending:
		return "pending"
	default:
		return "clean"
	}
}

func prependCIKind(body, kind string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return "CI_KIND: " + kind
	}
	return "CI_KIND: " + kind + "\n" + body
}

type runJob struct {
	DatabaseID int64  `json:"databaseId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Steps      []runStep `json:"steps"`
}

type runStep struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type runSummary struct {
	DatabaseID   int64    `json:"databaseId"`
	WorkflowName string   `json:"workflowName"`
	Status       string   `json:"status"`
	Conclusion   string   `json:"conclusion"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
	HeadBranch   string   `json:"headBranch"`
	Jobs         []runJob `json:"jobs"`
}

func (s *Server) handleGetRunSummary(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runIDFloat, err := request.RequireFloat("run_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runID := int64(runIDFloat)

	run, err := loadRunSummary(ctx, repo, runID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	conclusion := strings.ToLower(strings.TrimSpace(run.Conclusion))
	if conclusion == "" {
		conclusion = strings.ToLower(strings.TrimSpace(run.Status))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Run %d  %s\n", run.DatabaseID, run.WorkflowName)
	fmt.Fprintf(&b, "Branch: %s\n", run.HeadBranch)
	fmt.Fprintf(&b, "Status: %s  Conclusion: %s\n", strings.ToLower(run.Status), conclusion)
	if run.CreatedAt != "" && run.UpdatedAt != "" {
		fmt.Fprintf(&b, "Started: %s  Updated: %s\n", run.CreatedAt, run.UpdatedAt)
	}

	success, failed, pending, failedJobs := classifyRunJobs(run.Jobs)
	fmt.Fprintf(&b, "Jobs: %d success / %d failed / %d pending\n", success, failed, pending)
	if len(failedJobs) > 0 {
		b.WriteString("Failed jobs:\n")
		for _, j := range failedJobs {
			fmt.Fprintf(&b, "- %s  (job_id=%d)\n", j.Name, j.DatabaseID)
		}
		if steps := failedStepNames(run.Jobs); len(steps) > 0 {
			b.WriteString("Failed steps:\n")
			for _, s := range steps {
				fmt.Fprintf(&b, "  - %s\n", s)
			}
		}
		if runStillInProgress(run) && failed > 0 {
			b.WriteString(
				"Note: run is still in progress; ci_get_failed_logs can fetch logs for failed jobs that have finished.\n",
			)
		}
	}
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

// failingRunsForPR lists failing GitHub Actions workflow runs for a PR head commit.
func failingRunsForPR(ctx context.Context, repo string, prNum int) (headSHA string, failed []workflowRun, truncated bool, err error) {
	headSHA, err = prHeadSHA(ctx, repo, prNum)
	if err != nil {
		return "", nil, false, err
	}

	res := runRetry(ctx, "", "gh", "run", "list", "-R", repo,
		"--commit", headSHA, "--limit", fmt.Sprintf("%d", ciRunListLimit),
		"--json", "databaseId,workflowName,conclusion,status")
	if res.err != nil {
		return headSHA, nil, false, res.wrap("failed to list workflow runs")
	}

	var runs []workflowRun
	if err := json.Unmarshal([]byte(res.stdout), &runs); err != nil {
		return headSHA, nil, false, fmt.Errorf("failed to parse run list: %w", err)
	}
	truncated = len(runs) == ciRunListLimit

	for _, r := range runs {
		conc := strings.ToLower(strings.TrimSpace(r.Conclusion))
		switch conc {
		case "failure", "timed_out", "startup_failure", "action_required":
			failed = append(failed, r)
			continue
		}
		if conc != "" {
			continue
		}
		st := strings.ToLower(strings.TrimSpace(r.Status))
		if !runStatusInProgress(st) {
			continue
		}
		run, err := loadRunSummary(ctx, repo, r.DatabaseID)
		if err != nil {
			continue
		}
		_, failCount, _, failJobs := classifyRunJobs(run.Jobs)
		if failCount == 0 {
			continue
		}
		r.Conclusion = "failure (run in progress)"
		if len(failJobs) > 0 {
			r.Status = fmt.Sprintf("%s, %d failed job(s)", st, failCount)
		}
		failed = append(failed, r)
	}
	return headSHA, failed, truncated, nil
}

func (s *Server) handleGetFailedLogs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runIDFloat, err := request.RequireFloat("run_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runID := int64(runIDFloat)
	jobID := int64(request.GetFloat("job_id", 0))
	focus := request.GetString("focus", "last")
	offsetLines := int(request.GetFloat("offset_lines", 0))
	maxLines := int(request.GetFloat("max_lines", 0))
	if offsetLines < 0 {
		offsetLines = 0
	}
	if maxLines < 0 {
		maxLines = 0
	}

	run, err := loadRunSummary(ctx, repo, runID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	logText, failedJobs, err := fetchFailedLogs(ctx, repo, runID, jobID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if strings.TrimSpace(logText) == "" {
		if len(failedJobs) > 0 {
			synopsis := formatFailureLogSynopsis(repo, run, runID, failedJobs, "")
			var names []string
			for _, j := range failedJobs {
				state := jobEffectiveConclusion(j)
				if !jobLogsReady(j) {
					state += ", logs pending"
				}
				names = append(names, fmt.Sprintf("%s (job_id=%d, %s)", j.Name, j.DatabaseID, state))
			}
			return mcp.NewToolResultText(fmt.Sprintf(
				"%s\n\nNo log output yet for %d failed job(s): %s",
				synopsis, len(failedJobs), strings.Join(names, "; "))), nil
		}
		synopsis := formatFailureLogSynopsis(repo, run, runID, nil, "")
		return mcp.NewToolResultText(fmt.Sprintf(
			"%s\n\nRun %d has no failed-step logs (still running or cancelled).", synopsis, runID)), nil
	}

	opts := distillOptions{
		focus: focus,
		jobs:  mergeJobsForDistill(run.Jobs, failedJobs),
	}
	body, mode := distillFailedLogText(logText, opts)
	synopsis := formatFailureLogSynopsis(repo, run, runID, failedJobs, body)
	out := formatFailedLogsResponse(runID, synopsis, body, mode, offsetLines, maxLines)
	return mcp.NewToolResultText(out), nil
}

func (s *Server) handleListRuns(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	limit := int(request.GetFloat("limit", 15))
	if limit <= 0 {
		limit = 15
	}
	if limit > 50 {
		limit = 50
	}

	runs, err := listBranchRuns(ctx, repo, branch, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "branch: %s\n%d run(s) for %s:\n", branch, len(runs), repo)
	for _, r := range runs {
		conclusion := runConclusion(r)
		dur := formatRunDuration(r.CreatedAt, r.UpdatedAt, conclusion)
		fmt.Fprintf(&b, "%d  %s  %s  %s\n", r.DatabaseID, r.WorkflowName, conclusion, dur)
	}

	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

// formatRunDuration returns a compact duration for completed runs, or "-" while pending.
func formatRunDuration(created, updated, conclusion string) string {
	if runStatusInProgress(conclusion) || conclusion == "" {
		return "-"
	}
	if created == "" || updated == "" {
		return "-"
	}
	t0, err0 := time.Parse(time.RFC3339, created)
	t1, err1 := time.Parse(time.RFC3339, updated)
	if err0 != nil || err1 != nil {
		return "-"
	}
	d := t1.Sub(t0)
	if d < 0 {
		return "-"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func (s *Server) handleRerunCI(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runIDFloat, err := request.RequireFloat("run_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runID := int64(runIDFloat)

	res := run(ctx, "", "gh", "run", "rerun", fmt.Sprintf("%d", runID), "-R", repo, "--failed")
	if res.err != nil {
		return mcp.NewToolResultError(res.wrap("failed to rerun workflow").Error()), nil
	}

	return mcp.NewToolResultText(formatToolOK(fmt.Sprintf("Reran failed jobs in run %d.", runID))), nil
}

func loadRunSummary(ctx context.Context, repo string, runID int64) (runSummary, error) {
	res := runRetry(ctx, "", "gh", "run", "view", fmt.Sprintf("%d", runID), "-R", repo,
		"--json", "databaseId,workflowName,status,conclusion,createdAt,updatedAt,headBranch,jobs")
	if res.err != nil {
		return runSummary{}, res.wrap("failed to fetch run summary")
	}
	var run runSummary
	if err := json.Unmarshal([]byte(res.stdout), &run); err != nil {
		return runSummary{}, fmt.Errorf("failed to parse run summary: %w", err)
	}
	return run, nil
}

func jobEffectiveConclusion(j runJob) string {
	jc := strings.ToLower(strings.TrimSpace(j.Conclusion))
	if jc == "" {
		jc = strings.ToLower(strings.TrimSpace(j.Status))
	}
	return jc
}

func isFailedJobConclusion(jc string) bool {
	switch jc {
	case "failure", "timed_out", "cancelled", "startup_failure", "action_required":
		return true
	default:
		return false
	}
}

func classifyRunJobs(jobs []runJob) (success, failed, pending int, failedJobs []runJob) {
	for _, j := range jobs {
		switch jobEffectiveConclusion(j) {
		case "success", "skipped", "neutral":
			success++
		case "failure", "timed_out", "cancelled", "startup_failure", "action_required":
			failed++
			failedJobs = append(failedJobs, j)
		default:
			pending++
		}
	}
	return success, failed, pending, failedJobs
}

func failedStepNames(jobs []runJob) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, j := range jobs {
		if !isFailedJobConclusion(jobEffectiveConclusion(j)) {
			continue
		}
		for _, step := range j.Steps {
			conc := strings.ToLower(strings.TrimSpace(step.Conclusion))
			if conc == "" {
				conc = strings.ToLower(strings.TrimSpace(step.Status))
			}
			if conc != "failure" && conc != "timed_out" && conc != "cancelled" {
				continue
			}
			name := strings.TrimSpace(step.Name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}

func runStatusInProgress(status string) bool {
	switch status {
	case "queued", "in_progress", "pending", "requested", "waiting":
		return true
	default:
		return false
	}
}

func runStillInProgress(run runSummary) bool {
	st := strings.ToLower(strings.TrimSpace(run.Status))
	if runStatusInProgress(st) {
		return true
	}
	return strings.ToLower(strings.TrimSpace(run.Conclusion)) == ""
}

// ghRunLogRecoverable reports when a gh log fetch failed in a way we can work
// around: run still in progress, zip missing a job (skipped / stale), or empty
// output. Caller should fall back to per-job fetch or treat as no logs yet.
func ghRunLogRecoverable(res runResult) bool {
	if res.err == nil {
		return strings.TrimSpace(res.stdout) == ""
	}
	if ghRunLogUnavailableYet(res) {
		return true
	}
	low := strings.ToLower(res.combined())
	if strings.Contains(low, "log not found") {
		return true
	}
	return false
}

// ghRunLogUnavailableYet reports when gh refuses run-level logs because the
// workflow is still running (or returned no output yet).
func ghRunLogUnavailableYet(res runResult) bool {
	if res.err == nil {
		return strings.TrimSpace(res.stdout) == ""
	}
	low := strings.ToLower(res.combined())
	return strings.Contains(low, "still in progress") ||
		strings.Contains(low, "log will be available when it is complete")
}

// fetchFailedRunLogs tries run-level --log-failed first, then per failed job when the
// workflow run is still in progress (gh errors or returns empty until the run completes).
func fetchFailedRunLogs(ctx context.Context, repo string, runID int64) (string, []runJob, error) {
	res := runRetry(ctx, "", "gh", "run", "view", fmt.Sprintf("%d", runID),
		"-R", repo, "--log-failed")
	if strings.TrimSpace(res.stdout) != "" {
		return res.stdout, nil, nil
	}
	if res.err != nil && !ghRunLogRecoverable(res) {
		return "", nil, res.wrap("failed to fetch failed logs")
	}

	return fetchFailedJobLogs(ctx, repo, runID)
}

func fetchFailedJobLogs(ctx context.Context, repo string, runID int64) (string, []runJob, error) {
	run, err := loadRunSummary(ctx, repo, runID)
	if err != nil {
		return "", nil, err
	}
	_, _, _, failedJobs := classifyRunJobs(run.Jobs)
	if len(failedJobs) == 0 {
		return "", nil, nil
	}

	var parts []string
	for _, job := range failedJobs {
		raw, fetchErr := fetchFailedJobLogText(ctx, repo, runID, job)
		if fetchErr != nil {
			parts = append(parts, fmt.Sprintf("=== job: %s (job_id=%d) ===\nfailed to fetch logs: %s",
				job.Name, job.DatabaseID, fetchErr.Error()))
			continue
		}
		if strings.TrimSpace(raw) == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("=== job: %s (job_id=%d) ===\n%s",
			job.Name, job.DatabaseID, strings.TrimSpace(raw)))
	}
	return strings.Join(parts, "\n\n"), failedJobs, nil
}

// fetchFailedJobLogText loads logs for one failed job. Prefer gh's failed-step
// filter; the Actions job logs API returns the entire job log (all steps) and
// is only used as a last resort while a run is still in progress.
func fetchFailedJobLogText(ctx context.Context, repo string, runID int64, job runJob) (string, error) {
	if jobConclusionSkipped(job) {
		return "", nil
	}

	ghAttempts := [][]string{
		{"run", "view", "-R", repo, "--job", fmt.Sprintf("%d", job.DatabaseID), "--log-failed"},
		{"run", "view", fmt.Sprintf("%d", runID), "-R", repo, "--job", fmt.Sprintf("%d", job.DatabaseID), "--log-failed"},
		{"run", "view", "-R", repo, "--job", fmt.Sprintf("%d", job.DatabaseID), "--log"},
		{"run", "view", fmt.Sprintf("%d", runID), "-R", repo, "--job", fmt.Sprintf("%d", job.DatabaseID), "--log"},
	}
	for _, args := range ghAttempts {
		jobRes := runRetry(ctx, "", "gh", args...)
		if strings.TrimSpace(jobRes.stdout) != "" {
			return jobRes.stdout, nil
		}
		if jobRes.err != nil && !ghRunLogRecoverable(jobRes) {
			return "", jobRes.wrap(fmt.Sprintf("fetch logs for job %d", job.DatabaseID))
		}
	}

	if !jobLogsReady(job) {
		return "", nil
	}
	res := runRetry(ctx, "", "gh", "api",
		fmt.Sprintf("repos/%s/actions/jobs/%d/logs", repo, job.DatabaseID))
	if strings.TrimSpace(res.stdout) != "" {
		return res.stdout, nil
	}
	if res.err != nil && !ghRunLogRecoverable(res) {
		return "", res.wrap(fmt.Sprintf("fetch logs for job %d", job.DatabaseID))
	}
	return "", nil
}

func jobConclusionSkipped(job runJob) bool {
	return strings.EqualFold(strings.TrimSpace(job.Conclusion), "skipped")
}

func jobLogsReady(job runJob) bool {
	return strings.ToLower(strings.TrimSpace(job.Status)) == "completed"
}

const (
	errBudget      = 6_000 // max bytes of extracted error lines returned
	fallbackTail   = 4_000 // max bytes returned when no error lines are recognized
	errContext     = 4     // lines of context kept around each matched error line
	ciRunListLimit = 100   // max workflow runs fetched per commit
)

// errLineRE matches lines that typically carry the actual failure signal.
var errLineRE = regexp.MustCompile(`(?i)(\berror\b|\bfailed\b|\bfailure\b|\bpanic\b|\bfatal\b|exception|traceback|assert|\bundefined\b|cannot |not found|exit code [1-9]|exit status [1-9]|✗|\bFAIL\b|\[error\])`)

func isNoiseErrorLine(ln string) bool {
	low := strings.ToLower(ln)
	if strings.Contains(ln, "##[warning]") {
		return true
	}
	if strings.Contains(low, "unable to reserve cache") {
		return true
	}
	if strings.Contains(low, "failed to save:") && strings.Contains(low, "cache") {
		return true
	}
	if strings.Contains(low, "npm warn") {
		return true
	}
	if strings.Contains(low, "warning:") && !strings.Contains(ln, "##[error]") {
		return true
	}
	if strings.Contains(low, "retrying") || strings.Contains(low, "attempt 2 of") || strings.Contains(low, "attempt 3 of") {
		return true
	}
	if strings.Contains(low, "downloading") && (strings.Contains(low, "mb/") || strings.Contains(low, "mb ")) {
		return true
	}
	if strings.Contains(low, "uploaded artifact") {
		return true
	}
	return false
}

// extractErrors returns distilled error lines using last-cluster focus (default).
func extractErrors(clean string) (string, int) {
	return extractErrorsWithFocus(clean, "last", "")
}

func extractErrorsWithFocus(clean string, focusMode, stepName string) (string, int) {
	lines := strings.Split(clean, "\n")
	if body, n := extractMarkedLines(lines, "##[error]"); n > 0 {
		return applyErrorFocus(body, focusMode, stepName), n
	}
	body, matches := extractRegexLines(lines, errLineRE, isNoiseErrorLine)
	if matches == 0 {
		return "", 0
	}
	return applyErrorFocus(body, focusMode, stepName), matches
}

func applyErrorFocus(body, focusMode, stepName string) string {
	focusMode = strings.ToLower(strings.TrimSpace(focusMode))
	switch focusMode {
	case "", "last":
		return preferLastErrorCluster(body)
	case "all":
		return body
	case "step":
		if stepName == "" {
			return preferLastErrorCluster(body)
		}
		return filterErrorClustersByStep(body, stepName)
	default:
		if strings.HasPrefix(focusMode, "step:") {
			return filterErrorClustersByStep(body, strings.TrimSpace(focusMode[5:]))
		}
		return preferLastErrorCluster(body)
	}
}

func filterErrorClustersByStep(body, stepName string) string {
	stepName = strings.TrimSpace(stepName)
	if stepName == "" {
		return preferLastErrorCluster(body)
	}
	lowStep := strings.ToLower(stepName)
	parts := strings.Split(body, "…\n")
	var matched []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(strings.ToLower(p), lowStep) {
			matched = append(matched, p)
		}
	}
	if len(matched) == 0 {
		return preferLastErrorCluster(body)
	}
	if len(matched) == 1 {
		return matched[0]
	}
	return strings.Join(matched, "\n…\n")
}

func extractMarkedLines(lines []string, marker string) (string, int) {
	keep := make([]bool, len(lines))
	matches := 0
	for i, ln := range lines {
		if !strings.Contains(ln, marker) {
			continue
		}
		matches++
		lo, hi := i-errContext, i+errContext
		if lo < 0 {
			lo = 0
		}
		if hi >= len(lines) {
			hi = len(lines) - 1
		}
		for j := lo; j <= hi; j++ {
			keep[j] = true
		}
		expandErrorBlock(lines, keep, i)
	}
	if matches == 0 {
		return "", 0
	}
	return joinKeptLines(lines, keep), matches
}

// expandErrorBlock keeps lines after a ##[error] marker until a blank line (stack/diff).
func expandErrorBlock(lines []string, keep []bool, start int) {
	limit := start + errBlockMaxLines
	if limit >= len(lines) {
		limit = len(lines) - 1
	}
	for j := start + 1; j <= limit; j++ {
		if strings.TrimSpace(lines[j]) == "" && j > start+2 {
			break
		}
		keep[j] = true
	}
}

func extractRegexLines(lines []string, re *regexp.Regexp, skip func(string) bool) (string, int) {
	keep := make([]bool, len(lines))
	matches := 0
	for i, ln := range lines {
		if skip != nil && skip(ln) {
			continue
		}
		if !re.MatchString(ln) {
			continue
		}
		matches++
		lo, hi := i-errContext, i+errContext
		if lo < 0 {
			lo = 0
		}
		if hi >= len(lines) {
			hi = len(lines) - 1
		}
		for j := lo; j <= hi; j++ {
			keep[j] = true
		}
	}
	if matches == 0 {
		return "", 0
	}
	return joinKeptLines(lines, keep), matches
}

func joinKeptLines(lines []string, keep []bool) string {
	var b strings.Builder
	gapOpen := false
	last := ""
	for i, ln := range lines {
		if !keep[i] {
			if gapOpen {
				b.WriteString("…\n")
				gapOpen = false
			}
			continue
		}
		gapOpen = true
		if isNoiseErrorLine(ln) && !strings.Contains(ln, "##[error]") {
			continue
		}
		if ln == last {
			continue // collapse consecutive duplicates
		}
		b.WriteString(ln)
		b.WriteByte('\n')
		last = ln
	}
	return strings.TrimSpace(b.String())
}

// preferLastErrorCluster keeps the final error region when extractErrors matched
// multiple disjoint areas (e.g. setup noise vs the real failing step).
func preferLastErrorCluster(body string) string {
	parts := strings.Split(body, "…\n")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return body
	}
	return last
}

var (
	ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	// Raw Actions job log API lines: "<RFC3339 ts> message" (no job/step columns).
	rawLogPrefixRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T[\d:.]+Z\s*`)
)

// cleanGHLog strips ANSI and gh prefixes; preserves job > step when available.
func cleanGHLog(s string) string {
	return cleanGHLogAnchored(s, nil)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// tail returns at most n trailing bytes of s, prefixed with a notice when truncated.
// paginateLines returns a slice of lines [offset, offset+maxLines) from text.
func paginateLines(text string, offsetLines, maxLines int) (page string, totalLines, nextOffset int, hasMore bool) {
	if maxLines <= 0 {
		return text, 0, 0, false
	}
	lines := strings.Split(text, "\n")
	totalLines = len(lines)
	if offsetLines >= totalLines {
		return "", totalLines, totalLines, false
	}
	end := offsetLines + maxLines
	if end > totalLines {
		end = totalLines
	}
	page = strings.Join(lines[offsetLines:end], "\n")
	nextOffset = end
	hasMore = end < totalLines
	return page, totalLines, nextOffset, hasMore
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…[truncated]…\n" + s[len(s)-n:]
}
