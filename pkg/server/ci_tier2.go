package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

type runCorrelateMeta struct {
	DatabaseID   int64  `json:"databaseId"`
	WorkflowName string `json:"workflowName"`
	HeadBranch   string `json:"headBranch"`
	HeadSHA      string `json:"headSha"`
	CreatedAt    string `json:"createdAt"`
	Conclusion   string `json:"conclusion"`
	Status       string `json:"status"`
}

func (s *Server) ciTier2Tools() []toolEntry {
	jobLogsTool := mcp.NewTool("ci_get_job_logs",
		mcp.WithDescription(
			"Distilled logs for one workflow job (job_id from ci_get_run_summary). "+
				"Use when ci_get_failed_logs is too large. Next: ci_failure_fingerprint or policy_classify_failure."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("run_id", mcp.Required(), mcp.Description("Workflow run ID")),
		mcp.WithNumber("job_id", mcp.Required(), mcp.Description("Job ID from ci_get_run_summary")),
		mcp.WithNumber("offset_lines", mcp.Description("Line offset for pagination (default 0)")),
		mcp.WithNumber("max_lines", mcp.Description("Lines per page (default 0 = single chunk ~6KB). Set e.g. 80 to page.")),
	)

	correlateTool := mcp.NewTool("ci_correlate_prs",
		mcp.WithDescription(
			"List recently merged PRs on the run branch before a failing CI run (regression-link). "+
				"Next: pr_get_overview on suspect PRs or ci_compare_runs with last green run."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("run_id", mcp.Required(), mcp.Description("Failing workflow run ID")),
		mcp.WithNumber("window_days", mcp.Description("Look back N days before the run (default 7)")),
		mcp.WithNumber("limit", mcp.Description("Max PRs to return (default 10, max 30)")),
	)

	listWorkflowsTool := mcp.NewTool("ci_list_workflows",
		mcp.WithDescription(
			"List GitHub Actions workflow names and IDs for a repository. "+
				"Use before ci_list_runs when workflow names are unknown. Next: ci_list_runs or ci_branch_health."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("limit", mcp.Description("Max workflows to return (default 30, max 100)")),
	)

	return []toolEntry{
		{tool: jobLogsTool, handler: s.handleGetJobLogs},
		{tool: correlateTool, handler: s.handleCorrelatePRs},
		{tool: listWorkflowsTool, handler: s.handleListWorkflows},
	}
}

type workflowRow struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

func (s *Server) handleListWorkflows(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := int(request.GetFloat("limit", 30))
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}

	workflows, err := listRepoWorkflows(ctx, repo, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	text := formatWorkflowList(repo, workflows)
	return mcp.NewToolResultText(text), nil
}

func listRepoWorkflows(ctx context.Context, repo string, limit int) ([]workflowRow, error) {
	res := runRetry(ctx, "", "gh", "workflow", "list", "-R", repo,
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "id,name,state")
	if res.err != nil {
		return nil, res.wrap("failed to list workflows")
	}
	var rows []workflowRow
	if err := json.Unmarshal([]byte(res.stdout), &rows); err != nil {
		return nil, fmt.Errorf("failed to parse workflow list: %w", err)
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return rows, nil
}

func formatWorkflowList(repo string, workflows []workflowRow) string {
	if len(workflows) == 0 {
		return fmt.Sprintf("No workflows found for %s.", repo)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d workflow(s) for %s:\n", len(workflows), repo)
	for _, wf := range workflows {
		state := strings.ToLower(strings.TrimSpace(wf.State))
		if state == "" {
			state = "unknown"
		}
		fmt.Fprintf(&b, "%d  %s  %s\n", wf.ID, wf.Name, state)
	}
	b.WriteString("Next: ci_list_runs or ci_branch_health on the default branch.")
	return strings.TrimSpace(b.String())
}

func (s *Server) handleGetJobLogs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runIDFloat, err := request.RequireFloat("run_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	jobIDFloat, err := request.RequireFloat("job_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runID := int64(runIDFloat)
	jobID := int64(jobIDFloat)
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
	job, ok := findRunJob(run.Jobs, jobID)
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("job_id %d not found in run %d", jobID, runID)), nil
	}

	raw, fetchErr := fetchJobLogText(ctx, repo, runID, job, true)
	if fetchErr != nil {
		return mcp.NewToolResultError(fetchErr.Error()), nil
	}
	if strings.TrimSpace(raw) == "" {
		state := jobEffectiveConclusion(job)
		if !jobLogsReady(job) {
			state += ", logs pending"
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Job %s (job_id=%d) in run %d has no log output yet (%s).",
			job.Name, jobID, runID, state)), nil
	}

	text := formatDistilledJobLogs(runID, jobID, job.Name, raw, offsetLines, maxLines)
	return mcp.NewToolResultText(text), nil
}

func findRunJob(jobs []runJob, jobID int64) (runJob, bool) {
	for _, j := range jobs {
		if j.DatabaseID == jobID {
			return j, true
		}
	}
	return runJob{}, false
}

func fetchJobLogText(ctx context.Context, repo string, runID int64, job runJob, preferFailed bool) (string, error) {
	if preferFailed {
		raw, err := fetchFailedJobLogText(ctx, repo, runID, job)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(raw) != "" {
			return raw, nil
		}
	}
	jobRes := runRetry(ctx, "", "gh", "run", "view", fmt.Sprintf("%d", runID),
		"-R", repo, "--job", fmt.Sprintf("%d", job.DatabaseID), "--log")
	if strings.TrimSpace(jobRes.stdout) != "" {
		return jobRes.stdout, nil
	}
	if jobRes.err != nil && !ghRunLogRecoverable(jobRes) {
		return "", jobRes.wrap(fmt.Sprintf("fetch logs for job %d", job.DatabaseID))
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

func formatDistilledJobLogs(runID, jobID int64, jobName, logText string, offsetLines, maxLines int) string {
	clean := cleanGHLog(logText)
	var body string
	var mode string
	if extracted, n := extractErrors(clean); n > 0 {
		body = extracted
		mode = "error lines"
	} else {
		body = clean
		mode = "log tail"
	}

	if maxLines > 0 {
		page, total, next, hasMore := paginateLines(body, offsetLines, maxLines)
		if total == 0 {
			return fmt.Sprintf("Run %d job %s (job_id=%d) — empty %s (offset %d).",
				runID, jobName, jobID, mode, offsetLines)
		}
		start := offsetLines + 1
		end := next
		if end > total {
			end = total
		}
		pageNum := offsetLines/maxLines + 1
		totalPages := (total + maxLines - 1) / maxLines
		header := fmt.Sprintf(
			"PAGE: offset=%d total_lines=%d has_more=%t next_offset_lines=%d page=%d/%d",
			offsetLines, total, hasMore, next, pageNum, totalPages)
		return fmt.Sprintf("%s\nRun %d job %s (job_id=%d) — %s lines %d-%d of %d\n\n%s",
			header, runID, jobName, jobID, mode, start, end, total, page)
	}

	if mode == "error lines" {
		return fmt.Sprintf("Run %d job %s (job_id=%d) — %d error line(s):\n\n%s",
			runID, jobName, jobID, strings.Count(body, "\n")+1, tail(body, errBudget))
	}
	return fmt.Sprintf("Run %d job %s (job_id=%d) — no recognizable error lines, showing tail:\n\n%s",
		runID, jobName, jobID, tail(body, fallbackTail))
}

func (s *Server) handleCorrelatePRs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runIDFloat, err := request.RequireFloat("run_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	runID := int64(runIDFloat)
	windowDays := int(request.GetFloat("window_days", 7))
	if windowDays <= 0 {
		windowDays = 7
	}
	if windowDays > 30 {
		windowDays = 30
	}
	limit := int(request.GetFloat("limit", 10))
	if limit <= 0 {
		limit = 10
	}
	if limit > 30 {
		limit = 30
	}

	meta, err := loadRunCorrelateMeta(ctx, repo, runID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	runAt, err := time.Parse(time.RFC3339, meta.CreatedAt)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse run createdAt %q: %v", meta.CreatedAt, err)), nil
	}
	sinceDate := runAt.AddDate(0, 0, -windowDays).Format("2006-01-02")
	branch := strings.TrimSpace(meta.HeadBranch)
	if branch == "" {
		branch, err = defaultBranch(ctx, repo)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	}

	prs, err := mergedPRsOnBranch(ctx, repo, branch, sinceDate, limit*3)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	suspects := filterPRsBeforeRun(prs, runAt, limit)

	conclusion := strings.ToLower(strings.TrimSpace(meta.Conclusion))
	if conclusion == "" {
		conclusion = strings.ToLower(strings.TrimSpace(meta.Status))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Run %d (%s) on %s @%s — %s\n",
		meta.DatabaseID, meta.WorkflowName, branch, short(meta.HeadSHA), conclusion)
	fmt.Fprintf(&b, "Merged PRs on %s in %d days before run (%s):\n",
		branch, windowDays, sinceDate)
	if len(suspects) == 0 {
		b.WriteString("(none in window)\n")
	} else {
		for _, pr := range suspects {
			merged := pr.MergedAt
			if len(merged) >= 10 {
				merged = merged[:10]
			}
			fmt.Fprintf(&b, "#%d  %s  @%s  merged:%s\n",
				pr.Number, pr.Title, pr.Author.Login, merged)
		}
	}
	b.WriteString("Next: pr_get_overview on top rows or ci_compare_runs with last green run.")
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func loadRunCorrelateMeta(ctx context.Context, repo string, runID int64) (runCorrelateMeta, error) {
	res := runRetry(ctx, "", "gh", "run", "view", fmt.Sprintf("%d", runID), "-R", repo,
		"--json", "databaseId,workflowName,headBranch,headSha,createdAt,conclusion,status")
	if res.err != nil {
		return runCorrelateMeta{}, res.wrap("failed to fetch run metadata")
	}
	var meta runCorrelateMeta
	if err := json.Unmarshal([]byte(res.stdout), &meta); err != nil {
		return runCorrelateMeta{}, fmt.Errorf("failed to parse run metadata: %w", err)
	}
	return meta, nil
}

func mergedPRsOnBranch(ctx context.Context, repo, branch, sinceDate string, fetchLimit int) ([]prMergedRow, error) {
	if fetchLimit <= 0 {
		fetchLimit = 30
	}
	search := fmt.Sprintf("merged:>=%s base:%s", sinceDate, branch)
	args := []string{"pr", "list", "-R", repo, "--state", "merged",
		"--limit", fmt.Sprintf("%d", fetchLimit),
		"--json", "number,title,author,mergedAt",
		"--search", search}
	res := runRetry(ctx, "", "gh", args...)
	if res.err != nil {
		return nil, res.wrap("failed to list merged PRs")
	}
	var prs []prMergedRow
	if err := json.Unmarshal([]byte(res.stdout), &prs); err != nil {
		return nil, fmt.Errorf("failed to parse merged PR list: %w", err)
	}
	sort.Slice(prs, func(i, j int) bool {
		return prs[i].MergedAt > prs[j].MergedAt
	})
	return prs, nil
}

func filterPRsBeforeRun(prs []prMergedRow, runAt time.Time, limit int) []prMergedRow {
	var out []prMergedRow
	for _, pr := range prs {
		mergedAt, err := time.Parse(time.RFC3339, pr.MergedAt)
		if err != nil {
			continue
		}
		if !mergedAt.Before(runAt) {
			continue
		}
		out = append(out, pr)
		if len(out) >= limit {
			break
		}
	}
	return out
}
