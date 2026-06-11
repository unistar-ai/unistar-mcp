package server

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func callReq(args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	return req
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("result has no content")
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", res.Content[0])
	}
	return text.Text
}

func TestToolListContainsAllTools(t *testing.T) {
	s := New(Options{LazyLoading: true})

	res, err := s.handleToolList(context.Background(), callReq(nil))
	if err != nil {
		t.Fatal(err)
	}
	out := resultText(t, res)

	for _, name := range []string{
		"pr_list_open", "pr_get_status",
		"ci_analyze_pr_failures", "ci_get_failed_logs", "ci_rerun_workflow",
		"pr_create_backport",
	} {
		if !strings.Contains(out, name) {
			t.Errorf("tool_list output missing %q:\n%s", name, out)
		}
	}
}

func TestToolDescribeReturnsSchema(t *testing.T) {
	s := New(Options{LazyLoading: true})

	res, err := s.handleToolDescribe(context.Background(),
		callReq(map[string]any{"name": "pr_create_backport"}))
	if err != nil {
		t.Fatal(err)
	}
	out := resultText(t, res)

	for _, want := range []string{"target_branch", "repo_dir", "required"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe output missing %q:\n%s", want, out)
		}
	}
}

func TestToolDescribeUnknownToolListsAvailable(t *testing.T) {
	s := New(Options{LazyLoading: true})

	res, err := s.handleToolDescribe(context.Background(),
		callReq(map[string]any{"name": "no_such_tool"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected an error result")
	}
	out := resultText(t, res)
	if !strings.Contains(out, "pr_list_open") {
		t.Errorf("unknown-tool error should list available tools:\n%s", out)
	}
}

func TestToolCallMissingRequiredReturnsSchema(t *testing.T) {
	s := New(Options{LazyLoading: true})

	res, err := s.handleToolCall(context.Background(),
		callReq(map[string]any{"name": "pr_list_open", "args": map[string]any{}}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected an error result")
	}
	out := resultText(t, res)
	if !strings.Contains(out, "repo") || !strings.Contains(out, "properties") {
		t.Errorf("missing-param error should name the parameter and include the schema:\n%s", out)
	}
}

func TestToolCallRejectsStringArgs(t *testing.T) {
	s := New(Options{LazyLoading: true})

	res, err := s.handleToolCall(context.Background(),
		callReq(map[string]any{"name": "pr_list_open", "args": `{"repo": "a/b"}`}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected an error result")
	}
	out := resultText(t, res)
	if !strings.Contains(out, "JSON object") {
		t.Errorf("string args should be rejected with guidance:\n%s", out)
	}
}
