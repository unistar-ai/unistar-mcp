package server

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sirupsen/logrus"
)

// toolEntry pairs a tool definition with its handler so the server can either
// register the tool directly (full mode) or dispatch to it through the
// lazy-loading meta tools without advertising its schema up front.
type toolEntry struct {
	tool    mcp.Tool
	handler server.ToolHandlerFunc
}

// findTool looks up a registered tool by name.
func (s *Server) findTool(name string) *toolEntry {
	for i := range s.tools {
		if s.tools[i].tool.Name == name {
			return &s.tools[i]
		}
	}
	return nil
}

// toolNames returns the names of all registered tools, in registration order.
func (s *Server) toolNames() []string {
	names := make([]string, 0, len(s.tools))
	for i := range s.tools {
		names = append(names, s.tools[i].tool.Name)
	}
	return names
}

// responseLogCap bounds the tool response text included in debug logs. It is
// larger than execLogCap because the response is usually the most useful thing
// to inspect.
const responseLogCap = 8_000

// addTool registers a tool through the MCP server, wrapping its handler so
// that in debug mode the response sent back to the client is logged.
func (s *Server) addTool(tool mcp.Tool, handler server.ToolHandlerFunc) {
	s.mcpServer.AddTool(tool, s.logResponse(tool.Name, handler))
}

// logResponse wraps a handler to log its result at debug level.
func (s *Server) logResponse(name string, h server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := h(ctx, req)
		if logrus.IsLevelEnabled(logrus.DebugLevel) {
			logrus.Debugf("tool response: %s -> %s", name, summarizeResult(res, err))
		}
		return res, err
	}
}

// summarizeResult renders a tool result for a debug log line: the text content
// (capped), flagged when it is an error result.
func summarizeResult(res *mcp.CallToolResult, err error) string {
	if err != nil {
		return "transport error: " + err.Error()
	}
	if res == nil {
		return "<nil result>"
	}
	var b strings.Builder
	if res.IsError {
		b.WriteString("[isError] ")
	}
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return clipForLog(b.String(), responseLogCap)
}
