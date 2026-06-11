package server

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
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
