package server

import (
	"context"
	"net/http"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sirupsen/logrus"
)

const (
	mcpEndpointPath = "/mcp"
	serverName      = "unistar-mcp"
)

// Options contains the configuration for the MCP server.
type Options struct {
	// Address is the listen address for HTTP mode (e.g. ":8080").
	// It is ignored in stdio mode.
	Address string

	// LazyLoading exposes only the tool_list/tool_describe/tool_call meta
	// tools instead of advertising every tool schema in tools/list, keeping
	// the schema payload constant as the tool count grows.
	LazyLoading bool
}

type Server struct {
	mcpServer *server.MCPServer
	opts      Options
	tools     []toolEntry
}

func New(opts Options) *Server {
	s := server.NewMCPServer(
		serverName,
		"0.0.1",
		server.WithLogging(),
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)

	srv := &Server{
		mcpServer: s,
		opts:      opts,
	}

	srv.registerTools()

	return srv
}

// preflight checks the external dependencies (gh, git, gh auth) once at startup
// and logs warnings. It does not fail startup — a missing dependency only
// matters when the relevant tool is actually called, and the per-call errors
// give the agent actionable guidance.
func preflight() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if res := run(ctx, "", "gh", "--version"); res.err != nil {
		logrus.Warn("`gh` (GitHub CLI) not found on PATH — all tools will fail until it is installed: https://cli.github.com/")
	} else if res := run(ctx, "", "gh", "auth", "status"); res.err != nil {
		logrus.Warn("GitHub CLI is not authenticated — run `gh auth login` or set GH_TOKEN for the server process")
	} else {
		logrus.Info("GitHub CLI detected and authenticated")
	}

	if res := run(ctx, "", "git", "--version"); res.err != nil {
		logrus.Warn("`git` not found on PATH — the pr_create_backport tool will fail until it is installed")
	}
}

func (s *Server) StartStdio() error {
	preflight()
	logrus.Info("Starting MCP Server over STDIO")
	return server.ServeStdio(s.mcpServer)
}

func (s *Server) StartHTTP(ctx context.Context) error {
	preflight()
	addr := s.opts.Address
	if addr == "" {
		addr = ":8080"
	}

	// Own the underlying http.Server so shutdown can force-close connections.
	// The MCP streamable transport keeps a long-lived streaming connection
	// open, which a graceful Shutdown would wait on until a timeout — making
	// Ctrl-C feel slow. Injecting our own server lets us Close() it at once.
	// When an http.Server is provided, Start does not wire its handler, so the
	// MCP endpoint is mounted here.
	// ReadHeaderTimeout bounds how long a client may take to send request
	// headers, closing the Slowloris hole gosec flags on a bare http.Server.
	httpServer := &http.Server{Addr: addr, ReadHeaderTimeout: 10 * time.Second}
	mcpHTTP := server.NewStreamableHTTPServer(
		s.mcpServer,
		server.WithStateLess(true),
		server.WithStreamableHTTPServer(httpServer),
	)
	mux := http.NewServeMux()
	mux.Handle(mcpEndpointPath, mcpHTTP)
	httpServer.Handler = mux

	errCh := make(chan error, 1)
	go func() {
		logrus.Infof("Starting MCP Server over Streamable HTTP on %s (endpoint: %s)", addr, mcpEndpointPath)
		errCh <- mcpHTTP.Start(addr)
	}()

	select {
	case <-ctx.Done():
		// The signal context has already cancelled any in-flight tool calls,
		// so there is nothing to drain. Force-close immediately, including the
		// idle streaming connection a graceful Shutdown would block on.
		logrus.Info("Shutting down MCP HTTP server")
		return httpServer.Close()
	case err := <-errCh:
		return err
	}
}

func (s *Server) registerTools() {
	s.tools = append(s.tools, s.ciTools()...)
	s.tools = append(s.tools, s.prTools()...)
	s.tools = append(s.tools, s.backportTools()...)
	s.tools = append(s.tools, s.issueTools()...)
	s.tools = append(s.tools, s.securityTools()...)

	if s.opts.LazyLoading {
		logrus.Info("Lazy loading enabled: exposing tool_list/tool_describe/tool_call meta tools")
		s.registerLazyTools()
		return
	}
	for i := range s.tools {
		s.addTool(s.tools[i].tool, s.tools[i].handler)
	}
}
