package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return data
}

func TestFormatExternalCheckSummary_fromFixture(t *testing.T) {
	var pr struct {
		StatusCheck []checkRollup `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(loadFixture(t, "pr_status_external_rollup.json"), &pr); err != nil {
		t.Fatal(err)
	}

	out := formatExternalCheckSummary(pr.StatusCheck)
	if !strings.Contains(out, "ci/jenkins: failure") {
		t.Errorf("missing jenkins failure:\n%s", out)
	}
	if strings.Contains(out, "CheckRun") || strings.Contains(out, "\n  - CI:") {
		t.Errorf("CheckRun should not appear in external summary:\n%s", out)
	}
	if !strings.Contains(out, "codecov/patch: pending") {
		t.Errorf("external summary should list pending StatusContext checks:\n%s", out)
	}
	if !strings.Contains(out, "do not call ci_get_failed_logs") {
		t.Errorf("missing guidance:\n%s", out)
	}
}

func TestPendingCheckSummary_fromFixture(t *testing.T) {
	var pr struct {
		StatusCheck []checkRollup `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(loadFixture(t, "pr_status_external_rollup.json"), &pr); err != nil {
		t.Fatal(err)
	}

	out := pendingCheckSummary(pr.StatusCheck)
	if !strings.Contains(out, "codecov/patch: pending") {
		t.Errorf("missing pending check:\n%s", out)
	}
}

func TestWrapErrorPrefixContract(t *testing.T) {
	r := runResult{
		stderr:   "HTTP 404: Not Found",
		exitCode: 1,
		err:      errExit(1),
	}
	msg := r.wrap("failed to fetch PR").Error()
	if !strings.HasPrefix(msg, "ERROR: NOT_FOUND |") {
		t.Fatalf("expected ERROR: NOT_FOUND prefix, got:\n%s", msg)
	}
	if !strings.Contains(msg, "hint:") {
		t.Fatalf("expected hint in wrapped error:\n%s", msg)
	}
}

// errExit is a minimal stand-in for exec.ExitError in unit tests.
type errExit int

func (e errExit) Error() string { return "exit status" }
