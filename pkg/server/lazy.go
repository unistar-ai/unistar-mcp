package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerLazyTools exposes the meta tools used in lazy-loading mode. Instead
// of advertising every tool schema in tools/list, the server advertises only
// these three: the agent discovers tools by name through tool_list, fetches a
// schema on demand through tool_describe, and executes through tool_call.
// This keeps the tools/list payload constant no matter how many tools exist.
func (s *Server) registerLazyTools() {
	listTool := mcp.NewTool("tool_list",
		mcp.WithDescription(
			"List the available tools, one per line as \"name — summary\". "+
				"Use tool_describe to see a tool's parameters, and tool_call to execute it."),
		mcp.WithReadOnlyHintAnnotation(true),
	)
	s.addTool(listTool, s.handleToolList)

	describeTool := mcp.NewTool("tool_describe",
		mcp.WithDescription("Show the full description and parameter schema of a tool from tool_list."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("name", mcp.Required(), mcp.Description("The tool name, e.g. pr_list_open")),
	)
	s.addTool(describeTool, s.handleToolDescribe)

	callTool := mcp.NewTool("tool_call",
		mcp.WithDescription(
			"Execute a tool by name. Pass the tool's parameters as a JSON object in \"args\", "+
				"e.g. {\"repo\": \"owner/repo\", \"pr_number\": 1}. If a required parameter is "+
				"missing, the error message includes the tool's schema."),
		mcp.WithString("name", mcp.Required(), mcp.Description("The tool name to execute, from tool_list")),
		mcp.WithObject("args", mcp.Description("The tool parameters as a JSON object (not a string)")),
	)
	s.addTool(callTool, s.handleToolCall)
}

func (s *Server) handleToolList(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "%d tool(s) available:\n", len(s.tools))
	for i := range s.tools {
		t := &s.tools[i].tool
		fmt.Fprintf(&b, "%s — %s\n", t.Name, brief(t.Description))
	}
	return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
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
