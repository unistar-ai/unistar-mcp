package server

import (
	"strings"
	"testing"
	"time"
)

func TestFormatFlakyFingerprintHint(t *testing.T) {
	t.Setenv("UNISTAR_MCP_EVENT_FILE", "off")
	s := New(Options{EventCapacity: 10})
	fp := "test-fp-empty-abc"
	hint := s.formatFlakyFingerprintHint("acme/widget", fp)
	if !strings.Contains(hint, "new fingerprint") {
		t.Fatalf("empty ledger: %q", hint)
	}

	s.events.add(eventRecord{
		At:          time.Now(),
		Kind:        "workflow_run.completed",
		Repo:        "acme/widget",
		Fingerprint: fp,
	})
	hint = s.formatFlakyFingerprintHint("acme/widget", fp)
	if !strings.Contains(hint, "once before") {
		t.Fatalf("single hit: %q", hint)
	}

	s.events.add(eventRecord{
		At:          time.Now(),
		Kind:        "workflow_run.completed",
		Repo:        "acme/widget",
		Fingerprint: fp,
	})
	s.events.add(eventRecord{
		At:          time.Now(),
		Kind:        "workflow_run.completed",
		Repo:        "acme/widget",
		Fingerprint: fp,
	})
	hint = s.formatFlakyFingerprintHint("acme/widget", fp)
	if !strings.Contains(hint, "seen 3 times") {
		t.Fatalf("recurring: %q", hint)
	}
}

func TestCountByFingerprint_repoFilter(t *testing.T) {
	t.Setenv("UNISTAR_MCP_EVENT_FILE", "off")
	es := newEventStore(10)
	es.add(eventRecord{Repo: "acme/a", Fingerprint: "fp1"})
	es.add(eventRecord{Repo: "acme/b", Fingerprint: "fp1"})
	if got := es.countByFingerprint("acme/a", "fp1"); got != 1 {
		t.Fatalf("repo filter = %d", got)
	}
	if got := es.countByFingerprint("", "fp1"); got != 2 {
		t.Fatalf("all repos = %d", got)
	}
}
