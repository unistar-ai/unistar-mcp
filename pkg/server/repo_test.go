package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHandleRepoGetInfo_requiresRepo(t *testing.T) {
	s := New(Options{})

	res, err := s.handleRepoGetInfo(context.Background(), callReq(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error result for missing repo")
	}
	if !strings.Contains(resultText(t, res), "repo") {
		t.Fatalf("expected repo requirement in error: %s", resultText(t, res))
	}
}

func TestFormatRepoInfo_compact(t *testing.T) {
	var info repoInfo
	if err := json.Unmarshal([]byte(`{
		"name":"widget",
		"description":"Example repository",
		"isPrivate":false,
		"url":"https://github.com/acme/widget",
		"owner":{"login":"acme"},
		"defaultBranchRef":{"name":"main"},
		"primaryLanguage":{"name":"Go"},
		"licenseInfo":{"name":"MIT"},
		"repositoryTopics":[{"name":"api"},{"name":"ci"}]
	}`), &info); err != nil {
		t.Fatal(err)
	}

	out := formatRepoInfo("acme/widget", &info, []string{"bug", "enhancement"}, nil)
	for _, want := range []string{
		"acme/widget",
		"Default branch: main",
		"Language: Go",
		"License: MIT",
		"Topics: api, ci",
		"Labels (2): bug, enhancement",
		"Example repository",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRepoTopicNames_skipsEmpty(t *testing.T) {
	var info repoInfo
	if err := json.Unmarshal([]byte(`{
		"repositoryTopics":[{"name":"api"},{"name":""},{"name":"ci"}]
	}`), &info); err != nil {
		t.Fatal(err)
	}
	got := repoTopicNames(&info)
	if len(got) != 2 || got[0] != "api" || got[1] != "ci" {
		t.Fatalf("topics=%v", got)
	}
}
