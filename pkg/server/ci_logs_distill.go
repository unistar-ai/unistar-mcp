package server

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var ghLogLineRE = regexp.MustCompile(`^([^\t]*)\t([^\t]*)\t\d{4}-\d{2}-\d{2}T[\d:.]+Z (.*)$`)

const (
	errBlockMaxLines = 12
)

type jobLogChunk struct {
	jobName string
	jobID   int64
	text    string
}

// fetchFailedLogs loads raw failed-step logs for a whole run or one job.
func fetchFailedLogs(ctx context.Context, repo string, runID, jobID int64) (string, []runJob, error) {
	if jobID <= 0 {
		return fetchFailedRunLogs(ctx, repo, runID)
	}

	run, err := loadRunSummary(ctx, repo, runID)
	if err != nil {
		return "", nil, err
	}
	job, ok := findRunJob(run.Jobs, jobID)
	if !ok {
		return "", nil, fmt.Errorf("job_id %d not found in run %d", jobID, runID)
	}
	if !isFailedJobConclusion(jobEffectiveConclusion(job)) {
		return "", nil, fmt.Errorf("job_id %d (%s) is not failed", jobID, job.Name)
	}

	raw, err := fetchFailedJobLogText(ctx, repo, runID, job)
	if err != nil {
		return "", nil, err
	}
	return raw, []runJob{job}, nil
}

func failedStepNamesForJobs(jobs []runJob) []string {
	return failedStepNames(jobs)
}

func formatFailureLogSynopsis(repo string, run runSummary, runID int64, targetJobs []runJob, distilled string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run %d  %s  branch:%s\n", runID, run.WorkflowName, run.HeadBranch)

	if len(targetJobs) == 0 {
		_, _, _, targetJobs = classifyRunJobs(run.Jobs)
	}
	for _, j := range targetJobs {
		fmt.Fprintf(&b, "Failed job: %s (job_id=%d)\n", j.Name, j.DatabaseID)
	}
	if steps := failedStepNamesForJobs(targetJobs); len(steps) > 0 {
		fmt.Fprintf(&b, "Failed steps: %s\n", strings.Join(steps, ", "))
	}

	testName := extractTestNameFromLogs(distilled)
	if testName != "" {
		fmt.Fprintf(&b, "Test: %s\n", testName)
	}
	sig := extractErrorSignature(distilled)
	if sig != "" {
		fmt.Fprintf(&b, "Sig: %s\n", sig)
	}

	jobName := ""
	if len(targetJobs) > 0 {
		jobName = targetJobs[0].Name
	}
	fp := computeFailureFingerprint(repo, run.WorkflowName, jobName, testName, sig)
	fmt.Fprintf(&b, "FP: %s\n", fp)
	b.WriteString("Next: policy_classify_failure; ci_get_job_logs for deeper single-job logs\n---")
	return strings.TrimSpace(b.String())
}

type distillOptions struct {
	focus string   // last (default), all, or step:<name>
	jobs  []runJob // failed jobs for step anchoring
}

func parseLogFocus(raw string) (mode string, stepName string) {
	raw = strings.TrimSpace(raw)
	low := strings.ToLower(raw)
	if raw == "" || low == "last" {
		return "last", ""
	}
	if low == "all" {
		return "all", ""
	}
	if strings.HasPrefix(low, "step:") {
		return "step", strings.TrimSpace(raw[5:])
	}
	return "last", ""
}

func failedStepNamesForJob(job runJob) []string {
	var out []string
	for _, step := range job.Steps {
		conc := strings.ToLower(strings.TrimSpace(step.Conclusion))
		if conc == "" {
			conc = strings.ToLower(strings.TrimSpace(step.Status))
		}
		if conc != "failure" && conc != "timed_out" && conc != "cancelled" {
			continue
		}
		name := strings.TrimSpace(step.Name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func anchorStepsForChunk(chunk jobLogChunk, jobs []runJob) []string {
	for _, j := range jobs {
		if chunk.jobID > 0 && j.DatabaseID == chunk.jobID {
			return failedStepNamesForJob(j)
		}
		if chunk.jobName != "" && j.Name == chunk.jobName {
			return failedStepNamesForJob(j)
		}
	}
	return nil
}

func distillFailedLogText(rawLog string, opts distillOptions) (body string, mode string) {
	focusMode, stepName := parseLogFocus(opts.focus)
	if focusMode == "step" && stepName == "" {
		focusMode = "last"
	}

	chunks := splitLogsIntoJobChunks(rawLog)
	if len(chunks) <= 1 {
		anchor := anchorStepsForChunk(jobLogChunk{text: rawLog}, opts.jobs)
		clean := cleanGHLogAnchored(rawLog, anchor)
		return distillSingleLog(clean, focusMode, stepName)
	}

	var parts []string
	overallMode := "error lines"
	for _, chunk := range chunks {
		anchor := anchorStepsForChunk(chunk, opts.jobs)
		clean := cleanGHLogAnchored(chunk.text, anchor)
		part, partMode := distillSingleLog(clean, focusMode, stepName)
		if strings.TrimSpace(part) == "" {
			continue
		}
		label := chunk.jobName
		if chunk.jobID > 0 {
			label = fmt.Sprintf("%s (job_id=%d)", chunk.jobName, chunk.jobID)
		}
		parts = append(parts, fmt.Sprintf("[%s]\n%s", label, part))
		if partMode == "log tail" {
			overallMode = "log tail"
		}
	}
	if len(parts) == 0 {
		return "", "log tail"
	}
	return strings.Join(parts, "\n\n"), overallMode
}

func distillSingleLog(clean string, focusMode, stepName string) (body string, mode string) {
	if extracted, n := extractErrorsWithFocus(clean, focusMode, stepName); n > 0 {
		return extracted, "error lines"
	}
	if strings.TrimSpace(clean) == "" {
		return "", "log tail"
	}
	return tail(clean, fallbackTail), "log tail"
}

func splitLogsIntoJobChunks(raw string) []jobLogChunk {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	if strings.Contains(raw, "=== job:") {
		return splitMarkedJobSections(raw)
	}
	return splitGHPrefixJobChunks(raw)
}

var jobSectionHeaderRE = regexp.MustCompile(`(?m)^=== job: (.+?) \(job_id=(\d+)\) ===\n`)

func splitMarkedJobSections(raw string) []jobLogChunk {
	matches := jobSectionHeaderRE.FindAllStringSubmatchIndex(raw, -1)
	if len(matches) == 0 {
		return splitGHPrefixJobChunks(raw)
	}

	var chunks []jobLogChunk
	for i, loc := range matches {
		name := raw[loc[2]:loc[3]]
		var id int64
		fmt.Sscanf(raw[loc[4]:loc[5]], "%d", &id)
		start := loc[1]
		end := len(raw)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		text := strings.TrimSpace(raw[start:end])
		if text == "" {
			continue
		}
		chunks = append(chunks, jobLogChunk{jobName: name, jobID: id, text: text})
	}
	return chunks
}

func splitGHPrefixJobChunks(raw string) []jobLogChunk {
	byJob := map[string]*strings.Builder{}
	var order []string
	for _, line := range strings.Split(raw, "\n") {
		if m := ghLogLineRE.FindStringSubmatch(line); len(m) == 4 {
			job := strings.TrimSpace(m[1])
			if job == "" {
				continue
			}
			if _, ok := byJob[job]; !ok {
				order = append(order, job)
				byJob[job] = &strings.Builder{}
			}
			byJob[job].WriteString(line)
			byJob[job].WriteByte('\n')
			continue
		}
		if len(order) > 0 {
			last := order[len(order)-1]
			byJob[last].WriteString(line)
			byJob[last].WriteByte('\n')
		}
	}
	if len(order) <= 1 {
		return nil
	}
	var chunks []jobLogChunk
	for _, name := range order {
		text := strings.TrimSpace(byJob[name].String())
		if text != "" {
			chunks = append(chunks, jobLogChunk{jobName: name, text: text})
		}
	}
	return chunks
}

func formatGHLogLine(line string) string {
	return formatGHLogLineAnchored(line, nil)
}

func formatGHLogLineAnchored(line string, anchorSteps []string) string {
	line = strings.TrimRight(line, "\r")
	line = ansiRE.ReplaceAllString(line, "")
	if m := ghLogLineRE.FindStringSubmatch(line); len(m) == 4 {
		job := strings.TrimSpace(m[1])
		step := strings.TrimSpace(m[2])
		msg := strings.TrimSpace(m[3])
		if step == "" || strings.EqualFold(step, "UNKNOWN STEP") {
			step = resolveAnchorStep(msg, anchorSteps)
			if step == "" {
				if msg == "" {
					return job + ":"
				}
				return job + ": " + msg
			}
		}
		if msg == "" {
			return job + " > " + step + ":"
		}
		return job + " > " + step + ": " + msg
	}
	line = rawLogPrefixRE.ReplaceAllString(line, "")
	return strings.TrimRight(line, " ")
}

func resolveAnchorStep(msg string, anchorSteps []string) string {
	if len(anchorSteps) == 0 {
		return ""
	}
	if len(anchorSteps) == 1 {
		return anchorSteps[0]
	}
	lowMsg := strings.ToLower(msg)
	for _, s := range anchorSteps {
		if strings.Contains(lowMsg, strings.ToLower(s)) {
			return s
		}
	}
	return anchorSteps[0]
}

func cleanGHLogAnchored(s string, anchorSteps []string) string {
	s = strings.TrimPrefix(s, "\uFEFF")

	var b strings.Builder
	blank := 0
	for _, line := range strings.Split(s, "\n") {
		line = formatGHLogLineAnchored(line, anchorSteps)
		if line == "" {
			if blank > 0 {
				continue
			}
			blank++
		} else {
			blank = 0
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func formatFailedLogsResponse(runID int64, synopsis, body, mode string, offsetLines, maxLines int) string {
	if maxLines > 0 {
		pageBody, total, next, hasMore := paginateLines(body, offsetLines, maxLines)
		if total == 0 && strings.TrimSpace(body) == "" {
			return fmt.Sprintf("%s\n\nRun %d — empty %s (offset %d).", synopsis, runID, mode, offsetLines)
		}
		start := offsetLines + 1
		end := next
		if end > total {
			end = total
		}
		pageNum := offsetLines/maxLines + 1
		totalPages := (total + maxLines - 1) / maxLines
		if totalPages == 0 {
			totalPages = 1
		}
		header := fmt.Sprintf(
			"PAGE: offset=%d total_lines=%d has_more=%t next_offset_lines=%d page=%d/%d",
			offsetLines, total, hasMore, next, pageNum, totalPages)
		prefix := synopsis + "\n\n"
		if offsetLines > 0 {
			prefix = ""
		}
		return fmt.Sprintf("%s%s\nRun %d — %s lines %d-%d of %d\n\n%s",
			prefix, header, runID, mode, start, end, total, pageBody)
	}

	if mode == "error lines" {
		lineCount := strings.Count(body, "\n") + 1
		if lineCount == 0 && body != "" {
			lineCount = 1
		}
		hint := ""
		if maxLines == 0 {
			hint = "\n(hint: pass max_lines=80 to page through long logs)"
		}
		return fmt.Sprintf("%s\n\nRun %d — %d distilled line(s):%s\n\n%s",
			synopsis, runID, lineCount, hint, tail(body, errBudget))
	}

	hint := ""
	if maxLines == 0 {
		hint = "\n(hint: pass max_lines=80 to page through long logs)"
	}
	return fmt.Sprintf("%s\n\nRun %d — no recognizable error lines, log tail:%s\n\n%s",
		synopsis, runID, hint, tail(body, fallbackTail))
}
