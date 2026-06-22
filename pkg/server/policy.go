package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

type failureVerdict string

const (
	verdictTest      failureVerdict = "test"
	verdictInfra     failureVerdict = "infra"
	verdictAuth      failureVerdict = "auth"
	verdictTimeout   failureVerdict = "timeout"
	verdictExternal  failureVerdict = "external_ci"
	verdictUnknown   failureVerdict = "unknown"
)

type policyRule struct {
	id         string
	verdict    failureVerdict
	substrings []string
}

var defaultPolicyRules = []policyRule{
	{id: "external_ci_hint", verdict: verdictExternal, substrings: []string{
		"external ci", "status context", "jenkins", "codecov", "third-party check",
	}},
	{id: "timeout", verdict: verdictTimeout, substrings: []string{
		"timeout", "timed out", "deadline exceeded", "context deadline", "i/o timeout",
	}},
	{id: "auth", verdict: verdictAuth, substrings: []string{
		"401", "403", "unauthorized", "authentication failed", "permission denied",
		"bad credentials", "invalid token", "access denied",
	}},
	{id: "infra", verdict: verdictInfra, substrings: []string{
		"connection refused", "connection reset", "no space left", "out of memory",
		"oom", "docker", "registry unreachable", "503 service unavailable",
		"502 bad gateway", "504 gateway", "network is unreachable", "cannot connect",
		"runner lost communication", "pod evicted",
	}},
}

func (s *Server) policyTools() []toolEntry {
	tool := mcp.NewTool("policy_classify_failure",
		mcp.WithDescription(
			"Rule-based failure classification (test / infra / auth / timeout / external_ci). "+
				"Call after ci_failure_fingerprint to decide rerun vs investigate. "+
				"Next: ci_rerun_workflow for timeout/infra flakes; ci_get_failed_logs for test failures."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository in owner/repo form")),
		mcp.WithNumber("run_id", mcp.Required(), mcp.Description("Workflow run ID")),
	)

	return []toolEntry{
		{tool: tool, handler: s.handlePolicyClassifyFailure},
	}
}

func (s *Server) handlePolicyClassifyFailure(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(formatToolError(ErrValidation, err.Error(),
			"pass repo and run_id")), nil
	}
	runIDFloat, err := request.RequireFloat("run_id")
	if err != nil {
		return mcp.NewToolResultError(formatToolError(ErrValidation, err.Error(),
			"pass run_id from ci_analyze_pr_failures or ci_failure_fingerprint")), nil
	}
	runID := int64(runIDFloat)

	analysis, err := analyzeRunFailure(ctx, repo, runID)
	if err != nil {
		return mcp.NewToolResultError(formatToolError(ErrNotFound, err.Error(),
			"confirm run_id with ci_get_run_summary")), nil
	}

	text := formatPolicyClassification(repo, analysis)
	return mcp.NewToolResultText(text), nil
}

func classifyFailure(analysis runFailureAnalysis) (failureVerdict, string) {
	corpus := strings.ToLower(strings.Join([]string{
		analysis.ErrorSig, analysis.TestName, analysis.Job, analysis.Step, analysis.Workflow,
	}, " "))

	for _, rule := range defaultPolicyRules {
		for _, sub := range rule.substrings {
			if strings.Contains(corpus, sub) {
				return rule.verdict, rule.id
			}
		}
	}

	if strings.TrimSpace(analysis.TestName) != "" {
		return verdictTest, "named_test_failure"
	}

	low := strings.ToLower(analysis.ErrorSig)
	if strings.Contains(low, "assert") || strings.Contains(low, "expect") ||
		strings.Contains(low, "panic:") || strings.Contains(low, "failed:") {
		return verdictTest, "test_assertion"
	}

	return verdictUnknown, "no_rule_match"
}

func formatPolicyClassification(repo string, analysis runFailureAnalysis) string {
	verdict, ruleID := classifyFailure(analysis)
	var b strings.Builder
	fmt.Fprintf(&b, "VERDICT: %s\n", verdict)
	fmt.Fprintf(&b, "Matched rule: %s\n", ruleID)
	fmt.Fprintf(&b, "Run %d  %s\n", analysis.RunID, analysis.Workflow)
	if analysis.Job != "" {
		fmt.Fprintf(&b, "Job: %s\n", analysis.Job)
	}
	if analysis.TestName != "" {
		fmt.Fprintf(&b, "Test: %s\n", analysis.TestName)
	}
	if analysis.ErrorSig != "" {
		fmt.Fprintf(&b, "Error signature: %s\n", analysis.ErrorSig)
	}
	fmt.Fprintf(&b, "Fingerprint: %s\n", analysis.Fingerprint)

	switch verdict {
	case verdictTimeout, verdictInfra:
		b.WriteString("Next: ci_rerun_workflow if this looks transient; ci_compare_runs after rerun.")
	case verdictAuth:
		b.WriteString("Next: fix credentials/secrets — do not rerun until auth is resolved.")
	case verdictExternal:
		b.WriteString("Next: ci_list_external_checks — do not call ci_get_failed_logs for external CI.")
	case verdictTest:
		b.WriteString("Next: ci_get_failed_logs for details; avoid blind rerun.")
	default:
		b.WriteString("Next: ci_get_failed_logs then re-run policy_classify_failure.")
	}
	_ = repo
	return strings.TrimSpace(b.String())
}
