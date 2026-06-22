package server

import "testing"

func TestComputeFailureFingerprint_stableAndDistinct(t *testing.T) {
	withTest := computeFailureFingerprint("acme/widget", "CI", "integration", "TestFoo", "err sig")
	if len(withTest) != 64 {
		t.Fatalf("fingerprint length = %d, want 64 hex chars", len(withTest))
	}
	again := computeFailureFingerprint("acme/widget", "CI", "integration", "TestFoo", "other err ignored")
	if again != withTest {
		t.Fatalf("fingerprint not stable when test name set: %q vs %q", again, withTest)
	}

	emptyTest := computeFailureFingerprint("acme/widget", "CI", "", "", "connection refused")
	if emptyTest == withTest {
		t.Fatal("expected different fingerprint when test name empty vs set")
	}
	stableEmpty := computeFailureFingerprint("acme/widget", "CI", "", "", "connection refused")
	if stableEmpty != emptyTest {
		t.Fatal("empty test name should fall back to error signature")
	}
}

func TestExtractTestNameFromLogs(t *testing.T) {
	logs := "ok ok\n--- FAIL: TestWidget/Broken (0.01s)\nmore"
	if got := extractTestNameFromLogs(logs); got == "" {
		t.Fatal("expected test name")
	}
}

func TestExtractErrorSignature(t *testing.T) {
	logs := "##[error]Process completed with exit code 1.\nError: connection refused"
	sig := extractErrorSignature(logs)
	if sig == "" {
		t.Fatal("expected error signature")
	}
	if len([]rune(sig)) > 200 {
		t.Fatalf("signature too long: %d runes", len([]rune(sig)))
	}
}
