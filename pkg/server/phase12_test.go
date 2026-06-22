package server

import (
	"strings"
	"testing"
)

func TestAnalyzeConflictMarkers(t *testing.T) {
	content := `line
<<<<<<< HEAD
ours line
=======
theirs line 1
theirs line 2
>>>>>>> abc123
`
	ours, theirs, hint := analyzeConflictMarkers(content)
	if ours != 1 || theirs != 2 {
		t.Fatalf("ours=%d theirs=%d", ours, theirs)
	}
	if hint == "" {
		t.Fatal("expected hint")
	}
}

func TestCodeownersPatternMatch(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.go", "pkg/foo.go", true},
		{"*.go", "pkg/foo.rs", false},
		{"/docs/*", "docs/guide.md", true},
		{"/docs/*", "src/docs/x", false},
		{"**", "anything/here", true},
	}
	for _, tc := range cases {
		if got := codeownersPatternMatch(tc.pattern, tc.path); got != tc.want {
			t.Errorf("match(%q, %q) = %v want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestMatchCODEOWNERS(t *testing.T) {
	rules := parseCODEOWNERS(`
# comment
*.go @team-go
/docs/* @team-docs
`)
	owners := matchCODEOWNERS(rules, "pkg/server.go")
	if len(owners) != 1 || owners[0] != "@team-go" {
		t.Fatalf("owners=%v", owners)
	}
	owners = matchCODEOWNERS(rules, "docs/readme.md")
	if len(owners) != 1 || owners[0] != "@team-docs" {
		t.Fatalf("docs owners=%v", owners)
	}
}

func TestFormatAlertSeveritySummary(t *testing.T) {
	a1 := dependabotAlertRow{Number: 1}
	a1.SecurityAdvisory.Severity = "critical"
	a1.SecurityAdvisory.Summary = "RCE in lib"
	a2 := dependabotAlertRow{Number: 2}
	a2.SecurityAdvisory.Severity = "high"
	a2.SecurityAdvisory.Summary = "XSS"
	alerts := []dependabotAlertRow{a1, a2}
	out := formatAlertSeveritySummary("acme/widget", alerts)
	if !strings.Contains(out, "CRITICAL: 1") || !strings.Contains(out, "HIGH: 1") {
		t.Fatalf("summary missing counts: %q", out)
	}
}

func TestCheckDetailsURL(t *testing.T) {
	if got := checkDetailsURL(checkRollup{DetailsURL: "https://a.example"}); got != "https://a.example" {
		t.Fatalf("details: %q", got)
	}
	if got := checkDetailsURL(checkRollup{TargetURL: "https://b.example"}); got != "https://b.example" {
		t.Fatalf("target: %q", got)
	}
}
