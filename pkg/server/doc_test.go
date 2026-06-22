package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const expectedBusinessToolCount = 52

// expectedBusinessTools is the canonical set registered in registerTools().
var expectedBusinessTools = []string{
	"ci_analyze_pr_failures",
	"ci_get_failed_logs",
	"ci_get_failure_digest",
	"ci_get_run_summary",
	"ci_list_runs",
	"ci_rerun_workflow",
	"ci_failure_fingerprint",
	"ci_compare_runs",
	"ci_correlate_prs",
	"ci_get_job_logs",
	"ci_list_workflows",
	"ci_list_external_checks",
	"ci_get_check_url",
	"ci_branch_health",
	"ci_workflow_stats",
	"policy_classify_failure",
	"pr_list_open",
	"pr_get_status",
	"pr_list_changed_files",
	"pr_list_stale",
	"pr_list_merged",
	"pr_get_diff",
	"pr_post_comment",
	"pr_get_overview",
	"pr_get_ci_snapshot",
	"pr_get_merge_blockers",
	"pr_list_waiting_review",
	"pr_list_merge_ready",
	"pr_list_merge_blocked",
	"pr_draft_ci_comment",
	"pr_list_large",
	"pr_get_review_routing",
	"pr_get_status_batch",
	"pr_get_overview_batch",
	"pr_get_review_state",
	"pr_diff_risk_scan",
	"repo_get_info",
	"pr_create_backport",
	"backport_get_conflict_files",
	"backport_suggest_resolution",
	"pr_list_backport_candidates",
	"pr_is_docs_only",
	"issue_list_open",
	"issue_get",
	"issue_add_label",
	"issue_search",
	"alert_list_open",
	"alert_summarize_open",
	"notify_post_slack",
	"event_list_recent",
	"release_list_tags",
	"release_notes_draft",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// Tests run with cwd = module root or pkg/server depending on invocation.
	if _, err := os.Stat("README.md"); err == nil {
		return "."
	}
	if _, err := os.Stat(filepath.Join("..", "..", "README.md")); err == nil {
		return filepath.Join("..", "..")
	}
	t.Fatal("could not locate repo root (README.md)")
	return ""
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func TestRegisteredBusinessToolCount(t *testing.T) {
	s := New(Options{})
	names := s.toolNames()
	if len(names) != expectedBusinessToolCount {
		t.Fatalf("registered %d tools, want %d: %v", len(names), expectedBusinessToolCount, names)
	}
}

func TestRegisteredToolsMatchCanonicalList(t *testing.T) {
	s := New(Options{})
	got := map[string]bool{}
	for _, n := range s.toolNames() {
		got[n] = true
	}
	for _, want := range expectedBusinessTools {
		if !got[want] {
			t.Errorf("registry missing tool %q", want)
		}
	}
	if len(got) != len(expectedBusinessTools) {
		t.Errorf("unexpected extra tools in registry: %v", s.toolNames())
	}
}

func TestDocsMentionAllRegisteredTools(t *testing.T) {
	s := New(Options{})
	readme := readRepoFile(t, "README.md")
	toolsDoc := readRepoFile(t, "docs/TOOLS.md")

	for _, name := range s.toolNames() {
		if !strings.Contains(readme, "`"+name+"`") && !strings.Contains(readme, name) {
			t.Errorf("README.md missing tool %q", name)
		}
		if !strings.Contains(toolsDoc, "`"+name+"`") {
			t.Errorf("docs/TOOLS.md missing tool %q", name)
		}
	}
}
