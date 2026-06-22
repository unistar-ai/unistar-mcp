package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sirupsen/logrus"
)

const (
	defaultEventCapacity   = 200
	defaultWebhookPath     = "/hooks/github"
	maxWebhookBodyBytes    = 256 << 10
	defaultEventListLimit  = 20
	maxEventListLimit      = 100
)

// eventRecord is a compact webhook-derived event kept in memory.
type eventRecord struct {
	At          time.Time
	Kind        string // e.g. pull_request.opened
	Repo        string // owner/repo
	Summary     string
	Delivery    string
	Fingerprint string // optional; filled async for failed workflow_run events
}

type eventStore struct {
	mu       sync.RWMutex
	capacity int
	events   []eventRecord
	filePath string
}

func newEventStore(capacity int) *eventStore {
	if capacity <= 0 {
		capacity = defaultEventCapacity
	}
	es := &eventStore{
		capacity: capacity,
		filePath: resolveEventFilePath(),
	}
	es.loadFromFile()
	return es
}

func (es *eventStore) add(rec eventRecord) {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.events = append(es.events, rec)
	if len(es.events) > es.capacity {
		es.events = es.events[len(es.events)-es.capacity:]
	}
	es.persistLocked()
}

func (es *eventStore) list(repo, kindPrefix string, limit int) []eventRecord {
	es.mu.RLock()
	defer es.mu.RUnlock()
	if limit <= 0 {
		limit = defaultEventListLimit
	}
	repo = strings.TrimSpace(repo)
	kindPrefix = strings.TrimSpace(kindPrefix)
	out := make([]eventRecord, 0, limit)
	for i := len(es.events) - 1; i >= 0 && len(out) < limit; i-- {
		ev := es.events[i]
		if repo != "" && !strings.EqualFold(ev.Repo, repo) {
			continue
		}
		if kindPrefix != "" && !strings.HasPrefix(ev.Kind, kindPrefix) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

func (es *eventStore) len() int {
	es.mu.RLock()
	defer es.mu.RUnlock()
	return len(es.events)
}

func (es *eventStore) setFingerprintByDelivery(delivery, fingerprint string) bool {
	delivery = strings.TrimSpace(delivery)
	fingerprint = strings.TrimSpace(fingerprint)
	if delivery == "" || fingerprint == "" {
		return false
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	for i := len(es.events) - 1; i >= 0; i-- {
		if es.events[i].Delivery != delivery {
			continue
		}
		es.events[i].Fingerprint = fingerprint
		es.persistLocked()
		return true
	}
	return false
}

func (s *Server) webhookPath() string {
	if p := strings.TrimSpace(s.opts.WebhookPath); p != "" {
		return p
	}
	return defaultWebhookPath
}

func (s *Server) eventTools() []toolEntry {
	tool := mcp.NewTool("event_list_recent",
		mcp.WithDescription(
			"List recent GitHub webhook events ingested by this MCP server (newest first). "+
				"Requires HTTP mode with POST "+defaultWebhookPath+". "+
				"Failed workflow_run events may include async fp: fingerprint. "+
				"On pull_request events, chain to pr_get_overview or pr_get_status."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Description("Filter to owner/repo")),
		mcp.WithString("kind", mcp.Description("Filter by event kind prefix, e.g. pull_request or workflow_run")),
		mcp.WithNumber("limit", mcp.Description("Max events (default 20, max 100)")),
	)

	return []toolEntry{
		{tool: tool, handler: s.handleEventListRecent},
	}
}

func (s *Server) handleEventListRecent(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo := strings.TrimSpace(request.GetString("repo", ""))
	kind := strings.TrimSpace(request.GetString("kind", ""))
	limit := int(request.GetFloat("limit", defaultEventListLimit))
	if limit <= 0 {
		limit = defaultEventListLimit
	}
	if limit > maxEventListLimit {
		limit = maxEventListLimit
	}

	events := s.events.list(repo, kind, limit)
	var b strings.Builder
	if len(events) == 0 {
		b.WriteString(fmt.Sprintf("No recent webhook events (%d stored total).\n", s.events.len()))
		b.WriteString("hint: run `unistar-mcp http`, point GitHub webhooks to ")
		b.WriteString(s.webhookPath())
		b.WriteString(", set GITHUB_WEBHOOK_SECRET when configured on GitHub")
		if path := s.events.filePathHint(); path != "" {
			b.WriteString("\nshared event file: ")
			b.WriteString(path)
			b.WriteString(" (stdio + HTTP processes)")
		}
		return mcp.NewToolResultText(b.String()), nil
	}

	fmt.Fprintf(&b, "%d recent event(s) (newest first, %d stored total):\n", len(events), s.events.len())
	for _, ev := range events {
		line := fmt.Sprintf("%s  %s  %s  %s",
			ev.At.UTC().Format(time.RFC3339), ev.Kind, ev.Repo, ev.Summary)
		if ev.Delivery != "" {
			line += fmt.Sprintf("  delivery:%s", ev.Delivery)
		}
		if ev.Fingerprint != "" {
			line += fmt.Sprintf("  fp:%s", ev.Fingerprint)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
}

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	secret := strings.TrimSpace(os.Getenv("GITHUB_WEBHOOK_SECRET"))
	if secret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if !verifyGitHubWebhookSignature(secret, sig, body) {
			logrus.Warn("github webhook rejected: invalid signature")
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	eventType := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	delivery := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	rec, payload := compressGitHubWebhook(body, eventType, delivery)
	s.events.add(rec)
	s.maybeEnrichWorkflowFailureFingerprint(rec, payload)

	logrus.Debugf("github webhook %s %s %s", rec.Kind, rec.Repo, rec.Summary)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func verifyGitHubWebhookSignature(secret, header string, body []byte) bool {
	if !strings.HasPrefix(header, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	got, err := hex.DecodeString(header[7:])
	if err != nil {
		return false
	}
	return hmac.Equal(expected, got)
}

type webhookRepo struct {
	FullName string `json:"full_name"`
}

type webhookUser struct {
	Login string `json:"login"`
}

type webhookPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	User   webhookUser `json:"user"`
}

type webhookIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	User   webhookUser `json:"user"`
}

type webhookWorkflowRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	HeadBranch string `json:"head_branch"`
	HTMLURL    string `json:"html_url"`
}

type webhookCheckRun struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	HeadBranch string `json:"head_branch"`
}

type webhookPayload struct {
	Action      string              `json:"action"`
	Repository  *webhookRepo        `json:"repository"`
	PullRequest *webhookPR          `json:"pull_request"`
	Issue       *webhookIssue       `json:"issue"`
	WorkflowRun *webhookWorkflowRun   `json:"workflow_run"`
	CheckRun    *webhookCheckRun    `json:"check_run"`
	Ref         string              `json:"ref"`
	Commits     []json.RawMessage   `json:"commits"`
	Pusher      *webhookUser        `json:"pusher"`
	Sender      *webhookUser        `json:"sender"`
}

func compressGitHubWebhook(body []byte, eventType, delivery string) (eventRecord, *webhookPayload) {
	var p *webhookPayload
	if eventType != "ping" {
		var parsed webhookPayload
		if err := json.Unmarshal(body, &parsed); err == nil {
			p = &parsed
		}
	}
	return compressGitHubWebhookPayload(p, eventType, delivery), p
}

func compressGitHubWebhookPayload(p *webhookPayload, eventType, delivery string) eventRecord {
	rec := eventRecord{
		At:       time.Now().UTC(),
		Kind:     strings.TrimSpace(eventType),
		Delivery: delivery,
		Summary:  "webhook received",
	}

	if eventType == "ping" {
		rec.Summary = "ping ok"
		return rec
	}

	if p == nil {
		rec.Summary = "unparsed payload"
		return rec
	}

	if p.Repository != nil {
		rec.Repo = strings.TrimSpace(p.Repository.FullName)
	}

	action := strings.TrimSpace(p.Action)
	if action != "" && eventType != "" {
		rec.Kind = eventType + "." + action
	}

	switch eventType {
	case "pull_request":
		if p.PullRequest != nil {
			who := p.PullRequest.User.Login
			if who == "" && p.Sender != nil {
				who = p.Sender.Login
			}
			rec.Summary = fmt.Sprintf("#%d %q by %s", p.PullRequest.Number,
				clipForLog(p.PullRequest.Title, 80), who)
		}
	case "issues":
		if p.Issue != nil {
			who := p.Issue.User.Login
			rec.Summary = fmt.Sprintf("#%d %q by %s", p.Issue.Number,
				clipForLog(p.Issue.Title, 80), who)
		}
	case "workflow_run":
		if p.WorkflowRun != nil {
			wf := p.WorkflowRun
			rec.Summary = fmt.Sprintf("%s %s branch:%s run_id=%d",
				wf.Name, strings.TrimSpace(wf.Conclusion), wf.HeadBranch, wf.ID)
		}
	case "check_run", "check_suite":
		if p.CheckRun != nil {
			cr := p.CheckRun
			rec.Summary = fmt.Sprintf("%s %s branch:%s",
				cr.Name, strings.TrimSpace(cr.Conclusion), cr.HeadBranch)
		}
	case "push":
		n := len(p.Commits)
		who := ""
		if p.Pusher != nil {
			who = p.Pusher.Login
		}
		rec.Summary = fmt.Sprintf("push %s %d commit(s) by %s", p.Ref, n, who)
	case "workflow_job":
		rec.Summary = actionSummary(action, *p)
	default:
		if action != "" {
			rec.Summary = action
		}
	}

	return rec
}

func isFailedWorkflowConclusion(conclusion string) bool {
	switch strings.ToLower(strings.TrimSpace(conclusion)) {
	case "failure", "timed_out", "startup_failure", "action_required":
		return true
	default:
		return false
	}
}

func (s *Server) maybeEnrichWorkflowFailureFingerprint(rec eventRecord, p *webhookPayload) {
	if p == nil || p.WorkflowRun == nil {
		return
	}
	if !strings.HasPrefix(rec.Kind, "workflow_run.") {
		return
	}
	if !isFailedWorkflowConclusion(p.WorkflowRun.Conclusion) {
		return
	}
	repo := strings.TrimSpace(rec.Repo)
	runID := p.WorkflowRun.ID
	delivery := strings.TrimSpace(rec.Delivery)
	if repo == "" || runID == 0 || delivery == "" {
		return
	}
	go s.enrichWorkflowFailureFingerprint(delivery, repo, runID)
}

func (s *Server) enrichWorkflowFailureFingerprint(delivery, repo string, runID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	analysis, err := analyzeRunFailure(ctx, repo, runID)
	if err != nil {
		logrus.Debugf("webhook fingerprint enrich run %d in %s: %v", runID, repo, err)
		return
	}
	if analysis.Fingerprint == "" {
		return
	}
	if !s.events.setFingerprintByDelivery(delivery, analysis.Fingerprint) {
		logrus.Debugf("webhook fingerprint enrich: delivery %s not found", delivery)
	}
}

func actionSummary(action string, p webhookPayload) string {
	if action != "" {
		return action
	}
	if p.Sender != nil && p.Sender.Login != "" {
		return "by " + p.Sender.Login
	}
	return "webhook received"
}
