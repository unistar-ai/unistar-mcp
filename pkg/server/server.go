package server

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sirupsen/logrus"
)

const shutdownTimeout = 10 * time.Second

// Options contains the configuration for the MCP server.
type Options struct {
	// Address is the listen address for HTTP mode (e.g. ":8080").
	// It is ignored in stdio mode.
	Address string
}

type Server struct {
	mcpServer *server.MCPServer
	opts      Options
}

func New(opts Options) *Server {
	s := server.NewMCPServer(
		"unistar-mcp",
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

	httpServer := server.NewStreamableHTTPServer(
		s.mcpServer,
		server.WithStateLess(true),
	)

	errCh := make(chan error, 1)
	go func() {
		logrus.Infof("Starting MCP Server over Streamable HTTP on %s (endpoint: /mcp)", addr)
		errCh <- httpServer.Start(addr)
	}()

	select {
	case <-ctx.Done():
		logrus.Info("Shutting down MCP HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) registerTools() {
	s.registerCITools()
	s.registerBackportTools()
}
