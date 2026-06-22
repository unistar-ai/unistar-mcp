package server

import (
	"testing"
)

func TestClassifyFailureTimeout(t *testing.T) {
	v, rule := classifyFailure(runFailureAnalysis{
		ErrorSig: "Error: connection timeout after 30s",
		Job:      "integration",
	})
	if v != verdictTimeout || rule != "timeout" {
		t.Fatalf("got %s %s", v, rule)
	}
}

func TestClassifyFailureNamedTest(t *testing.T) {
	v, rule := classifyFailure(runFailureAnalysis{
		TestName: "TestFoo::Bar FAILED",
		ErrorSig: "assertion failed",
	})
	if v != verdictTest || rule != "named_test_failure" {
		t.Fatalf("got %s %s", v, rule)
	}
}

func TestClassifyFailureAuth(t *testing.T) {
	v, _ := classifyFailure(runFailureAnalysis{
		ErrorSig: "HTTP 401 Unauthorized: bad credentials",
	})
	if v != verdictAuth {
		t.Fatalf("got %s", v)
	}
}

func TestFormatPolicyClassification(t *testing.T) {
	text := formatPolicyClassification("acme/x", runFailureAnalysis{
		RunID:       99,
		Workflow:    "CI",
		Fingerprint: "abc",
		ErrorSig:    "timed out",
	})
	if !containsAll(text, "VERDICT: timeout", "ci_rerun_workflow", "Fingerprint: abc") {
		t.Fatalf("text %q", text)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !containsStr(s, sub) {
			return false
		}
	}
	return true
}

func containsStr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexStr(s, sub) >= 0)
}

func indexStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
