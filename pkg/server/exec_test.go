package server

import (
	"errors"
	"strings"
	"testing"
)

func TestTransientDetection(t *testing.T) {
	cases := []struct {
		stderr string
		want   bool
	}{
		{"failed to get run log: HTTP 502 Bad Gateway", true},
		{"HTTP 503: Service Unavailable", true},
		{"net/http: TLS handshake timeout", true},
		{"read: connection reset by peer", true},
		{"HTTP 404: Not Found", false},
		{"HTTP 401: Bad credentials", false},
	}
	for _, c := range cases {
		r := runResult{stderr: c.stderr, err: errors.New("exit status 1")}
		if got := r.transient(); got != c.want {
			t.Errorf("transient(%q) = %v, want %v", c.stderr, got, c.want)
		}
	}

	// A successful run is never transient, whatever the output says.
	ok := runResult{stdout: "HTTP 502 mentioned in a log line"}
	if ok.transient() {
		t.Error("successful run misclassified as transient")
	}
}

func TestRateLimitNotRetriedAndClassified(t *testing.T) {
	// Secondary rate limits arrive as HTTP 403 — they must classify as
	// rate-limit (wait guidance), not permission-denied, and must not count
	// as transient (runRetry would burn quota).
	r := runResult{
		stderr:   "HTTP 403: You have exceeded a secondary rate limit. Please wait a few minutes before you try again.",
		exitCode: 1,
		err:      errors.New("exit status 1"),
	}
	if r.transient() {
		t.Error("rate limit misclassified as transient")
	}
	msg := r.wrap("failed to list pull requests").Error()
	if !strings.Contains(msg, "ERROR: RATE_LIMIT") || !strings.Contains(msg, "wait") {
		t.Errorf("rate limit should produce ERROR: RATE_LIMIT with wait guidance:\n%s", msg)
	}
	if strings.Contains(msg, "ERROR: FORBIDDEN") {
		t.Errorf("rate limit misclassified as FORBIDDEN:\n%s", msg)
	}
}

func TestWrapClassifiesServerErrors(t *testing.T) {
	r := runResult{
		stderr:   "failed to get run log: HTTP 502 Bad Gateway",
		exitCode: 1,
		err:      errors.New("exit status 1"),
	}
	msg := r.wrap("failed to fetch failed logs").Error()
	if !strings.Contains(msg, "ERROR: TRANSIENT") || !strings.Contains(msg, "Retry") {
		t.Errorf("5xx error should produce ERROR: TRANSIENT with retry hint:\n%s", msg)
	}
}

func TestWrapCapsDetailSize(t *testing.T) {
	r := runResult{
		stdout:   strings.Repeat("x", 100_000),
		exitCode: 1,
		err:      errors.New("exit status 1"),
	}
	msg := r.wrap("failed").Error()
	if len(msg) > errDetailBudget+200 {
		t.Errorf("wrapped error not capped: %d bytes", len(msg))
	}
}
