// nolint: revive
package signal

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"
)

var (
	onlyOneSignalHandler = make(chan struct{})
	shutdownHandler      chan os.Signal
	shutdownSignals      = []os.Signal{os.Interrupt, syscall.SIGTERM}
)

// SetupSignalContext is same as SetupSignalHandler, but a context.Context is returned.
// Only one of SetupSignalContext and SetupSignalHandler should be called, and only can
// be called once.
func SetupSignalContext() context.Context {
	close(onlyOneSignalHandler) // panics when called twice

	shutdownHandler = make(chan os.Signal, 2)

	ctx, cancel := context.WithCancel(context.Background())
	signal.Notify(shutdownHandler, shutdownSignals...)
	go func() {
		s := <-shutdownHandler
		cancel()
		// Newline to stderr (never stdout): in stdio mode stdout carries the
		// MCP protocol, and this only separates ^C from the log lines below.
		fmt.Fprintln(os.Stderr)
		logrus.Warnf("Abort: [%s] received, cleaning up resources", s.String())
		logrus.Warnf("Use 'Ctrl-C' again to force exit (not recommended)")
		<-shutdownHandler

		// second signal. Exit directly.
		logrus.Warnf("MCP Server was forced to stop.")
		os.Exit(130)
	}()

	return ctx
}
