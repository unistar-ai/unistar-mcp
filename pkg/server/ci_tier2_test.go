package server

import (
	"strings"
	"testing"
	"time"
)

func TestFilterPRsBeforeRun(t *testing.T) {
	runAt := time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC)
	prs := []prMergedRow{
		{Number: 1, Title: "after", MergedAt: "2024-06-10T13:00:00Z"},
		{Number: 2, Title: "before", MergedAt: "2024-06-09T10:00:00Z"},
		{Number: 3, Title: "also before", MergedAt: "2024-06-08T10:00:00Z"},
	}
	out := filterPRsBeforeRun(prs, runAt, 10)
	if len(out) != 2 || out[0].Number != 2 || out[1].Number != 3 {
		t.Fatalf("filterPRsBeforeRun = %+v", out)
	}
}

func TestFormatDistilledJobLogs_paging(t *testing.T) {
	raw := strings.Join([]string{
		"##[error]first failure",
		"##[error]second failure",
		"##[error]third failure",
	}, "\n")
	text := formatDistilledJobLogs(1, 2, "test", raw, 0, 2)
	if !strings.Contains(text, "PAGE: offset=0") {
		t.Fatalf("missing page header: %q", text)
	}
	if !strings.Contains(text, "job_id=2") {
		t.Fatalf("missing job id: %q", text)
	}
}

func TestIsoDatePrefix(t *testing.T) {
	if got := isoDatePrefix("2024-01-15T10:00:00Z"); got != "2024-01-15" {
		t.Fatalf("isoDatePrefix = %q", got)
	}
	if got := isoDatePrefix("2024-01-15"); got != "2024-01-15" {
		t.Fatalf("isoDatePrefix date = %q", got)
	}
}

func TestFormatWorkflowList(t *testing.T) {
	text := formatWorkflowList("acme/widget", []workflowRow{
		{ID: 2, Name: "Release", State: "active"},
		{ID: 1, Name: "CI", State: "active"},
	})
	if !strings.Contains(text, "2 workflow(s)") {
		t.Fatalf("text %q", text)
	}
	if !strings.Contains(text, "1  CI") {
		t.Fatalf("sorted CI first: %q", text)
	}
}
