package server

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestToolCacheHit(t *testing.T) {
	t.Setenv("UNISTAR_MCP_CACHE_TTL", "30")
	c := newToolCache()
	if c == nil {
		t.Fatal("expected cache")
	}
	c.set("k", "v")
	if v, ok := c.get("k"); !ok || v != "v" {
		t.Fatalf("get = %q ok=%v", v, ok)
	}
}

func TestToolCacheDisabled(t *testing.T) {
	t.Setenv("UNISTAR_MCP_CACHE_TTL", "off")
	if c := newToolCache(); c != nil {
		t.Fatal("expected nil cache when disabled")
	}
}

func TestCachedStringOnServer(t *testing.T) {
	t.Setenv("UNISTAR_MCP_CACHE_TTL", "60")
	t.Setenv("UNISTAR_MCP_EVENT_FILE", "off")
	s := New(Options{})
	calls := 0
	v1, err := s.cachedString("demo", "acme/x", "a", func() (string, error) {
		calls++
		return "ok", nil
	})
	if err != nil || v1 != "ok" || calls != 1 {
		t.Fatalf("first call v1=%q err=%v calls=%d", v1, err, calls)
	}
	v2, err := s.cachedString("demo", "acme/x", "a", func() (string, error) {
		calls++
		return "other", nil
	})
	if err != nil || v2 != "ok" || calls != 1 {
		t.Fatalf("cached call v2=%q err=%v calls=%d", v2, err, calls)
	}
}

func TestEventStoreFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	t.Setenv("UNISTAR_MCP_EVENT_FILE", path)

	es := newEventStore(3)
	es.add(eventRecord{
		At:      time.Now().UTC(),
		Kind:    "workflow_run.completed",
		Repo:    "acme/x",
		Summary: "CI failure",
		Delivery: "d1",
	})

	es2 := newEventStore(3)
	if es2.len() != 1 {
		t.Fatalf("reload len=%d", es2.len())
	}
	if !es2.setFingerprintByDelivery("d1", "abc") {
		t.Fatal("patch fingerprint")
	}

	es3 := newEventStore(3)
	all := es3.list("", "", 1)
	if len(all) != 1 || all[0].Fingerprint != "abc" {
		t.Fatalf("fp reload %+v", all)
	}
}

func TestResolveEventFileOff(t *testing.T) {
	t.Setenv("UNISTAR_MCP_EVENT_FILE", "off")
	if got := resolveEventFilePath(); got != "" {
		t.Fatalf("want empty got %q", got)
	}
}

func TestFormatPROverviewBatchLine(t *testing.T) {
	line := formatPROverviewBatchLine(pullRequestOverviewBatch{
		pullRequest: pullRequest{
			Number: 7,
			Title:  "Batch me",
			Author: prAuthor{Login: "bob"},
			StatusCheck: []checkRollup{
				{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"},
			},
			ReviewDecision: "REVIEW_REQUIRED",
		},
		ChangedFiles: 3,
		Additions:    10,
		Deletions:    2,
	})
	for _, part := range []string{"#7", "Batch me", "@bob", "files:3", "+10/-2"} {
		if !strings.Contains(line, part) {
			t.Fatalf("line %q missing %q", line, part)
		}
	}
}
