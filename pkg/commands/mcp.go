package commands

import (
	"github.com/STARRY-S/unistar-mcp/pkg/server"
	"github.com/spf13/cobra"
)

func Execute(args []string) error {
	cmd := newServerCmd()
	cmd.addCommands()
	cmd.cmd.SetArgs(args)

	_, err := cmd.cmd.ExecuteC()
	if err != nil {
		if signalContext.Err() != nil {
			return signalContext.Err()
		}
		return err
	}
	return nil
}

type serverCmd struct {
	*baseCmd
}

func newServerCmd() *serverCmd {
	cc := &serverCmd{}

	cc.baseCmd = newBaseCmd(&cobra.Command{
		Use:   "unistar-mcp",
		Short: "Start the advanced CI Workflow MCP Helper Agent (stdio mode).",
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := server.New(server.Options{})
			return srv.StartStdio()
		},
	})
	cc.cmd.Version = getVersion()
	cc.cmd.SilenceUsage = true
	cc.cmd.SilenceErrors = true

	flags := cc.cmd.PersistentFlags()
	flags.BoolVarP(&cc.baseCmd.debug, "debug", "", false, "enable debug output")

	return cc
}

func (cc *serverCmd) getCommand() *cobra.Command {
	return cc.cmd
}

func (cc *serverCmd) addCommands() {
	addCommands(
		cc.cmd,
		newHTTPCmd(),
		newVersionCmd(),
	)
}
