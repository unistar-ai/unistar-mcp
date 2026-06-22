package server

import (
	"strings"
	"testing"
)

func TestFormatGHLogLine_preservesJobStep(t *testing.T) {
	raw := "build\tRun tests\t2026-06-17T10:28:57.3196697Z ##[error]assertion failed"
	got := formatGHLogLine(raw)
	want := "build > Run tests: ##[error]assertion failed"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatGHLogLine_unknownStep(t *testing.T) {
	raw := "build\tUNKNOWN STEP\t2026-06-17T10:28:57.3196697Z ##[error]boom"
	got := formatGHLogLine(raw)
	if !strings.Contains(got, "build:") || !strings.Contains(got, "boom") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "UNKNOWN STEP") {
		t.Fatalf("should drop UNKNOWN STEP: %q", got)
	}
}

func TestSplitMarkedJobSections(t *testing.T) {
	raw := `=== job: lint (job_id=11) ===
##[error]lint fail

=== job: test (job_id=22) ===
FAIL: TestFoo`
	chunks := splitMarkedJobSections(raw)
	if len(chunks) != 2 || chunks[0].jobID != 11 || chunks[1].jobName != "test" {
		t.Fatalf("chunks=%+v", chunks)
	}
}

func TestDistillFailedLogText_perJob(t *testing.T) {
	raw := `=== job: lint (job_id=11) ===
setup noise failed download
##[error]lint error

=== job: test (job_id=22) ===
random failed in setup
FAIL: TestWidget/Broken
exit code 1`
	body, mode := distillFailedLogText(raw, distillOptions{focus: "last"})
	if mode != "error lines" {
		t.Fatalf("mode=%q", mode)
	}
	if !strings.Contains(body, "[lint (job_id=11)]") || !strings.Contains(body, "[test (job_id=22)]") {
		t.Fatalf("missing job sections: %q", body)
	}
	if !strings.Contains(body, "lint error") || !strings.Contains(body, "TestWidget/Broken") {
		t.Fatalf("missing errors: %q", body)
	}
}

func TestFormatFailureLogSynopsis(t *testing.T) {
	run := runSummary{
		WorkflowName: "CI",
		HeadBranch:   "main",
		Jobs: []runJob{
			{
				Name: "build", DatabaseID: 99, Status: "completed", Conclusion: "failure",
				Steps: []runStep{
					{Name: "Run tests", Status: "completed", Conclusion: "failure"},
				},
			},
		},
	}
	jobs := []runJob{run.Jobs[0]}
	body := "--- FAIL: TestHandler\n##[error]expected nil"
	synopsis := formatFailureLogSynopsis("acme/widget", run, 123, jobs, body)
	for _, want := range []string{
		"Run 123  CI  branch:main",
		"Failed job: build (job_id=99)",
		"Failed steps: Run tests",
		"Test:",
		"FP:",
		"---",
	} {
		if !strings.Contains(synopsis, want) {
			t.Fatalf("synopsis missing %q:\n%s", want, synopsis)
		}
	}
}

func TestExpandErrorBlock(t *testing.T) {
	lines := []string{
		"ok",
		"##[error]panic: boom",
		"goroutine 1",
		"main.go:10",
		"",
		"later",
	}
	keep := make([]bool, len(lines))
	keep[1] = true
	expandErrorBlock(lines, keep, 1)
	if !keep[2] || !keep[3] {
		t.Fatal("expected stack lines kept")
	}
	if keep[5] {
		t.Fatal("should stop at blank line")
	}
}

func TestFormatGHLogLine_anchorUnknownStep(t *testing.T) {
	got := formatGHLogLineAnchored(
		"build\tUNKNOWN STEP\t2026-06-17T10:28:57.3196697Z ##[error]boom",
		[]string{"Run tests"},
	)
	if !strings.Contains(got, "build > Run tests:") {
		t.Fatalf("got %q", got)
	}
}

func TestApplyErrorFocus_all(t *testing.T) {
	body := "cluster1\n…\ncluster2"
	if got := applyErrorFocus(body, "all", ""); got != body {
		t.Fatalf("got %q", got)
	}
}

func TestApplyErrorFocus_step(t *testing.T) {
	body := "lint > Run: setup failed\n…\ntest > Run tests: FAIL TestFoo"
	got := applyErrorFocus(body, "step", "Run tests")
	if !strings.Contains(got, "TestFoo") || strings.Contains(got, "setup failed") {
		t.Fatalf("got %q", got)
	}
}

func TestParseLogFocus(t *testing.T) {
	mode, step := parseLogFocus("step:Run tests")
	if mode != "step" || step != "Run tests" {
		t.Fatalf("mode=%q step=%q", mode, step)
	}
}

func TestExtractTestNameFromLogs_jest(t *testing.T) {
	logs := "✕ should return 200 (5 ms)\nExpected: true\nReceived: false"
	if name := extractTestNameFromLogs(logs); !strings.Contains(name, "should return 200") {
		t.Fatalf("got %q", name)
	}
}

func TestGoldenDistill_goTestFixture(t *testing.T) {
	raw := string(loadFixture(t, "go_test_failure.log"))
	jobs := []runJob{{
		Name: "build", DatabaseID: 11, Status: "completed", Conclusion: "failure",
		Steps: []runStep{{Name: "Run tests", Status: "completed", Conclusion: "failure"}},
	}}
	body, mode := distillFailedLogText(raw, distillOptions{focus: "last", jobs: jobs})
	if mode != "error lines" {
		t.Fatalf("mode=%q", mode)
	}
	for _, want := range []string{"TestHandler", "handler_test.go", "exit code 1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in distilled body:\n%s", want, body)
		}
	}
	if strings.Contains(body, "failed download") {
		t.Fatalf("noise should be filtered: %q", body)
	}
}

func TestGoldenDistill_pytestFixture(t *testing.T) {
	raw := string(loadFixture(t, "pytest_failure.log"))
	jobs := []runJob{{
		Name: "test", DatabaseID: 22, Status: "completed", Conclusion: "failure",
		Steps: []runStep{{Name: "Pytest", Status: "completed", Conclusion: "failure"}},
	}}
	body, _ := distillFailedLogText(raw, distillOptions{focus: "last", jobs: jobs})
	for _, want := range []string{"test_create_user", "401 Unauthorized", "exit code 1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in distilled body:\n%s", want, body)
		}
	}
}

func TestFormatFailedLogsResponse_pagingKeepsSynopsisOnFirstPage(t *testing.T) {
	body := strings.Join([]string{"line1", "line2", "line3", "line4"}, "\n")
	out := formatFailedLogsResponse(1, "SYNOPSIS", body, "error lines", 0, 2)
	if !strings.HasPrefix(out, "SYNOPSIS") {
		t.Fatalf("first page missing synopsis: %q", out)
	}
	if !strings.Contains(out, "PAGE: offset=0") {
		t.Fatalf("missing page header: %q", out)
	}
	out2 := formatFailedLogsResponse(1, "SYNOPSIS", body, "error lines", 2, 2)
	if strings.Contains(out2, "SYNOPSIS") {
		t.Fatalf("second page should not repeat synopsis: %q", out2)
	}
}
