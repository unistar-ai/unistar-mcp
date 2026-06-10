package commands

import (
	"context"
	"time"

	"github.com/STARRY-S/unistar-mcp/pkg/signal"
	"github.com/spf13/cobra"
)

var (
	signalContext context.Context = signal.SetupSignalContext()
)

type baseCmd struct {
	*baseOpts
	cmd *cobra.Command
}

func newBaseCmd(cmd *cobra.Command) *baseCmd {
	return &baseCmd{cmd: cmd, baseOpts: &globalOpts}
}

type baseOpts struct {
	debug          bool   // Enable debug output
	policyPath     string // Path to a signature verification policy file
	insecurePolicy bool   // Use an "allow everything" signature verification policy
	hideLogTime    bool   // Hide log output time (used in validation test)
}

var globalOpts = baseOpts{}

func (cc *baseCmd) getCommand() *cobra.Command {
	return cc.cmd
}

func (cc *baseCmd) ctxWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	var (
		ctx                       = signalContext
		cancel context.CancelFunc = func() {}
	)
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	return ctx, cancel
}

type cmder interface {
	getCommand() *cobra.Command
}

func addCommands(root *cobra.Command, commands ...cmder) {
	for _, command := range commands {
		cmd := command.getCommand()
		if cmd == nil {
			continue
		}
		root.AddCommand(cmd)
	}
}
