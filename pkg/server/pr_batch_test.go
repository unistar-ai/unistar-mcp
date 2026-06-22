package server

import (
	"strings"
	"testing"
)

func TestParsePRNumberList(t *testing.T) {
	nums, err := parsePRNumberList("42, 43, #99")
	if err != nil {
		t.Fatal(err)
	}
	if len(nums) != 3 || nums[0] != 42 || nums[2] != 99 {
		t.Fatalf("unexpected nums: %v", nums)
	}

	if _, err := parsePRNumberList(""); err == nil {
		t.Fatal("expected error for empty")
	}
	if _, err := parsePRNumberList("abc"); err == nil {
		t.Fatal("expected error for invalid")
	}
}

func TestFormatPRListLine(t *testing.T) {
	line := formatPRListLine(pullRequest{
		Number: 1,
		Title:  "Fix bug",
		Author: prAuthor{Login: "alice"},
		StatusCheck: []checkRollup{
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"},
		},
		ReviewDecision: "APPROVED",
	})
	for _, part := range []string{"#1", "Fix bug", "@alice", "CI:passing", "review:approved"} {
		if !strings.Contains(line, part) {
			t.Fatalf("line missing %q: %s", part, line)
		}
	}
}

func TestPROverviewResourceURI(t *testing.T) {
	m := prOverviewResourceRE.FindStringSubmatch("github://pull/acme/widget/42/overview")
	if m == nil {
		t.Fatal("expected match")
	}
	if m[1] != "acme" || m[2] != "widget" || m[3] != "42" {
		t.Fatalf("unexpected groups: %v", m)
	}
}
