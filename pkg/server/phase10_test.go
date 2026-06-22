package server

import (
	"testing"
)

func TestBuildBranchHealthFailures(t *testing.T) {
	runs := []branchRun{
		{DatabaseID: 3, WorkflowName: "CI", Conclusion: "failure", CreatedAt: "2026-01-01T10:00:00Z", UpdatedAt: "2026-01-01T10:05:00Z"},
		{DatabaseID: 2, WorkflowName: "CI", Conclusion: "failure", CreatedAt: "2026-01-01T09:00:00Z", UpdatedAt: "2026-01-01T09:04:00Z"},
		{DatabaseID: 1, WorkflowName: "CI", Conclusion: "success", CreatedAt: "2026-01-01T08:00:00Z", UpdatedAt: "2026-01-01T08:03:00Z"},
	}
	text := buildBranchHealthText("acme/widget", "main", runs)
	if !containsAll(text, "Failure streak", "2", "run_id=3", "failures: 2/3") {
		t.Fatalf("text %q", text)
	}
}

func TestFormatDiffRiskScanLockfile(t *testing.T) {
	text := formatDiffRiskScan("acme/x", 1, []prFileChange{
		{Filename: "go.sum", Additions: 2, Deletions: 1, Status: "modified"},
		{Filename: ".github/workflows/ci.yml", Additions: 5, Deletions: 0, Status: "modified"},
	})
	if !containsAll(text, "lockfile", "workflow_changed", "go.sum") {
		t.Fatalf("text %q", text)
	}
}

func TestIsBackportWorkspace(t *testing.T) {
	if !isBackportWorkspace("/tmp/unistar-backport-12345") {
		t.Fatal("expected backport workspace")
	}
	if isBackportWorkspace("/etc/passwd") {
		t.Fatal("unexpected match")
	}
}

func TestIsLockfile(t *testing.T) {
	if !isLockfile("package-lock.json") {
		t.Fatal("expected lockfile")
	}
}
