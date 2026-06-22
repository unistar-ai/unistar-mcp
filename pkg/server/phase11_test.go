package server

import (
	"testing"
	"time"
)

func TestAggregateWorkflowStats(t *testing.T) {
	runs := []branchRun{
		{DatabaseID: 1, WorkflowName: "CI", Conclusion: "failure", Status: "completed",
			CreatedAt: "2024-01-01T10:00:00Z", UpdatedAt: "2024-01-01T10:04:00Z"},
		{DatabaseID: 2, WorkflowName: "CI", Conclusion: "success", Status: "completed",
			CreatedAt: "2024-01-01T11:00:00Z", UpdatedAt: "2024-01-01T11:02:00Z"},
		{DatabaseID: 3, WorkflowName: "Lint", Conclusion: "", Status: "in_progress"},
	}
	stats := aggregateWorkflowStats(runs)
	if stats["CI"].runs != 2 || stats["CI"].failures != 1 {
		t.Fatalf("CI stats: %+v", stats["CI"])
	}
	if stats["CI"].durationSamples != 2 {
		t.Fatalf("duration samples = %d", stats["CI"].durationSamples)
	}
	if _, ok := stats["Lint"]; ok {
		t.Fatal("in_progress should be skipped")
	}
}

func TestFormatDurationCompact(t *testing.T) {
	if got := formatDurationCompact(4 * time.Minute); got != "4m0s" {
		t.Fatalf("got %q", got)
	}
}

func TestIsMergeReadyAndBlocked(t *testing.T) {
	greenApproved := pullRequest{
		Number: 1, IsDraft: false, Mergeable: "MERGEABLE", ReviewDecision: "APPROVED",
		StatusCheck: []checkRollup{
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"},
		},
	}
	if !isMergeReady(greenApproved) {
		t.Fatal("expected merge ready")
	}

	conflict := pullRequest{
		Number: 2, IsDraft: false, Mergeable: "CONFLICTING", ReviewDecision: "APPROVED",
		StatusCheck: greenApproved.StatusCheck,
	}
	if !isCIGreen(conflict) || isMergeReady(conflict) {
		t.Fatal("conflict should be green but not ready")
	}
	if mergeQueueBlocker(conflict) != "merge conflicts" {
		t.Fatalf("blocker %q", mergeQueueBlocker(conflict))
	}
}

func TestFormatDraftCIComment(t *testing.T) {
	text := formatDraftCIComment("acme/x", 42, runFailureAnalysis{
		RunID: 99, Workflow: "CI", Job: "test", TestName: "TestFoo", ErrorSig: "panic",
		Fingerprint: "abc",
	}, verdictTest, "named_test_failure")
	if len(text) < 50 {
		t.Fatalf("short draft: %q", text)
	}
}
