package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerLazyTools exposes meta tools for progressive tool discovery.
func (s *Server) registerLazyTools() {
	listTool := mcp.NewTool("tool_list",
		mcp.WithDescription(
			"List all tools (one line each). Prefer tool_search or tool_list_category when you know the domain."),
		mcp.WithReadOnlyHintAnnotation(true),
	)
	s.addTool(listTool, s.handleToolList)

	categoryTool := mcp.NewTool("tool_list_category",
		mcp.WithDescription("List tools in one category (CI, PR, Repo, Issue, Security, Release, Policy, Backport, Notify, Event)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("category", mcp.Required(), mcp.Description("Category name, e.g. CI or PR")),
	)
	s.addTool(categoryTool, s.handleToolListCategory)

	searchTool := mcp.NewTool("tool_search",
		mcp.WithDescription("Search tools by keyword (matches name and description). Returns top matches."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search terms, e.g. \"pr ci snapshot\"")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 5, max 15)")),
	)
	s.addTool(searchTool, s.handleToolSearch)

	describeTool := mcp.NewTool("tool_describe",
		mcp.WithDescription("Optional: full parameter schema for one tool. tool_call returns schema on missing args."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("name", mcp.Required(), mcp.Description("The tool name, e.g. pr_list_open")),
	)
	s.addTool(describeTool, s.handleToolDescribe)

	callTool := mcp.NewTool("tool_call",
		mcp.WithDescription(
			"Execute a tool by name. Pass parameters in \"args\" JSON object. Missing required args return the schema."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Tool name from tool_search or skill chain")),
		mcp.WithObject("args", mcp.Description("Tool parameters as a JSON object (not a string)")),
	)
	s.addTool(callTool, s.handleToolCall)
}

func (s *Server) handleToolList(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "%d tool(s) available:\n", len(s.tools))
	for i := range s.tools {
		t := &s.tools[i].tool
		fmt.Fprintf(&b, "[%s] %s — %s\n", toolCategory(t.Name), t.Name, brief(t.Description))
	}
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handleToolListCategory(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	category, err := request.RequireString("category")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	want := normalizeCategory(category)
	if want == "" {
		return mcp.NewToolResultError(fmt.Sprintf(
			"unknown category %q — use CI, PR, Repo, Issue, Security, Release, Policy, Backport, Notify, or Event",
			category)), nil
	}
	var b strings.Builder
	n := 0
	for i := range s.tools {
		t := &s.tools[i].tool
		if toolCategory(t.Name) != want {
			continue
		}
		n++
		fmt.Fprintf(&b, "[%s] %s — %s\n", want, t.Name, brief(t.Description))
	}
	if n == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("no tools in category %q", want)), nil
	}
	header := fmt.Sprintf("%d tool(s) in [%s]:\n", n, want)
	return mcp.NewToolResultText(strings.TrimSpace(header + b.String())), nil
}

func (s *Server) handleToolSearch(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := 5
	if raw, ok := request.GetArguments()["limit"].(float64); ok && raw >= 1 {
		limit = int(raw)
	}
	if limit > 15 {
		limit = 15
	}
	tokens := searchTokens(query)
	if len(tokens) == 0 {
		return mcp.NewToolResultError("query is empty — pass keywords like \"pr ci\" or \"merge blockers\""), nil
	}
	type scored struct {
		score int
		line  string
	}
	var matches []scored
	for i := range s.tools {
		t := &s.tools[i].tool
		score := scoreToolSearch(t.Name, t.Description, tokens)
		if score <= 0 {
			continue
		}
		matches = append(matches, scored{
			score: score,
			line:  fmt.Sprintf("[%s] %s — %s", toolCategory(t.Name), t.Name, brief(t.Description)),
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	if len(matches) == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("no tools matched %q — try tool_list_category or tool_list", query)), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d match(es) for %q:\n", len(matches), query)
	for _, m := range matches {
		b.WriteString(m.line)
		b.WriteByte('\n')
	}
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func normalizeCategory(raw string) string {
	c := strings.TrimSpace(strings.ToUpper(raw))
	switch c {
	case "CI", "PR":
		return c
	case "REPO":
		return "Repo"
	case "ISSUE":
		return "Issue"
	case "SECURITY":
		return "Security"
	case "RELEASE":
		return "Release"
	case "POLICY":
		return "Policy"
	case "BACKPORT":
		return "Backport"
	case "NOTIFY":
		return "Notify"
	case "EVENT":
		return "Event"
	case "TOOL":
		return "Tool"
	default:
		return ""
	}
}

func searchTokens(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	var out []string
	for _, part := range strings.FieldsFunc(query, func(r rune) bool {
		return r == ' ' || r == '_' || r == '-' || r == ','
	}) {
		if len(part) >= 2 {
			out = append(out, part)
		}
	}
	return out
}

func scoreToolSearch(name, desc string, tokens []string) int {
	lowName := strings.ToLower(name)
	lowDesc := strings.ToLower(desc)
	score := 0
	for _, tok := range tokens {
		if strings.Contains(lowName, tok) {
			score += 10 + len(tok)
		}
		if strings.Contains(lowDesc, tok) {
			score += 4 + len(tok)/2
		}
	}
	return score
}

func (s *Server) handleToolDescribe(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	entry := s.findTool(name)
	if entry == nil {
		return mcp.NewToolResultError(unknownToolMsg(name, s.toolNames())), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n", entry.tool.Name, entry.tool.Description)
	if ro := entry.tool.Annotations.ReadOnlyHint; ro != nil && *ro {
		b.WriteString("This tool is read-only.\n")
	}
	fmt.Fprintf(&b, "\nParameters (JSON Schema):\n%s", schemaJSON(&entry.tool))

	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
}

func (s *Server) handleToolCall(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	entry := s.findTool(name)
	if entry == nil {
		return mcp.NewToolResultError(unknownToolMsg(name, s.toolNames())), nil
	}

	// args must be a JSON object, not a stringified one — small models get the
	// encoding wrong otherwise, so reject strings with a pointed message.
	rawArgs := request.GetArguments()["args"]
	args, ok := rawArgs.(map[string]any)
	if rawArgs != nil && !ok {
		return mcp.NewToolResultError(fmt.Sprintf(
			"\"args\" must be a JSON object, not %T. Example: {\"name\": %q, \"args\": {\"repo\": \"owner/repo\"}}",
			rawArgs, name)), nil
	}
	if args == nil {
		args = map[string]any{}
	}

	// Check required parameters before dispatching so a wrong call comes back
	// with the schema in one round trip instead of a bare missing-param error.
	if missing := missingRequired(&entry.tool, args); len(missing) > 0 {
		return mcp.NewToolResultError(fmt.Sprintf(
			"tool %q is missing required parameter(s): %s.\n\nParameters (JSON Schema):\n%s",
			name, strings.Join(missing, ", "), schemaJSON(&entry.tool))), nil
	}

	var req mcp.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	return entry.handler(ctx, req)
}

// brief returns the first sentence of a tool description for compact listing.
func brief(desc string) string {
	if i := strings.Index(desc, ". "); i >= 0 {
		return desc[:i+1]
	}
	return desc
}

func toolCategory(name string) string {
	switch {
	case strings.HasPrefix(name, "ci_"):
		return "CI"
	case strings.HasPrefix(name, "pr_"):
		return "PR"
	case strings.HasPrefix(name, "repo_"):
		return "Repo"
	case strings.HasPrefix(name, "issue_"):
		return "Issue"
	case strings.HasPrefix(name, "alert_"):
		return "Security"
	case strings.HasPrefix(name, "notify_"):
		return "Notify"
	case strings.HasPrefix(name, "event_"):
		return "Event"
	case strings.HasPrefix(name, "policy_"):
		return "Policy"
	case strings.HasPrefix(name, "backport_"):
		return "Backport"
	case strings.HasPrefix(name, "release_"):
		return "Release"
	default:
		return "Tool"
	}
}

// unknownToolMsg builds an error message that includes the valid tool names so
// the agent can self-correct without an extra tool_list call.
func unknownToolMsg(name string, names []string) string {
	return fmt.Sprintf("unknown tool %q. Available tools: %s", name, strings.Join(names, ", "))
}

// missingRequired returns the required parameters of a tool that are absent
// from the given arguments.
func missingRequired(tool *mcp.Tool, args map[string]any) []string {
	var missing []string
	for _, key := range tool.InputSchema.Required {
		if _, ok := args[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}

// schemaJSON renders a tool's input schema as indented JSON.
func schemaJSON(tool *mcp.Tool) string {
	data, err := json.MarshalIndent(tool.InputSchema, "", "  ")
	if err != nil {
		return fmt.Sprintf("(failed to render schema: %s)", err)
	}
	return string(data)
}
