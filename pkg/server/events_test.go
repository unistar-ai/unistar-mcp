package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompressPullRequestWebhook(t *testing.T) {
	body := []byte(`{
		"action": "opened",
		"repository": {"full_name": "acme/widget"},
		"pull_request": {"number": 42, "title": "Fix CI", "user": {"login": "alice"}}
	}`)
	rec, _ := compressGitHubWebhook(body, "pull_request", "del-1")
	if rec.Kind != "pull_request.opened" {
		t.Fatalf("kind %q", rec.Kind)
	}
	if rec.Repo != "acme/widget" {
		t.Fatalf("repo %q", rec.Repo)
	}
	if !strings.Contains(rec.Summary, "#42") || !strings.Contains(rec.Summary, "alice") {
		t.Fatalf("summary %q", rec.Summary)
	}
}

func TestCompressWorkflowRunWebhook(t *testing.T) {
	body := []byte(`{
		"action": "completed",
		"repository": {"full_name": "acme/widget"},
		"workflow_run": {"id": 999, "name": "CI", "conclusion": "failure", "head_branch": "main"}
	}`)
	rec, _ := compressGitHubWebhook(body, "workflow_run", "")
	if rec.Kind != "workflow_run.completed" {
		t.Fatalf("kind %q", rec.Kind)
	}
	if !strings.Contains(rec.Summary, "CI") || !strings.Contains(rec.Summary, "failure") {
		t.Fatalf("summary %q", rec.Summary)
	}
	if !strings.Contains(rec.Summary, "run_id=999") {
		t.Fatalf("missing run_id in summary %q", rec.Summary)
	}
}

func TestIsFailedWorkflowConclusion(t *testing.T) {
	if !isFailedWorkflowConclusion("failure") {
		t.Fatal("failure should enrich")
	}
	if isFailedWorkflowConclusion("success") {
		t.Fatal("success should not enrich")
	}
}

func TestEventStoreFingerprintByDelivery(t *testing.T) {
	es := newEventStore(5)
	es.add(eventRecord{Delivery: "d1", Summary: "x"})
	if !es.setFingerprintByDelivery("d1", "abc123") {
		t.Fatal("expected patch")
	}
	all := es.list("", "", 1)
	if len(all) != 1 || all[0].Fingerprint != "abc123" {
		t.Fatalf("got %+v", all)
	}
	if es.setFingerprintByDelivery("missing", "x") {
		t.Fatal("expected false for unknown delivery")
	}
}

func TestEventStoreRingBuffer(t *testing.T) {
	t.Setenv("UNISTAR_MCP_EVENT_FILE", "off")
	es := newEventStore(3)
	for i := 1; i <= 5; i++ {
		es.add(eventRecord{Kind: fmtKind(i), Repo: "a/b"})
	}
	all := es.list("", "", 10)
	if len(all) != 3 {
		t.Fatalf("want 3 got %d", len(all))
	}
	if all[0].Kind != "kind.5" || all[2].Kind != "kind.3" {
		t.Fatalf("order %+v", all)
	}
}

func fmtKind(i int) string {
	return fmt.Sprintf("kind.%d", i)
}

func TestEventListRecentFilters(t *testing.T) {
	t.Setenv("UNISTAR_MCP_EVENT_FILE", "off")
	s := New(Options{})
	s.events.add(eventRecord{Kind: "pull_request.opened", Repo: "acme/a", Summary: "a"})
	s.events.add(eventRecord{Kind: "workflow_run.completed", Repo: "acme/b", Summary: "b"})
	s.events.add(eventRecord{Kind: "pull_request.closed", Repo: "acme/a", Summary: "c"})

	res, err := s.handleEventListRecent(context.Background(), callReq(map[string]any{
		"repo":  "acme/a",
		"kind":  "pull_request",
		"limit": 5,
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := resultText(t, res)
	if strings.Count(out, "pull_request") != 2 {
		t.Fatalf("expected 2 PR events:\n%s", out)
	}
	if strings.Contains(out, "workflow_run") {
		t.Fatalf("workflow leaked:\n%s", out)
	}
}

func TestGitHubWebhookHandlerStoresEvent(t *testing.T) {
	t.Setenv("UNISTAR_MCP_EVENT_FILE", "off")
	s := New(Options{})
	body := []byte(`{"action":"opened","repository":{"full_name":"acme/x"},"pull_request":{"number":1,"title":"T","user":{"login":"u"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/github", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", "abc")
	rec := httptest.NewRecorder()

	s.handleGitHubWebhook(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if s.events.len() != 1 {
		t.Fatalf("stored %d", s.events.len())
	}
}

func TestGitHubWebhookSignature(t *testing.T) {
	t.Setenv("UNISTAR_MCP_EVENT_FILE", "off")
	secret := "shhh"
	body := []byte(`{"zen":"test"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	s := New(Options{})
	req := httptest.NewRequest(http.MethodPost, "/hooks/github", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-Hub-Signature-256", sig)
	t.Setenv("GITHUB_WEBHOOK_SECRET", secret)
	rec := httptest.NewRecorder()

	s.handleGitHubWebhook(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/hooks/github", strings.NewReader(string(body)))
	req2.Header.Set("X-GitHub-Event", "ping")
	req2.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	rec2 := httptest.NewRecorder()
	s.handleGitHubWebhook(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", rec2.Code)
	}
}

func TestEventListRecentEmptyHint(t *testing.T) {
	t.Setenv("UNISTAR_MCP_EVENT_FILE", "off")
	s := New(Options{})
	res, err := s.handleEventListRecent(context.Background(), callReq(nil))
	if err != nil {
		t.Fatal(err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "No recent webhook events") {
		t.Fatalf("out %q", out)
	}
	if !strings.Contains(out, "/hooks/github") {
		t.Fatalf("missing hint: %q", out)
	}
}
