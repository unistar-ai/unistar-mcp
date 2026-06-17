package server

import (
	"fmt"
	"strings"
	"testing"
)

func TestPaginateLines(t *testing.T) {
	text := "a\nb\nc\nd\ne"
	page, total, next, hasMore := paginateLines(text, 0, 2)
	if total != 5 || page != "a\nb" || next != 2 || !hasMore {
		t.Fatalf("page0: total=%d next=%d hasMore=%v page=%q", total, next, hasMore, page)
	}
	page, total, next, hasMore = paginateLines(text, 2, 2)
	if total != 5 || page != "c\nd" || next != 4 || !hasMore {
		t.Fatalf("page1: total=%d next=%d hasMore=%v page=%q", total, next, hasMore, page)
	}
	page, total, next, hasMore = paginateLines(text, 4, 2)
	if total != 5 || page != "e" || next != 5 || hasMore {
		t.Fatalf("page2: total=%d next=%d hasMore=%v page=%q", total, next, hasMore, page)
	}
	_, total, next, hasMore = paginateLines(text, 5, 2)
	if total != 5 || next != 5 || hasMore {
		t.Fatalf("past end: total=%d next=%d hasMore=%v", total, next, hasMore)
	}
}

func TestClassifyRunJobs_inProgressWithFailedJob(t *testing.T) {
	jobs := []runJob{
		{Name: "lint", Status: "completed", Conclusion: "success", DatabaseID: 1},
		{Name: "test", Status: "completed", Conclusion: "failure", DatabaseID: 2},
		{Name: "deploy", Status: "in_progress", Conclusion: "", DatabaseID: 3},
	}
	success, failed, pending, failedJobs := classifyRunJobs(jobs)
	if success != 1 || failed != 1 || pending != 1 {
		t.Fatalf("counts: success=%d failed=%d pending=%d", success, failed, pending)
	}
	if len(failedJobs) != 1 || failedJobs[0].Name != "test" {
		t.Fatalf("failedJobs=%+v", failedJobs)
	}
}

func TestRunStatusInProgress(t *testing.T) {
	if !runStatusInProgress("in_progress") {
		t.Fatal("expected in_progress")
	}
	if runStatusInProgress("completed") {
		t.Fatal("completed is not in progress")
	}
}

func TestIsFailedJobConclusion(t *testing.T) {
	if !isFailedJobConclusion("failure") {
		t.Fatal("failure should count")
	}
	if isFailedJobConclusion("success") {
		t.Fatal("success should not count")
	}
}

func TestJobLogsReady(t *testing.T) {
	if !jobLogsReady(runJob{Status: "completed", Conclusion: "failure"}) {
		t.Fatal("completed failed job should be log-ready")
	}
	if jobLogsReady(runJob{Status: "in_progress", Conclusion: ""}) {
		t.Fatal("in-progress job is not log-ready")
	}
}

func TestExtractErrors_prefersGitHubErrorAnnotations(t *testing.T) {
	raw := strings.Join([]string{
		"setup ok",
		"##[warning]Failed to save: Unable to reserve cache with key foo",
		"##[error]pkg/server/security.go:15:1: File is not properly formatted (gofmt)",
		"##[error]issues found",
		"post cleanup",
	}, "\n")
	body, n := extractErrors(cleanGHLog(raw))
	if n != 2 {
		t.Fatalf("matches=%d body=%q", n, body)
	}
	if strings.Contains(body, "Unable to reserve cache") {
		t.Fatalf("warning noise leaked into errors: %q", body)
	}
	if !strings.Contains(body, "gofmt") {
		t.Fatalf("expected real error line: %q", body)
	}
}

func TestExtractErrors_prefersLastClusterForGenericMatches(t *testing.T) {
	raw := strings.Join([]string{
		"line 1",
		"random failed download in setup",
		"…",
		"middle",
		"FAIL: TestWidget/Broken",
		"exit code 1",
	}, "\n")
	body, n := extractErrors(raw)
	if n == 0 {
		t.Fatal("expected matches")
	}
	if !strings.Contains(body, "TestWidget/Broken") {
		t.Fatalf("expected last cluster kept: %q", body)
	}
	if strings.Contains(body, "random failed download") {
		t.Fatalf("expected earlier cluster dropped: %q", body)
	}
}

func TestCleanGHLog_stripsRawAPITimestampPrefix(t *testing.T) {
	raw := "2026-06-16T09:46:40.5754043Z ##[error]boom\n"
	clean := cleanGHLog(raw)
	if strings.Contains(clean, "2026-06-16") {
		t.Fatalf("timestamp prefix not stripped: %q", clean)
	}
	if clean != "##[error]boom" {
		t.Fatalf("unexpected clean output: %q", clean)
	}
}

func TestGhRunLogUnavailableYet(t *testing.T) {
	inProgress := runResult{
		err:    fmt.Errorf("exit 1"),
		stderr: "run 27672089136 is still in progress; log will be available when it is complete",
	}
	if !ghRunLogUnavailableYet(inProgress) {
		t.Fatal("expected in-progress gh error to be recoverable")
	}
	empty := runResult{stdout: "  \n"}
	if !ghRunLogUnavailableYet(empty) {
		t.Fatal("expected empty stdout to trigger fallback")
	}
	ok := runResult{stdout: "error line\n"}
	if ghRunLogUnavailableYet(ok) {
		t.Fatal("non-empty stdout should not trigger fallback")
	}
	auth := runResult{
		err:    fmt.Errorf("exit 1"),
		stderr: "HTTP 401: Bad credentials",
	}
	if ghRunLogUnavailableYet(auth) {
		t.Fatal("auth errors must not be treated as in-progress")
	}
}
